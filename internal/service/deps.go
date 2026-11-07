// Package service implements the domain logic of community_service: permission
// computation, role hierarchy, community/channel/invite/membership/role rules,
// the two internal oracles and the EVT-004 application. It performs no I/O
// beyond the repository ports and the event publisher, and it never parses HTTP
// bodies.
package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"community_service/internal/events"
	"community_service/internal/models"
	"community_service/internal/obs"
	"community_service/internal/repository"
)

// Deps is the set of collaborators every domain service needs; wiring the same
// instance keeps the transaction boundaries of the repository intact.
type Deps struct {
	Communities repository.CommunityStore
	Channels    repository.ChannelStore
	Invites     repository.InviteStore
	Memberships repository.MembershipStore
	Roles       repository.RoleStore
	Sanctions   repository.SanctionStore
	Publisher   events.Publisher
	Clock       models.Clock
	Logger      *zap.Logger
	Metrics     *obs.Metrics
}

// withDefaults fills the injectable collaborators with production defaults so
// tests only have to provide what they exercise.
func (d Deps) withDefaults() Deps {
	if d.Clock == nil {
		d.Clock = models.SystemClock{}
	}
	if d.Logger == nil {
		d.Logger = zap.NewNop()
	}
	if d.Metrics == nil {
		d.Metrics = obs.NewMetrics()
	}
	return d
}

// access is the resolved caller context of one community-scoped request.
type access struct {
	Community  *models.Community
	Membership *models.Membership
	Roles      []*models.Role
}

// isOwner reports whether the user owns the community; ownership is implicit
// supremacy, never a role.
func (a *access) isOwner(userID uuid.UUID) bool {
	return a.Community != nil && a.Community.OwnerUserID == userID
}

// isMember reports whether the caller currently has an active membership.
func (a *access) isMember() bool { return a.Membership != nil && a.Membership.IsActive() }

// resolve loads the community, the caller's membership and the caller's roles.
func (d Deps) resolve(ctx context.Context, communityID, userID uuid.UUID) (*access, error) {
	community, err := d.Communities.GetCommunity(ctx, communityID)
	if err != nil {
		return nil, err
	}

	resolved := &access{Community: community}

	membership, err := d.Memberships.GetMembership(ctx, communityID, userID)
	switch {
	case err == nil:
		resolved.Membership = membership
	case errors.Is(err, models.ErrMembershipNotFound):
		return resolved, nil
	default:
		return nil, err
	}

	resolved.Roles, err = d.roleSet(ctx, communityID, membership.RoleIDs)
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

// roleSet resolves role ids into roles, preserving rank order.
func (d Deps) roleSet(ctx context.Context, communityID uuid.UUID, roleIDs []uuid.UUID) ([]*models.Role, error) {
	if len(roleIDs) == 0 {
		return nil, nil
	}

	all, err := d.Roles.ListRoles(ctx, communityID)
	if err != nil {
		return nil, err
	}
	return selectRoles(all, roleIDs), nil
}

// selectRoles keeps the roles whose id is present, preserving the input order of
// the community's rank ordering.
func selectRoles(all []*models.Role, roleIDs []uuid.UUID) []*models.Role {
	wanted := make(map[uuid.UUID]struct{}, len(roleIDs))
	for _, roleID := range roleIDs {
		wanted[roleID] = struct{}{}
	}

	selected := make([]*models.Role, 0, len(roleIDs))
	for _, role := range all {
		if role == nil {
			continue
		}
		if _, ok := wanted[role.ID]; ok {
			selected = append(selected, role)
		}
	}
	return selected
}

// requireMember rejects a caller who may read community state only as a member.
func (d Deps) requireMember(userID uuid.UUID, resolved *access) error {
	if resolved.isOwner(userID) || resolved.isMember() {
		return nil
	}
	return models.ErrNotAMember
}

// requirePermission rejects a caller who lacks the permission. A non-member holds
// no permission at all, so the documented `missing_permission` code is returned
// for writes regardless of membership.
func (d Deps) requirePermission(userID uuid.UUID, resolved *access, permission models.Permission) error {
	if !HasPermission(resolved.isOwner(userID), resolved.Roles, permission) {
		return models.ErrMissingPermission
	}
	return nil
}

// memberEnvelope builds an EVT-002 envelope.
func memberEnvelope(traceID, correlationID string, causationID *string, now time.Time) events.Envelope {
	return events.NewEnvelope(events.EventTypeCommunityMemberChanged, events.EventVersionV1, traceID, correlationID, causationID, now)
}

// channelEnvelope builds an EVT-003 envelope.
func channelEnvelope(traceID, correlationID string, now time.Time) events.Envelope {
	return events.NewEnvelope(events.EventTypeCommunityChannelChanged, events.EventVersionV1, traceID, correlationID, nil, now)
}

// publishMemberChanged emits EVT-002 for one committed member snapshot. The
// state change is already committed, so a publish failure is logged and counted
// instead of failing the request; the retrying publisher has already retried.
func (d Deps) publishMemberChanged(ctx context.Context, envelope events.Envelope, change string, membership *models.Membership, sanctionID *uuid.UUID) {
	payload := events.CommunityMemberChanged{
		CommunityID:   membership.CommunityID,
		UserID:        membership.UserID,
		Change:        change,
		Status:        membership.Status,
		IsBanned:      membership.IsBanned,
		MutedUntil:    membership.MutedUntil,
		RoleIDs:       models.RoleIDsOrEmpty(membership.RoleIDs),
		SanctionID:    sanctionID,
		MemberVersion: membership.MemberVersion,
	}

	message := events.Message{Envelope: envelope, Payload: payload}
	if err := d.Publisher.Publish(ctx, events.CommunityTopic, membership.CommunityID.String(), message); err != nil {
		d.Logger.Error("community_member_changed publish failed after commit",
			zap.String("trace_id", envelope.TraceID),
			zap.String("community_id", membership.CommunityID.String()),
			zap.String("user_id", membership.UserID.String()),
			zap.Int64("member_version", membership.MemberVersion),
			zap.Error(err))
		d.Metrics.RecordPublishFailure()
	}
}

// publishChannelChanged emits EVT-003 for one committed channel snapshot.
func (d Deps) publishChannelChanged(ctx context.Context, envelope events.Envelope, change string, channel *models.Channel) {
	payload := events.CommunityChannelChanged{
		CommunityID:    channel.CommunityID,
		ChannelID:      channel.ID,
		Change:         change,
		Kind:           string(channel.Kind),
		Name:           channel.Name,
		Position:       channel.Position,
		Capacity:       channel.Capacity,
		ChannelVersion: channel.ChannelVersion,
	}

	message := events.Message{Envelope: envelope, Payload: payload}
	if err := d.Publisher.Publish(ctx, events.CommunityTopic, channel.CommunityID.String(), message); err != nil {
		d.Logger.Error("community_channel_changed publish failed after commit",
			zap.String("trace_id", envelope.TraceID),
			zap.String("community_id", channel.CommunityID.String()),
			zap.String("channel_id", channel.ID.String()),
			zap.Int64("channel_version", channel.ChannelVersion),
			zap.Error(err))
		d.Metrics.RecordPublishFailure()
	}
}
