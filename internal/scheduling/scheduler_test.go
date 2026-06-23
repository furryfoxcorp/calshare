package scheduling

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/furryfoxcorp/calshare/internal/storage"
)

func newDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "sched.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestOnPutDeliversLocalAndQueuesExternal(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	owner, _ := db.UpsertUserOnLogin(ctx, "owner", "owner@example.com", "Owner")
	zoe, _ := db.UpsertUserOnLogin(ctx, "zoe", "zoe@example.com", "Zoe")
	cal, _ := db.CreateCalendar(ctx, &storage.Calendar{UserID: owner.ID, DisplayName: "Home"})

	obj, err := db.PutObject(ctx, cal.ID, "meet.ics", []byte(inviteEvent))
	if err != nil {
		t.Fatal(err)
	}

	res, err := New(db).OnPut(ctx, obj, owner)
	if err != nil {
		t.Fatalf("OnPut: %v", err)
	}
	if res.LocalDelivered != 1 {
		t.Errorf("local delivered = %d, want 1", res.LocalDelivered)
	}
	if res.ExternalQueued != 1 {
		t.Errorf("external queued = %d, want 1", res.ExternalQueued)
	}

	// Zoe should have an Inbox REQUEST.
	inbox, _ := db.InboxForUser(ctx, zoe.ID)
	if len(inbox) != 1 || inbox[0].Method != "REQUEST" {
		t.Fatalf("zoe inbox = %+v, want one REQUEST", inbox)
	}

	// The external guest should be queued.
	due, _ := db.DueIMIP(ctx, time.Now().UTC(), 10)
	if len(due) != 1 || due[0].ToAddress != "guest@elsewhere.com" {
		t.Fatalf("imip queue = %+v, want one to guest", due)
	}

	// Attendee state recorded for both.
	states, _ := db.AttendeesForObject(ctx, obj.ID)
	if len(states) != 2 {
		t.Fatalf("attendee states = %d, want 2", len(states))
	}
}

func TestOnPutNoSchedulingNoop(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	owner, _ := db.UpsertUserOnLogin(ctx, "o", "o@example.com", "O")
	cal, _ := db.CreateCalendar(ctx, &storage.Calendar{UserID: owner.ID, DisplayName: "Home"})
	plain := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:solo\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260110T150000Z\r\nSUMMARY:Solo\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	obj, _ := db.PutObject(ctx, cal.ID, "solo.ics", []byte(plain))
	res, err := New(db).OnPut(ctx, obj, owner)
	if err != nil {
		t.Fatal(err)
	}
	if res.LocalDelivered != 0 || res.ExternalQueued != 0 {
		t.Errorf("expected no scheduling for an event without attendees")
	}
}

func TestScheduleStatusWritten(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	owner, _ := db.UpsertUserOnLogin(ctx, "owner", "owner@example.com", "Owner")
	db.UpsertUserOnLogin(ctx, "zoe", "zoe@example.com", "Zoe")
	cal, _ := db.CreateCalendar(ctx, &storage.Calendar{UserID: owner.ID, DisplayName: "Home"})
	obj, _ := db.PutObject(ctx, cal.ID, "meet.ics", []byte(inviteEvent))

	if _, err := New(db).OnPut(ctx, obj, owner); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := db.ObjectByHref(ctx, cal.ID, "meet.ics")
	if !contains(string(reloaded.Blob), "SCHEDULE-STATUS") {
		t.Errorf("stored event not annotated with SCHEDULE-STATUS:\n%s", reloaded.Blob)
	}
}

func TestOnDeleteSendsCancel(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	owner, _ := db.UpsertUserOnLogin(ctx, "owner", "owner@example.com", "Owner")
	zoe, _ := db.UpsertUserOnLogin(ctx, "zoe", "zoe@example.com", "Zoe")
	cal, _ := db.CreateCalendar(ctx, &storage.Calendar{UserID: owner.ID, DisplayName: "Home"})
	obj, _ := db.PutObject(ctx, cal.ID, "meet.ics", []byte(inviteEvent))

	if err := New(db).OnDelete(ctx, obj, owner); err != nil {
		t.Fatal(err)
	}
	inbox, _ := db.InboxForUser(ctx, zoe.ID)
	if len(inbox) != 1 || inbox[0].Method != "CANCEL" {
		t.Fatalf("zoe should have a CANCEL, got %+v", inbox)
	}
	due, _ := db.DueIMIP(ctx, timeNow(), 10)
	if len(due) != 1 || due[0].Method != "CANCEL" {
		t.Fatalf("external CANCEL should be queued, got %+v", due)
	}
}

func TestRespondLocalDeliversReply(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	owner, _ := db.UpsertUserOnLogin(ctx, "owner", "owner@example.com", "Owner")
	zoe, _ := db.UpsertUserOnLogin(ctx, "zoe", "zoe@example.com", "Zoe")
	cal, _ := db.CreateCalendar(ctx, &storage.Calendar{UserID: owner.ID, DisplayName: "Home"})
	obj, _ := db.PutObject(ctx, cal.ID, "meet.ics", []byte(inviteEvent))
	New(db).OnPut(ctx, obj, owner)

	if err := New(db).RespondLocal(ctx, zoe, "meet-1", "ACCEPTED"); err != nil {
		t.Fatalf("RespondLocal: %v", err)
	}
	states, _ := db.AttendeesForObject(ctx, obj.ID)
	found := false
	for _, st := range states {
		if st.Email == "zoe@example.com" && st.Partstat == "ACCEPTED" {
			found = true
		}
	}
	if !found {
		t.Errorf("zoe partstat not updated: %+v", states)
	}
	inbox, _ := db.InboxForUser(ctx, owner.ID)
	hasReply := false
	for _, o := range inbox {
		if o.Method == "REPLY" {
			hasReply = true
		}
	}
	if !hasReply {
		t.Error("organizer should receive a REPLY")
	}
}

func contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}

func timeNow() time.Time { return time.Now().UTC() }
