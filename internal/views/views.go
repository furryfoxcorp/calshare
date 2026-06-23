// Package views resolves a privacy "view" over a set of calendars: it filters
// events by include flags and rewrites or strips fields per the view's preset
// and per-field overrides, producing an iCalendar body suitable for a public
// share link. Tasks (VTODO) are never included.
package views

// Rule is what to do with a single iCalendar property.
type Rule string

const (
	Keep    Rule = "keep"
	Strip   Rule = "strip"
	Replace Rule = "replace" // SUMMARY only: replace with the busy label
)

// Preset names the starting point for a view's field rules.
type Preset string

const (
	PresetFull   Preset = "full"
	PresetTitles Preset = "titles"
	PresetBusy   Preset = "busy"
)

// managedFields are the properties a view controls. Properties outside this
// list (UID, DTSTART, DTEND, RRULE, EXDATE, RECURRENCE-ID, and similar
// structural fields) are always preserved.
var managedFields = []string{
	"SUMMARY", "DESCRIPTION", "LOCATION", "URL", "ATTENDEE", "ORGANIZER",
	"CATEGORIES", "GEO", "ATTACH", "COMMENT", "RESOURCES", "PRIORITY", "CLASS",
	"STATUS", "TRANSP", "VALARM", "CREATED", "LAST-MODIFIED", "DTSTAMP", "SEQUENCE",
}

// PresetRules returns the default field rules for a preset, per the design's
// field configuration table. VALARM is always stripped by default.
func PresetRules(p Preset) map[string]Rule {
	full := map[string]Rule{
		"SUMMARY": Keep, "DESCRIPTION": Keep, "LOCATION": Keep, "URL": Keep,
		"ATTENDEE": Keep, "ORGANIZER": Keep, "CATEGORIES": Keep, "GEO": Keep,
		"ATTACH": Keep, "COMMENT": Keep, "RESOURCES": Keep, "PRIORITY": Keep,
		"CLASS": Keep, "STATUS": Keep, "TRANSP": Keep, "VALARM": Strip,
		"CREATED": Keep, "LAST-MODIFIED": Keep, "DTSTAMP": Keep, "SEQUENCE": Keep,
	}
	switch p {
	case PresetTitles:
		r := clone(full)
		for _, f := range []string{"DESCRIPTION", "LOCATION", "URL", "ATTENDEE",
			"ORGANIZER", "CATEGORIES", "GEO", "ATTACH", "COMMENT", "RESOURCES",
			"PRIORITY", "CLASS", "CREATED"} {
			r[f] = Strip
		}
		return r
	case PresetBusy:
		r := clone(full)
		r["SUMMARY"] = Replace
		for _, f := range []string{"DESCRIPTION", "LOCATION", "URL", "ATTENDEE",
			"ORGANIZER", "CATEGORIES", "GEO", "ATTACH", "COMMENT", "RESOURCES",
			"PRIORITY", "CLASS", "CREATED"} {
			r[f] = Strip
		}
		return r
	default:
		return full
	}
}

func clone(m map[string]Rule) map[string]Rule {
	out := make(map[string]Rule, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Spec is a fully resolved view: a preset plus overrides and include flags.
type Spec struct {
	Preset             Preset
	BusyLabel          string
	IncludePrivate     bool
	IncludeCancelled   bool
	IncludeTentative   bool
	IncludeTransparent bool
	// FieldOverrides are deltas applied on top of the preset's rules.
	FieldOverrides map[string]Rule
}

// effectiveRules merges the preset defaults with any overrides.
func (s Spec) effectiveRules() map[string]Rule {
	r := PresetRules(s.Preset)
	for k, v := range s.FieldOverrides {
		r[k] = v
	}
	return r
}

func (s Spec) busyLabel() string {
	if s.BusyLabel != "" {
		return s.BusyLabel
	}
	return "Busy"
}
