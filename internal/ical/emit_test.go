package ical

import (
	"strings"
	"testing"
)

func TestEmitRoundtripHasProdIDAndCRLF(t *testing.T) {
	obj, err := Parse(wrap(simpleEvent))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := Emit(obj.Cal)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "PRODID") {
		t.Error("output missing PRODID")
	}
	if !strings.Contains(s, "\r\n") {
		t.Error("output not CRLF folded")
	}
	if !strings.Contains(s, "UID:ev-1") {
		t.Error("output missing event")
	}
}

func TestETagStableAcrossEmits(t *testing.T) {
	obj, _ := Parse(wrap(simpleEvent))
	a, _ := Emit(obj.Cal)
	obj2, _ := Parse(a)
	b, _ := Emit(obj2.Cal)
	if ETag(a) != ETag(b) {
		t.Errorf("ETag not idempotent: %s vs %s", ETag(a), ETag(b))
	}
}

func TestCanonicalizeMutatedFlag(t *testing.T) {
	// A body missing PRODID should be reported as mutated.
	noProdID := []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" + simpleEvent + "END:VCALENDAR\r\n")
	out, mutated, err := Canonicalize(noProdID)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if !mutated {
		t.Error("expected mutated=true for body missing PRODID")
	}
	if !strings.Contains(string(out), "PRODID") {
		t.Error("canonical form missing PRODID")
	}

	// Re-canonicalizing the output must be a no-op.
	out2, mutated2, err := Canonicalize(out)
	if err != nil {
		t.Fatalf("second Canonicalize: %v", err)
	}
	if mutated2 {
		t.Errorf("Canonicalize not idempotent: second pass reported mutated; out=%q out2=%q", out, out2)
	}
}

func TestBundleTimezonesInjectsVTIMEZONE(t *testing.T) {
	body := "BEGIN:VEVENT\r\nUID:tz-ev\r\nDTSTAMP:20260101T000000Z\r\nDTSTART;TZID=America/New_York:20260705T090000\r\nDTEND;TZID=America/New_York:20260705T100000\r\nSUMMARY:Summer NY\r\nEND:VEVENT\r\n"
	obj, err := Parse(wrap(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := BundleTimezones(obj.Cal); err != nil {
		t.Fatalf("BundleTimezones: %v", err)
	}
	out, err := Emit(obj.Cal)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "BEGIN:VTIMEZONE") {
		t.Fatal("no VTIMEZONE injected")
	}
	if !strings.Contains(s, "TZID:America/New_York") {
		t.Error("VTIMEZONE missing TZID")
	}
	if !strings.Contains(s, "BEGIN:STANDARD") {
		t.Error("VTIMEZONE missing STANDARD")
	}
	if !strings.Contains(s, "BEGIN:DAYLIGHT") {
		t.Error("VTIMEZONE missing DAYLIGHT (America/New_York observes DST)")
	}
	// RRULE must not be TEXT-escaped (no backslash before the semicolons).
	if strings.Contains(s, "FREQ=YEARLY\\;") {
		t.Error("RRULE semicolons were escaped")
	}
	if !strings.Contains(s, "FREQ=YEARLY;BYMONTH=") {
		t.Error("VTIMEZONE missing yearly RRULE")
	}
}

func TestBundleTimezonesIdempotent(t *testing.T) {
	body := "BEGIN:VEVENT\r\nUID:tz-ev2\r\nDTSTAMP:20260101T000000Z\r\nDTSTART;TZID=Europe/London:20260705T090000\r\nSUMMARY:London\r\nEND:VEVENT\r\n"
	obj, _ := Parse(wrap(body))
	if err := BundleTimezones(obj.Cal); err != nil {
		t.Fatal(err)
	}
	before := countVTIMEZONE(obj)
	if err := BundleTimezones(obj.Cal); err != nil {
		t.Fatal(err)
	}
	if after := countVTIMEZONE(obj); after != before {
		t.Errorf("VTIMEZONE count changed on second bundle: %d -> %d", before, after)
	}
}

func countVTIMEZONE(o *Object) int {
	n := 0
	for _, c := range o.Cal.Children {
		if c.Name == "VTIMEZONE" {
			n++
		}
	}
	return n
}

func TestEmitFoldsLongLines(t *testing.T) {
	longDesc := strings.Repeat("A", 600)
	body := "BEGIN:VEVENT\r\nUID:fold-1\r\nDTSTAMP:20260101T000000Z\r\nDTSTART:20260105T090000Z\r\nDESCRIPTION:" + longDesc + "\r\nSUMMARY:Folded\r\nEND:VEVENT\r\n"
	obj, err := Parse(wrap(body))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Emit(obj.Cal)
	if err != nil {
		t.Fatal(err)
	}
	// Every physical line must be <= 75 octets.
	for _, line := range strings.Split(string(out), "\r\n") {
		if len(line) > 75 {
			t.Fatalf("unfolded line of %d octets: %.80q", len(line), line)
		}
	}
	// Continuation lines (starting with a space) must exist for the long prop.
	if !strings.Contains(string(out), "\r\n ") {
		t.Error("no fold continuation produced")
	}
	// Unfolding (remove CRLF+space) must restore the original description.
	unfolded := strings.ReplaceAll(string(out), "\r\n ", "")
	if !strings.Contains(unfolded, "DESCRIPTION:"+longDesc) {
		t.Error("folding was not reversible")
	}
}
