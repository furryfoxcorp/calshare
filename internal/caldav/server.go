package caldav

import (
	"net/http"
	"strings"

	"github.com/emersion/go-webdav/caldav"

	"github.com/furryfoxcorp/calshare/internal/storage"
)

// Server mounts the CalDAV handler with authentication.
type Server struct {
	db         *storage.DB
	prefix     string
	trustProxy bool
	handler    *caldav.Handler
}

// NewServer builds a CalDAV server. prefix is the mount point (for example
// "/dav"); trustProxy controls whether X-Forwarded-For is honored for client
// IP logging.
func NewServer(db *storage.DB, prefix string, trustProxy bool) *Server {
	prefix = "/" + strings.Trim(prefix, "/")
	return &Server{
		db:         db,
		prefix:     prefix,
		trustProxy: trustProxy,
		handler: &caldav.Handler{
			Backend: NewBackend(db, prefix),
			Prefix:  prefix,
		},
	}
}

// Handler returns the authenticated CalDAV http.Handler. Mount it for both the
// prefix subtree and /.well-known/caldav.
func (s *Server) Handler() http.Handler {
	return s.basicAuth(s.handler)
}

// Register attaches the CalDAV routes to a mux.
func (s *Server) Register(mux *http.ServeMux) {
	h := s.Handler()
	mux.Handle(s.prefix+"/", h)
	mux.Handle("/.well-known/caldav", h)
}
