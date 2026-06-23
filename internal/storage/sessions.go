package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"
)

// Session is a server-side web session.
type Session struct {
	ID         string
	UserID     int64
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	UserAgent  string
	ClientIP   string
}

func randomID(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CreateSession mints a session for a user with the given lifetime.
func (db *DB) CreateSession(ctx context.Context, userID int64, ttl time.Duration, userAgent, ip string) (*Session, error) {
	id, err := randomID(32)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	exp := now.Add(ttl)
	_, err = db.ExecContext(ctx, `
		INSERT INTO sessions (id, user_id, created_at, expires_at, last_seen_at, user_agent, client_ip)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, userID, now.Format(time.RFC3339Nano), exp.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano), nullString(userAgent), nullString(ip))
	if err != nil {
		return nil, err
	}
	return &Session{ID: id, UserID: userID, CreatedAt: now, ExpiresAt: exp, LastSeenAt: now, UserAgent: userAgent, ClientIP: ip}, nil
}

// SessionByID returns a non-expired session, or ErrNotFound.
func (db *DB) SessionByID(ctx context.Context, id string) (*Session, error) {
	var s Session
	var created, expires, lastSeen string
	var ua, ip sql.NullString
	err := db.QueryRowContext(ctx,
		"SELECT id, user_id, created_at, expires_at, last_seen_at, user_agent, client_ip FROM sessions WHERE id = ?", id).
		Scan(&s.ID, &s.UserID, &created, &expires, &lastSeen, &ua, &ip)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	s.CreatedAt = parseTime(created)
	s.ExpiresAt = parseTime(expires)
	s.LastSeenAt = parseTime(lastSeen)
	s.UserAgent = ua.String
	s.ClientIP = ip.String
	if time.Now().UTC().After(s.ExpiresAt) {
		_ = db.DeleteSession(ctx, id)
		return nil, ErrNotFound
	}
	return &s, nil
}

// TouchSession slides a session's expiry forward and updates last_seen.
func (db *DB) TouchSession(ctx context.Context, id string, ttl time.Duration, ip string) error {
	now := time.Now().UTC()
	_, err := db.ExecContext(ctx,
		"UPDATE sessions SET expires_at = ?, last_seen_at = ?, client_ip = ? WHERE id = ?",
		now.Add(ttl).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), nullString(ip), id)
	return err
}

// DeleteSession removes a session (logout).
func (db *DB) DeleteSession(ctx context.Context, id string) error {
	_, err := db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", id)
	return err
}

// DeleteExpiredSessions purges sessions past their expiry.
func (db *DB) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	res, err := db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < ?", time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// OIDCFlow is short-lived state for an in-progress login.
type OIDCFlow struct {
	State        string
	CodeVerifier string
	Nonce        string
	RedirectTo   string
}

// CreateOIDCFlow stores login flow state keyed by the state parameter.
func (db *DB) CreateOIDCFlow(ctx context.Context, f OIDCFlow, ttl time.Duration) error {
	now := time.Now().UTC()
	_, err := db.ExecContext(ctx, `
		INSERT INTO oidc_flows (state, code_verifier, nonce, redirect_to, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		f.State, f.CodeVerifier, f.Nonce, nullString(f.RedirectTo),
		now.Format(time.RFC3339Nano), now.Add(ttl).Format(time.RFC3339Nano))
	return err
}

// TakeOIDCFlow returns and deletes the flow for a state, or ErrNotFound if it
// is missing or expired.
func (db *DB) TakeOIDCFlow(ctx context.Context, state string) (*OIDCFlow, error) {
	var f OIDCFlow
	var redirect sql.NullString
	var expires string
	err := db.QueryRowContext(ctx,
		"SELECT state, code_verifier, nonce, redirect_to, expires_at FROM oidc_flows WHERE state = ?", state).
		Scan(&f.State, &f.CodeVerifier, &f.Nonce, &redirect, &expires)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_, _ = db.ExecContext(ctx, "DELETE FROM oidc_flows WHERE state = ?", state)
	f.RedirectTo = redirect.String
	if time.Now().UTC().After(parseTime(expires)) {
		return nil, ErrNotFound
	}
	return &f, nil
}
