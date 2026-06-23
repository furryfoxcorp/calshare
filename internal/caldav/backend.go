package caldav

import (
	"bytes"
	"context"
	"errors"
	"net/http"

	goical "github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"

	"github.com/furryfoxcorp/calshare/internal/ical"
	"github.com/furryfoxcorp/calshare/internal/scheduling"
	"github.com/furryfoxcorp/calshare/internal/storage"
)

// maxResourceSize is advertised to clients and enforced on PUT.
const maxResourceSize = 10 << 20 // 10 MiB

// Backend implements caldav.Backend over the project's storage.
type Backend struct {
	db    *storage.DB
	prefix string
	sched *scheduling.Scheduler // may be nil when scheduling is disabled
}

// NewBackend builds a CalDAV backend. prefix is the URL prefix the handler is
// mounted at, for example "/dav". sched may be nil to disable auto-schedule.
func NewBackend(db *storage.DB, prefix string, sched *scheduling.Scheduler) *Backend {
	return &Backend{db: db, prefix: prefix, sched: sched}
}

func authedUser(ctx context.Context) (*storage.User, error) {
	u, ok := UserFromContext(ctx)
	if !ok {
		return nil, webdav.NewHTTPError(http.StatusUnauthorized, errors.New("not authenticated"))
	}
	return u, nil
}

// CurrentUserPrincipal returns the authenticated user's principal path.
func (b *Backend) CurrentUserPrincipal(ctx context.Context) (string, error) {
	u, err := authedUser(ctx)
	if err != nil {
		return "", err
	}
	return b.principalPath(u.Email), nil
}

// CalendarHomeSetPath returns the authenticated user's calendar home set.
func (b *Backend) CalendarHomeSetPath(ctx context.Context) (string, error) {
	u, err := authedUser(ctx)
	if err != nil {
		return "", err
	}
	return b.homeSetPath(u.Email), nil
}

func componentSet(c *storage.Calendar) []string {
	if c.SupportsVTODO {
		return []string{ical.CompEvent, ical.CompTodo}
	}
	return []string{ical.CompEvent}
}

func (b *Backend) toCalDAV(email string, c *storage.Calendar) caldav.Calendar {
	return caldav.Calendar{
		Path:                  b.calendarPath(email, c.Slug),
		Name:                  c.DisplayName,
		Description:           c.Description,
		MaxResourceSize:       maxResourceSize,
		SupportedComponentSet: componentSet(c),
	}
}

// resolveCalendar finds the calendar named by a parsed path and checks the
// authenticated user may reach it. ACL-based sharing is added later; for now a
// user reaches only their own calendars.
func (b *Backend) resolveCalendar(ctx context.Context, p parsed) (*storage.User, *storage.Calendar, error) {
	authed, err := authedUser(ctx)
	if err != nil {
		return nil, nil, err
	}
	owner, err := b.db.UserByEmail(ctx, p.email)
	if err != nil {
		return nil, nil, webdav.NewHTTPError(http.StatusNotFound, err)
	}
	if owner.ID != authed.ID {
		return nil, nil, webdav.NewHTTPError(http.StatusForbidden, errors.New("not your calendar"))
	}
	cal, err := b.db.CalendarBySlug(ctx, owner.ID, p.slug)
	if err != nil {
		return nil, nil, webdav.NewHTTPError(http.StatusNotFound, err)
	}
	return owner, cal, nil
}

// CreateCalendar handles MKCALENDAR.
func (b *Backend) CreateCalendar(ctx context.Context, cal *caldav.Calendar) error {
	u, err := authedUser(ctx)
	if err != nil {
		return err
	}
	p := b.parsePath(cal.Path)
	supportsVTODO := false
	for _, comp := range cal.SupportedComponentSet {
		if comp == ical.CompTodo {
			supportsVTODO = true
		}
	}
	_, err = b.db.CreateCalendar(ctx, &storage.Calendar{
		UserID:        u.ID,
		Slug:          p.slug,
		SourceType:    "native",
		DisplayName:   cal.Name,
		Description:   cal.Description,
		SupportsVTODO: supportsVTODO,
	})
	return err
}

// ListCalendars returns the authenticated user's calendars.
func (b *Backend) ListCalendars(ctx context.Context) ([]caldav.Calendar, error) {
	u, err := authedUser(ctx)
	if err != nil {
		return nil, err
	}
	cals, err := b.db.CalendarsForUser(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	out := make([]caldav.Calendar, 0, len(cals))
	for i := range cals {
		out = append(out, b.toCalDAV(u.Email, &cals[i]))
	}
	return out, nil
}

// GetCalendar returns one calendar by path.
func (b *Backend) GetCalendar(ctx context.Context, path string) (*caldav.Calendar, error) {
	p := b.parsePath(path)
	owner, cal, err := b.resolveCalendar(ctx, p)
	if err != nil {
		return nil, err
	}
	c := b.toCalDAV(owner.Email, cal)
	return &c, nil
}

func decodeCalendar(blob []byte) (*goical.Calendar, error) {
	return goical.NewDecoder(bytes.NewReader(blob)).Decode()
}

func (b *Backend) toObject(email, slug string, o *storage.Object) (caldav.CalendarObject, error) {
	cal, err := decodeCalendar(o.Blob)
	if err != nil {
		return caldav.CalendarObject{}, err
	}
	return caldav.CalendarObject{
		Path:          b.objectPath(email, slug, o.Href),
		ModTime:       o.ModifiedAt,
		ContentLength: o.SizeBytes,
		ETag:          o.ETag,
		Data:          cal,
	}, nil
}

// GetCalendarObject returns one stored object by path.
func (b *Backend) GetCalendarObject(ctx context.Context, path string, req *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	p := b.parsePath(path)
	owner, cal, err := b.resolveCalendar(ctx, p)
	if err != nil {
		return nil, err
	}
	obj, err := b.db.ObjectByHref(ctx, cal.ID, p.object)
	if err != nil {
		return nil, webdav.NewHTTPError(http.StatusNotFound, err)
	}
	co, err := b.toObject(owner.Email, cal.Slug, obj)
	if err != nil {
		return nil, err
	}
	return &co, nil
}

// ListCalendarObjects returns every object in a calendar.
func (b *Backend) ListCalendarObjects(ctx context.Context, path string, req *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	p := b.parsePath(path)
	owner, cal, err := b.resolveCalendar(ctx, p)
	if err != nil {
		return nil, err
	}
	objs, err := b.db.ListObjects(ctx, cal.ID)
	if err != nil {
		return nil, err
	}
	out := make([]caldav.CalendarObject, 0, len(objs))
	for i := range objs {
		co, err := b.toObject(owner.Email, cal.Slug, &objs[i])
		if err != nil {
			return nil, err
		}
		out = append(out, co)
	}
	return out, nil
}

// QueryCalendarObjects filters a calendar's objects against a calendar-query
// REPORT.
func (b *Backend) QueryCalendarObjects(ctx context.Context, path string, query *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	all, err := b.ListCalendarObjects(ctx, path, &query.CompRequest)
	if err != nil {
		return nil, err
	}
	var out []caldav.CalendarObject
	for i := range all {
		ok, err := caldav.Match(query.CompFilter, &all[i])
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, all[i])
		}
	}
	return out, nil
}

// PutCalendarObject stores an object, honoring If-Match and If-None-Match.
func (b *Backend) PutCalendarObject(ctx context.Context, path string, cal *goical.Calendar, opts *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	p := b.parsePath(path)
	owner, calRow, err := b.resolveCalendar(ctx, p)
	if err != nil {
		return nil, err
	}

	blob, err := ical.Emit(cal)
	if err != nil {
		return nil, webdav.NewHTTPError(http.StatusBadRequest, err)
	}

	existing, getErr := b.db.ObjectByHref(ctx, calRow.ID, p.object)
	switch {
	case opts != nil && opts.IfNoneMatch.IsSet():
		if getErr == nil {
			return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("resource exists"))
		}
	case opts != nil && opts.IfMatch.IsSet():
		if getErr != nil {
			return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("resource does not exist"))
		}
		ok, err := opts.IfMatch.MatchETag(existing.ETag)
		if err != nil {
			return nil, webdav.NewHTTPError(http.StatusBadRequest, err)
		}
		if !ok {
			return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("etag mismatch"))
		}
	}

	obj, err := b.db.PutObject(ctx, calRow.ID, p.object, blob)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrUIDConflict):
			return nil, webdav.NewHTTPError(http.StatusConflict, err)
		case errors.Is(err, storage.ErrComponentSwap):
			return nil, webdav.NewHTTPError(http.StatusConflict, err)
		default:
			return nil, webdav.NewHTTPError(http.StatusBadRequest, err)
		}
	}

	// Auto-schedule: deliver invitations for events that carry attendees.
	// Failures here must not fail the PUT itself.
	if b.sched != nil && obj.HasScheduling {
		if authed, aerr := authedUser(ctx); aerr == nil {
			_, _ = b.sched.OnPut(ctx, obj, authed)
		}
	}

	return &caldav.CalendarObject{
		Path:          b.objectPath(owner.Email, calRow.Slug, obj.Href),
		ModTime:       obj.ModifiedAt,
		ContentLength: obj.SizeBytes,
		ETag:          obj.ETag,
	}, nil
}

// DeleteCalendarObject removes one object.
func (b *Backend) DeleteCalendarObject(ctx context.Context, path string) error {
	p := b.parsePath(path)
	_, cal, err := b.resolveCalendar(ctx, p)
	if err != nil {
		return err
	}
	if err := b.db.DeleteObject(ctx, cal.ID, p.object); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return webdav.NewHTTPError(http.StatusNotFound, err)
		}
		return err
	}
	return nil
}
