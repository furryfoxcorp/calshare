// Package caldav serves the CalDAV surface at /dav. It implements the
// go-webdav caldav.Backend over the project's SQLite storage and adds
// app-password Basic authentication. Auto-schedule, ACL, and the
// schedule Inbox/Outbox are layered on in later work.
package caldav

import (
	"context"

	"github.com/furryfoxcorp/calshare/internal/storage"
)

type ctxKey int

const userCtxKey ctxKey = iota

// withUser returns a context carrying the authenticated CalDAV user.
func withUser(ctx context.Context, u *storage.User) context.Context {
	return context.WithValue(ctx, userCtxKey, u)
}

// UserFromContext returns the authenticated user, if any.
func UserFromContext(ctx context.Context) (*storage.User, bool) {
	u, ok := ctx.Value(userCtxKey).(*storage.User)
	return u, ok
}
