package main

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// keyMsg builds the tea.KeyMsg for a key name, so tests drive handleKey the way
// bubbletea does.
func keyMsg(key string) tea.KeyMsg {
	switch key {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
}

func day(y int, mo time.Month, d int) time.Time {
	return time.Date(y, mo, d, 0, 0, 0, 0, time.Local)
}

// The whole point of the panel: a multi-day window that started days ago and
// ends days from now is "active", even though the agenda has scrolled past it.
func TestIsActiveAtCoversMultiDayAllDayWindows(t *testing.T) {
	now := time.Date(2026, 7, 27, 14, 0, 0, 0, time.Local)
	// All-day events carry dates only; the API end date is exclusive.
	window := &Event{
		Title:     "maintenance window apne2",
		StartDate: day(2026, 7, 25),
		EndDate:   day(2026, 7, 29), // covers 25,26,27,28
	}
	if !isActiveAt(window, now) {
		t.Error("a 25→28 all-day window must be active on the 27th")
	}
	if isActiveAt(window, time.Date(2026, 7, 29, 0, 0, 0, 0, time.Local)) {
		t.Error("the exclusive end date must not count as active")
	}
	if isActiveAt(window, time.Date(2026, 7, 24, 23, 59, 0, 0, time.Local)) {
		t.Error("the window must not be active before it starts")
	}
}

// A single-day all-day event with no end date must still cover its own day
// rather than collapsing to a zero-length span.
func TestIsActiveAtHandlesMissingAllDayEnd(t *testing.T) {
	ev := &Event{Title: "Holiday", StartDate: day(2026, 7, 27)}
	if !isActiveAt(ev, time.Date(2026, 7, 27, 9, 0, 0, 0, time.Local)) {
		t.Error("an all-day event with no end date must cover its own day")
	}
	if isActiveAt(ev, time.Date(2026, 7, 28, 9, 0, 0, 0, time.Local)) {
		t.Error("it must not spill into the next day")
	}
}

func TestIsActiveAtTimedEvents(t *testing.T) {
	start := time.Date(2026, 7, 27, 14, 0, 0, 0, time.Local)
	ev := &Event{Title: "Sync", StartAt: start, EndAt: start.Add(time.Hour), StartTime: "14:00"}
	cases := []struct {
		at   time.Time
		want bool
	}{
		{start.Add(-time.Minute), false},
		{start, true},
		{start.Add(30 * time.Minute), true},
		{start.Add(time.Hour), false}, // end is exclusive
		{start.Add(2 * time.Hour), false},
	}
	for _, tc := range cases {
		if got := isActiveAt(ev, tc.at); got != tc.want {
			t.Errorf("isActiveAt(%v) = %v, want %v", tc.at.Format("15:04"), got, tc.want)
		}
	}
}

// The panel is ordered by what lapses first, so the thing you may need to
// extend or act on is at the top.
func TestActiveEventsSortSoonestEndingFirst(t *testing.T) {
	now := time.Date(2026, 7, 27, 14, 0, 0, 0, time.Local)
	long := Event{
		Title: "long window", StartDate: day(2026, 7, 20), EndDate: day(2026, 8, 1),
	}
	short := Event{
		Title: "ending soon", StartAt: now.Add(-30 * time.Minute), EndAt: now.Add(10 * time.Minute),
		StartDate: day(2026, 7, 27), StartTime: "13:30",
	}
	mid := Event{
		Title: "mid", StartAt: now.Add(-time.Hour), EndAt: now.Add(3 * time.Hour),
		StartDate: day(2026, 7, 27), StartTime: "13:00",
	}
	past := Event{
		Title: "already over", StartAt: now.Add(-3 * time.Hour), EndAt: now.Add(-2 * time.Hour),
		StartDate: day(2026, 7, 27), StartTime: "11:00",
	}
	future := Event{
		Title: "not yet", StartAt: now.Add(time.Hour), EndAt: now.Add(2 * time.Hour),
		StartDate: day(2026, 7, 27), StartTime: "15:00",
	}
	m := model{events: []Event{long, past, future, mid, short}}
	got := m.activeEvents(now)
	want := []string{"ending soon", "mid", "long window"}
	if len(got) != len(want) {
		t.Fatalf("got %d active events, want %d (%v)", len(got), len(want), titlesOf(got))
	}
	for i, w := range want {
		if got[i].Title != w {
			t.Errorf("active[%d] = %q, want %q (full order %v)", i, got[i].Title, w, titlesOf(got))
		}
	}
}

func titlesOf(evs []*Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Title
	}
	return out
}

// twoWindows is a calendar with one finished event and two running ones, which
// is the shape every panel-focus test needs.
func twoWindows() model {
	now := time.Now()
	return model{
		calendar: "me",
		view:     viewList,
		anchor:   today(),
		width:    120,
		height:   40,
		events: []Event{
			{ID: "past", Title: "over", StartAt: now.Add(-3 * time.Hour), EndAt: now.Add(-2 * time.Hour),
				StartDate: today(), StartTime: "01:00"},
			{ID: "live", Title: "running window", StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
				StartDate: today(), StartTime: "02:00"},
			{ID: "longer", Title: "longer window", StartAt: now.Add(-time.Hour), EndAt: now.Add(3 * time.Hour),
				StartDate: today(), StartTime: "02:30"},
		},
	}
}

// The core of the requirement: `a` shows the panel WITHOUT taking focus or
// moving the cursor. You glance at it and carry on where you were.
func TestActiveToggleDoesNotStealFocusOrSelection(t *testing.T) {
	m := twoWindows()
	m.selected = 0 // sitting on the finished event
	mm, _ := m.handleKey(keyMsg("a"))
	got := mm.(model)
	if !got.activeOpen {
		t.Fatal("a did not open the panel")
	}
	if got.focusPane != focusMain {
		t.Errorf("a stole focus: focusPane=%v, want focusMain", got.focusPane)
	}
	if got.selected != 0 {
		t.Errorf("a moved the selection to %d; it must leave the cursor alone", got.selected)
	}
	// Normal navigation still works with the panel open.
	nav, _ := got.handleKey(keyMsg("j"))
	navd := nav.(model)
	if navd.selected != 1 {
		t.Errorf("j did not move the agenda selection while the panel was open: %d", navd.selected)
	}
	if !navd.activeOpen {
		t.Error("navigating closed the panel")
	}
	// And a second `a` closes it.
	closed, _ := navd.handleKey(keyMsg("a"))
	if closed.(model).activeOpen {
		t.Error("a did not toggle the panel closed")
	}
}

// View switches, search, refresh etc. must keep working untouched while the
// panel is open but unfocused — that is what "not modal" means.
func TestPanelOpenDoesNotSwallowNormalKeys(t *testing.T) {
	m := twoWindows()
	mm, _ := m.handleKey(keyMsg("a"))
	open := mm.(model)

	grid, _ := open.handleKey(keyMsg("g"))
	if grid.(model).view != viewWeek {
		t.Error("g did not switch to the week grid while the panel was open")
	}
	search, _ := open.handleKey(keyMsg("/"))
	if search.(model).mode != modeSearch {
		t.Error("/ did not open search while the panel was open")
	}
	newEv, _ := open.handleKey(keyMsg("N"))
	if newEv.(model).mode != modeCreate {
		t.Error("N did not open the create form while the panel was open")
	}
}

// tab moves focus into the panel; from there j/k drive the panel's cursor.
func TestTabFocusesPanelAndNavigatesInside(t *testing.T) {
	m := twoWindows()
	mm, _ := m.handleKey(keyMsg("a"))
	mm, _ = mm.(model).handleKey(keyMsg("tab"))
	focused := mm.(model)
	if !focused.activePanelFocused() {
		t.Fatalf("tab did not focus the panel: focusPane=%v", focused.focusPane)
	}
	// Entering the panel puts the selection on the cursor's event immediately.
	if ev := focused.selectedEvent(); ev == nil || ev.ID != "live" {
		t.Errorf("focusing the panel did not sync the selection, got %+v", ev)
	}
	// j moves the PANEL cursor (not the agenda by itself).
	mm, _ = focused.handleKey(keyMsg("j"))
	moved := mm.(model)
	if moved.activeIndex != 1 {
		t.Errorf("j did not move the panel cursor: %d", moved.activeIndex)
	}
	if ev := moved.selectedEvent(); ev == nil || ev.ID != "longer" {
		t.Errorf("panel cursor move did not follow through to the selection, got %+v", ev)
	}
	// esc leaves the panel but keeps it open.
	mm, _ = moved.handleKey(keyMsg("esc"))
	back := mm.(model)
	if back.activePanelFocused() {
		t.Error("esc did not release focus from the panel")
	}
	if !back.activeOpen {
		t.Error("esc closed the panel; it should only release focus (a closes)")
	}
}

// tab must skip panes that are not on screen, so it never lands nowhere.
func TestTabSkipsAbsentPanes(t *testing.T) {
	// List view, no panel: nothing else to focus.
	m := twoWindows()
	mm, _ := m.handleKey(keyMsg("tab"))
	if mm.(model).focusPane != focusMain {
		t.Errorf("tab focused a pane that isn't on screen: %v", mm.(model).focusPane)
	}
	// Grid view with the panel open: main -> detail -> active -> main.
	g := twoWindows()
	g.view = viewWeek
	g.activeOpen = true
	want := []paneFocus{focusDetail, focusActive, focusMain}
	cur := tea.Model(g)
	for i, w := range want {
		cur, _ = cur.(model).handleKey(keyMsg("tab"))
		if got := cur.(model).focusPane; got != w {
			t.Errorf("tab %d landed on %v, want %v", i+1, got, w)
		}
	}
}

// Closing the panel while focused must hand focus back, or keys would go to a
// pane that is no longer rendered.
func TestClosingFocusedPanelReleasesFocus(t *testing.T) {
	m := twoWindows()
	mm, _ := m.handleKey(keyMsg("a"))
	mm, _ = mm.(model).handleKey(keyMsg("tab"))
	if !mm.(model).activePanelFocused() {
		t.Fatal("setup: panel not focused")
	}
	mm, _ = mm.(model).handleKey(keyMsg("a"))
	got := mm.(model)
	if got.activeOpen {
		t.Error("a did not close the panel")
	}
	if got.focusPane != focusMain {
		t.Errorf("closing a focused panel left focus at %v", got.focusPane)
	}
}

// X while the panel is focused must target the panel's event, since the panel
// keeps the schedule selection in sync rather than intercepting the key.
func TestActivePanelDeleteTargetsThePanelEvent(t *testing.T) {
	m := twoWindows()
	m.selected = 0 // the agenda is sitting on the finished event
	mm, _ := m.handleKey(keyMsg("a"))
	mm, _ = mm.(model).handleKey(keyMsg("tab"))
	mm, _ = mm.(model).handleKey(keyMsg("X"))
	got := mm.(model)
	if got.mode != modeConfirmDelete {
		t.Fatalf("X should open the delete confirmation, mode=%v", got.mode)
	}
	ev := got.selectedEvent()
	if ev == nil || ev.ID != "live" {
		t.Fatalf("delete targets %+v, want the active event 'live'", ev)
	}
}

// E while the panel is focused must open the edit form on the panel's event.
func TestActivePanelEditTargetsThePanelEvent(t *testing.T) {
	m := twoWindows()
	m.selected = 0
	mm, _ := m.handleKey(keyMsg("a"))
	mm, _ = mm.(model).handleKey(keyMsg("tab"))
	mm, _ = mm.(model).handleKey(keyMsg("j")) // move to the second window
	mm, _ = mm.(model).handleKey(keyMsg("E"))
	got := mm.(model)
	if got.mode != modeCreate {
		t.Fatalf("E should open the edit form, mode=%v", got.mode)
	}
	if got.create.eventID != "longer" {
		t.Errorf("edit targets %q, want 'longer'", got.create.eventID)
	}
}

func TestActiveCountLabelQuietWhenNothingActive(t *testing.T) {
	now := time.Date(2026, 7, 27, 14, 0, 0, 0, time.Local)
	m := model{events: []Event{{
		Title: "later", StartAt: now.Add(time.Hour), EndAt: now.Add(2 * time.Hour),
		StartDate: day(2026, 7, 27), StartTime: "15:00",
	}}}
	if got := m.activeCountLabel(now); got != "" {
		t.Errorf("activeCountLabel = %q, want empty when nothing is active", got)
	}
	m.events[0].StartAt = now.Add(-time.Hour)
	m.events[0].EndAt = now.Add(time.Hour)
	if got := m.activeCountLabel(now); got == "" {
		t.Error("activeCountLabel must report an active event")
	}
}

func TestRemainingAndElapsedLabels(t *testing.T) {
	now := time.Date(2026, 7, 27, 14, 0, 0, 0, time.Local)
	ev := &Event{
		Title: "window", StartAt: now.Add(-25 * time.Hour), EndAt: now.Add(90 * time.Minute),
		StartDate: day(2026, 7, 26), StartTime: "13:00",
	}
	if got := remainingLabel(ev, now); got != "ends in 1h30m" {
		t.Errorf("remainingLabel = %q, want 'ends in 1h30m'", got)
	}
	if got := elapsedLabel(ev, now); got != "started 1d1h ago" {
		t.Errorf("elapsedLabel = %q, want 'started 1d1h ago'", got)
	}
	over := &Event{Title: "x", StartAt: now.Add(-2 * time.Hour), EndAt: now.Add(-time.Hour), StartTime: "12:00"}
	if got := remainingLabel(over, now); got != "ended" {
		t.Errorf("remainingLabel(past) = %q, want 'ended'", got)
	}
}

// The all-day span label must show the last covered day, not the exclusive end
// date, or a 25→28 window would read as "to the 29th".
func TestActiveSpanLabelShowsInclusiveAllDayEnd(t *testing.T) {
	m := model{}
	multi := &Event{Title: "w", StartDate: day(2026, 7, 25), EndDate: day(2026, 7, 29)}
	if got := m.activeSpanLabel(multi); got != "Jul 25 -> Jul 28 all-day" {
		t.Errorf("activeSpanLabel = %q, want 'Jul 25 -> Jul 28 all-day'", got)
	}
	single := &Event{Title: "w", StartDate: day(2026, 7, 27), EndDate: day(2026, 7, 28)}
	if got := m.activeSpanLabel(single); got != "Jul 27 all-day" {
		t.Errorf("activeSpanLabel = %q, want 'Jul 27 all-day'", got)
	}
}

// Enter in the focused panel jumps the schedule to the event and hands focus
// back — the panel is a launcher, not a place to get stuck.
func TestActivePanelEnterJumpsAndReleasesFocus(t *testing.T) {
	m := twoWindows()
	m.selected = 0
	mm, _ := m.handleKey(keyMsg("a"))
	mm, _ = mm.(model).handleKey(keyMsg("tab"))
	mm, _ = mm.(model).handleKey(keyMsg("j")) // second window
	mm, _ = mm.(model).handleKey(keyMsg("enter"))
	got := mm.(model)
	if got.activePanelFocused() {
		t.Error("Enter should hand focus back to the schedule")
	}
	if !got.activeOpen {
		t.Error("Enter closed the panel; it should stay open")
	}
	if ev := got.selectedEvent(); ev == nil || ev.ID != "longer" {
		t.Errorf("Enter selected %+v, want the panel's event 'longer'", ev)
	}
}

// The gcalcli TSV path supplies dates and wall-clock times in separate columns.
// When those never became absolute instants, every timed event fell through to
// the all-day branch of activeSpan: the whole day reported itself "in effect",
// and the "now" divider — which sorts on eventSortInstant — sank below the day
// because a zero StartAt sorts as year 0001.
func TestTimedEventWithoutAbsoluteInstantsIsNotTreatedAsAllDay(t *testing.T) {
	day := time.Date(2026, 8, 13, 0, 0, 0, 0, time.Local)
	now := time.Date(2026, 8, 13, 14, 6, 0, 0, time.Local)
	ev := &Event{
		Title: "Core Platform daily sync", StartDate: day, EndDate: day,
		StartTime: "09:30", EndTime: "10:30",
	}

	start, end := activeSpan(ev)
	wantStart := time.Date(2026, 8, 13, 9, 30, 0, 0, time.Local)
	wantEnd := time.Date(2026, 8, 13, 10, 30, 0, 0, time.Local)
	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Errorf("activeSpan = %v..%v, want %v..%v", start, end, wantStart, wantEnd)
	}
	if isActiveAt(ev, now) {
		t.Error("a 09:30-10:30 event must not report itself active at 14:06")
	}
	if got := eventSortInstant(ev); !got.Equal(wantStart) {
		t.Errorf("eventSortInstant = %v, want %v", got, wantStart)
	}
}

// An end time on the following day (a window crossing midnight) must keep its
// real end rather than collapsing onto the start date.
func TestTsvInstantsCrossMidnight(t *testing.T) {
	ev := &Event{
		StartDate: time.Date(2026, 8, 13, 0, 0, 0, 0, time.Local),
		EndDate:   time.Date(2026, 8, 14, 0, 0, 0, 0, time.Local),
		StartTime: "23:30", EndTime: "00:30",
	}
	start, end := tsvInstants(ev)
	if want := time.Date(2026, 8, 13, 23, 30, 0, 0, time.Local); !start.Equal(want) {
		t.Errorf("start = %v, want %v", start, want)
	}
	if want := time.Date(2026, 8, 14, 0, 30, 0, 0, time.Local); !end.Equal(want) {
		t.Errorf("end = %v, want %v", end, want)
	}
}

// All-day events keep zero instants: AllDay()-aware code reads StartDate/EndDate
// and relies on the zero value to tell the two shapes apart.
func TestTsvInstantsLeavesAllDayZero(t *testing.T) {
	ev := &Event{StartDate: time.Date(2026, 8, 13, 0, 0, 0, 0, time.Local)}
	if start, end := tsvInstants(ev); !start.IsZero() || !end.IsZero() {
		t.Errorf("all-day got %v..%v, want zero instants", start, end)
	}
}
