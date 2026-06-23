package scheduling

import (
	"context"
	"strings"

	goical "github.com/emersion/go-ical"

	"github.com/furryfoxcorp/calshare/internal/ical"
	"github.com/furryfoxcorp/calshare/internal/storage"
)

// Scheduler applies RFC 6638 auto-schedule on a stored event: it classifies
// attendees, records their state, deposits REQUESTs in local users' Inboxes,
// and queues iMIP email for external attendees.
type Scheduler struct {
	db *storage.DB
}

// New builds a Scheduler.
func New(db *storage.DB) *Scheduler {
	return &Scheduler{db: db}
}

// Result summarizes a scheduling pass.
type Result struct {
	LocalDelivered int
	ExternalQueued int
}

type attendee struct {
	email    string
	cn       string
	role     string
	partstat string
	agent    string // SCHEDULE-AGENT: SERVER (default), CLIENT, or NONE
}

// OnPut runs auto-schedule for a freshly stored object. It is a no-op when the
// event carries no scheduling information. organizer is the authenticated user
// who wrote the event.
func (s *Scheduler) OnPut(ctx context.Context, obj *storage.Object, organizer *storage.User) (*Result, error) {
	if !obj.HasScheduling {
		return &Result{}, nil
	}
	cal, err := goical.NewDecoder(strings.NewReader(string(obj.Blob))).Decode()
	if err != nil {
		return nil, err
	}
	master := masterEvent(cal)
	if master == nil {
		return &Result{}, nil
	}

	attendees := extractAttendees(master)
	if len(attendees) == 0 {
		return &Result{}, nil
	}

	request, err := BuildRequest(cal)
	if err != nil {
		return nil, err
	}
	requestBody, err := ical.Emit(request)
	if err != nil {
		return nil, err
	}

	res := &Result{}
	for _, a := range attendees {
		// Skip the organizer's own attendee line.
		if normalizeEmail(a.email) == normalizeEmail(organizer.Email) {
			continue
		}
		local, _ := s.db.UserByEmail(ctx, a.email)
		state := &storage.AttendeeState{
			ObjectID: obj.ID,
			Email:    a.email,
			CN:       a.cn,
			Role:     a.role,
			Partstat: defaultPartstat(a.partstat),
			RSVP:     true,
		}
		if local != nil {
			state.IsLocalUser = true
			state.LocalUserID = &local.ID
		}

		deliver := a.agent == "" || strings.EqualFold(a.agent, "SERVER")
		switch {
		case deliver && local != nil:
			if err := s.deliverLocal(ctx, local, organizer, obj.UID, requestBody); err != nil {
				return nil, err
			}
			state.ScheduleStatus = "1.2"
			res.LocalDelivered++
		case deliver && local == nil:
			if err := s.queueExternal(ctx, organizer, obj, a.email, requestBody); err != nil {
				return nil, err
			}
			state.ScheduleStatus = "1.1"
			res.ExternalQueued++
		default:
			state.ScheduleStatus = "" // client-managed; we only record state
		}

		if err := s.db.UpsertAttendeeState(ctx, state); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func (s *Scheduler) deliverLocal(ctx context.Context, recipient, organizer *storage.User, uid string, body []byte) error {
	href := sanitizeHref(uid) + ".ics"
	return s.db.CreateInboxObject(ctx, &storage.InboxObject{
		UserID:       recipient.ID,
		Href:         href,
		ETag:         ical.ETag(body),
		Blob:         body,
		Method:       MethodRequest,
		UID:          uid,
		OriginUserID: &organizer.ID,
		OriginEmail:  organizer.Email,
	})
}

func (s *Scheduler) queueExternal(ctx context.Context, organizer *storage.User, obj *storage.Object, to string, body []byte) error {
	msgID := generateMessageID(obj.UID)
	objID := obj.ID
	_, err := s.db.EnqueueIMIP(ctx, &storage.IMIPMessage{
		ObjectID:   &objID,
		FromUserID: organizer.ID,
		ToAddress:  to,
		Method:     MethodRequest,
		MessageID:  msgID,
		UID:        obj.UID,
		Body:       body,
	})
	return err
}

func masterEvent(cal *goical.Calendar) *goical.Component {
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent && c.Props.Get("RECURRENCE-ID") == nil {
			return c
		}
	}
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent {
			return c
		}
	}
	return nil
}

func extractAttendees(event *goical.Component) []attendee {
	var out []attendee
	for _, p := range event.Props["ATTENDEE"] {
		email := mailtoAddress(p.Value)
		if email == "" {
			continue
		}
		a := attendee{email: email}
		if p.Params != nil {
			a.cn = p.Params.Get("CN")
			a.role = p.Params.Get("ROLE")
			a.partstat = p.Params.Get("PARTSTAT")
			a.agent = p.Params.Get("SCHEDULE-AGENT")
		}
		out = append(out, a)
	}
	return out
}

func defaultPartstat(p string) string {
	if p == "" {
		return "NEEDS-ACTION"
	}
	return p
}

func sanitizeHref(uid string) string {
	var b strings.Builder
	for _, r := range uid {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "invite"
	}
	return b.String()
}
