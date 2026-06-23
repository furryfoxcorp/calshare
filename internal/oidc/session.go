package oidc

import (
	"context"
	"net/http"
	"time"

	"github.com/furryfoxcorp/calshare/internal/storage"
)

// SessionCookie is the name of the web session cookie.
const SessionCookie = "caldav_session"

// SessionTTL is the sliding session lifetime.
const SessionTTL = 30 * 24 * time.Hour

type ctxKey int

const userCtxKey ctxKey = iota

// Manager creates, validates, and clears web sessions.
type Manager struct {
	db     *storage.DB
	key    []byte
	secure bool
}

// NewManager builds a session manager. key signs the cookie; secure marks the
// cookie Secure (set false only for local plaintext testing).
func NewManager(db *storage.DB, key []byte, secure bool) *Manager {
	return &Manager{db: db, key: key, secure: secure}
}

// StartSession creates a session for userID and writes the cookie.
func (m *Manager) StartSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	sess, err := m.db.CreateSession(r.Context(), userID, SessionTTL, r.UserAgent(), clientIP(r))
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    signValue(m.key, sess.ID),
		Path:     "/",
		Expires:  sess.ExpiresAt,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// ClearSession deletes the current session and expires the cookie.
func (m *Manager) ClearSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil {
		if id, ok := verifyValue(m.key, c.Value); ok {
			_ = m.db.DeleteSession(r.Context(), id)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// lookup resolves the request's session and user, sliding the expiry.
func (m *Manager) lookup(r *http.Request) (*storage.User, bool) {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return nil, false
	}
	id, ok := verifyValue(m.key, c.Value)
	if !ok {
		return nil, false
	}
	sess, err := m.db.SessionByID(r.Context(), id)
	if err != nil {
		return nil, false
	}
	user, err := m.db.UserByID(r.Context(), sess.UserID)
	if err != nil {
		return nil, false
	}
	_ = m.db.TouchSession(r.Context(), id, SessionTTL, clientIP(r))
	return user, true
}

// RequireUser is middleware that enforces an authenticated session, redirecting
// to /login otherwise.
func (m *Manager) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := m.lookup(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), user)))
	})
}

// OptionalUser injects the user into the context when present, without
// requiring it.
func (m *Manager) OptionalUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, ok := m.lookup(r); ok {
			r = r.WithContext(withUser(r.Context(), user))
		}
		next.ServeHTTP(w, r)
	})
}

func withUser(ctx context.Context, u *storage.User) context.Context {
	return context.WithValue(ctx, userCtxKey, u)
}

// UserFromContext returns the session user, if any.
func UserFromContext(ctx context.Context) (*storage.User, bool) {
	u, ok := ctx.Value(userCtxKey).(*storage.User)
	return u, ok
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := indexByte(xff, ','); i >= 0 {
			return trim(xff[:i])
		}
		return trim(xff)
	}
	return r.RemoteAddr
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func trim(s string) string {
	i, j := 0, len(s)
	for i < j && s[i] == ' ' {
		i++
	}
	for j > i && s[j-1] == ' ' {
		j--
	}
	return s[i:j]
}
