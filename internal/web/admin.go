package web

import (
	"net/http"
	"strconv"

	"github.com/furryfoxcorp/calshare/internal/ical"
	"github.com/furryfoxcorp/calshare/internal/oidc"
	"github.com/furryfoxcorp/calshare/internal/storage"
)

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	if !user.IsAdmin {
		http.Error(w, "admins only", http.StatusForbidden)
		return
	}
	users, _ := s.db.AllUsers(r.Context())
	events, _ := s.db.ListAuditEvents(r.Context(), 100)
	queue, _ := s.db.RecentIMIP(r.Context(), 50)
	s.render(w, "admin", page{
		User:   user,
		Active: "admin",
		Data: struct {
			Users  []storage.User
			Audit  []storage.AuditEvent
			Queue  []storage.IMIPMessage
			IMAPOn bool
		}{users, events, queue, false},
	})
}

func (s *Server) handleEventDetail(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	obj, err := s.db.ObjectByID(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	cal, err := s.db.CalendarByID(r.Context(), obj.CalendarID)
	if err != nil || cal.UserID != user.ID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	attendees, _ := s.db.AttendeesForObject(r.Context(), obj.ID)
	summary := ""
	if parsed, perr := ical.Parse(obj.Blob); perr == nil {
		for _, c := range parsed.Cal.Children {
			if c.Name == ical.CompEvent {
				if p := c.Props.Get("SUMMARY"); p != nil {
					summary = p.Value
				}
				break
			}
		}
	}
	s.render(w, "event_detail", page{
		User:   user,
		Active: "calendars",
		Data: struct {
			Object    *storage.Object
			Summary   string
			Attendees []storage.AttendeeState
		}{obj, summary, attendees},
	})
}

// handleResendInvite re-queues an iMIP invitation for an external attendee.
func (s *Server) handleResendInvite(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	obj, err := s.db.ObjectByID(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	cal, err := s.db.CalendarByID(r.Context(), obj.CalendarID)
	if err != nil || cal.UserID != user.ID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if s.sched != nil {
		_, _ = s.sched.OnPut(r.Context(), obj, user)
	}
	http.Redirect(w, r, "/events/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}
