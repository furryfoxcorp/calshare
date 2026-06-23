package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ShareToken is a recipient's opaque subscription link to a view.
type ShareToken struct {
	ID             int64
	ViewID         int64
	Label          string
	ExpiresAt      *time.Time
	HasPassword    bool
	CreatedAt      time.Time
	RevokedAt      *time.Time
	LastAccessedAt *time.Time
	LastAccessedIP string
	AccessCount    int64
	passwordHash   string
}

// NewShareTokenSecret returns a fresh opaque token (about 144 bits), URL-safe.
func NewShareTokenSecret() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashShareToken returns the sha256 of a token secret, as stored.
func HashShareToken(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// CreateShareToken stores a token for a view. secret is the cleartext token
// (only its hash is stored). password, if non-empty, gates access with HTTP
// Basic. expiresAt may be nil for no expiry. It returns the new row id.
func (db *DB) CreateShareToken(ctx context.Context, viewID int64, label, secret, password string, expiresAt *time.Time) (int64, error) {
	var pwHash any
	if password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return 0, err
		}
		pwHash = string(h)
	}
	var exp any
	if expiresAt != nil {
		exp = expiresAt.UTC().Format(time.RFC3339Nano)
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO share_tokens (view_id, label, token_hash, expires_at, password_hash, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		viewID, label, HashShareToken(secret), exp, pwHash, nowUTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func scanShareToken(row interface{ Scan(...any) error }) (*ShareToken, error) {
	var s ShareToken
	var created string
	var exp, revoked, lastAt, lastIP, pwHash sql.NullString
	err := row.Scan(&s.ID, &s.ViewID, &s.Label, &exp, &pwHash, &created,
		&revoked, &lastAt, &lastIP, &s.AccessCount)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	s.CreatedAt = parseTime(created)
	if exp.Valid {
		t := parseTime(exp.String)
		s.ExpiresAt = &t
	}
	if revoked.Valid {
		t := parseTime(revoked.String)
		s.RevokedAt = &t
	}
	if lastAt.Valid {
		t := parseTime(lastAt.String)
		s.LastAccessedAt = &t
	}
	s.LastAccessedIP = lastIP.String
	s.passwordHash = pwHash.String
	s.HasPassword = pwHash.String != ""
	return &s, nil
}

const shareTokenCols = `id, view_id, label, expires_at, password_hash, created_at,
	revoked_at, last_accessed_at, last_accessed_ip, access_count`

// ShareTokenBySecret resolves a token by its cleartext secret.
func (db *DB) ShareTokenBySecret(ctx context.Context, secret string) (*ShareToken, error) {
	return scanShareToken(db.QueryRowContext(ctx,
		"SELECT "+shareTokenCols+" FROM share_tokens WHERE token_hash = ?", HashShareToken(secret)))
}

// CheckSharePassword compares a supplied password against the token's hash.
func (s *ShareToken) CheckSharePassword(password string) bool {
	if s.passwordHash == "" {
		return true
	}
	return bcrypt.CompareHashAndPassword([]byte(s.passwordHash), []byte(password)) == nil
}

// Usable reports whether the token may currently serve content.
func (s *ShareToken) Usable(now time.Time) bool {
	if s.RevokedAt != nil {
		return false
	}
	if s.ExpiresAt != nil && now.After(*s.ExpiresAt) {
		return false
	}
	return true
}

// TokensForView lists a view's tokens, newest first.
func (db *DB) TokensForView(ctx context.Context, viewID int64) ([]ShareToken, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+shareTokenCols+" FROM share_tokens WHERE view_id = ? ORDER BY id DESC", viewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShareToken
	for rows.Next() {
		s, err := scanShareToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// ListAllShareTokens lists every token across all views (for the CLI).
func (db *DB) ListAllShareTokens(ctx context.Context) ([]ShareToken, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+shareTokenCols+" FROM share_tokens ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShareToken
	for rows.Next() {
		s, err := scanShareToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// RevokeShareToken marks a token revoked.
func (db *DB) RevokeShareToken(ctx context.Context, id int64) error {
	res, err := db.ExecContext(ctx, "UPDATE share_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL", nowUTC(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchShareToken records an access (count, time, IP).
func (db *DB) TouchShareToken(ctx context.Context, id int64, ip string) error {
	_, err := db.ExecContext(ctx,
		"UPDATE share_tokens SET access_count = access_count + 1, last_accessed_at = ?, last_accessed_ip = ? WHERE id = ?",
		nowUTC(), nullString(ip), id)
	return err
}
