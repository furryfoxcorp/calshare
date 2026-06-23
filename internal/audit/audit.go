// Package audit records security-relevant actions to both the audit_events
// table and the structured log stream.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/furryfoxcorp/calshare/internal/storage"
)

// Logger writes audit events.
type Logger struct {
	db  *storage.DB
	log *slog.Logger
}

// New builds an audit logger.
func New(db *storage.DB, log *slog.Logger) *Logger {
	return &Logger{db: db, log: log}
}

// Entry describes one auditable action.
type Entry struct {
	ActorUserID *int64
	ActorKind   string // user, admin, system, anonymous
	Event       string // dotted name, e.g. share_token.create
	TargetKind  string
	TargetID    *int64
	ClientIP    string
	Metadata    map[string]any
}

// Record persists the entry and emits a log line. A nil Logger is a no-op so
// callers need not guard.
func (l *Logger) Record(ctx context.Context, e Entry) {
	if l == nil {
		return
	}
	metaJSON := ""
	if len(e.Metadata) > 0 {
		if b, err := json.Marshal(e.Metadata); err == nil {
			metaJSON = string(b)
		}
	}
	_ = l.db.InsertAuditEvent(ctx, storage.AuditEvent{
		ActorUserID:  e.ActorUserID,
		ActorKind:    e.ActorKind,
		Event:        e.Event,
		TargetKind:   e.TargetKind,
		TargetID:     e.TargetID,
		MetadataJSON: metaJSON,
		ClientIP:     e.ClientIP,
	})
	l.log.Info("audit",
		"audit", true,
		"event", e.Event,
		"actor_kind", e.ActorKind,
		"actor_user_id", actorVal(e.ActorUserID),
		"target_kind", e.TargetKind,
		"target_id", targetVal(e.TargetID),
		"client_ip", e.ClientIP,
	)
}

func actorVal(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func targetVal(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// UserActor builds an Entry actor from a user id.
func UserActor(id int64) *int64 { return &id }
