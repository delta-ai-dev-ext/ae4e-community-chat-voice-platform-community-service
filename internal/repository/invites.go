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

const inviteSelect = `
SELECT id, community_id, code, created_by, created_at, expires_at, max_uses, use_count, revoked_at
FROM invites`

// scanInvite reads one invite row.
func scanInvite(row pgx.Row) (*models.Invite, error) {
	invite := &models.Invite{}
	var inviteID, communityID, createdBy pgUUID

	if err := row.Scan(&inviteID, &communityID, &invite.Code, &createdBy, &invite.CreatedAt,
		&invite.ExpiresAt, &invite.MaxUses, &invite.UseCount, &invite.RevokedAt); err != nil {
		return nil, err
	}

	invite.ID = inviteID.UUID
	invite.CommunityID = communityID.UUID
	invite.CreatedBy = createdBy.UUID
	return invite, nil
}

// CreateInvite inserts an invite code.
func (s *Store) CreateInvite(ctx context.Context, invite *models.Invite) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO invites (id, community_id, code, created_by, expires_at, max_uses, use_count, revoked_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id(invite.ID), id(invite.CommunityID), invite.Code, id(invite.CreatedBy),
		invite.ExpiresAt, invite.MaxUses, invite.UseCount, invite.RevokedAt, invite.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert invite: %w", err)
	}
	return nil
}

// GetInviteByCode loads an invite by its code.
func (s *Store) GetInviteByCode(ctx context.Context, code string) (*models.Invite, error) {
	invite, err := scanInvite(s.pool.QueryRow(ctx, inviteSelect+` WHERE code = $1`, code))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrInviteNotFound
		}
		return nil, fmt.Errorf("load invite: %w", err)
	}
	return invite, nil
}

// RevokeInvite revokes an invite idempotently, keeping the first revocation
// timestamp so a retried revoke does not rewrite history.
func (s *Store) RevokeInvite(ctx context.Context, communityID uuid.UUID, code string, now time.Time) (*models.Invite, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE invites SET revoked_at = COALESCE(revoked_at, $3)
		WHERE community_id = $1 AND code = $2
		RETURNING id, community_id, code, created_by, created_at, expires_at, max_uses, use_count, revoked_at`,
		id(communityID), code, now)

	invite, err := scanInvite(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrInviteNotFound
		}
		return nil, fmt.Errorf("revoke invite: %w", err)
	}
	return invite, nil
}

// JoinWithInvite performs the accept flow in one transaction: it locks the
// invite row, re-checks usability and the ban list under the lock, then inserts
// or reactivates the membership and increments use_count. The in-transaction
// re-check keeps two concurrent accepts from exceeding max_uses.
func (s *Store) JoinWithInvite(ctx context.Context, communityID, userID, membershipID uuid.UUID, code string, now time.Time) (*models.Membership, bool, error) {
	var (
		membership *models.Membership
		changed    bool
	)

	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		invite, err := scanInvite(tx.QueryRow(ctx, inviteSelect+` WHERE code = $1 FOR UPDATE`, code))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return models.ErrInviteNotFound
			}
			return fmt.Errorf("load invite: %w", err)
		}
		if invite.CommunityID != communityID {
			return models.ErrInviteNotFound
		}
		if reason := models.CheckInvite(invite, now); reason != "" {
			return models.ErrInviteExpired
		}

		var banned bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM community_bans
				WHERE community_id = $1 AND user_id = $2 AND lifted_at IS NULL
			)`, id(communityID), id(userID)).Scan(&banned); err != nil {
			return fmt.Errorf("check ban: %w", err)
		}
		if banned {
			return models.ErrBanned
		}

		existing, err := scanMembership(tx.QueryRow(ctx,
			membershipSelect+` WHERE m.community_id = $1 AND m.user_id = $2 FOR UPDATE`,
			id(communityID), id(userID)))
		switch {
		case err == nil:
			if existing.IsActive() {
				// Idempotent re-accept: return the current membership without
				// consuming another use of the invite.
				membership, changed = existing, false
				return nil
			}
			if _, err := tx.Exec(ctx, `
				UPDATE memberships
				SET status = 'active', joined_at = $3, left_at = NULL, member_version = member_version + 1
				WHERE community_id = $1 AND user_id = $2`,
				id(communityID), id(userID), now); err != nil {
				return fmt.Errorf("reactivate membership: %w", err)
			}
			changed = true
		case errors.Is(err, pgx.ErrNoRows):
			if _, err := tx.Exec(ctx, `
				INSERT INTO memberships (id, community_id, user_id, status, joined_at, member_version)
				VALUES ($1, $2, $3, 'active', $4, 1)`,
				id(membershipID), id(communityID), id(userID), now); err != nil {
				return fmt.Errorf("insert membership: %w", err)
			}
			changed = true
		default:
			return fmt.Errorf("load membership: %w", err)
		}

		if changed {
			if _, err := tx.Exec(ctx, `UPDATE invites SET use_count = use_count + 1 WHERE id = $1`, id(invite.ID)); err != nil {
				return fmt.Errorf("increment invite use count: %w", err)
			}
		}

		membership, err = scanMembership(tx.QueryRow(ctx,
			membershipSelect+` WHERE m.community_id = $1 AND m.user_id = $2`, id(communityID), id(userID)))
		if err != nil {
			return fmt.Errorf("reload membership: %w", err)
		}
		return nil
	})

	if err != nil {
		return nil, false, err
	}
	return membership, changed, nil
}
