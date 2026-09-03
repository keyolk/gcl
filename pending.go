package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Staged (unsaved) time adjustments.
//
// The nudge/resize/day-move keys — ) ( } { > < — used to patch Google Calendar
// on every single keypress. That turned one mental adjustment ("push this an
// hour later") into four API round trips, and a mis-key was already live on
// other people's calendars before you saw the result.
//
// They now only *stage* the change: the schedule renders the new time with an
// `!` marker, the hint bar turns into an UNSAVED banner, and nothing reaches
// Google until `s`. `esc` throws the staged change away.
//
// Only one event can carry a staged change at a time — nudging a different
// event while one is staged is refused rather than silently dropping the first.
type pendingShift struct {
	calendar string
	eventID  string
	title    string
	// Deltas accumulate against the event's original times, so repeated
	// keypresses compose ( ")))" = +45m ) and collapse into one label and one
	// patch on save.
	startDelta time.Duration
	endDelta   time.Duration
	// dayDelta counts whole CALENDAR days moved with > / <, kept separate from
	// the duration deltas on purpose. Adding 24h across a DST boundary moves a
	// 10:00 meeting to 11:00 — "next day, same time" is calendar arithmetic, not
	// a fixed number of hours, so it has to be applied with AddDate.
	dayDelta int
	// loc is the timezone the day move is evaluated in (the display tz at the
	// time of the nudge). AddDate on a UTC instant would shift the wrong day
	// boundary for a user reading the calendar in KST.
	loc       *time.Location
	origStart time.Time
	origEnd   time.Time
	// patchEvent replaces the whole event, so the fields we are not touching
	// have to be echoed back on commit or they would be cleared.
	location    string
	description string
	attendees   []string
	// saving is true while the patch is in flight. The staged change is kept
	// (not cleared optimistically) so a failed save leaves it recoverable
	// instead of silently discarding work.
	saving bool
}

// start/end apply the staged change to the event's original times: whole days
// first (as calendar days, so the wall-clock time survives a DST boundary),
// then the minute-level deltas.
func (p *pendingShift) start() time.Time { return p.shift(p.origStart, p.startDelta) }
func (p *pendingShift) end() time.Time   { return p.shift(p.origEnd, p.endDelta) }

func (p *pendingShift) shift(t time.Time, delta time.Duration) time.Time {
	if p.dayDelta != 0 {
		loc := p.loc
		if loc == nil {
			loc = time.Local
		}
		t = t.In(loc).AddDate(0, 0, p.dayDelta)
	}
	return t.Add(delta)
}

// empty reports whether the staged deltas cancelled out (nudged back to the
// saved time), in which case there is nothing left to save.
func (p *pendingShift) empty() bool {
	return p.startDelta == 0 && p.endDelta == 0 && p.dayDelta == 0
}

// label summarizes the staged change as a move plus a duration change, e.g.
// "+1d", "+45m", "+15m, 30m longer".
func (p *pendingShift) label() string {
	var parts []string
	if p.dayDelta != 0 {
		parts = append(parts, signedDays(p.dayDelta))
	}
	if p.startDelta != 0 {
		parts = append(parts, signedDuration(p.startDelta))
	}
	if grow := p.endDelta - p.startDelta; grow != 0 {
		word := "longer"
		if grow < 0 {
			word = "shorter"
			grow = -grow
		}
		parts = append(parts, humanDuration(grow)+" "+word)
	}
	if len(parts) == 0 {
		return "no change"
	}
	return strings.Join(parts, ", ")
}

// diffParts returns the saved and staged time spans separately, so the caller can
// join them on one line or stack them when the pane is too narrow. The date is
// repeated on the staged side only when the change actually moves the event to
// another day — otherwise it is noise.
func (p *pendingShift) diffParts(loc *time.Location) (before, after string) {
	from := p.origStart.In(loc)
	to := p.start().In(loc)
	before = from.Format("Jan 02 15:04") + "-" + p.origEnd.In(loc).Format("15:04")
	after = to.Format("15:04") + "-" + p.end().In(loc).Format("15:04")
	if !sameDay(from, to) {
		after = to.Format("Jan 02 15:04") + "-" + p.end().In(loc).Format("15:04")
	}
	return before, after
}

// diffLine is diffParts joined for one-line contexts.
func (p *pendingShift) diffLine(loc *time.Location) string {
	before, after := p.diffParts(loc)
	return before + " -> " + after
}

// signedDays renders a whole-day move: "+1d", "-2d".
func signedDays(n int) string {
	if n < 0 {
		return fmt.Sprintf("-%dd", -n)
	}
	return fmt.Sprintf("+%dd", n)
}

// signedDuration renders a delta with its direction: "+1d", "-15m", "+1d2h".
func signedDuration(d time.Duration) string {
	sign := "+"
	if d < 0 {
		sign = "-"
		d = -d
	}
	return sign + humanDuration(d)
}

// humanDuration renders an unsigned duration compactly, spelling out whole days
// so a next-day move reads as "1d" instead of "24h".
func humanDuration(d time.Duration) string {
	days := int(d / (24 * time.Hour))
	rest := d % (24 * time.Hour)
	if days > 0 {
		if rest == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd%s", days, humanMinutes(int(rest/time.Minute)))
	}
	return humanMinutes(int(d / time.Minute))
}

// stagePending folds another nudge into the staged change for ev and returns the
// status line to show. Rejections (all-day, zero duration, a different event
// already staged) are reported as status text — nothing is sent to Google here.
//
// days is a whole-CALENDAR-day move (> / <), applied separately from the
// minute-level deltas so "next day, same time" survives a DST boundary.
func (m *model) stagePending(ev *Event, startDelta, endDelta time.Duration, days int) string {
	if ev.AllDay() {
		return "all-day events cannot be nudged"
	}
	if m.pending != nil && (m.pending.eventID != ev.ID || m.pending.calendar != m.eventCalendar(ev)) {
		return "unsaved change on \"" + m.pending.title + "\" — s to save or esc to discard first"
	}

	// Copy rather than mutate in place: the previous model value is still
	// referenced by bubbletea until this Update returns.
	var next pendingShift
	if m.pending != nil {
		next = *m.pending
	} else {
		title := strings.TrimSpace(ev.Title)
		if title == "" {
			title = "(untitled)"
		}
		atts := make([]string, 0, len(ev.Attendees))
		for _, a := range ev.Attendees {
			if e := attendeeEmail(a); e != "" {
				atts = append(atts, e)
			}
		}
		next = pendingShift{
			// An overlay row belongs to its own calendar; staging against
			// m.calendar would patch the wrong one on save.
			calendar:    m.eventCalendar(ev),
			eventID:     ev.ID,
			title:       title,
			origStart:   ev.StartAt,
			origEnd:     ev.EndAt,
			location:    ev.Location,
			description: ev.Description,
			attendees:   atts,
			loc:         m.tz(),
		}
	}

	next.startDelta += startDelta
	next.endDelta += endDelta
	next.dayDelta += days
	if !next.end().After(next.start()) {
		return "duration would be zero or negative"
	}
	if next.empty() {
		m.pending = nil
		return "back to the saved time (nothing to save)"
	}
	m.pending = &next
	return "unsaved: " + next.label() + " · s save · esc discard"
}

// discardPending drops the staged change without touching Google.
func (m *model) discardPending() string {
	if m.pending == nil {
		return ""
	}
	label := m.pending.label()
	title := m.pending.title
	m.pending = nil
	return "discarded unsaved " + label + " on \"" + title + "\""
}

// commitPendingCmd writes the staged change to Google in a single patch. The
// undo entry restores the pre-stage times, so `u` still reverses a save.
//
// The staged change stays in the model until the patch is confirmed: if the
// request fails, the amber UNSAVED bar is still there and `s` can retry.
func (m *model) commitPendingCmd() tea.Cmd {
	if m.pending == nil {
		return nil
	}
	// Swap in a copy rather than mutating through the pointer: the previous
	// model value still shares that pendingShift until Update returns.
	p := *m.pending
	p.saving = true
	m.pending = &p
	restore := &undoEntry{
		kind:        undoPatch,
		calendar:    p.calendar,
		eventID:     p.eventID,
		label:       p.title,
		title:       p.title,
		start:       p.origStart,
		end:         p.origEnd,
		location:    p.location,
		description: p.description,
		attendees:   p.attendees,
	}
	label := "saved " + p.label() + " on \"" + p.title + "\""
	return func() tea.Msg {
		if _, err := patchEvent(patchEventInput{
			Calendar:    p.calendar,
			EventID:     p.eventID,
			Title:       p.title,
			Start:       p.start(),
			End:         p.end(),
			Location:    p.location,
			Description: p.description,
			Attendees:   p.attendees,
			// Staged nudges are calendar hygiene; saving one should not mail
			// every attendee. Use E when they need to know.
			Notify: false,
		}); err != nil {
			return eventShiftedMsg{err: err, committed: true}
		}
		return eventShiftedMsg{label: label, restore: restore, committed: true}
	}
}

// pendingFor returns the staged change targeting ev, or nil.
func (m model) pendingFor(ev *Event) *pendingShift {
	if m.pending == nil || ev == nil || ev.ID == "" {
		return nil
	}
	if m.pending.eventID != ev.ID || m.pending.calendar != m.eventCalendar(ev) {
		return nil
	}
	return m.pending
}

// effectiveTimes returns the times to render for ev: the staged ones when a
// change is staged, the saved ones otherwise. unsaved says which it is, so
// callers can mark the row.
func (m model) effectiveTimes(ev *Event) (start, end time.Time, unsaved bool) {
	if p := m.pendingFor(ev); p != nil {
		return p.start(), p.end(), true
	}
	return ev.StartAt, ev.EndAt, false
}

// pendingBadge is the compact row marker for a staged change: "!+1d".
func pendingBadge(p *pendingShift) string {
	return "!" + p.label()
}
