package views

import (
	goical "github.com/emersion/go-ical"

	"github.com/furryfoxcorp/calshare/internal/ical"
)

// Apply produces a new calendar containing only the VEVENTs from src that pass
// the view's include flags, with each event's fields rewritten per the
// resolved rules. VTODOs are dropped, VALARMs are stripped unless kept, and
// VTIMEZONEs are regenerated for the surviving events.
func Apply(src *goical.Calendar, s Spec) (*goical.Calendar, error) {
	rules := s.effectiveRules()
	out := goical.NewCalendar()
	out.Props.SetText("VERSION", "2.0")
	out.Props.SetText("PRODID", "-//furryfoxcorp//calshare//EN")

	// Group VEVENTs by UID so a recurring master and its RECURRENCE-ID
	// overrides are filtered together.
	order := []string{}
	groups := map[string][]*goical.Component{}
	for _, child := range src.Children {
		if child.Name != ical.CompEvent {
			continue // drop VTODO and any source VTIMEZONE; we regenerate zones
		}
		uid := propValue(child, "UID")
		if _, seen := groups[uid]; !seen {
			order = append(order, uid)
		}
		groups[uid] = append(groups[uid], child)
	}

	for _, uid := range order {
		out.Children = append(out.Children, filterSeries(groups[uid], rules, s)...)
	}

	if err := ical.BundleTimezones(out); err != nil {
		return nil, err
	}
	return out, nil
}

// filterSeries applies the view to one UID's components (a master plus any
// RECURRENCE-ID overrides), implementing the recurrence-under-filters rules:
// failed override instances become EXDATEs on the master, and surviving
// overrides of an excluded master are promoted to standalone events.
func filterSeries(comps []*goical.Component, rules map[string]Rule, s Spec) []*goical.Component {
	var master *goical.Component
	var overrides []*goical.Component
	for _, c := range comps {
		if c.Props.Get("RECURRENCE-ID") == nil && master == nil {
			master = c
		} else if c.Props.Get("RECURRENCE-ID") != nil {
			overrides = append(overrides, c)
		}
	}

	// No recurring master: filter each surviving component independently.
	if master == nil || master.Props.Get("RRULE") == nil {
		var out []*goical.Component
		for _, c := range comps {
			if !dropByFlags(c, s) {
				out = append(out, filterEvent(c, rules, s.busyLabel()))
			}
		}
		return out
	}

	masterTZID := tzidOf(master, "DTSTART")

	if !dropByFlags(master, s) {
		// Master is included. Keep it; reconcile overrides.
		kept := filterEvent(master, rules, s.busyLabel())
		var out []*goical.Component
		for _, ov := range overrides {
			if dropByFlags(ov, s) {
				addExdate(kept, ov, masterTZID) // hide this instance
			} else {
				out = append(out, filterEvent(ov, rules, s.busyLabel()))
			}
		}
		return append([]*goical.Component{kept}, out...)
	}

	// Master is excluded. Promote any surviving override to a standalone event.
	var out []*goical.Component
	for _, ov := range overrides {
		if dropByFlags(ov, s) {
			continue
		}
		promoted := filterEvent(ov, rules, s.busyLabel())
		rid := propValue(ov, "RECURRENCE-ID")
		delete(promoted.Props, "RECURRENCE-ID")
		delete(promoted.Props, "RRULE")
		uid := propValue(ov, "UID")
		promoted.Props["UID"] = []goical.Prop{{Name: "UID", Value: uid + "-" + sanitizeID(rid)}}
		out = append(out, promoted)
	}
	return out
}

// tzidOf returns the TZID parameter of a property, if any.
func tzidOf(c *goical.Component, prop string) string {
	if p := c.Props.Get(prop); p != nil && p.Params != nil {
		return p.Params.Get("TZID")
	}
	return ""
}

// addExdate appends an EXDATE to the master for the override's RECURRENCE-ID,
// matching the master DTSTART's TZID (the most common published-feed bug).
func addExdate(master, override *goical.Component, masterTZID string) {
	rid := override.Props.Get("RECURRENCE-ID")
	if rid == nil {
		return
	}
	ex := goical.Prop{Name: "EXDATE", Value: rid.Value}
	if masterTZID != "" {
		ex.Params = goical.Params{"TZID": []string{masterTZID}}
	}
	master.Props["EXDATE"] = append(master.Props["EXDATE"], ex)
}

func sanitizeID(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}

func propValue(c *goical.Component, name string) string {
	if p := c.Props.Get(name); p != nil {
		return p.Value
	}
	return ""
}

// dropByFlags reports whether an event should be excluded by the view's
// include flags.
func dropByFlags(c *goical.Component, s Spec) bool {
	switch propValue(c, "CLASS") {
	case "PRIVATE", "CONFIDENTIAL":
		if !s.IncludePrivate {
			return true
		}
	}
	switch propValue(c, "STATUS") {
	case "CANCELLED":
		if !s.IncludeCancelled {
			return true
		}
	case "TENTATIVE":
		if !s.IncludeTentative {
			return true
		}
	}
	if propValue(c, "TRANSP") == "TRANSPARENT" && !s.IncludeTransparent {
		return true
	}
	return false
}

// filterEvent returns a copy of the event with field rules applied.
func filterEvent(src *goical.Component, rules map[string]Rule, busyLabel string) *goical.Component {
	dst := goical.NewComponent(ical.CompEvent)

	for name, props := range src.Props {
		rule, managed := rules[name]
		if !managed {
			dst.Props[name] = copyProps(props)
			continue
		}
		switch rule {
		case Keep:
			dst.Props[name] = copyProps(props)
		case Replace:
			if name == "SUMMARY" {
				dst.Props.SetText("SUMMARY", busyLabel)
			} else {
				dst.Props[name] = copyProps(props)
			}
		case Strip:
			// omit
		}
	}

	// If the preset replaces SUMMARY but the source had none, still label it.
	if rules["SUMMARY"] == Replace && dst.Props.Get("SUMMARY") == nil {
		dst.Props.SetText("SUMMARY", busyLabel)
	}

	// Copy child components, honoring the VALARM rule.
	for _, sub := range src.Children {
		if sub.Name == "VALARM" && rules["VALARM"] == Strip {
			continue
		}
		dst.Children = append(dst.Children, sub)
	}
	return dst
}

func copyProps(in []goical.Prop) []goical.Prop {
	out := make([]goical.Prop, len(in))
	for i, p := range in {
		cp := goical.Prop{Name: p.Name, Value: p.Value}
		if p.Params != nil {
			cp.Params = make(goical.Params, len(p.Params))
			for k, v := range p.Params {
				vv := make([]string, len(v))
				copy(vv, v)
				cp.Params[k] = vv
			}
		}
		out[i] = cp
	}
	return out
}
