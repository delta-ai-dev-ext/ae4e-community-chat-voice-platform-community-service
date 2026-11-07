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

const communitySelect = `
SELECT id, name, description, icon_url, owner_user_id, created_at, updated_at
FROM communities`

// scanCommunity reads one community row.
func scanCommunity(row pgx.Row) (*models.Community, error) {
	community := &models.Community{}
	var communityID, ownerID pgUUID

	if err := row.Scan(&communityID, &community.Name, &community.Description, &community.IconURL, &ownerID, &community.CreatedAt, &community.UpdatedAt); err != nil {
		return nil, err
	}

	community.ID = communityID.UUID
	community.OwnerUserID = ownerID.UUID
	return community, nil
}

// CreateCommunity inserts the community, its owner membership and its default
// channels in one transaction, so a community never exists without an owner
// membership and its default channels.
func (s *Store) CreateCommunity(ctx context.Context, community *models.Community, ownerMembership *models.Membership, defaultChannels []*models.Channel) error {
	return s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO communities (id, name, description, icon_url, owner_user_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			id(community.ID), community.Name, community.Description, community.IconURL,
			id(community.OwnerUserID), community.CreatedAt, community.UpdatedAt,
		); err != nil {
			return fmt.Errorf("insert community: %w", err)
		}

		if ownerMembership != nil {
			if _, err := tx.Exec(ctx, `
				INSERT INTO memberships (id, community_id, user_id, status, joined_at, member_version)
				VALUES ($1, $2, $3, 'active', $4, 1)`,
				id(ownerMembership.ID), id(community.ID), id(community.OwnerUserID), ownerMembership.JoinedAt,
			); err != nil {
				return fmt.Errorf("insert owner membership: %w", err)
			}
		}

		for _, channel := range defaultChannels {
			if err := insertChannel(ctx, tx, channel); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetCommunity loads a community by id.
func (s *Store) GetCommunity(ctx context.Context, communityID uuid.UUID) (*models.Community, error) {
	community, err := scanCommunity(s.pool.QueryRow(ctx, communitySelect+` WHERE id = $1`, id(communityID)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrCommunityNotFound
		}
		return nil, fmt.Errorf("load community: %w", err)
	}
	return community, nil
}

// UpdateCommunity applies a partial update; only provided fields change.
func (s *Store) UpdateCommunity(ctx context.Context, communityID uuid.UUID, patch models.CommunityPatch, now time.Time) (*models.Community, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE communities SET
			name = CASE WHEN $2::boolean THEN $3::varchar ELSE name END,
			description = CASE WHEN $4::boolean THEN $5::varchar ELSE description END,
			icon_url = CASE WHEN $6::boolean THEN $7::text ELSE icon_url END,
			updated_at = $8
		WHERE id = $1
		RETURNING id, name, description, icon_url, owner_user_id, created_at, updated_at`,
		id(communityID),
		patch.Name != nil, patch.Name,
		patch.Description.Set, patch.Description.Value,
		patch.IconURL.Set, patch.IconURL.Value,
		now,
	)

	community, err := scanCommunity(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrCommunityNotFound
		}
		return nil, fmt.Errorf("update community: %w", err)
	}
	return community, nil
}

// ListCommunitiesByMember returns the communities where the user has an active
// membership, newest first.
func (s *Store) ListCommunitiesByMember(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*models.Community, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.name, c.description, c.icon_url, c.owner_user_id, c.created_at, c.updated_at
		FROM communities c
		JOIN memberships m ON m.community_id = c.id
		WHERE m.user_id = $1 AND m.status = 'active'
		ORDER BY c.created_at DESC, c.id DESC
		LIMIT $2 OFFSET $3`,
		id(userID), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list communities: %w", err)
	}
	defer rows.Close()

	communities := make([]*models.Community, 0, limit)
	for rows.Next() {
		community, err := scanCommunity(rows)
		if err != nil {
			return nil, fmt.Errorf("scan community: %w", err)
		}
		communities = append(communities, community)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate communities: %w", err)
	}
	return communities, nil
}

// CountActiveMembers counts the active members of a community.
func (s *Store) CountActiveMembers(ctx context.Context, communityID uuid.UUID) (int, error) {
	var count int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM memberships WHERE community_id = $1 AND status = 'active'`,
		id(communityID),
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active members: %w", err)
	}
	return count, nil
}
