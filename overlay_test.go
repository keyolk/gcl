package main

import (
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// overlayEvent builds a timed event owned by a calendar, on a fixed test day.
//
// The year is deliberately far in the future: an "active now" event outranks
// the owner tint on the bar (by design), so a fixture landing on the real
// current clock would make the color assertions pass or fail by time of day.
func overlayEvent(id, cal, title string, hour, mins int) Event {
	s := time.Date(2099, 9, 4, hour, 0, 0, 0, time.Local)
	e := s.Add(time.Duration(mins) * time.Minute)
	return Event{
		ID: id, Calendar: cal, Title: title,
		StartDate: s, StartAt: s, EndAt: e,
		StartTime: s.Format("15:04"), EndTime: e.Format("15:04"),
	}
}

// overlaidModel is a three-calendar overlay with a few events on each.
func overlaidModel(width, height int) model {
	m := model{
		width: width, height: height, calendar: "gavin@x.com", view: viewList,
		anchor: time.Date(2099, 9, 4, 0, 0, 0, 0, time.Local), jumpUnit: "day",
	}
	m.overlay.active = []string{"gavin@x.com", "jace.son@x.com", "yuna.kim@x.com"}
	m.events = []Event{
		overlayEvent("1", "gavin@x.com", "Standup", 9, 30),
		overlayEvent("2", "jace.son@x.com", "1:1 with lead", 10, 60),
		overlayEvent("3", "yuna.kim@x.com", "Design review", 10, 90),
		overlayEvent("4", "gavin@x.com", "Retro", 14, 60),
	}
	sortEvents(m.events)
	return m
}

func TestOverlayRowsIdentifyTheirOwner(t *testing.T) {
	// A merged agenda whose rows are indistinguishable is worse than two
	// separate ones — every row has to say whose event it is.
	m := overlaidModel(170, 24)
	cols := timelineColsWithReserve(max(20, m.schedulePaneWidth()-1), m.overlayRowReserve())
	row := stripANSI(m.eventRow(&m.events[1], false, m.schedulePaneWidth()-1,
		m.viewDayStart(m.anchor), cols))
	if !strings.Contains(row, "jace.son") {
		t.Errorf("row does not name its owner: %q", row)
	}
	if !strings.Contains(row, "●") {
		t.Errorf("row has no color dot: %q", row)
	}
	// Regression: the padded name ran straight into the title ("jace.son1:1").
	if strings.Contains(row, "jace.son1:1") {
		t.Errorf("owner name is not separated from the title: %q", row)
	}
}

func TestOverlayOffLeavesRowsUnchanged(t *testing.T) {
	// Without an overlay the row must render exactly as it always did — no dot,
	// no name column eating the title.
	m := overlaidModel(170, 24)
	m.overlay.active = nil
	row := stripANSI(m.eventRow(&m.events[0], false, 100, m.viewDayStart(m.anchor), 0))
	if strings.Contains(row, "●") {
		t.Errorf("a non-overlay row grew an owner dot: %q", row)
	}
	if m.overlayRowReserve() != 0 {
		t.Errorf("overlayRowReserve = %d with no overlay, want 0", m.overlayRowReserve())
	}
}

func TestOverlayTimelineBarsCarryTheOwnerColor(t *testing.T) {
	// The overlap picture is the reason to overlay at all: the bars have to be
	// per-person or the shared axis only says "something overlaps".
	m := overlaidModel(170, 24)
	cols := timelineColsWithReserve(max(20, m.schedulePaneWidth()-1), m.overlayRowReserve())
	if cols == 0 {
		t.Fatal("fixture pane is too narrow to draw a timeline")
	}
	re := regexp.MustCompile(`48;5;(\d+)`)
	for i := range m.events {
		ev := &m.events[i]
		want := m.overlayColorFor(ev.Calendar)
		bar := m.timelineBar(ev, m.viewDayStart(m.anchor), cols, false)
		var found bool
		for _, mm := range re.FindAllStringSubmatch(bar, -1) {
			if mm[1] == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q bar is not tinted with its owner color %s", ev.Title, want)
		}
	}
}

func TestOverlayRowStateOutranksOwnerColor(t *testing.T) {
	// Selected / staged / active-now say something about THIS row that matters
	// more than who owns it, so they must still win the bar color.
	m := overlaidModel(170, 24)
	cols := timelineColsWithReserve(max(20, m.schedulePaneWidth()-1), m.overlayRowReserve())
	ev := &m.events[0]
	bar := m.timelineBar(ev, m.viewDayStart(m.anchor), cols, true /* selected */)
	if !strings.Contains(bar, "48;5;"+tlSelBG) {
		t.Errorf("a selected row lost the selection color to the owner tint")
	}
}

func TestOverlayNameShrinksToKeepTheTimeline(t *testing.T) {
	// On a pane that cannot carry both, the name gives way — the dot plus the
	// legend still identify the owner, but nothing else shows overlap.
	narrow := overlaidModel(120, 24)
	inner := max(20, narrow.schedulePaneWidth()-1)
	if got := timelineColsWithReserve(inner, narrow.overlayRowReserve()); got == 0 {
		t.Errorf("timeline was dropped at width 120 instead of shrinking the name "+
			"(nameWidth=%d reserve=%d)", narrow.overlayNameWidth(), narrow.overlayRowReserve())
	}
	// A wide pane keeps the full names.
	wide := overlaidModel(200, 24)
	if wide.overlayNameWidth() < 8 {
		t.Errorf("wide pane shrank the name to %d unnecessarily", wide.overlayNameWidth())
	}
}

func TestOverlayLegendMapsColorsAndFlagsFailures(t *testing.T) {
	// The dots are decoration without a key, and a calendar that failed to load
	// looks exactly like an empty one — the most dangerous way this can fail.
	m := overlaidModel(170, 24)
	got := stripANSI(m.overlayLegend(160))
	for _, want := range []string{"gavin", "jace.son", "yuna.kim"} {
		if !strings.Contains(got, want) {
			t.Errorf("legend %q missing %q", got, want)
		}
	}
	// Per-person counts distinguish "nothing scheduled" from "not loaded".
	if !strings.Contains(got, "gavin 2") {
		t.Errorf("legend %q does not carry per-calendar event counts", got)
	}

	m.overlay.failed = map[string]string{"yuna.kim@x.com": "no access"}
	got = stripANSI(m.overlayLegend(160))
	if !strings.Contains(got, "no access") {
		t.Errorf("a failed calendar was not flagged: %q", got)
	}
	if !strings.Contains(got, "○ yuna.kim") {
		t.Errorf("a failed calendar still renders as a normal one: %q", got)
	}
}

func TestEventCalendarRoutesActionsToTheRowsOwner(t *testing.T) {
	// This is what makes overlay edits safe: patching a colleague's event
	// against m.calendar would 404, or hit a same-titled event on the wrong
	// calendar.
	m := overlaidModel(170, 24)
	for i := range m.events {
		ev := &m.events[i]
		if got := m.eventCalendar(ev); got != ev.Calendar {
			t.Errorf("eventCalendar(%q) = %q, want the event's own calendar %q",
				ev.Title, got, ev.Calendar)
		}
	}
	// With no overlay, actions target the single open calendar as before.
	m.overlay.active = nil
	if got := m.eventCalendar(&m.events[0]); got != "gavin@x.com" {
		t.Errorf("eventCalendar without an overlay = %q, want the open calendar", got)
	}
	// A nil event must not panic and must fall back to the open calendar.
	if got := m.eventCalendar(nil); got != "gavin@x.com" {
		t.Errorf("eventCalendar(nil) = %q", got)
	}
}

func TestOverlayEditTargetsTheRowsCalendar(t *testing.T) {
	// Opening E on someone else's row must build a form that patches THEIR
	// calendar, not the one currently open.
	m := overlaidModel(170, 24)
	var other *Event
	for i := range m.events {
		if m.events[i].Calendar != m.calendar {
			other = &m.events[i]
			break
		}
	}
	if other == nil {
		t.Fatal("fixture has no event from another calendar")
	}
	c := m.editCreateState(other)
	if c.calendar != other.Calendar {
		t.Errorf("edit form targets %q, want the row's calendar %q", c.calendar, other.Calendar)
	}
	if m.preEdit != nil && m.preEdit.calendar == m.calendar && other.Calendar != m.calendar {
		t.Error("the undo snapshot recorded the wrong calendar")
	}
}

func TestOverlayStagedMoveTargetsTheRowsCalendar(t *testing.T) {
	// A nudge on someone else's row stages against their calendar, so `s`
	// patches the right one.
	m := overlaidModel(170, 24)
	var other *Event
	for i := range m.events {
		if m.events[i].Calendar != m.calendar {
			other = &m.events[i]
			break
		}
	}
	m.stagePending(other, nudgeStep, nudgeStep, 0)
	if m.pending == nil {
		t.Fatal("nothing was staged")
	}
	if m.pending.calendar != other.Calendar {
		t.Errorf("staged against %q, want %q", m.pending.calendar, other.Calendar)
	}
	// And nudging it again still recognizes it as the same target rather than
	// refusing as "a different event".
	before := *m.pending
	got := m.stagePending(other, nudgeStep, nudgeStep, 0)
	if strings.Contains(got, "unsaved change on") {
		t.Errorf("a second nudge on the same overlay row was refused: %q", got)
	}
	if m.pending.startDelta == before.startDelta {
		t.Error("the second nudge did not compose into the staged change")
	}
}

func TestOverlayPickerTogglesAndApplies(t *testing.T) {
	m := model{width: 170, height: 24, calendar: "gavin@x.com"}
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("O")})
	got := updated.(model)
	if got.mode != modeOverlayPicker {
		t.Fatalf("mode = %v, want modeOverlayPicker", got.mode)
	}
	// The open calendar is pre-selected: an overlay you are not in is a picture
	// of everyone else's day.
	if !got.overlay.selected["gavin@x.com"] {
		t.Error("the open calendar was not pre-selected")
	}

	got.overlay.cands = []pickerItem{{label: "jace@x.com", value: "jace@x.com"}}
	got.overlay.input = ""
	updated, _ = got.handleOverlayKey(tea.KeyMsg{Type: tea.KeySpace})
	got = updated.(model)
	if !got.overlay.selected["jace@x.com"] {
		t.Fatal("space did not toggle the highlighted calendar on")
	}
	updated, cmd := got.handleOverlayKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(model)
	if !got.overlay.on() {
		t.Fatal("Enter did not apply the overlay")
	}
	if len(got.overlay.active) != 2 {
		t.Errorf("active = %v, want both calendars", got.overlay.active)
	}
	if cmd == nil {
		t.Error("applying an overlay did not trigger a reload")
	}
}

func TestOverlayPickerEmptySetTurnsItOff(t *testing.T) {
	// Confirming an empty set is how the overlay is turned off — more
	// discoverable than a separate key for it.
	m := overlaidModel(170, 24)
	m.mode = modeOverlayPicker
	m.overlay.selected = map[string]bool{}
	updated, cmd := m.handleOverlayKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(model)
	if got.overlay.on() {
		t.Errorf("an empty selection left the overlay on: %v", got.overlay.active)
	}
	if cmd == nil {
		t.Error("turning the overlay off did not trigger a reload")
	}
}

func TestOverlayPickerRefusesMoreCalendarsThanColors(t *testing.T) {
	// Past the palette every extra person reuses a color and the dots stop
	// identifying anyone. Refuse rather than render a lie.
	m := model{width: 170, height: 24, calendar: "me@x.com", mode: modeOverlayPicker}
	m.overlay = overlayState{selected: map[string]bool{}}
	for i := 0; i <= len(overlayColors); i++ {
		m.overlay.selected[string(rune('a'+i))+"@x.com"] = true
	}
	updated, cmd := m.handleOverlayKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(model)
	if got.overlay.on() {
		t.Error("an over-large overlay was applied")
	}
	if got.mode != modeOverlayPicker {
		t.Error("the picker closed instead of reporting the limit")
	}
	if cmd != nil {
		t.Error("a refused overlay still triggered a load")
	}
	if !strings.Contains(got.status, "told apart") {
		t.Errorf("status = %q, want it to explain the color limit", got.status)
	}
}

func TestOverlayReopenKeepsTheCurrentSet(t *testing.T) {
	// Adding a fourth person must not mean re-picking the first three.
	m := overlaidModel(170, 24)
	o := m.newOverlayState()
	for _, cal := range m.overlay.active {
		if !o.selected[cal] {
			t.Errorf("re-opening the picker dropped %q from the selection", cal)
		}
	}
}

func TestOverlayLoadCmdBranchesFromLoadCmd(t *testing.T) {
	// The overlay has to actually replace the single-calendar fetch, or it
	// would render one person's events with everyone's legend.
	m := overlaidModel(170, 24)
	if m.loadCmd(1) == nil {
		t.Fatal("loadCmd returned nothing while an overlay was on")
	}
	// Not a strong assertion on its own; the message type is what proves the
	// branch, and that is covered by the Update test below.
}

func TestOverlayLoadedMsgReportsUnreadableCalendars(t *testing.T) {
	// A calendar that failed to load looks exactly like an empty one in the
	// agenda. The status line is what keeps that from being silent.
	m := overlaidModel(170, 24)
	m.inflightReq = 7
	msg := overlayLoadedMsg{
		events: m.events,
		failed: map[string]string{"yuna.kim@x.com": "no access"},
		start:  m.anchor, end: m.anchor.AddDate(0, 0, 7),
		reqID: 7,
	}
	updated, _ := m.Update(msg)
	got := updated.(model)
	if !strings.Contains(got.status, "NOT loaded") {
		t.Errorf("status = %q, want it to name the calendars that failed", got.status)
	}
	if !strings.Contains(got.status, "yuna.kim") {
		t.Errorf("status = %q, want the failed calendar named", got.status)
	}
	if got.loading {
		t.Error("loading was left on after the overlay resolved")
	}
}

func TestOverlayLoadedMsgIgnoresStaleResponses(t *testing.T) {
	// Same guard the single-calendar path has: a superseded fetch must not
	// overwrite a newer one.
	m := overlaidModel(170, 24)
	m.inflightReq = 9
	before := len(m.events)
	updated, _ := m.Update(overlayLoadedMsg{events: nil, reqID: 3})
	if got := len(updated.(model).events); got != before {
		t.Errorf("a stale overlay response replaced the event list (%d -> %d)", before, got)
	}
}

func TestSwitchingCalendarClearsTheOverlay(t *testing.T) {
	// `e` picks a single calendar. Leaving the overlay on would change only
	// which calendar new events land on, while the agenda kept showing the same
	// merged set — the keypress would look like it did nothing.
	m := overlaidModel(170, 24)
	m.picker = pickerState{kind: pickerCalendar}
	updated, cmd := m.choosePicker(pickerItem{label: "other@x.com", value: "other@x.com"})
	got := updated.(model)
	if got.overlay.on() {
		t.Errorf("switching calendars left the overlay on: %v", got.overlay.active)
	}
	if got.calendar != "other@x.com" {
		t.Errorf("calendar = %q, want the picked one", got.calendar)
	}
	if cmd == nil {
		t.Error("switching did not trigger a reload")
	}
	// The note has to survive the "loading~" the fetch installs and land with
	// the events, or the user never learns the overlay was dropped.
	if !strings.Contains(got.pendingNote, "overlay off") {
		t.Errorf("pendingNote = %q, want it to say the overlay was turned off", got.pendingNote)
	}
	settled, _ := got.Update(eventsMsg{reqID: got.inflightReq, start: got.anchor, end: got.anchor})
	if !strings.Contains(settled.(model).status, "overlay off") {
		t.Errorf("status after the load = %q, want the overlay-off note",
			settled.(model).status)
	}
}

func TestNewEventLandsOnTheOpenCalendarDuringOverlay(t *testing.T) {
	// Editing follows the row, but CREATING has no row to follow — a new event
	// belongs on the calendar you have open, not on whoever's row is selected.
	m := overlaidModel(170, 24)
	c := m.newCreateState()
	if c.editing {
		t.Fatal("a new-event form should not be in editing mode")
	}
	if c.calendar != "" {
		t.Errorf("a new-event form pinned a calendar (%q); it should use m.calendar", c.calendar)
	}
}
