// Package sources polls external ICS feeds and folds their events into the
// per-user calendar model. One sweep runs on a ticker; each feed is polled
// when its own interval has elapsed, with ETag and If-Modified-Since to avoid
// refetching unchanged feeds.
package sources

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	goical "github.com/emersion/go-ical"

	"github.com/furryfoxcorp/calshare/internal/ical"
	"github.com/furryfoxcorp/calshare/internal/storage"
)

// Poller fetches ICS feeds and syncs their events into storage.
type Poller struct {
	db              *storage.DB
	client          *http.Client
	logger          *slog.Logger
	defaultInterval time.Duration
}

// New builds a poller. defaultInterval is used for feeds with no explicit
// interval.
func New(db *storage.DB, defaultInterval time.Duration, logger *slog.Logger) *Poller {
	return &Poller{
		db:              db,
		client:          &http.Client{Timeout: 30 * time.Second},
		logger:          logger,
		defaultInterval: defaultInterval,
	}
}

// Run sweeps due feeds every minute until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	p.sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.sweep(ctx)
		}
	}
}

func (p *Poller) sweep(ctx context.Context) {
	cals, err := p.db.ICSCalendars(ctx)
	if err != nil {
		p.logger.Error("ics sweep: list calendars", "err", err)
		return
	}
	now := time.Now().UTC()
	for i := range cals {
		c := cals[i]
		if !p.due(&c, now) {
			continue
		}
		if err := p.PollOnce(ctx, &c); err != nil {
			p.logger.Warn("ics poll failed", "calendar_id", c.ID, "url", c.ICSURL, "err", err)
		}
	}
}

func (p *Poller) due(c *storage.Calendar, now time.Time) bool {
	if c.ICSURL == "" {
		return false
	}
	if c.ICSLastPolledAt == nil {
		return true
	}
	interval := p.defaultInterval
	if c.ICSPollInterval > 0 {
		interval = time.Duration(c.ICSPollInterval) * time.Second
	}
	return now.Sub(*c.ICSLastPolledAt) >= interval
}

// PollOnce fetches one feed and syncs it. It records poll state regardless of
// outcome and never deletes existing events on a transport error.
func (p *Poller) PollOnce(ctx context.Context, c *storage.Calendar) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.ICSURL, nil)
	if err != nil {
		return err
	}
	if c.ICSETag != "" {
		req.Header.Set("If-None-Match", c.ICSETag)
	}
	if c.ICSLastModified != "" {
		req.Header.Set("If-Modified-Since", c.ICSLastModified)
	}
	if c.ICSBasicUser != "" {
		req.SetBasicAuth(c.ICSBasicUser, "")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		_ = p.db.UpdateICSPollState(ctx, c.ID, c.ICSETag, c.ICSLastModified, "unreachable", err.Error())
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotModified:
		return p.db.UpdateICSPollState(ctx, c.ID, c.ICSETag, c.ICSLastModified, "not_modified", "")
	case resp.StatusCode >= 400:
		_ = p.db.UpdateICSPollState(ctx, c.ID, c.ICSETag, c.ICSLastModified, "http_error", resp.Status)
		return fmt.Errorf("feed returned %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 25<<20))
	if err != nil {
		_ = p.db.UpdateICSPollState(ctx, c.ID, c.ICSETag, c.ICSLastModified, "unreachable", err.Error())
		return err
	}
	if err := p.sync(ctx, c, body); err != nil {
		_ = p.db.UpdateICSPollState(ctx, c.ID, c.ICSETag, c.ICSLastModified, "parse_error", err.Error())
		return err
	}
	return p.db.UpdateICSPollState(ctx, c.ID, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), "ok", "")
}

// sync diffs the feed's events against stored objects: upsert each VEVENT UID,
// delete objects no longer present.
func (p *Poller) sync(ctx context.Context, c *storage.Calendar, body []byte) error {
	feed, err := goical.NewDecoder(bytes.NewReader(body)).Decode()
	if err != nil {
		return err
	}

	// Group VEVENT components by UID (master plus RECURRENCE-ID overrides).
	byUID := map[string][]*goical.Component{}
	for _, child := range feed.Children {
		if child.Name != ical.CompEvent {
			continue
		}
		uid := ""
		if p := child.Props.Get("UID"); p != nil {
			uid = p.Value
		}
		if uid == "" {
			continue
		}
		byUID[uid] = append(byUID[uid], child)
	}

	seen := map[string]bool{}
	for uid, comps := range byUID {
		href := sanitizeHref(uid) + ".ics"
		seen[href] = true

		cal := goical.NewCalendar()
		cal.Props.SetText("VERSION", "2.0")
		cal.Props.SetText("PRODID", "-//furryfoxcorp//calshare//EN")
		cal.Children = append(cal.Children, comps...)
		if err := ical.BundleTimezones(cal); err != nil {
			return err
		}
		blob, err := ical.Emit(cal)
		if err != nil {
			continue
		}
		if _, err := p.db.PutObject(ctx, c.ID, href, blob); err != nil {
			// Skip a single bad object rather than failing the whole feed.
			p.logger.Warn("ics sync: store object", "calendar_id", c.ID, "uid", uid, "err", err)
		}
	}

	existing, err := p.db.ListObjects(ctx, c.ID)
	if err != nil {
		return err
	}
	for i := range existing {
		if !seen[existing[i].Href] {
			_ = p.db.DeleteObject(ctx, c.ID, existing[i].Href)
		}
	}
	return nil
}

func sanitizeHref(uid string) string {
	var b strings.Builder
	for _, r := range uid {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s := b.String()
	if s == "" {
		return "object"
	}
	return s
}
