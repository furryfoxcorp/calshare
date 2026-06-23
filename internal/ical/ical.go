package ical

import (
	"strings"
	"time"

	goical "github.com/emersion/go-ical"
)

// Component type names we store.
const (
	CompEvent = "VEVENT"
	CompTodo  = "VTODO"
)

// Object is a parsed iCalendar resource: one logical VEVENT or VTODO (its
// master plus any RECURRENCE-ID overrides), all sharing a UID.
type Object struct {
	Cal           *goical.Calendar
	UID           string
	ComponentType string
	HasRRULE      bool
	HasScheduling bool
	First         *time.Time // earliest occurrence start, UTC; nil if undatable
	Last          *time.Time // latest occurrence end, UTC; nil if unbounded
}

// timed components are VEVENT and VTODO children of the calendar.
func timedComponents(cal *goical.Calendar) []*goical.Component {
	var out []*goical.Component
	for _, c := range cal.Children {
		if c.Name == CompEvent || c.Name == CompTodo {
			out = append(out, c)
		}
	}
	return out
}

// propDateTime reads a date or date-time property, honoring an explicit TZID
// parameter, a trailing Z for UTC, and a VALUE=DATE form. defaultLoc is used
// when the value carries no zone information. The second return is false when
// the property is absent.
func propDateTime(c *goical.Component, name string, defaultLoc *time.Location) (time.Time, bool, error) {
	p := c.Props.Get(name)
	if p == nil {
		return time.Time{}, false, nil
	}
	val := p.Value
	if val == "" {
		return time.Time{}, false, nil
	}

	// Date-only value, e.g. 20260105.
	if !strings.Contains(val, "T") {
		t, err := time.ParseInLocation("20060102", val, locOrUTC(defaultLoc))
		return t, err == nil, err
	}

	// UTC, e.g. 20260105T090000Z.
	if strings.HasSuffix(val, "Z") {
		t, err := time.ParseInLocation("20060102T150405Z", val, time.UTC)
		return t, err == nil, err
	}

	// Floating or TZID-qualified local time.
	loc := defaultLoc
	if tzid := p.Params.Get("TZID"); tzid != "" {
		if l, err := time.LoadLocation(tzid); err == nil {
			loc = l
		}
	}
	t, err := time.ParseInLocation("20060102T150405", val, locOrUTC(loc))
	return t, err == nil, err
}

func locOrUTC(l *time.Location) *time.Location {
	if l == nil {
		return time.UTC
	}
	return l
}

// exdates reads EXDATE properties (possibly multiple, possibly comma-joined)
// from a component.
func exdates(c *goical.Component, defaultLoc *time.Location) []time.Time {
	var out []time.Time
	for _, p := range c.Props["EXDATE"] {
		loc := defaultLoc
		if tzid := p.Params.Get("TZID"); tzid != "" {
			if l, err := time.LoadLocation(tzid); err == nil {
				loc = l
			}
		}
		for _, raw := range strings.Split(p.Value, ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			var (
				t   time.Time
				err error
			)
			switch {
			case !strings.Contains(raw, "T"):
				t, err = time.ParseInLocation("20060102", raw, locOrUTC(loc))
			case strings.HasSuffix(raw, "Z"):
				t, err = time.ParseInLocation("20060102T150405Z", raw, time.UTC)
			default:
				t, err = time.ParseInLocation("20060102T150405", raw, locOrUTC(loc))
			}
			if err == nil {
				out = append(out, t)
			}
		}
	}
	return out
}
