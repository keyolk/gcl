package main

import (
	"strings"
	"testing"
	"time"
)

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
	for _, key := range []string{"j", "k", "h", "l", "g", "M", "N", "E", "X", "/", "?"} {
		if _, _, handled := m.handleQuickAction(key); handled {
			t.Errorf("handleQuickAction claimed %q, which belongs to normal navigation", key)
		}
	}
}

func TestHandleQuickActionClaimsItsKeys(t *testing.T) {
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	m := model{
		calendar: "me",
		view:     viewList,
		events: []Event{{
			ID: "evt1", Title: "Standup", StartAt: start, EndAt: start.Add(30 * time.Minute),
			StartDate: start, StartTime: "10:00",
		}},
	}
	for _, key := range []string{")", "(", "}", "{", ">", "<", "D", "W"} {
		_, cmd, handled := m.handleQuickAction(key)
		if !handled {
			t.Errorf("handleQuickAction did not claim %q", key)
			continue
		}
		if cmd == nil {
			t.Errorf("handleQuickAction(%q) returned no command", key)
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

func TestShiftEventRejectsAllDayAndZeroDuration(t *testing.T) {
	m := model{calendar: "me"}
	// All-day events have an empty StartTime.
	allDay := &Event{ID: "x", Title: "Holiday"}
	cmd := m.shiftEventCmd(allDay, nudgeStep, nudgeStep, "moved")
	if cmd == nil {
		t.Fatal("expected a command reporting the all-day rejection")
	}
	msg, ok := cmd().(eventShiftedMsg)
	if !ok || msg.err == nil {
		t.Errorf("expected an error for all-day nudge, got %+v", msg)
	}

	// Shrinking a 15-minute event by 15 minutes would zero it out.
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.Local)
	short := &Event{ID: "y", Title: "Quick", StartAt: start, EndAt: start.Add(nudgeStep), StartTime: "10:00"}
	cmd = m.shiftEventCmd(short, 0, -nudgeStep, "shortened")
	if cmd == nil {
		t.Fatal("expected a command reporting the zero-duration rejection")
	}
	msg, ok = cmd().(eventShiftedMsg)
	if !ok || msg.err == nil {
		t.Errorf("expected an error for zero-duration resize, got %+v", msg)
	}
}
