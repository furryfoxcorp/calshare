package share

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/furryfoxcorp/calshare/internal/storage"
)

func event(uid, summary string) string {
	return fmt.Sprintf("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:%s\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260605T090000Z\r\nDTEND:20260605T100000Z\r\nSUMMARY:%s\r\nLOCATION:Secret place\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n", uid, summary)
}

type setup struct {
	srv    *Server
	db     *storage.DB
	view   *storage.View
	secret string
}

func newSetup(t *testing.T, password string) *setup {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "share.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u, _ := db.UpsertUserOnLogin(ctx, "sub", "owner@example.com", "Owner")
	cal, _ := db.CreateCalendar(ctx, &storage.Calendar{UserID: u.ID, DisplayName: "Home"})
	db.PutObject(ctx, cal.ID, "e1.ics", []byte(event("e1", "Therapy")))

	view, _ := db.CreateView(ctx, &storage.View{UserID: u.ID, Name: "Zoe view", Preset: "busy", BusyLabel: "Busy"})
	db.SetViewCalendar(ctx, &storage.ViewCalendar{ViewID: view.ID, CalendarID: cal.ID})

	secret, _ := storage.NewShareTokenSecret()
	var exp *time.Time
	if _, err := db.CreateShareToken(ctx, view.ID, "Zoe", secret, password, exp); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(db)
	srv.now = func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }
	return &setup{srv: srv, db: db, view: view, secret: secret}
}

func (s *setup) get(t *testing.T, path string, basic bool, pass string) *http.Response {
	t.Helper()
	mux := http.NewServeMux()
	s.srv.Register(mux)
	req := httptest.NewRequest("GET", path, nil)
	if basic {
		req.SetBasicAuth("ignored", pass)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w.Result()
}

func TestShareServesFilteredCalendar(t *testing.T) {
	s := newSetup(t, "")
	res := s.get(t, "/share/"+s.secret+".ics", false, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	raw, _ := io.ReadAll(res.Body)
	body := string(raw)
	if !contains(body, "SUMMARY:Busy") {
		t.Errorf("busy label missing:\n%s", body)
	}
	if contains(body, "Therapy") || contains(body, "Secret place") {
		t.Errorf("private detail leaked:\n%s", body)
	}
	if res.Header.Get("ETag") == "" {
		t.Error("missing ETag")
	}
}

func TestShareUnknownTokenIs404(t *testing.T) {
	s := newSetup(t, "")
	res := s.get(t, "/share/doesnotexist.ics", false, "")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}

func TestShareRevokedIs404(t *testing.T) {
	s := newSetup(t, "")
	tokens, _ := s.db.TokensForView(context.Background(), s.view.ID)
	s.db.RevokeShareToken(context.Background(), tokens[0].ID)
	res := s.get(t, "/share/"+s.secret+".ics", false, "")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}

func TestSharePasswordGate(t *testing.T) {
	s := newSetup(t, "hunter2")
	res := s.get(t, "/share/"+s.secret+".ics", false, "")
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no password: status = %d, want 401", res.StatusCode)
	}
	res = s.get(t, "/share/"+s.secret+".ics", true, "wrong")
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: status = %d, want 401", res.StatusCode)
	}
	res = s.get(t, "/share/"+s.secret+".ics", true, "hunter2")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("correct password: status = %d, want 200", res.StatusCode)
	}
}

func TestShareRateLimit(t *testing.T) {
	s := newSetup(t, "")
	var last int
	for i := 0; i < 65; i++ {
		res := s.get(t, "/share/"+s.secret+".ics", false, "")
		last = res.StatusCode
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after burst, got %d", last)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

func TestShareEmptyViewServesValidCalendar(t *testing.T) {
	// A view with no calendars must serve a valid empty calendar, not 500.
	db, err := storage.Open(filepath.Join(t.TempDir(), "empty.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	db.Migrate(ctx)
	u, _ := db.UpsertUserOnLogin(ctx, "s", "owner@example.com", "Owner")
	view, _ := db.CreateView(ctx, &storage.View{UserID: u.ID, Name: "Empty", Preset: "titles"})
	secret, _ := storage.NewShareTokenSecret()
	db.CreateShareToken(ctx, view.ID, "x", secret, "", nil)

	srv := NewServer(db)
	mux := http.NewServeMux()
	srv.Register(mux)
	req := httptest.NewRequest("GET", "/share/"+secret+".ics", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	out, _ := io.ReadAll(res.Body)
	if !contains(string(out), "BEGIN:VCALENDAR") || !contains(string(out), "END:VCALENDAR") {
		t.Errorf("not a valid empty calendar:\n%s", out)
	}
}
