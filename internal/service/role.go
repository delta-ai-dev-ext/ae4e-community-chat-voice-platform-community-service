package service

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"community_service/internal/models"
)

// RoleService owns role configuration and role assignment.
type RoleService struct {
	deps Deps
}

// NewRoleService builds the role service.
func NewRoleService(deps Deps) *RoleService {
	return &RoleService{deps: deps.withDefaults()}
}

// List returns the community's roles ordered by rank; it requires an active
// membership.
func (s *RoleService) List(ctx context.Context, userID, communityID uuid.UUID) (*models.RoleListResponse, error) {
	resolved, err := s.deps.resolve(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}
	if err := s.deps.requireMember(userID, resolved); err != nil {
		return nil, err
	}

	roles, err := s.deps.Roles.ListRoles(ctx, communityID)
	if err != nil {
		return nil, err
	}
	return &models.RoleListResponse{Roles: roles}, nil
}

// Create defines a role; it requires `manage_roles`.
func (s *RoleService) Create(ctx context.Context, userID, communityID uuid.UUID, request *models.CreateRoleRequest) (*models.Role, error) {
	resolved, err := s.deps.resolve(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}
	if err := s.deps.requirePermission(userID, resolved, models.PermissionManageRoles); err != nil {
		return nil, err
	}

	now := s.deps.Clock.Now()
	role := &models.Role{
		ID:          uuid.New(),
		CommunityID: communityID,
		Name:        strings.TrimSpace(request.Name),
		Permissions: toPermissions(request.Permissions),
		Position:    *request.Position,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.deps.Roles.CreateRole(ctx, role); err != nil {
		return nil, err
	}
	return role, nil
}

// Update renames, re-permissions or re-ranks a role and emits `roles_changed` for
// every active member holding it, because their effective permissions or rank
// may have changed.
func (s *RoleService) Update(ctx context.Context, userID, communityID, roleID uuid.UUID, traceID, correlationID string, request *models.UpdateRoleRequest) (*models.Role, error) {
	resolved, err := s.deps.resolve(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}
	if err := s.deps.requirePermission(userID, resolved, models.PermissionManageRoles); err != nil {
		return nil, err
	}

	patch := models.RolePatch{
		Position: request.Position,
	}
	if request.Name != nil {
		trimmed := strings.TrimSpace(*request.Name)
		patch.Name = &trimmed
	}
	if request.Permissions != nil {
		permissions := toPermissions(*request.Permissions)
		patch.Permissions = &permissions
	}

	now := s.deps.Clock.Now()
	role, affected, err := s.deps.Roles.UpdateRole(ctx, communityID, roleID, patch, now)
	if err != nil {
		return nil, err
	}

	s.publishRolesChanged(ctx, traceID, correlationID, affected, now)
	return role, nil
}

// Delete removes a role and its assignments, emitting `roles_changed` for every
// affected member.
func (s *RoleService) Delete(ctx context.Context, userID, communityID, roleID uuid.UUID, traceID, correlationID string) error {
	resolved, err := s.deps.resolve(ctx, communityID, userID)
	if err != nil {
		return err
	}
	if err := s.deps.requirePermission(userID, resolved, models.PermissionManageRoles); err != nil {
		return err
	}

	_, affected, err := s.deps.Roles.DeleteRole(ctx, communityID, roleID)
	if err != nil {
		return err
	}

	s.publishRolesChanged(ctx, traceID, correlationID, affected, s.deps.Clock.Now())
	return nil
}

// SetMemberRoles replaces a member's complete role set. Hierarchy is enforced
// before the write: an actor cannot target themselves, cannot modify the owner,
// and cannot modify a member who outranks them.
func (s *RoleService) SetMemberRoles(ctx context.Context, actorID, communityID, targetUserID uuid.UUID, traceID, correlationID string, request *models.SetMemberRolesRequest) (*models.Member, error) {
	resolved, err := s.deps.resolve(ctx, communityID, actorID)
	if err != nil {
		return nil, err
	}
	if err := s.deps.requirePermission(actorID, resolved, models.PermissionManageRoles); err != nil {
		return nil, err
	}

	if actorID == targetUserID {
		return nil, models.ErrTargetIsSelf
	}

	if targetUserID == resolved.Community.OwnerUserID {
		return nil, models.ErrTargetIsOwner
	}

	target, err := s.deps.Memberships.GetMembership(ctx, communityID, targetUserID)
	if err != nil {
		if err == models.ErrMembershipNotFound || err == models.ErrMemberNotFound {
			return nil, models.ErrMemberNotFound
		}
		return nil, err
	}
	if !target.IsActive() {
		return nil, models.ErrMemberNotFound
	}

	targetRoles, err := s.deps.roleSet(ctx, communityID, target.RoleIDs)
	if err != nil {
		return nil, err
	}
	if !Outranks(resolved.isOwner(actorID), resolved.Roles, false, targetRoles) {
		return nil, models.ErrTargetOutranksActor
	}

	if err := s.validateRoleIDs(ctx, communityID, request.RoleIDs); err != nil {
		return nil, err
	}

	now := s.deps.Clock.Now()
	updated, err := s.deps.Roles.SetMemberRoles(ctx, communityID, targetUserID, request.RoleIDs, now)
	if err != nil {
		return nil, err
	}

	s.deps.publishMemberChanged(ctx, memberEnvelope(traceID, correlationID, nil, now), models.ChangeRolesChanged, updated, nil)
	s.deps.Metrics.RecordRoleAssignments()

	member := updated.AsMember()
	return &member, nil
}

// validateRoleIDs rejects role ids that do not belong to the community.
func (s *RoleService) validateRoleIDs(ctx context.Context, communityID uuid.UUID, roleIDs []uuid.UUID) error {
	if len(roleIDs) == 0 {
		return nil
	}

	roles, err := s.deps.Roles.ListRoles(ctx, communityID)
	if err != nil {
		return err
	}

	known := make(map[uuid.UUID]struct{}, len(roles))
	for _, role := range roles {
		known[role.ID] = struct{}{}
	}
	for _, roleID := range roleIDs {
		if _, ok := known[roleID]; !ok {
			return models.WithDetail(models.ErrValidationFailed, "role_ids must reference roles of this community")
		}
	}
	return nil
}

func (s *RoleService) publishRolesChanged(ctx context.Context, traceID, correlationID string, affected []*models.Membership, now time.Time) {
	for _, membership := range affected {
		s.deps.publishMemberChanged(ctx, memberEnvelope(traceID, correlationID, nil, now), models.ChangeRolesChanged, membership, nil)
	}
}

// toPermissions converts wire permission keys into the domain type.
func toPermissions(values []string) []models.Permission {
	permissions := make([]models.Permission, 0, len(values))
	for _, value := range values {
		permissions = append(permissions, models.Permission(value))
	}
	return permissions
}
