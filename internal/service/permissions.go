package service

import (
	"time"

	"github.com/google/uuid"

	"community_service/internal/models"
)

// EffectivePermissions computes the effective permission set of a member: the
// union of every permission granted by their roles, plus all ten keys when the
// member is the community owner (owner supremacy is implicit, never a role).
// The result is deterministic and de-duplicated in specification order.
func EffectivePermissions(isOwner bool, roles []*models.Role) []models.Permission {
	if isOwner {
		return append([]models.Permission{}, models.AllPermissions...)
	}

	granted := make(map[models.Permission]struct{}, len(models.AllPermissions))
	for _, role := range roles {
		if role == nil {
			continue
		}
		for _, permission := range role.Permissions {
			if models.ValidPermission(permission) {
				granted[permission] = struct{}{}
			}
		}
	}

	effective := make([]models.Permission, 0, len(granted))
	for _, permission := range models.AllPermissions {
		if _, ok := granted[permission]; ok {
			effective = append(effective, permission)
		}
	}
	return effective
}

// HasPermission reports whether the member holds want through ownership or
// through one of their roles.
func HasPermission(isOwner bool, roles []*models.Role, want models.Permission) bool {
	if isOwner {
		return true
	}
	for _, permission := range EffectivePermissions(false, roles) {
		if permission == want {
			return true
		}
	}
	return false
}

// RankPosition returns the member's highest rank: the lowest role position they
// hold. The boolean is false when the member holds no role at all.
func RankPosition(roles []*models.Role) (int, bool) {
	highest, found := 0, false
	for _, role := range roles {
		if role == nil {
			continue
		}
		if !found || role.Position < highest {
			highest, found = role.Position, true
		}
	}
	return highest, found
}

// Outranks reports whether the actor ranks strictly above the target. A lower
// position ranks higher, the owner outranks every role, nothing outranks the
// owner, and a member without roles outranks nobody but is outranked by every
// role holder.
func Outranks(actorOwner bool, actorRoles []*models.Role, targetOwner bool, targetRoles []*models.Role) bool {
	if targetOwner {
		return false
	}
	if actorOwner {
		return true
	}

	actorPosition, actorRanked := RankPosition(actorRoles)
	targetPosition, targetRanked := RankPosition(targetRoles)

	switch {
	case !actorRanked:
		return false
	case !targetRanked:
		return true
	default:
		return actorPosition < targetPosition
	}
}

// AuthorizationTarget is the target snapshot the authorization decision needs.
type AuthorizationTarget struct {
	UserID     uuid.UUID
	IsMember   bool
	IsBanned   bool
	IsOwner    bool
	Roles      []*models.Role
	MutedUntil *time.Time
}

// AuthorizationInput is everything the authorization oracle needs to decide one
// check, assembled from the repository by the service.
type AuthorizationInput struct {
	ActorUserID   uuid.UUID
	ActorIsMember bool
	ActorIsOwner  bool
	ActorRoles    []*models.Role
	Permission    models.Permission
	Target        *AuthorizationTarget
}

// DecideAuthorization returns whether the check is allowed and, when it is not,
// the documented denial reason. The order matters: membership, then permission,
// then hierarchy, so a non-member never learns hierarchy information.
func DecideAuthorization(in AuthorizationInput) (allowed bool, denialReason *string) {
	deny := func(reason string) (bool, *string) { return false, pointer(reason) }

	if !in.ActorIsMember && !in.ActorIsOwner {
		return deny(models.DenialActorNotMember)
	}
	if !HasPermission(in.ActorIsOwner, in.ActorRoles, in.Permission) {
		return deny(models.DenialMissingPermission)
	}
	if in.Target == nil {
		return true, nil
	}
	if in.ActorUserID == in.Target.UserID {
		return deny(models.DenialTargetIsSelf)
	}
	if in.Target.IsOwner {
		return deny(models.DenialTargetIsOwner)
	}
	if !Outranks(in.ActorIsOwner, in.ActorRoles, in.Target.IsOwner, in.Target.Roles) {
		return deny(models.DenialTargetOutranksActor)
	}
	return true, nil
}

func pointer[T any](value T) *T { return &value }
