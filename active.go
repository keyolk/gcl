package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// "Active now" — every event that straddles the present moment.
//
// A maintenance-window calendar answers one question far more often than any
// other: *what is in effect right now?* The agenda cannot answer it, because a
// window that started three days ago and ends tomorrow sits far above the "now"
// divider, scrolled out of sight, indistinguishable from something long over.
// Multi-day windows on several calendars make that worse: the ones that matter
// are exactly the ones the agenda buries.
//
// `a` collects them into one panel, sorted by how soon they end (the ones about
// to lapse first). The header also carries a live count so an active window is
// visible without opening anything.

// activeSpan resolves the wall-clock interval an event occupies, normalizing
// all-day events (which carry dates, not times) into a real interval.
//
// Google's all-day `end` date is exclusive; the parsed EndDate may also be zero
// when it came from a source that did not supply one, so fall back to a
// single-day span.
func activeSpan(ev *Event) (start, end time.Time) {
	if !ev.AllDay() && !ev.StartAt.IsZero() {
		end = ev.EndAt
		if end.IsZero() {
			end = ev.StartAt
		}
		return ev.StartAt, end
	}
	start = ev.StartDate
	end = ev.EndDate
	if end.IsZero() || !end.After(start) {
		end = start.AddDate(0, 0, 1)
	}
	return start, end
}

// isActiveAt reports whether ev is in effect at t (start inclusive, end
// exclusive, so an event does not linger for a frame after it ends).
func isActiveAt(ev *Event, t time.Time) bool {
	start, end := activeSpan(ev)
	if start.IsZero() {
		return false
	}
	return !t.Before(start) && t.Before(end)
}

// activeEvents returns every loaded event in effect at t, soonest-ending first.
// Ties break on start time then title so the order is stable across refreshes.
func (m model) activeEvents(t time.Time) []*Event {
	var out []*Event
	for i := range m.events {
		if isActiveAt(&m.events[i], t) {
			out = append(out, &m.events[i])
		}
	}
	sortByEndThenStart(out)
	return out
}

func sortByEndThenStart(evs []*Event) {
	// Insertion sort: the active set is tiny (a handful of windows), and this
	// keeps the comparison inline and obvious.
	for i := 1; i < len(evs); i++ {
		for j := i; j > 0 && activeLess(evs[j], evs[j-1]); j-- {
			evs[j], evs[j-1] = evs[j-1], evs[j]
		}
	}
}

func activeLess(a, b *Event) bool {
	_, aEnd := activeSpan(a)
	_, bEnd := activeSpan(b)
	if !aEnd.Equal(bEnd) {
		return aEnd.Before(bEnd)
	}
	aStart, _ := activeSpan(a)
	bStart, _ := activeSpan(b)
	if !aStart.Equal(bStart) {
		return aStart.Before(bStart)
	}
	return a.Title < b.Title
}

// remainingLabel renders how much of an active event is left, e.g. "ends in
// 25m", "ends in 2d3h". Past the end it reports "ended".
func remainingLabel(ev *Event, now time.Time) string {
	_, end := activeSpan(ev)
	left := end.Sub(now)
	if left <= 0 {
		return "ended"
	}
	return "ends in " + humanDuration(left.Round(time.Minute))
}

// elapsedLabel renders how long an active event has been running, e.g. "started
// 3d ago". Sub-minute ages read as "just started" rather than "0m ago".
func elapsedLabel(ev *Event, now time.Time) string {
	start, _ := activeSpan(ev)
	in := now.Sub(start)
	if in < time.Minute {
		return "just started"
	}
	return "started " + humanDuration(in.Round(time.Minute)) + " ago"
}

// activeSpanLabel describes the whole span in the current timezone. Multi-day
// spans get dates on both ends; same-day ones stay compact.
func (m model) activeSpanLabel(ev *Event) string {
	loc := m.tz()
	start, end := activeSpan(ev)
	s := start.In(loc)
	e := end.In(loc)
	if ev.AllDay() {
		// All-day end dates are exclusive; show the last day the window covers.
		last := e.AddDate(0, 0, -1)
		if sameDay(s, last) {
			return s.Format("Jan 02") + " all-day"
		}
		return s.Format("Jan 02") + " -> " + last.Format("Jan 02") + " all-day"
	}
	if sameDay(s, e) {
		return s.Format("Jan 02 15:04") + "-" + e.Format("15:04")
	}
	return s.Format("Jan 02 15:04") + " -> " + e.Format("Jan 02 15:04")
}

// activeCountLabel is the header pill: how many events are in effect right now.
// Empty when nothing is active, so the header stays quiet on an idle calendar.
func (m model) activeCountLabel(now time.Time) string {
	n := len(m.activeEvents(now))
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("🔴 %d active", n)
}

// activePanelFocused reports whether keystrokes should drive the panel's cursor.
// The panel being open is not enough — focus must have been moved into it with
// `tab`, which is what keeps `a` a non-disruptive toggle.
func (m model) activePanelFocused() bool {
	return m.activeOpen && m.focusPane == focusActive
}

// clampActiveIndex keeps the panel cursor inside the current active set, which
// shrinks on its own as windows lapse.
func (m *model) clampActiveIndex() {
	n := len(m.activeEvents(time.Now()))
	if n == 0 {
		m.activeIndex = 0
		return
	}
	m.activeIndex = max(0, min(m.activeIndex, n-1))
}

// syncSelectionToActive points the schedule's selection at the panel's cursor,
// so E/X/L/A/o and the quick actions act on the highlighted window without the
// panel needing to intercept them. Called on every panel cursor move: the
// selection is the single source of truth for "the current event", and keeping
// it in step is what lets the panel stay non-modal.
func (m *model) syncSelectionToActive() {
	evs := m.activeEvents(time.Now())
	if len(evs) == 0 {
		return
	}
	target := evs[max(0, min(m.activeIndex, len(evs)-1))]
	for i := range m.events {
		if &m.events[i] == target {
			m.selected = i
			break
		}
	}
	m.anchor = target.StartDate
	if m.view != viewList {
		m.gridDetail = 0
		m.keepGridStable()
	}
}

// activePanelHeight is how many body rows the docked panel takes. It is capped
// at a third of the body so the schedule it sits above stays usable — the panel
// is a companion view, not a replacement for the agenda.
func (m model) activePanelHeight(bodyHeight int) int {
	if !m.activeOpen {
		return 0
	}
	evs := m.activeEvents(time.Now())
	// title + one row per event (+ a meta row each) + footer hint.
	want := 2 + len(evs)*2
	if len(evs) == 0 {
		want = 3
	}
	return min(want, max(4, bodyHeight/3))
}

// viewActivePanel renders the docked "active now" list: one block per in-effect
// event with how much is left, its span, and where it lives. Unlike an overlay,
// it occupies its own rows and never covers the schedule.
func (m model) viewActivePanel(width, height int) string {
	now := time.Now()
	evs := m.activeEvents(now)
	inner := max(20, width-3)
	focused := m.activePanelFocused()

	title := fmt.Sprintf(" ◉ Active now (%d) · %s ", len(evs), now.In(m.tz()).Format("15:04"))
	var lines []string
	if focused {
		lines = append(lines, sectionTitleStyle.Render(title)+" "+mutedStyle.Render("j/k move · Enter jump · esc back to schedule"))
	} else {
		// Unfocused, the panel advertises how to get into it — otherwise `tab`
		// is as undiscoverable as `e` used to be.
		lines = append(lines, activePillStyle.Render(title)+" "+mutedStyle.Render("tab to focus"))
	}
	if len(evs) == 0 {
		lines = append(lines, mutedStyle.Render("  nothing in effect right now"))
		lines = append(lines, mutedStyle.Render("  (a span only counts when it covers this moment)"))
		return strings.Join(lines, "\n")
	}

	idx := max(0, min(m.activeIndex, len(evs)-1))
	// Scroll the list so the cursor stays visible when more windows are active
	// than the capped height can show.
	rows := max(1, height-1)
	perEvent := 2
	visible := max(1, rows/perEvent)
	start := 0
	if idx >= visible {
		start = idx - visible + 1
	}
	for i := start; i < min(len(evs), start+visible); i++ {
		ev := evs[i]
		// The countdown leads the row: "which of these lapses first" is the
		// question the panel exists to answer, so it must never be the part
		// that truncates.
		remaining := remainingLabel(ev, now)
		title := truncate(ev.Title, max(10, inner-lipgloss.Width(remaining)-8))
		row := remaining + "  ·  " + title
		switch {
		case focused && i == idx:
			lines = append(lines, selectedRowStyle.Render("▸ "+truncate(row, inner-2)))
		case i == idx:
			// Cursor position is still shown while unfocused (dimmed), so
			// tabbing in doesn't feel like it jumps somewhere unexpected.
			lines = append(lines, mutedStyle.Render("▸ "+truncate(row, inner-2)))
		default:
			lines = append(lines, "  "+truncate(row, inner-2))
		}
		meta := m.activeSpanLabel(ev)
		if where := activeWhere(ev); where != "" {
			meta += "  ·  " + where
		}
		lines = append(lines, mutedStyle.Render("    "+truncate(meta, inner-4)))
	}
	if rest := len(evs) - (start + visible); rest > 0 {
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("    ~ +%d more active", rest)))
	}
	return strings.Join(lines, "\n")
}

// activeWhere is the event's location/room, falling back to its calendar — the
// "where does this apply" column of the panel.
func activeWhere(ev *Event) string {
	where := locationWithoutRooms(ev)
	if len(ev.Rooms) > 0 {
		if where != "" {
			where += " / "
		}
		where += strings.Join(ev.Rooms, ", ")
	}
	if where == "" && ev.Calendar != "" {
		where = displayNameForCalendar(ev.Calendar)
	}
	return where
}
