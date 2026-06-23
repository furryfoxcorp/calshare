package ical

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	goical "github.com/emersion/go-ical"
)

// Parse errors.
var (
	ErrNoComponent  = errors.New("ical: no VEVENT or VTODO component")
	ErrMixedTypes   = errors.New("ical: mixed component types in one resource")
	ErrMultipleUIDs = errors.New("ical: multiple UIDs in one resource")
)

// wideWindow bounds the search for the last occurrence of a bounded series.
func wideWindow(from time.Time) Range {
	return Range{Start: from.Add(-time.Hour), End: from.AddDate(100, 0, 0)}
}

// Parse decodes an iCalendar blob into an Object and computes its denormalized
// fields. It enforces one resource = one component type = one UID, allowing
// RECURRENCE-ID overrides that share the master UID.
func Parse(blob []byte) (*Object, error) {
	cal, err := goical.NewDecoder(bytes.NewReader(blob)).Decode()
	if err != nil {
		return nil, fmt.Errorf("ical: decode: %w", err)
	}

	comps := timedComponents(cal)
	if len(comps) == 0 {
		return nil, ErrNoComponent
	}

	obj := &Object{Cal: cal}
	obj.ComponentType = comps[0].Name

	var master *goical.Component
	for _, c := range comps {
		if c.Name != obj.ComponentType {
			return nil, ErrMixedTypes
		}
		uid := textProp(c, "UID")
		if uid == "" {
			return nil, fmt.Errorf("ical: component missing UID")
		}
		if obj.UID == "" {
			obj.UID = uid
		} else if uid != obj.UID {
			return nil, ErrMultipleUIDs
		}
		if c.Props.Get("RRULE") != nil {
			obj.HasRRULE = true
		}
		if c.Props.Get("ATTENDEE") != nil || c.Props.Get("ORGANIZER") != nil {
			obj.HasScheduling = true
		}
		if c.Props.Get("RECURRENCE-ID") == nil && master == nil {
			master = c
		}
	}
	if master == nil {
		master = comps[0]
	}

	first, last := computeBounds(comps, master)
	obj.First = first
	obj.Last = last
	return obj, nil
}

func textProp(c *goical.Component, name string) string {
	p := c.Props.Get(name)
	if p == nil {
		return ""
	}
	return p.Value
}

// startOf returns the start instant of a component (DTSTART, or DUE for a
// VTODO that has no DTSTART).
func startOf(c *goical.Component) (time.Time, bool) {
	if t, ok, _ := propDateTime(c, "DTSTART", time.UTC); ok {
		return t, true
	}
	if c.Name == CompTodo {
		if t, ok, _ := propDateTime(c, "DUE", time.UTC); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

// durationOf returns the component's span (end minus start). Zero if there is
// no end.
func durationOf(c *goical.Component, start time.Time) time.Duration {
	if t, ok, _ := propDateTime(c, "DTEND", time.UTC); ok {
		return t.Sub(start)
	}
	if c.Name == CompTodo {
		if t, ok, _ := propDateTime(c, "DUE", time.UTC); ok {
			return t.Sub(start)
		}
	}
	return 0
}

// computeBounds derives first and last occurrence (UTC) across the master and
// any overrides. last is nil for an unbounded recurring series.
func computeBounds(comps []*goical.Component, master *goical.Component) (*time.Time, *time.Time) {
	masterStart, ok := startOf(master)
	if !ok {
		return nil, nil
	}
	dur := durationOf(master, masterStart)

	firstUTC := masterStart.UTC()
	first := &firstUTC

	rrule := textProp(master, "RRULE")
	if rrule != "" {
		if !isBounded(rrule) {
			return first, nil // unbounded
		}
		ex := exdates(master, masterStart.Location())
		occ, err := ExpandRRULE(masterStart, rrule, ex, wideWindow(masterStart))
		if err != nil || len(occ) == 0 {
			lastUTC := masterStart.Add(dur).UTC()
			return first, &lastUTC
		}
		lastStart := occ[len(occ)-1]
		lastUTC := lastStart.Add(dur).UTC()
		return first, &lastUTC
	}

	// Non-recurring: last is the end of the single instance, widened by any
	// overrides that move an instance later.
	lastUTC := masterStart.Add(dur).UTC()
	for _, c := range comps {
		if s, ok := startOf(c); ok {
			end := s.Add(durationOf(c, s)).UTC()
			if end.After(lastUTC) {
				lastUTC = end
			}
		}
	}
	return first, &lastUTC
}

// isBounded reports whether an RRULE value terminates (has COUNT or UNTIL).
func isBounded(rrule string) bool {
	up := strings.ToUpper(rrule)
	return strings.Contains(up, "COUNT=") || strings.Contains(up, "UNTIL=")
}

// master returns the master (non-RECURRENCE-ID) component, or the first.
func (o *Object) master() *goical.Component {
	comps := timedComponents(o.Cal)
	for _, c := range comps {
		if c.Props.Get("RECURRENCE-ID") == nil {
			return c
		}
	}
	if len(comps) > 0 {
		return comps[0]
	}
	return nil
}

// Duration returns the master component's span (end minus start), or zero.
func (o *Object) Duration() time.Duration {
	m := o.master()
	if m == nil {
		return 0
	}
	s, ok := startOf(m)
	if !ok {
		return 0
	}
	return durationOf(m, s)
}

// Transparent reports whether the event does not block time (TRANSP:TRANSPARENT).
func (o *Object) Transparent() bool {
	m := o.master()
	return m != nil && textProp(m, "TRANSP") == "TRANSPARENT"
}

// Private reports whether the event is marked private or confidential.
func (o *Object) Private() bool {
	m := o.master()
	if m == nil {
		return false
	}
	switch textProp(m, "CLASS") {
	case "PRIVATE", "CONFIDENTIAL":
		return true
	}
	return false
}

// Cancelled reports whether the event status is CANCELLED.
func (o *Object) Cancelled() bool {
	m := o.master()
	return m != nil && textProp(m, "STATUS") == "CANCELLED"
}

// Occurrences returns the start instants of the object within window,
// expanding any RRULE on the master component. Single instances return their
// start when it falls in the window.
func (o *Object) Occurrences(window Range) ([]time.Time, error) {
	comps := timedComponents(o.Cal)
	var master *goical.Component
	for _, c := range comps {
		if c.Props.Get("RECURRENCE-ID") == nil {
			master = c
			break
		}
	}
	if master == nil && len(comps) > 0 {
		master = comps[0]
	}
	if master == nil {
		return nil, nil
	}
	start, ok := startOf(master)
	if !ok {
		return nil, nil
	}
	rrule := textProp(master, "RRULE")
	if rrule == "" {
		if window.Contains(start) {
			return []time.Time{start}, nil
		}
		return nil, nil
	}
	return ExpandRRULE(start, rrule, exdates(master, start.Location()), window)
}
