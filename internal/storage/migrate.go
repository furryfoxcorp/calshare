package storage

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads the embedded up migrations sorted by version.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		numStr, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("migration %q missing version prefix", name)
		}
		n, err := strconv.Atoi(numStr)
		if err != nil {
			return nil, fmt.Errorf("migration %q has non-numeric version: %w", name, err)
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: n, name: name, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// SchemaVersion returns the highest applied migration version, or 0 if none
// have been applied (or the tracking table does not yet exist).
func (db *DB) SchemaVersion(ctx context.Context) (int, error) {
	var exists int
	err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'").Scan(&exists)
	if err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, nil
	}
	var v *int
	if err := db.QueryRowContext(ctx, "SELECT max(version) FROM schema_migrations").Scan(&v); err != nil {
		return 0, err
	}
	if v == nil {
		return 0, nil
	}
	return *v, nil
}

// Migrate applies every embedded migration whose version exceeds the current
// schema version, each in its own transaction, and returns how many it
// applied.
func (db *DB) Migrate(ctx context.Context) (int, error) {
	migs, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	current, err := db.SchemaVersion(ctx)
	if err != nil {
		return 0, err
	}
	applied := 0
	for _, m := range migs {
		if m.version <= current {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return applied, err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return applied, fmt.Errorf("apply %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)",
			m.version, nowUTC()); err != nil {
			tx.Rollback()
			return applied, fmt.Errorf("record %s: %w", m.name, err)
		}
		if err := tx.Commit(); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}
