package ical

import (
	"fmt"
	"sort"
	"sync"
	"time"

	goical "github.com/emersion/go-ical"
)

// vtzCache memoizes generated VTIMEZONE components by IANA name.
var (
	vtzMu    sync.Mutex
	vtzCache = map[string]*goical.Component{}
)

// BundleTimezones scans every TZID parameter referenced anywhere in the
// calendar and injects one VTIMEZONE component per IANA zone that is not
// already present. Apple Calendar silently drops events whose TZID has no
// matching VTIMEZONE, so this runs before emitting stored objects.
func BundleTimezones(cal *goical.Calendar) error {
	wanted := referencedTZIDs(cal)
	if len(wanted) == 0 {
		return nil
	}
	present := map[string]bool{}
	for _, c := range cal.Children {
		if c.Name == "VTIMEZONE" {
			if id := c.Props.Get("TZID"); id != nil {
				present[id.Value] = true
			}
		}
	}

	names := make([]string, 0, len(wanted))
	for name := range wanted {
		names = append(names, name)
	}
	sort.Strings(names)

	var injected []*goical.Component
	for _, name := range names {
		if present[name] {
			continue
		}
		vtz, err := vtimezoneFor(name)
		if err != nil {
			// A bad or non-IANA TZID is not fatal; skip it rather than
			// refusing to emit the whole calendar.
			continue
		}
		injected = append(injected, vtz)
	}
	// VTIMEZONE components belong before the event components.
	cal.Children = append(injected, cal.Children...)
	return nil
}

func referencedTZIDs(cal *goical.Calendar) map[string]struct{} {
	out := map[string]struct{}{}
	var walk func(c *goical.Component)
	walk = func(c *goical.Component) {
		for _, props := range c.Props {
			for _, p := range props {
				if tzid := p.Params.Get("TZID"); tzid != "" {
					out[tzid] = struct{}{}
				}
			}
		}
		for _, child := range c.Children {
			walk(child)
		}
	}
	for _, c := range cal.Children {
		walk(c)
	}
	return out
}

// vtimezoneFor builds (and caches) a VTIMEZONE component for an IANA zone.
func vtimezoneFor(name string) (*goical.Component, error) {
	vtzMu.Lock()
	defer vtzMu.Unlock()
	if c, ok := vtzCache[name]; ok {
		return c, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}

	vtz := goical.NewComponent("VTIMEZONE")
	vtz.Props.SetText("TZID", name)

	transitions := detectTransitions(loc)
	if len(transitions) == 0 {
		// Fixed-offset zone: one STANDARD subcomponent with the constant
		// offset, anchored at the epoch.
		_, offset := time.Date(1970, 1, 1, 0, 0, 0, 0, loc).Zone()
		std := goical.NewComponent("STANDARD")
		setRaw(std, "DTSTART", "19700101T000000")
		setRaw(std, "TZOFFSETFROM", formatOffset(offset))
		setRaw(std, "TZOFFSETTO", formatOffset(offset))
		nm, _ := time.Date(1970, 1, 1, 0, 0, 0, 0, loc).Zone()
		std.Props.SetText("TZNAME", nm)
		vtz.Children = append(vtz.Children, std)
		vtzCache[name] = vtz
		return vtz, nil
	}

	// Use the most recent transition into each of DAYLIGHT and STANDARD as a
	// representative rule. This matches what clients need for current and
	// near-future events.
	var latestStd, latestDst *transition
	for i := range transitions {
		t := &transitions[i]
		if t.isDST {
			if latestDst == nil || t.at.After(latestDst.at) {
				latestDst = t
			}
		} else {
			if latestStd == nil || t.at.After(latestStd.at) {
				latestStd = t
			}
		}
	}
	if latestStd != nil {
		vtz.Children = append(vtz.Children, subcomponent("STANDARD", latestStd, loc))
	}
	if latestDst != nil {
		vtz.Children = append(vtz.Children, subcomponent("DAYLIGHT", latestDst, loc))
	}
	vtzCache[name] = vtz
	return vtz, nil
}

type transition struct {
	at         time.Time // instant of the change, UTC
	offsetFrom int       // seconds east of UTC before
	offsetTo   int       // seconds east of UTC after
	isDST      bool      // whether the zone is in DST after the change
	name       string    // zone abbreviation after the change
}

// detectTransitions samples a window around the present and binary-searches for
// offset changes, returning the transitions found.
func detectTransitions(loc *time.Location) []transition {
	from := time.Date(time.Now().UTC().Year()-2, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(5, 0, 0)

	step := 6 * time.Hour
	var out []transition
	prev := from
	_, prevOff := prev.In(loc).Zone()
	for t := from.Add(step); t.Before(to); t = t.Add(step) {
		_, off := t.In(loc).Zone()
		if off != prevOff {
			at := binarySearchTransition(loc, prev, t)
			_, before := at.Add(-time.Second).In(loc).Zone()
			nm, after := at.In(loc).Zone()
			out = append(out, transition{
				at:         at,
				offsetFrom: before,
				offsetTo:   after,
				isDST:      isDSTAt(loc, at),
				name:       nm,
			})
			prevOff = off
		}
		prev = t
	}
	return out
}

// binarySearchTransition narrows [lo, hi] (which straddle an offset change) to
// the second the change occurs.
func binarySearchTransition(loc *time.Location, lo, hi time.Time) time.Time {
	_, loOff := lo.In(loc).Zone()
	for hi.Sub(lo) > time.Second {
		mid := lo.Add(hi.Sub(lo) / 2)
		_, midOff := mid.In(loc).Zone()
		if midOff == loOff {
			lo = mid
		} else {
			hi = mid
		}
	}
	return hi
}

// isDSTAt reports whether loc observes daylight saving at instant t. Go does
// not expose this directly, so compare the offset against the zone's January
// offset (a heuristic that holds for northern and southern hemispheres alike
// because DST always increases the offset relative to standard time).
func isDSTAt(loc *time.Location, t time.Time) bool {
	_, off := t.In(loc).Zone()
	_, jan := time.Date(t.Year(), 1, 1, 0, 0, 0, 0, loc).Zone()
	_, jul := time.Date(t.Year(), 7, 1, 0, 0, 0, 0, loc).Zone()
	std := jan
	if jul < std {
		std = jul // southern hemisphere: standard time is the smaller offset
	}
	return off > std
}

// subcomponent builds a STANDARD or DAYLIGHT block with a yearly recurrence
// derived from the transition date.
func subcomponent(kind string, tr *transition, loc *time.Location) *goical.Component {
	c := goical.NewComponent(kind)
	// DTSTART in a VTIMEZONE subcomponent is wall-clock local time in the
	// offset that was in effect just before the change (offsetFrom).
	beforeLocal := tr.at.Add(-time.Second).In(time.FixedZone("", tr.offsetFrom)).Add(time.Second)
	setRaw(c, "DTSTART", beforeLocal.Format("20060102T150405"))
	setRaw(c, "TZOFFSETFROM", formatOffset(tr.offsetFrom))
	setRaw(c, "TZOFFSETTO", formatOffset(tr.offsetTo))
	if tr.name != "" {
		c.Props.SetText("TZNAME", tr.name)
	}
	// The yearly recurrence must describe the same wall-clock date as DTSTART,
	// so derive its month/weekday from beforeLocal, not the post-change instant.
	setRaw(c, "RRULE", yearlyRule(beforeLocal))
	return c
}

// yearlyRule returns a FREQ=YEARLY rule for the nth weekday of the month that
// the transition falls on.
func yearlyRule(t time.Time) string {
	week := (t.Day()-1)/7 + 1
	// If the date is in the last 7 days of the month, express it as the last
	// occurrence, which is how tzdata rules are usually written.
	last := time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location()).Add(-24 * time.Hour)
	if t.Day() > last.Day()-7 {
		week = -1
	}
	days := []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}
	return fmt.Sprintf("FREQ=YEARLY;BYMONTH=%d;BYDAY=%d%s", int(t.Month()), week, days[t.Weekday()])
}

// formatOffset renders seconds-east-of-UTC as +HHMM or -HHMM (or +HHMMSS).
func formatOffset(seconds int) string {
	sign := "+"
	if seconds < 0 {
		sign = "-"
		seconds = -seconds
	}
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	if s != 0 {
		return fmt.Sprintf("%s%02d%02d%02d", sign, h, m, s)
	}
	return fmt.Sprintf("%s%02d%02d", sign, h, m)
}

// setRaw sets a property to a raw, unescaped value (for RECUR, UTC-OFFSET, and
// DATE-TIME values that must not be TEXT-escaped).
func setRaw(c *goical.Component, name, value string) {
	p := goical.NewProp(name)
	p.Value = value
	c.Props.Set(p)
}
