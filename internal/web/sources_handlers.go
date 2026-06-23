package web

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/furryfoxcorp/calshare/internal/oidc"
	"github.com/furryfoxcorp/calshare/internal/sources"
	"github.com/furryfoxcorp/calshare/internal/storage"
)

func (s *Server) icsCalendarsForUser(r *http.Request, userID int64) []storage.Calendar {
	all, _ := s.db.CalendarsForUser(r.Context(), userID)
	out := make([]storage.Calendar, 0)
	for _, c := range all {
		if c.SourceType == "ics" {
			out = append(out, c)
		}
	}
	return out
}

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	s.render(w, "sources", page{
		User:   user,
		Active: "sources",
		Data:   struct{ Sources []storage.Calendar }{s.icsCalendarsForUser(r, user.ID)},
	})
}

func (s *Server) handleAddSource(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	url := strings.TrimSpace(r.FormValue("url"))
	name := strings.TrimSpace(r.FormValue("name"))
	if url == "" || name == "" {
		http.Error(w, "link and name are required", http.StatusBadRequest)
		return
	}
	interval := 0
	if v := r.FormValue("interval"); v != "" {
		if mins, err := strconv.Atoi(v); err == nil && mins > 0 {
			interval = mins * 60
		}
	}
	cal, err := s.db.CreateCalendar(r.Context(), &storage.Calendar{
		UserID:          user.ID,
		SourceType:      "ics",
		DisplayName:     name,
		Color:           strings.TrimSpace(r.FormValue("color")),
		SupportsVTODO:   false,
		ICSURL:          url,
		ICSPollInterval: interval,
	})
	if err != nil {
		http.Error(w, "could not add subscription", http.StatusInternalServerError)
		return
	}
	// Fetch once immediately so the list shows real status.
	go func(c storage.Calendar) {
		poller := sources.New(s.db, 15*time.Minute, slog.Default())
		_ = poller.PollOnce(context.Background(), &c)
	}(*cal)

	s.writeSourceList(w, r, user.ID)
}

func (s *Server) handlePollSource(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	cal, err := s.db.CalendarByID(r.Context(), id)
	if err != nil || cal.UserID != user.ID || cal.SourceType != "ics" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	poller := sources.New(s.db, 15*time.Minute, slog.Default())
	_ = poller.PollOnce(r.Context(), cal)
	s.writeSourceList(w, r, user.ID)
}

func (s *Server) writeSourceList(w http.ResponseWriter, r *http.Request, userID int64) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.partials.ExecuteTemplate(w, "sourceList", s.icsCalendarsForUser(r, userID))
}
