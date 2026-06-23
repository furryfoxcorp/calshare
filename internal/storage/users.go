package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrNotFound is returned by repository lookups when no row matches.
var ErrNotFound = errors.New("not found")

// User mirrors a row in the users table.
type User struct {
	ID          int64
	OIDCSub     string
	Email       string
	DisplayName string
	IsAdmin     bool
	DisplayTZ   string
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var created string
	var last sql.NullString
	err := row.Scan(&u.ID, &u.OIDCSub, &u.Email, &u.DisplayName, &u.IsAdmin, &u.DisplayTZ, &created, &last)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.CreatedAt = parseTime(created)
	if last.Valid {
		t := parseTime(last.String)
		u.LastLoginAt = &t
	}
	return &u, nil
}

const userCols = "id, oidc_sub, email, display_name, is_admin, display_tz, created_at, last_login_at"

// UpsertUserOnLogin inserts a new user keyed by OIDC sub, or updates the
// email, display name, and last_login_at of an existing one. It returns the
// resulting user.
func (db *DB) UpsertUserOnLogin(ctx context.Context, sub, email, name string) (*User, error) {
	now := nowUTC()
	_, err := db.ExecContext(ctx, `
		INSERT INTO users (oidc_sub, email, display_name, created_at, last_login_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(oidc_sub) DO UPDATE SET
			email = excluded.email,
			display_name = excluded.display_name,
			last_login_at = excluded.last_login_at`,
		sub, email, name, now, now)
	if err != nil {
		return nil, err
	}
	return db.UserBySub(ctx, sub)
}

// UserBySub looks up a user by OIDC subject.
func (db *DB) UserBySub(ctx context.Context, sub string) (*User, error) {
	row := db.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE oidc_sub = ?", sub)
	return scanUser(row)
}

// UserByID looks up a user by primary key.
func (db *DB) UserByID(ctx context.Context, id int64) (*User, error) {
	row := db.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE id = ?", id)
	return scanUser(row)
}

// UserByEmail looks up a user by email, case-insensitive.
func (db *DB) UserByEmail(ctx context.Context, email string) (*User, error) {
	row := db.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE LOWER(email) = LOWER(?)", email)
	return scanUser(row)
}

// SearchUsers finds users whose email or display name contains q
// (case-insensitive). An empty query returns all users.
func (db *DB) SearchUsers(ctx context.Context, q string) ([]User, error) {
	like := "%" + strings.ToLower(q) + "%"
	rows, err := db.QueryContext(ctx,
		"SELECT "+userCols+" FROM users WHERE LOWER(email) LIKE ? OR LOWER(display_name) LIKE ? ORDER BY display_name",
		like, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// AllUsers returns every user, for the admin page.
func (db *DB) AllUsers(ctx context.Context) ([]User, error) {
	return db.SearchUsers(ctx, "")
}

// SetAdmin sets or clears the admin flag on the user matched by email.
func (db *DB) SetAdmin(ctx context.Context, email string, admin bool) error {
	res, err := db.ExecContext(ctx, "UPDATE users SET is_admin = ? WHERE LOWER(email) = LOWER(?)", admin, email)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDisplayTZ updates a user's display timezone.
func (db *DB) SetDisplayTZ(ctx context.Context, userID int64, tz string) error {
	_, err := db.ExecContext(ctx, "UPDATE users SET display_tz = ? WHERE id = ?", tz, userID)
	return err
}
