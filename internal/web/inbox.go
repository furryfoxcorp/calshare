package web

import (
	"net/http"
	"strings"

	"github.com/furryfoxcorp/calshare/internal/oidc"
	"github.com/furryfoxcorp/calshare/internal/storage"
)

func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	items, _ := s.db.InboxForUser(r.Context(), user.ID)
	s.render(w, "inbox", page{
		User:   user,
		Active: "inbox",
		Data:   struct{ Items []storage.InboxObject }{items},
	})
}

// handleInboxRespond accepts, declines, or dismisses an Inbox item. For a
// REQUEST, accept/decline records the response and delivers a REPLY to the
// organizer; dismiss just removes it.
func (s *Server) handleInboxRespond(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	href := r.FormValue("href")
	action := r.FormValue("action")
	uid := r.FormValue("uid")
	if href == "" {
		http.Error(w, "missing item", http.StatusBadRequest)
		return
	}

	switch action {
	case "accept", "decline", "tentative":
		if s.sched != nil && uid != "" {
			partstat := map[string]string{"accept": "ACCEPTED", "decline": "DECLINED", "tentative": "TENTATIVE"}[action]
			_ = s.sched.RespondLocal(r.Context(), user, uid, partstat)
		}
	}
	_ = s.db.DeleteInboxObject(r.Context(), user.ID, href)
	http.Redirect(w, r, "/inbox", http.StatusSeeOther)
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	s.render(w, "profile", page{
		User:   user,
		Active: "profile",
		Data:   struct{ Zones []string }{commonZones},
	})
}

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	tz := strings.TrimSpace(r.FormValue("display_tz"))
	if tz != "" {
		_ = s.db.SetDisplayTZ(r.Context(), user.ID, tz)
	}
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
}

// commonZones is a short list offered on the profile page.
var commonZones = []string{
	"UTC",
	"America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles",
	"Europe/London", "Europe/Paris", "Europe/Berlin", "Europe/Madrid",
	"Asia/Tokyo", "Asia/Shanghai", "Asia/Kolkata", "Australia/Sydney",
}
