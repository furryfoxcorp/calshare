package caldav

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/furryfoxcorp/calshare/internal/ical"
	"github.com/furryfoxcorp/calshare/internal/storage"
)

// intercept handles the REPORT types go-webdav does not implement
// (free-busy-query and principal-property-search) and delegates everything
// else to the wrapped handler.
func (s *Server) intercept(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Augment PROPFIND on the user principal with the scheduling and Apple
		// properties go-webdav does not emit.
		if r.Method == "PROPFIND" && s.isPrincipalPath(r.URL.Path) {
			s.handlePrincipalPropfind(w, r)
			return
		}
		if r.Method != "REPORT" {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		root := reportRoot(body)
		// Restore the body for the delegate path.
		r.Body = io.NopCloser(bytes.NewReader(body))

		switch root {
		case "free-busy-query":
			s.handleFreeBusy(w, r, body)
		case "principal-property-search":
			s.handlePrincipalSearch(w, r, body)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// isPrincipalPath reports whether path is exactly a user principal
// (prefix/<email>/), as opposed to the root or the calendar home set.
func (s *Server) isPrincipalPath(path string) bool {
	trimmed := strings.Trim(strings.TrimPrefix(strings.TrimSuffix(path, "/"), s.prefix), "/")
	if trimmed == "" {
		return false
	}
	return !strings.Contains(trimmed, "/") // exactly one segment: the email
}

// handlePrincipalPropfind serves a principal PROPFIND with the full property
// set Apple Calendar and DAVx5 need to enable scheduling.
func (s *Server) handlePrincipalPropfind(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Only serve the authenticated user's own principal.
	p := s.backend.parsePath(r.URL.Path)
	if normalizeLower(p.email) != normalizeLower(user.Email) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	principal := s.backend.principalPath(user.Email)
	home := s.backend.homeSetPath(user.Email)
	inbox := principal + "inbox/"
	outbox := principal + "outbox/"

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav" xmlns:CS="http://calendarserver.org/ns/">` + "\n")
	fmt.Fprintf(&b, "  <D:response>\n    <D:href>%s</D:href>\n    <D:propstat>\n      <D:prop>\n", xmlEscape(principal))
	b.WriteString("        <D:resourcetype><D:principal/><D:collection/></D:resourcetype>\n")
	fmt.Fprintf(&b, "        <D:displayname>%s</D:displayname>\n", xmlEscape(user.DisplayName))
	fmt.Fprintf(&b, "        <D:current-user-principal><D:href>%s</D:href></D:current-user-principal>\n", xmlEscape(principal))
	fmt.Fprintf(&b, "        <D:principal-URL><D:href>%s</D:href></D:principal-URL>\n", xmlEscape(principal))
	fmt.Fprintf(&b, "        <C:calendar-home-set><D:href>%s</D:href></C:calendar-home-set>\n", xmlEscape(home))
	fmt.Fprintf(&b, "        <C:calendar-user-address-set>\n          <D:href>mailto:%s</D:href>\n          <D:href>%s</D:href>\n        </C:calendar-user-address-set>\n", xmlEscape(user.Email), xmlEscape(principal))
	b.WriteString("        <C:calendar-user-type>INDIVIDUAL</C:calendar-user-type>\n")
	fmt.Fprintf(&b, "        <C:schedule-inbox-URL><D:href>%s</D:href></C:schedule-inbox-URL>\n", xmlEscape(inbox))
	fmt.Fprintf(&b, "        <C:schedule-outbox-URL><D:href>%s</D:href></C:schedule-outbox-URL>\n", xmlEscape(outbox))
	fmt.Fprintf(&b, "        <CS:email-address-set><CS:email-address>%s</CS:email-address></CS:email-address-set>\n", xmlEscape(user.Email))
	b.WriteString("      </D:prop>\n      <D:status>HTTP/1.1 200 OK</D:status>\n    </D:propstat>\n  </D:response>\n")
	b.WriteString("</D:multistatus>\n")

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	io.WriteString(w, b.String())
}

func normalizeLower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func reportRoot(body []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local
		}
	}
}

// handleFreeBusy answers a CALDAV:free-busy-query with a VFREEBUSY built from
// the calendar's opaque, non-private events in the requested window.
func (s *Server) handleFreeBusy(w http.ResponseWriter, r *http.Request, body []byte) {
	start, end, ok := parseTimeRange(body)
	if !ok {
		http.Error(w, "missing time-range", http.StatusBadRequest)
		return
	}
	p := s.backend.parsePath(r.URL.Path)
	authed, ok := UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	owner, err := s.db.UserByEmail(r.Context(), p.email)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	cal, err := s.db.CalendarBySlug(r.Context(), owner.ID, p.slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	level, err := s.db.AccessLevel(r.Context(), cal.ID, authed.ID)
	if err != nil || level == storage.AccessNone {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	window := ical.Range{Start: start, End: end}

	objs, err := s.db.ListObjects(r.Context(), cal.ID)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	var periods []string
	for i := range objs {
		obj, err := ical.Parse(objs[i].Blob)
		if err != nil {
			continue
		}
		if obj.Transparent() || obj.Cancelled() {
			continue
		}
		// Private events only count as busy for the owner.
		if obj.Private() && level != storage.AccessOwner {
			continue
		}
		occ, err := obj.Occurrences(window)
		if err != nil {
			continue
		}
		dur := obj.Duration()
		for _, st := range occ {
			en := st.Add(dur)
			if en.Before(start) || st.After(end) {
				continue
			}
			periods = append(periods, fmt.Sprintf("%s/%s", utcStamp(st), utcStamp(en)))
		}
	}

	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//furryfoxcorp//calshare//EN\r\nMETHOD:REPLY\r\n")
	b.WriteString("BEGIN:VFREEBUSY\r\n")
	fmt.Fprintf(&b, "DTSTAMP:%s\r\n", utcStamp(time.Now().UTC()))
	fmt.Fprintf(&b, "DTSTART:%s\r\n", utcStamp(start))
	fmt.Fprintf(&b, "DTEND:%s\r\n", utcStamp(end))
	for _, p := range periods {
		fmt.Fprintf(&b, "FREEBUSY;FBTYPE=BUSY:%s\r\n", p)
	}
	b.WriteString("END:VFREEBUSY\r\nEND:VCALENDAR\r\n")

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, b.String())
}

// handlePrincipalSearch answers DAV:principal-property-search with the
// matching local users as principals.
func (s *Server) handlePrincipalSearch(w http.ResponseWriter, r *http.Request, body []byte) {
	query := principalSearchMatch(body)
	users, err := s.db.SearchUsers(r.Context(), query)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">` + "\n")
	for _, u := range users {
		principal := s.backend.principalPath(u.Email)
		fmt.Fprintf(&b, "  <D:response>\n    <D:href>%s</D:href>\n    <D:propstat>\n      <D:prop>\n", xmlEscape(principal))
		fmt.Fprintf(&b, "        <D:displayname>%s</D:displayname>\n", xmlEscape(u.DisplayName))
		fmt.Fprintf(&b, "        <C:calendar-user-address-set>\n          <D:href>mailto:%s</D:href>\n          <D:href>%s</D:href>\n        </C:calendar-user-address-set>\n", xmlEscape(u.Email), xmlEscape(principal))
		b.WriteString("      </D:prop>\n      <D:status>HTTP/1.1 200 OK</D:status>\n    </D:propstat>\n  </D:response>\n")
	}
	b.WriteString("</D:multistatus>\n")

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	io.WriteString(w, b.String())
}

// parseTimeRange extracts the start and end attributes of a <time-range>
// element anywhere in the body.
func parseTimeRange(body []byte) (time.Time, time.Time, bool) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return time.Time{}, time.Time{}, false
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "time-range" {
			continue
		}
		var startStr, endStr string
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "start":
				startStr = a.Value
			case "end":
				endStr = a.Value
			}
		}
		start, e1 := time.ParseInLocation("20060102T150405Z", startStr, time.UTC)
		end, e2 := time.ParseInLocation("20060102T150405Z", endStr, time.UTC)
		if e1 != nil || e2 != nil {
			return time.Time{}, time.Time{}, false
		}
		return start, end, true
	}
}

// principalSearchMatch returns the first <match> text in the request.
func principalSearchMatch(body []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "match" {
			var text string
			if t, err := dec.Token(); err == nil {
				if cd, ok := t.(xml.CharData); ok {
					text = strings.TrimSpace(string(cd))
				}
			}
			return text
		}
	}
}

func utcStamp(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}
