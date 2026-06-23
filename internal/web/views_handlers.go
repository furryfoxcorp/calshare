package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/furryfoxcorp/calshare/internal/oidc"
	"github.com/furryfoxcorp/calshare/internal/storage"
)

// handleGenPassword returns a fresh random password inside a replacement input
// element, for the token form's Generate button.
func (s *Server) handleGenPassword(w http.ResponseWriter, r *http.Request) {
	pw, err := generateAppPassword()
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	// Trim to a friendlier 14 characters for a share-link password.
	pw = strings.ReplaceAll(pw, "-", "")
	if len(pw) > 14 {
		pw = pw[:14]
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<input type="text" id="password" name="password" value="` + pw + `">`))
}

func (s *Server) handleViews(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	views, _ := s.db.ViewsForUser(r.Context(), user.ID)
	s.render(w, "views", page{
		User:   user,
		Active: "views",
		Data:   struct{ Views []storage.View }{views},
	})
}

func (s *Server) handleCreateView(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	preset := r.FormValue("preset")
	v, err := s.db.CreateView(r.Context(), &storage.View{
		UserID:           user.ID,
		Name:             name,
		Preset:           preset,
		IncludeTentative: true,
	})
	if err != nil {
		http.Error(w, "could not create view", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/views/"+strconv.FormatInt(v.ID, 10), http.StatusSeeOther)
}

// loadOwnedView fetches a view and confirms the requester owns it.
func (s *Server) loadOwnedView(w http.ResponseWriter, r *http.Request) (*storage.View, bool) {
	user, _ := oidc.UserFromContext(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return nil, false
	}
	v, err := s.db.ViewByID(r.Context(), id)
	if err != nil || v.UserID != user.ID {
		http.Error(w, "not found", http.StatusNotFound)
		return nil, false
	}
	return v, true
}

type viewDetailData struct {
	View      *storage.View
	Calendars []storage.Calendar
	Selected  map[int64]bool
	Tokens    []storage.ShareToken
}

func (s *Server) viewDetailData(r *http.Request, v *storage.View) viewDetailData {
	ctx := r.Context()
	cals, _ := s.db.CalendarsForUser(ctx, v.UserID)
	vcs, _ := s.db.ViewCalendars(ctx, v.ID)
	selected := map[int64]bool{}
	for _, vc := range vcs {
		selected[vc.CalendarID] = true
	}
	all, _ := s.db.TokensForView(ctx, v.ID)
	tokens := make([]storage.ShareToken, 0, len(all))
	for _, t := range all {
		if t.RevokedAt == nil {
			tokens = append(tokens, t)
		}
	}
	return viewDetailData{View: v, Calendars: cals, Selected: selected, Tokens: tokens}
}

func (s *Server) handleViewDetail(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadOwnedView(w, r)
	if !ok {
		return
	}
	user, _ := oidc.UserFromContext(r.Context())
	s.render(w, "view_detail", page{User: user, Active: "views", Data: s.viewDetailData(r, v)})
}

func (s *Server) handleUpdateView(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadOwnedView(w, r)
	if !ok {
		return
	}
	v.Name = strings.TrimSpace(r.FormValue("name"))
	v.Preset = r.FormValue("preset")
	v.BusyLabel = strings.TrimSpace(r.FormValue("busy_label"))
	if v.BusyLabel == "" {
		v.BusyLabel = "Busy"
	}
	v.IncludePrivate = r.FormValue("include_private") == "1"
	v.IncludeCancelled = r.FormValue("include_cancelled") == "1"
	v.IncludeTentative = r.FormValue("include_tentative") == "1"
	v.IncludeTransparent = r.FormValue("include_transparent") == "1"
	if err := s.db.UpdateView(r.Context(), v); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/views/"+strconv.FormatInt(v.ID, 10), http.StatusSeeOther)
}

func (s *Server) handleToggleViewCalendar(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadOwnedView(w, r)
	if !ok {
		return
	}
	calID, err := strconv.ParseInt(r.FormValue("calendar_id"), 10, 64)
	if err != nil {
		http.Error(w, "bad calendar id", http.StatusBadRequest)
		return
	}
	cal, err := s.db.CalendarByID(r.Context(), calID)
	if err != nil || cal.UserID != v.UserID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Toggle membership based on current state.
	vcs, _ := s.db.ViewCalendars(r.Context(), v.ID)
	member := false
	for _, vc := range vcs {
		if vc.CalendarID == calID {
			member = true
			break
		}
	}
	if member {
		_ = s.db.RemoveViewCalendar(r.Context(), v.ID, calID)
	} else {
		_ = s.db.SetViewCalendar(r.Context(), &storage.ViewCalendar{ViewID: v.ID, CalendarID: calID})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadOwnedView(w, r)
	if !ok {
		return
	}
	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" {
		http.Error(w, "a name is required", http.StatusBadRequest)
		return
	}
	var expires *time.Time
	if d := r.FormValue("expires"); d != "" {
		if t, err := time.Parse("2006-01-02", d); err == nil {
			// Expire at the end of the chosen day, UTC.
			end := t.Add(24*time.Hour - time.Second)
			expires = &end
		}
	}
	secret, err := storage.NewShareTokenSecret()
	if err != nil {
		http.Error(w, "could not generate link", http.StatusInternalServerError)
		return
	}
	tokenID, err := s.db.CreateShareToken(r.Context(), v.ID, label, secret, r.FormValue("password"), expires)
	if err != nil {
		http.Error(w, "could not save link", http.StatusInternalServerError)
		return
	}
	s.audited(r, "share_token.create", "view", v.ID, map[string]any{"label": label, "token_id": tokenID})

	base := strings.TrimRight(s.externalURL, "/") + "/share/" + secret + ".ics"
	webcal := base
	if strings.HasPrefix(webcal, "https://") {
		webcal = "webcal://" + strings.TrimPrefix(webcal, "https://")
	} else if strings.HasPrefix(webcal, "http://") {
		webcal = "webcal://" + strings.TrimPrefix(webcal, "http://")
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.partials.ExecuteTemplate(w, "tokenCreated", struct {
		Label, WebcalURL, HTTPSURL string
	}{label, webcal, base})
	_ = s.partials.ExecuteTemplate(w, "tokenListOOB", s.viewDetailData(r, v))
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	user, _ := oidc.UserFromContext(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	// Confirm the token belongs to one of the user's views before revoking.
	tok, view, ok := s.resolveToken(r, id)
	if !ok || view.UserID != user.ID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	_ = s.db.RevokeShareToken(r.Context(), tok.ID)
	s.audited(r, "share_token.revoke", "view", view.ID, map[string]any{"token_id": tok.ID})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.partials.ExecuteTemplate(w, "tokenList", s.viewDetailData(r, view))
}

// resolveToken finds a token and its view by scanning the user's views. The
// token table has no direct user link, so we look it up through ownership.
func (s *Server) resolveToken(r *http.Request, tokenID int64) (*storage.ShareToken, *storage.View, bool) {
	user, _ := oidc.UserFromContext(r.Context())
	views, _ := s.db.ViewsForUser(r.Context(), user.ID)
	for i := range views {
		toks, _ := s.db.TokensForView(r.Context(), views[i].ID)
		for j := range toks {
			if toks[j].ID == tokenID {
				return &toks[j], &views[i], true
			}
		}
	}
	return nil, nil, false
}
