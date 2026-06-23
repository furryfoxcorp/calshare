package storage

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// AttendeeState mirrors a row in attendee_state.
type AttendeeState struct {
	ID             int64
	ObjectID       int64
	Email          string
	CN             string
	Role           string
	Partstat       string
	RSVP           bool
	IsLocalUser    bool
	LocalUserID    *int64
	ScheduleStatus string
	LastUpdatedAt  time.Time
}

// UpsertAttendeeState inserts or updates an attendee row keyed by
// (object_id, email).
func (db *DB) UpsertAttendeeState(ctx context.Context, a *AttendeeState) error {
	var localID any
	if a.LocalUserID != nil {
		localID = *a.LocalUserID
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO attendee_state
			(object_id, attendee_email, cn, role, partstat, rsvp, is_local_user,
			 local_user_id, schedule_status, last_updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(object_id, attendee_email) DO UPDATE SET
			cn = excluded.cn, role = excluded.role, partstat = excluded.partstat,
			rsvp = excluded.rsvp, is_local_user = excluded.is_local_user,
			local_user_id = excluded.local_user_id,
			schedule_status = excluded.schedule_status,
			last_updated_at = excluded.last_updated_at`,
		a.ObjectID, normalizeEmail(a.Email), nullString(a.CN), nullString(a.Role),
		a.Partstat, boolToInt(a.RSVP), boolToInt(a.IsLocalUser), localID,
		nullString(a.ScheduleStatus), nowUTC())
	return err
}

// AttendeesForObject lists the attendee state for an object.
func (db *DB) AttendeesForObject(ctx context.Context, objectID int64) ([]AttendeeState, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, object_id, attendee_email, cn, role, partstat, rsvp,
			is_local_user, local_user_id, schedule_status, last_updated_at
		FROM attendee_state WHERE object_id = ? ORDER BY id`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttendeeState
	for rows.Next() {
		var a AttendeeState
		var cn, role, status sql.NullString
		var localID sql.NullInt64
		var rsvp, local int
		var updated string
		if err := rows.Scan(&a.ID, &a.ObjectID, &a.Email, &cn, &role, &a.Partstat,
			&rsvp, &local, &localID, &status, &updated); err != nil {
			return nil, err
		}
		a.CN = cn.String
		a.Role = role.String
		a.RSVP = rsvp != 0
		a.IsLocalUser = local != 0
		if localID.Valid {
			a.LocalUserID = &localID.Int64
		}
		a.ScheduleStatus = status.String
		a.LastUpdatedAt = parseTime(updated)
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateAttendeePartstat records a new participation status (used when a REPLY
// arrives).
func (db *DB) UpdateAttendeePartstat(ctx context.Context, objectID int64, email, partstat, scheduleStatus string) error {
	res, err := db.ExecContext(ctx, `
		UPDATE attendee_state SET partstat = ?, schedule_status = ?, last_updated_at = ?
		WHERE object_id = ? AND attendee_email = ?`,
		partstat, nullString(scheduleStatus), nowUTC(), objectID, normalizeEmail(email))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// InboxObject is a pending iTIP message in a user's scheduling Inbox.
type InboxObject struct {
	ID           int64
	UserID       int64
	Href         string
	ETag         string
	Blob         []byte
	Method       string
	UID          string
	OriginUserID *int64
	OriginEmail  string
	CreatedAt    time.Time
}

// CreateInboxObject deposits an iTIP message in a user's Inbox.
func (db *DB) CreateInboxObject(ctx context.Context, o *InboxObject) error {
	var origin any
	if o.OriginUserID != nil {
		origin = *o.OriginUserID
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO schedule_inbox_objects
			(user_id, href, etag, ical_blob, itip_method, uid, origin_user_id, origin_email, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, href) DO UPDATE SET
			etag = excluded.etag, ical_blob = excluded.ical_blob,
			itip_method = excluded.itip_method, uid = excluded.uid,
			origin_user_id = excluded.origin_user_id, origin_email = excluded.origin_email,
			created_at = excluded.created_at`,
		o.UserID, o.Href, o.ETag, o.Blob, o.Method, o.UID, origin, nullString(o.OriginEmail), nowUTC())
	return err
}

// InboxForUser lists pending Inbox messages for a user.
func (db *DB) InboxForUser(ctx context.Context, userID int64) ([]InboxObject, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, user_id, href, etag, ical_blob, itip_method, uid, origin_email, created_at
		FROM schedule_inbox_objects WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InboxObject
	for rows.Next() {
		var o InboxObject
		var origin sql.NullString
		var created string
		if err := rows.Scan(&o.ID, &o.UserID, &o.Href, &o.ETag, &o.Blob, &o.Method, &o.UID, &origin, &created); err != nil {
			return nil, err
		}
		o.OriginEmail = origin.String
		o.CreatedAt = parseTime(created)
		out = append(out, o)
	}
	return out, rows.Err()
}

// DeleteInboxObject removes a processed Inbox message.
func (db *DB) DeleteInboxObject(ctx context.Context, userID int64, href string) error {
	_, err := db.ExecContext(ctx, "DELETE FROM schedule_inbox_objects WHERE user_id = ? AND href = ?", userID, href)
	return err
}

// IMIPMessage is a queued outbound email invitation or reply.
type IMIPMessage struct {
	ID            int64
	ObjectID      *int64
	FromUserID    int64
	ToAddress     string
	Method        string
	MessageID     string
	InReplyTo     string
	UID           string
	Body          []byte
	Status        string
	AttemptCount  int
	NextAttemptAt *time.Time
	LastError     string
}

// EnqueueIMIP adds a message to the outbound queue as pending.
func (db *DB) EnqueueIMIP(ctx context.Context, m *IMIPMessage) (int64, error) {
	var objID any
	if m.ObjectID != nil {
		objID = *m.ObjectID
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO imip_outbound_queue
			(object_id, from_user_id, to_address, itip_method, message_id, in_reply_to,
			 uid, body_blob, status, attempt_count, next_attempt_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?)`,
		objID, m.FromUserID, m.ToAddress, m.Method, m.MessageID, nullString(m.InReplyTo),
		m.UID, m.Body, nowUTC(), nowUTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DueIMIP returns queued messages ready to send (pending or scheduled for
// retry at or before now), oldest first.
func (db *DB) DueIMIP(ctx context.Context, now time.Time, limit int) ([]IMIPMessage, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, object_id, from_user_id, to_address, itip_method, message_id,
			in_reply_to, uid, body_blob, status, attempt_count
		FROM imip_outbound_queue
		WHERE status IN ('pending','failed_retry')
			AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
		ORDER BY id LIMIT ?`, now.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IMIPMessage
	for rows.Next() {
		var m IMIPMessage
		var objID sql.NullInt64
		var inReply sql.NullString
		if err := rows.Scan(&m.ID, &objID, &m.FromUserID, &m.ToAddress, &m.Method,
			&m.MessageID, &inReply, &m.UID, &m.Body, &m.Status, &m.AttemptCount); err != nil {
			return nil, err
		}
		if objID.Valid {
			m.ObjectID = &objID.Int64
		}
		m.InReplyTo = inReply.String
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkIMIPSent flags a message sent.
func (db *DB) MarkIMIPSent(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx,
		"UPDATE imip_outbound_queue SET status = 'sent', attempt_count = attempt_count + 1, sent_at = ?, last_error = NULL WHERE id = ?",
		nowUTC(), id)
	return err
}

// MarkIMIPRetry schedules a retry with the given next attempt time.
func (db *DB) MarkIMIPRetry(ctx context.Context, id int64, next time.Time, errStr string) error {
	_, err := db.ExecContext(ctx,
		"UPDATE imip_outbound_queue SET status = 'failed_retry', attempt_count = attempt_count + 1, next_attempt_at = ?, last_error = ? WHERE id = ?",
		next.UTC().Format(time.RFC3339Nano), errStr, id)
	return err
}

// MarkIMIPFinal flags a message as permanently failed.
func (db *DB) MarkIMIPFinal(ctx context.Context, id int64, errStr string) error {
	_, err := db.ExecContext(ctx,
		"UPDATE imip_outbound_queue SET status = 'failed_final', attempt_count = attempt_count + 1, last_error = ? WHERE id = ?",
		errStr, id)
	return err
}

// RecentIMIP returns the most recent outbound queue rows for the admin view.
func (db *DB) RecentIMIP(ctx context.Context, limit int) ([]IMIPMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, object_id, from_user_id, to_address, itip_method, message_id,
			in_reply_to, uid, body_blob, status, attempt_count
		FROM imip_outbound_queue ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IMIPMessage
	for rows.Next() {
		var m IMIPMessage
		var objID sql.NullInt64
		var inReply sql.NullString
		if err := rows.Scan(&m.ID, &objID, &m.FromUserID, &m.ToAddress, &m.Method,
			&m.MessageID, &inReply, &m.UID, &m.Body, &m.Status, &m.AttemptCount); err != nil {
			return nil, err
		}
		if objID.Valid {
			m.ObjectID = &objID.Int64
		}
		m.InReplyTo = inReply.String
		out = append(out, m)
	}
	return out, rows.Err()
}

// IMIPProcessed reports whether an inbound Message-ID was already handled.
func (db *DB) IMIPProcessed(ctx context.Context, messageID string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, "SELECT count(*) FROM imip_inbound_processed WHERE message_id = ?", messageID).Scan(&n)
	return n > 0, err
}

// MarkIMIPInbound records an inbound message and its outcome.
func (db *DB) MarkIMIPInbound(ctx context.Context, messageID, uid, outcome string) error {
	_, err := db.ExecContext(ctx,
		"INSERT OR IGNORE INTO imip_inbound_processed (message_id, first_seen_at, uid, outcome) VALUES (?, ?, ?, ?)",
		messageID, nowUTC(), nullString(uid), outcome)
	return err
}
