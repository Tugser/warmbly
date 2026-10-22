package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type schemaReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func requiredMigrationVersion() (int, error) {
	paths, err := fs.Glob(migrationsFS, "migrations/*.up.sql")
	if err != nil {
		return 0, err
	}
	version := 0
	for _, path := range paths {
		base := strings.TrimPrefix(path, "migrations/")
		prefix, _, _ := strings.Cut(base, "_")
		n, err := strconv.Atoi(prefix)
		if err != nil {
			return 0, fmt.Errorf("invalid embedded migration version: %w", err)
		}
		version = max(version, n)
	}
	if version == 0 {
		return 0, errors.New("no embedded migrations")
	}
	return version, nil
}

// WaitForSchema keeps services from starting before the required schema is ready.
func WaitForSchema(ctx context.Context, reader schemaReader) error {
	required, err := requiredMigrationVersion()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return waitForSchema(ctx, reader, required, time.Second)
}

func waitForSchema(ctx context.Context, reader schemaReader, required int, interval time.Duration) error {
	state := "migrations have not completed"
	var queryErr error
	for ctx.Err() == nil {
		var version int
		var dirty bool
		err := reader.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty)
		var pgErr *pgconn.PgError
		switch {
		case err == nil:
			queryErr = nil
			state = fmt.Sprintf("database version %d (dirty=%t), require clean version >= %d", version, dirty, required)
			if !dirty && version >= required && ctx.Err() == nil {
				var visible bool
				if err := reader.QueryRow(ctx, `SELECT to_regclass('warmup_pending_filings') IS NOT NULL AND to_regclass('warmup_recovery_identifiers') IS NOT NULL`).Scan(&visible); err != nil {
					return fmt.Errorf("database schema visibility check failed: %w", err)
				}
				if !visible {
					return errors.New("database schema version is current but required warmup relations are not visible; check database and search_path")
				}
				if ctx.Err() == nil {
					return nil
				}
			}
		case errors.Is(err, pgx.ErrNoRows), errors.As(err, &pgErr) && pgErr.Code == "42P01":
			queryErr = err
			state = "schema_migrations is not initialized"
		case ctx.Err() != nil && errors.Is(err, ctx.Err()):
		default:
			return fmt.Errorf("database schema readiness query failed: %w", err)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return fmt.Errorf("waiting for database migrations: %s; upgrade the backend or run the deployment migration job and check its logs: %w", state, errors.Join(ctx.Err(), queryErr))
}
