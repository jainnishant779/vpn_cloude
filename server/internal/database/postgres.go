package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationsAdvisoryLockID int64 = 427001

// PoolConfig holds optional connection-pool tuning applied on top of the
// connection string.  Zero values fall back to pgx defaults.
type PoolConfig struct {
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// DB wraps the PostgreSQL pool and migration metadata.
type DB struct {
	Pool          *pgxpool.Pool
	migrationsDir string
}

// NewPostgresDB creates a tuned connection pool and verifies connectivity.
func NewPostgresDB(connStr string, poolCfg PoolConfig) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("new postgres db: parse config: %w", err)
	}

	// Apply pool tuning when non-zero values are provided.
	if poolCfg.MaxConns > 0 {
		cfg.MaxConns = poolCfg.MaxConns
	}
	if poolCfg.MinConns > 0 {
		cfg.MinConns = poolCfg.MinConns
	}
	if poolCfg.MaxConnLifetime > 0 {
		cfg.MaxConnLifetime = poolCfg.MaxConnLifetime
	}
	if poolCfg.MaxConnIdleTime > 0 {
		cfg.MaxConnIdleTime = poolCfg.MaxConnIdleTime
	}

	// Health-check every acquired connection to fail fast on stale ones.
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("new postgres db: connect: %w", err)
	}

	// Verify basic connectivity with a 10-second budget.
	pingCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("new postgres db: ping: %w", err)
	}

	return &DB{
		Pool:          pool,
		migrationsDir: filepath.Join("internal", "database", "migrations"),
	}, nil
}

// Close releases database resources.
func (db *DB) Close() {
	if db == nil || db.Pool == nil {
		return
	}
	db.Pool.Close()
}

// Stats returns a snapshot of the current pool state for health reporting.
type Stats struct {
	TotalConns    int32
	IdleConns     int32
	MaxConns      int32
	AcquiredConns int32
}

// PoolStats returns a snapshot of pool health metrics.
func (db *DB) PoolStats() Stats {
	if db == nil || db.Pool == nil {
		return Stats{}
	}
	s := db.Pool.Stat()
	return Stats{
		TotalConns:    s.TotalConns(),
		IdleConns:     s.IdleConns(),
		MaxConns:      s.MaxConns(),
		AcquiredConns: s.AcquiredConns(),
	}
}

// Migrate applies SQL files in lexical order and tracks applied files.
func (db *DB) Migrate(ctx context.Context) error {
	if db == nil || db.Pool == nil {
		return fmt.Errorf("migrate: database not initialized")
	}

	lockConn, err := db.Pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquire advisory-lock connection: %w", err)
	}
	defer lockConn.Release()

	if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationsAdvisoryLockID); err != nil {
		return fmt.Errorf("migrate: acquire advisory lock: %w", err)
	}
	defer func() {
		_, _ = lockConn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationsAdvisoryLockID)
	}()

	if err := db.ensureMigrationsTable(ctx); err != nil {
		return fmt.Errorf("migrate: ensure migrations table: %w", err)
	}

	entries, err := os.ReadDir(db.migrationsDir)
	if err != nil {
		return fmt.Errorf("migrate: read migrations directory: %w", err)
	}

	filenames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		filenames = append(filenames, entry.Name())
	}
	sort.Strings(filenames)

	for _, filename := range filenames {
		applied, err := db.isMigrationApplied(ctx, filename)
		if err != nil {
			return fmt.Errorf("migrate: check migration %s: %w", filename, err)
		}
		if applied {
			continue
		}
		if err := db.applyMigration(ctx, filename); err != nil {
			return fmt.Errorf("migrate: apply migration %s: %w", filename, err)
		}
	}

	return nil
}

func (db *DB) ensureMigrationsTable(ctx context.Context) error {
	const query = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    filename   TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`
	if _, err := db.Pool.Exec(ctx, query); err != nil {
		return fmt.Errorf("ensure migrations table: %w", err)
	}
	return nil
}

func (db *DB) isMigrationApplied(ctx context.Context, filename string) (bool, error) {
	const query = `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE filename = $1);`
	var exists bool
	if err := db.Pool.QueryRow(ctx, query, filename).Scan(&exists); err != nil {
		return false, fmt.Errorf("is migration applied: %w", err)
	}
	return exists, nil
}

func (db *DB) applyMigration(ctx context.Context, filename string) error {
	path := filepath.Join(db.migrationsDir, filename)
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("apply migration: read file: %w", err)
	}

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("apply migration: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, string(content)); err != nil {
		return fmt.Errorf("apply migration: execute sql: %w", err)
	}

	const markApplied = `INSERT INTO schema_migrations (filename) VALUES ($1);`
	if _, err := tx.Exec(ctx, markApplied, filename); err != nil {
		return fmt.Errorf("apply migration: mark applied: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("apply migration: commit transaction: %w", err)
	}
	return nil
}
