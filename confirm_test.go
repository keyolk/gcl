package main

import (
	"strings"
	"testing"
	"time"
)

// confirmNow is a fixed "now" for warning tests: 2026-09-03 (Thu) 14:00 local.
func confirmNow() time.Time {
	return time.Date(2026, 9, 3, 14, 0, 0, 0, time.Local)
}

func TestConfirmOverlayShowsTheEventBeingCreated(t *testing.T) {
	// Regression: the emptiness check was inverted, so every submit rendered
	// "(no event selected)" and the confirmation stopped confirming anything.
	m := model{width: 110, height: 40, mode: modeConfirmSubmit}
	m.create = createState{
		title: "Team sync", date: "2026-09-04", start: "10:00",
		durationStr: "60", duration: 60,
		selected: map[string]bool{"a@x.com": true},
	}
	got := stripANSI(m.viewConfirmSubmit())
	if strings.Contains(got, "no event selected") {
		t.Fatalf("confirm overlay claimed nothing was selected:\n%s", got)
	}
	for _, want := range []string{"Team sync", "10:00", "11:00"} {
		if !strings.Contains(got, want) {
			t.Errorf("confirm overlay missing %q:\n%s", want, got)
		}
	}
	// Regression: the footer was built with a %s that had no argument, so it
	// rendered the verb literally.
	if strings.Contains(got, "%s") {
		t.Errorf("unformatted %%s leaked into the footer:\n%s", got)
	}
}

func TestConfirmOverlayListsOnlyWhatChanges(t *testing.T) {
	// An edit confirmation that restates every field cannot be read. Only the
	// touched fields appear — a rename is one line.
	m := model{width: 110, height: 40, mode: modeConfirmSubmit}
	m.create = createState{
		title: "Renamed", date: "2026-09-04", start: "10:00",
		durationStr: "60", duration: 60, location: "Room A",
		editing: true, eventID: "e1",
		selected: map[string]bool{"a@x.com": true},
		orig: &editSnapshot{
			title: "Team sync", date: "2026-09-04", start: "10:00",
			duration: 60, location: "Room A", attendees: []string{"a@x.com"},
		},
	}
	got := stripANSI(m.viewConfirmSubmit())
	if !strings.Contains(got, "Title") || !strings.Contains(got, "Renamed") {
		t.Errorf("the rename is not shown:\n%s", got)
	}
	// Location was not touched, so it must not be listed as a change.
	if strings.Contains(got, "Location") {
		t.Errorf("an untouched field was reported as a change:\n%s", got)
	}
	if !strings.Contains(got, "1 change") {
		t.Errorf("expected a single change, got:\n%s", got)
	}
}

func TestConfirmOverlayFlagsAnUnchangedEdit(t *testing.T) {
	// Submitting an untouched form mails everyone for nothing. It has to say so
	// rather than looking like a normal save.
	orig := &editSnapshot{title: "Team sync", date: "2026-09-04", start: "10:00",
		duration: 60, attendees: []string{"a@x.com"}}
	m := model{width: 110, height: 40, mode: modeConfirmSubmit}
	m.create = createState{
		title: "Team sync", date: "2026-09-04", start: "10:00",
		durationStr: "60", duration: 60, editing: true, eventID: "e1",
		selected: map[string]bool{"a@x.com": true},
		orig:     orig,
	}
	got := stripANSI(m.viewConfirmSubmit())
	if !strings.Contains(got, "nothing changed") {
		t.Errorf("an unchanged edit was not flagged:\n%s", got)
	}
}

func TestChangesIgnoresShorthandThatResolvesToTheSameTime(t *testing.T) {
	// The form may still hold "tmr" while the snapshot holds "2026-09-04".
	// Comparing the raw text would report a change that isn't one.
	tomorrow := confirmNow().AddDate(0, 0, 1)
	c := &createState{
		title: "x", date: "tmr", start: "3pm", durationStr: "1h",
		editing: true,
		orig: &editSnapshot{
			title: "x", date: tomorrow.Format("2006-01-02"), start: "15:00", duration: 60,
		},
	}
	if got := c.changes(confirmNow()); len(got) != 0 {
		t.Errorf("shorthand that resolves to the stored time reported changes: %+v", got)
	}
}

func TestChangesReportsAddedAndRemovedAttendees(t *testing.T) {
	// "2 → 2" hides that one person was dropped and another added. Dropping
	// someone silently un-invites them, so both directions are named.
	c := &createState{
		title: "x", date: "2026-09-04", start: "10:00", durationStr: "60",
		editing:  true,
		selected: map[string]bool{"a@x.com": true, "c@x.com": true},
		orig: &editSnapshot{
			title: "x", date: "2026-09-04", start: "10:00", duration: 60,
			attendees: []string{"a@x.com", "b@x.com"},
		},
	}
	changes := c.changes(confirmNow())
	var att *fieldChange
	for i := range changes {
		if changes[i].label == "Attendees" {
			att = &changes[i]
		}
	}
	if att == nil {
		t.Fatalf("attendee change not reported: %+v", changes)
	}
	if !strings.Contains(att.to, "+c") || !strings.Contains(att.to, "-b") {
		t.Errorf("attendee diff = %q, want both the addition and the removal", att.to)
	}
	if !att.risky {
		t.Error("an attendee change reaches other people and should be marked risky")
	}
}

func TestNotifiesAttendeesCountsRemovedPeople(t *testing.T) {
	// Someone removed from an event still gets a cancellation, so the "will
	// email N" count must not undercount them.
	c := &createState{
		editing:  true,
		selected: map[string]bool{"a@x.com": true},
		orig:     &editSnapshot{attendees: []string{"a@x.com", "b@x.com"}},
	}
	if got := c.notifiesAttendees(); got != 2 {
		t.Errorf("notifiesAttendees = %d, want 2 (1 remaining + 1 cancelled)", got)
	}
}

func TestSubmitWarningsFlagsAPastStart(t *testing.T) {
	// "-3d" typed for "+3d" silently lands an event in last week. Nothing
	// rejects it, so the confirm step has to say it out loud.
	m := model{width: 110, height: 40}
	c := createState{title: "x", date: "2026-08-29", start: "10:00", durationStr: "60"}
	warns := m.submitWarnings(c, confirmNow())
	if len(warns) == 0 || !strings.Contains(warns[0], "PAST") {
		t.Errorf("a past start was not warned about: %v", warns)
	}
	// A future event produces no warning at all.
	c.date = "2026-09-10"
	if warns := m.submitWarnings(c, confirmNow()); len(warns) != 0 {
		t.Errorf("a normal future event warned unnecessarily: %v", warns)
	}
}

func TestSubmitWarningsFlagsOutlierDurations(t *testing.T) {
	m := model{width: 110, height: 40}
	long := createState{title: "x", date: "2026-09-10", start: "10:00", durationStr: "600"}
	if warns := m.submitWarnings(long, confirmNow()); len(warns) == 0 ||
		!strings.Contains(warns[0], "10h") {
		t.Errorf("a 10h event was not questioned: %v", warns)
	}
	tiny := createState{title: "x", date: "2026-09-10", start: "10:00", durationStr: "2"}
	if warns := m.submitWarnings(tiny, confirmNow()); len(warns) == 0 {
		t.Errorf("a 2m event was not questioned: %v", warns)
	}
	normal := createState{title: "x", date: "2026-09-10", start: "10:00", durationStr: "60"}
	if warns := m.submitWarnings(normal, confirmNow()); len(warns) != 0 {
		t.Errorf("an ordinary 1h event warned: %v", warns)
	}
}

func TestConfirmOverlayWarnsAboutEndlessRecurrence(t *testing.T) {
	// "weekly" with no count creates an event that never ends — the one field
	// where a mistake keeps multiplying after you have forgotten about it.
	m := model{width: 110, height: 40, mode: modeConfirmSubmit}
	m.create = createState{title: "x", date: "2026-09-10", start: "10:00",
		durationStr: "60", duration: 60, repeat: "weekly"}
	got := stripANSI(m.viewConfirmSubmit())
	if !strings.Contains(got, "forever") {
		t.Errorf("endless recurrence was not flagged:\n%s", got)
	}
	// Bounded recurrence is normal and must not carry the same warning.
	m.create.repeat = "weekly x4"
	if got := stripANSI(m.viewConfirmSubmit()); strings.Contains(got, "forever") {
		t.Errorf("a bounded repeat was flagged as endless:\n%s", got)
	}
}

func TestChangeLineStacksRatherThanTruncating(t *testing.T) {
	// A truncated value cannot be confirmed. When the pair does not fit, the
	// row stacks; neither side is shortened into "Sep 04 10:00-11:~".
	m := model{width: 110, height: 40}
	ch := fieldChange{
		label: "Time",
		from:  "Fri Sep 04 10:00-11:00",
		to:    "Sat Sep 05 14:00-14:30",
		risky: true,
	}
	got := stripANSI(m.changeLine(ch, 40))
	if strings.Contains(got, "~") {
		t.Errorf("a confirmable value was truncated: %q", got)
	}
	if !strings.Contains(got, "\n") {
		t.Errorf("expected the row to stack at this width: %q", got)
	}
	for _, want := range []string{"10:00-11:00", "14:00-14:30"} {
		if !strings.Contains(got, want) {
			t.Errorf("row %q lost %q", got, want)
		}
	}
}

func TestEditFormSnapshotsOriginalValues(t *testing.T) {
	// Without the snapshot the confirm step has nothing to diff against and
	// silently degrades to restating the form.
	m := model{width: 110, height: 40, calendar: "c"}
	start := time.Date(2026, 9, 4, 10, 0, 0, 0, time.Local)
	ev := &Event{ID: "e1", Title: "Team sync", StartAt: start, EndAt: start.Add(time.Hour),
		StartTime: "10:00", Location: "Room A", Attendees: []string{"a@x.com"}}
	c := m.editCreateState(ev)
	if c.orig == nil {
		t.Fatal("edit form did not snapshot the original values")
	}
	if c.orig.title != "Team sync" || c.orig.start != "10:00" || c.orig.duration != 60 {
		t.Errorf("snapshot = %+v, want the event's own values", c.orig)
	}
	if len(c.orig.attendees) != 1 || c.orig.attendees[0] != "a@x.com" {
		t.Errorf("snapshot attendees = %v", c.orig.attendees)
	}
	// A freshly-opened edit form has, by definition, changed nothing.
	if got := c.changes(confirmNow()); len(got) != 0 {
		t.Errorf("an untouched edit form reported changes: %+v", got)
	}
}
