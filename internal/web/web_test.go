package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/furryfoxcorp/calshare/internal/oidc"
	"github.com/furryfoxcorp/calshare/internal/storage"
)

type harness struct {
	mux    *http.ServeMux
	db     *storage.DB
	cookie *http.Cookie
	user   *storage.User
}

func setup(t *testing.T) *harness {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "web.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	u, err := db.UpsertUserOnLogin(context.Background(), "sub", "owner@example.com", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	sessions := oidc.NewManager(db, key, false)

	srv := NewServer(db, sessions, nil, nil, "https://vulpes.calshare.fyi")
	mux := http.NewServeMux()
	srv.Register(mux)

	// Mint a session cookie for the user.
	rec := httptest.NewRecorder()
	if err := sessions.StartSession(rec, httptest.NewRequest("GET", "/", nil), u.ID); err != nil {
		t.Fatal(err)
	}
	return &harness{mux: mux, db: db, cookie: rec.Result().Cookies()[0], user: u}
}

func (h *harness) req(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.AddCookie(h.cookie)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, r)
	return rec
}

func TestLoginPageRendersLion(t *testing.T) {
	h := setup(t)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/login", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	out := rec.Body.String()
	for _, want := range []string{"leather", "Sign in with SSO", "calshare"} {
		if !strings.Contains(out, want) {
			t.Errorf("login page missing %q", want)
		}
	}
}

func TestDashboardRequiresAuth(t *testing.T) {
	h := setup(t)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil)) // no cookie
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want redirect", rec.Code)
	}
}

func TestDashboardRenders(t *testing.T) {
	h := setup(t)
	rec := h.req(t, "GET", "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	out := rec.Body.String()
	if !strings.Contains(out, "Owner") || !strings.Contains(out, "Home") {
		t.Errorf("dashboard missing expected content:\n%s", out[:min(400, len(out))])
	}
}

func TestCreateAndDeleteCalendar(t *testing.T) {
	h := setup(t)
	rec := h.req(t, "POST", "/calendars", "name=Home&color=%23d24b3a&tasks=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Home") {
		t.Errorf("calendar list missing new calendar:\n%s", rec.Body.String())
	}

	cals, _ := h.db.CalendarsForUser(context.Background(), h.user.ID)
	if len(cals) != 1 {
		t.Fatalf("got %d calendars, want 1", len(cals))
	}

	del := h.req(t, "POST", "/calendars/"+itoa(cals[0].ID)+"/delete", "")
	if del.Code != http.StatusOK {
		t.Fatalf("delete status = %d", del.Code)
	}
	cals, _ = h.db.CalendarsForUser(context.Background(), h.user.ID)
	if len(cals) != 0 {
		t.Fatalf("calendar not deleted, %d remain", len(cals))
	}
}

func TestCreateDeviceShowsPasswordOnce(t *testing.T) {
	h := setup(t)
	rec := h.req(t, "POST", "/devices", "label=iPhone")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	out := rec.Body.String()
	if !strings.Contains(out, "credential") {
		t.Errorf("device creation did not reveal a password:\n%s", out)
	}
	if !strings.Contains(out, "device-list") {
		t.Error("device creation did not include an out-of-band list refresh")
	}
	devices, _ := h.db.AppPasswordsForUser(context.Background(), h.user.ID)
	if len(devices) != 1 {
		t.Fatalf("got %d devices, want 1", len(devices))
	}
}

func TestRevokeDevice(t *testing.T) {
	h := setup(t)
	id, _ := h.db.CreateAppPassword(context.Background(), h.user.ID, "Mac", "pw-aaaaaa")
	rec := h.req(t, "POST", "/devices/"+itoa(id)+"/revoke", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	devices := []storage.AppPassword{}
	all, _ := h.db.AppPasswordsForUser(context.Background(), h.user.ID)
	for _, d := range all {
		if d.RevokedAt == nil {
			devices = append(devices, d)
		}
	}
	if len(devices) != 0 {
		t.Fatalf("device not revoked")
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

func TestCreateViewRedirectsToDetail(t *testing.T) {
	h := setup(t)
	rec := h.req(t, "POST", "/views", "name=Zoe+view&preset=busy")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/views/") {
		t.Fatalf("redirect = %q", loc)
	}
	detail := h.req(t, "GET", loc, "")
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d", detail.Code)
	}
	if !strings.Contains(detail.Body.String(), "Zoe view") || !strings.Contains(detail.Body.String(), "Share links") {
		t.Errorf("view detail missing expected content")
	}
}

func TestMintShareToken(t *testing.T) {
	h := setup(t)
	v, _ := h.db.CreateView(context.Background(), &storage.View{UserID: h.user.ID, Name: "V", Preset: "busy"})
	rec := h.req(t, "POST", "/views/"+itoa(v.ID)+"/tokens", "label=Zoe")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	out := rec.Body.String()
	if !strings.Contains(out, "webcal://") || !strings.Contains(out, "/share/") {
		t.Errorf("token reveal missing subscribe link:\n%s", out)
	}
	toks, _ := h.db.TokensForView(context.Background(), v.ID)
	if len(toks) != 1 {
		t.Fatalf("got %d tokens, want 1", len(toks))
	}
}

func TestToggleViewCalendar(t *testing.T) {
	h := setup(t)
	cal, _ := h.db.CreateCalendar(context.Background(), &storage.Calendar{UserID: h.user.ID, DisplayName: "Home"})
	v, _ := h.db.CreateView(context.Background(), &storage.View{UserID: h.user.ID, Name: "V", Preset: "busy"})

	add := h.req(t, "POST", "/views/"+itoa(v.ID)+"/calendars", "calendar_id="+itoa(cal.ID))
	if add.Code != http.StatusNoContent {
		t.Fatalf("toggle on status = %d", add.Code)
	}
	vcs, _ := h.db.ViewCalendars(context.Background(), v.ID)
	if len(vcs) != 1 {
		t.Fatalf("calendar not added to view")
	}
	h.req(t, "POST", "/views/"+itoa(v.ID)+"/calendars", "calendar_id="+itoa(cal.ID))
	vcs, _ = h.db.ViewCalendars(context.Background(), v.ID)
	if len(vcs) != 0 {
		t.Fatalf("calendar not removed on second toggle")
	}
}

func TestAddSource(t *testing.T) {
	h := setup(t)
	rec := h.req(t, "POST", "/sources", "url=https%3A%2F%2Fexample.com%2Ffeed.ics&name=Holidays&interval=30")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Holidays") {
		t.Errorf("source list missing new source:\n%s", rec.Body.String())
	}
	cals, _ := h.db.CalendarsForUser(context.Background(), h.user.ID)
	found := false
	for _, c := range cals {
		if c.SourceType == "ics" && c.ICSURL == "https://example.com/feed.ics" && c.ICSPollInterval == 1800 {
			found = true
		}
	}
	if !found {
		t.Error("ICS calendar not created with expected fields")
	}
}
