package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/furryfoxcorp/calshare/internal/ical"
)

// Object-related errors.
var (
	ErrUIDConflict  = errors.New("storage: UID already exists at a different href")
	ErrComponentSwap = errors.New("storage: cannot change component type of an existing resource")
)

// Object mirrors a row in the objects table.
type Object struct {
	ID            int64
	CalendarID    int64
	UID           string
	Href          string
	ETag          string
	Blob          []byte
	SizeBytes     int64
	ComponentType string
	First         *time.Time
	Last          *time.Time
	HasRRULE      bool
	HasScheduling bool
	CreatedAt     time.Time
	ModifiedAt    time.Time
}

// PutObject inserts or replaces the object at (calendarID, href). It parses the
// blob to fill the denormalized columns, enforces the one-UID-per-href and
// no-component-swap rules, records a change-journal row, and bumps the
// calendar's sync sequence, all in one transaction. It returns the stored
// object.
func (db *DB) PutObject(ctx context.Context, calendarID int64, href string, blob []byte) (*Object, error) {
	parsed, err := ical.Parse(blob)
	if err != nil {
		return nil, err
	}
	etag := ical.ETag(blob)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// A different href in this calendar must not already own this UID.
	var conflictHref string
	err = tx.QueryRowContext(ctx,
		"SELECT href FROM objects WHERE calendar_id = ? AND uid = ? AND href <> ?",
		calendarID, parsed.UID, href).Scan(&conflictHref)
	if err == nil {
		return nil, ErrUIDConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// Detect an existing row at this href to enforce no component-type swap and
	// to choose insert vs update.
	var existingID int64
	var existingType string
	var createdAt string
	err = tx.QueryRowContext(ctx,
		"SELECT id, component_type, created_at FROM objects WHERE calendar_id = ? AND href = ?",
		calendarID, href).Scan(&existingID, &existingType, &createdAt)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if exists && existingType != parsed.ComponentType {
		return nil, ErrComponentSwap
	}

	now := nowUTC()
	op := "added"
	if exists {
		op = "modified"
		_, err = tx.ExecContext(ctx, `
			UPDATE objects SET uid = ?, etag = ?, ical_blob = ?, size_bytes = ?,
				component_type = ?, first_occurrence_utc = ?, last_occurrence_utc = ?,
				has_rrule = ?, has_scheduling = ?, modified_at = ?
			WHERE id = ?`,
			parsed.UID, etag, blob, len(blob), parsed.ComponentType,
			utcStr(parsed.First), utcStr(parsed.Last), boolToInt(parsed.HasRRULE),
			boolToInt(parsed.HasScheduling), now, existingID)
		if err != nil {
			return nil, err
		}
	} else {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO objects
				(calendar_id, uid, href, etag, ical_blob, size_bytes, component_type,
				 first_occurrence_utc, last_occurrence_utc, has_rrule, has_scheduling,
				 created_at, modified_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			calendarID, parsed.UID, href, etag, blob, len(blob), parsed.ComponentType,
			utcStr(parsed.First), utcStr(parsed.Last), boolToInt(parsed.HasRRULE),
			boolToInt(parsed.HasScheduling), now, now)
		if err != nil {
			return nil, err
		}
		existingID, _ = res.LastInsertId()
		createdAt = now
	}

	seq, _, err := bumpSync(ctx, tx, calendarID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO object_changes (calendar_id, seq, op, href, etag, changed_at) VALUES (?, ?, ?, ?, ?, ?)",
		calendarID, seq, op, href, etag, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &Object{
		ID: existingID, CalendarID: calendarID, UID: parsed.UID, Href: href,
		ETag: etag, Blob: blob, SizeBytes: int64(len(blob)),
		ComponentType: parsed.ComponentType, First: parsed.First, Last: parsed.Last,
		HasRRULE: parsed.HasRRULE, HasScheduling: parsed.HasScheduling,
		CreatedAt: parseTime(createdAt), ModifiedAt: parseTime(now),
	}, nil
}

const objCols = `id, calendar_id, uid, href, etag, ical_blob, size_bytes,
	component_type, first_occurrence_utc, last_occurrence_utc, has_rrule,
	has_scheduling, created_at, modified_at`

func scanObject(row interface{ Scan(...any) error }) (*Object, error) {
	var o Object
	var first, last sql.NullString
	var hasR, hasS int
	var created, modified string
	err := row.Scan(&o.ID, &o.CalendarID, &o.UID, &o.Href, &o.ETag, &o.Blob,
		&o.SizeBytes, &o.ComponentType, &first, &last, &hasR, &hasS, &created, &modified)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if first.Valid {
		t := parseTime(first.String)
		o.First = &t
	}
	if last.Valid {
		t := parseTime(last.String)
		o.Last = &t
	}
	o.HasRRULE = hasR != 0
	o.HasScheduling = hasS != 0
	o.CreatedAt = parseTime(created)
	o.ModifiedAt = parseTime(modified)
	return &o, nil
}

// ObjectByHref returns the object stored at href in a calendar.
func (db *DB) ObjectByHref(ctx context.Context, calendarID int64, href string) (*Object, error) {
	return scanObject(db.QueryRowContext(ctx,
		"SELECT "+objCols+" FROM objects WHERE calendar_id = ? AND href = ?", calendarID, href))
}

// ObjectByUID returns the object with a given UID in a calendar.
func (db *DB) ObjectByUID(ctx context.Context, calendarID int64, uid string) (*Object, error) {
	return scanObject(db.QueryRowContext(ctx,
		"SELECT "+objCols+" FROM objects WHERE calendar_id = ? AND uid = ?", calendarID, uid))
}

// ObjectByID returns one object by primary key.
func (db *DB) ObjectByID(ctx context.Context, id int64) (*Object, error) {
	return scanObject(db.QueryRowContext(ctx, "SELECT "+objCols+" FROM objects WHERE id = ?", id))
}

// ObjectByUIDAny returns the first object with a given UID across all
// calendars, used to match an inbound iMIP reply to its originating event.
func (db *DB) ObjectByUIDAny(ctx context.Context, uid string) (*Object, error) {
	return scanObject(db.QueryRowContext(ctx, "SELECT "+objCols+" FROM objects WHERE uid = ? ORDER BY id LIMIT 1", uid))
}

// CalendarOwner returns the user id that owns a calendar.
func (db *DB) CalendarOwner(ctx context.Context, calendarID int64) (int64, error) {
	var uid int64
	err := db.QueryRowContext(ctx, "SELECT user_id FROM calendars WHERE id = ?", calendarID).Scan(&uid)
	return uid, err
}

// ListObjects returns every object in a calendar.
func (db *DB) ListObjects(ctx context.Context, calendarID int64) ([]Object, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+objCols+" FROM objects WHERE calendar_id = ? ORDER BY id", calendarID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Object
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// DeleteObject removes the object at href, records a deletion in the change
// journal, and bumps the calendar's sync sequence.
func (db *DB) DeleteObject(ctx context.Context, calendarID int64, href string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, "DELETE FROM objects WHERE calendar_id = ? AND href = ?", calendarID, href)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	seq, _, err := bumpSync(ctx, tx, calendarID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO object_changes (calendar_id, seq, op, href, changed_at) VALUES (?, ?, 'deleted', ?, ?)",
		calendarID, seq, href, nowUTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// Change is a row from the per-collection change journal.
type Change struct {
	Seq  int64
	Op   string // added, modified, deleted
	Href string
	ETag string
}

// ChangesSince returns journal entries with seq greater than sinceSeq, oldest
// first. Pass 0 to get every recorded change.
func (db *DB) ChangesSince(ctx context.Context, calendarID, sinceSeq int64) ([]Change, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT seq, op, href, etag FROM object_changes WHERE calendar_id = ? AND seq > ? ORDER BY seq",
		calendarID, sinceSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Change
	for rows.Next() {
		var c Change
		var etag sql.NullString
		if err := rows.Scan(&c.Seq, &c.Op, &c.Href, &etag); err != nil {
			return nil, err
		}
		c.ETag = etag.String
		out = append(out, c)
	}
	return out, rows.Err()
}

func utcStr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}
