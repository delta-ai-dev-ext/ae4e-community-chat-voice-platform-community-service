package models

import "time"

// CheckInvite reports why an invite can no longer be used, or "" when it is
// still usable. A revoked invite takes precedence over expiry, which takes
// precedence over exhaustion; a nil invite is reported as unusable so callers
// that forgot the existence check still fail closed.
func CheckInvite(invite *Invite, now time.Time) string {
	if invite == nil {
		return InviteInvalidExpired
	}
	if invite.RevokedAt != nil {
		return InviteInvalidRevoked
	}
	if invite.ExpiresAt != nil && !now.Before(*invite.ExpiresAt) {
		return InviteInvalidExpired
	}
	if invite.MaxUses != nil && invite.UseCount >= *invite.MaxUses {
		return InviteInvalidExhausted
	}
	return ""
}

// MutedNow reports whether the membership is currently muted, ignoring mute
// windows that already ended so an expired mute never restricts a member.
func MutedNow(membership *Membership, now time.Time) bool {
	if membership == nil || membership.MutedUntil == nil {
		return false
	}
	return now.Before(*membership.MutedUntil)
}
