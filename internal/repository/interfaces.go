// Package repository is the PostgreSQL persistence layer of the service. It
// owns every SQL statement and every multi-row invariant: the domain services
// see only these ports, and handlers never touch the database directly.
//
// Two rules hold for every method here: statements are parameterized (values are
// never interpolated into SQL), and each documented invariant — community plus
// its two default channels, invite accept, role replacement, EVT-004
// application — is applied inside a single transaction.
package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"community_service/internal/models"
)

// Pinger reports whether PostgreSQL is reachable; used by the readiness probe.
type Pinger interface {
	Ping(ctx context.Context) error
}

// CommunityStore persists communities.
type CommunityStore interface {
	// CreateCommunity inserts the community, its owner membership and its
	// default channels transactionally, so a community never exists without an
	// owner and its default channels.
	CreateCommunity(ctx context.Context, community *models.Community, ownerMembership *models.Membership, defaultChannels []*models.Channel) error
	GetCommunity(ctx context.Context, communityID uuid.UUID) (*models.Community, error)
	UpdateCommunity(ctx context.Context, communityID uuid.UUID, patch models.CommunityPatch, now time.Time) (*models.Community, error)
	// ListCommunitiesByMember returns communities with an active membership,
	// newest first, using limit and offset.
	ListCommunitiesByMember(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*models.Community, error)
	CountActiveMembers(ctx context.Context, communityID uuid.UUID) (int, error)
}

// ChannelStore persists channels.
type ChannelStore interface {
	ListChannels(ctx context.Context, communityID uuid.UUID) ([]*models.Channel, error)
	ListVoiceChannels(ctx context.Context, communityID uuid.UUID) ([]*models.Channel, error)
	GetChannel(ctx context.Context, communityID, channelID uuid.UUID) (*models.Channel, error)
	CountChannels(ctx context.Context, communityID uuid.UUID) (int, error)
	// CreateChannel inserts the channel, bumping channel_version to 1; a
	// negative Position means "append after the last channel".
	CreateChannel(ctx context.Context, channel *models.Channel) error
	UpdateChannel(ctx context.Context, communityID, channelID uuid.UUID, patch models.ChannelPatch, now time.Time) (*models.Channel, error)
	// DeleteChannel refuses to delete the last remaining channel of a
	// community with models.ErrLastChannel.
	DeleteChannel(ctx context.Context, communityID, channelID uuid.UUID) (*models.Channel, error)
}

// InviteStore persists invites and the join that consumes them.
type InviteStore interface {
	CreateInvite(ctx context.Context, invite *models.Invite) error
	GetInviteByCode(ctx context.Context, code string) (*models.Invite, error)
	// RevokeInvite is idempotent: revoking an already revoked invite keeps the
	// original revocation timestamp and succeeds.
	RevokeInvite(ctx context.Context, communityID uuid.UUID, code string, now time.Time) (*models.Invite, error)
	// JoinWithInvite applies the whole accept flow in one transaction: it locks
	// the invite, re-checks usability and the ban list, inserts or reactivates
	// the membership, and increments use_count. The boolean result reports
	// whether the membership state changed (false for an idempotent re-accept).
	JoinWithInvite(ctx context.Context, communityID, userID, membershipID uuid.UUID, code string, now time.Time) (*models.Membership, bool, error)
}

// MembershipStore persists membership state and the ban list.
type MembershipStore interface {
	GetMembership(ctx context.Context, communityID, userID uuid.UUID) (*models.Membership, error)
	ListMembers(ctx context.Context, communityID uuid.UUID, limit, offset int) ([]*models.Membership, error)
	RemoveMembership(ctx context.Context, communityID, userID uuid.UUID, now time.Time) (*models.Membership, error)
	// GetActiveBan returns nil when the user has no active ban.
	GetActiveBan(ctx context.Context, communityID, userID uuid.UUID) (*models.Ban, error)
}

// RoleStore persists roles and role assignments.
type RoleStore interface {
	ListRoles(ctx context.Context, communityID uuid.UUID) ([]*models.Role, error)
	GetRole(ctx context.Context, communityID, roleID uuid.UUID) (*models.Role, error)
	CreateRole(ctx context.Context, role *models.Role) error
	// UpdateRole bumps member_version for every active member holding the role
	// and returns their post-change snapshots so EVT-002 can be emitted.
	UpdateRole(ctx context.Context, communityID, roleID uuid.UUID, patch models.RolePatch, now time.Time) (*models.Role, []*models.Membership, error)
	// DeleteRole removes the role and its assignments, bumping member_version
	// for every affected active member.
	DeleteRole(ctx context.Context, communityID, roleID uuid.UUID) (*models.Role, []*models.Membership, error)
	// SetMemberRoles replaces the member's role set and bumps member_version in
	// one transaction.
	SetMemberRoles(ctx context.Context, communityID, userID uuid.UUID, roleIDs []uuid.UUID, now time.Time) (*models.Membership, error)
}

// SanctionStore applies consumed EVT-004 events.
type SanctionStore interface {
	// ApplySanctionApplication records the processed event, applies the member
	// state change and the ban change, and bumps member_version atomically. It
	// returns models.ErrDuplicateEvent for a replayed event and
	// models.ErrStaleVersion when the member state already moved on.
	ApplySanctionApplication(ctx context.Context, application *models.SanctionApplication) (*models.Membership, error)
	// MarkEventProcessed records an event that required no state change.
	MarkEventProcessed(ctx context.Context, eventID uuid.UUID, eventType string, now time.Time) error
}

// Repository is the complete persistence port used by the wiring layer.
type Repository interface {
	CommunityStore
	ChannelStore
	InviteStore
	MembershipStore
	RoleStore
	SanctionStore
	Pinger
}
