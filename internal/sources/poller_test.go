package sources

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/furryfoxcorp/calshare/internal/secret"
	"github.com/furryfoxcorp/calshare/internal/storage"
)

const feedV1 = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//feed//EN
BEGIN:VEVENT
UID:holiday-1@feed
DTSTAMP:20260101T000000Z
DTSTART:20260704T000000Z
SUMMARY:Independence Day
END:VEVENT
BEGIN:VEVENT
UID:holiday-2@feed
DTSTAMP:20260101T000000Z
DTSTART:20261225T000000Z
SUMMARY:Christmas
END:VEVENT
END:VCALENDAR
`

const feedV2 = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//feed//EN
BEGIN:VEVENT
UID:holiday-1@feed
DTSTAMP:20260101T000000Z
DTSTART:20260704T000000Z
SUMMARY:Independence Day (observed)
END:VEVENT
END:VCALENDAR
`

func newPollerDB(t *testing.T, url string) (*storage.DB, *storage.Calendar) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "src.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u, _ := db.UpsertUserOnLogin(ctx, "s", "u@example.com", "U")
	cal, _ := db.CreateCalendar(ctx, &storage.Calendar{UserID: u.ID, SourceType: "ics", DisplayName: "Holidays", ICSURL: url})
	return db, cal
}

func TestPollImportsEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		io.WriteString(w, feedV1)
	}))
	defer srv.Close()
	db, cal := newPollerDB(t, srv.URL)
	p := New(db, 15*time.Minute, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := p.PollOnce(context.Background(), cal); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	objs, _ := db.ListObjects(context.Background(), cal.ID)
	if len(objs) != 2 {
		t.Fatalf("imported %d objects, want 2", len(objs))
	}
	fresh, _ := db.CalendarByID(context.Background(), cal.ID)
	if fresh.ICSETag != `"v1"` || fresh.ICSLastStatus != "ok" {
		t.Errorf("poll state not recorded: etag=%q status=%q", fresh.ICSETag, fresh.ICSLastStatus)
	}
}

func TestPollDeletesRemovedEvents(t *testing.T) {
	feed := feedV1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, feed)
	}))
	defer srv.Close()
	db, cal := newPollerDB(t, srv.URL)
	p := New(db, 15*time.Minute, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := p.PollOnce(context.Background(), cal); err != nil {
		t.Fatal(err)
	}
	feed = feedV2 // second poll: one event removed, one changed
	if err := p.PollOnce(context.Background(), cal); err != nil {
		t.Fatal(err)
	}
	objs, _ := db.ListObjects(context.Background(), cal.ID)
	if len(objs) != 1 {
		t.Fatalf("after update got %d objects, want 1", len(objs))
	}
}

func TestPollNotModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		io.WriteString(w, feedV1)
	}))
	defer srv.Close()
	db, cal := newPollerDB(t, srv.URL)
	p := New(db, 15*time.Minute, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	p.PollOnce(context.Background(), cal)
	fresh, _ := db.CalendarByID(context.Background(), cal.ID)
	if err := p.PollOnce(context.Background(), fresh); err != nil {
		t.Fatal(err)
	}
	fresh2, _ := db.CalendarByID(context.Background(), cal.ID)
	if fresh2.ICSLastStatus != "not_modified" {
		t.Errorf("status = %q, want not_modified", fresh2.ICSLastStatus)
	}
}

func TestPollHTTPErrorKeepsEvents(t *testing.T) {
	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "fail" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		io.WriteString(w, feedV1)
	}))
	defer srv.Close()
	db, cal := newPollerDB(t, srv.URL)
	p := New(db, 15*time.Minute, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	p.PollOnce(context.Background(), cal)
	mode = "fail"
	fresh, _ := db.CalendarByID(context.Background(), cal.ID)
	if err := p.PollOnce(context.Background(), fresh); err == nil {
		t.Fatal("expected error on 500")
	}
	objs, _ := db.ListObjects(context.Background(), cal.ID)
	if len(objs) != 2 {
		t.Fatalf("events should survive an HTTP error, got %d", len(objs))
	}
}

func TestPollSendsBasicAuth(t *testing.T) {
	gotUser, gotPass := "", ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		io.WriteString(w, feedV1)
	}))
	defer srv.Close()

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	enc, err := secret.Encrypt(key, []byte("feedpass"))
	if err != nil {
		t.Fatal(err)
	}

	db, err := storage.Open(filepath.Join(t.TempDir(), "ba.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	db.Migrate(ctx)
	u, _ := db.UpsertUserOnLogin(ctx, "s", "u@example.com", "U")
	cal, _ := db.CreateCalendar(ctx, &storage.Calendar{
		UserID: u.ID, SourceType: "ics", DisplayName: "Private feed",
		ICSURL: srv.URL, ICSBasicUser: "feeduser", ICSBasicPassEnc: enc,
	})

	p := New(db, 15*time.Minute, key, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := p.PollOnce(ctx, cal); err != nil {
		t.Fatal(err)
	}
	if gotUser != "feeduser" || gotPass != "feedpass" {
		t.Errorf("basic auth = %q:%q, want feeduser:feedpass", gotUser, gotPass)
	}
}

func TestBackoffGrowsWithFailures(t *testing.T) {
	p := New(nil, 15*time.Minute, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	base := 15 * time.Minute
	cases := []struct {
		fails int
		want  time.Duration
	}{
		{0, base},
		{1, 2 * base},
		{2, 4 * base},
		{10, maxBackoff}, // capped
	}
	for _, c := range cases {
		got := p.interval(&storage.Calendar{ICSPollInterval: 900, ICSFailCount: c.fails})
		if got != c.want {
			t.Errorf("failCount %d: interval = %v, want %v", c.fails, got, c.want)
		}
	}
}

func TestDueRespectsBackoff(t *testing.T) {
	p := New(nil, 15*time.Minute, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Now().UTC()
	last := now.Add(-30 * time.Minute)
	// With one failure, backoff is 30m, so a 30m-old poll is exactly due.
	cal := &storage.Calendar{ICSURL: "x", ICSPollInterval: 900, ICSFailCount: 1, ICSLastPolledAt: &last}
	if !p.due(cal, now) {
		t.Error("should be due after backoff elapsed")
	}
	cal.ICSFailCount = 5 // backoff far longer than 30m
	if p.due(cal, now) {
		t.Error("should not be due while backing off")
	}
}

func TestNormalizeFeedURL(t *testing.T) {
	cases := map[string]string{
		"webcal://p149-caldav.icloud.com/published/2/abc":  "https://p149-caldav.icloud.com/published/2/abc",
		"webcals://example.com/cal.ics":                    "https://example.com/cal.ics",
		"https://example.com/cal.ics":                      "https://example.com/cal.ics",
		"http://example.com/cal.ics":                       "http://example.com/cal.ics",
	}
	for in, want := range cases {
		if got := NormalizeFeedURL(in); got != want {
			t.Errorf("NormalizeFeedURL(%q) = %q, want %q", in, got, want)
		}
	}
}

