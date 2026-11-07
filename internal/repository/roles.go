package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"community_service/internal/models"
)

const roleSelect = `
SELECT id, community_id, name, permissions, position, created_at, updated_at
FROM roles`

// roleConflictErrors maps role constraints onto documented conflicts; a role
// position must stay unique per community because position is the rank order.
var roleConflictErrors = map[string]*models.Error{
	"roles_community_id_name_key":     models.ErrRoleNameConflict,
	"roles_community_id_position_key": models.ErrRolePositionConflict,
}

// scanRole reads one role row.
func scanRole(row pgx.Row) (*models.Role, error) {
	role := &models.Role{}
	var roleID, communityID pgUUID
	var permissions []string

	if err := row.Scan(&roleID, &communityID, &role.Name, &permissions, &role.Position, &role.CreatedAt, &role.UpdatedAt); err != nil {
		return nil, err
	}

	role.ID = roleID.UUID
	role.CommunityID = communityID.UUID
	role.Permissions = make([]models.Permission, 0, len(permissions))
	for _, permission := range permissions {
		role.Permissions = append(role.Permissions, models.Permission(permission))
	}
	return role, nil
}

// ListRoles returns the roles of a community ordered by rank.
func (s *Store) ListRoles(ctx context.Context, communityID uuid.UUID) ([]*models.Role, error) {
	rows, err := s.pool.Query(ctx, roleSelect+` WHERE community_id = $1 ORDER BY position, name`, id(communityID))
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	defer rows.Close()

	roles := make([]*models.Role, 0, 8)
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			return nil, fmt.Errorf("scan role: %w", err)
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate roles: %w", err)
	}
	return roles, nil
}

// GetRole loads one role of a community.
func (s *Store) GetRole(ctx context.Context, communityID, roleID uuid.UUID) (*models.Role, error) {
	role, err := scanRole(s.pool.QueryRow(ctx, roleSelect+` WHERE community_id = $1 AND id = $2`, id(communityID), id(roleID)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrRoleNotFound
		}
		return nil, fmt.Errorf("load role: %w", err)
	}
	return role, nil
}

// CreateRole inserts a role.
func (s *Store) CreateRole(ctx context.Context, role *models.Role) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO roles (id, community_id, name, permissions, position, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id(role.ID), id(role.CommunityID), role.Name, permissionsToStrings(role.Permissions),
		role.Position, role.CreatedAt, role.UpdatedAt,
	)
	if err != nil {
		if mapped := mapConstraintError(err, roleConflictErrors); mapped != err {
			return mapped
		}
		return fmt.Errorf("insert role: %w", err)
	}
	return nil
}

// UpdateRole applies a partial update and returns the affected member snapshots
// so the service can emit `roles_changed` per member.
func (s *Store) UpdateRole(ctx context.Context, communityID, roleID uuid.UUID, patch models.RolePatch, now time.Time) (*models.Role, []*models.Membership, error) {
	var (
		updated  *models.Role
		affected []*models.Membership
	)

	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var permissions []string
		if patch.Permissions != nil {
			permissions = permissionsToStrings(*patch.Permissions)
		}

		role, err := scanRole(tx.QueryRow(ctx, `
			UPDATE roles SET
				name = CASE WHEN $3::boolean THEN $4::varchar ELSE name END,
				permissions = CASE WHEN $5::boolean THEN $6::text[] ELSE permissions END,
				position = CASE WHEN $7::boolean THEN $8::integer ELSE position END,
				updated_at = $9
			WHERE community_id = $1 AND id = $2
			RETURNING id, community_id, name, permissions, position, created_at, updated_at`,
			id(communityID), id(roleID),
			patch.Name != nil, patch.Name,
			patch.Permissions != nil, permissions,
			patch.Position != nil, patch.Position,
			now,
		))
		if err != nil {
			if mapped := mapConstraintError(err, roleConflictErrors); mapped != err {
				return mapped
			}
			if errors.Is(err, pgx.ErrNoRows) {
				return models.ErrRoleNotFound
			}
			return fmt.Errorf("update role: %w", err)
		}
		updated = role

		affected, err = s.bumpRoleHolders(ctx, tx, communityID, roleID)
		return err
	})
	if err != nil {
		return nil, nil, err
	}

	return updated, affected, nil
}

// DeleteRole deletes a role, its assignments and returns the affected member
// snapshots so the service can emit `roles_changed` per member.
func (s *Store) DeleteRole(ctx context.Context, communityID, roleID uuid.UUID) (*models.Role, []*models.Membership, error) {
	var (
		deleted  *models.Role
		affected []*models.Membership
	)

	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		role, err := scanRole(tx.QueryRow(ctx,
			roleSelect+` WHERE community_id = $1 AND id = $2 FOR UPDATE`,
			id(communityID), id(roleID)))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return models.ErrRoleNotFound
			}
			return fmt.Errorf("load role: %w", err)
		}
		deleted = role

		if _, err := tx.Exec(ctx, `DELETE FROM roles WHERE community_id = $1 AND id = $2`, id(communityID), id(roleID)); err != nil {
			return fmt.Errorf("delete role: %w", err)
		}

		affected, err = s.bumpRoleHolders(ctx, tx, communityID, roleID)
		return err
	})
	if err != nil {
		return nil, nil, err
	}

	return deleted, affected, nil
}

// bumpRoleHolders increments member_version for every active member holding the
// role and returns their post-change snapshots. The role id no longer resolves
// after a delete, so the caller passes the member ids that held it.
func (s *Store) bumpRoleHolders(ctx context.Context, tx pgx.Tx, communityID, roleID uuid.UUID) ([]*models.Membership, error) {
	rows, err := tx.Query(ctx, `
		UPDATE memberships SET member_version = member_version + 1
		WHERE community_id = $1 AND status = 'active'
		  AND user_id IN (SELECT user_id FROM member_roles WHERE community_id = $1 AND role_id = $2)
		RETURNING user_id`,
		id(communityID), id(roleID))
	if err != nil {
		return nil, fmt.Errorf("bump role holders: %w", err)
	}

	var userIDs []pgUUID
	for rows.Next() {
		var userID pgUUID
		if err := rows.Scan(&userID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan role holder: %w", err)
		}
		userIDs = append(userIDs, userID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate role holders: %w", err)
	}

	affected := make([]*models.Membership, 0, len(userIDs))
	for _, userID := range userIDs {
		membership, err := scanMembership(tx.QueryRow(ctx,
			membershipSelect+` WHERE m.community_id = $1 AND m.user_id = $2`, id(communityID), userID))
		if err != nil {
			return nil, fmt.Errorf("reload role holder: %w", err)
		}
		affected = append(affected, membership)
	}
	return affected, nil
}

// SetMemberRoles replaces the member's role set and bumps member_version in one
// transaction.
func (s *Store) SetMemberRoles(ctx context.Context, communityID, userID uuid.UUID, roleIDs []uuid.UUID, now time.Time) (*models.Membership, error) {
	var updated *models.Membership

	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM memberships WHERE community_id = $1 AND user_id = $2 AND status = 'active'
			)`, id(communityID), id(userID)).Scan(&exists); err != nil {
			return fmt.Errorf("check membership: %w", err)
		}
		if !exists {
			return models.ErrMemberNotFound
		}

		if _, err := tx.Exec(ctx, `DELETE FROM member_roles WHERE community_id = $1 AND user_id = $2`, id(communityID), id(userID)); err != nil {
			return fmt.Errorf("clear member roles: %w", err)
		}

		for _, roleID := range roleIDs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO member_roles (community_id, user_id, role_id, assigned_at)
				VALUES ($1, $2, $3, $4)`,
				id(communityID), id(userID), id(roleID), now); err != nil {
				return fmt.Errorf("assign role: %w", err)
			}
		}

		tag, err := tx.Exec(ctx, `
			UPDATE memberships SET member_version = member_version + 1
			WHERE community_id = $1 AND user_id = $2`,
			id(communityID), id(userID))
		if err != nil {
			return fmt.Errorf("bump member version: %w", err)
		}
		if err := requireRow(tag, models.ErrMemberNotFound); err != nil {
			return err
		}

		updated, err = scanMembership(tx.QueryRow(ctx,
			membershipSelect+` WHERE m.community_id = $1 AND m.user_id = $2`, id(communityID), id(userID)))
		if err != nil {
			return fmt.Errorf("reload membership: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// permissionsToStrings converts the closed permission set into text[] values.
func permissionsToStrings(values []models.Permission) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, string(value))
	}
	return result
}
