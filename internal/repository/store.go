package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"community_service/internal/models"
)

// postgresUniqueViolation is SQLSTATE 23505.
const postgresUniqueViolation = "23505"

// Store is the pgx-backed implementation of Repository.
type Store struct {
	pool *pgxpool.Pool
}

// Open creates the connection pool described by databaseURL.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	return &Store{pool: pool}, nil
}

// Ping verifies PostgreSQL is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Close releases the connection pool.
func (s *Store) Close() { s.pool.Close() }

// withTx runs fn inside a transaction. The deferred rollback makes an error or a
// panic leave no partial state, which is what keeps the documented data
// invariants atomic.
func (s *Store) withTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := fn(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// mapConstraintError translates a unique violation on one of constraints into
// the documented conflict error, so services never parse driver errors.
func mapConstraintError(err error, constraints map[string]*models.Error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == postgresUniqueViolation {
		if mapped, ok := constraints[pgErr.ConstraintName]; ok {
			return mapped
		}
	}
	return err
}

// requireRow turns "0 rows affected" into the given error.
func requireRow(tag pgconn.CommandTag, want error) error {
	if tag.RowsAffected() == 0 {
		return want
	}
	return nil
}

// toUUIDs converts scanned identifier columns back into plain UUID values.
func toUUIDs(values []pgUUID) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		result = append(result, value.UUID)
	}
	return result
}

// toPGUUIDs converts UUID values into query parameters.
func toPGUUIDs(values []uuid.UUID) []pgUUID {
	result := make([]pgUUID, 0, len(values))
	for _, value := range values {
		result = append(result, id(value))
	}
	return result
}
