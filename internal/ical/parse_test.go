package ical

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func wrap(body string) []byte {
	return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n" + body + "END:VCALENDAR\r\n")
}

const simpleEvent = "BEGIN:VEVENT\r\nUID:ev-1\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260105T090000Z\r\nDTEND:20260105T100000Z\r\nSUMMARY:Standup\r\nEND:VEVENT\r\n"

func TestParseSimpleEvent(t *testing.T) {
	obj, err := Parse(wrap(simpleEvent))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if obj.UID != "ev-1" {
		t.Errorf("UID = %q", obj.UID)
	}
	if obj.ComponentType != CompEvent {
		t.Errorf("type = %q", obj.ComponentType)
	}
	if obj.HasRRULE {
		t.Error("HasRRULE should be false")
	}
	if obj.First == nil || !obj.First.Equal(time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("First = %v", obj.First)
	}
	if obj.Last == nil || !obj.Last.Equal(time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("Last = %v", obj.Last)
	}
}

func TestParseBoundedRRULEHasLast(t *testing.T) {
	body := "BEGIN:VEVENT\r\nUID:ev-2\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260105T090000Z\r\nDTEND:20260105T093000Z\r\nRRULE:FREQ=WEEKLY;COUNT=3\r\nSUMMARY:Weekly\r\nEND:VEVENT\r\n"
	obj, err := Parse(wrap(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !obj.HasRRULE {
		t.Error("HasRRULE should be true")
	}
	if obj.Last == nil {
		t.Fatal("bounded series should have a Last")
	}
	wantLast := time.Date(2026, 1, 19, 9, 30, 0, 0, time.UTC) // third occurrence end
	if !obj.Last.Equal(wantLast) {
		t.Errorf("Last = %v, want %v", obj.Last, wantLast)
	}
}

func TestParseUnboundedRRULENilLast(t *testing.T) {
	body := "BEGIN:VEVENT\r\nUID:ev-3\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260105T090000Z\r\nRRULE:FREQ=DAILY\r\nSUMMARY:Forever\r\nEND:VEVENT\r\n"
	obj, err := Parse(wrap(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if obj.Last != nil {
		t.Errorf("unbounded series Last = %v, want nil", obj.Last)
	}
}

func TestParseSchedulingFlag(t *testing.T) {
	body := "BEGIN:VEVENT\r\nUID:ev-4\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260105T090000Z\r\nORGANIZER:mailto:a@example.com\r\nATTENDEE:mailto:b@example.com\r\nSUMMARY:Meeting\r\nEND:VEVENT\r\n"
	obj, err := Parse(wrap(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !obj.HasScheduling {
		t.Error("HasScheduling should be true")
	}
}

func TestParseRejectsMultipleUIDs(t *testing.T) {
	body := simpleEvent + "BEGIN:VEVENT\r\nUID:other\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260106T090000Z\r\nSUMMARY:Other\r\nEND:VEVENT\r\n"
	_, err := Parse(wrap(body))
	if !errors.Is(err, ErrMultipleUIDs) {
		t.Fatalf("err = %v, want ErrMultipleUIDs", err)
	}
}

func TestParseVTODO(t *testing.T) {
	body := "BEGIN:VTODO\r\nUID:todo-1\r\nDTSTAMP:20260101T000000Z\r\nDUE:20260110T170000Z\r\nSUMMARY:Pay rent\r\nEND:VTODO\r\n"
	obj, err := Parse(wrap(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if obj.ComponentType != CompTodo {
		t.Errorf("type = %q, want VTODO", obj.ComponentType)
	}
}

func TestParseRejectsEmpty(t *testing.T) {
	_, err := Parse([]byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nEND:VCALENDAR\r\n"))
	if !errors.Is(err, ErrNoComponent) {
		t.Fatalf("err = %v, want ErrNoComponent", err)
	}
}

func TestParseTZIDLocalTime(t *testing.T) {
	body := "BEGIN:VEVENT\r\nUID:ev-tz\r\nDTSTAMP:20260101T000000Z\r\nDTSTART;TZID=America/New_York:20260105T090000\r\nDTEND;TZID=America/New_York:20260105T100000\r\nSUMMARY:NY morning\r\nEND:VEVENT\r\n"
	obj, err := Parse(wrap(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// 9am EST is 14:00 UTC in January.
	want := time.Date(2026, 1, 5, 14, 0, 0, 0, time.UTC)
	if obj.First == nil || !obj.First.Equal(want) {
		t.Errorf("First = %v, want %v", obj.First, want)
	}
}

func TestOccurrencesExpands(t *testing.T) {
	body := "BEGIN:VEVENT\r\nUID:ev-occ\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260105T090000Z\r\nRRULE:FREQ=DAILY;COUNT=4\r\nSUMMARY:Daily\r\nEND:VEVENT\r\n"
	obj, err := Parse(wrap(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	w := Range{Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}
	occ, err := obj.Occurrences(w)
	if err != nil {
		t.Fatalf("Occurrences: %v", err)
	}
	if len(occ) != 4 {
		t.Fatalf("got %d occurrences, want 4", len(occ))
	}
}

func TestParsePreservesUnknownProps(t *testing.T) {
	body := "BEGIN:VEVENT\r\nUID:ev-x\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260105T090000Z\r\nX-CUSTOM-THING:keepme\r\nSUMMARY:X\r\nEND:VEVENT\r\n"
	obj, err := Parse(wrap(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := textProp(timedComponents(obj.Cal)[0], "X-CUSTOM-THING"); !strings.Contains(got, "keepme") {
		t.Errorf("X-CUSTOM-THING not preserved: %q", got)
	}
}
