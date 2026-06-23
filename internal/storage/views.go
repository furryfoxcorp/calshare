package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// View is a named privacy configuration over a set of calendars.
type View struct {
	ID                 int64
	UserID             int64
	Name               string
	Preset             string
	BusyLabel          string
	IncludePrivate     bool
	IncludeCancelled   bool
	IncludeTentative   bool
	IncludeTransparent bool
	FieldsJSON         string
	CreatedAt          time.Time
	ModifiedAt         time.Time
}

// ViewCalendar links a calendar into a view, with optional per-calendar
// overrides.
type ViewCalendar struct {
	ID             int64
	ViewID         int64
	CalendarID     int64
	OverridePreset string // empty means inherit the view preset
	FieldsJSON     string // empty means no per-calendar overrides
}

// CountViews returns how many views a user has defined.
func (db *DB) CountViews(ctx context.Context, userID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, "SELECT count(*) FROM views WHERE user_id = ?", userID).Scan(&n)
	return n, err
}

// CreateView inserts a view and returns it with its id.
func (db *DB) CreateView(ctx context.Context, v *View) (*View, error) {
	if v.Preset == "" {
		v.Preset = "titles"
	}
	if v.BusyLabel == "" {
		v.BusyLabel = "Busy"
	}
	if v.FieldsJSON == "" {
		v.FieldsJSON = "{}"
	}
	now := nowUTC()
	res, err := db.ExecContext(ctx, `
		INSERT INTO views (user_id, name, preset, busy_label, include_private,
			include_cancelled, include_tentative, include_transparent, fields_json,
			created_at, modified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.UserID, v.Name, v.Preset, v.BusyLabel, boolToInt(v.IncludePrivate),
		boolToInt(v.IncludeCancelled), boolToInt(v.IncludeTentative),
		boolToInt(v.IncludeTransparent), v.FieldsJSON, now, now)
	if err != nil {
		return nil, err
	}
	v.ID, _ = res.LastInsertId()
	v.CreatedAt = parseTime(now)
	v.ModifiedAt = parseTime(now)
	return v, nil
}

const viewCols = `id, user_id, name, preset, busy_label, include_private,
	include_cancelled, include_tentative, include_transparent, fields_json,
	created_at, modified_at`

func scanView(row interface{ Scan(...any) error }) (*View, error) {
	var v View
	var priv, canc, tent, transp int
	var created, modified string
	err := row.Scan(&v.ID, &v.UserID, &v.Name, &v.Preset, &v.BusyLabel,
		&priv, &canc, &tent, &transp, &v.FieldsJSON, &created, &modified)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	v.IncludePrivate = priv != 0
	v.IncludeCancelled = canc != 0
	v.IncludeTentative = tent != 0
	v.IncludeTransparent = transp != 0
	v.CreatedAt = parseTime(created)
	v.ModifiedAt = parseTime(modified)
	return &v, nil
}

// ViewByID returns one view.
func (db *DB) ViewByID(ctx context.Context, id int64) (*View, error) {
	return scanView(db.QueryRowContext(ctx, "SELECT "+viewCols+" FROM views WHERE id = ?", id))
}

// ViewsForUser lists a user's views, newest first.
func (db *DB) ViewsForUser(ctx context.Context, userID int64) ([]View, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+viewCols+" FROM views WHERE user_id = ? ORDER BY id DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []View
	for rows.Next() {
		v, err := scanView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// UpdateView writes a view's editable fields.
func (db *DB) UpdateView(ctx context.Context, v *View) error {
	_, err := db.ExecContext(ctx, `
		UPDATE views SET name = ?, preset = ?, busy_label = ?, include_private = ?,
			include_cancelled = ?, include_tentative = ?, include_transparent = ?,
			fields_json = ?, modified_at = ? WHERE id = ?`,
		v.Name, v.Preset, v.BusyLabel, boolToInt(v.IncludePrivate),
		boolToInt(v.IncludeCancelled), boolToInt(v.IncludeTentative),
		boolToInt(v.IncludeTransparent), v.FieldsJSON, nowUTC(), v.ID)
	return err
}

// DeleteView removes a view (cascading to its calendars and tokens).
func (db *DB) DeleteView(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, "DELETE FROM views WHERE id = ?", id)
	return err
}

// SetViewCalendar adds or updates a calendar's membership in a view.
func (db *DB) SetViewCalendar(ctx context.Context, vc *ViewCalendar) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO view_calendars (view_id, calendar_id, override_preset, fields_json)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(view_id, calendar_id) DO UPDATE SET
			override_preset = excluded.override_preset,
			fields_json = excluded.fields_json`,
		vc.ViewID, vc.CalendarID, nullString(vc.OverridePreset), nullString(vc.FieldsJSON))
	return err
}

// RemoveViewCalendar drops a calendar from a view.
func (db *DB) RemoveViewCalendar(ctx context.Context, viewID, calendarID int64) error {
	_, err := db.ExecContext(ctx, "DELETE FROM view_calendars WHERE view_id = ? AND calendar_id = ?", viewID, calendarID)
	return err
}

// ViewCalendars lists the calendars in a view.
func (db *DB) ViewCalendars(ctx context.Context, viewID int64) ([]ViewCalendar, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT id, view_id, calendar_id, override_preset, fields_json FROM view_calendars WHERE view_id = ?", viewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ViewCalendar
	for rows.Next() {
		var vc ViewCalendar
		var preset, fields sql.NullString
		if err := rows.Scan(&vc.ID, &vc.ViewID, &vc.CalendarID, &preset, &fields); err != nil {
			return nil, err
		}
		vc.OverridePreset = preset.String
		vc.FieldsJSON = fields.String
		out = append(out, vc)
	}
	return out, rows.Err()
}
