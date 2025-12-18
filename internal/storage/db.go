package storage

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
}

func NewDB(ctx context.Context) (*DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		host := getEnv("DB_HOST", "db")
		port := getEnv("DB_PORT", "5432")
		user := getEnv("DB_USER", "mural")
		pass := getEnv("DB_PASSWORD", "mural")
		name := getEnv("DB_NAME", "mural")
		dsn = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", user, pass, host, port, name)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}

	cfg.MaxConns = 5
	cfg.MaxConnLifetime = time.Hour

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}

	return &DB{Pool: pool}, nil
}

// Migrate ensures the core schema required by the application exists. This is
// primarily intended for environments like Fly.io where we don't mount the
// local db/001_init.sql into the database container.
func (db *DB) Migrate(ctx context.Context) error {
	const ddl = `
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_name TEXT NOT NULL,
    customer_email TEXT,
    items JSONB NOT NULL,
    amount_usdc NUMERIC(18,6) NOT NULL,
    amount_cop NUMERIC(18,2),
    status TEXT NOT NULL,
    mural_payout_request_id UUID,
    mural_payout_status TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);
`
	_, err := db.Pool.Exec(ctx, ddl)
	if err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}


