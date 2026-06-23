// Package ical wraps github.com/emersion/go-ical with the parsing, emitting,
// recurrence expansion, and filtering this server needs. It holds no HTTP or
// storage concerns.
package ical

import (
	"time"

	"github.com/teambition/rrule-go"
)

// Range is a half-open time window [Start, End).
type Range struct {
	Start, End time.Time
}

// Contains reports whether t falls in [Start, End).
func (r Range) Contains(t time.Time) bool {
	return !t.Before(r.Start) && t.Before(r.End)
}

// ExpandRRULE returns the start instants of a recurring series within window.
// dtstart is the series start (carrying its own location), rruleProp is the
// RRULE value without the "RRULE:" prefix (for example
// "FREQ=WEEKLY;COUNT=5"), and exdates lists instants to exclude. The result
// is sorted ascending.
func ExpandRRULE(dtstart time.Time, rruleProp string, exdates []time.Time, window Range) ([]time.Time, error) {
	// Parse in the series' own location so a floating UNTIL (one without a
	// trailing Z, which real clients emit for zoned events) resolves in that
	// zone rather than UTC, which would clip the last occurrences.
	opt, err := rrule.StrToROptionInLocation(rruleProp, dtstart.Location())
	if err != nil {
		return nil, err
	}
	opt.Dtstart = dtstart
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil, err
	}

	set := &rrule.Set{}
	set.RRule(r)
	for _, ex := range exdates {
		set.ExDate(ex)
	}

	// Between is inclusive on both ends; our Range is half-open, so filter the
	// upper bound out below.
	occ := set.Between(window.Start, window.End, true)
	out := make([]time.Time, 0, len(occ))
	for _, t := range occ {
		if window.Contains(t) {
			out = append(out, t)
		}
	}
	return out, nil
}
