package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"community_service/internal/events"
	"community_service/internal/models"
	"community_service/internal/obs"
)

// SanctionApplier consumes EVT-004 `member_sanction_changed` from
// moderation_events, applies the sanction to member state idempotently and emits
// the matching EVT-002 carrying the originating sanction id.
type SanctionApplier struct {
	deps Deps
}

// NewSanctionApplier builds the EVT-004 consumer handler.
func NewSanctionApplier(deps Deps) *SanctionApplier {
	return &SanctionApplier{deps: deps.withDefaults()}
}

// HandleEvent applies one consumed moderation event.
func (s *SanctionApplier) HandleEvent(ctx context.Context, raw events.RawMessage) error {
	if err := raw.Envelope.Validate(); err != nil {
		return fmt.Errorf("invalid event envelope: %w", err)
	}
	if raw.Envelope.EventType != events.EventTypeMemberSanctionChanged {
		return fmt.Errorf("unexpected event type %q", raw.Envelope.EventType)
	}

	var payload events.MemberSanctionChanged
	if err := json.Unmarshal(raw.Payload, &payload); err != nil {
		return fmt.Errorf("decode member_sanction_changed payload: %w", err)
	}
	if payload.CommunityID == uuid.Nil || payload.TargetUserID == uuid.Nil || payload.SanctionID == uuid.Nil {
		return fmt.Errorf("member_sanction_changed payload is missing identifiers")
	}

	eventID, err := uuid.Parse(raw.Envelope.EventID)
	if err != nil {
		return fmt.Errorf("invalid event id: %w", err)
	}

	now := s.deps.Clock.Now()

	membership, err := s.membershipOrNil(ctx, payload.CommunityID, payload.TargetUserID)
	if err != nil {
		return err
	}

	ban, err := s.deps.Memberships.GetActiveBan(ctx, payload.CommunityID, payload.TargetUserID)
	if err != nil {
		return err
	}

	application, applicable := BuildSanctionApplication(raw.Envelope, eventID, payload, membership, ban, now)
	if !applicable {
		if err := s.deps.Sanctions.MarkEventProcessed(ctx, eventID, raw.Envelope.EventType, now); err != nil {
			if errors.Is(err, models.ErrDuplicateEvent) {
				s.logSkipped(raw, "duplicate_event")
				return nil
			}
			return err
		}
		s.logSkipped(raw, "no_state_change")
		return nil
	}

	applied, err := s.deps.Sanctions.ApplySanctionApplication(ctx, application)
	switch {
	case errors.Is(err, models.ErrDuplicateEvent):
		s.logSkipped(raw, "duplicate_event")
		return nil
	case errors.Is(err, models.ErrStaleVersion):
		s.logSkipped(raw, "stale_member_version")
		return nil
	case err != nil:
		return err
	}

	s.recordSanctionMetrics(application)

	if applied == nil || application.Change == "" {
		// A ban of a user who was never a member records only the ban: there is
		// no membership snapshot to project and no member_version to guard with.
		s.logSkipped(raw, "no_membership_snapshot")
		return nil
	}

	// A sanction-driven EVT-002 continues the causing flow: trace_id and
	// correlation_id are copied and causation_id is the EVT-004 event id.
	envelope := memberEnvelope(raw.Envelope.TraceID, raw.Envelope.CorrelationID, &raw.Envelope.EventID, now)
	s.deps.publishMemberChanged(ctx, envelope, application.Change, applied, &payload.SanctionID)

	return nil
}

func (s *SanctionApplier) membershipOrNil(ctx context.Context, communityID, userID uuid.UUID) (*models.Membership, error) {
	membership, err := s.deps.Memberships.GetMembership(ctx, communityID, userID)
	if err != nil {
		if errors.Is(err, models.ErrMembershipNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return membership, nil
}

func (s *SanctionApplier) recordSanctionMetrics(application *models.SanctionApplication) {
	switch application.Change {
	case models.ChangeRemoved:
		s.deps.Metrics.RecordLeave(obs.LeaveReasonKicked)
	case models.ChangeBanned:
		s.deps.Metrics.RecordLeave(obs.LeaveReasonBanned)
	}
}

func (s *SanctionApplier) logSkipped(raw events.RawMessage, reason string) {
	s.deps.Logger.Info("sanction event skipped",
		zap.String("event_id", raw.Envelope.EventID),
		zap.String("trace_id", raw.Envelope.TraceID),
		zap.String("reason", reason))
}

// BuildSanctionApplication maps one EVT-004 onto the member-state transition it
// requires (docs/architecture.md §3.3). The boolean is false when the event
// changes no state at all: a kick of someone who never joined, a mute that is
// already expired, an unmute of a member who is not muted, an unban of a user
// who is not banned, or a ban that is already recorded for a removed member.
// Those events are still deduplicated so a redelivery stays a no-op.
func BuildSanctionApplication(envelope events.Envelope, eventID uuid.UUID, payload events.MemberSanctionChanged, membership *models.Membership, ban *models.Ban, now time.Time) (*models.SanctionApplication, bool) {
	application := &models.SanctionApplication{
		EventID:     eventID,
		EventType:   envelope.EventType,
		CommunityID: payload.CommunityID,
		UserID:      payload.TargetUserID,
		SanctionID:  &payload.SanctionID,
		OccurredAt:  now,
	}

	switch payload.Action {
	case models.ActionKick:
		if membership == nil || !membership.IsActive() {
			return nil, false
		}
		application.Change = models.ChangeRemoved
		application.Status = models.StatusRemoved
		application.ExpectedMemberVersion = membership.MemberVersion
		return application, true

	case models.ActionBan:
		application.BanChange = models.BanChangeRecord
		application.BanReason = payload.Reason

		if membership == nil {
			// Ban of a never-joined user: record the ban, invent no membership.
			return application, true
		}
		if !membership.IsActive() && membership.IsBanned {
			return nil, false
		}
		application.Change = models.ChangeBanned
		application.Status = models.StatusRemoved
		application.ExpectedMemberVersion = membership.MemberVersion
		return application, true

	case models.ActionUnban:
		if ban == nil {
			return nil, false
		}
		application.BanChange = models.BanChangeClear
		if membership == nil {
			return application, true
		}
		application.Change = models.ChangeUnbanned
		application.Status = membership.Status
		application.ExpectedMemberVersion = membership.MemberVersion
		return application, true

	case models.ActionMute:
		if membership == nil || !membership.IsActive() {
			return nil, false
		}
		if payload.ExpiresAt == nil || !now.Before(*payload.ExpiresAt) {
			// A mute without an end time, or one that already elapsed, cannot be
			// expressed as a future muted_until and would restrict nothing.
			return nil, false
		}
		if membership.MutedUntil != nil && membership.MutedUntil.Equal(*payload.ExpiresAt) {
			return nil, false
		}
		application.Change = models.ChangeMuted
		application.Status = membership.Status
		application.MutedUntilWritten = true
		application.MutedUntil = payload.ExpiresAt
		application.ExpectedMemberVersion = membership.MemberVersion
		return application, true

	case models.ActionUnmute:
		if membership == nil || membership.MutedUntil == nil {
			return nil, false
		}
		application.Change = models.ChangeUnmuted
		application.Status = membership.Status
		application.MutedUntilWritten = true
		application.MutedUntil = nil
		application.ExpectedMemberVersion = membership.MemberVersion
		return application, true

	default:
		return nil, false
	}
}
