package views

import (
	"bytes"
	"strings"
	"testing"

	goical "github.com/emersion/go-ical"
)

func parse(t *testing.T, body string) *goical.Calendar {
	t.Helper()
	cal, err := goical.NewDecoder(strings.NewReader(body)).Decode()
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

const richEvent = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
BEGIN:VEVENT
UID:e1
DTSTAMP:20260101T000000Z
DTSTART:20260105T090000Z
DTEND:20260105T100000Z
SUMMARY:Therapy appointment
DESCRIPTION:Weekly session
LOCATION:123 Main St
ORGANIZER:mailto:me@example.com
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER:-PT15M
END:VALARM
END:VEVENT
END:VCALENDAR
`

func TestBusyPresetReplacesSummaryAndStripsDetails(t *testing.T) {
	out, err := Apply(parse(t, richEvent), Spec{Preset: PresetBusy, BusyLabel: "Busy", IncludeTentative: true})
	if err != nil {
		t.Fatal(err)
	}
	s := emit(t, out)
	if !strings.Contains(s, "SUMMARY:Busy") {
		t.Errorf("summary not replaced:\n%s", s)
	}
	if strings.Contains(s, "Therapy") {
		t.Error("original summary leaked")
	}
	if strings.Contains(s, "DESCRIPTION") || strings.Contains(s, "LOCATION") || strings.Contains(s, "ORGANIZER") {
		t.Errorf("details not stripped:\n%s", s)
	}
	if strings.Contains(s, "VALARM") {
		t.Error("VALARM not stripped")
	}
	// Structural fields survive.
	if !strings.Contains(s, "DTSTART:20260105T090000Z") || !strings.Contains(s, "UID:e1") {
		t.Error("structural fields missing")
	}
}

func TestTitlesPresetKeepsSummaryDropsDetails(t *testing.T) {
	out, _ := Apply(parse(t, richEvent), Spec{Preset: PresetTitles, IncludeTentative: true})
	s := emit(t, out)
	if !strings.Contains(s, "SUMMARY:Therapy appointment") {
		t.Errorf("summary should be kept:\n%s", s)
	}
	if strings.Contains(s, "LOCATION") || strings.Contains(s, "DESCRIPTION") {
		t.Error("details should be stripped in titles preset")
	}
}

func TestFullPresetKeepsEverythingExceptAlarms(t *testing.T) {
	out, _ := Apply(parse(t, richEvent), Spec{Preset: PresetFull, IncludeTentative: true})
	s := emit(t, out)
	if !strings.Contains(s, "DESCRIPTION:Weekly session") || !strings.Contains(s, "LOCATION:123 Main St") {
		t.Errorf("full preset should keep details:\n%s", s)
	}
	if strings.Contains(s, "VALARM") {
		t.Error("VALARM should be stripped even in full preset")
	}
}

func TestPrivateEventDroppedUnlessIncluded(t *testing.T) {
	body := strings.Replace(richEvent, "SUMMARY:Therapy appointment", "SUMMARY:Secret\nCLASS:PRIVATE", 1)

	out, _ := Apply(parse(t, body), Spec{Preset: PresetFull, IncludeTentative: true})
	if len(out.Children) != 0 {
		t.Error("private event should be dropped by default")
	}

	out2, _ := Apply(parse(t, body), Spec{Preset: PresetFull, IncludePrivate: true, IncludeTentative: true})
	if len(out2.Children) != 1 {
		t.Error("private event should be kept when IncludePrivate is set")
	}
}

func TestTransparentDroppedInBusy(t *testing.T) {
	body := strings.Replace(richEvent, "DTEND:20260105T100000Z", "DTEND:20260105T100000Z\nTRANSP:TRANSPARENT", 1)
	out, _ := Apply(parse(t, body), Spec{Preset: PresetBusy, IncludeTentative: true})
	if len(out.Children) != 0 {
		t.Error("transparent event should be dropped when IncludeTransparent is false")
	}
}

func TestFieldOverrideBeatsPreset(t *testing.T) {
	// Busy preset strips LOCATION, but an override keeps it.
	out, _ := Apply(parse(t, richEvent), Spec{
		Preset:           PresetBusy,
		IncludeTentative: true,
		FieldOverrides:   map[string]Rule{"LOCATION": Keep},
	})
	s := emit(t, out)
	if !strings.Contains(s, "LOCATION:123 Main St") {
		t.Errorf("override should keep LOCATION:\n%s", s)
	}
}

func TestRecurrencePreserved(t *testing.T) {
	body := strings.Replace(richEvent, "DTEND:20260105T100000Z", "DTEND:20260105T100000Z\nRRULE:FREQ=WEEKLY;COUNT=10", 1)
	out, _ := Apply(parse(t, body), Spec{Preset: PresetBusy, IncludeTentative: true})
	s := emit(t, out)
	if !strings.Contains(s, "RRULE:FREQ=WEEKLY;COUNT=10") {
		t.Errorf("RRULE should be preserved:\n%s", s)
	}
}

const recurringWithPrivateOverride = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//t//EN
BEGIN:VEVENT
UID:series-1
DTSTAMP:20260101T000000Z
DTSTART;TZID=America/New_York:20260105T090000
DTEND;TZID=America/New_York:20260105T093000
RRULE:FREQ=WEEKLY;COUNT=5
SUMMARY:Standup
END:VEVENT
BEGIN:VEVENT
UID:series-1
DTSTAMP:20260101T000000Z
RECURRENCE-ID;TZID=America/New_York:20260112T090000
DTSTART;TZID=America/New_York:20260112T090000
DTEND;TZID=America/New_York:20260112T093000
CLASS:PRIVATE
SUMMARY:Private one-off
END:VEVENT
END:VCALENDAR
`

func TestPrivateOverrideBecomesExdate(t *testing.T) {
	out, err := Apply(parse(t, recurringWithPrivateOverride), Spec{Preset: PresetFull, IncludeTentative: true})
	if err != nil {
		t.Fatal(err)
	}
	s := emit(t, out)
	// The master with RRULE survives.
	if !strings.Contains(s, "RRULE:FREQ=WEEKLY;COUNT=5") {
		t.Errorf("master RRULE missing:\n%s", s)
	}
	// The private instance is excluded via EXDATE with the master's TZID.
	if !strings.Contains(s, "EXDATE;TZID=America/New_York:20260112T090000") {
		t.Errorf("EXDATE for private instance missing:\n%s", s)
	}
	// The private override itself must not appear.
	if strings.Contains(s, "Private one-off") {
		t.Error("private override leaked into output")
	}
}

func TestExcludedMasterPromotesPublicOverride(t *testing.T) {
	// Master is private (excluded by default), the override is public.
	body := strings.Replace(recurringWithPrivateOverride, "SUMMARY:Standup", "SUMMARY:Standup\nCLASS:PRIVATE", 1)
	body = strings.Replace(body, "CLASS:PRIVATE\nSUMMARY:Private one-off", "SUMMARY:Public exception", 1)

	out, err := Apply(parse(t, body), Spec{Preset: PresetFull, IncludeTentative: true})
	if err != nil {
		t.Fatal(err)
	}
	s := emit(t, out)
	if strings.Contains(s, "FREQ=WEEKLY") {
		t.Errorf("excluded master's weekly RRULE should not appear:\n%s", s)
	}
	if !strings.Contains(s, "Public exception") {
		t.Errorf("public override should be promoted:\n%s", s)
	}
	if !strings.Contains(s, "UID:series-1-") {
		t.Errorf("promoted override should get a suffixed UID:\n%s", s)
	}
	if strings.Contains(s, "RECURRENCE-ID") {
		t.Error("promoted standalone event should drop RECURRENCE-ID")
	}
}
