package storage

import (
	"context"
	"database/sql"
	"errors"
)

// Access levels a viewer can have on a calendar.
const (
	AccessOwner     = "owner"
	AccessReadWrite = "read-write"
	AccessRead      = "read"
	AccessNone      = ""
)

// ACLEntry is one grant on a calendar.
type ACLEntry struct {
	CalendarID    int64
	GranteeUserID int64
	GranteeEmail  string
	GranteeName   string
	Privilege     string // read or read-write
}

// GrantCalendarAccess gives a local user read or read-write access to a
// calendar. Re-granting updates the privilege.
func (db *DB) GrantCalendarAccess(ctx context.Context, calendarID, granteeUserID int64, privilege string) error {
	if privilege != AccessRead && privilege != AccessReadWrite {
		return errors.New("storage: invalid privilege")
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO calendar_acl (calendar_id, grantee_user_id, privilege, granted_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(calendar_id, grantee_user_id) DO UPDATE SET privilege = excluded.privilege`,
		calendarID, granteeUserID, privilege, nowUTC())
	return err
}

// RevokeCalendarAccess removes a grant.
func (db *DB) RevokeCalendarAccess(ctx context.Context, calendarID, granteeUserID int64) error {
	_, err := db.ExecContext(ctx, "DELETE FROM calendar_acl WHERE calendar_id = ? AND grantee_user_id = ?", calendarID, granteeUserID)
	return err
}

// CalendarACL lists the grants on a calendar, with grantee details.
func (db *DB) CalendarACL(ctx context.Context, calendarID int64) ([]ACLEntry, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT a.calendar_id, a.grantee_user_id, u.email, u.display_name, a.privilege
		FROM calendar_acl a JOIN users u ON u.id = a.grantee_user_id
		WHERE a.calendar_id = ? ORDER BY u.email`, calendarID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ACLEntry
	for rows.Next() {
		var e ACLEntry
		if err := rows.Scan(&e.CalendarID, &e.GranteeUserID, &e.GranteeEmail, &e.GranteeName, &e.Privilege); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CalendarsSharedWith returns calendars another user has shared with userID.
func (db *DB) CalendarsSharedWith(ctx context.Context, userID int64) ([]Calendar, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT `+calCols+` FROM calendars c
		JOIN calendar_acl a ON a.calendar_id = c.id
		WHERE a.grantee_user_id = ? ORDER BY c.id`, userID)
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

// AccessLevel returns the viewer's access to a calendar: owner, read-write,
// read, or none.
func (db *DB) AccessLevel(ctx context.Context, calendarID, userID int64) (string, error) {
	var ownerID int64
	err := db.QueryRowContext(ctx, "SELECT user_id FROM calendars WHERE id = ?", calendarID).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AccessNone, ErrNotFound
		}
		return AccessNone, err
	}
	if ownerID == userID {
		return AccessOwner, nil
	}
	var priv string
	err = db.QueryRowContext(ctx, "SELECT privilege FROM calendar_acl WHERE calendar_id = ? AND grantee_user_id = ?", calendarID, userID).Scan(&priv)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AccessNone, nil
		}
		return AccessNone, err
	}
	return priv, nil
}
