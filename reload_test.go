package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// loadedModel is a list view that already has one fetch landed.
func loadedModel() model {
	return model{
		width: 120, height: 30, view: viewList, anchor: today(), jumpUnit: "day",
		calendar:    "me",
		loadedStart: today().AddDate(0, 0, -2),
		loadedEnd:   today().AddDate(0, 0, 45),
		events: []Event{{
			ID: "e1", Title: "Quarterly planning", StartDate: today(), StartTime: "10:00",
			StartAt: today().Add(10 * time.Hour), EndAt: today().Add(11 * time.Hour),
		}},
	}
}

// A failed reload used to set m.events = nil, blanking the agenda on any
// transient error after a nudge or save. The last good events must stay.
func TestFailedReloadKeepsLastGoodEvents(t *testing.T) {
	m := loadedModel()
	cmd := m.reload()
	_ = cmd // never executed: it would hit the Google API
	got, _ := m.Update(eventsMsg{reqID: m.inflightReq, err: errors.New("503 backend error")})
	gm := got.(model)
	if len(gm.events) != 1 {
		t.Fatalf("events = %d after failed reload, want the last good 1", len(gm.events))
	}
	if !strings.Contains(gm.status, "503 backend error") {
		t.Errorf("status = %q, want the load error", gm.status)
	}
	out := stripANSI(gm.View())
	if !strings.Contains(out, "Quarterly planning") {
		t.Errorf("agenda replaced after failed reload:\n%s", out)
	}
}

// While reloading over an existing agenda the body stays and the header carries
// the indicator, instead of the body flashing to a placeholder.
func TestReloadKeepsBodyAndShowsHeaderIndicator(t *testing.T) {
	m := loadedModel()
	_ = m.reload()
	out := stripANSI(m.View())
	if strings.Contains(out, "loading calendar events") {
		t.Errorf("body replaced by placeholder during reload:\n%s", out)
	}
	if !strings.Contains(out, "Quarterly planning") {
		t.Errorf("agenda hidden during reload:\n%s", out)
	}
	header := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(header, "loading~") {
		t.Errorf("header lacks reload indicator: %q", header)
	}
}

// With nothing loaded yet there is no agenda to keep: the placeholder is right.
func TestFirstLoadShowsPlaceholder(t *testing.T) {
	m := model{width: 120, height: 30, view: viewList, anchor: today(), loading: true}
	out := stripANSI(m.View())
	if !strings.Contains(out, "loading calendar events") {
		t.Errorf("first load lacks placeholder:\n%s", out)
	}
}

// Switching calendars must not show the old calendar's events under the new
// calendar's name while (or after failing) loading.
func TestCalendarSwitchDropsPreviousCalendarsEvents(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	m := loadedModel()
	m.mode = modeCalendarPicker
	m.picker = pickerState{kind: pickerCalendar}
	got, _ := m.choosePicker(pickerItem{label: "team", value: "team@example.com"})
	gm := got.(model)
	if len(gm.events) != 0 || gm.hasLoaded() {
		t.Fatalf("old calendar's events kept across switch: %d events", len(gm.events))
	}
	failed, _ := gm.Update(eventsMsg{reqID: gm.inflightReq, err: errors.New("403 forbidden")})
	out := stripANSI(failed.(model).View())
	if strings.Contains(out, "Quarterly planning") {
		t.Errorf("old calendar's event shown after failed switch:\n%s", out)
	}
	if !strings.Contains(out, "403 forbidden") {
		t.Errorf("failed first load of new calendar shows no error:\n%s", out)
	}
}
