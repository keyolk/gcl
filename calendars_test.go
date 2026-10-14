package main

import (
	"errors"
	"strings"
	"testing"
)

// e opens the calendar picker at once from what is in memory; the calendar
// list (an HTTP call on first use) is read by the returned Cmd, never inside
// Update. The Cmd is not run here: it would reach Google.
func TestCalendarPickerOpensWithoutReadingTheCalendarList(t *testing.T) {
	m := loadedModel()
	m.recents = []string{"a@x.com", "b@x.com"}
	next, cmd := m.Update(keyMsg("e"))
	got := next.(model)
	if got.mode != modeCalendarPicker {
		t.Fatalf("e left mode %v, want the calendar picker", got.mode)
	}
	if cmd == nil {
		t.Fatal("e scheduled no Cmd to read the calendar list")
	}
	if n := len(got.picker.items); n < 2 || got.picker.items[0].value != "a@x.com" {
		t.Errorf("picker opened with %+v, want the recents first", got.picker.items)
	}
}

// The full list replaces the quick one without moving the highlight off the
// calendar it was on, and is dropped once the picker has closed.
func TestCalendarItemsKeepTheHighlight(t *testing.T) {
	m := loadedModel()
	m.recents = []string{"a@x.com", "b@x.com"}
	next, _ := m.Update(keyMsg("e"))
	m = next.(model)
	m.picker.index = 1 // b@x.com
	full := calendarPickerItems(m.recents,
		[]CalendarEntry{{ID: "team@group.calendar.google.com", Name: "Team"}},
		func(s string) string { return strings.ToUpper(s) })
	next, _ = m.Update(calendarItemsMsg{items: full})
	m = next.(model)
	if len(m.picker.items) != len(full) {
		t.Fatalf("picker has %d items, want %d", len(m.picker.items), len(full))
	}
	if it := m.picker.filtered(m.input)[m.picker.index]; it.value != "b@x.com" {
		t.Errorf("highlight moved to %q, want b@x.com", it.value)
	}

	m.mode = modeNormal
	next, _ = m.Update(calendarItemsMsg{items: nil, err: errors.New("offline")})
	m = next.(model)
	if len(m.picker.items) != len(full) {
		t.Error("a late calendar list changed a closed picker")
	}
	if !strings.Contains(m.statusText(), "offline") {
		t.Errorf("calendar list error not shown: %q", m.statusText())
	}
}
