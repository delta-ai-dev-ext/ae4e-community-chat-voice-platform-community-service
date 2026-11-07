package unit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"community_service/internal/events"
	"community_service/internal/models"
	"community_service/internal/service"
	"community_service/tests/mocks"
)

// newSanctionApplier builds the EVT-004 handler over the provided doubles.
func newSanctionApplier(repository *mocks.Repository, publisher *mocks.Publisher) *service.SanctionApplier {
	return service.NewSanctionApplier(service.Deps{
		Communities: repository,
		Channels:    repository,
		Invites:     repository,
		Memberships: repository,
		Roles:       repository,
		Sanctions:   repository,
		Publisher:   publisher,
		Clock:       mocks.NewClock(frozenNow),
	})
}

// sanctionEvent builds the EVT-004 message moderation_service would publish.
func sanctionEvent(t *testing.T, action string, communityID, targetUserID, sanctionID uuid.UUID, expiresAt *time.Time) events.RawMessage {
	t.Helper()

	envelope := events.NewEnvelope(events.EventTypeMemberSanctionChanged, events.EventVersionV1,
		strings.Repeat("c", 32), uuid.NewString(), nil, frozenNow.Add(-time.Second))
	envelope.Producer = "moderation_service"

	payload, err := json.Marshal(events.MemberSanctionChanged{
		SanctionID:   sanctionID,
		CommunityID:  communityID,
		TargetUserID: targetUserID,
		Action:       action,
		Source:       "moderator",
		ExpiresAt:    expiresAt,
	})
	require.NoError(t, err)

	return events.RawMessage{Envelope: envelope, Payload: payload}
}

func TestBuildSanctionApplicationMapping(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	targetID := uuid.New()
	sanctionID := uuid.New()
	future := frozenNow.Add(time.Hour)
	past := frozenNow.Add(-time.Hour)
	otherFuture := frozenNow.Add(2 * time.Hour)

	active := activeMembership(communityID, targetID)
	active.MemberVersion = 4

	removedAndBanned := activeMembership(communityID, targetID)
	removedAndBanned.Status = models.StatusRemoved
	removedAndBanned.IsBanned = true
	removedAndBanned.MemberVersion = 7

	muted := activeMembership(communityID, targetID)
	muted.MutedUntil = &future

	envelope := events.NewEnvelope(events.EventTypeMemberSanctionChanged, events.EventVersionV1, strings.Repeat("d", 32), uuid.NewString(), nil, frozenNow)

	testCases := []struct {
		name        string
		action      string
		membership  *models.Membership
		ban         *models.Ban
		expiresAt   *time.Time
		applicable  bool
		wantChange  string
		wantStatus  string
		wantBan     string
		wantMuted   bool
		wantVersion int64
	}{
		{
			name:        "kick removes an active membership",
			action:      models.ActionKick,
			membership:  active,
			applicable:  true,
			wantChange:  models.ChangeRemoved,
			wantStatus:  models.StatusRemoved,
			wantVersion: 4,
		},
		{
			name:       "kick of a non member changes nothing",
			action:     models.ActionKick,
			applicable: false,
		},
		{
			name:       "kick of an already removed membership changes nothing",
			action:     models.ActionKick,
			membership: removedAndBanned,
			applicable: false,
		},
		{
			name:        "ban removes the membership and records the ban",
			action:      models.ActionBan,
			membership:  active,
			applicable:  true,
			wantChange:  models.ChangeBanned,
			wantStatus:  models.StatusRemoved,
			wantBan:     models.BanChangeRecord,
			wantVersion: 4,
		},
		{
			name:       "ban of a never joined user records only the ban",
			action:     models.ActionBan,
			applicable: true,
			wantBan:    models.BanChangeRecord,
		},
		{
			name:       "ban of an already banned removed member is a no-op",
			action:     models.ActionBan,
			membership: removedAndBanned,
			applicable: false,
		},
		{
			name:        "unban clears the ban and reports the snapshot",
			action:      models.ActionUnban,
			membership:  removedAndBanned,
			ban:         &models.Ban{ID: uuid.New()},
			applicable:  true,
			wantChange:  models.ChangeUnbanned,
			wantStatus:  models.StatusRemoved,
			wantBan:     models.BanChangeClear,
			wantVersion: 7,
		},
		{
			name:       "unban of a non member clears only the ban",
			action:     models.ActionUnban,
			ban:        &models.Ban{ID: uuid.New()},
			applicable: true,
			wantBan:    models.BanChangeClear,
		},
		{
			name:       "unban without an active ban changes nothing",
			action:     models.ActionUnban,
			membership: active,
			applicable: false,
		},
		{
			name:        "mute sets muted_until",
			action:      models.ActionMute,
			membership:  active,
			expiresAt:   &future,
			applicable:  true,
			wantChange:  models.ChangeMuted,
			wantStatus:  models.StatusActive,
			wantMuted:   true,
			wantVersion: 4,
		},
		{
			name:       "mute that already elapsed changes nothing",
			action:     models.ActionMute,
			membership: active,
			expiresAt:  &past,
			applicable: false,
		},
		{
			name:       "mute without an end time changes nothing",
			action:     models.ActionMute,
			membership: active,
			applicable: false,
		},
		{
			name:       "mute of a non member changes nothing",
			action:     models.ActionMute,
			expiresAt:  &future,
			applicable: false,
		},
		{
			name:       "mute already at that instant changes nothing",
			action:     models.ActionMute,
			membership: muted,
			expiresAt:  &future,
			applicable: false,
		},
		{
			name:        "mute to a different instant applies",
			action:      models.ActionMute,
			membership:  muted,
			expiresAt:   &otherFuture,
			applicable:  true,
			wantChange:  models.ChangeMuted,
			wantStatus:  models.StatusActive,
			wantMuted:   true,
			wantVersion: 1,
		},
		{
			name:        "unmute clears muted_until",
			action:      models.ActionUnmute,
			membership:  muted,
			applicable:  true,
			wantChange:  models.ChangeUnmuted,
			wantStatus:  models.StatusActive,
			wantMuted:   true,
			wantVersion: 1,
		},
		{
			name:       "unmute of a member who is not muted changes nothing",
			action:     models.ActionUnmute,
			membership: active,
			applicable: false,
		},
		{
			name:       "unknown action changes nothing",
			action:     "teleport",
			membership: active,
			applicable: false,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			application, applicable := service.BuildSanctionApplication(envelope, uuid.MustParse(envelope.EventID), events.MemberSanctionChanged{
				SanctionID:   sanctionID,
				CommunityID:  communityID,
				TargetUserID: targetID,
				Action:       tc.action,
				ExpiresAt:    tc.expiresAt,
			}, tc.membership, tc.ban, frozenNow)

			require.Equal(t, tc.applicable, applicable)
			if !tc.applicable {
				assert.Nil(t, application)
				return
			}

			assert.Equal(t, tc.wantChange, application.Change)
			assert.Equal(t, tc.wantStatus, application.Status)
			assert.Equal(t, tc.wantBan, application.BanChange)
			assert.Equal(t, tc.wantMuted, application.MutedUntilWritten)
			assert.Equal(t, tc.wantVersion, application.ExpectedMemberVersion)
			require.NotNil(t, application.SanctionID)
			assert.Equal(t, sanctionID, *application.SanctionID)
			if tc.expiresAt != nil && tc.action == models.ActionMute {
				require.NotNil(t, application.MutedUntil)
				assert.Equal(t, *tc.expiresAt, *application.MutedUntil)
			}
			if tc.action == models.ActionUnmute {
				assert.Nil(t, application.MutedUntil)
			}
		})
	}
}

func TestHandleEventKickAppliesAndEmitsRemoved(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	targetID := uuid.New()
	sanctionID := uuid.New()

	active := activeMembership(communityID, targetID)
	removed := activeMembership(communityID, targetID)
	removed.Status = models.StatusRemoved
	removed.MemberVersion = 2

	repository := &mocks.Repository{}
	publisher := mocks.NewPublisher()
	applier := newSanctionApplier(repository, publisher)

	repository.On("GetMembership", mock.Anything, communityID, targetID).Return(active, nil)
	repository.On("GetActiveBan", mock.Anything, communityID, targetID).Return(nil, nil)
	repository.On("ApplySanctionApplication", mock.Anything, mock.MatchedBy(func(application *models.SanctionApplication) bool {
		return application.Change == models.ChangeRemoved &&
			application.Status == models.StatusRemoved &&
			application.SanctionID != nil && *application.SanctionID == sanctionID &&
			application.ExpectedMemberVersion == active.MemberVersion
	})).Return(removed, nil)

	message := sanctionEvent(t, models.ActionKick, communityID, targetID, sanctionID, nil)
	require.NoError(t, applier.HandleEvent(context.Background(), message))

	messages := publisher.Messages()
	require.Len(t, messages, 1)

	envelope := messages[0].Message.Envelope
	assert.Equal(t, events.EventTypeCommunityMemberChanged, envelope.EventType)
	assert.Equal(t, message.Envelope.TraceID, envelope.TraceID, "trace_id is copied from the causing event")
	assert.Equal(t, message.Envelope.CorrelationID, envelope.CorrelationID, "correlation_id is copied from the causing event")
	require.NotNil(t, envelope.CausationID)
	assert.Equal(t, message.Envelope.EventID, *envelope.CausationID)
	assert.Equal(t, communityID.String(), messages[0].Key)

	payload, ok := messages[0].Message.Payload.(events.CommunityMemberChanged)
	require.True(t, ok)
	assert.Equal(t, models.ChangeRemoved, payload.Change)
	assert.Equal(t, models.StatusRemoved, payload.Status)
	require.NotNil(t, payload.SanctionID)
	assert.Equal(t, sanctionID, *payload.SanctionID)
	assert.Equal(t, int64(2), payload.MemberVersion)
}

func TestHandleEventBanOfOutsiderRecordsOnlyTheBan(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	targetID := uuid.New()

	repository := &mocks.Repository{}
	publisher := mocks.NewPublisher()
	applier := newSanctionApplier(repository, publisher)

	repository.On("GetMembership", mock.Anything, communityID, targetID).Return(nil, models.ErrMembershipNotFound)
	repository.On("GetActiveBan", mock.Anything, communityID, targetID).Return(nil, nil)
	repository.On("ApplySanctionApplication", mock.Anything, mock.MatchedBy(func(application *models.SanctionApplication) bool {
		return application.TouchesMembership() == false && application.BanChange == models.BanChangeRecord
	})).Return(nil, nil)

	message := sanctionEvent(t, models.ActionBan, communityID, targetID, uuid.New(), nil)
	require.NoError(t, applier.HandleEvent(context.Background(), message))

	assert.Empty(t, publisher.Messages(), "a ban of a non member has no member snapshot to project")
}

func TestHandleEventDuplicateEventIsSkipped(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	targetID := uuid.New()

	repository := &mocks.Repository{}
	publisher := mocks.NewPublisher()
	applier := newSanctionApplier(repository, publisher)

	repository.On("GetMembership", mock.Anything, communityID, targetID).Return(activeMembership(communityID, targetID), nil)
	repository.On("GetActiveBan", mock.Anything, communityID, targetID).Return(nil, nil)
	repository.On("ApplySanctionApplication", mock.Anything, mock.Anything).Return(nil, models.ErrDuplicateEvent)

	require.NoError(t, applier.HandleEvent(context.Background(), sanctionEvent(t, models.ActionKick, communityID, targetID, uuid.New(), nil)))
	assert.Empty(t, publisher.Messages(), "a replayed event_id must not emit a second change event")
}

func TestHandleEventStaleMemberVersionIsIgnored(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	targetID := uuid.New()

	repository := &mocks.Repository{}
	publisher := mocks.NewPublisher()
	applier := newSanctionApplier(repository, publisher)

	repository.On("GetMembership", mock.Anything, communityID, targetID).Return(activeMembership(communityID, targetID), nil)
	repository.On("GetActiveBan", mock.Anything, communityID, targetID).Return(nil, nil)
	repository.On("ApplySanctionApplication", mock.Anything, mock.Anything).Return(nil, models.ErrStaleVersion)

	require.NoError(t, applier.HandleEvent(context.Background(), sanctionEvent(t, models.ActionKick, communityID, targetID, uuid.New(), nil)))
	assert.Empty(t, publisher.Messages(), "a stale application must not emit a change event")
}

func TestHandleEventNoOpIsDeduplicatedWithoutEvent(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	targetID := uuid.New()

	repository := &mocks.Repository{}
	publisher := mocks.NewPublisher()
	applier := newSanctionApplier(repository, publisher)

	repository.On("GetMembership", mock.Anything, communityID, targetID).Return(activeMembership(communityID, targetID), nil)
	repository.On("GetActiveBan", mock.Anything, communityID, targetID).Return(nil, nil)
	repository.On("MarkEventProcessed", mock.Anything, mock.Anything, events.EventTypeMemberSanctionChanged, frozenNow).Return(nil)

	require.NoError(t, applier.HandleEvent(context.Background(), sanctionEvent(t, models.ActionUnmute, communityID, targetID, uuid.New(), nil)))

	repository.AssertNotCalled(t, "ApplySanctionApplication", mock.Anything, mock.Anything)
	assert.Empty(t, publisher.Messages())
}

func TestHandleEventNoOpReplayIsSwallowed(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	targetID := uuid.New()

	repository := &mocks.Repository{}
	publisher := mocks.NewPublisher()
	applier := newSanctionApplier(repository, publisher)

	repository.On("GetMembership", mock.Anything, communityID, targetID).Return(activeMembership(communityID, targetID), nil)
	repository.On("GetActiveBan", mock.Anything, communityID, targetID).Return(nil, nil)
	repository.On("MarkEventProcessed", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(models.ErrDuplicateEvent)

	require.NoError(t, applier.HandleEvent(context.Background(), sanctionEvent(t, models.ActionUnmute, communityID, targetID, uuid.New(), nil)))
	assert.Empty(t, publisher.Messages())
}

func TestHandleEventPropagatesFailureForRetry(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	targetID := uuid.New()
	failure := errors.New("postgres unavailable")

	repository := &mocks.Repository{}
	publisher := mocks.NewPublisher()
	applier := newSanctionApplier(repository, publisher)

	repository.On("GetMembership", mock.Anything, communityID, targetID).Return(activeMembership(communityID, targetID), nil)
	repository.On("GetActiveBan", mock.Anything, communityID, targetID).Return(nil, nil)
	repository.On("ApplySanctionApplication", mock.Anything, mock.Anything).Return(nil, failure)

	err := applier.HandleEvent(context.Background(), sanctionEvent(t, models.ActionKick, communityID, targetID, uuid.New(), nil))

	require.Error(t, err)
	assert.ErrorIs(t, err, failure)
	assert.Empty(t, publisher.Messages())
}

func TestHandleEventRejectsUnexpectedContracts(t *testing.T) {
	t.Parallel()

	repository := &mocks.Repository{}
	applier := newSanctionApplier(repository, mocks.NewPublisher())

	t.Run("wrong event type", func(t *testing.T) {
		t.Parallel()

		envelope := events.NewEnvelope(events.EventTypeCommunityMemberChanged, events.EventVersionV1, strings.Repeat("e", 32), uuid.NewString(), nil, frozenNow)
		err := applier.HandleEvent(context.Background(), events.RawMessage{Envelope: envelope, Payload: json.RawMessage(`{}`)})
		require.Error(t, err)
	})

	t.Run("invalid envelope", func(t *testing.T) {
		t.Parallel()

		envelope := events.NewEnvelope(events.EventTypeMemberSanctionChanged, events.EventVersionV1, "", uuid.NewString(), nil, frozenNow)
		envelope.TraceID = ""
		err := applier.HandleEvent(context.Background(), events.RawMessage{Envelope: envelope, Payload: json.RawMessage(`{}`)})
		require.Error(t, err)
	})

	t.Run("payload without identifiers", func(t *testing.T) {
		t.Parallel()

		envelope := events.NewEnvelope(events.EventTypeMemberSanctionChanged, events.EventVersionV1, strings.Repeat("f", 32), uuid.NewString(), nil, frozenNow)
		err := applier.HandleEvent(context.Background(), events.RawMessage{Envelope: envelope, Payload: json.RawMessage(`{"action":"kick"}`)})
		require.Error(t, err)
	})
}
