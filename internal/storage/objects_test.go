package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestPutObjectInsertAndDenormalize(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	u := testUser(t, db)
	cal, _ := db.CreateCalendar(ctx, &Calendar{UserID: u.ID, DisplayName: "Home"})

	blob := []byte(fmt.Sprintf(ev, "obj-1"))
	obj, err := db.PutObject(ctx, cal.ID, "obj-1.ics", blob)
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if obj.UID != "obj-1" {
		t.Errorf("UID = %q", obj.UID)
	}
	if obj.First == nil {
		t.Error("first_occurrence not denormalized")
	}
	if obj.ETag == "" {
		t.Error("etag not set")
	}

	// Calendar sync_seq should have advanced from 0 to 1.
	got, _ := db.CalendarByID(ctx, cal.ID)
	if got.SyncSeq != 1 {
		t.Errorf("sync_seq = %d, want 1", got.SyncSeq)
	}

	changes, _ := db.ChangesSince(ctx, cal.ID, 0)
	if len(changes) != 1 || changes[0].Op != "added" {
		t.Errorf("changes = %+v, want one added", changes)
	}
}

func TestPutObjectUpdateWritesModified(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	u := testUser(t, db)
	cal, _ := db.CreateCalendar(ctx, &Calendar{UserID: u.ID, DisplayName: "Home"})

	db.PutObject(ctx, cal.ID, "o.ics", []byte(fmt.Sprintf(ev, "o")))
	updated := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:o\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260105T090000Z\r\nDTEND:20260105T110000Z\r\nSUMMARY:Changed\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if _, err := db.PutObject(ctx, cal.ID, "o.ics", []byte(updated)); err != nil {
		t.Fatalf("update PutObject: %v", err)
	}
	changes, _ := db.ChangesSince(ctx, cal.ID, 0)
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2", len(changes))
	}
	if changes[1].Op != "modified" {
		t.Errorf("second op = %q, want modified", changes[1].Op)
	}
}

func TestPutObjectUIDConflict(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	u := testUser(t, db)
	cal, _ := db.CreateCalendar(ctx, &Calendar{UserID: u.ID, DisplayName: "Home"})

	db.PutObject(ctx, cal.ID, "first.ics", []byte(fmt.Sprintf(ev, "dup")))
	_, err := db.PutObject(ctx, cal.ID, "second.ics", []byte(fmt.Sprintf(ev, "dup")))
	if !errors.Is(err, ErrUIDConflict) {
		t.Fatalf("err = %v, want ErrUIDConflict", err)
	}
}

func TestPutObjectComponentSwapRejected(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	u := testUser(t, db)
	cal, _ := db.CreateCalendar(ctx, &Calendar{UserID: u.ID, DisplayName: "Home"})

	db.PutObject(ctx, cal.ID, "x.ics", []byte(fmt.Sprintf(ev, "x")))
	todo := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VTODO\r\nUID:x\r\nDTSTAMP:20260101T000000Z\r\nDUE:20260105T090000Z\r\nSUMMARY:Now a task\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	_, err := db.PutObject(ctx, cal.ID, "x.ics", []byte(todo))
	if !errors.Is(err, ErrComponentSwap) {
		t.Fatalf("err = %v, want ErrComponentSwap", err)
	}
}

func TestPutObjectUnboundedRRULENullLast(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	u := testUser(t, db)
	cal, _ := db.CreateCalendar(ctx, &Calendar{UserID: u.ID, DisplayName: "Home"})

	blob := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:rr\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260105T090000Z\r\nRRULE:FREQ=DAILY\r\nSUMMARY:Daily\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	obj, err := db.PutObject(ctx, cal.ID, "rr.ics", []byte(blob))
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if !obj.HasRRULE {
		t.Error("has_rrule should be true")
	}
	if obj.Last != nil {
		t.Errorf("unbounded RRULE Last = %v, want nil", obj.Last)
	}
}

func TestDeleteObject(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	u := testUser(t, db)
	cal, _ := db.CreateCalendar(ctx, &Calendar{UserID: u.ID, DisplayName: "Home"})

	db.PutObject(ctx, cal.ID, "d.ics", []byte(fmt.Sprintf(ev, "d")))
	if err := db.DeleteObject(ctx, cal.ID, "d.ics"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if _, err := db.ObjectByHref(ctx, cal.ID, "d.ics"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("object still present: %v", err)
	}
	changes, _ := db.ChangesSince(ctx, cal.ID, 0)
	if changes[len(changes)-1].Op != "deleted" {
		t.Errorf("last op = %q, want deleted", changes[len(changes)-1].Op)
	}
}

func TestChangesSinceFiltersBySeq(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	u := testUser(t, db)
	cal, _ := db.CreateCalendar(ctx, &Calendar{UserID: u.ID, DisplayName: "Home"})

	db.PutObject(ctx, cal.ID, "a.ics", []byte(fmt.Sprintf(ev, "a")))
	cur, _ := db.CalendarByID(ctx, cal.ID)
	db.PutObject(ctx, cal.ID, "b.ics", []byte(fmt.Sprintf(ev, "b")))

	all, _ := db.ChangesSince(ctx, cal.ID, 0)
	if len(all) != 2 {
		t.Fatalf("ChangesSince(0) = %d, want 2", len(all))
	}
	recent, _ := db.ChangesSince(ctx, cal.ID, cur.SyncSeq)
	if len(recent) != 1 || recent[0].Href != "b.ics" {
		t.Fatalf("ChangesSince(%d) = %+v, want only b.ics", cur.SyncSeq, recent)
	}
}
