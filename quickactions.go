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
//	)  / (      stage a start nudge later / earlier by nudgeStep (keeps duration)
//	}  / {      stage a lengthen / shorten by nudgeStep (keeps start)
//	>  / <      stage a move to the next / previous day (keeps time of day)
//	s           save the staged change to Google
//	esc         discard the staged change
//	D           duplicate the event (same time, "(copy)" suffix)
//	W           duplicate into the same slot next week
//	u           undo the last create / edit / delete / quick action
//
// The nudge/resize/day-move keys are deliberately NOT immediate: they only
// change what the view shows until `s` commits them (see pending.go). Anything
// that creates or deletes (D / W / X) still acts immediately, since there is no
// half-applied state to render for those.
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

// duplicateEventCmd copies the selected event, optionally offset by `offset`
// (used for "same slot next week"). The copy keeps attendees but never mails
// them — a duplicate is a draft until you explicitly edit and notify.
func (m model) duplicateEventCmd(ev *Event, offset time.Duration, label string) tea.Cmd {
	if ev == nil || ev.ID == "" {
		return nil
	}
	cal := m.calendar
	title := ev.Title
	if offset == 0 {
		title = strings.TrimSpace(ev.Title) + " (copy)"
	}
	// All-day events carry their date in StartDate/EndDate (StartAt/EndAt are
	// zero); shift the date fields and flag the copy as all-day so the
	// API payload uses `date` instead of `dateTime`. Timed events shift
	// the absolute StartAt/EndAt as before.
	allDay := ev.AllDay()
	start := ev.StartAt
	end := ev.EndAt
	if allDay {
		start = ev.StartDate.Add(offset)
		end = ev.EndDate.Add(offset)
	} else {
		start = start.Add(offset)
		end = end.Add(offset)
	}
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
			AllDay:      allDay,
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

	// `s` / `esc` only belong to quick actions while a change is staged;
	// otherwise esc must stay available for whatever else uses it.
	if m.pending != nil {
		switch key {
		case "s":
			if m.pending.saving {
				m.status = "already saving~"
				return m, nil, true
			}
			label := m.pending.label()
			title := m.pending.title
			// Keep the staged change until the patch is confirmed, so a failed
			// save is retryable rather than silently lost.
			cmd := m.commitPendingCmd()
			m.status = "saving " + label + " on \"" + title + "\"~"
			return m, cmd, true
		case "esc":
			if m.pending.saving {
				m.status = "save in flight — can't discard now"
				return m, nil, true
			}
			m.status = m.discardPending()
			return m, nil, true
		}
	} else if key == "s" {
		m.status = "nothing staged to save (use )( }{ >< to adjust the time first)"
		return m, nil, true
	}

	ev := m.selectedEvent()
	if ev == nil || ev.ID == "" {
		return m, nil, false
	}

	// Nudges/resizes/day-moves stage a change; they no longer patch Google.
	switch key {
	case ")":
		m.status = m.stagePending(ev, nudgeStep, nudgeStep)
		return m, nil, true
	case "(":
		m.status = m.stagePending(ev, -nudgeStep, -nudgeStep)
		return m, nil, true
	case "}":
		m.status = m.stagePending(ev, 0, nudgeStep)
		return m, nil, true
	case "{":
		m.status = m.stagePending(ev, 0, -nudgeStep)
		return m, nil, true
	case ">":
		m.status = m.stagePending(ev, 24*time.Hour, 24*time.Hour)
		return m, nil, true
	case "<":
		m.status = m.stagePending(ev, -24*time.Hour, -24*time.Hour)
		return m, nil, true
	case "D":
		m.status = "duplicating~"
		return m, m.duplicateEventCmd(ev, 0, "duplicated \""+ev.Title+"\""), true
	case "W":
		m.status = "copying to next week~"
		return m, m.duplicateEventCmd(ev, 7*24*time.Hour, "copied \""+ev.Title+"\" to next week"), true
	}
	return m, nil, false
}
