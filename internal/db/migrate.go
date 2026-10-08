package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Minimal embedded migrator, modeled on nakama/migrate:
//   - SQL files live in migrations/ and are compiled into the binary (go:embed)
//   - applied versions are recorded in the schema_migrations table
//   - CLI: `helix migrate up|status`; startup: Check() fails fast if outdated
func ensureMigrationsTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	return err
}

func applied(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

func migrationFiles() ([]string, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // timestamp-prefixed names sort chronologically
	return names, nil
}

// Up applies all pending migrations in order
func Up(ctx context.Context, db *sql.DB) error {
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return err
	}
	done, err := applied(ctx, db)
	if err != nil {
		return err
	}
	files, err := migrationFiles()
	if err != nil {
		return err
	}
	for _, name := range files {
		if done[name] {
			continue
		}
		raw, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		log.Printf("migrate: applying %s", name)
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(raw)); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Check fails fast at startup when the schema is behind the binary
func Check(ctx context.Context, db *sql.DB) error {
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return err
	}
	done, err := applied(ctx, db)
	if err != nil {
		return err
	}
	files, err := migrationFiles()
	if err != nil {
		return err
	}
	var pending []string
	for _, name := range files {
		if !done[name] {
			pending = append(pending, name)
		}
	}
	if len(pending) > 0 {
		return fmt.Errorf("DB schema outdated, run `helix migrate up` (pending: %s)",
			strings.Join(pending, ", "))
	}
	return nil
}

// Status prints applied/pending migrations
func Status(ctx context.Context, db *sql.DB) error {
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return err
	}
	done, err := applied(ctx, db)
	if err != nil {
		return err
	}
	files, err := migrationFiles()
	if err != nil {
		return err
	}
	for _, name := range files {
		mark := "pending"
		if done[name] {
			mark = "applied"
		}
		fmt.Printf("%-8s %s\n", mark, name)
	}
	return nil
}
