package web

import (
	"crypto/rand"
	"net/http"
	"strconv"
	"strings"

	"github.com/furryfoxcorp/calshare/internal/oidc"
	"github.com/furryfoxcorp/calshare/internal/storage"
)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	data := struct {
		Error    string
		DevLogin bool
	}{DevLogin: s.dev != nil}
	if s.auth == nil && s.dev == nil {
		data.Error = "Single sign-on is not configured yet. Check the server settings."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.login.ExecuteTemplate(w, "login.html", data)
}

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.auth.LoginStart(w, r)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.auth.Callback(w, r)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.sessions.ClearSession(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) placeholder(heading, message string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := oidc.UserFromContext(r.Context())
		active := strings.ToLower(heading)
		s.render(w, "placeholder", page{
			User:   user,
			Active: active,
			Data:   struct{ Heading, Message string }{heading, message},
		})
	}
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	ctx := r.Context()
	cals, _ := s.db.CalendarsForUser(ctx, user.ID)
	devices, _ := s.db.AppPasswordsForUser(ctx, user.ID)
	viewCount, _ := s.db.CountViews(ctx, user.ID)

	activeDevices := 0
	for _, d := range devices {
		if d.RevokedAt == nil {
			activeDevices++
		}
	}
	s.render(w, "dashboard", page{
		User:   user,
		Active: "home",
		Data: struct {
			Calendars   []storage.Calendar
			DeviceCount int
			ViewCount   int
		}{cals, activeDevices, viewCount},
	})
}

func (s *Server) handleCalendars(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	cals, _ := s.db.CalendarsForUser(r.Context(), user.ID)
	davBase := strings.TrimRight(s.externalURL, "/") + "/dav/" + user.Email + "/calendars/"
	s.render(w, "calendars", page{
		User:   user,
		Active: "calendars",
		Data: struct {
			Calendars []storage.Calendar
			DAVBase   string
		}{cals, davBase},
	})
}

func (s *Server) handleCreateCalendar(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	cal, err := s.db.CreateCalendar(r.Context(), &storage.Calendar{
		UserID:        user.ID,
		SourceType:    "native",
		DisplayName:   name,
		Color:         strings.TrimSpace(r.FormValue("color")),
		SupportsVTODO: r.FormValue("tasks") == "1",
	})
	if err != nil {
		http.Error(w, "could not create calendar", http.StatusInternalServerError)
		return
	}
	s.audited(r, "calendar.create", "calendar", cal.ID, map[string]any{"name": name})
	s.writeCalendarList(w, r, user.ID)
}

func (s *Server) handleDeleteCalendar(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	cal, err := s.db.CalendarByID(r.Context(), id)
	if err != nil || cal.UserID != user.ID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := s.db.DeleteCalendar(r.Context(), id); err != nil {
		http.Error(w, "could not delete", http.StatusInternalServerError)
		return
	}
	s.audited(r, "calendar.delete", "calendar", id, nil)
	if cal.SourceType == "ics" {
		s.writeSourceList(w, r, user.ID)
		return
	}
	s.writeCalendarList(w, r, user.ID)
}

func (s *Server) writeCalendarList(w http.ResponseWriter, r *http.Request, userID int64) {
	cals, _ := s.db.CalendarsForUser(r.Context(), userID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.partials.ExecuteTemplate(w, "calendarList", cals)
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	devices := s.activeDevices(r, user.ID)
	s.render(w, "devices", page{
		User:   user,
		Active: "devices",
		Data:   struct{ Devices []storage.AppPassword }{devices},
	})
}

func (s *Server) handleCreateDevice(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" {
		http.Error(w, "device name is required", http.StatusBadRequest)
		return
	}
	pw, err := generateAppPassword()
	if err != nil {
		http.Error(w, "could not generate password", http.StatusInternalServerError)
		return
	}
	id, err := s.db.CreateAppPassword(r.Context(), user.ID, label, pw)
	if err != nil {
		http.Error(w, "could not save password", http.StatusInternalServerError)
		return
	}
	s.audited(r, "app_password.create", "app_password", id, map[string]any{"label": label})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.partials.ExecuteTemplate(w, "deviceCreated", struct{ Label, Password string }{label, pw})
	// Out-of-band refresh of the list below.
	_ = s.partials.ExecuteTemplate(w, "deviceListOOB", s.activeDevices(r, user.ID))
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	// Confirm ownership before revoking.
	owned := false
	for _, d := range s.activeDevices(r, user.ID) {
		if d.ID == id {
			owned = true
			break
		}
	}
	if owned {
		_ = s.db.RevokeAppPassword(r.Context(), id)
		s.audited(r, "app_password.revoke", "app_password", id, nil)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.partials.ExecuteTemplate(w, "deviceList", s.activeDevices(r, user.ID))
}

func (s *Server) activeDevices(r *http.Request, userID int64) []storage.AppPassword {
	all, _ := s.db.AppPasswordsForUser(r.Context(), userID)
	out := make([]storage.AppPassword, 0, len(all))
	for _, d := range all {
		if d.RevokedAt == nil {
			out = append(out, d)
		}
	}
	return out
}

// appPasswordAlphabet excludes lookalike characters for easier transcription.
const appPasswordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// generateAppPassword returns a 24-character password formatted as four groups
// of six separated by dashes (about 140 bits of entropy).
func generateAppPassword() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	var b strings.Builder
	for i, v := range raw {
		if i > 0 && i%6 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(appPasswordAlphabet[int(v)%len(appPasswordAlphabet)])
	}
	return b.String(), nil
}
