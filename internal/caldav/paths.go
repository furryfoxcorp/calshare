package caldav

import (
	"net/url"
	"path"
	"strings"
)

// homeSetName is the single path segment under a principal that holds the
// user's calendar collections.
const homeSetName = "calendars"

// seg URL-escapes one path segment. For ordinary emails and slugs this is a
// no-op, but it keeps hrefs valid for addresses with unusual characters.
func seg(s string) string { return url.PathEscape(s) }

// principalPath returns the user principal URL, for example
// "/dav/owner@example.com/".
func (b *Backend) principalPath(email string) string {
	return b.prefix + "/" + seg(email) + "/"
}

// homeSetPath returns the calendar home set URL, for example
// "/dav/owner@example.com/calendars/".
func (b *Backend) homeSetPath(email string) string {
	return b.prefix + "/" + seg(email) + "/" + homeSetName + "/"
}

// calendarPath returns a calendar collection URL (no trailing slash, matching
// go-webdav's convention), for example
// "/dav/owner@example.com/calendars/<slug>".
func (b *Backend) calendarPath(email, slug string) string {
	return b.prefix + "/" + seg(email) + "/" + homeSetName + "/" + seg(slug)
}

// objectPath returns a calendar object URL, for example
// "/dav/owner@example.com/calendars/<slug>/<name>".
func (b *Backend) objectPath(email, slug, name string) string {
	return b.calendarPath(email, slug) + "/" + seg(name)
}

// parsed holds the components extracted from a request path.
type parsed struct {
	email  string
	slug   string
	object string // the trailing object name, for example "uid.ics"
}

// parsePath splits a request path into its principal email, calendar slug, and
// object name. Absent components are empty strings.
func (b *Backend) parsePath(p string) parsed {
	p = path.Clean(p)
	p = strings.TrimPrefix(p, b.prefix)
	p = strings.Trim(p, "/")
	if p == "" {
		return parsed{}
	}
	parts := strings.Split(p, "/")
	var out parsed
	if len(parts) >= 1 {
		out.email = parts[0]
	}
	// parts[1] is the home set segment ("calendars"); skip it.
	if len(parts) >= 3 {
		out.slug = parts[2]
	}
	if len(parts) >= 4 {
		out.object = parts[3]
	}
	return out
}
