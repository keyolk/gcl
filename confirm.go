package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// What the confirm step actually confirms.
//
// The create/edit form routes through a confirmation overlay so a mistyped
// submit is not already live on other people's calendars. That only works if
// the overlay answers the question the user is about to answer: *is this
// right?* Restating the whole form does not — the eye slides over eight fields
// that all look plausible.
//
// So the two cases are shown differently:
//
//   - creating: the resolved date/time, the attendee count, and anything
//     irreversible (invitations, recurrence) — the things that are hard to see
//     from the shorthand still sitting in the fields.
//   - editing: only what CHANGES, old → new. A rename shows one line. Fields
//     you did not touch are not mentioned at all, because they are not what a
//     mis-edit gets wrong.

// fieldChange is one before/after pair for the edit confirmation.
type fieldChange struct {
	label string
	from  string
	to    string
	// risky marks changes that reach other people (time moves, attendee
	// changes), so the overlay can lead with them.
	risky bool
}

// changes computes what an edit would actually alter, in form order. Returns
// nil for a create (nothing to diff against) or when nothing changed.
//
// The date/start/duration comparison uses the NORMALIZED values: the form may
// still hold "tmr" or "3pm" as typed, and comparing that to a stored "15:00"
// would report a change that isn't one.
func (c *createState) changes(now time.Time) []fieldChange {
	if c.orig == nil {
		return nil
	}
	o := c.orig
	var out []fieldChange

	if s := strings.TrimSpace(c.title); s != o.title {
		out = append(out, fieldChange{label: "Title", from: o.title, to: s})
	}

	// Time is one concept to the user even though it is three fields, and a
	// move is the change most likely to disrupt other people — so it is
	// reported as a single before/after span rather than three separate lines.
	newDate, dErr := parseFlexibleDate(c.date, now)
	newStart, sErr := parseFlexibleTime(c.start)
	newDur, durErr := parseDurationMinutes(c.durationStr)
	if dErr == nil && sErr == nil && durErr == nil {
		oldSpan := spanLabel(o.date, o.start, o.duration)
		newSpan := spanLabel(newDate.Format("2006-01-02"), newStart, newDur)
		if oldSpan != newSpan {
			out = append(out, fieldChange{label: "Time", from: oldSpan, to: newSpan, risky: true})
		}
	}

	if s := strings.TrimSpace(c.location); s != strings.TrimSpace(o.location) {
		out = append(out, fieldChange{label: "Location", from: orNone(o.location), to: orNone(s)})
	}
	if s := strings.TrimSpace(c.description); s != strings.TrimSpace(o.description) {
		out = append(out, fieldChange{
			label: "Notes",
			from:  orNone(truncate(oneLine(o.description), 28)),
			to:    orNone(truncate(oneLine(s), 28)),
		})
	}

	// Attendees are compared as sets: who was added and who was dropped is the
	// useful readout, not "3 → 4". Dropping someone silently un-invites them.
	added, removed := diffStringSets(o.attendees, sortedKeys(c.selected))
	if len(added) > 0 || len(removed) > 0 {
		var parts []string
		if len(added) > 0 {
			parts = append(parts, "+"+shortNames(added))
		}
		if len(removed) > 0 {
			parts = append(parts, "-"+shortNames(removed))
		}
		out = append(out, fieldChange{
			label: "Attendees",
			from:  fmt.Sprintf("%d", len(o.attendees)),
			to:    fmt.Sprintf("%d  (%s)", len(c.selected), strings.Join(parts, ", ")),
			risky: true,
		})
	}
	return out
}

// spanLabel renders a date/start/duration triple as one readable span, e.g.
// "Thu Sep 04 10:00-11:00". Used on both sides of the time diff so a change is
// visible as a difference in the text, not as three numbers to compare.
func spanLabel(date, start string, durMins int) string {
	d, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		return date + " " + start
	}
	t, err := time.Parse("15:04", start)
	if err != nil {
		return d.Format("Mon Jan 02") + " " + start
	}
	s := time.Date(d.Year(), d.Month(), d.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
	e := s.Add(time.Duration(durMins) * time.Minute)
	if e.Day() != s.Day() {
		return fmt.Sprintf("%s %s-%s(+1d)", s.Format("Mon Jan 02"), s.Format("15:04"), e.Format("15:04"))
	}
	return fmt.Sprintf("%s %s-%s", s.Format("Mon Jan 02"), s.Format("15:04"), e.Format("15:04"))
}

// diffStringSets returns what is in b but not a (added) and in a but not b
// (removed). Both inputs are expected sorted; comparison is case-insensitive
// because Google echoes addresses back in whatever case they were entered.
func diffStringSets(a, b []string) (added, removed []string) {
	inA := make(map[string]bool, len(a))
	for _, s := range a {
		inA[strings.ToLower(s)] = true
	}
	inB := make(map[string]bool, len(b))
	for _, s := range b {
		inB[strings.ToLower(s)] = true
	}
	for _, s := range b {
		if !inA[strings.ToLower(s)] {
			added = append(added, s)
		}
	}
	for _, s := range a {
		if !inB[strings.ToLower(s)] {
			removed = append(removed, s)
		}
	}
	return added, removed
}

// orNone renders an empty value as "(none)" so a cleared field reads as
// deliberate rather than as a rendering gap.
func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}

// oneLine flattens newlines so a multi-line description fits a diff row.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

// notifiesAttendees reports how many people Google will email on submit.
// Editing notifies the event's whole attendee list, not just the people added —
// which is exactly the surprise worth spelling out before the send.
func (c *createState) notifiesAttendees() int {
	if !c.editing || c.orig == nil {
		return len(c.selected)
	}
	// A patch mails everyone on the resulting event, plus anyone removed (they
	// get a cancellation). Report the union so the count is never an undercount.
	_, removed := diffStringSets(c.orig.attendees, sortedKeys(c.selected))
	return len(c.selected) + len(removed)
}

// submitWarnings lists things that are legal but are usually a mistake, in the
// order they matter. These are WARNINGS, not validation errors: each has a real
// use (logging a retro after the fact, an all-day-long workshop, an open-ended
// standup), so blocking them would be wrong. Showing them at the confirm step
// is what turns a slip into a caught slip.
func (m model) submitWarnings(c createState, now time.Time) []string {
	var out []string

	d, dErr := parseFlexibleDate(c.date, now)
	hm, sErr := parseFlexibleTime(c.start)
	mins, durErr := parseDurationMinutes(c.durationStr)
	if dErr == nil && sErr == nil && durErr == nil {
		loc := m.tz()
		if t, err := time.Parse("15:04", hm); err == nil {
			start := time.Date(d.Year(), d.Month(), d.Day(), t.Hour(), t.Minute(), 0, 0, loc)
			// A start in the past is the single most common slip: "-3d" for
			// "+3d", or a month that has already gone by. Nothing rejects it
			// today, so an event silently lands in last week.
			if start.Before(now) {
				out = append(out, fmt.Sprintf("starts in the PAST (%s ago)",
					humanDuration(now.Sub(start).Round(time.Minute))))
			}
		}
		// A duration typed as "8" reading as 8 minutes, or "8h" meant as 8
		// hours, both look plausible in the field. Flag the outliers.
		switch {
		case mins >= 8*60:
			out = append(out, fmt.Sprintf("runs for %s — is that right?", humanMinutes(mins)))
		case mins < 5:
			out = append(out, fmt.Sprintf("only %s long", humanMinutes(mins)))
		}
	}
	return out
}

// viewConfirmSubmit renders the last stop before anything reaches Google.
//
// It is the only screen standing between a typo and other people's calendars,
// so it must be readable at a glance and must never be empty: an earlier
// version had its emptiness check inverted and showed "(no event selected)" for
// every submit, which quietly turned the confirmation into a second Enter.
func (m model) viewConfirmSubmit() string {
	c := m.create
	now := time.Now()
	verb := "Create"
	if c.editing {
		verb = "Save changes to"
	}
	// A confirmation whose values are truncated cannot be confirmed. popupSize
	// is tuned for narrow fuzzy pickers; the before → after rows need real
	// width, so this overlay asks for its own box (still bounded by the pane).
	_, h := m.popupSize(10)
	w := min(max(62, m.preferredOverlayWidth()), max(40, m.width-4))
	inner := max(20, w-4)

	var lines []string
	lines = append(lines, sectionTitleStyle.Render(fmt.Sprintf(" %s event? ", verb)))
	lines = append(lines, "")
	lines = append(lines, pillStyle.Render(truncate(orUntitled(c.title), inner-2)))
	// In an overlay the edited row may belong to someone else. Which calendar
	// the write lands on is then part of what is being confirmed.
	if m.overlay.on() {
		target := m.calendarDisplayName()
		if c.editing && c.calendar != "" {
			target = displayNameForCalendar(c.calendar)
		}
		lines = append(lines, mutedStyle.Render("on ")+errorStyle.Render(truncate(target, inner-6)))
	}

	if c.editing {
		changes := c.changes(now)
		if len(changes) == 0 {
			// Submitting an unchanged form would send invitation mail for
			// nothing. Say so rather than letting it look like a normal save.
			lines = append(lines, "")
			lines = append(lines, mutedStyle.Render("nothing changed — n/ESC goes back, y saves anyway"))
		} else {
			lines = append(lines, mutedStyle.Render(fmt.Sprintf("%d change(s):", len(changes))))
			for _, ch := range changes {
				lines = append(lines, m.changeLine(ch, inner))
			}
		}
	} else {
		lines = append(lines, mutedStyle.Render(c.previewLine(m.tz(), m.tzLabel(), now)))
		if loc := strings.TrimSpace(c.location); loc != "" {
			lines = append(lines, mutedStyle.Render("@ "+truncate(loc, inner-4)))
		}
		if rules := c.recurrenceRules(); len(rules) > 0 {
			// Recurrence is the field where a mistake multiplies: "weekly" with
			// no count creates an event that never ends.
			warn := "repeats " + describeRepeat(c.repeat)
			if _, count := splitRepeat(c.repeat); count == 0 {
				warn += " — forever (add \"x4\" to limit it)"
			}
			lines = append(lines, errorStyle.Render("↻ "+truncate(warn, inner-4)))
		}
	}

	// Warnings apply to both paths: an edit can move an event into the past just
	// as easily as a create can put it there.
	for _, w := range m.submitWarnings(c, now) {
		lines = append(lines, warnStyle.Render("⚠ "+truncate(w, inner-4)))
	}

	if n := c.notifiesAttendees(); n > 0 {
		lines = append(lines, "")
		lines = append(lines, errorStyle.Render(fmt.Sprintf("✉ Google will email %d %s",
			n, plural(n, "person", "people"))))
	}

	lines = append(lines, "")
	lines = append(lines, mutedStyle.Render(fmt.Sprintf("y/Enter %s  |  n/ESC back to form",
		strings.ToLower(verb))))
	return modalStyle.Width(w).Height(min(max(h, len(lines)+2), max(8, m.height-4))).
		Render(strings.Join(lines, "\n"))
}

// changeLine renders one before → after row. Changes that reach other people
// are marked, so they are not read at the same weight as a typo fix.
//
// A truncated value cannot be confirmed, so when the pair does not fit on one
// line the row stacks instead of shortening either side. Two readable lines
// beat one line reading "Fri Sep 04 10:00-11:~".
func (m model) changeLine(ch fieldChange, inner int) string {
	marker := "  "
	if ch.risky {
		marker = errorStyle.Render("! ")
	}
	label := fmt.Sprintf("%-9s ", ch.label)
	// 2 (marker) + label + 3 (" → ") is the fixed overhead of the row.
	budget := max(10, inner-lipgloss.Width(label)-5)
	if lipgloss.Width(ch.from)+lipgloss.Width(ch.to) <= budget {
		return marker + label + mutedStyle.Render(ch.from) + " → " + selectedRowStyle.Render(ch.to)
	}
	// Stacked: old on the label row, new indented under it with the arrow.
	wide := max(10, inner-lipgloss.Width(label)-2)
	return marker + label + mutedStyle.Render(truncate(ch.from, wide)) + "\n" +
		strings.Repeat(" ", 2+lipgloss.Width(label)-2) + "→ " +
		selectedRowStyle.Render(truncate(ch.to, wide))
}

// orUntitled keeps the title pill from rendering as an empty box.
func orUntitled(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(untitled)"
	}
	return strings.TrimSpace(s)
}

// plural picks the right noun for a count.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
