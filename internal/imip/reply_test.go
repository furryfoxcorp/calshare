package imip

import (
	"context"
	"testing"
	"time"

	"github.com/furryfoxcorp/calshare/internal/storage"
)

const storedInvite = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:meet-1\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260110T150000Z\r\nDTEND:20260110T160000Z\r\nSUMMARY:Planning\r\nORGANIZER:mailto:owner@example.com\r\nATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:guest@elsewhere.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

const replyICal = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//c//EN\r\nMETHOD:REPLY\r\nBEGIN:VEVENT\r\nUID:meet-1\r\nDTSTAMP:20260102T000000Z\r\nORGANIZER:mailto:owner@example.com\r\nATTENDEE;PARTSTAT=ACCEPTED:mailto:guest@elsewhere.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func TestApplyReplyUpdatesPartstat(t *testing.T) {
	db, owner := newDB(t)
	ctx := context.Background()
	cal, _ := db.CreateCalendar(ctx, &storage.Calendar{UserID: owner.ID, DisplayName: "Home"})
	obj, err := db.PutObject(ctx, cal.ID, "meet.ics", []byte(storedInvite))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertAttendeeState(ctx, &storage.AttendeeState{
		ObjectID: obj.ID, Email: "guest@elsewhere.com", Partstat: "NEEDS-ACTION",
	}); err != nil {
		t.Fatal(err)
	}

	raw := Build(Envelope{
		From:      "guest@elsewhere.com",
		To:        "replies@example.com",
		Subject:   "Re: Planning",
		MessageID: "<reply-1@elsewhere.com>",
		Method:    "REPLY",
		ICal:      []byte(replyICal),
		Date:      time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC),
	})

	outcome, uid, err := ApplyReply(ctx, db, raw)
	if err != nil {
		t.Fatalf("ApplyReply: %v", err)
	}
	if outcome != OutcomeApplied {
		t.Fatalf("outcome = %q, want applied", outcome)
	}
	if uid != "meet-1" {
		t.Errorf("uid = %q", uid)
	}

	states, _ := db.AttendeesForObject(ctx, obj.ID)
	if len(states) != 1 || states[0].Partstat != "ACCEPTED" {
		t.Fatalf("partstat not updated: %+v", states)
	}

	// The organizer should have a REPLY in their Inbox.
	inbox, _ := db.InboxForUser(ctx, owner.ID)
	if len(inbox) != 1 || inbox[0].Method != "REPLY" {
		t.Errorf("organizer inbox = %+v, want one REPLY", inbox)
	}
}

func TestApplyReplyNoMatch(t *testing.T) {
	db, _ := newDB(t)
	ctx := context.Background()
	raw := Build(Envelope{
		From: "guest@elsewhere.com", To: "replies@example.com", Subject: "Re: x",
		MessageID: "<r2@e.com>", Method: "REPLY", ICal: []byte(replyICal),
		Date: time.Now().UTC(),
	})
	outcome, _, err := ApplyReply(ctx, db, raw)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != OutcomeNoMatch {
		t.Errorf("outcome = %q, want no_match (no such event)", outcome)
	}
}
