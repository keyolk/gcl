package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// calendarDisplayName renders the currently-viewed calendar in a human-friendly
// way: personal emails become "Jace Son" (title-cased local part), group ids
// resolve to their calendarList name, and aliases keep their display name.
func (m model) viewCreate() string {
	c := m.create
	w := min(max(52, m.width*3/5), max(40, m.width-4))
	inner := max(16, w-4)
	stepNames := []string{"Title", "Date", "Start", "Duration", "Location", "Repeat", "Description", "Attendees", "Confirm"}
	stepName := stepNames[min(int(c.step), len(stepNames)-1)]

	var lines []string
	formTitle := "New event"
	if c.editing {
		formTitle = "Edit event"
	}
	lines = append(lines, sectionTitleStyle.Render(fmt.Sprintf(" %s | %s ", formTitle, stepName)))
	// The form writes to c.calendar when editing an overlay row, so the header
	// has to name that calendar rather than the one currently open.
	formCal := m.calendarDisplayName()
	if c.editing && c.calendar != "" {
		formCal = displayNameForCalendar(c.calendar)
	}
	if m.overlay.on() {
		// The header reads "3 calendars" during an overlay, so the form is the
		// only place that can say which single one this write lands on.
		lines = append(lines, mutedStyle.Render("on ")+errorStyle.Render(formCal))
	} else {
		lines = append(lines, mutedStyle.Render("on "+formCal))
	}
	// Onboarding hint: show the shorthand summary once, right after the form
	// opens, so the user sees what they can type before touching a field. It
	// disappears as soon as the title is filled (the per-field inline hints
	// take over from there).
	if !c.editing && strings.TrimSpace(c.title) == "" {
		lines = append(lines, mutedStyle.Render("  date: today · tmr · +3d · 7/20   start: 3pm · 1530 · 15시   dur: 1h30m · 90m"))
		lines = append(lines, mutedStyle.Render("  repeat: weekly x4   location: Room A   notes: agenda body"))
		lines = append(lines, "")
	}
	lines = append(lines, "")

	// field renders one form row. When the value is empty and the field is not
	// focused, `placeholder` is shown muted (an example of what to type) so an
	// empty form isn't a row of dashes with no clue what goes there. When the field
	// is focused, the edit cursor shows instead.
	field := func(label, val, placeholder string, active bool) string {
		if val == "" {
			if active {
				val = "|"
			} else {
				return mutedStyle.Render(fmt.Sprintf("%-10s %s", label, placeholder))
			}
		}
		line := fmt.Sprintf("%-10s %s", label, truncate(val, max(4, inner-lipgloss.Width(label)-3)))
		if active {
			if c.editingField {
				return selectedStyle.Width(inner).Render(line + "|")
			}
			return selectedRowStyle.Render("▸ " + line)
		}
		return line
	}

	lines = append(lines, field("Title", c.title, "e.g. 1:1 with Gavin", c.step == stepTitle))
	lines = append(lines, field("Date", c.date, "today · tmr · +3d · 7/20", c.step == stepDate))
	lines = append(lines, field("Start", c.start, "3pm · 1530 · 15시", c.step == stepStart))
	lines = append(lines, field("Duration", c.durationStr+"m", "30 · 1h30m · 90m", c.step == stepDuration))
	lines = append(lines, field("Location", c.location, "Room A / Zoom link", c.step == stepLocation))
	lines = append(lines, field("Repeat", c.repeat, "daily · weekly x4 · 매주", c.step == stepRepeat))
	lines = append(lines, field("Notes", c.description, "agenda body / meeting notes", c.step == stepDescription))

	// Inline hint for the focused field: shows what shorthand is accepted, so
	// the flexible parsers are discoverable instead of hidden.
	if hint := fieldHint(c.step); hint != "" {
		lines = append(lines, mutedStyle.Render("  "+hint))
	}

	// Location suggestions from events already loaded — recurring rooms and
	// places don't have to be retyped.
	if c.step == stepLocation && c.editingField {
		cands := filterPickerItems(c.locCands, c.location)
		for i := 0; i < min(len(cands), 4); i++ {
			line := "  " + truncate(cands[i].label, max(4, inner-4))
			if i == c.locCandidx {
				lines = append(lines, selectedStyle.Width(inner-2).Render(line))
			} else {
				lines = append(lines, mutedStyle.Render(line))
			}
		}
		if len(cands) > 0 {
			lines = append(lines, mutedStyle.Render("  ctrl+n/ctrl+p pick · ctrl+y accept"))
		}
	}

	// Attendees block - rendered through the same field() helper so its label
	// lines up with Title/Date/Start/Duration. field() appends the edit cursor.
	atts := sortedKeys(c.selected)
	attVal := fmt.Sprintf("(%d)", len(atts))
	if c.step == stepAttendees && c.editingField {
		attVal = fmt.Sprintf("(%d) | filter: %s", len(atts), c.attInput)
	}
	lines = append(lines, field("Attendees", attVal, "a@example.com …", c.step == stepAttendees))
	if c.step == stepAttendees {
		cands := filterPickerItems(c.attCands, c.attInput)
		rows := max(1, min(6, len(cands)))
		start := 0
		if c.attCandidx >= rows {
			start = c.attCandidx - rows + 1
		}
		for i := start; i < min(len(cands), start+rows); i++ {
			mark := "  "
			if c.selected[cands[i].value] {
				mark = "* "
			}
			line := mark + truncate(cands[i].label, max(4, inner-4))
			if i == c.attCandidx && c.editingField {
				lines = append(lines, selectedStyle.Width(inner-2).Render(line))
			} else {
				lines = append(lines, line)
			}
		}
	} else if len(atts) > 0 {
		for i, a := range atts {
			if i >= 4 {
				lines = append(lines, mutedStyle.Render(fmt.Sprintf("  ~ +%d more", len(atts)-4)))
				break
			}
			lines = append(lines, "  | "+truncate(a, inner-4))
		}
	}

	lines = append(lines, "")
	lines = append(lines, pillStyle.Render(strings.TrimSpace(c.title)))
	// Live preview resolved through the flexible parsers, so typing "tmr" /
	// "3pm" / "1h30m" immediately shows the concrete day, time, and end time it
	// will create.
	lines = append(lines, mutedStyle.Render(c.previewLine(m.tz(), m.tzLabel(), time.Now())))
	if loc := strings.TrimSpace(c.location); loc != "" {
		lines = append(lines, mutedStyle.Render("@ "+truncate(loc, max(4, inner-2))))
	}
	if rules := c.recurrenceRules(); len(rules) > 0 {
		lines = append(lines, mutedStyle.Render("repeats "+describeRepeat(c.repeat)))
	}
	// Warn while typing, not only at the confirm step: a date that resolved to
	// last week is easiest to fix in the field it came from, and the preview
	// line alone does not make "Aug 01" register as wrong in September.
	for _, w := range m.submitWarnings(c, time.Now()) {
		lines = append(lines, warnStyle.Render("⚠ "+truncate(w, max(4, inner-4))))
	}
	if n := c.notifiesAttendees(); n > 0 {
		// Editing mails the whole resulting attendee list plus anyone removed,
		// not just the people added — notifiesAttendees is what knows that.
		verb := "invitation emails will be sent to"
		if c.editing {
			verb = "update emails will be sent to"
		}
		lines = append(lines, errorStyle.Render(fmt.Sprintf("⚠ %s %d %s", verb, n, plural(n, "person", "people"))))
	}
	if c.submitting {
		lines = append(lines, statusStyle.Render("creating~"))
	} else {
		if c.editingField {
			lines = append(lines, mutedStyle.Render("ESC blur field | Enter save | Tab/S-Tab next field | Space toggle attendee"))
		} else {
			lines = append(lines, mutedStyle.Render("j/k move field | Enter/i edit field | Enter on any field saves | ESC close"))
		}
	}
	if c.err != "" {
		lines = append(lines, errorStyle.Render("x "+c.err))
	}

	h := min(len(lines)+2, max(8, m.height-4))
	return modalStyle.Width(w).Height(h).Render(strings.Join(lines, "\n"))
}
