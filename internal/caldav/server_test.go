package caldav

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/furryfoxcorp/calshare/internal/storage"
)

const testEmail = "owner@example.com"
const testPass = "device-pass-123456"

type fixture struct {
	srv  *Server
	db   *storage.DB
	user *storage.User
	cal  *storage.Calendar
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dav.sqlite")
	db, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	u, err := db.UpsertUserOnLogin(ctx, "sub", testEmail, "Owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAppPassword(ctx, u.ID, "Mac", testPass); err != nil {
		t.Fatal(err)
	}
	cal, err := db.CreateCalendar(ctx, &storage.Calendar{UserID: u.ID, DisplayName: "Home", SupportsVTODO: true})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{srv: NewServer(db, "/dav", false, nil), db: db, user: u, cal: cal}
}

func (f *fixture) do(t *testing.T, method, path, body string, headers map[string]string) *http.Response {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.SetBasicAuth(testEmail, testPass)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(w, req)
	return w.Result()
}

func body(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return string(b)
}

func eventICS(uid string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//c//EN\r\nBEGIN:VEVENT\r\nUID:" + uid +
		"\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260105T090000Z\r\nDTEND:20260105T100000Z\r\nSUMMARY:Standup\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
}

func TestUnauthenticated(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest("PROPFIND", "/dav/", nil)
	w := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(w, req)
	res := w.Result()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}
	if !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Basic") {
		t.Error("missing Basic challenge")
	}
}

func TestPropFindRootPrincipal(t *testing.T) {
	f := newFixture(t)
	pf := `<?xml version="1.0"?><d:propfind xmlns:d="DAV:"><d:prop><d:current-user-principal/></d:prop></d:propfind>`
	res := f.do(t, "PROPFIND", "/dav/", pf, map[string]string{"Depth": "0", "Content-Type": "application/xml"})
	got := body(t, res)
	if !strings.Contains(got, "/dav/"+testEmail+"/") {
		t.Errorf("principal path not in response:\n%s", got)
	}
}

func TestPropFindHomeSetListsCalendars(t *testing.T) {
	f := newFixture(t)
	pf := `<?xml version="1.0"?><d:propfind xmlns:d="DAV:"><d:prop><d:displayname/><d:resourcetype/></d:prop></d:propfind>`
	res := f.do(t, "PROPFIND", "/dav/"+testEmail+"/calendars/", pf, map[string]string{"Depth": "1", "Content-Type": "application/xml"})
	got := body(t, res)
	if !strings.Contains(got, f.cal.Slug) {
		t.Errorf("calendar slug %q not listed:\n%s", f.cal.Slug, got)
	}
	if !strings.Contains(got, "Home") {
		t.Error("calendar display name not listed")
	}
}

func TestPutGetDeleteRoundtrip(t *testing.T) {
	f := newFixture(t)
	objPath := "/dav/" + testEmail + "/calendars/" + f.cal.Slug + "/ev1.ics"

	put := f.do(t, "PUT", objPath, eventICS("ev1"), map[string]string{"Content-Type": "text/calendar"})
	if put.StatusCode != http.StatusCreated && put.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT status = %d, body=%s", put.StatusCode, body(t, put))
	}

	get := f.do(t, "GET", objPath, "", nil)
	if get.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d", get.StatusCode)
	}
	gotBody := body(t, get)
	if !strings.Contains(gotBody, "UID:ev1") {
		t.Errorf("GET body missing event:\n%s", gotBody)
	}
	if get.Header.Get("ETag") == "" {
		t.Error("GET missing ETag")
	}

	del := f.do(t, "DELETE", objPath, "", nil)
	if del.StatusCode != http.StatusNoContent && del.StatusCode != http.StatusOK {
		t.Fatalf("DELETE status = %d", del.StatusCode)
	}
	get2 := f.do(t, "GET", objPath, "", nil)
	if get2.StatusCode != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", get2.StatusCode)
	}
}

func TestIfNoneMatchConflict(t *testing.T) {
	f := newFixture(t)
	objPath := "/dav/" + testEmail + "/calendars/" + f.cal.Slug + "/ev2.ics"
	f.do(t, "PUT", objPath, eventICS("ev2"), map[string]string{"Content-Type": "text/calendar"})

	res := f.do(t, "PUT", objPath, eventICS("ev2"), map[string]string{
		"Content-Type":  "text/calendar",
		"If-None-Match": "*",
	})
	if res.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412", res.StatusCode)
	}
}

func TestCalendarQueryReport(t *testing.T) {
	f := newFixture(t)
	objPath := "/dav/" + testEmail + "/calendars/" + f.cal.Slug + "/ev3.ics"
	f.do(t, "PUT", objPath, eventICS("ev3"), map[string]string{"Content-Type": "text/calendar"})

	report := `<?xml version="1.0"?>
<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:prop><d:getetag/><c:calendar-data/></d:prop>
  <c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VEVENT"/></c:comp-filter></c:filter>
</c:calendar-query>`
	res := f.do(t, "REPORT", "/dav/"+testEmail+"/calendars/"+f.cal.Slug+"/", report, map[string]string{"Depth": "1", "Content-Type": "application/xml"})
	got := body(t, res)
	if !strings.Contains(got, "ev3.ics") {
		t.Errorf("REPORT did not return the event:\n%s", got)
	}
}

func TestOptionsAdvertisesCalendarAccess(t *testing.T) {
	f := newFixture(t)
	res := f.do(t, "OPTIONS", "/dav/"+testEmail+"/calendars/"+f.cal.Slug+"/", "", nil)
	dav := res.Header.Get("DAV")
	if !strings.Contains(dav, "calendar-access") {
		t.Errorf("OPTIONS DAV header = %q, want calendar-access", dav)
	}
}
