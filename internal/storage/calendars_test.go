package storage

import (
	"context"
	"errors"
	"testing"
)

func testUser(t *testing.T, db *DB) *User {
	t.Helper()
	u, err := db.UpsertUserOnLogin(context.Background(), "sub-cal", "owner@example.com", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

const ev = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:%s\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260105T090000Z\r\nDTEND:20260105T100000Z\r\nSUMMARY:Thing\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func TestCreateCalendarDefaults(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	u := testUser(t, db)

	native, err := db.CreateCalendar(ctx, &Calendar{UserID: u.ID, DisplayName: "Home", SupportsVTODO: true})
	if err != nil {
		t.Fatalf("create native: %v", err)
	}
	if native.SourceType != "native" || !native.SupportsVTODO {
		t.Errorf("native defaults wrong: %+v", native)
	}
	if native.Slug == "" {
		t.Error("slug not generated")
	}

	ics, err := db.CreateCalendar(ctx, &Calendar{UserID: u.ID, DisplayName: "Holidays", SourceType: "ics", ICSURL: "https://example/feed.ics"})
	if err != nil {
		t.Fatalf("create ics: %v", err)
	}
	if ics.Slug == native.Slug {
		t.Error("slugs collided")
	}

	got, err := db.CalendarBySlug(ctx, u.ID, native.Slug)
	if err != nil {
		t.Fatalf("CalendarBySlug: %v", err)
	}
	if got.ID != native.ID {
		t.Error("slug lookup returned wrong calendar")
	}
}

func TestCalendarListAndDelete(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	u := testUser(t, db)
	c1, _ := db.CreateCalendar(ctx, &Calendar{UserID: u.ID, DisplayName: "A"})
	db.CreateCalendar(ctx, &Calendar{UserID: u.ID, DisplayName: "B"})
	list, err := db.CalendarsForUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d calendars, want 2", len(list))
	}
	if err := db.DeleteCalendar(ctx, c1.ID); err != nil {
		t.Fatal(err)
	}
	list, _ = db.CalendarsForUser(ctx, u.ID)
	if len(list) != 1 {
		t.Fatalf("after delete got %d, want 1", len(list))
	}
}

func TestCalendarByIDNotFound(t *testing.T) {
	db := newTestDB(t)
	_, err := db.CalendarByID(context.Background(), 999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
