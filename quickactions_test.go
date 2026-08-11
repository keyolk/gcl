package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// errFake stands in for a Google API failure in save-path tests.
var errFake = errors.New("calendar API 503")

func TestPreviewLineResolvesShorthand(t *testing.T) {
	now := time.Date(2026, 7, 15, 14, 23, 0, 0, time.Local)
	c := &createState{date: "tmr", start: "3pm", durationStr: "1h30m"}
	got := c.previewLine(time.Local, "KST", now)
	// Thursday 2026-07-16, 15:00 → 16:30
	for _, want := range []string{"Thu Jul 16", "15:00", "16:30", "1h30m", "KST"} {
		if !strings.Contains(got, want) {
			t.Errorf("previewLine = %q, missing %q", got, want)
		}
	}
}

func TestPreviewLineFlagsUnparseableInput(t *testing.T) {
	now := time.Date(2026, 7, 15, 14, 23, 0, 0, time.Local)
	if got := (&createState{date: "zzz", start: "3pm", durationStr: "30"}).previewLine(time.Local, "KST", now); !strings.Contains(got, "date not recognized") {
		t.Errorf("expected date hint, got %q", got)
	}
	if got := (&createState{date: "tmr", start: "zzz", durationStr: "30"}).previewLine(time.Local, "KST", now); !strings.Contains(got, "time not recognized") {
		t.Errorf("expected time hint, got %q", got)
	}
	if got := (&createState{date: "tmr", start: "3pm", durationStr: "zzz"}).previewLine(time.Local, "KST", now); !strings.Contains(got, "duration not recognized") {
		t.Errorf("expected duration hint, got %q", got)
	}
}

func TestPreviewLineMarksMidnightSpill(t *testing.T) {
	now := time.Date(2026, 7, 15, 14, 23, 0, 0, time.Local)
	// 23:00 + 2h lands on the next day.
	c := &createState{date: "2026-07-15", start: "23:00", durationStr: "2h"}
	if got := c.previewLine(time.Local, "KST", now); !strings.Contains(got, "+1d") {
		t.Errorf("expected +1d marker for a midnight-spanning event, got %q", got)
	}
}

func TestHumanMinutes(t *testing.T) {
	cases := map[int]string{30: "30m", 45: "45m", 60: "1h", 90: "1h30m", 135: "2h15m", 120: "2h"}
	for in, want := range cases {
		if got := humanMinutes(in); got != want {
			t.Errorf("humanMinutes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestDescribeRepeat(t *testing.T) {
	cases := map[string]string{
		"":          "",
		"weekly":    "weekly",
		"w":         "weekly",
		"매주":        "weekly",
		"biweekly":  "biweekly",
		"weekdays":  "weekdays",
		"weekly x4": "weekly, 4 times",
		"daily x10": "daily, 10 times",
	}
	for in, want := range cases {
		if got := describeRepeat(in); got != want {
			t.Errorf("describeRepeat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUndoSnapshotCapturesFields(t *testing.T) {
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	ev := &Event{
		ID:          "evt1",
		Title:       "Standup",
		StartAt:     start,
		EndAt:       start.Add(30 * time.Minute),
		Location:    "Room A",
		Description: "daily sync",
		Attendees:   []string{"a@example.com", "b@example.com"},
	}
	snap := undoSnapshot(ev, "me", undoPatch)
	if snap == nil {
		t.Fatal("expected a snapshot")
	}
	if snap.kind != undoPatch || snap.eventID != "evt1" || snap.calendar != "me" {
		t.Errorf("unexpected snapshot identity: %+v", snap)
	}
	if snap.title != "Standup" || snap.location != "Room A" || snap.description != "daily sync" {
		t.Errorf("unexpected snapshot fields: %+v", snap)
	}
	if !snap.start.Equal(start) || !snap.end.Equal(start.Add(30*time.Minute)) {
		t.Errorf("unexpected snapshot times: %v → %v", snap.start, snap.end)
	}
	if len(snap.attendees) != 2 {
		t.Errorf("expected 2 attendees, got %v", snap.attendees)
	}
	if snap.label != "Standup" {
		t.Errorf("label = %q, want Standup", snap.label)
	}
}

func TestUndoSnapshotLabelsUntitled(t *testing.T) {
	ev := &Event{ID: "x", Title: "   ", StartAt: time.Now(), EndAt: time.Now().Add(time.Hour)}
	if snap := undoSnapshot(ev, "me", undoDelete); snap == nil || snap.label != "(untitled)" {
		t.Errorf("expected (untitled) label, got %+v", snap)
	}
	if undoSnapshot(nil, "me", undoDelete) != nil {
		t.Error("expected nil snapshot for nil event")
	}
}

// handleQuickAction must not claim keys it does not own, otherwise normal
// navigation (j/k/h/l) would break.
func TestHandleQuickActionIgnoresUnrelatedKeys(t *testing.T) {
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	m := model{
		calendar: "me",
		view:     viewList,
		events: []Event{{
			ID: "evt1", Title: "Standup", StartAt: start, EndAt: start.Add(30 * time.Minute),
			StartDate: start, StartTime: "10:00",
		}},
	}
	for _, key := range []string{"j", "k", "h", "l", "g", "M", "N", "E", "X", "/", "?", "a"} {
		if _, _, handled := m.handleQuickAction(key); handled {
			t.Errorf("handleQuickAction claimed %q, which belongs to normal navigation", key)
		}
	}
}

// esc must stay available to the rest of the app while nothing is staged,
// otherwise it would stop backing out of overlays.
func TestHandleQuickActionLeavesEscAloneWithoutPending(t *testing.T) {
	m := model{calendar: "me", view: viewList}
	if _, _, handled := m.handleQuickAction("esc"); handled {
		t.Error("handleQuickAction claimed esc with nothing staged")
	}
}

func TestHandleQuickActionClaimsItsKeys(t *testing.T) {
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	base := model{
		calendar: "me",
		view:     viewList,
		events: []Event{{
			ID: "evt1", Title: "Standup", StartAt: start, EndAt: start.Add(30 * time.Minute),
			StartDate: start, StartTime: "10:00",
		}},
	}
	// Nudges/resizes/day-moves stage a change instead of issuing a command, so
	// "claimed" here means handled with the staged state updated.
	for _, key := range []string{")", "(", "}", "{", ">", "<"} {
		mm, cmd, handled := base.handleQuickAction(key)
		if !handled {
			t.Errorf("handleQuickAction did not claim %q", key)
			continue
		}
		if cmd != nil {
			t.Errorf("handleQuickAction(%q) issued a command; nudges must only stage", key)
		}
		if mm.(model).pending == nil {
			t.Errorf("handleQuickAction(%q) staged nothing", key)
		}
	}
	// Duplicates still act immediately - there is no half-applied copy to show.
	for _, key := range []string{"D", "W"} {
		_, cmd, handled := base.handleQuickAction(key)
		if !handled {
			t.Errorf("handleQuickAction did not claim %q", key)
			continue
		}
		if cmd == nil {
			t.Errorf("handleQuickAction(%q) returned no command", key)
		}
	}
}

func TestNudgeDoesNotCommitUntilSave(t *testing.T) {
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	m := model{
		calendar: "me",
		view:     viewList,
		events: []Event{{
			ID: "evt1", Title: "Standup", StartAt: start, EndAt: start.Add(30 * time.Minute),
			StartDate: start, StartTime: "10:00",
		}},
	}
	// Three nudges compose into one staged +45m, still uncommitted.
	for i := 0; i < 3; i++ {
		mm, cmd, _ := m.handleQuickAction(")")
		if cmd != nil {
			t.Fatalf("nudge %d issued a command before save", i+1)
		}
		m = mm.(model)
	}
	p := m.pending
	if p == nil {
		t.Fatal("expected a staged change")
	}
	if got := p.start(); !got.Equal(start.Add(45 * time.Minute)) {
		t.Errorf("staged start = %v, want %v", got, start.Add(45*time.Minute))
	}
	if got := p.end(); !got.Equal(start.Add(75 * time.Minute)) {
		t.Errorf("staged end = %v, want %v (duration must be preserved)", got, start.Add(75*time.Minute))
	}
	if !strings.Contains(p.label(), "+45m") {
		t.Errorf("label = %q, want it to mention +45m", p.label())
	}
	// The underlying event is untouched: the view reads through effectiveTimes.
	if !m.events[0].StartAt.Equal(start) {
		t.Errorf("staging mutated the event: %v", m.events[0].StartAt)
	}
	gotStart, gotEnd, unsaved := m.effectiveTimes(&m.events[0])
	if !unsaved || !gotStart.Equal(start.Add(45*time.Minute)) || !gotEnd.Equal(start.Add(75*time.Minute)) {
		t.Errorf("effectiveTimes = %v..%v unsaved=%v, want the staged times", gotStart, gotEnd, unsaved)
	}
	// Only `s` produces the patch command.
	_, cmd, handled := m.handleQuickAction("s")
	if !handled || cmd == nil {
		t.Fatalf("s did not produce a save command (handled=%v)", handled)
	}
}

func TestNudgeBackToOriginalClearsPending(t *testing.T) {
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	m := model{
		calendar: "me",
		view:     viewList,
		events: []Event{{
			ID: "evt1", Title: "Standup", StartAt: start, EndAt: start.Add(30 * time.Minute),
			StartDate: start, StartTime: "10:00",
		}},
	}
	mm, _, _ := m.handleQuickAction(")")
	m = mm.(model)
	mm, _, _ = m.handleQuickAction("(")
	m = mm.(model)
	if m.pending != nil {
		t.Errorf("nudging back to the saved time should clear the staged change, got %+v", m.pending)
	}
}

func TestDiscardPendingDropsTheChange(t *testing.T) {
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	m := model{
		calendar: "me",
		view:     viewList,
		events: []Event{{
			ID: "evt1", Title: "Standup", StartAt: start, EndAt: start.Add(30 * time.Minute),
			StartDate: start, StartTime: "10:00",
		}},
	}
	mm, _, _ := m.handleQuickAction(">")
	m = mm.(model)
	if m.pending == nil {
		t.Fatal("expected a staged day move")
	}
	mm, cmd, handled := m.handleQuickAction("esc")
	if !handled {
		t.Fatal("esc must be claimed while a change is staged")
	}
	if cmd != nil {
		t.Error("discarding must not touch Google")
	}
	if mm.(model).pending != nil {
		t.Error("esc did not discard the staged change")
	}
}

func TestStagePendingRejectsAllDayAndZeroDuration(t *testing.T) {
	m := model{calendar: "me"}
	// All-day events have an empty StartTime.
	allDay := &Event{ID: "x", Title: "Holiday"}
	if got := m.stagePending(allDay, nudgeStep, nudgeStep); !strings.Contains(got, "all-day") {
		t.Errorf("expected an all-day rejection, got %q", got)
	}
	if m.pending != nil {
		t.Error("a rejected nudge must not stage anything")
	}

	// Shrinking a 15-minute event by 15 minutes would zero it out.
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	short := &Event{ID: "y", Title: "Quick", StartAt: start, EndAt: start.Add(nudgeStep), StartTime: "10:00"}
	if got := m.stagePending(short, 0, -nudgeStep); !strings.Contains(got, "zero or negative") {
		t.Errorf("expected a zero-duration rejection, got %q", got)
	}
	if m.pending != nil {
		t.Error("a rejected resize must not stage anything")
	}
}

// Staging a second event while one is unsaved would silently drop the first
// change, so it is refused instead.
func TestStagePendingRefusesASecondEvent(t *testing.T) {
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	first := &Event{ID: "a", Title: "First", StartAt: start, EndAt: start.Add(time.Hour), StartTime: "10:00"}
	second := &Event{ID: "b", Title: "Second", StartAt: start, EndAt: start.Add(time.Hour), StartTime: "10:00"}
	m := model{calendar: "me"}
	m.stagePending(first, nudgeStep, nudgeStep)
	got := m.stagePending(second, nudgeStep, nudgeStep)
	if !strings.Contains(got, "First") {
		t.Errorf("expected a refusal naming the already-staged event, got %q", got)
	}
	if m.pending == nil || m.pending.eventID != "a" {
		t.Errorf("the first staged change must survive, got %+v", m.pending)
	}
}

func TestPendingLabelDescribesMoveAndResize(t *testing.T) {
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	cases := []struct {
		startDelta, endDelta time.Duration
		want                 string
	}{
		{nudgeStep, nudgeStep, "+15m"},
		{-24 * time.Hour, -24 * time.Hour, "-1d"},
		{0, nudgeStep, "15m longer"},
		{0, -nudgeStep, "15m shorter"},
		{nudgeStep, 2 * nudgeStep, "+15m, 15m longer"},
	}
	for _, tc := range cases {
		p := pendingShift{origStart: start, origEnd: start.Add(time.Hour), startDelta: tc.startDelta, endDelta: tc.endDelta}
		if got := p.label(); got != tc.want {
			t.Errorf("label(%v,%v) = %q, want %q", tc.startDelta, tc.endDelta, got, tc.want)
		}
	}
}

// The diff line repeats the date on the right side only when the change actually
// crosses to another day; otherwise it wastes width in a narrow detail pane.
func TestDiffLineRepeatsDateOnlyOnDayChange(t *testing.T) {
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	sameDayShift := pendingShift{
		origStart: start, origEnd: start.Add(30 * time.Minute),
		startDelta: nudgeStep, endDelta: nudgeStep,
	}
	if got := sameDayShift.diffLine(time.Local); got != "Jul 15 10:00-10:30 -> 10:15-10:45" {
		t.Errorf("diffLine (same day) = %q", got)
	}
	dayShift := pendingShift{
		origStart: start, origEnd: start.Add(30 * time.Minute),
		startDelta: 24 * time.Hour, endDelta: 24 * time.Hour,
	}
	if got := dayShift.diffLine(time.Local); got != "Jul 15 10:00-10:30 -> Jul 16 10:00-10:30" {
		t.Errorf("diffLine (day move) = %q", got)
	}
}

func TestHumanDurationSpellsOutDays(t *testing.T) {
	cases := map[time.Duration]string{
		15 * time.Minute:              "15m",
		90 * time.Minute:              "1h30m",
		24 * time.Hour:                "1d",
		24*time.Hour + 3*time.Hour:    "1d3h",
		48*time.Hour + 30*time.Minute: "2d30m",
		24*time.Hour + 90*time.Minute: "1d1h30m",
	}
	for in, want := range cases {
		if got := humanDuration(in); got != want {
			t.Errorf("humanDuration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestUndoKeyReportsNothingToUndo(t *testing.T) {
	m := model{calendar: "me", view: viewList}
	mm, cmd, handled := m.handleQuickAction("u")
	if !handled {
		t.Fatal("expected u to be handled")
	}
	if cmd != nil {
		t.Error("expected no command when there is nothing to undo")
	}
	if got := mm.(model).status; got != "nothing to undo" {
		t.Errorf("status = %q, want 'nothing to undo'", got)
	}
}

// A failed save must leave the staged change in place, or the user silently
// loses the adjustment they were told was pending.
func TestFailedSaveKeepsPendingForRetry(t *testing.T) {
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	m := model{
		calendar: "me",
		view:     viewList,
		events: []Event{{
			ID: "evt1", Title: "Standup", StartAt: start, EndAt: start.Add(30 * time.Minute),
			StartDate: start, StartTime: "10:00",
		}},
	}
	mm, _, _ := m.handleQuickAction(")")
	m = mm.(model)
	// Pressing s marks the staged change in-flight but does not drop it.
	mm, cmd, _ := m.handleQuickAction("s")
	m = mm.(model)
	if cmd == nil {
		t.Fatal("s produced no save command")
	}
	if m.pending == nil || !m.pending.saving {
		t.Fatalf("s must keep the staged change and mark it saving, got %+v", m.pending)
	}
	// The patch comes back as a failure.
	mm, _ = m.Update(eventShiftedMsg{err: errFake, committed: true})
	got := mm.(model)
	if got.pending == nil {
		t.Fatal("a failed save discarded the staged change")
	}
	if got.pending.saving {
		t.Error("a failed save must clear the in-flight flag so s can retry")
	}
	if !strings.Contains(got.status, "retries") {
		t.Errorf("status = %q, want it to offer a retry", got.status)
	}
	// Retry works.
	if _, cmd, _ := got.handleQuickAction("s"); cmd == nil {
		t.Error("s did not retry after a failed save")
	}
}

func TestSuccessfulSaveClearsPending(t *testing.T) {
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	m := model{
		calendar: "me",
		view:     viewList,
		events: []Event{{
			ID: "evt1", Title: "Standup", StartAt: start, EndAt: start.Add(30 * time.Minute),
			StartDate: start, StartTime: "10:00",
		}},
	}
	mm, _, _ := m.handleQuickAction(")")
	m = mm.(model)
	mm, _, _ = m.handleQuickAction("s")
	m = mm.(model)
	restore := &undoEntry{kind: undoPatch, calendar: "me", eventID: "evt1", label: "Standup"}
	mm, _ = m.Update(eventShiftedMsg{label: "saved +15m", restore: restore, committed: true})
	got := mm.(model)
	if got.pending != nil {
		t.Errorf("a successful save must clear the staged change, got %+v", got.pending)
	}
	if got.lastUndo != restore {
		t.Error("a successful save must record the undo entry")
	}
}

// q must not quit out from under an unsaved change.
func TestQuitRefusedWhilePending(t *testing.T) {
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	m := model{
		calendar: "me",
		view:     viewList,
		events: []Event{{
			ID: "evt1", Title: "Standup", StartAt: start, EndAt: start.Add(30 * time.Minute),
			StartDate: start, StartTime: "10:00",
		}},
	}
	mm, _, _ := m.handleQuickAction(")")
	m = mm.(model)
	mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil {
		t.Error("q must not quit while a change is unsaved")
	}
	if !strings.Contains(mm.(model).status, "unsaved") {
		t.Errorf("status = %q, want it to explain the refusal", mm.(model).status)
	}
	// After discarding, q quits.
	discarded, _, _ := mm.(model).handleQuickAction("esc")
	if _, cmd := discarded.(model).handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil {
		t.Error("q must quit once nothing is staged")
	}
}

func TestDuplicateEventHandlesAllDay(t *testing.T) {
	m := model{calendar: "me"}
	// All-day events used to be rejected by duplicate; now they duplicate
	// by shifting the date fields and flagging the copy as all-day. The
	// command is constructed locally (no API call needed to verify that
	// the all-day path is taken, mirroring the shift rejection test).
	allDay := &Event{
		ID:        "x",
		Title:     "Holiday",
		StartDate: time.Date(2026, 7, 15, 0, 0, 0, 0, time.Local),
		EndDate:   time.Date(2026, 7, 15, 0, 0, 0, 0, time.Local),
	}
	cmd := m.duplicateEventCmd(allDay, 0, "duplicated \"Holiday\"")
	if cmd == nil {
		t.Fatal("expected a duplicate command for an all-day event (was rejected before)")
	}
}

func TestDuplicateEventCopiesToNextWeek(t *testing.T) {
	m := model{calendar: "me"}
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	ev := &Event{
		ID:        "x",
		Title:     "Standup",
		StartAt:   start,
		EndAt:     start.Add(30 * time.Minute),
		StartDate: start,
		StartTime: "10:00",
	}
	// W copies into the same slot next week (offset=7 days). The command
	// is constructed locally; the real API call happens when bubbletea
	// executes it (not during this test, so no OAuth is needed here).
	cmd := m.duplicateEventCmd(ev, 7*24*time.Hour, "copied \"Standup\" to next week")
	if cmd == nil {
		t.Fatal("expected a duplicate command for next week")
	}
}
