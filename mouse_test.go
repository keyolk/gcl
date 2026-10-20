package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/mouse"
)

// Mouse tests click on coordinates read back out of View(), not on numbers
// recomputed with the handler's own arithmetic: if the hit test and the
// drawing ever disagree, a click here lands on the wrong text and fails.

var mouseBase = time.Date(2026, 7, 15, 12, 0, 0, 0, time.Local)

// mouseEvent is a timed event on day offset d (from today) at 10:00.
func mouseEvent(id, title string, d int) Event {
	day := today().AddDate(0, 0, d)
	start := time.Date(day.Year(), day.Month(), day.Day(), 10, 0, 0, 0, time.Local)
	return Event{ID: id, Title: title, StartDate: day, StartTime: "10:00", EndTime: "11:00",
		StartAt: start, EndAt: start.Add(time.Hour), Calendar: "me"}
}

func mouseModel(t *testing.T, w, h int, evs ...Event) model {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // hermetic: no local config or ui state
	m := model{
		width: w, height: h, view: viewList, anchor: today().AddDate(0, 0, 1), jumpUnit: "day",
		calendar: "me", events: evs, loadedStart: today().AddDate(0, 0, -30), loadedEnd: today().AddDate(0, 0, 90),
	}
	m.now = func() time.Time { return mouseBase }
	m.selected = 0
	return m
}

func threeEvents(t *testing.T, w, h int) model {
	return mouseModel(t, w, h,
		mouseEvent("a", "Alpha standup", 1),
		mouseEvent("b", "Bravo review", 1),
		mouseEvent("c", "Charlie sync", 2),
	)
}

// at returns the screen cell of the first occurrence of text in View().
func at(t *testing.T, m model, text string) (x, y int) {
	t.Helper()
	cells := occurrences(m, text)
	if len(cells) == 0 {
		t.Fatalf("%q is not on screen:\n%s", text, stripANSI(m.View()))
	}
	return cells[0][0], cells[0][1]
}

// lastAt is the occurrence furthest down, then right: in the bottom split the
// detail pane is below the schedule, so it wins over a same-titled schedule row.
func lastAt(t *testing.T, m model, text string) (x, y int) {
	t.Helper()
	cells := occurrences(m, text)
	if len(cells) == 0 {
		t.Fatalf("%q is not on screen:\n%s", text, stripANSI(m.View()))
	}
	best := cells[0]
	for _, c := range cells {
		if c[1] > best[1] || (c[1] == best[1] && c[0] > best[0]) {
			best = c
		}
	}
	return best[0], best[1]
}

// rightmostAt is the occurrence furthest right: in the right split the detail
// pane is right of the schedule.
func rightmostAt(t *testing.T, m model, text string) (x, y int) {
	t.Helper()
	cells := occurrences(m, text)
	if len(cells) == 0 {
		t.Fatalf("%q is not on screen:\n%s", text, stripANSI(m.View()))
	}
	best := cells[0]
	for _, c := range cells {
		if c[0] > best[0] {
			best = c
		}
	}
	return best[0], best[1]
}

func occurrences(m model, text string) [][2]int {
	var out [][2]int
	for y, line := range strings.Split(stripANSI(m.View()), "\n") {
		from := 0
		for {
			i := strings.Index(line[from:], text)
			if i < 0 {
				break
			}
			out = append(out, [2]int{lipgloss.Width(line[:from+i]), y})
			from += i + len(text)
		}
	}
	return out
}

func press(m model, x, y int) (model, tea.Cmd) {
	return send(m, tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
}

func send(m model, msg tea.MouseMsg) (model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(model), cmd
}

func wheel(m model, x, y int, down bool) (model, tea.Cmd) {
	b := tea.MouseButtonWheelUp
	if down {
		b = tea.MouseButtonWheelDown
	}
	return send(m, tea.MouseMsg{X: x, Y: y, Button: b, Action: tea.MouseActionPress})
}

func toastText(m model) string {
	var parts []string
	for _, e := range m.toasts.Items() {
		parts = append(parts, e.Text)
	}
	return strings.Join(parts, " | ")
}

func TestClickSelectsAgendaEvent(t *testing.T) {
	m := threeEvents(t, 120, 40)
	x, y := at(t, m, "Charlie sync")
	m, cmd := press(m, x, y)
	if m.selected != 2 {
		t.Fatalf("selected = %d, want 2 (Charlie)", m.selected)
	}
	if cmd != nil {
		t.Errorf("a single click must not run anything")
	}
	if got := toastText(m); strings.Contains(got, "Google Calendar link") {
		t.Errorf("a single click must not open: %s", got)
	}
	// The anchor follows the selection, as with j/k.
	if !sameDay(m.anchor, today().AddDate(0, 0, 2)) {
		t.Errorf("anchor = %v, want the selected event's day", m.anchor)
	}
}

func TestSecondClickOnSelectedOpens(t *testing.T) {
	m := threeEvents(t, 120, 40)
	x, y := at(t, m, "Bravo review")
	m, _ = press(m, x, y)
	m, _ = press(m, x, y)
	// No link on the event: open's own warning proves it ran the open action.
	if got := toastText(m); !strings.Contains(got, "no Google Calendar link") {
		t.Errorf("second click did not run open; toasts = %q", got)
	}

	// With a link, open returns its command.
	m = threeEvents(t, 120, 40)
	m.events[1].HTMLLink = "https://calendar.google.com/calendar/event?eid=x"
	m, _ = press(m, x, y)
	_, cmd := press(m, x, y)
	if cmd == nil {
		t.Errorf("second click on a linked event should return the open command")
	}
}

func TestSecondClickOnADifferentEventOnlySelects(t *testing.T) {
	m := threeEvents(t, 120, 40)
	ax, ay := at(t, m, "Alpha standup")
	bx, by := at(t, m, "Bravo review")
	m, _ = press(m, ax, ay)
	m, cmd := press(m, bx, by)
	if m.selected != 1 || cmd != nil || strings.Contains(toastText(m), "Google Calendar link") {
		t.Errorf("clicking two different rows must select, not open: selected=%d cmd=%v toasts=%q", m.selected, cmd != nil, toastText(m))
	}
}

func TestSlowSecondClickIsNotADoubleClick(t *testing.T) {
	m := threeEvents(t, 120, 40)
	x, y := at(t, m, "Bravo review")
	m, _ = press(m, x, y)
	m.now = func() time.Time { return mouseBase.Add(2 * time.Second) }
	m, _ = press(m, x, y)
	if strings.Contains(toastText(m), "Google Calendar link") {
		t.Errorf("two clicks 2s apart opened the event")
	}
}

// A key between two clicks breaks the pair. Click an event, j, k back to it,
// click again inside the double-click window: that is a fresh first click,
// so it selects and must not open.
func TestKeyBetweenClicksBreaksTheDoubleClick(t *testing.T) {
	m := threeEvents(t, 120, 40)
	x, y := at(t, m, "Bravo review")
	m, _ = press(m, x, y)
	for _, k := range []string{"j", "k"} {
		next, _ := m.Update(keyMsg(k))
		m = next.(model)
	}
	if m.selected != 1 {
		t.Fatalf("selected = %d after j, k; want back on 1 (Bravo)", m.selected)
	}
	m, _ = press(m, x, y)
	if got := toastText(m); strings.Contains(got, "Google Calendar link") {
		t.Errorf("a key between the clicks must break the double click; open ran: %q", got)
	}
	if m.selected != 1 {
		t.Errorf("selected = %d, want 1 (Bravo) still selected", m.selected)
	}
}

func TestClickOnDayHeaderDoesNothing(t *testing.T) {
	m := threeEvents(t, 120, 40)
	m.selected = 1
	hdr := today().AddDate(0, 0, 2).Format("Mon Jan 02")
	x, y := at(t, m, hdr)
	before := m
	m, cmd := press(m, x, y)
	if m.selected != before.selected || cmd != nil || m.focusPane != before.focusPane {
		t.Errorf("a header click changed state: selected=%d", m.selected)
	}
}

func TestClickFocusesTheSchedulePane(t *testing.T) {
	m := threeEvents(t, 120, 40)
	m.activeOpen = true
	m.focusPane = focusActive
	x, y := at(t, m, "Bravo review")
	m, _ = press(m, x, y)
	if m.focusPane != focusMain || m.selected != 1 {
		t.Errorf("focus=%v selected=%d, want the schedule focused with Bravo selected", m.focusPane, m.selected)
	}
}

// A scrolled agenda: the selected row is far down, so rows are offset from the
// top of the event list. A hit test that forgot the offset would pick an event
// from the top of the list.
func TestClickInAScrolledAgendaHitsTheRowUnderThePointer(t *testing.T) {
	var evs []Event
	for i := 0; i < 60; i++ {
		evs = append(evs, mouseEvent(fmt.Sprintf("e%02d", i), fmt.Sprintf("Event number %02d", i), 1+i/3))
	}
	m := mouseModel(t, 120, 30, evs...)
	m.selected = 50
	m.anchor = m.viewDate(&m.events[50])
	x, y := at(t, m, "Event number 52")
	m, _ = press(m, x, y)
	if m.selected != 52 {
		t.Fatalf("selected = %d, want 52", m.selected)
	}
	if m.events[m.selected].ID != "e52" {
		t.Errorf("selection is not the clicked event")
	}
}

func TestWheelMovesTheSelectionThreeRows(t *testing.T) {
	var evs []Event
	for i := 0; i < 20; i++ {
		evs = append(evs, mouseEvent(fmt.Sprintf("e%02d", i), fmt.Sprintf("Event number %02d", i), 1+i))
	}
	m := mouseModel(t, 120, 40, evs...)
	x, y := at(t, m, "Event number 05")
	m, _ = wheel(m, x, y, true)
	if m.selected != 3 {
		t.Fatalf("wheel down: selected = %d, want 3", m.selected)
	}
	m, _ = wheel(m, x, y, true)
	m, _ = wheel(m, x, y, false)
	if m.selected != 3 {
		t.Errorf("down, down, up: selected = %d, want 3", m.selected)
	}
	m.focusPane = focusActive
	m, _ = wheel(m, x, y, true)
	if m.focusPane != focusMain {
		t.Errorf("wheel over the schedule must focus it")
	}
}

func TestWheelOverTheReadOnlyDetailCardDoesNothing(t *testing.T) {
	m := threeEvents(t, 120, 40)
	x, y := rightmostAt(t, m, "Alpha standup") // the detail card repeats the title
	if x < 40 {
		t.Fatalf("expected the detail card on the right, got x=%d", x)
	}
	before := m.selected
	m, _ = wheel(m, x, y, true)
	if m.selected != before {
		t.Errorf("wheel over the list view's detail card moved the selection")
	}
}

func TestReleaseMotionAndOtherButtonsAreIgnored(t *testing.T) {
	m := threeEvents(t, 120, 40)
	x, y := at(t, m, "Charlie sync")
	for name, msg := range map[string]tea.MouseMsg{
		"release": {X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease},
		"motion":  {X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion},
		"drag":    {X: x, Y: y, Button: tea.MouseButtonNone, Action: tea.MouseActionMotion},
		"right":   {X: x, Y: y, Button: tea.MouseButtonRight, Action: tea.MouseActionPress},
		"middle":  {X: x, Y: y, Button: tea.MouseButtonMiddle, Action: tea.MouseActionPress},
	} {
		got, cmd := send(m, msg)
		if got.selected != 0 || cmd != nil {
			t.Errorf("%s changed state: selected=%d", name, got.selected)
		}
	}
}

func TestClicksOnEmptyAreasDoNothing(t *testing.T) {
	m := threeEvents(t, 120, 40)
	m.selected = 1
	_, ly := at(t, m, "Charlie sync")
	for name, c := range map[string][2]int{
		"blank rows below the last event": {5, ly + 3},
		"header bar":                      {5, 0},
		"status bar":                      {5, m.height - 2},
		"hint bar":                        {5, m.height - 1},
		"past the right edge":             {m.width + 10, 5},
	} {
		got, cmd := press(m, c[0], c[1])
		if got.selected != 1 || cmd != nil || got.mode != modeNormal {
			t.Errorf("%s: click changed state (selected=%d)", name, got.selected)
		}
	}
}

func TestNarrowLayoutClicks(t *testing.T) {
	// 80 columns: no right-hand detail pane; the detail card moves below.
	m := threeEvents(t, 80, 30)
	if m.splitMode() != splitBottom {
		t.Fatalf("expected the bottom split at 80x30, got %v", m.splitMode())
	}
	x, y := at(t, m, "Charlie sync")
	m, _ = press(m, x, y)
	if m.selected != 2 {
		t.Errorf("narrow: selected = %d, want 2", m.selected)
	}
	// The detail card below is read-only.
	dx, dy := lastAt(t, m, "Charlie sync")
	if dy <= y {
		t.Fatalf("detail card not below the schedule (y=%d vs %d)", dy, y)
	}
	m.selected = 0
	m, _ = press(m, dx, dy)
	if m.selected != 0 {
		t.Errorf("a click in the read-only detail card changed the selection")
	}

	// Tiny: no detail pane at all.
	m = threeEvents(t, 60, 14)
	if m.splitMode() != splitNone {
		t.Fatalf("expected no split at 60x14, got %v", m.splitMode())
	}
	x, y = at(t, m, "Bravo review")
	m, _ = press(m, x, y)
	if m.selected != 1 {
		t.Errorf("tiny: selected = %d, want 1", m.selected)
	}
}

func TestLayoutMatchesWhatViewDraws(t *testing.T) {
	// The frame must still fill exactly m.height rows with the shared layout.
	for _, size := range [][2]int{{120, 40}, {80, 30}, {60, 14}} {
		m := threeEvents(t, size[0], size[1])
		if got := strings.Count(m.View(), "\n") + 1; got != size[1] {
			t.Errorf("%dx%d: frame is %d rows", size[0], size[1], got)
		}
	}
}

func activeFixture(t *testing.T) model {
	now := time.Now()
	mk := func(id, title string, from, to time.Duration) Event {
		e := mouseEvent(id, title, 1)
		e.StartAt, e.EndAt = now.Add(from), now.Add(to)
		e.StartDate = today()
		return e
	}
	m := mouseModel(t, 120, 40,
		mk("w1", "Window ending soon", -time.Hour, 30*time.Minute),
		mk("w2", "Window ending later", -time.Hour, 5*time.Hour),
		mouseEvent("later", "Tomorrow thing", 3),
	)
	m.activeOpen = true
	m.anchor = today()
	return m
}

func TestActivePanelClickSelectsAndSecondClickJumps(t *testing.T) {
	m := activeFixture(t)
	x, y := at(t, m, "Window ending later")
	m, _ = press(m, x, y)
	if m.focusPane != focusActive || m.activeIndex != 1 {
		t.Fatalf("focus=%v activeIndex=%d, want the panel focused on row 1", m.focusPane, m.activeIndex)
	}
	if got := m.selectedEvent().ID; got != "w2" {
		t.Errorf("schedule selection = %q, want w2 (kept in step with the panel)", got)
	}
	m, _ = press(m, x, y)
	if m.focusPane != focusMain {
		t.Errorf("second click should run panel.jump and hand focus back, focus=%v", m.focusPane)
	}
}

// More active windows than the panel has rows: it scrolls, so the first drawn
// row is not event 0.
func TestActivePanelClickWhileScrolled(t *testing.T) {
	now := time.Now()
	var evs []Event
	for i := 0; i < 9; i++ {
		e := mouseEvent(fmt.Sprintf("w%d", i), fmt.Sprintf("Window %d", i), 1)
		e.StartAt, e.EndAt = now.Add(-time.Hour), now.Add(time.Duration(i+1)*time.Hour)
		e.StartDate = today()
		evs = append(evs, e)
	}
	m := mouseModel(t, 120, 40, evs...)
	m.activeOpen, m.anchor = true, today()
	m.focusPane, m.activeIndex = focusActive, 8
	if start, _ := activePanelWindow(m.paneLayout().activeH, 8); start == 0 {
		t.Fatalf("test setup: the panel did not scroll past event 0")
	}
	x, y := at(t, m, "Window 6")
	m, _ = press(m, x, y)
	if m.activeIndex != 6 {
		t.Errorf("activeIndex = %d, want 6", m.activeIndex)
	}
}

func TestActivePanelWheelMovesItsCursor(t *testing.T) {
	m := activeFixture(t)
	x, y := at(t, m, "Window ending soon")
	m, _ = wheel(m, x, y, true)
	if m.focusPane != focusActive || m.activeIndex != 1 {
		t.Errorf("wheel in the panel: focus=%v activeIndex=%d, want focused, clamped to 1", m.focusPane, m.activeIndex)
	}
}

func weekModel(t *testing.T) model {
	m := mouseModel(t, 160, 40,
		mouseEvent("a", "Alpha standup", 1),
		mouseEvent("b", "Bravo review", 1),
		mouseEvent("c", "Charlie sync", 2),
	)
	m.view = viewWeek
	m.anchor = today()
	m.resetGridTop()
	return m
}

func TestGridClickMovesFocusToTheDay(t *testing.T) {
	m := weekModel(t)
	x, y := at(t, m, "Charli") // cells truncate titles
	m, _ = press(m, x, y)
	if !sameDay(m.anchor, today().AddDate(0, 0, 2)) {
		t.Fatalf("anchor = %v, want the clicked day", m.anchor)
	}
	if got := m.currentActionEvent(); got == nil || got.ID != "c" {
		t.Errorf("current event = %v, want Charlie", got)
	}
	// A second click on the focused day is Enter.
	m, _ = press(m, x, y)
	if !strings.Contains(toastText(m), "no Google Calendar link") {
		t.Errorf("second click did not run open: %q", toastText(m))
	}
}

// A date row (not an event row) of a later week: a hit test that forgot the
// weekday header row would land one line too high, in the previous week.
func TestGridClickOnALaterWeeksDateRow(t *testing.T) {
	m := weekModel(t)
	want := weekStart(today()).AddDate(0, 0, 7+3) // Wednesday of the second week
	x, y := at(t, m, want.Format("1/2"))
	m, _ = press(m, x, y)
	if !sameDay(m.anchor, want) {
		t.Errorf("anchor = %v, want %v", m.anchor, want)
	}
}

func TestGridWheelStepsAWeek(t *testing.T) {
	m := weekModel(t)
	x, y := at(t, m, "Charli")
	before := m.anchor
	m, _ = wheel(m, x, y, true)
	if got := daysBetween(before, m.anchor); got != 7 {
		t.Errorf("wheel down moved %d days, want 7 (grid.down)", got)
	}
}

func TestGridDetailPaneClickSelectsAnEvent(t *testing.T) {
	m := weekModel(t)
	m.anchor = today().AddDate(0, 0, 1)
	x, y := rightmostAt(t, m, "Bravo review")
	if x < 100 {
		t.Fatalf("expected the day-detail pane on the right, x=%d", x)
	}
	m, _ = press(m, x, y)
	if m.focusPane != focusDetail || m.gridDetail != 1 {
		t.Fatalf("focus=%v gridDetail=%d, want the detail pane on row 1", m.focusPane, m.gridDetail)
	}
	if got := m.currentActionEvent(); got == nil || got.ID != "b" {
		t.Errorf("current event = %v, want Bravo", got)
	}
	m, _ = wheel(m, x, y, false)
	if m.gridDetail != 0 {
		t.Errorf("wheel up in the detail pane: gridDetail=%d, want 0", m.gridDetail)
	}
}

func TestMonthGridClick(t *testing.T) {
	m := weekModel(t)
	m.view = viewMonth
	m.resetGridTop()
	x, y := at(t, m, "Charli")
	m, _ = press(m, x, y)
	if !sameDay(m.anchor, today().AddDate(0, 0, 2)) {
		t.Errorf("anchor = %v, want the clicked day", m.anchor)
	}
}

// Overlays own the mouse.

func TestHelpOwnsTheMouse(t *testing.T) {
	m := threeEvents(t, 120, 40)
	m.mode = modeHelp
	hx, hy := at(t, m, "Help")

	got, _ := wheel(m, hx, hy, true)
	if got.helpOff != 3 {
		t.Errorf("wheel in help: helpOff = %d, want 3", got.helpOff)
	}
	got, _ = wheel(got, hx, hy, false)
	if got.helpOff != 0 {
		t.Errorf("wheel up in help: helpOff = %d, want 0", got.helpOff)
	}

	// Inside: nothing, and the schedule underneath is untouched.
	got, cmd := press(m, hx, hy)
	if got.mode != modeHelp || got.selected != 0 || cmd != nil {
		t.Errorf("click inside help changed state: mode=%v selected=%d", got.mode, got.selected)
	}
	// A click that would hit the row behind the help popup must not reach it.
	bx, by := at(t, m, "Charlie sync")
	if rect := m.modalRect(); rect.Contains(bx, by) {
		t.Fatalf("test setup: the row is under the popup")
	}
	got, _ = press(m, bx, by)
	if got.selected != 0 {
		t.Errorf("a click outside help reached the agenda (selected=%d)", got.selected)
	}
	if got.mode != modeNormal {
		t.Errorf("a click outside help should close it, mode=%v", got.mode)
	}
}

func TestConfirmDeleteOwnsTheMouse(t *testing.T) {
	m := threeEvents(t, 120, 40)
	m.mode = modeConfirmDelete
	x, y := at(t, m, "Delete event?")
	got, cmd := press(m, x, y)
	if got.mode != modeConfirmDelete || cmd != nil {
		t.Errorf("a click inside the delete confirm must do nothing: mode=%v cmd=%v", got.mode, cmd != nil)
	}
	got, cmd = wheel(m, x, y, true)
	if got.mode != modeConfirmDelete || got.selected != 0 || cmd != nil {
		t.Errorf("wheel over the delete confirm leaked: selected=%d", got.selected)
	}
	got, _ = press(m, 2, 3)
	if got.mode != modeNormal {
		t.Errorf("a click outside is cancel (esc): mode=%v", got.mode)
	}
	if !strings.Contains(toastText(got), "delete cancelled") {
		t.Errorf("outside click should go through the cancel path: %q", toastText(got))
	}
}

func TestSearchPopupRowsAndOutsideClick(t *testing.T) {
	m := threeEvents(t, 120, 40)
	m.mode = modeSearch
	// Labels are cut to the popup width; "Charlie" survives, "sync" does not.
	x, y := lastAt(t, m, "Charlie")
	if !m.modalRect().Contains(x, y) {
		t.Fatalf("expected the search result inside the popup")
	}
	got, _ := press(m, x, y)
	if got.mode != modeSearch || got.searchIndex != 2 {
		t.Fatalf("click on a result: mode=%v searchIndex=%d, want it highlighted", got.mode, got.searchIndex)
	}
	if got.selected != 0 {
		t.Errorf("selecting a result must not jump yet")
	}
	got, _ = press(got, x, y)
	if got.mode != modeNormal || got.selected != 2 {
		t.Errorf("second click should run search.jump: mode=%v selected=%d", got.mode, got.selected)
	}

	got, _ = wheel(m, x, y, true)
	if got.searchIndex != 2 {
		t.Errorf("wheel in search: searchIndex=%d, want 2 (clamped from +3)", got.searchIndex)
	}
	got, _ = press(m, 2, 3)
	if got.mode != modeNormal || got.selected != 0 {
		t.Errorf("outside click should cancel the search: mode=%v", got.mode)
	}
}

func TestPickerRowClickSelectsAndOutsideCancels(t *testing.T) {
	m := threeEvents(t, 120, 40)
	m.mode = modeLinkPicker
	m.picker = pickerState{title: "Links - Enter opens", kind: pickerLinks, items: []pickerItem{
		{label: "https://one.example", value: "https://one.example"},
		{label: "https://two.example", value: "https://two.example"},
	}}
	x, y := at(t, m, "https://two.example")
	got, cmd := press(m, x, y)
	if got.picker.index != 1 || got.mode != modeLinkPicker || cmd != nil {
		t.Errorf("click on a picker row: index=%d mode=%v cmd=%v", got.picker.index, got.mode, cmd != nil)
	}
	got, _ = press(m, 2, 3)
	if got.mode != modeNormal {
		t.Errorf("outside click should cancel the picker")
	}
}

func TestCreateFormSwallowsTheMouse(t *testing.T) {
	m := threeEvents(t, 120, 40)
	m.mode = modeCreate
	m.create = m.newCreateState()
	m.create.title = "half-typed title"
	for name, c := range map[string][2]int{"outside": {2, 3}, "agenda row": {5, 4}} {
		got, cmd := press(m, c[0], c[1])
		if got.mode != modeCreate || got.create.title != "half-typed title" || cmd != nil {
			t.Errorf("%s click disturbed the form", name)
		}
	}
	got, _ := wheel(m, 60, 20, true)
	if got.mode != modeCreate || got.selected != 0 {
		t.Errorf("wheel disturbed the form")
	}
}

func TestOverlayPickerAndFindTimeCloseWithTheirEsc(t *testing.T) {
	m := threeEvents(t, 120, 40)
	m.mode = modeOverlayPicker
	m.overlay = m.newOverlayState()
	got, _ := press(m, 2, 3)
	if got.mode != modeNormal {
		t.Errorf("outside click should cancel the overlay picker")
	}
	m = threeEvents(t, 120, 40)
	m.mode = modeFindTime
	m.find = m.newFindState()
	got, _ = press(m, 2, 3)
	if got.mode != modeNormal {
		t.Errorf("outside click in find-a-time (who) should cancel, mode=%v", got.mode)
	}
}

// Staged edits: the mouse has no way to apply or discard one.

func stagedModel(t *testing.T) model {
	m := threeEvents(t, 120, 40)
	next, _ := m.run("nudge.later", tea.KeyMsg{})
	m = next.(model)
	if m.pending == nil {
		t.Fatal("nudge did not stage a change")
	}
	return m
}

func TestMouseNeverAppliesOrDiscardsAStagedEdit(t *testing.T) {
	staged := stagedModel(t)
	want := *staged.pending
	check := func(name string, m model, cmd tea.Cmd) {
		t.Helper()
		if m.pending == nil {
			t.Errorf("%s: the staged edit was discarded", name)
			return
		}
		if m.pending.saving {
			t.Errorf("%s: the staged edit was sent to Google", name)
		}
		if m.pending.startDelta != want.startDelta || m.pending.eventID != want.eventID {
			t.Errorf("%s: the staged edit changed", name)
		}
		_ = cmd
	}

	ax, ay := at(t, staged, "Alpha standup")
	bx, by := at(t, staged, "Bravo review")
	m, cmd := press(staged, bx, by)
	check("click another row", m, cmd)
	if cmd != nil {
		t.Errorf("click on another row returned a command")
	}
	m, cmd = press(staged, ax, ay)
	check("click the staged row", m, cmd)
	m, cmd = press(m, ax, ay) // double click: open, never save
	check("double click", m, cmd)
	m, cmd = wheel(staged, bx, by, true)
	check("wheel", m, cmd)
	m, cmd = press(staged, 2, 3)
	check("click empty area", m, cmd)
	for _, b := range []tea.MouseButton{tea.MouseButtonRight, tea.MouseButtonMiddle} {
		m, cmd = send(staged, tea.MouseMsg{X: ax, Y: ay, Button: b, Action: tea.MouseActionPress})
		check("other button", m, cmd)
		if cmd != nil {
			t.Errorf("right/middle click returned a command")
		}
	}

	// Overlays opened over a staged edit: outside click closes the overlay, the
	// staged edit stays.
	for _, mode := range []inputMode{modeHelp, modeConfirmDelete, modeSearch} {
		o := staged
		o.mode = mode
		m, cmd = press(o, 2, 3)
		check(fmt.Sprintf("outside click, mode %d", mode), m, cmd)
		if m.mode != modeNormal {
			t.Errorf("mode %d did not close", mode)
		}
	}
}

func TestMouseIgnoredWhileYankPrefixIsPending(t *testing.T) {
	m := threeEvents(t, 120, 40)
	m.yankPending = true
	x, y := at(t, m, "Charlie sync")
	got, _ := press(m, x, y)
	if got.selected != 0 || !got.yankPending {
		t.Errorf("a click disturbed the pending y prefix")
	}
}

func TestMouseIgnoredBeforeFirstLoad(t *testing.T) {
	m := threeEvents(t, 120, 40)
	m.loadedStart, m.loadedEnd = time.Time{}, time.Time{}
	m.loading = true
	got, _ := press(m, 10, 3)
	if got.selected != 0 || got.mode != modeNormal {
		t.Errorf("a click on the loading placeholder changed state")
	}
}

func TestClipRectAndDaysBetween(t *testing.T) {
	body := mouse.Rect{X: 0, Y: 1, W: 20, H: 10}
	if got := clipRect(mouse.Rect{X: 0, Y: 8, W: 30, H: 10}, body); got != (mouse.Rect{X: 0, Y: 8, W: 20, H: 3}) {
		t.Errorf("clipRect = %+v", got)
	}
	if got := clipRect(mouse.Rect{X: 0, Y: 20, W: 5, H: 5}, body); got != (mouse.Rect{}) {
		t.Errorf("a rect below the body must clip to nothing, got %+v", got)
	}
	a := time.Date(2026, 3, 7, 10, 0, 0, 0, time.Local)
	b := time.Date(2026, 3, 9, 0, 0, 0, 0, time.Local)
	if got := daysBetween(a, b); got != 2 {
		t.Errorf("daysBetween = %d, want 2", got)
	}
}
