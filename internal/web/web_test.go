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

	srv := NewServer(db, sessions, nil, nil, nil, nil, "https://calendar.example.com")
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

func TestCalendarDetailAndShare(t *testing.T) {
	h := setup(t)
	cal, _ := h.db.CreateCalendar(context.Background(), &storage.Calendar{UserID: h.user.ID, DisplayName: "Home"})
	grantee, _ := h.db.UpsertUserOnLogin(context.Background(), "z", "zoe@example.com", "Zoe")

	detail := h.req(t, "GET", "/calendars/"+itoa(cal.ID), "")
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d", detail.Code)
	}
	if !strings.Contains(detail.Body.String(), "Shared with") {
		t.Error("detail page missing sharing section")
	}

	share := h.req(t, "POST", "/calendars/"+itoa(cal.ID)+"/share", "email=zoe@example.com&privilege=read-write")
	if share.Code != http.StatusSeeOther {
		t.Fatalf("share status = %d", share.Code)
	}
	level, _ := h.db.AccessLevel(context.Background(), cal.ID, grantee.ID)
	if level != storage.AccessReadWrite {
		t.Errorf("grant level = %q, want read-write", level)
	}
}

func TestGenPassword(t *testing.T) {
	h := setup(t)
	rec := h.req(t, "POST", "/genpw", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `id="password"`) || !strings.Contains(rec.Body.String(), "value=") {
		t.Errorf("genpw did not return a filled input:\n%s", rec.Body.String())
	}
}

func TestProfileUpdate(t *testing.T) {
	h := setup(t)
	rec := h.req(t, "POST", "/profile", "display_tz=Europe/London")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	u, _ := h.db.UserByID(context.Background(), h.user.ID)
	if u.DisplayTZ != "Europe/London" {
		t.Errorf("display tz = %q, want Europe/London", u.DisplayTZ)
	}
}

func TestInboxRenders(t *testing.T) {
	h := setup(t)
	if rec := h.req(t, "GET", "/inbox", ""); rec.Code != http.StatusOK {
		t.Fatalf("inbox status = %d", rec.Code)
	}
}

func TestAdminAccess(t *testing.T) {
	h := setup(t)
	// Non-admin is forbidden.
	if rec := h.req(t, "GET", "/admin", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin admin status = %d, want 403", rec.Code)
	}
	// After promotion, a fresh session reflects the admin flag and the page renders.
	h.db.SetAdmin(context.Background(), h.user.Email, true)
	rec := httptest.NewRecorder()
	sessions := oidc.NewManager(h.db, []byte("0123456789abcdef0123456789abcdef"), false)
	sessions.StartSession(rec, httptest.NewRequest("GET", "/", nil), h.user.ID)
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequest("GET", "/admin", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin (as admin) status = %d, want 200", w.Code)
	}
}

func TestRevokeTokenReflectedInList(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	v, _ := h.db.CreateView(ctx, &storage.View{UserID: h.user.ID, Name: "V", Preset: "busy"})
	secret, _ := storage.NewShareTokenSecret()
	id, _ := h.db.CreateShareToken(ctx, v.ID, "Zoe", secret, "", nil)

	rec := h.req(t, "POST", "/tokens/"+itoa(id)+"/revoke", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	out := rec.Body.String()
	// A revoked token disappears from the list entirely.
	if strings.Contains(out, "/tokens/"+itoa(id)+"/revoke") {
		t.Errorf("revoked token still shown in list:\n%s", out)
	}
	if !strings.Contains(out, "No share links yet") {
		t.Errorf("expected empty list after revoking the only token:\n%s", out)
	}
	tok, _ := h.db.TokensForView(ctx, v.ID)
	if tok[0].RevokedAt == nil {
		t.Error("token not revoked in DB")
	}
}

func TestUpdateViewFields(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	// Full preset shows everything but strips alerts by default.
	v, _ := h.db.CreateView(ctx, &storage.View{UserID: h.user.ID, Name: "V", Preset: "full"})

	// Override: re-enable VALARM (show alerts) and hide LOCATION.
	form := "field_VALARM=keep&field_LOCATION=strip"
	// Include the other fields at their full-preset defaults so they aren't recorded as deltas.
	rec := h.req(t, "POST", "/views/"+itoa(v.ID)+"/fields", form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	updated, _ := h.db.ViewByID(ctx, v.ID)
	if !strings.Contains(updated.FieldsJSON, "VALARM") || !strings.Contains(updated.FieldsJSON, "keep") {
		t.Errorf("VALARM override not saved: %s", updated.FieldsJSON)
	}
	if !strings.Contains(updated.FieldsJSON, "LOCATION") {
		t.Errorf("LOCATION override not saved: %s", updated.FieldsJSON)
	}
	// The detail page should reflect the overrides.
	detail := h.req(t, "GET", "/views/"+itoa(v.ID), "")
	if !strings.Contains(detail.Body.String(), "Field details") {
		t.Error("field editor not rendered")
	}
}
