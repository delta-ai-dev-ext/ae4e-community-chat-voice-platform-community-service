// Package models holds the domain types, the wire request/response shapes and
// the typed errors shared by the service, repository and handler layers.
package models

import (
	"time"

	"github.com/google/uuid"
)

// Membership statuses.
const (
	StatusActive  = "active"
	StatusRemoved = "removed"
)

// ChannelKind distinguishes text from voice channels.
type ChannelKind string

// Supported channel kinds.
const (
	ChannelKindText  ChannelKind = "text"
	ChannelKindVoice ChannelKind = "voice"
)

// Member change kinds carried by EVT-002 `community_member_changed`.
const (
	ChangeJoined       = "joined"
	ChangeLeft         = "left"
	ChangeRemoved      = "removed"
	ChangeBanned       = "banned"
	ChangeUnbanned     = "unbanned"
	ChangeMuted        = "muted"
	ChangeUnmuted      = "unmuted"
	ChangeRolesChanged = "roles_changed"
)

// Channel change kinds carried by EVT-003 `community_channel_changed`.
const (
	ChannelChangeCreated = "created"
	ChannelChangeUpdated = "updated"
	ChannelChangeDeleted = "deleted"
)

// Sanction actions carried by EVT-004 `member_sanction_changed`.
const (
	ActionMute   = "mute"
	ActionUnmute = "unmute"
	ActionKick   = "kick"
	ActionBan    = "ban"
	ActionUnban  = "unban"
)

// Ban change selectors used when applying a sanction.
const (
	BanChangeNone   = ""
	BanChangeRecord = "record"
	BanChangeClear  = "clear"
)

// Voice channel capacity bounds from the specification.
const (
	MinVoiceCapacity     = 1
	MaxVoiceCapacity     = 10
	DefaultVoiceCapacity = 10
)

// Pagination bounds from the specification.
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 100
)

// Community is a community with its configuration and owner.
type Community struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	IconURL     *string   `json:"icon_url"`
	OwnerUserID uuid.UUID `json:"owner_user_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Channel is a text or voice channel of a community. ChannelVersion is the
// monotonic per-channel version guarded by EVT-003 consumers and is therefore
// not part of the public representation.
type Channel struct {
	ID             uuid.UUID   `json:"id"`
	CommunityID    uuid.UUID   `json:"community_id"`
	Kind           ChannelKind `json:"kind"`
	Name           string      `json:"name"`
	Position       int         `json:"position"`
	Capacity       *int        `json:"capacity"`
	ChannelVersion int64       `json:"-"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

// Invite is a community invite code with optional expiry and use limit.
type Invite struct {
	ID          uuid.UUID  `json:"id"`
	CommunityID uuid.UUID  `json:"community_id"`
	Code        string     `json:"code"`
	CreatedBy   uuid.UUID  `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	MaxUses     *int       `json:"max_uses"`
	UseCount    int        `json:"use_count"`
	RevokedAt   *time.Time `json:"-"`
}

// Membership is the authoritative per-member state of a community.
type Membership struct {
	ID            uuid.UUID   `json:"-"`
	CommunityID   uuid.UUID   `json:"community_id"`
	UserID        uuid.UUID   `json:"user_id"`
	Status        string      `json:"status"`
	IsBanned      bool        `json:"is_banned"`
	MutedUntil    *time.Time  `json:"muted_until"`
	RoleIDs       []uuid.UUID `json:"role_ids"`
	JoinedAt      time.Time   `json:"joined_at"`
	LeftAt        *time.Time  `json:"-"`
	MemberVersion int64       `json:"-"`
}

// Member is the public representation of a membership inside a member list.
type Member struct {
	UserID     uuid.UUID   `json:"user_id"`
	Status     string      `json:"status"`
	IsBanned   bool        `json:"is_banned"`
	MutedUntil *time.Time  `json:"muted_until"`
	RoleIDs    []uuid.UUID `json:"role_ids"`
	JoinedAt   time.Time   `json:"joined_at"`
}

// AsMember converts a membership into its public member representation.
func (m *Membership) AsMember() Member {
	return Member{
		UserID:     m.UserID,
		Status:     m.Status,
		IsBanned:   m.IsBanned,
		MutedUntil: m.MutedUntil,
		RoleIDs:    roleIDsOrEmpty(m.RoleIDs),
		JoinedAt:   m.JoinedAt,
	}
}

// IsActive reports whether the membership currently grants community access.
func (m *Membership) IsActive() bool { return m.Status == StatusActive }

// Ban is an entry of the community ban list; an entry is active while LiftedAt
// is nil.
type Ban struct {
	ID          uuid.UUID
	CommunityID uuid.UUID
	UserID      uuid.UUID
	Reason      *string
	BannedAt    time.Time
	LiftedAt    *time.Time
}

// Role is a named permission set with an ordered position; a lower position
// ranks higher.
type Role struct {
	ID          uuid.UUID    `json:"id"`
	CommunityID uuid.UUID    `json:"community_id"`
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions"`
	Position    int          `json:"position"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// SanctionApplication is the fully computed member-state transition for one
// consumed EVT-004. The domain layer derives it from the event and the current
// member state; the repository persists it atomically together with the
// processed-event record, guarded by ExpectedMemberVersion.
type SanctionApplication struct {
	EventID               uuid.UUID
	EventType             string
	CommunityID           uuid.UUID
	UserID                uuid.UUID
	Change                string
	ExpectedMemberVersion int64
	Status                string
	MutedUntilWritten     bool
	MutedUntil            *time.Time
	BanChange             string
	BanReason             *string
	SanctionID            *uuid.UUID
	OccurredAt            time.Time
}

// TouchesMembership reports whether the application writes the membership row.
func (a *SanctionApplication) TouchesMembership() bool { return a.Status != "" }

// RoleIDsOrEmpty returns a non-nil slice so wire payloads always carry `[]`
// instead of `null` for "no roles".
func RoleIDsOrEmpty(values []uuid.UUID) []uuid.UUID { return roleIDsOrEmpty(values) }

func roleIDsOrEmpty(values []uuid.UUID) []uuid.UUID {
	if values == nil {
		return []uuid.UUID{}
	}
	return values
}

// PermissionsOrEmpty returns a non-nil permission slice for wire payloads.
func PermissionsOrEmpty(values []Permission) []Permission {
	if values == nil {
		return []Permission{}
	}
	return values
}
