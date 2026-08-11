package main

import (
	"strings"
	"testing"
	"time"
)

// The panel is the whole feature: if it renders empty or drops the countdown,
// the maintenance-window use case is unserved even though the logic is right.
func TestViewActivePanelShowsSpansAndCountdowns(t *testing.T) {
	now := time.Now()
	m := model{
		width:      120,
		height:     40,
		activeOpen: true,
		events: []Event{
			{ID: "w", Title: "apne2 maintenance window",
				StartDate: today().AddDate(0, 0, -2), EndDate: today().AddDate(0, 0, 2)},
			{ID: "s", Title: "release sync", StartAt: now.Add(-20 * time.Minute), EndAt: now.Add(40 * time.Minute),
				StartDate: today(), StartTime: "00:00", Location: "Zoom"},
		},
	}
	out := stripANSI(m.viewActivePanel(118, m.activePanelHeight(37)))
	for _, want := range []string{"Active now", "apne2 maintenance window", "release sync", "ends in", "all-day"} {
		if !strings.Contains(out, want) {
			t.Errorf("active panel missing %q\n---\n%s", want, out)
		}
	}
	// Unfocused, it must say how to get into it.
	if !strings.Contains(out, "tab to focus") {
		t.Errorf("unfocused panel does not advertise tab:\n%s", out)
	}
	// Focused, it shows its own navigation keys instead.
	m.focusPane = focusActive
	focused := stripANSI(m.viewActivePanel(118, m.activePanelHeight(37)))
	if !strings.Contains(focused, "j/k move") {
		t.Errorf("focused panel does not show its nav keys:\n%s", focused)
	}
}

func TestViewActivePanelEmptyStateExplainsItself(t *testing.T) {
	m := model{width: 120, height: 40, activeOpen: true}
	out := stripANSI(m.viewActivePanel(118, m.activePanelHeight(37)))
	if !strings.Contains(out, "nothing in effect right now") {
		t.Errorf("expected an explicit empty state, got:\n%s", out)
	}
}

// The docked panel must sit above the schedule without covering it — an overlay
// would defeat the "glance while staying where you are" requirement.
func TestDockedPanelDoesNotCoverTheSchedule(t *testing.T) {
	now := time.Now()
	m := model{
		width: 120, height: 30, view: viewList, anchor: today(), jumpUnit: "day",
		calendar: "me",
		events: []Event{
			{ID: "live", Title: "running window", StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
				StartDate: today(), StartTime: "02:00"},
			{ID: "later", Title: "Standup tomorrow", StartAt: now.Add(26 * time.Hour), EndAt: now.Add(27 * time.Hour),
				StartDate: today().AddDate(0, 0, 1), StartTime: "10:00"},
		},
	}
	closed := stripANSI(m.View())
	m.activeOpen = true
	open := stripANSI(m.View())

	if !strings.Contains(open, "Active now") {
		t.Fatalf("panel did not render:\n%s", open)
	}
	// Both agenda entries survive the panel taking rows.
	for _, want := range []string{"running window", "Standup tomorrow"} {
		if !strings.Contains(open, want) {
			t.Errorf("opening the panel hid %q from the schedule:\n%s", want, open)
		}
	}
	// The frame stays exactly m.height rows in both states.
	for label, frame := range map[string]string{"closed": closed, "open": open} {
		if got := strings.Count(frame, "\n") + 1; got != m.height {
			t.Errorf("%s frame is %d rows, want %d", label, got, m.height)
		}
	}
}

// The staged time must be what the row shows, with a marker — otherwise an
// unsaved nudge is indistinguishable from a saved one.
func TestEventRowShowsStagedTimeWithMarker(t *testing.T) {
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	m := model{
		calendar: "me",
		width:    100,
		events: []Event{{
			ID: "evt1", Title: "Standup", StartAt: start, EndAt: start.Add(30 * time.Minute),
			StartDate: start, StartTime: "10:00",
		}},
	}
	saved := stripANSI(m.eventRow(&m.events[0], false, 80))
	if !strings.Contains(saved, "10:00-10:30") {
		t.Fatalf("saved row = %q, want the saved time", saved)
	}
	if strings.Contains(saved, "!") {
		t.Errorf("saved row must carry no unsaved marker: %q", saved)
	}

	mm, _, _ := m.handleQuickAction(")")
	m = mm.(model)
	staged := stripANSI(m.eventRow(&m.events[0], false, 80))
	if !strings.Contains(staged, "10:15-10:45") {
		t.Errorf("staged row = %q, want the staged time 10:15-10:45", staged)
	}
	if !strings.Contains(staged, "!+15m") {
		t.Errorf("staged row = %q, want an unsaved marker", staged)
	}
}

// The hint bar is the only always-visible surface, so an unsaved change has to
// own it and name both resolving keys.
func TestHintBarBecomesUnsavedBanner(t *testing.T) {
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	m := model{
		calendar: "me",
		view:     viewList,
		events: []Event{{
			ID: "evt1", Title: "Standup", StartAt: start, EndAt: start.Add(30 * time.Minute),
			StartDate: start, StartTime: "10:00",
		}},
	}
	normal := m.shortcutHint()
	for _, want := range []string{"a active", "e calendar", "s saves"} {
		if !strings.Contains(normal, want) {
			t.Errorf("normal hint missing %q: %q", want, normal)
		}
	}

	mm, _, _ := m.handleQuickAction(")")
	staged := mm.(model).shortcutHint()
	for _, want := range []string{"UNSAVED", "+15m", "s SAVE", "esc discard"} {
		if !strings.Contains(staged, want) {
			t.Errorf("unsaved hint missing %q: %q", want, staged)
		}
	}
}

// The frame must stay exactly m.height rows and within m.width columns at every
// size, in every view, with the panel open or closed. A wrapped header or an
// over-tall panel silently steals a body row, which is how the docked panel
// could break layouts the overlay never touched.
func TestFrameFitsAtEverySize(t *testing.T) {
	now := time.Now()
	mk := func(w, h int, view viewMode, open bool) model {
		return model{
			width: w, height: h, view: view, anchor: today(), jumpUnit: "day",
			calendar: "me", status: "loaded", activeOpen: open, gridTop: weekStart(today()),
			events: []Event{
				{ID: "w", Title: "apne2 maintenance window",
					StartDate: today().AddDate(0, 0, -2), EndDate: today().AddDate(0, 0, 2)},
				{ID: "r", Title: "istiod upgrade", StartAt: now.Add(-20 * time.Minute), EndAt: now.Add(40 * time.Minute),
					StartDate: today(), StartTime: "10:50", EndTime: "11:50", Location: "ops-apne2"},
			},
		}
	}
	for _, w := range []int{60, 70, 80, 100, 110, 160} {
		for _, h := range []int{12, 20, 24, 40} {
			for _, view := range []viewMode{viewList, viewWeek, viewMonth} {
				for _, open := range []bool{false, true} {
					m := mk(w, h, view, open)
					out := m.View()
					if rows := strings.Count(out, "\n") + 1; rows != h {
						t.Errorf("%dx%d view=%d panel=%v: %d rows, want %d", w, h, view, open, rows, h)
					}
					for i, l := range strings.Split(stripANSI(out), "\n") {
						if n := len([]rune(l)); n > w {
							t.Errorf("%dx%d view=%d panel=%v: line %d is %d cols", w, h, view, open, i, n)
						}
					}
				}
			}
		}
	}
}

// `e` and `a` must be discoverable from the header, which is on screen at all
// times — the original complaint was that `e` existed only in the help overlay.
func TestHeaderAdvertisesCalendarSwitchAndActiveCount(t *testing.T) {
	now := time.Now()
	m := model{
		width:  140,
		height: 40,
		view:   viewList,
		anchor: today(),
		events: []Event{{
			ID: "live", Title: "running", StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
			StartDate: today(), StartTime: "00:00",
		}},
	}
	out := stripANSI(m.View())
	header := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(header, "e:switch") {
		t.Errorf("header does not advertise the calendar switcher: %q", header)
	}
	if !strings.Contains(header, "active") {
		t.Errorf("header does not show the active count: %q", header)
	}
}

func TestEmptyScheduleNamesTheCalendarSwitcher(t *testing.T) {
	m := model{width: 100, height: 30, view: viewList, anchor: today()}
	out := stripANSI(m.viewScheduleCards(80, 20))
	if !strings.Contains(out, "switch calendar") {
		t.Errorf("empty state does not name the calendar switcher:\n%s", out)
	}
}
