package imip

import (
	"context"
	"io"
	"log/slog"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/furryfoxcorp/calshare/internal/storage"
)

const sampleICal = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:m1\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260110T150000Z\r\nSUMMARY:Planning\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func TestBuildMIME(t *testing.T) {
	msg := Build(Envelope{
		From:      "owner@example.com",
		To:        "guest@elsewhere.com",
		ReplyTo:   "replies@example.com",
		Subject:   "Invitation: Planning",
		MessageID: "<abc@calshare>",
		Method:    "REQUEST",
		ICal:      []byte(sampleICal),
		Date:      time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
	})
	s := string(msg)
	for _, want := range []string{
		"From: owner@example.com",
		"To: guest@elsewhere.com",
		"Reply-To: replies@example.com",
		"Subject: Invitation: Planning",
		"Message-ID: <abc@calshare>",
		"multipart/mixed",
		"text/calendar; method=REQUEST",
		"application/ics",
		"filename=\"invite.ics\"",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("message missing %q", want)
		}
	}
}

func TestSubject(t *testing.T) {
	if got := Subject("REQUEST", "Lunch"); got != "Invitation: Lunch" {
		t.Errorf("REQUEST subject = %q", got)
	}
	if got := Subject("CANCEL", "Lunch"); got != "Cancelled: Lunch" {
		t.Errorf("CANCEL subject = %q", got)
	}
	if got := Subject("REPLY", "Lunch"); got != "Re: Lunch" {
		t.Errorf("REPLY subject = %q", got)
	}
}

func newDB(t *testing.T) (*storage.DB, *storage.User) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "imip.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	u, _ := db.UpsertUserOnLogin(context.Background(), "o", "owner@example.com", "Owner")
	return db, u
}

func TestDrainSendsAndMarksSent(t *testing.T) {
	db, u := newDB(t)
	ctx := context.Background()
	db.EnqueueIMIP(ctx, &storage.IMIPMessage{
		FromUserID: u.ID, ToAddress: "guest@elsewhere.com", Method: "REQUEST",
		MessageID: "<m1@calshare>", UID: "m1", Body: []byte(sampleICal),
	})

	var capturedTo, capturedFrom string
	s := NewSender(db, SMTPConfig{Host: "smtp.example", Port: 587, ReplyAddress: "replies@example.com"}, discardLogger())
	s.send = func(cfg SMTPConfig, from, to string, msg []byte) error {
		capturedFrom, capturedTo = from, to
		return nil
	}

	n, err := s.Drain(ctx)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if n != 1 {
		t.Fatalf("sent %d, want 1", n)
	}
	if capturedTo != "guest@elsewhere.com" || capturedFrom != "owner@example.com" {
		t.Errorf("addresses wrong: from=%q to=%q", capturedFrom, capturedTo)
	}
	// Nothing left due.
	due, _ := db.DueIMIP(ctx, time.Now().UTC(), 10)
	if len(due) != 0 {
		t.Errorf("queue not drained, %d remain", len(due))
	}
}

func TestDrainRetriesThenGivesUp(t *testing.T) {
	db, u := newDB(t)
	ctx := context.Background()
	db.EnqueueIMIP(ctx, &storage.IMIPMessage{
		FromUserID: u.ID, ToAddress: "guest@elsewhere.com", Method: "REQUEST",
		MessageID: "<m2@calshare>", UID: "m2", Body: []byte(sampleICal),
	})

	now := time.Now().UTC()
	s := NewSender(db, SMTPConfig{Host: "smtp.example", Port: 587}, discardLogger())
	s.send = func(SMTPConfig, string, string, []byte) error { return errors.New("relay down") }
	s.now = func() time.Time { return now }

	// Each drain advances "now" past the backoff so the retry is due again.
	for i := 0; i < maxAttempts; i++ {
		s.now = func() time.Time { return now.Add(time.Duration(i) * time.Hour) }
		if _, err := s.Drain(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// After maxAttempts it should be failed_final and no longer due.
	due, _ := db.DueIMIP(ctx, now.Add(1000*time.Hour), 10)
	if len(due) != 0 {
		t.Errorf("message should be failed_final, but %d still due", len(due))
	}
}

func TestRunNoopWhenUnconfigured(t *testing.T) {
	db, _ := newDB(t)
	s := NewSender(db, SMTPConfig{}, discardLogger())
	// Run should return immediately when SMTP is not configured.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Run(ctx) // must not block
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
