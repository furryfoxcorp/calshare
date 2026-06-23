package share

import (
	"sync"
	"time"
)

// limiter is a per-token sliding-window rate limiter: at most `max` requests
// per `window`. It holds recent request timestamps per token id in memory.
type limiter struct {
	mu     sync.Mutex
	hits   map[int64][]time.Time
	max    int
	window time.Duration
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{hits: map[int64][]time.Time{}, max: max, window: window}
}

// allow reports whether a request for tokenID is permitted right now, and if
// not, how long until it would be.
func (l *limiter) allow(tokenID int64, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-l.window)
	times := l.hits[tokenID]
	kept := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		retry := kept[0].Add(l.window).Sub(now)
		if retry < 0 {
			retry = 0
		}
		l.hits[tokenID] = kept
		return false, retry
	}
	kept = append(kept, now)
	l.hits[tokenID] = kept
	return true, 0
}
