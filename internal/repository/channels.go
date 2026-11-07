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

const channelSelect = `
SELECT id, community_id, kind, name, position, capacity, channel_version, created_at, updated_at
FROM channels`

// channelConflictErrors maps channel constraints onto documented conflicts.
var channelConflictErrors = map[string]*models.Error{
	"channels_community_id_name_key": models.ErrChannelNameConflict,
}

// scanChannel reads one channel row.
func scanChannel(row pgx.Row) (*models.Channel, error) {
	channel := &models.Channel{}
	var channelID, communityID pgUUID

	if err := row.Scan(&channelID, &communityID, &channel.Kind, &channel.Name, &channel.Position, &channel.Capacity, &channel.ChannelVersion, &channel.CreatedAt, &channel.UpdatedAt); err != nil {
		return nil, err
	}

	channel.ID = channelID.UUID
	channel.CommunityID = communityID.UUID
	return channel, nil
}

// insertChannel inserts one channel, resolving the append position inside the
// transaction so two concurrent creates cannot pick the same position.
func insertChannel(ctx context.Context, tx pgx.Tx, channel *models.Channel) error {
	if channel.Position < 0 {
		var position int
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(position) + 1, 0) FROM channels WHERE community_id = $1`,
			id(channel.CommunityID),
		).Scan(&position); err != nil {
			return fmt.Errorf("compute channel position: %w", err)
		}
		channel.Position = position
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO channels (id, community_id, kind, name, position, capacity, channel_version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id(channel.ID), id(channel.CommunityID), string(channel.Kind), channel.Name,
		channel.Position, channel.Capacity, channel.ChannelVersion, channel.CreatedAt, channel.UpdatedAt,
	)
	if err != nil {
		if mapped := mapConstraintError(err, channelConflictErrors); mapped != err {
			return mapped
		}
		return fmt.Errorf("insert channel: %w", err)
	}
	return nil
}

// ListChannels returns every channel of a community in display order.
func (s *Store) ListChannels(ctx context.Context, communityID uuid.UUID) ([]*models.Channel, error) {
	return s.listChannels(ctx, communityID, "")
}

// ListVoiceChannels returns the voice channels of a community in display order.
func (s *Store) ListVoiceChannels(ctx context.Context, communityID uuid.UUID) ([]*models.Channel, error) {
	return s.listChannels(ctx, communityID, ` AND kind = 'voice'`)
}

func (s *Store) listChannels(ctx context.Context, communityID uuid.UUID, filter string) ([]*models.Channel, error) {
	rows, err := s.pool.Query(ctx, channelSelect+` WHERE community_id = $1`+filter+` ORDER BY position, name, id`, id(communityID))
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	defer rows.Close()

	channels := make([]*models.Channel, 0, 8)
	for rows.Next() {
		channel, err := scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("scan channel: %w", err)
		}
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate channels: %w", err)
	}
	return channels, nil
}

// GetChannel loads one channel of a community.
func (s *Store) GetChannel(ctx context.Context, communityID, channelID uuid.UUID) (*models.Channel, error) {
	channel, err := scanChannel(s.pool.QueryRow(ctx, channelSelect+` WHERE community_id = $1 AND id = $2`, id(communityID), id(channelID)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrChannelNotFound
		}
		return nil, fmt.Errorf("load channel: %w", err)
	}
	return channel, nil
}

// CountChannels counts the channels of a community.
func (s *Store) CountChannels(ctx context.Context, communityID uuid.UUID) (int, error) {
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM channels WHERE community_id = $1`, id(communityID)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count channels: %w", err)
	}
	return count, nil
}

// CreateChannel inserts a channel with channel_version 1.
func (s *Store) CreateChannel(ctx context.Context, channel *models.Channel) error {
	return s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return insertChannel(ctx, tx, channel)
	})
}

// UpdateChannel applies a partial update and bumps channel_version.
func (s *Store) UpdateChannel(ctx context.Context, communityID, channelID uuid.UUID, patch models.ChannelPatch, now time.Time) (*models.Channel, error) {
	channel, err := scanChannel(s.pool.QueryRow(ctx, `
		UPDATE channels SET
			name = CASE WHEN $3::boolean THEN $4::varchar ELSE name END,
			position = CASE WHEN $5::boolean THEN $6::integer ELSE position END,
			capacity = CASE WHEN $7::boolean THEN $8::integer ELSE capacity END,
			channel_version = channel_version + 1,
			updated_at = $9
		WHERE community_id = $1 AND id = $2
		RETURNING id, community_id, kind, name, position, capacity, channel_version, created_at, updated_at`,
		id(communityID), id(channelID),
		patch.Name != nil, patch.Name,
		patch.Position != nil, patch.Position,
		patch.Capacity.Set, patch.Capacity.Value,
		now,
	))
	if err != nil {
		if mapped := mapConstraintError(err, channelConflictErrors); mapped != err {
			return nil, mapped
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrChannelNotFound
		}
		return nil, fmt.Errorf("update channel: %w", err)
	}
	return channel, nil
}

// DeleteChannel deletes a channel, refusing to remove the last one of a
// community.
func (s *Store) DeleteChannel(ctx context.Context, communityID, channelID uuid.UUID) (*models.Channel, error) {
	var deleted *models.Channel

	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		channel, err := scanChannel(tx.QueryRow(ctx,
			channelSelect+` WHERE community_id = $1 AND id = $2 FOR UPDATE`,
			id(communityID), id(channelID)))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return models.ErrChannelNotFound
			}
			return fmt.Errorf("load channel: %w", err)
		}

		var count int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM channels WHERE community_id = $1`, id(communityID)).Scan(&count); err != nil {
			return fmt.Errorf("count channels: %w", err)
		}
		if count <= 1 {
			return models.ErrLastChannel
		}

		if _, err := tx.Exec(ctx, `DELETE FROM channels WHERE community_id = $1 AND id = $2`, id(communityID), id(channelID)); err != nil {
			return fmt.Errorf("delete channel: %w", err)
		}

		deleted = channel
		return nil
	})

	return deleted, err
}
