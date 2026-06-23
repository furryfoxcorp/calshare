package storage

import (
	"context"
	"database/sql"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// AppPassword mirrors a row in app_passwords. The cleartext is never stored.
type AppPassword struct {
	ID         int64
	UserID     int64
	Label      string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	LastUsedIP string
	RevokedAt  *time.Time
}

// CreateAppPassword stores a bcrypt hash of the cleartext for the user and
// returns the new row id.
func (db *DB) CreateAppPassword(ctx context.Context, userID int64, label, cleartext string) (int64, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(cleartext), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	res, err := db.ExecContext(ctx,
		"INSERT INTO app_passwords (user_id, label, password_hash, created_at) VALUES (?, ?, ?, ?)",
		userID, label, string(hash), nowUTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AppPasswordsForUser lists a user's app passwords, newest first.
func (db *DB) AppPasswordsForUser(ctx context.Context, userID int64) ([]AppPassword, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT id, user_id, label, created_at, last_used_at, last_used_ip, revoked_at FROM app_passwords WHERE user_id = ? ORDER BY id DESC",
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppPassword
	for rows.Next() {
		var a AppPassword
		var created string
		var lastUsed, ip, revoked sql.NullString
		if err := rows.Scan(&a.ID, &a.UserID, &a.Label, &created, &lastUsed, &ip, &revoked); err != nil {
			return nil, err
		}
		a.CreatedAt = parseTime(created)
		if lastUsed.Valid {
			t := parseTime(lastUsed.String)
			a.LastUsedAt = &t
		}
		a.LastUsedIP = ip.String
		if revoked.Valid {
			t := parseTime(revoked.String)
			a.RevokedAt = &t
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// MatchAppPassword finds the user by email, then bcrypt-compares the cleartext
// against each of that user's non-revoked app passwords. On a match it returns
// the user and the matched password row. It returns ErrNotFound when the user
// does not exist or no password matches.
func (db *DB) MatchAppPassword(ctx context.Context, email, cleartext string) (*User, *AppPassword, error) {
	user, err := db.UserByEmail(ctx, email)
	if err != nil {
		return nil, nil, err
	}
	rows, err := db.QueryContext(ctx,
		"SELECT id, password_hash FROM app_passwords WHERE user_id = ? AND revoked_at IS NULL",
		user.ID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var matchID int64
	found := false
	for rows.Next() {
		var id int64
		var hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return nil, nil, err
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(cleartext)) == nil {
			matchID = id
			found = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if !found {
		return nil, nil, ErrNotFound
	}
	return user, &AppPassword{ID: matchID, UserID: user.ID}, nil
}

// TouchAppPassword records the last use time and client IP for a password.
func (db *DB) TouchAppPassword(ctx context.Context, id int64, ip string) error {
	_, err := db.ExecContext(ctx,
		"UPDATE app_passwords SET last_used_at = ?, last_used_ip = ? WHERE id = ?",
		nowUTC(), ip, id)
	return err
}

// RevokeAppPassword marks an app password revoked.
func (db *DB) RevokeAppPassword(ctx context.Context, id int64) error {
	res, err := db.ExecContext(ctx,
		"UPDATE app_passwords SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL",
		nowUTC(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
