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

// duplicateSpan computes where a duplicate lands. Split out of
// duplicateEventCmd so it is testable without an API call — the DST behaviour
// below is exactly the kind of thing that needs a test.
//
// The offset is applied as whole CALENDAR days in the display timezone. Adding
// 7*24h instead would land "same slot next week" an hour off across a DST
// boundary, which is invisible until someone misses the meeting.
//
// All-day events carry their date in StartDate/EndDate (StartAt/EndAt are
// zero), so those are the fields shifted for them.
func duplicateSpan(ev *Event, days int, tz *time.Location) (start, end time.Time) {
	shift := func(t time.Time) time.Time {
		if days == 0 || t.IsZero() {
			return t
		}
		return t.In(tz).AddDate(0, 0, days)
	}
	if ev.AllDay() {
		return shift(ev.StartDate), shift(ev.EndDate)
	}
	return shift(ev.StartAt), shift(ev.EndAt)
}

// duplicateEventCmd copies the selected event, optionally offset by `days`
// whole calendar days (used for "same slot next week"). The copy keeps
// attendees but never mails them — a duplicate is a draft until you explicitly
// edit and notify.
//
// The offset is in DAYS rather than a duration on purpose: adding 7*24h across
// a DST boundary lands the copy an hour off the slot it was supposed to mirror.
func (m model) duplicateEventCmd(ev *Event, days int, label string) tea.Cmd {
	if ev == nil || ev.ID == "" {
		return nil
	}
	// A duplicate lands on the calendar the source event lives on, which in an
	// overlay is not necessarily the one currently open.
	cal := m.eventCalendar(ev)
	title := ev.Title
	if days == 0 {
		title = strings.TrimSpace(ev.Title) + " (copy)"
	}
	allDay := ev.AllDay()
	start, end := duplicateSpan(ev, days, m.tz())
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

// runQuick runs the quick actions (undo, save/discard of a staged change, the
// nudges and duplicates). ok is false for any other action, so the caller
// continues its own dispatch.
func (m model) runQuick(action string) (tea.Model, tea.Cmd, bool) {
	switch action {
	case "undo":
		// `u` works even with no event selected.
		if m.lastUndo == nil {
			m.say("nothing to undo")
			return m, nil, true
		}
		cmd := m.running("undoing~", m.undoCmd())
		return m, cmd, true
	case "save":
		if m.pending == nil {
			m.say("nothing staged to save (use )( }{ >< to adjust the time first)")
			return m, nil, true
		}
		if m.pending.saving {
			m.say("already saving~")
			return m, nil, true
		}
		label := m.pending.label()
		// Keep the staged change until the patch is confirmed, so a failed
		// save is retryable rather than silently lost.
		n := len(m.pending.attendees)
		cmd := m.commitPendingCmd()
		if n > 0 {
			// Quick saves pass Notify:false. Moving a meeting without telling
			// the people in it is a real hazard, so say it plainly instead of
			// leaving it to the source comment.
			m.warn(fmt.Sprintf("saving %s~ · %d %s NOT notified (%s to edit and notify)",
				label, n, plural(n, "attendee", "attendees"), m.keyName(ctxSchedule, "edit")))
		} else {
			cmd = m.running("saving "+label+"~", cmd)
		}
		return m, cmd, true
	case "discard":
		// Only bound while a change is staged (ctxPending), so esc stays free
		// for whatever else uses it otherwise.
		if m.pending == nil {
			return m, nil, false
		}
		if m.pending.saving {
			m.warn("save in flight — can't discard now")
			return m, nil, true
		}
		m.say(m.discardPending())
		return m, nil, true
	}

	ev := m.selectedEvent()
	if ev == nil || ev.ID == "" {
		return m, nil, false
	}
	// Nudges/resizes/day-moves stage a change; they do not patch Google.
	switch action {
	case "nudge.later":
		m.say(m.stagePending(ev, nudgeStep, nudgeStep, 0))
	case "nudge.earlier":
		m.say(m.stagePending(ev, -nudgeStep, -nudgeStep, 0))
	case "nudge.longer":
		m.say(m.stagePending(ev, 0, nudgeStep, 0))
	case "nudge.shorter":
		m.say(m.stagePending(ev, 0, -nudgeStep, 0))
	case "nudge.nextday":
		m.say(m.stagePending(ev, 0, 0, 1))
	case "nudge.prevday":
		m.say(m.stagePending(ev, 0, 0, -1))
	case "dup":
		cmd := m.running("duplicating~", m.duplicateEventCmd(ev, 0, "duplicated \""+ev.Title+"\""))
		return m, cmd, true
	case "dup.week":
		cmd := m.running("copying to next week~", m.duplicateEventCmd(ev, 7, "copied \""+ev.Title+"\" to next week"))
		return m, cmd, true
	default:
		return m, nil, false
	}
	return m, nil, true
}
