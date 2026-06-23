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

	for _, child := range src.Children {
		if child.Name != ical.CompEvent {
			continue // drop VTODO and any source VTIMEZONE; we regenerate zones
		}
		if dropByFlags(child, s) {
			continue
		}
		out.Children = append(out.Children, filterEvent(child, rules, s.busyLabel()))
	}

	if err := ical.BundleTimezones(out); err != nil {
		return nil, err
	}
	return out, nil
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
