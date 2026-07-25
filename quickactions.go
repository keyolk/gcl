package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Quick actions and single-level undo.
//
// Rescheduling an event used to mean opening the edit form, retyping the time,
// and submitting. These actions operate on the selected event directly:
//
//	)  / (      nudge start later / earlier by nudgeStep (keeps duration)
//	}  / {      lengthen / shorten by nudgeStep (keeps start)
//	>  / <      move to the next / previous day (keeps time of day)
//	D           duplicate the event (same time, "(copy)" suffix)
//	W           duplicate into the same slot next week
//	u           undo the last create / edit / delete / quick action
//
// Every mutating action records a single-level undo entry, so a mis-nudge or an
// accidental delete is recoverable without leaving the TUI.

// nudgeStep is the granularity for time nudges and resizes. 15 minutes matches
// the smallest slot people normally schedule on.
const nudgeStep = 15 * time.Minute

// undoSnapshot captures an event's current field values so the action can be
// reversed later. kind describes how to reverse it.
func undoSnapshot(ev *Event, calendar string, kind undoKind) *undoEntry {
	if ev == nil {
		return nil
	}
	label := ev.Title
	if strings.TrimSpace(label) == "" {
		label = "(untitled)"
	}
	atts := make([]string, 0, len(ev.Attendees))
	for _, a := range ev.Attendees {
		if e := attendeeEmail(a); e != "" {
			atts = append(atts, e)
		}
	}
	return &undoEntry{
		kind:        kind,
		calendar:    calendar,
		eventID:     ev.ID,
		label:       label,
		title:       ev.Title,
		start:       ev.StartAt,
		end:         ev.EndAt,
		location:    ev.Location,
		description: ev.Description,
		attendees:   atts,
	}
}

// undoCmd reverses the recorded action. Create → delete, delete → re-create,
// patch/quick-action → restore the snapshotted field values.
func (m model) undoCmd() tea.Cmd {
	u := m.lastUndo
	if u == nil {
		return nil
	}
	entry := *u // copy: the command runs after Update returns
	return func() tea.Msg {
		switch entry.kind {
		case undoCreate:
			if err := deleteEvent(entry.calendar, entry.eventID, len(entry.attendees) > 0); err != nil {
				return undoneMsg{err: err}
			}
			return undoneMsg{label: "create of \"" + entry.label + "\""}

		case undoDelete:
			created, err := createEvent(createEventInput{
				Calendar:    entry.calendar,
				Title:       entry.title,
				Start:       entry.start,
				End:         entry.end,
				Location:    entry.location,
				Description: entry.description,
				Attendees:   entry.attendees,
				// Re-creating after an accidental delete should not spam a
				// second round of invitation mail.
				Notify: false,
			})
			if err != nil {
				return undoneMsg{err: err}
			}
			_ = created
			return undoneMsg{label: "delete of \"" + entry.label + "\""}

		case undoPatch:
			if _, err := patchEvent(patchEventInput{
				Calendar:    entry.calendar,
				EventID:     entry.eventID,
				Title:       entry.title,
				Start:       entry.start,
				End:         entry.end,
				Location:    entry.location,
				Description: entry.description,
				Attendees:   entry.attendees,
				Notify:      false,
			}); err != nil {
				return undoneMsg{err: err}
			}
			return undoneMsg{label: "edit of \"" + entry.label + "\""}
		}
		return undoneMsg{err: fmt.Errorf("nothing to undo")}
	}
}

// shiftEventCmd moves and/or resizes the selected event by the given deltas and
// reports an eventShiftedMsg. startDelta moves the start (duration preserved
// when endDelta matches it); endDelta moves the end.
func (m model) shiftEventCmd(ev *Event, startDelta, endDelta time.Duration, label string) tea.Cmd {
	if ev == nil || ev.ID == "" {
		return nil
	}
	if ev.AllDay() {
		return func() tea.Msg {
			return eventShiftedMsg{err: fmt.Errorf("all-day events cannot be nudged")}
		}
	}
	cal := m.calendar
	restore := undoSnapshot(ev, cal, undoPatch)
	newStart := ev.StartAt.Add(startDelta)
	newEnd := ev.EndAt.Add(endDelta)
	if !newEnd.After(newStart) {
		return func() tea.Msg {
			return eventShiftedMsg{err: fmt.Errorf("duration would be zero or negative")}
		}
	}
	snap := *restore
	title := ev.Title
	loc := ev.Location
	desc := ev.Description
	atts := snap.attendees
	return func() tea.Msg {
		if _, err := patchEvent(patchEventInput{
			Calendar:    cal,
			EventID:     snap.eventID,
			Title:       title,
			Start:       newStart,
			End:         newEnd,
			Location:    loc,
			Description: desc,
			Attendees:   atts,
			// Quick nudges are for your own calendar hygiene; don't mail
			// attendees on every 15-minute tweak.
			Notify: false,
		}); err != nil {
			return eventShiftedMsg{err: err}
		}
		return eventShiftedMsg{label: label, restore: &snap}
	}
}

// duplicateEventCmd copies the selected event, optionally offset by `offset`
// (used for "same slot next week"). The copy keeps attendees but never mails
// them — a duplicate is a draft until you explicitly edit and notify.
func (m model) duplicateEventCmd(ev *Event, offset time.Duration, label string) tea.Cmd {
	if ev == nil || ev.ID == "" {
		return nil
	}
	if ev.AllDay() {
		return func() tea.Msg {
			return eventShiftedMsg{err: fmt.Errorf("all-day events cannot be duplicated yet")}
		}
	}
	cal := m.calendar
	title := ev.Title
	if offset == 0 {
		title = strings.TrimSpace(ev.Title) + " (copy)"
	}
	start := ev.StartAt.Add(offset)
	end := ev.EndAt.Add(offset)
	loc := ev.Location
	desc := ev.Description
	var atts []string
	for _, a := range ev.Attendees {
		if e := attendeeEmail(a); e != "" {
			atts = append(atts, e)
		}
	}
	return func() tea.Msg {
		created, err := createEvent(createEventInput{
			Calendar:    cal,
			Title:       title,
			Start:       start,
			End:         end,
			Location:    loc,
			Description: desc,
			Attendees:   atts,
			Notify:      false,
		})
		if err != nil {
			return eventShiftedMsg{err: err}
		}
		// The duplicate is reversed by deleting it.
		return eventShiftedMsg{
			label: label,
			restore: &undoEntry{
				kind:      undoCreate,
				calendar:  cal,
				eventID:   created.ID,
				label:     title,
				attendees: atts,
			},
		}
	}
}

// handleQuickAction dispatches the quick-action keys. It returns handled=false
// when the key is not a quick action, so the caller can continue its own
// dispatch.
func (m model) handleQuickAction(key string) (tea.Model, tea.Cmd, bool) {
	// `u` works even with no event selected.
	if key == "u" {
		if m.lastUndo == nil {
			m.status = "nothing to undo"
			return m, nil, true
		}
		m.status = "undoing~"
		return m, m.undoCmd(), true
	}

	ev := m.selectedEvent()
	if ev == nil || ev.ID == "" {
		return m, nil, false
	}

	switch key {
	case ")":
		m.status = "moving +15m~"
		return m, m.shiftEventCmd(ev, nudgeStep, nudgeStep, "moved +15m"), true
	case "(":
		m.status = "moving -15m~"
		return m, m.shiftEventCmd(ev, -nudgeStep, -nudgeStep, "moved -15m"), true
	case "}":
		m.status = "lengthening +15m~"
		return m, m.shiftEventCmd(ev, 0, nudgeStep, "lengthened +15m"), true
	case "{":
		m.status = "shortening -15m~"
		return m, m.shiftEventCmd(ev, 0, -nudgeStep, "shortened -15m"), true
	case ">":
		m.status = "moving to next day~"
		return m, m.shiftEventCmd(ev, 24*time.Hour, 24*time.Hour, "moved +1 day"), true
	case "<":
		m.status = "moving to previous day~"
		return m, m.shiftEventCmd(ev, -24*time.Hour, -24*time.Hour, "moved -1 day"), true
	case "D":
		m.status = "duplicating~"
		return m, m.duplicateEventCmd(ev, 0, "duplicated \""+ev.Title+"\""), true
	case "W":
		m.status = "copying to next week~"
		return m, m.duplicateEventCmd(ev, 7*24*time.Hour, "copied \""+ev.Title+"\" to next week"), true
	}
	return m, nil, false
}
