package models

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrorResponse is the platform wire shape for every failed request.
type ErrorResponse struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

// HealthStatus is the probe response body.
type HealthStatus struct {
	Status string `json:"status"`
}

// CreateCommunityRequest is the body of POST /communities.
type CreateCommunityRequest struct {
	Name        string  `json:"name" validate:"required,max=100"`
	Description *string `json:"description" validate:"omitempty,max=500"`
}

// Validate enforces the boundary rules of US-001.
func (r *CreateCommunityRequest) Validate() *Error {
	if name := strings.TrimSpace(r.Name); name == "" || len([]rune(name)) > 100 {
		return WithDetail(ErrValidationFailed, "name must be between 1 and 100 characters")
	}
	if r.Description != nil && len([]rune(*r.Description)) > 500 {
		return WithDetail(ErrValidationFailed, "description must be at most 500 characters")
	}
	return nil
}

// UpdateCommunityRequest is the body of PATCH /communities/{id}; only provided
// fields change.
type UpdateCommunityRequest struct {
	Name        *string        `json:"name" validate:"omitempty,min=1,max=100"`
	Description OptionalString `json:"description"`
	IconURL     OptionalString `json:"icon_url"`
}

// Validate enforces the boundary rules of US-004.
func (r *UpdateCommunityRequest) Validate() *Error {
	if r.Name == nil && !r.Description.Set && !r.IconURL.Set {
		return WithDetail(ErrValidationFailed, "at least one of name, description or icon_url is required")
	}
	if r.Name != nil {
		if name := strings.TrimSpace(*r.Name); name == "" || len([]rune(name)) > 100 {
			return WithDetail(ErrValidationFailed, "name must be between 1 and 100 characters")
		}
	}
	if r.Description.Set && r.Description.Value != nil && len([]rune(*r.Description.Value)) > 500 {
		return WithDetail(ErrValidationFailed, "description must be at most 500 characters")
	}
	if r.IconURL.Set && r.IconURL.Value != nil {
		if err := validateHTTPURL(*r.IconURL.Value); err != nil {
			return WithDetail(ErrValidationFailed, "icon_url must be an http(s) URL")
		}
	}
	return nil
}

// CreateChannelRequest is the body of POST /communities/{id}/channels.
type CreateChannelRequest struct {
	Name     string `json:"name" validate:"required,max=100"`
	Kind     string `json:"kind" validate:"required,oneof=text voice"`
	Capacity *int   `json:"capacity" validate:"omitempty,min=1,max=10"`
}

// Validate enforces the boundary rules of US-005.
func (r *CreateChannelRequest) Validate() *Error {
	if name := strings.TrimSpace(r.Name); name == "" || len([]rune(name)) > 100 {
		return WithDetail(ErrValidationFailed, "name must be between 1 and 100 characters")
	}
	if r.Kind != string(ChannelKindText) && r.Kind != string(ChannelKindVoice) {
		return WithDetail(ErrValidationFailed, "kind must be text or voice")
	}
	if r.Capacity != nil {
		if r.Kind == string(ChannelKindText) {
			return WithDetail(ErrValidationFailed, "capacity applies to voice channels only")
		}
		if *r.Capacity < MinVoiceCapacity || *r.Capacity > MaxVoiceCapacity {
			return WithDetail(ErrValidationFailed, "capacity must be between 1 and 10")
		}
	}
	return nil
}

// UpdateChannelRequest is the body of PATCH /communities/{id}/channels/{id}.
type UpdateChannelRequest struct {
	Name     *string     `json:"name" validate:"omitempty,min=1,max=100"`
	Position *int        `json:"position" validate:"omitempty,min=0"`
	Capacity OptionalInt `json:"capacity"`
}

// Validate enforces the boundary rules of US-006.
func (r *UpdateChannelRequest) Validate() *Error {
	if r.Name == nil && r.Position == nil && !r.Capacity.Set {
		return WithDetail(ErrValidationFailed, "at least one of name, position or capacity is required")
	}
	if r.Name != nil {
		if name := strings.TrimSpace(*r.Name); name == "" || len([]rune(name)) > 100 {
			return WithDetail(ErrValidationFailed, "name must be between 1 and 100 characters")
		}
	}
	if r.Position != nil && *r.Position < 0 {
		return WithDetail(ErrValidationFailed, "position must be zero or greater")
	}
	if r.Capacity.Set && r.Capacity.Value != nil {
		if *r.Capacity.Value < MinVoiceCapacity || *r.Capacity.Value > MaxVoiceCapacity {
			return WithDetail(ErrValidationFailed, "capacity must be between 1 and 10")
		}
	}
	return nil
}

// CreateInviteRequest is the body of POST /communities/{id}/invites.
type CreateInviteRequest struct {
	ExpiresInSeconds *int `json:"expires_in_seconds" validate:"omitempty,min=60,max=2592000"`
	MaxUses          *int `json:"max_uses" validate:"omitempty,min=1"`
}

// Validate enforces the boundary rules of US-008.
func (r *CreateInviteRequest) Validate() *Error {
	if r.ExpiresInSeconds != nil && (*r.ExpiresInSeconds < 60 || *r.ExpiresInSeconds > 2592000) {
		return WithDetail(ErrValidationFailed, "expires_in_seconds must be between 60 and 2592000")
	}
	if r.MaxUses != nil && *r.MaxUses < 1 {
		return WithDetail(ErrValidationFailed, "max_uses must be at least 1")
	}
	return nil
}

// CreateRoleRequest is the body of POST /communities/{id}/roles.
type CreateRoleRequest struct {
	Name        string   `json:"name" validate:"required,max=100"`
	Permissions []string `json:"permissions"`
	Position    *int     `json:"position" validate:"required,min=0"`
}

// Validate enforces the boundary rules of US-014.
func (r *CreateRoleRequest) Validate() *Error {
	if name := strings.TrimSpace(r.Name); name == "" || len([]rune(name)) > 100 {
		return WithDetail(ErrValidationFailed, "name must be between 1 and 100 characters")
	}
	if r.Permissions == nil {
		return WithDetail(ErrValidationFailed, "permissions is required")
	}
	if invalid := InvalidPermissions(toPermissions(r.Permissions)); len(invalid) > 0 {
		return WithDetail(ErrValidationFailed, fmt.Sprintf("unknown permission key %q", invalid[0]))
	}
	if r.Position == nil {
		return WithDetail(ErrValidationFailed, "position is required")
	}
	if *r.Position < 0 {
		return WithDetail(ErrValidationFailed, "position must be zero or greater")
	}
	return nil
}

// UpdateRoleRequest is the body of PATCH /communities/{id}/roles/{id}.
type UpdateRoleRequest struct {
	Name        *string   `json:"name" validate:"omitempty,min=1,max=100"`
	Permissions *[]string `json:"permissions"`
	Position    *int      `json:"position" validate:"omitempty,min=0"`
}

// Validate enforces the boundary rules of US-014 for partial updates.
func (r *UpdateRoleRequest) Validate() *Error {
	if r.Name == nil && r.Permissions == nil && r.Position == nil {
		return WithDetail(ErrValidationFailed, "at least one of name, permissions or position is required")
	}
	if r.Name != nil {
		if name := strings.TrimSpace(*r.Name); name == "" || len([]rune(name)) > 100 {
			return WithDetail(ErrValidationFailed, "name must be between 1 and 100 characters")
		}
	}
	if r.Permissions != nil {
		if invalid := InvalidPermissions(toPermissions(*r.Permissions)); len(invalid) > 0 {
			return WithDetail(ErrValidationFailed, fmt.Sprintf("unknown permission key %q", invalid[0]))
		}
	}
	if r.Position != nil && *r.Position < 0 {
		return WithDetail(ErrValidationFailed, "position must be zero or greater")
	}
	return nil
}

// SetMemberRolesRequest is the body of PUT
// /communities/{id}/members/{user_id}/roles. A nil RoleIDs slice means the
// property was absent; an empty slice clears every role.
type SetMemberRolesRequest struct {
	RoleIDs []uuid.UUID `json:"role_ids"`
}

// Validate enforces the boundary rules of US-015.
func (r *SetMemberRolesRequest) Validate() *Error {
	if r.RoleIDs == nil {
		return WithDetail(ErrValidationFailed, "role_ids is required")
	}
	for _, roleID := range r.RoleIDs {
		if roleID == uuid.Nil {
			return WithDetail(ErrValidationFailed, "role_ids must contain UUIDs")
		}
	}
	return nil
}

// AuthorizationCheckRequest is the body of API-001.
type AuthorizationCheckRequest struct {
	ActorUserID  uuid.UUID  `json:"actor_user_id"`
	Permission   Permission `json:"permission"`
	TargetUserID *uuid.UUID `json:"target_user_id"`
}

// Validate enforces the API-001 contract: a malformed body or an unknown
// permission key is a 400, never a denial.
func (r *AuthorizationCheckRequest) Validate() *Error {
	if r.ActorUserID == uuid.Nil {
		return WithDetail(ErrInvalidRequest, "actor_user_id must be a UUID")
	}
	if !ValidPermission(r.Permission) {
		return WithDetail(ErrInvalidRequest, fmt.Sprintf("unknown permission key %q", r.Permission))
	}
	if r.TargetUserID != nil && *r.TargetUserID == uuid.Nil {
		return WithDetail(ErrInvalidRequest, "target_user_id must be a UUID or null")
	}
	return nil
}

// VoiceAccessCheckRequest is the body of API-002.
type VoiceAccessCheckRequest struct {
	UserID uuid.UUID `json:"user_id"`
}

// Validate enforces the API-002 contract.
func (r *VoiceAccessCheckRequest) Validate() *Error {
	if r.UserID == uuid.Nil {
		return WithDetail(ErrInvalidRequest, "user_id must be a UUID")
	}
	return nil
}

// CallerContext describes the calling member inside a community detail.
type CallerContext struct {
	IsMember    bool         `json:"is_member"`
	IsOwner     bool         `json:"is_owner"`
	RoleIDs     []uuid.UUID  `json:"role_ids"`
	Permissions []Permission `json:"permissions"`
}

// CommunityDetailResponse is the body of GET /communities/{id}.
type CommunityDetailResponse struct {
	Community *Community    `json:"community"`
	Channels  []*Channel    `json:"channels"`
	Caller    CallerContext `json:"caller"`
}

// CommunityListResponse is the body of GET /communities/mine.
type CommunityListResponse struct {
	Communities []*Community `json:"communities"`
	NextCursor  *string      `json:"next_cursor"`
}

// MemberListResponse is the body of GET /communities/{id}/members.
type MemberListResponse struct {
	Members    []Member `json:"members"`
	NextCursor *string  `json:"next_cursor"`
}

// RoleListResponse is the body of GET /communities/{id}/roles.
type RoleListResponse struct {
	Roles []*Role `json:"roles"`
}

// InvitePreviewCommunity is the community preview inside an invite preview.
type InvitePreviewCommunity struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	IconURL     *string   `json:"icon_url"`
	MemberCount int       `json:"member_count"`
}

// InvitePreviewResponse is the body of GET /invites/{code}.
type InvitePreviewResponse struct {
	Valid         bool                   `json:"valid"`
	InvalidReason *string                `json:"invalid_reason"`
	Community     InvitePreviewCommunity `json:"community"`
}

// AuthorizationTarget is the target snapshot of API-001.
type AuthorizationTarget struct {
	UserID           uuid.UUID  `json:"user_id"`
	IsMember         bool       `json:"is_member"`
	IsBanned         bool       `json:"is_banned"`
	IsOwner          bool       `json:"is_owner"`
	OutrankedByActor bool       `json:"outranked_by_actor"`
	MutedUntil       *time.Time `json:"muted_until"`
}

// AuthorizationCheckResponse is the body of API-001; a denied check is a 200
// with Allowed false and a DenialReason.
type AuthorizationCheckResponse struct {
	CommunityID      uuid.UUID            `json:"community_id"`
	ActorUserID      uuid.UUID            `json:"actor_user_id"`
	Permission       Permission           `json:"permission"`
	Allowed          bool                 `json:"allowed"`
	DenialReason     *string              `json:"denial_reason"`
	ActorIsMember    bool                 `json:"actor_is_member"`
	ActorIsOwner     bool                 `json:"actor_is_owner"`
	ActorPermissions []Permission         `json:"actor_permissions"`
	Target           *AuthorizationTarget `json:"target"`
}

// VoiceChannelSummary is the voice channel projection of API-002.
type VoiceChannelSummary struct {
	ChannelID uuid.UUID `json:"channel_id"`
	Name      string    `json:"name"`
	Capacity  int       `json:"capacity"`
}

// VoiceAccessCheckResponse is the body of API-002; a denied check is a 200 with
// Allowed false and a DenialReason.
type VoiceAccessCheckResponse struct {
	CommunityID   uuid.UUID             `json:"community_id"`
	UserID        uuid.UUID             `json:"user_id"`
	Allowed       bool                  `json:"allowed"`
	DenialReason  *string               `json:"denial_reason"`
	Channel       *VoiceChannelSummary  `json:"channel,omitempty"`
	MutedUntil    *time.Time            `json:"muted_until"`
	VoiceChannels []VoiceChannelSummary `json:"voice_channels"`
}

// CommunityPatch is a partial community update handed to the repository.
type CommunityPatch struct {
	Name        *string
	Description OptionalString
	IconURL     OptionalString
}

// ChannelPatch is a partial channel update handed to the repository.
type ChannelPatch struct {
	Name     *string
	Position *int
	Capacity OptionalInt
}

// RolePatch is a partial role update handed to the repository.
type RolePatch struct {
	Name        *string
	Permissions *[]Permission
	Position    *int
}

// PageQuery is one page request decoded from `cursor` and `limit`.
type PageQuery struct {
	Limit  int
	Offset int
}

// DecodePageQuery validates the pagination query parameters.
func DecodePageQuery(cursor string, limit *int) (PageQuery, *Error) {
	page := PageQuery{Limit: DefaultPageLimit}

	if limit != nil {
		if *limit < 1 || *limit > MaxPageLimit {
			return PageQuery{}, WithDetail(ErrInvalidRequest, fmt.Sprintf("limit must be between 1 and %d", MaxPageLimit))
		}
		page.Limit = *limit
	}

	if cursor == "" {
		return page, nil
	}

	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return PageQuery{}, WithDetail(ErrInvalidRequest, "cursor is not valid")
	}
	raw := strings.TrimPrefix(string(decoded), "v1:")
	offset, err := strconv.Atoi(raw)
	if err != nil || offset < 0 {
		return PageQuery{}, WithDetail(ErrInvalidRequest, "cursor is not valid")
	}
	page.Offset = offset

	return page, nil
}

// EncodeCursor renders the opaque cursor for the next page.
func EncodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("v1:" + strconv.Itoa(offset)))
}

func toPermissions(values []string) []Permission {
	permissions := make([]Permission, 0, len(values))
	for _, value := range values {
		permissions = append(permissions, Permission(value))
	}
	return permissions
}

func validateHTTPURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("missing host")
	}
	return nil
}
