// Package web serves the owner-facing HTML interface: a Mac OS X Lion styled
// dashboard for calendars, devices, and shared views. It renders html/template
// pages with htmx for inline updates and authenticates via web sessions.
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"time"

	"github.com/furryfoxcorp/calshare/internal/audit"
	"github.com/furryfoxcorp/calshare/internal/oidc"
	"github.com/furryfoxcorp/calshare/internal/storage"
)

//go:embed templates/*.html
var tmplFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Server renders and serves the web UI.
type Server struct {
	db          *storage.DB
	sessions    *oidc.Manager
	auth        *oidc.Authenticator // may be nil if OIDC is not yet reachable
	audit       *audit.Logger
	dataKey     []byte
	externalURL string
	pages       map[string]*template.Template
	login       *template.Template
	partials    *template.Template
}

// NewServer builds the web server. auth may be nil; when nil the login page
// explains that SSO is unavailable. audit may be nil to skip audit logging.
func NewServer(db *storage.DB, sessions *oidc.Manager, auth *oidc.Authenticator, audlog *audit.Logger, dataKey []byte, externalURL string) *Server {
	funcs := template.FuncMap{"date": fmtDate}

	pageFiles := map[string]string{
		"dashboard":   "dashboard.html",
		"devices":     "devices.html",
		"calendars":   "calendars.html",
		"views":       "views.html",
		"view_detail": "view_detail.html",
		"sources":     "sources.html",
		"placeholder": "placeholder.html",
	}
	pages := map[string]*template.Template{}
	for name, file := range pageFiles {
		t := template.Must(template.New("").Funcs(funcs).ParseFS(tmplFS,
			"templates/layout.html", "templates/partials.html", "templates/"+file))
		pages[name] = t
	}

	return &Server{
		db:          db,
		sessions:    sessions,
		auth:        auth,
		audit:       audlog,
		dataKey:     dataKey,
		externalURL: externalURL,
		pages:       pages,
		login:       template.Must(template.New("").Funcs(funcs).ParseFS(tmplFS, "templates/login.html")),
		partials:    template.Must(template.New("").Funcs(funcs).ParseFS(tmplFS, "templates/partials.html")),
	}
}

// page is the data envelope passed to every full-page template.
type page struct {
	User   *storage.User
	Active string
	Flash  string
	Data   any
}

// Register attaches all web routes to the mux.
func (s *Server) Register(mux *http.ServeMux) {
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheControl(http.FileServer(http.FS(static)))))

	// Public routes.
	mux.HandleFunc("GET /login", s.handleLogin)
	mux.HandleFunc("GET /oidc/login", s.handleOIDCLogin)
	mux.HandleFunc("GET /oidc/callback", s.handleOIDCCallback)
	mux.HandleFunc("POST /logout", s.handleLogout)

	// Authenticated app routes.
	app := http.NewServeMux()
	app.HandleFunc("GET /{$}", s.handleDashboard)
	app.HandleFunc("GET /calendars", s.handleCalendars)
	app.HandleFunc("POST /calendars", s.handleCreateCalendar)
	app.HandleFunc("POST /calendars/{id}/delete", s.handleDeleteCalendar)
	app.HandleFunc("GET /devices", s.handleDevices)
	app.HandleFunc("POST /devices", s.handleCreateDevice)
	app.HandleFunc("POST /devices/{id}/revoke", s.handleRevokeDevice)
	app.HandleFunc("GET /sources", s.handleSources)
	app.HandleFunc("POST /sources", s.handleAddSource)
	app.HandleFunc("POST /sources/{id}/poll", s.handlePollSource)
	app.HandleFunc("GET /views", s.handleViews)
	app.HandleFunc("POST /views", s.handleCreateView)
	app.HandleFunc("GET /views/{id}", s.handleViewDetail)
	app.HandleFunc("POST /views/{id}", s.handleUpdateView)
	app.HandleFunc("POST /views/{id}/calendars", s.handleToggleViewCalendar)
	app.HandleFunc("POST /views/{id}/tokens", s.handleCreateToken)
	app.HandleFunc("POST /tokens/{id}/revoke", s.handleRevokeToken)
	app.HandleFunc("GET /admin", s.placeholder("Admin", "Users, audit log, and system status."))

	mux.Handle("/", s.sessions.RequireUser(app))
}

func (s *Server) render(w http.ResponseWriter, name string, p page) {
	t, ok := s.pages[name]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", p); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// audited records an audit event for the current request, resolving the actor
// from the session.
func (s *Server) audited(r *http.Request, event, targetKind string, targetID int64, meta map[string]any) {
	var actor *int64
	kind := "anonymous"
	if u, ok := oidc.UserFromContext(r.Context()); ok {
		actor = &u.ID
		kind = "user"
		if u.IsAdmin {
			kind = "admin"
		}
	}
	var tid *int64
	if targetID != 0 {
		tid = &targetID
	}
	s.audit.Record(r.Context(), audit.Entry{
		ActorUserID: actor, ActorKind: kind, Event: event,
		TargetKind: targetKind, TargetID: tid, ClientIP: webClientIP(r), Metadata: meta,
	})
}

func webClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := indexByteWeb(xff, ','); i >= 0 {
			return trimSpaceWeb(xff[:i])
		}
		return trimSpaceWeb(xff)
	}
	return r.RemoteAddr
}

func indexByteWeb(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func trimSpaceWeb(s string) string {
	i, j := 0, len(s)
	for i < j && s[i] == ' ' {
		i++
	}
	for j > i && s[j-1] == ' ' {
		j--
	}
	return s[i:j]
}

func cacheControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		next.ServeHTTP(w, r)
	})
}

func fmtDate(v any) string {
	var t time.Time
	switch x := v.(type) {
	case time.Time:
		t = x
	case *time.Time:
		if x == nil {
			return "never"
		}
		t = *x
	default:
		return ""
	}
	return t.Local().Format("Jan 2, 2006")
}
