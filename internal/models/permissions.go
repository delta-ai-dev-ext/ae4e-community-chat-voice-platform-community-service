package models

// Permission is one of the ten closed-set authorization keys a role may grant.
type Permission string

// The closed permission set from docs/user_stories.md.
const (
	PermissionManageCommunity Permission = "manage_community"
	PermissionManageChannels  Permission = "manage_channels"
	PermissionManageRoles     Permission = "manage_roles"
	PermissionCreateInvites   Permission = "create_invites"
	PermissionMuteMembers     Permission = "mute_members"
	PermissionKickMembers     Permission = "kick_members"
	PermissionBanMembers      Permission = "ban_members"
	PermissionManageAutomod   Permission = "manage_automod"
	PermissionViewAuditLog    Permission = "view_audit_log"
	PermissionReviewAppeals   Permission = "review_appeals"
)

// AllPermissions is the closed permission set in specification order; the
// order is used to emit deterministic, de-duplicated permission lists.
var AllPermissions = []Permission{
	PermissionManageCommunity,
	PermissionManageChannels,
	PermissionManageRoles,
	PermissionCreateInvites,
	PermissionMuteMembers,
	PermissionKickMembers,
	PermissionBanMembers,
	PermissionManageAutomod,
	PermissionViewAuditLog,
	PermissionReviewAppeals,
}

// ValidPermission reports whether key belongs to the closed permission set.
func ValidPermission(key Permission) bool {
	for _, known := range AllPermissions {
		if key == known {
			return true
		}
	}
	return false
}

// InvalidPermissions returns the permission keys that are outside the closed
// set, preserving input order.
func InvalidPermissions(keys []Permission) []Permission {
	var invalid []Permission
	for _, key := range keys {
		if !ValidPermission(key) {
			invalid = append(invalid, key)
		}
	}
	return invalid
}

// HTTP denial reasons returned in-band by the internal oracles.
const (
	DenialActorNotMember      = "actor_not_member"
	DenialMissingPermission   = "missing_permission"
	DenialTargetOutranksActor = "target_outranks_actor"
	DenialTargetIsOwner       = "target_is_owner"
	DenialTargetIsSelf        = "target_is_self"
	DenialNotAMember          = "not_a_member"
	DenialBanned              = "banned"
	DenialChannelNotFound     = "channel_not_found"
	DenialNotAVoiceChannel    = "not_a_voice_channel"
)

// Invite invalidity reasons reported by preview and accept.
const (
	InviteInvalidExpired   = "invite_expired"
	InviteInvalidExhausted = "invite_exhausted"
	InviteInvalidRevoked   = "invite_revoked"
)
