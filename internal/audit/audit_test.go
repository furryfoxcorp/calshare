package audit

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/furryfoxcorp/calshare/internal/storage"
)

func TestRecordPersistsAndLists(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "a.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	u, _ := db.UpsertUserOnLogin(context.Background(), "s", "u@example.com", "U")

	l := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	id := u.ID
	l.Record(context.Background(), Entry{
		ActorUserID: &id, ActorKind: "user", Event: "share_token.create",
		TargetKind: "view", TargetID: &id, ClientIP: "1.2.3.4",
		Metadata: map[string]any{"label": "Zoe"},
	})

	events, err := db.ListAuditEvents(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Event != "share_token.create" || events[0].ClientIP != "1.2.3.4" {
		t.Errorf("unexpected event: %+v", events[0])
	}
	if events[0].MetadataJSON == "" {
		t.Error("metadata not stored")
	}
}

func TestNilLoggerIsNoop(t *testing.T) {
	var l *Logger
	l.Record(context.Background(), Entry{Event: "noop"}) // must not panic
}
