package ical

import (
	"testing"
	"time"
)

func TestExpandWeeklyCount(t *testing.T) {
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC) // Monday
	window := Range{Start: start.Add(-time.Hour), End: start.AddDate(0, 0, 90)}
	occ, err := ExpandRRULE(start, "FREQ=WEEKLY;COUNT=5", nil, window)
	if err != nil {
		t.Fatalf("ExpandRRULE: %v", err)
	}
	if len(occ) != 5 {
		t.Fatalf("got %d occurrences, want 5", len(occ))
	}
	if !occ[0].Equal(start) {
		t.Errorf("first occurrence = %v, want %v", occ[0], start)
	}
	if !occ[1].Equal(start.AddDate(0, 0, 7)) {
		t.Errorf("second occurrence = %v, want +7d", occ[1])
	}
}

func TestExpandExdateRemoved(t *testing.T) {
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	skip := start.AddDate(0, 0, 7)
	window := Range{Start: start.Add(-time.Hour), End: start.AddDate(0, 0, 90)}
	occ, err := ExpandRRULE(start, "FREQ=WEEKLY;COUNT=5", []time.Time{skip}, window)
	if err != nil {
		t.Fatalf("ExpandRRULE: %v", err)
	}
	if len(occ) != 4 {
		t.Fatalf("got %d, want 4 after EXDATE", len(occ))
	}
	for _, o := range occ {
		if o.Equal(skip) {
			t.Fatal("EXDATE instant still present")
		}
	}
}

func TestExpandUnboundedClippedToWindow(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	window := Range{Start: start, End: start.AddDate(0, 0, 30)}
	occ, err := ExpandRRULE(start, "FREQ=DAILY", nil, window)
	if err != nil {
		t.Fatalf("ExpandRRULE: %v", err)
	}
	if len(occ) != 30 {
		t.Fatalf("got %d, want 30 daily occurrences in a 30-day window", len(occ))
	}
}

func TestExpandAcrossDSTKeepsWallClock(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load tz: %v", err)
	}
	// 9am daily starting before the spring-forward (2026-03-08 in US).
	start := time.Date(2026, 3, 6, 9, 0, 0, 0, ny)
	window := Range{Start: start.Add(-time.Hour), End: start.AddDate(0, 0, 6)}
	occ, err := ExpandRRULE(start, "FREQ=DAILY;COUNT=6", nil, window)
	if err != nil {
		t.Fatalf("ExpandRRULE: %v", err)
	}
	if len(occ) != 6 {
		t.Fatalf("got %d, want 6", len(occ))
	}
	for _, o := range occ {
		local := o.In(ny)
		if local.Hour() != 9 {
			t.Errorf("occurrence %v is not at 9am local (got hour %d)", local, local.Hour())
		}
	}
}
