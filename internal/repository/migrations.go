package repository

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

// migrationsFS carries the schema in the binary so a deployment ships the
// schema it was built against.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies every pending migration against databaseURL and reports
// whether the schema changed.
func Migrate(databaseURL string) (applied bool, err error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return false, fmt.Errorf("open database for migration: %w", err)
	}
	defer func() {
		_ = db.Close()
	}()

	if err := db.Ping(); err != nil {
		return false, fmt.Errorf("ping database for migration: %w", err)
	}

	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return false, fmt.Errorf("load embedded migrations: %w", err)
	}

	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return false, fmt.Errorf("initialise migration driver: %w", err)
	}

	migrator, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return false, fmt.Errorf("initialise migrator: %w", err)
	}

	if err := migrator.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			return false, nil
		}
		return false, fmt.Errorf("apply migrations: %w", err)
	}

	return true, nil
}
