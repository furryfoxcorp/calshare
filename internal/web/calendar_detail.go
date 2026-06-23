package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/furryfoxcorp/calshare/internal/ical"
	"github.com/furryfoxcorp/calshare/internal/oidc"
	"github.com/furryfoxcorp/calshare/internal/storage"
)

// loadOwnedCalendar fetches a calendar and confirms the requester owns it.
func (s *Server) loadOwnedCalendar(w http.ResponseWriter, r *http.Request) (*storage.User, *storage.Calendar, bool) {
	user, _ := oidc.UserFromContext(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return nil, nil, false
	}
	cal, err := s.db.CalendarByID(r.Context(), id)
	if err != nil || cal.UserID != user.ID {
		http.Error(w, "not found", http.StatusNotFound)
		return nil, nil, false
	}
	return user, cal, true
}

func (s *Server) handleCalendarDetail(w http.ResponseWriter, r *http.Request) {
	user, cal, ok := s.loadOwnedCalendar(w, r)
	if !ok {
		return
	}
	acl, _ := s.db.CalendarACL(r.Context(), cal.ID)
	davURL := strings.TrimRight(s.externalURL, "/") + "/dav/" + user.Email + "/calendars/" + cal.Slug + "/"
	s.render(w, "calendar_detail", page{
		User:   user,
		Active: "calendars",
		Data: struct {
			Calendar *storage.Calendar
			ACL      []storage.ACLEntry
			DAVURL   string
		}{cal, acl, davURL},
	})
}

func (s *Server) handleUpdateCalendar(w http.ResponseWriter, r *http.Request) {
	_, cal, ok := s.loadOwnedCalendar(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = cal.DisplayName
	}
	interval := cal.ICSPollInterval
	if v := r.FormValue("interval"); v != "" {
		if mins, err := strconv.Atoi(v); err == nil && mins > 0 {
			interval = mins * 60
		}
	}
	if err := s.db.UpdateCalendar(r.Context(), cal.ID, name, strings.TrimSpace(r.FormValue("color")), r.FormValue("tasks") == "1", interval); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/calendars/"+strconv.FormatInt(cal.ID, 10), http.StatusSeeOther)
}

func (s *Server) handleShareCalendar(w http.ResponseWriter, r *http.Request) {
	_, cal, ok := s.loadOwnedCalendar(w, r)
	if !ok {
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	priv := r.FormValue("privilege")
	grantee, err := s.db.UserByEmail(r.Context(), email)
	if err != nil {
		http.Error(w, "no local user with that email", http.StatusBadRequest)
		return
	}
	if priv != storage.AccessRead && priv != storage.AccessReadWrite {
		priv = storage.AccessRead
	}
	if err := s.db.GrantCalendarAccess(r.Context(), cal.ID, grantee.ID, priv); err != nil {
		http.Error(w, "could not share", http.StatusInternalServerError)
		return
	}
	s.audited(r, "calendar.share_grant", "calendar", cal.ID, map[string]any{"grantee": email, "privilege": priv})
	http.Redirect(w, r, "/calendars/"+strconv.FormatInt(cal.ID, 10), http.StatusSeeOther)
}

func (s *Server) handleUnshareCalendar(w http.ResponseWriter, r *http.Request) {
	_, cal, ok := s.loadOwnedCalendar(w, r)
	if !ok {
		return
	}
	granteeID, err := strconv.ParseInt(r.FormValue("grantee_user_id"), 10, 64)
	if err != nil {
		http.Error(w, "bad grantee", http.StatusBadRequest)
		return
	}
	_ = s.db.RevokeCalendarAccess(r.Context(), cal.ID, granteeID)
	s.audited(r, "calendar.share_revoke", "calendar", cal.ID, map[string]any{"grantee_user_id": granteeID})
	http.Redirect(w, r, "/calendars/"+strconv.FormatInt(cal.ID, 10), http.StatusSeeOther)
}

// taskRow is a VTODO summarized for the tasks page.
type taskRow struct {
	Href    string
	Summary string
	Status  string
	Due     string
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	user, cal, ok := s.loadOwnedCalendar(w, r)
	if !ok {
		return
	}
	objs, _ := s.db.ListObjects(r.Context(), cal.ID)
	var tasks []taskRow
	for i := range objs {
		if objs[i].ComponentType != ical.CompTodo {
			continue
		}
		tasks = append(tasks, summarizeTask(&objs[i]))
	}
	s.render(w, "tasks", page{
		User:   user,
		Active: "calendars",
		Data: struct {
			Calendar *storage.Calendar
			Tasks    []taskRow
		}{cal, tasks},
	})
}

func summarizeTask(o *storage.Object) taskRow {
	row := taskRow{Href: o.Href, Status: "NEEDS-ACTION"}
	if parsed, err := ical.Parse(o.Blob); err == nil {
		for _, c := range parsed.Cal.Children {
			if c.Name != ical.CompTodo {
				continue
			}
			if p := c.Props.Get("SUMMARY"); p != nil {
				row.Summary = p.Value
			}
			if p := c.Props.Get("STATUS"); p != nil {
				row.Status = p.Value
			}
			if p := c.Props.Get("DUE"); p != nil {
				row.Due = p.Value
			}
		}
	}
	if row.Summary == "" {
		row.Summary = "(untitled task)"
	}
	return row
}
