package storage

import (
	"context"
	"path/filepath"
	"testing"
)

// newTestDB opens a migrated database in a temp dir.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

func TestMigrateAppliesAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	n, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if n < 1 {
		t.Fatalf("first Migrate applied %d, want at least 1", n)
	}
	v, err := db.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != n {
		t.Fatalf("SchemaVersion = %d, want %d (one per applied migration)", v, n)
	}

	n2, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("second Migrate applied %d, want 0", n2)
	}
}

func TestSchemaHasExpectedTables(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	want := []string{
		"users", "app_passwords", "calendars", "objects", "object_changes",
		"calendar_acl", "attendee_state", "schedule_inbox_objects",
		"imip_outbound_queue", "imip_inbound_processed", "views",
		"view_calendars", "share_tokens", "audit_events", "settings",
	}
	for _, table := range want {
		var n int
		err := db.QueryRowContext(ctx,
			"SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n)
		if err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("table %q missing", table)
		}
	}
}

func TestForeignKeysEnabled(t *testing.T) {
	db := newTestDB(t)
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}
