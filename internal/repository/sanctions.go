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

// ApplySanctionApplication applies one consumed EVT-004 in a single transaction:
// the processed-event record (the deduplication key), the membership state
// change, the ban-list change and the member_version bump either all commit or
// none of them do.
func (s *Store) ApplySanctionApplication(ctx context.Context, application *models.SanctionApplication) (*models.Membership, error) {
	var membership *models.Membership

	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO processed_events (event_id, event_type, processed_at)
			VALUES ($1, $2, $3)
			ON CONFLICT (event_id) DO NOTHING`,
			id(application.EventID), application.EventType, application.OccurredAt)
		if err != nil {
			return fmt.Errorf("record processed event: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return models.ErrDuplicateEvent
		}

		if application.TouchesMembership() {
			// The optimistic member_version predicate is the last-writer-wins
			// guard: a stale application matches no row and is dropped instead
			// of overwriting a newer state.
			tag, err := tx.Exec(ctx, `
				UPDATE memberships SET
					status = $3::varchar,
					left_at = CASE WHEN $3::varchar = 'removed' AND status = 'active' THEN $4::timestamptz ELSE left_at END,
					muted_until = CASE WHEN $5::boolean THEN $6::timestamptz ELSE muted_until END,
					member_version = member_version + 1
				WHERE community_id = $1 AND user_id = $2
				  AND ($7::bigint = 0 OR member_version = $7::bigint)`,
				id(application.CommunityID), id(application.UserID),
				application.Status,
				application.OccurredAt,
				application.MutedUntilWritten, application.MutedUntil,
				application.ExpectedMemberVersion,
			)
			if err != nil {
				return fmt.Errorf("apply member state: %w", err)
			}
			if tag.RowsAffected() == 0 {
				return models.ErrStaleVersion
			}
		}

		switch application.BanChange {
		case models.BanChangeRecord:
			if _, err := tx.Exec(ctx, `
				INSERT INTO community_bans (id, community_id, user_id, reason, banned_at)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT DO NOTHING`,
				id(uuid.New()), id(application.CommunityID), id(application.UserID),
				application.BanReason, application.OccurredAt); err != nil {
				return fmt.Errorf("record ban: %w", err)
			}
		case models.BanChangeClear:
			if _, err := tx.Exec(ctx, `
				UPDATE community_bans SET lifted_at = $3
				WHERE community_id = $1 AND user_id = $2 AND lifted_at IS NULL`,
				id(application.CommunityID), id(application.UserID), application.OccurredAt); err != nil {
				return fmt.Errorf("clear ban: %w", err)
			}
		}

		membership, err = scanMembership(tx.QueryRow(ctx,
			membershipSelect+` WHERE m.community_id = $1 AND m.user_id = $2`,
			id(application.CommunityID), id(application.UserID)))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// A ban of a user who was never a member records only the ban.
				membership = nil
				return nil
			}
			return fmt.Errorf("reload membership: %w", err)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return membership, nil
}

// MarkEventProcessed records an event that required no state change, so a
// redelivery is skipped instead of re-evaluated.
func (s *Store) MarkEventProcessed(ctx context.Context, eventID uuid.UUID, eventType string, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO processed_events (event_id, event_type, processed_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (event_id) DO NOTHING`,
		id(eventID), eventType, now)
	if err != nil {
		return fmt.Errorf("record processed event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return models.ErrDuplicateEvent
	}
	return nil
}
