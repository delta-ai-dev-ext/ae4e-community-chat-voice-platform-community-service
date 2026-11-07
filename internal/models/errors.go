package models

import (
	"errors"
	"fmt"
	"net/http"
)

// Code is a stable, machine-readable, snake_case error code.
type Code string

// Error codes returned by the service.
const (
	CodeInvalidRequest       Code = "invalid_request"
	CodeUnauthorized         Code = "unauthorized"
	CodeValidationFailed     Code = "validation_failed"
	CodeMissingPermission    Code = "missing_permission"
	CodeNotAMember           Code = "not_a_member"
	CodeCommunityNotFound    Code = "community_not_found"
	CodeChannelNotFound      Code = "channel_not_found"
	CodeRoleNotFound         Code = "role_not_found"
	CodeMemberNotFound       Code = "member_not_found"
	CodeMembershipNotFound   Code = "membership_not_found"
	CodeInviteNotFound       Code = "invite_not_found"
	CodeInviteExpired        Code = "invite_expired"
	CodeBanned               Code = "banned"
	CodeOwnerCannotLeave     Code = "owner_cannot_leave"
	CodeChannelNameConflict  Code = "channel_name_conflict"
	CodeRoleNameConflict     Code = "role_name_conflict"
	CodeRolePositionConflict Code = "role_position_conflict"
	CodeLastChannel          Code = "last_channel"
	CodeTargetOutranksActor  Code = "target_outranks_actor"
	CodeTargetIsOwner        Code = "target_is_owner"
	CodeTargetIsSelf         Code = "target_is_self"
	CodeInternal             Code = "internal_error"
	CodeNotReady             Code = "not_ready"
)

// Error is the platform error shape of docs/engineering_guide.md: a stable
// code, a human-readable detail, and the HTTP status the handler must use. Cause
// is logged but never sent to clients.
type Error struct {
	Code   Code
	Detail string
	Status int
	Cause  error
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Detail, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

// Unwrap exposes the wrapped cause to errors.Is/errors.As.
func (e *Error) Unwrap() error { return e.Cause }

// Is compares error codes, so errors.Is works against a sentinel even when a
// request-specific copy adds detail.
func (e *Error) Is(target error) bool {
	var other *Error
	if !errors.As(target, &other) {
		return false
	}
	return e.Code == other.Code
}

// HTTPStatus returns the status code the handler must write.
func (e *Error) HTTPStatus() int {
	if e.Status == 0 {
		return http.StatusInternalServerError
	}
	return e.Status
}

// The service's error sentinels. Services return these (optionally copied with
// WithDetail) and handlers map them one-to-one onto HTTP responses.
var (
	ErrInvalidRequest       = &Error{Code: CodeInvalidRequest, Detail: "The request body could not be parsed.", Status: http.StatusBadRequest}
	ErrUnauthorized         = &Error{Code: CodeUnauthorized, Detail: "A valid access token is required.", Status: http.StatusUnauthorized}
	ErrValidationFailed     = &Error{Code: CodeValidationFailed, Detail: "One or more fields are invalid.", Status: http.StatusUnprocessableEntity}
	ErrMissingPermission    = &Error{Code: CodeMissingPermission, Detail: "You do not have permission to perform this action.", Status: http.StatusForbidden}
	ErrNotAMember           = &Error{Code: CodeNotAMember, Detail: "You are not a member of this community.", Status: http.StatusForbidden}
	ErrCommunityNotFound    = &Error{Code: CodeCommunityNotFound, Detail: "Community does not exist.", Status: http.StatusNotFound}
	ErrChannelNotFound      = &Error{Code: CodeChannelNotFound, Detail: "Channel not found in this community.", Status: http.StatusNotFound}
	ErrRoleNotFound         = &Error{Code: CodeRoleNotFound, Detail: "Role not found in this community.", Status: http.StatusNotFound}
	ErrMemberNotFound       = &Error{Code: CodeMemberNotFound, Detail: "Member not found in this community.", Status: http.StatusNotFound}
	ErrMembershipNotFound   = &Error{Code: CodeMembershipNotFound, Detail: "You are not a member of this community.", Status: http.StatusNotFound}
	ErrInviteNotFound       = &Error{Code: CodeInviteNotFound, Detail: "This invite does not exist.", Status: http.StatusNotFound}
	ErrInviteExpired        = &Error{Code: CodeInviteExpired, Detail: "This invite is no longer valid.", Status: http.StatusGone}
	ErrBanned               = &Error{Code: CodeBanned, Detail: "You are banned from this community.", Status: http.StatusForbidden}
	ErrOwnerCannotLeave     = &Error{Code: CodeOwnerCannotLeave, Detail: "Transfer ownership before leaving.", Status: http.StatusConflict}
	ErrChannelNameConflict  = &Error{Code: CodeChannelNameConflict, Detail: "A channel with this name already exists.", Status: http.StatusConflict}
	ErrRoleNameConflict     = &Error{Code: CodeRoleNameConflict, Detail: "A role with this name already exists.", Status: http.StatusConflict}
	ErrRolePositionConflict = &Error{Code: CodeRolePositionConflict, Detail: "A role with this position already exists.", Status: http.StatusConflict}
	ErrLastChannel          = &Error{Code: CodeLastChannel, Detail: "A community must keep at least one channel.", Status: http.StatusConflict}
	ErrTargetOutranksActor  = &Error{Code: CodeTargetOutranksActor, Detail: "You cannot modify a member who outranks you.", Status: http.StatusForbidden}
	ErrTargetIsOwner        = &Error{Code: CodeTargetIsOwner, Detail: "You cannot modify the community owner.", Status: http.StatusForbidden}
	ErrTargetIsSelf         = &Error{Code: CodeTargetIsSelf, Detail: "You cannot modify yourself.", Status: http.StatusForbidden}
	ErrInternal             = &Error{Code: CodeInternal, Detail: "An unexpected error occurred.", Status: http.StatusInternalServerError}
	ErrNotReady             = &Error{Code: CodeNotReady, Detail: "Dependencies are not reachable.", Status: http.StatusServiceUnavailable}
)

// Internal control-flow sentinels of the EVT-004 consumer. They never reach a
// client and are matched with errors.Is.
var (
	// ErrDuplicateEvent reports an envelope.event_id already recorded in
	// processed_events; the event has been applied before and must be skipped.
	ErrDuplicateEvent = errors.New("event already processed")
	// ErrStaleVersion reports a member state that is already newer than the
	// change being applied; last-writer-wins drops the stale change.
	ErrStaleVersion = errors.New("stale member version")
)

// WithDetail returns a copy of base carrying a call-specific detail. The shared
// sentinel is never mutated, so concurrent requests stay independent.
func WithDetail(base *Error, detail string) *Error {
	clone := *base
	clone.Detail = detail
	return &clone
}

// Wrap returns a copy of base carrying cause, which is logged but withheld from
// clients.
func Wrap(base *Error, cause error) *Error {
	clone := *base
	clone.Cause = cause
	return &clone
}

// AsError extracts the platform error shape from err.
func AsError(err error) (*Error, bool) {
	var target *Error
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}
