package caldav

import (
	"errors"
	"net"
	"net/http"

	"github.com/furryfoxcorp/calshare/internal/storage"
)

// basicAuth wraps next with HTTP Basic authentication backed by app passwords.
// On success the authenticated user is placed in the request context. On
// failure it returns a 401 with a Basic challenge.
func (s *Server) basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		email, pass, ok := r.BasicAuth()
		if !ok || email == "" || pass == "" {
			challenge(w)
			return
		}
		user, ap, err := s.db.MatchAppPassword(r.Context(), email, pass)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				challenge(w)
				return
			}
			http.Error(w, "auth error", http.StatusInternalServerError)
			return
		}
		// Record usage without blocking the request on a write failure.
		_ = s.db.TouchAppPassword(r.Context(), ap.ID, clientIP(r, s.trustProxy))

		ctx := withUser(r.Context(), user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="calshare CalDAV"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

// clientIP returns the best guess at the client address, honoring
// X-Forwarded-For only when the server is configured to trust proxy headers.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// First entry is the original client.
			for i := 0; i < len(xff); i++ {
				if xff[i] == ',' {
					return trimSpace(xff[:i])
				}
			}
			return trimSpace(xff)
		}
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			return xr
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && s[start] == ' ' {
		start++
	}
	end := len(s)
	for end > start && s[end-1] == ' ' {
		end--
	}
	return s[start:end]
}
