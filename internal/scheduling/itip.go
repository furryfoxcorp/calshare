// Package scheduling implements iTIP message construction (RFC 5546) and the
// attendee handling behind RFC 6638 auto-schedule: classifying attendees,
// recording their state, and building the REQUEST, REPLY, and CANCEL messages
// delivered locally or by email.
package scheduling

import (
	goical "github.com/emersion/go-ical"

	"github.com/furryfoxcorp/calshare/internal/ical"
)

// iTIP methods used here.
const (
	MethodRequest = "REQUEST"
	MethodReply   = "REPLY"
	MethodCancel  = "CANCEL"
)

// cloneCalendar makes a fresh VCALENDAR carrying the given METHOD plus copies
// of the source's VEVENT components (VALARMs removed). VTIMEZONEs are
// regenerated for the surviving events.
func cloneCalendar(src *goical.Calendar, method string) *goical.Calendar {
	out := goical.NewCalendar()
	out.Props.SetText("VERSION", "2.0")
	out.Props.SetText("PRODID", "-//furryfoxcorp//calshare//EN")
	out.Props.SetText("METHOD", method)
	for _, child := range src.Children {
		if child.Name == ical.CompEvent {
			out.Children = append(out.Children, cloneEvent(child))
		}
	}
	return out
}

func cloneEvent(src *goical.Component) *goical.Component {
	dst := goical.NewComponent(ical.CompEvent)
	for name, props := range src.Props {
		dst.Props[name] = stripServerParams(copyProps(props))
	}
	for _, sub := range src.Children {
		if sub.Name == "VALARM" {
			continue
		}
		dst.Children = append(dst.Children, sub)
	}
	return dst
}

// serverParams are scheduling parameters the server maintains on the
// organizer's stored copy; they must never be sent to attendees.
var serverParams = []string{"SCHEDULE-STATUS", "SCHEDULE-AGENT", "SCHEDULE-FORCE-SEND"}

// stripServerParams removes server-only scheduling parameters from properties
// (notably SCHEDULE-STATUS on ATTENDEE) before they go out in an iTIP message.
func stripServerParams(props []goical.Prop) []goical.Prop {
	for i := range props {
		if props[i].Params == nil {
			continue
		}
		for _, p := range serverParams {
			delete(props[i].Params, p)
		}
	}
	return props
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

// BuildRequest produces a METHOD:REQUEST invitation from a stored event.
func BuildRequest(src *goical.Calendar) (*goical.Calendar, error) {
	out := cloneCalendar(src, MethodRequest)
	if err := ical.BundleTimezones(out); err != nil {
		return nil, err
	}
	return out, nil
}

// BuildCancel produces a METHOD:CANCEL message, marking events cancelled and
// bumping SEQUENCE.
func BuildCancel(src *goical.Calendar) (*goical.Calendar, error) {
	out := cloneCalendar(src, MethodCancel)
	for _, c := range out.Children {
		if c.Name == ical.CompEvent {
			c.Props.SetText("STATUS", "CANCELLED")
			bumpSequence(c)
		}
	}
	if err := ical.BundleTimezones(out); err != nil {
		return nil, err
	}
	return out, nil
}

// BuildReply produces a METHOD:REPLY from one attendee, carrying only that
// attendee's participation status back to the organizer.
func BuildReply(src *goical.Calendar, attendeeEmail, partstat string) (*goical.Calendar, error) {
	out := cloneCalendar(src, MethodReply)
	want := "mailto:" + attendeeEmail
	for _, c := range out.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		// Keep only the replying attendee, with the new PARTSTAT.
		var kept []goical.Prop
		for _, p := range c.Props["ATTENDEE"] {
			if equalFoldMailto(p.Value, want) {
				if p.Params == nil {
					p.Params = goical.Params{}
				}
				p.Params.Set("PARTSTAT", partstat)
				kept = append(kept, p)
			}
		}
		if kept != nil {
			c.Props["ATTENDEE"] = kept
		} else {
			delete(c.Props, "ATTENDEE")
		}
	}
	if err := ical.BundleTimezones(out); err != nil {
		return nil, err
	}
	return out, nil
}

func bumpSequence(c *goical.Component) {
	seq := 0
	if p := c.Props.Get("SEQUENCE"); p != nil {
		if n, err := atoi(p.Value); err == nil {
			seq = n
		}
	}
	// SEQUENCE is an INTEGER value; set it raw so no VALUE=TEXT param is added.
	c.Props["SEQUENCE"] = []goical.Prop{{Name: "SEQUENCE", Value: itoa(seq + 1)}}
}
