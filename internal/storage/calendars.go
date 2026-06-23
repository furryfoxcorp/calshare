package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"time"
)

// Calendar mirrors a row in the calendars table.
type Calendar struct {
	ID            int64
	UserID        int64
	Slug          string
	SourceType    string // "native" or "ics"
	DisplayName   string
	Color         string
	Description   string
	Ctag          string
	SyncSeq       int64
	SupportsVTODO bool
	CreatedAt     time.Time

	// ICS source fields, set only when SourceType == "ics".
	ICSURL          string
	ICSPollInterval int // seconds; 0 means use the server default
	ICSETag         string
	ICSLastModified string
	ICSLastPolledAt *time.Time
	ICSLastStatus   string
	ICSLastError    string
	ICSBasicUser    string
	ICSBasicPassEnc []byte
}

// NewSlug returns a stable, URL-safe identifier for a calendar collection.
func NewSlug() string {
	b := make([]byte, 9) // 12 base64 chars
	if _, err := rand.Read(b); err != nil {
		// rand.Read failing is fatal-grade; fall back to a time-based value so
		// the caller still gets a usable slug rather than an empty string.
		return fmt.Sprintf("cal%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// CreateCalendar inserts a calendar, generating a slug if none is set, and
// returns it populated with its new id.
func (db *DB) CreateCalendar(ctx context.Context, c *Calendar) (*Calendar, error) {
	if c.Slug == "" {
		c.Slug = NewSlug()
	}
	if c.SourceType == "" {
		c.SourceType = "native"
	}
	c.Ctag = "0"
	c.SyncSeq = 0
	now := nowUTC()
	res, err := db.ExecContext(ctx, `
		INSERT INTO calendars
			(user_id, slug, source_type, display_name, color, description, ctag,
			 sync_seq, supports_vtodo, created_at, ics_url, ics_poll_interval,
			 ics_basic_user, ics_basic_pass_enc)
		VALUES (?, ?, ?, ?, ?, ?, '0', 0, ?, ?, ?, ?, ?, ?)`,
		c.UserID, c.Slug, c.SourceType, c.DisplayName, nullString(c.Color),
		nullString(c.Description), boolToInt(c.SupportsVTODO), now,
		nullString(c.ICSURL), nullInt(c.ICSPollInterval),
		nullString(c.ICSBasicUser), c.ICSBasicPassEnc)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	c.ID = id
	c.CreatedAt = parseTime(now)
	return c, nil
}

const calCols = `id, user_id, slug, source_type, display_name, color, description,
	ctag, sync_seq, supports_vtodo, created_at, ics_url, ics_poll_interval,
	ics_etag, ics_last_modified, ics_last_polled_at, ics_last_status,
	ics_last_error, ics_basic_user, ics_basic_pass_enc`

func scanCalendar(row interface{ Scan(...any) error }) (*Calendar, error) {
	var c Calendar
	var created string
	var color, desc, icsURL, icsETag, icsLastMod, icsPolled, icsStatus, icsErr, icsUser sql.NullString
	var pollInterval sql.NullInt64
	var supportsVTODO int
	err := row.Scan(&c.ID, &c.UserID, &c.Slug, &c.SourceType, &c.DisplayName,
		&color, &desc, &c.Ctag, &c.SyncSeq, &supportsVTODO, &created,
		&icsURL, &pollInterval, &icsETag, &icsLastMod, &icsPolled, &icsStatus,
		&icsErr, &icsUser, &c.ICSBasicPassEnc)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	c.Color = color.String
	c.Description = desc.String
	c.SupportsVTODO = supportsVTODO != 0
	c.CreatedAt = parseTime(created)
	c.ICSURL = icsURL.String
	c.ICSPollInterval = int(pollInterval.Int64)
	c.ICSETag = icsETag.String
	c.ICSLastModified = icsLastMod.String
	if icsPolled.Valid {
		t := parseTime(icsPolled.String)
		c.ICSLastPolledAt = &t
	}
	c.ICSLastStatus = icsStatus.String
	c.ICSLastError = icsErr.String
	c.ICSBasicUser = icsUser.String
	return &c, nil
}

// CalendarByID returns a calendar by primary key.
func (db *DB) CalendarByID(ctx context.Context, id int64) (*Calendar, error) {
	return scanCalendar(db.QueryRowContext(ctx, "SELECT "+calCols+" FROM calendars WHERE id = ?", id))
}

// CalendarBySlug returns a user's calendar by slug.
func (db *DB) CalendarBySlug(ctx context.Context, userID int64, slug string) (*Calendar, error) {
	return scanCalendar(db.QueryRowContext(ctx,
		"SELECT "+calCols+" FROM calendars WHERE user_id = ? AND slug = ?", userID, slug))
}

// CalendarsForUser lists a user's calendars, oldest first.
func (db *DB) CalendarsForUser(ctx context.Context, userID int64) ([]Calendar, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+calCols+" FROM calendars WHERE user_id = ? ORDER BY id", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Calendar
	for rows.Next() {
		c, err := scanCalendar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// RenameCalendar updates a calendar's display name.
func (db *DB) RenameCalendar(ctx context.Context, id int64, name string) error {
	_, err := db.ExecContext(ctx, "UPDATE calendars SET display_name = ? WHERE id = ?", name, id)
	return err
}

// DeleteCalendar removes a calendar and its objects (via cascade).
func (db *DB) DeleteCalendar(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, "DELETE FROM calendars WHERE id = ?", id)
	return err
}

// ICSCalendars lists every calendar backed by an ICS feed.
func (db *DB) ICSCalendars(ctx context.Context) ([]Calendar, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+calCols+" FROM calendars WHERE source_type = 'ics' ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Calendar
	for rows.Next() {
		c, err := scanCalendar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// UpdateICSPollState records the outcome of a poll: the new validators, a
// status word, and an error string (empty on success). It always stamps
// ics_last_polled_at.
func (db *DB) UpdateICSPollState(ctx context.Context, calID int64, etag, lastModified, status, errStr string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE calendars SET ics_etag = ?, ics_last_modified = ?, ics_last_polled_at = ?,
			ics_last_status = ?, ics_last_error = ? WHERE id = ?`,
		nullString(etag), nullString(lastModified), nowUTC(), status, nullString(errStr), calID)
	return err
}

// bumpSync increments a calendar's sync sequence inside tx and returns the new
// sequence and the ctag derived from it.
func bumpSync(ctx context.Context, tx *sql.Tx, calID int64) (int64, string, error) {
	var seq int64
	if err := tx.QueryRowContext(ctx,
		"UPDATE calendars SET sync_seq = sync_seq + 1 WHERE id = ? RETURNING sync_seq", calID).Scan(&seq); err != nil {
		return 0, "", err
	}
	ctag := fmt.Sprintf("%d", seq)
	if _, err := tx.ExecContext(ctx, "UPDATE calendars SET ctag = ? WHERE id = ?", ctag, calID); err != nil {
		return 0, "", err
	}
	return seq, ctag, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
