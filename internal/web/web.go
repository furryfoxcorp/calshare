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
	externalURL string
	pages       map[string]*template.Template
	login       *template.Template
	partials    *template.Template
}

// NewServer builds the web server. auth may be nil; when nil the login page
// explains that SSO is unavailable.
func NewServer(db *storage.DB, sessions *oidc.Manager, auth *oidc.Authenticator, externalURL string) *Server {
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
