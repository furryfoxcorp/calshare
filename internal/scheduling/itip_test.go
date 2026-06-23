package scheduling

import (
	"bytes"
	"strings"
	"testing"

	goical "github.com/emersion/go-ical"
)

const inviteEvent = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
BEGIN:VEVENT
UID:meet-1
DTSTAMP:20260101T000000Z
DTSTART:20260110T150000Z
DTEND:20260110T160000Z
SUMMARY:Planning
SEQUENCE:0
ORGANIZER:mailto:owner@example.com
ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:zoe@example.com
ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:guest@elsewhere.com
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER:-PT10M
END:VALARM
END:VEVENT
END:VCALENDAR
`

func parse(t *testing.T, s string) *goical.Calendar {
	t.Helper()
	cal, err := goical.NewDecoder(strings.NewReader(s)).Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return cal
}

func emit(t *testing.T, cal *goical.Calendar) string {
	t.Helper()
	var buf bytes.Buffer
	if err := goical.NewEncoder(&buf).Encode(cal); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.String()
}

func TestBuildRequest(t *testing.T) {
	out, err := BuildRequest(parse(t, inviteEvent))
	if err != nil {
		t.Fatal(err)
	}
	s := emit(t, out)
	if !strings.Contains(s, "METHOD:REQUEST") {
		t.Error("missing METHOD:REQUEST")
	}
	if strings.Contains(s, "VALARM") {
		t.Error("VALARM should be stripped from iTIP messages")
	}
	if !strings.Contains(s, "zoe@example.com") || !strings.Contains(s, "guest@elsewhere.com") {
		t.Error("request should carry all attendees")
	}
}

func TestBuildCancel(t *testing.T) {
	out, err := BuildCancel(parse(t, inviteEvent))
	if err != nil {
		t.Fatal(err)
	}
	s := emit(t, out)
	if !strings.Contains(s, "METHOD:CANCEL") {
		t.Error("missing METHOD:CANCEL")
	}
	if !strings.Contains(s, "STATUS:CANCELLED") {
		t.Error("cancel should mark events cancelled")
	}
	if !strings.Contains(s, "SEQUENCE:1") {
		t.Error("cancel should bump SEQUENCE")
	}
}

func TestBuildReplyKeepsOnlyReplier(t *testing.T) {
	out, err := BuildReply(parse(t, inviteEvent), "zoe@example.com", "ACCEPTED")
	if err != nil {
		t.Fatal(err)
	}
	s := emit(t, out)
	if !strings.Contains(s, "METHOD:REPLY") {
		t.Error("missing METHOD:REPLY")
	}
	if !strings.Contains(s, "zoe@example.com") {
		t.Error("reply should include the replier")
	}
	if strings.Contains(s, "guest@elsewhere.com") {
		t.Error("reply should not include other attendees")
	}
	if !strings.Contains(s, "PARTSTAT=ACCEPTED") {
		t.Errorf("reply should carry the new PARTSTAT:\n%s", s)
	}
}

func TestOutboundStripsScheduleStatus(t *testing.T) {
	// An event whose attendee carries a server-set SCHEDULE-STATUS must not
	// carry it into the outbound REQUEST.
	body := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:ss-1\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260110T150000Z\r\nSUMMARY:Sync\r\nORGANIZER:mailto:owner@example.com\r\nATTENDEE;PARTSTAT=NEEDS-ACTION;SCHEDULE-STATUS=1.2:mailto:zoe@example.com\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	out, err := BuildRequest(parse(t, body))
	if err != nil {
		t.Fatal(err)
	}
	s := emit(t, out)
	if strings.Contains(s, "SCHEDULE-STATUS") {
		t.Errorf("SCHEDULE-STATUS leaked into outbound REQUEST:\n%s", s)
	}
	if !strings.Contains(s, "zoe@example.com") {
		t.Error("attendee dropped")
	}
}
