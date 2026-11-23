package a2ui

import (
	"encoding/json"
	"testing"
	"time"
)

// An Agenda's events are a source's rows; the days it draws the dated
// proposals of are read from it as written: its date (today when it has
// none) and its days, 1 to MaxDays; none when it says "proposals": false
// or its date is not a written date.
func TestDaysShown(t *testing.T) {
	if RowsProp("Agenda") != "events" || RowsProp("List") != "" || RowsProp("Table") != "" {
		t.Error("RowsProp")
	}
	now := time.Date(2026, 11, 23, 10, 5, 0, 0, time.UTC)
	day := func(d int) time.Time { return time.Date(2026, 11, 23+d, 0, 0, 0, 0, time.UTC) }
	for _, tc := range []struct {
		comp string
		from time.Time
		days int
		ok   bool
	}{
		{`{"component":"Agenda","events":[]}`, day(0), 1, true},
		{`{"component":"Agenda","days":3,"events":[]}`, day(0), 3, true},
		{`{"component":"Agenda","days":30,"events":[]}`, day(0), MaxDays, true},
		{`{"component":"Agenda","date":"2026-11-25","events":[]}`, day(2), 1, true},
		{`{"component":"Agenda","date":"2026-11-25T08:00:00+01:00","days":2,"events":[]}`, day(2), 2, true},
		{`{"component":"Agenda","proposals":false,"events":[]}`, time.Time{}, 0, false},
		{`{"component":"Agenda","date":{"@path":"/jour"},"events":[]}`, time.Time{}, 0, false},
		{`{"component":"Agenda","date":"demain","events":[]}`, time.Time{}, 0, false},
		{`{"component":"Timeline","items":[]}`, time.Time{}, 0, false},
	} {
		var comp map[string]any
		json.Unmarshal([]byte(tc.comp), &comp)
		from, days, ok := DaysShown(comp, now)
		if ok != tc.ok || days != tc.days || !from.Equal(tc.from) {
			t.Errorf("%s: %v %d %v, want %v %d %v", tc.comp, from, days, ok, tc.from, tc.days, tc.ok)
		}
	}
}
