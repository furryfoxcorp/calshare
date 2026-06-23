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
	statuses := map[string]string{}
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
			state.ScheduleStatus = "1.2" // delivered to a local Inbox
			res.LocalDelivered++
		case deliver && local == nil:
			if err := s.queueExternal(ctx, organizer, obj, a.email, requestBody); err != nil {
				return nil, err
			}
			state.ScheduleStatus = "1.1" // sent (queued) to an external address
			res.ExternalQueued++
		default:
			state.ScheduleStatus = "" // client-managed; we only record state
		}

		if err := s.db.UpsertAttendeeState(ctx, state); err != nil {
			return nil, err
		}
		if state.ScheduleStatus != "" {
			statuses[normalizeEmail(a.email)] = state.ScheduleStatus
		}
	}

	// Annotate the stored event with per-attendee SCHEDULE-STATUS so clients
	// see the delivery outcome on their next sync (RFC 6638 3.2.3).
	if len(statuses) > 0 {
		s.writeScheduleStatus(ctx, obj, cal, master, statuses)
	}
	return res, nil
}

// writeScheduleStatus sets SCHEDULE-STATUS on the master event's ATTENDEE
// properties and re-stores the object.
func (s *Scheduler) writeScheduleStatus(ctx context.Context, obj *storage.Object, cal *goical.Calendar, master *goical.Component, statuses map[string]string) {
	changed := false
	for i := range master.Props["ATTENDEE"] {
		p := &master.Props["ATTENDEE"][i]
		st, ok := statuses[mailtoAddress(p.Value)]
		if !ok {
			continue
		}
		if p.Params == nil {
			p.Params = goical.Params{}
		}
		p.Params.Set("SCHEDULE-STATUS", st)
		changed = true
	}
	if !changed {
		return
	}
	blob, err := ical.Emit(cal)
	if err != nil {
		return
	}
	_, _ = s.db.PutObject(ctx, obj.CalendarID, obj.Href, blob)
}

// OnDelete sends CANCEL messages when a scheduled event the user organizes is
// removed. It is a no-op for events without attendees.
func (s *Scheduler) OnDelete(ctx context.Context, obj *storage.Object, organizer *storage.User) error {
	if !obj.HasScheduling {
		return nil
	}
	cal, err := goical.NewDecoder(strings.NewReader(string(obj.Blob))).Decode()
	if err != nil {
		return err
	}
	master := masterEvent(cal)
	if master == nil {
		return nil
	}
	cancel, err := BuildCancel(cal)
	if err != nil {
		return err
	}
	body, err := ical.Emit(cancel)
	if err != nil {
		return err
	}
	for _, a := range extractAttendees(master) {
		if normalizeEmail(a.email) == normalizeEmail(organizer.Email) {
			continue
		}
		if a.agent != "" && !strings.EqualFold(a.agent, "SERVER") {
			continue
		}
		if local, _ := s.db.UserByEmail(ctx, a.email); local != nil {
			_ = s.db.CreateInboxObject(ctx, &storage.InboxObject{
				UserID: local.ID, Href: sanitizeHref(obj.UID) + "-cancel.ics",
				ETag: ical.ETag(body), Blob: body, Method: MethodCancel, UID: obj.UID,
				OriginUserID: &organizer.ID, OriginEmail: organizer.Email,
			})
		} else {
			msgID := generateMessageID(obj.UID)
			objID := obj.ID
			_, _ = s.db.EnqueueIMIP(ctx, &storage.IMIPMessage{
				ObjectID: &objID, FromUserID: organizer.ID, ToAddress: a.email,
				Method: MethodCancel, MessageID: msgID, UID: obj.UID, Body: body,
			})
		}
	}
	return nil
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

// RespondLocal records a local attendee's response to an invitation and
// delivers a REPLY to the organizer's Inbox. partstat is ACCEPTED, DECLINED,
// or TENTATIVE.
func (s *Scheduler) RespondLocal(ctx context.Context, recipient *storage.User, uid, partstat string) error {
	obj, err := s.db.ObjectByUIDAny(ctx, uid)
	if err != nil {
		return err
	}
	_ = s.db.UpdateAttendeePartstat(ctx, obj.ID, recipient.Email, partstat, "2.0")

	cal, err := goical.NewDecoder(strings.NewReader(string(obj.Blob))).Decode()
	if err != nil {
		return err
	}
	reply, err := BuildReply(cal, recipient.Email, partstat)
	if err != nil {
		return err
	}
	body, err := ical.Emit(reply)
	if err != nil {
		return err
	}
	ownerID, err := s.db.CalendarOwner(ctx, obj.CalendarID)
	if err != nil {
		return err
	}
	return s.db.CreateInboxObject(ctx, &storage.InboxObject{
		UserID: ownerID, Href: sanitizeHref(uid) + "-reply-" + sanitizeHref(recipient.Email) + ".ics",
		ETag: ical.ETag(body), Blob: body, Method: MethodReply, UID: uid,
		OriginUserID: &recipient.ID, OriginEmail: recipient.Email,
	})
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
