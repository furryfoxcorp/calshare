// Package share serves the public share-link endpoint at /share/<token>.ics.
// It resolves an opaque token to a view, applies the view's privacy filter to
// the underlying calendars, and emits a single iCalendar body. Missing,
// revoked, or expired tokens return 404 so calendar clients stop polling
// quietly instead of prompting for credentials they cannot supply.
package share

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	goical "github.com/emersion/go-ical"

	"github.com/furryfoxcorp/calshare/internal/ical"
	"github.com/furryfoxcorp/calshare/internal/storage"
	"github.com/furryfoxcorp/calshare/internal/views"
)

const (
	lookback  = 90 * 24 * time.Hour
	lookahead = 400 * 24 * time.Hour
)

// Server serves share links.
type Server struct {
	db      *storage.DB
	limiter *limiter
	now     func() time.Time
}

// NewServer builds a share-link server.
func NewServer(db *storage.DB) *Server {
	return &Server{
		db:      db,
		limiter: newLimiter(60, 5*time.Minute),
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// Register attaches the share route.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /share/{file}", s.handle)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	secret := strings.TrimSuffix(r.PathValue("file"), ".ics")
	if secret == "" {
		http.NotFound(w, r)
		return
	}

	token, err := s.db.ShareTokenBySecret(r.Context(), secret)
	if err != nil || !token.Usable(s.now()) {
		http.NotFound(w, r)
		return
	}

	if token.HasPassword {
		_, pass, ok := r.BasicAuth()
		if !ok || !token.CheckSharePassword(pass) {
			w.Header().Set("WWW-Authenticate", `Basic realm="calshare share link"`)
			http.Error(w, "password required", http.StatusUnauthorized)
			return
		}
	}

	if ok, retry := s.limiter.allow(token.ID, s.now()); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	view, err := s.db.ViewByID(r.Context(), token.ViewID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	body, err := s.render(r, view)
	if err != nil {
		http.Error(w, "could not build calendar", http.StatusInternalServerError)
		return
	}

	etag := `"` + ical.ETag(body) + `"`
	_ = s.db.TouchShareToken(r.Context(), token.ID, clientIP(r))

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8; component=vevent")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", safeFilename(view.Name)+".ics"))
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=900")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")

	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(body)
}

// render resolves the view's calendars, applies the filter per calendar, and
// returns the emitted iCalendar bytes.
func (s *Server) render(r *http.Request, view *storage.View) ([]byte, error) {
	ctx := r.Context()
	window := ical.Range{Start: s.now().Add(-lookback), End: s.now().Add(lookahead)}

	out := goical.NewCalendar()
	out.Props.SetText("VERSION", "2.0")
	out.Props.SetText("PRODID", "-//furryfoxcorp//calshare//EN")
	out.Props.SetText("X-WR-CALNAME", view.Name)

	vcs, err := s.db.ViewCalendars(ctx, view.ID)
	if err != nil {
		return nil, err
	}
	for _, vc := range vcs {
		cal, err := s.db.CalendarByID(ctx, vc.CalendarID)
		if err != nil {
			continue
		}
		objs, err := s.db.ListObjects(ctx, cal.ID)
		if err != nil {
			return nil, err
		}
		combined := goical.NewCalendar()
		for i := range objs {
			if !overlaps(&objs[i], window) {
				continue
			}
			src, err := goical.NewDecoder(bytes.NewReader(objs[i].Blob)).Decode()
			if err != nil {
				continue
			}
			for _, child := range src.Children {
				if child.Name == ical.CompEvent {
					combined.Children = append(combined.Children, child)
				}
			}
		}
		spec := buildSpec(view, vc)
		filtered, err := views.Apply(combined, spec)
		if err != nil {
			return nil, err
		}
		for _, child := range filtered.Children {
			if child.Name == ical.CompEvent {
				out.Children = append(out.Children, child)
			}
		}
	}

	if err := ical.BundleTimezones(out); err != nil {
		return nil, err
	}
	return ical.Emit(out)
}

func overlaps(o *storage.Object, w ical.Range) bool {
	if o.Last == nil {
		return true // unbounded recurrence always overlaps the lookahead
	}
	if o.First != nil && o.First.After(w.End) {
		return false
	}
	if o.Last.Before(w.Start) {
		return false
	}
	return true
}

// buildSpec turns a view plus a per-calendar membership into a resolved filter
// spec.
func buildSpec(view *storage.View, vc storage.ViewCalendar) views.Spec {
	preset := view.Preset
	if vc.OverridePreset != "" {
		preset = vc.OverridePreset
	}
	overrides := parseFields(view.FieldsJSON)
	for k, v := range parseFields(vc.FieldsJSON) {
		overrides[k] = v
	}
	return views.Spec{
		Preset:             views.Preset(preset),
		BusyLabel:          view.BusyLabel,
		IncludePrivate:     view.IncludePrivate,
		IncludeCancelled:   view.IncludeCancelled,
		IncludeTentative:   view.IncludeTentative,
		IncludeTransparent: view.IncludeTransparent,
		FieldOverrides:     overrides,
	}
}

func parseFields(jsonStr string) map[string]views.Rule {
	out := map[string]views.Rule{}
	if jsonStr == "" {
		return out
	}
	var raw map[string]string
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return out
	}
	for k, v := range raw {
		out[k] = views.Rule(v)
	}
	return out
}

func safeFilename(name string) string {
	if name == "" {
		return "calendar"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == ' ':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.TrimSpace(b.String())
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}
