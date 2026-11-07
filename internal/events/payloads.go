package events

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// CommunityMemberChanged is the EVT-002 payload: the authoritative member
// snapshot after every membership state change.
type CommunityMemberChanged struct {
	CommunityID   uuid.UUID   `json:"community_id"`
	UserID        uuid.UUID   `json:"user_id"`
	Change        string      `json:"change"`
	Status        string      `json:"status"`
	IsBanned      bool        `json:"is_banned"`
	MutedUntil    *time.Time  `json:"muted_until"`
	RoleIDs       []uuid.UUID `json:"role_ids"`
	SanctionID    *uuid.UUID  `json:"sanction_id"`
	MemberVersion int64       `json:"member_version"`
}

// CommunityChannelChanged is the EVT-003 payload: the channel snapshot after a
// create, update or delete.
type CommunityChannelChanged struct {
	CommunityID    uuid.UUID `json:"community_id"`
	ChannelID      uuid.UUID `json:"channel_id"`
	Change         string    `json:"change"`
	Kind           string    `json:"kind"`
	Name           string    `json:"name"`
	Position       int       `json:"position"`
	Capacity       *int      `json:"capacity"`
	ChannelVersion int64     `json:"channel_version"`
}

// MemberSanctionChanged is the EVT-004 payload consumed from moderation_events.
type MemberSanctionChanged struct {
	SanctionID   uuid.UUID  `json:"sanction_id"`
	CommunityID  uuid.UUID  `json:"community_id"`
	TargetUserID uuid.UUID  `json:"target_user_id"`
	ActorUserID  *uuid.UUID `json:"actor_user_id"`
	Action       string     `json:"action"`
	Source       string     `json:"source"`
	Reason       *string    `json:"reason"`
	ExpiresAt    *time.Time `json:"expires_at"`
	Appealable   bool       `json:"appealable"`
}

// RawMessage is one consumed event: the decoded envelope plus the payload, still
// encoded so the consumer can decode only the contract it expects.
type RawMessage struct {
	Envelope Envelope        `json:"envelope"`
	Payload  json.RawMessage `json:"payload"`
}
