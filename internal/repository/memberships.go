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

// membershipSelect reads the authoritative member state together with the
// member's role ids and active ban flag, so one query answers every authorization
// and projection question.
const membershipSelect = `
SELECT m.id, m.community_id, m.user_id, m.status, m.joined_at, m.left_at, m.muted_until, m.member_version,
       COALESCE((
           SELECT array_agg(mr.role_id ORDER BY mr.assigned_at, mr.role_id)
           FROM member_roles mr
           WHERE mr.community_id = m.community_id AND mr.user_id = m.user_id
       ), '{}'::uuid[]) AS role_ids,
       EXISTS (
           SELECT 1 FROM community_bans b
           WHERE b.community_id = m.community_id AND b.user_id = m.user_id AND b.lifted_at IS NULL
       ) AS is_banned
FROM memberships m`

// scanMembership reads one membership row including roles and ban state.
func scanMembership(row pgx.Row) (*models.Membership, error) {
	membership := &models.Membership{}
	var membershipID, communityID, userID pgUUID
	var roleIDs []pgUUID

	if err := row.Scan(&membershipID, &communityID, &userID, &membership.Status, &membership.JoinedAt,
		&membership.LeftAt, &membership.MutedUntil, &membership.MemberVersion, &roleIDs, &membership.IsBanned); err != nil {
		return nil, err
	}

	membership.ID = membershipID.UUID
	membership.CommunityID = communityID.UUID
	membership.UserID = userID.UUID
	membership.RoleIDs = toUUIDs(roleIDs)
	return membership, nil
}

// GetMembership loads the full member state of one user in one community.
func (s *Store) GetMembership(ctx context.Context, communityID, userID uuid.UUID) (*models.Membership, error) {
	membership, err := scanMembership(s.pool.QueryRow(ctx,
		membershipSelect+` WHERE m.community_id = $1 AND m.user_id = $2`,
		id(communityID), id(userID)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrMembershipNotFound
		}
		return nil, fmt.Errorf("load membership: %w", err)
	}
	return membership, nil
}

// ListMembers returns the active members of a community in join order.
func (s *Store) ListMembers(ctx context.Context, communityID uuid.UUID, limit, offset int) ([]*models.Membership, error) {
	rows, err := s.pool.Query(ctx,
		membershipSelect+` WHERE m.community_id = $1 AND m.status = 'active' ORDER BY m.joined_at, m.user_id LIMIT $2 OFFSET $3`,
		id(communityID), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()

	members := make([]*models.Membership, 0, limit)
	for rows.Next() {
		membership, err := scanMembership(rows)
		if err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		members = append(members, membership)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate members: %w", err)
	}
	return members, nil
}

// RemoveMembership marks an active membership as removed and bumps
// member_version.
func (s *Store) RemoveMembership(ctx context.Context, communityID, userID uuid.UUID, now time.Time) (*models.Membership, error) {
	var removed *models.Membership

	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE memberships
			SET status = 'removed', left_at = $3, member_version = member_version + 1
			WHERE community_id = $1 AND user_id = $2 AND status = 'active'`,
			id(communityID), id(userID), now)
		if err != nil {
			return fmt.Errorf("remove membership: %w", err)
		}
		if err := requireRow(tag, models.ErrMembershipNotFound); err != nil {
			return err
		}

		removed, err = scanMembership(tx.QueryRow(ctx,
			membershipSelect+` WHERE m.community_id = $1 AND m.user_id = $2`,
			id(communityID), id(userID)))
		if err != nil {
			return fmt.Errorf("reload membership: %w", err)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return removed, nil
}

// GetActiveBan returns the active ban of a user in a community, or nil when the
// user is not banned.
func (s *Store) GetActiveBan(ctx context.Context, communityID, userID uuid.UUID) (*models.Ban, error) {
	ban := &models.Ban{}
	var banID, banCommunityID, banUserID pgUUID

	err := s.pool.QueryRow(ctx, `
		SELECT id, community_id, user_id, reason, banned_at, lifted_at
		FROM community_bans
		WHERE community_id = $1 AND user_id = $2 AND lifted_at IS NULL`,
		id(communityID), id(userID),
	).Scan(&banID, &banCommunityID, &banUserID, &ban.Reason, &ban.BannedAt, &ban.LiftedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("load ban: %w", err)
	}

	ban.ID = banID.UUID
	ban.CommunityID = banCommunityID.UUID
	ban.UserID = banUserID.UUID
	return ban, nil
}
