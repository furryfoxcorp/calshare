package storage

import (
	"context"
	"database/sql"
	"time"
)

// AuditEvent mirrors a row in audit_events.
type AuditEvent struct {
	ID           int64
	TS           time.Time
	ActorUserID  *int64
	ActorKind    string
	Event        string
	TargetKind   string
	TargetID     *int64
	MetadataJSON string
	ClientIP     string
}

// InsertAuditEvent appends an audit row.
func (db *DB) InsertAuditEvent(ctx context.Context, e AuditEvent) error {
	var actor, target any
	if e.ActorUserID != nil {
		actor = *e.ActorUserID
	}
	if e.TargetID != nil {
		target = *e.TargetID
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO audit_events (ts, actor_user_id, actor_kind, event, target_kind, target_id, metadata_json, client_ip)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		nowUTC(), actor, e.ActorKind, e.Event, nullString(e.TargetKind), target,
		nullString(e.MetadataJSON), nullString(e.ClientIP))
	return err
}

// ListAuditEvents returns the most recent audit rows, newest first.
func (db *DB) ListAuditEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, ts, actor_user_id, actor_kind, event, target_kind, target_id, metadata_json, client_ip
		FROM audit_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var e AuditEvent
		var ts string
		var actor, target sql.NullInt64
		var targetKind, meta, ip sql.NullString
		if err := rows.Scan(&e.ID, &ts, &actor, &e.ActorKind, &e.Event, &targetKind, &target, &meta, &ip); err != nil {
			return nil, err
		}
		e.TS = parseTime(ts)
		if actor.Valid {
			e.ActorUserID = &actor.Int64
		}
		if target.Valid {
			e.TargetID = &target.Int64
		}
		e.TargetKind = targetKind.String
		e.MetadataJSON = meta.String
		e.ClientIP = ip.String
		out = append(out, e)
	}
	return out, rows.Err()
}
