package web

import (
	"net/http"
	"strings"
)

// devLogin holds the credentials for the local development sign-in. It is only
// populated when CALDAV_DEV_LOGIN_PASSWORD is set, and must never be enabled in
// production: it bypasses OIDC and signs in as an admin.
type devLogin struct {
	email    string
	password string
}

// EnableDevLogin turns on the local password sign-in. A blank password leaves
// it disabled.
func (s *Server) EnableDevLogin(email, password string) {
	if password == "" {
		return
	}
	if email == "" {
		email = "dev@localhost"
	}
	s.dev = &devLogin{email: email, password: password}
}

func (s *Server) handleDevLogin(w http.ResponseWriter, r *http.Request) {
	if s.dev == nil {
		http.NotFound(w, r)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	if email == "" {
		email = s.dev.email
	}
	if r.FormValue("password") != s.dev.password || !strings.EqualFold(email, s.dev.email) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = s.login.ExecuteTemplate(w, "login.html", struct {
			Error    string
			DevLogin bool
		}{"Wrong development password.", true})
		return
	}
	user, err := s.db.UpsertUserOnLogin(r.Context(), "dev|"+strings.ToLower(email), email, devName(email))
	if err != nil {
		http.Error(w, "could not sign in", http.StatusInternalServerError)
		return
	}
	_ = s.db.SetAdmin(r.Context(), email, true)
	if err := s.sessions.StartSession(w, r, user.ID); err != nil {
		http.Error(w, "could not start session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func devName(email string) string {
	if i := strings.IndexByte(email, '@'); i > 0 {
		return strings.Title(email[:i])
	}
	return email
}
