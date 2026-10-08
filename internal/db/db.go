package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // same driver as Nakama (pgx stdlib)
)

// Connect opens and verifies a Postgres connection.
func Connect(ctx context.Context, dsn string) (*sql.DB, error) {
	u, err := url.Parse("postgresql://" + dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid database dsn: %w", err)
	}
	if u.User.Username() == "" {
		u.User = url.User("postgres")
	}

	db, err := sql.Open("pgx", u.String())
	if err != nil {
		return nil, err
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	db.SetConnMaxLifetime(time.Hour)
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(4)
	return db, nil
}
