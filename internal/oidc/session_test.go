package oidc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/furryfoxcorp/calshare/internal/storage"
)

func TestSignVerifyRoundtrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	signed := signValue(key, "session-id-123")
	got, ok := verifyValue(key, signed)
	if !ok || got != "session-id-123" {
		t.Fatalf("verify failed: got %q ok=%v", got, ok)
	}
}

func TestVerifyRejectsTamper(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	signed := signValue(key, "session-id-123")
	if _, ok := verifyValue(key, signed+"x"); ok {
		t.Error("tampered signature accepted")
	}
	if _, ok := verifyValue(key, "no-dot-here"); ok {
		t.Error("malformed value accepted")
	}
	other := []byte("ffffffffffffffffffffffffffffffff")
	if _, ok := verifyValue(other, signed); ok {
		t.Error("signature from a different key accepted")
	}
}

func newDB(t *testing.T) (*storage.DB, *storage.User) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "s.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	u, err := db.UpsertUserOnLogin(context.Background(), "sub", "u@example.com", "U")
	if err != nil {
		t.Fatal(err)
	}
	return db, u
}

func TestSessionMiddlewareRoundtrip(t *testing.T) {
	db, u := newDB(t)
	m := NewManager(db, []byte("0123456789abcdef0123456789abcdef"), false)

	// Start a session and capture the cookie.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	if err := m.StartSession(rec, req, u.ID); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	cookie := rec.Result().Cookies()[0]
	if cookie.Name != SessionCookie {
		t.Fatalf("cookie name = %q", cookie.Name)
	}

	// A protected handler should see the user.
	var sawUser *storage.User
	protected := m.RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, ok := UserFromContext(r.Context()); ok {
			sawUser = user
		}
		w.WriteHeader(http.StatusOK)
	}))

	req2 := httptest.NewRequest("GET", "/dashboard", nil)
	req2.AddCookie(cookie)
	rec2 := httptest.NewRecorder()
	protected.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec2.Code)
	}
	if sawUser == nil || sawUser.ID != u.ID {
		t.Fatal("middleware did not inject the user")
	}
}

func TestRequireUserRedirectsWithoutSession(t *testing.T) {
	db, _ := newDB(t)
	m := NewManager(db, []byte("0123456789abcdef0123456789abcdef"), false)
	protected := m.RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, httptest.NewRequest("GET", "/dashboard", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 redirect", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("redirect to %q, want /login", loc)
	}
}

func TestClearSessionLogsOut(t *testing.T) {
	db, u := newDB(t)
	m := NewManager(db, []byte("0123456789abcdef0123456789abcdef"), false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	m.StartSession(rec, req, u.ID)
	cookie := rec.Result().Cookies()[0]

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/logout", nil)
	req2.AddCookie(cookie)
	m.ClearSession(rec2, req2)

	// The session must no longer resolve.
	req3 := httptest.NewRequest("GET", "/dashboard", nil)
	req3.AddCookie(cookie)
	if _, ok := m.lookup(req3); ok {
		t.Error("session still valid after ClearSession")
	}
}
