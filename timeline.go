package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// 24-hour mini timeline for the LIST agenda.
//
// A one-line-per-event agenda answers "what is there?" but not "what runs at
// the same time as what?" — the question a maintenance-window calendar is
// actually read for. Google Calendar answers it with vertical lanes, but lanes
// cost `concurrent events × lane width` columns; 25 overlapping windows do not
// fit in any terminal. So the time axis is transposed 90°: every row keeps its
// single line and gains a fixed-width 00:00–24:00 track with the event's span
// drawn on it. Rows share one axis, so vertical alignment *is* the overlap.
//
// Above each day sits a ruler line whose background encodes how many events are
// in effect at that column — the concurrency readout that a per-row bar alone
// cannot give.
//
// Everything is drawn with background-colored SPACES, never block-drawing
// glyphs (U+2580–259F): those are East-Asian-ambiguous and render 2 cells wide
// in some terminals, which would desync the whole frame. The only non-space
// characters are ASCII (`|`, `<`, `>`, digits), all reliably 1 cell.

const (
	timelineMinCols = 24 // 1 column per hour
	timelineMidCols = 48 // 2 per hour — separates :00 from :30
	timelineMaxCols = 96 // 4 per hour — 15-min resolution, matching the )( nudge step
	// timelineReserve is the row space the timeline must NOT take: cursor
	// prefix (2) + time label (11) + gap (1) + a title worth reading (26) +
	// the gap before the bar (1) + slack for the badges (3). A bar that leaves
	// only a stub of the title trades away more than it buys, so a pane that
	// cannot afford this simply gets no timeline.
	timelineReserve = 44
)

// timelineCols returns how many columns a row of the given width can spare for
// the timeline, or 0 when the pane is too narrow to carry one at all. Only the
// three tier widths are ever returned, so hour ticks land on exact column
// boundaries and every row of the frame shares one axis.
func timelineCols(width int) int {
	switch avail := width - timelineReserve; {
	case avail >= timelineMaxCols:
		return timelineMaxCols
	case avail >= timelineMidCols:
		return timelineMidCols
	case avail >= timelineMinCols:
		return timelineMinCols
	default:
		return 0
	}
}

// dayStartIn returns midnight of day's calendar date in loc.
func dayStartIn(day time.Time, loc *time.Location) time.Time {
	y, mo, d := day.Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, loc)
}

// viewDate is the calendar DAY an event belongs to when read in the currently
// selected display timezone, returned as a local-midnight token.
//
// Two coordinate systems meet here and the distinction is the whole reason this
// function exists:
//
//   - Event.StartDate is a *local-timezone* calendar date, derived once at fetch
//     time. It never moves.
//   - Z switches the display timezone, which changes both the clock label AND
//     which calendar day an instant falls on: a KST 03:30 event is 18:30 on the
//     PREVIOUS day in UTC.
//
// Grouping by StartDate while labelling in the display tz made the two
// disagree — a row reading "18:30-01:30" sat under a header a day ahead of the
// day it actually starts, and the timeline axis (anchored on that header's
// date) started five hours after the event did, so every overnight window
// collapsed into a clipped stub at column 0.
//
// The returned time is normalized back to LOCAL midnight so it stays
// interchangeable with anchor/gridTop/weekStart, which are local-midnight day
// tokens throughout. Only the day IDENTITY comes from the display tz; the
// value is still just "a day".
func (m model) viewDate(ev *Event) time.Time {
	loc := m.tz()
	inst, _ := m.rowSpan(ev)
	if ev.AllDay() || inst.IsZero() {
		// All-day events carry a date, not an instant: they are the same
		// calendar day in every timezone, so no conversion applies.
		return dayStartIn(ev.StartDate, time.Local)
	}
	y, mo, d := inst.In(loc).Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, time.Local)
}

// viewDayStart is the absolute instant a day's timeline axis begins: midnight
// of that day IN THE DISPLAY TIMEZONE. Pairs with viewDate — the axis has to be
// anchored in the same coordinate system the day was grouped in, or bars land
// off it.
func (m model) viewDayStart(day time.Time) time.Time {
	return dayStartIn(day, m.tz())
}

// viewToday is today's day token in the display timezone. Late at night in KST
// it is still "yesterday" in UTC, and the agenda's `(today)` star has to follow
// whichever day the rows are actually grouped under.
func (m model) viewToday() time.Time {
	now := time.Now().In(m.tz())
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
}

// timelineSpan maps [start, end) onto column indices of a cols-wide axis
// covering the single day beginning at dayStart. before/after report that the
// event runs past that day's edge (a maintenance window crossing midnight), so
// the caller can draw an overflow arrow instead of silently clipping.
//
// ok is false when the event does not intersect the day at all.
func timelineSpan(start, end, dayStart time.Time, cols int) (lo, hi int, before, after, ok bool) {
	if cols <= 0 || start.IsZero() {
		return 0, 0, false, false, false
	}
	// AddDate, not Add(24h): a DST day is 23 or 25 hours long and the axis has
	// to cover exactly the day the header names.
	dayEnd := dayStart.AddDate(0, 0, 1)
	if end.IsZero() || !end.After(start) {
		end = start // zero-length events still deserve one visible column
	}
	if !end.After(dayStart) && !end.Equal(start) {
		return 0, 0, false, false, false
	}
	if !start.Before(dayEnd) {
		return 0, 0, false, false, false
	}
	if end.Before(dayStart) {
		return 0, 0, false, false, false
	}
	span := dayEnd.Sub(dayStart)
	col := func(t time.Time) float64 {
		return float64(t.Sub(dayStart)) / float64(span) * float64(cols)
	}
	lo = int(col(start))
	// The end instant is exclusive: an event ending exactly on a column
	// boundary must not light that column up.
	hi = int(col(end)*1000-1) / 1000
	if lo < 0 {
		lo = 0
	}
	if hi >= cols {
		hi = cols - 1
	}
	if hi < lo {
		hi = lo
	}
	if lo >= cols {
		return 0, 0, false, false, false
	}
	return lo, hi, start.Before(dayStart), end.After(dayEnd), true
}

// timelineDensity counts, per column, how many of evs are in effect somewhere
// inside that column's slice of the day.
func (m model) timelineDensity(evs []*Event, dayStart time.Time, cols int) []int {
	if cols <= 0 {
		return nil
	}
	out := make([]int, cols)
	for _, ev := range evs {
		start, end := m.rowSpan(ev)
		lo, hi, _, _, ok := timelineSpan(start, end, dayStart, cols)
		if !ok {
			continue
		}
		for c := lo; c <= hi; c++ {
			out[c]++
		}
	}
	return out
}

// Timeline palette. Backgrounds only — the bars are colored spaces.
var (
	tlTrackBG   = "235" // the empty 24h track a bar is drawn on
	tlBarBG     = "60"  // an ordinary event
	tlActiveBG  = "68"  // in effect at this very moment
	tlAllDayBG  = "238" // all-day / dateless: context, not a time slot
	tlSelBG     = "81"  // the selected row (matches timeSlotSelStyle)
	tlPendingBG = "214" // a staged, unsaved time (matches pendingStyle)
	// tlDensityBG ramps cool -> hot. A pure grey ramp was unreadable: four
	// adjacent greys are indistinguishable at one cell wide, which is exactly
	// the size these are drawn at. Hue does the work instead.
	tlDensityBG = []string{"24", "30", "65", "107", "179", "172", "160"}
	// tlDensityFG is the hour-label color per ramp level: the light middle of
	// the ramp needs dark text or the tick digits vanish.
	tlDensityFG = []string{"231", "231", "231", "232", "232", "232", "231"}
	tlNowFG     = "203" // the vertical now marker on the empty track
	tlMarkFG    = "231" // markers drawn on top of a filled bar
)

// densityLevel maps a concurrent-event count to a ramp index, scaled so the
// busiest column in the WHOLE loaded range is the top of the ramp. Relative
// rather than absolute, so the same color means the same load on every day on
// screen; the ruler prints the day's actual peak so the number is recoverable.
// Returns -1 for "nothing running here".
func densityLevel(n, peak int) int {
	if n <= 0 {
		return -1
	}
	if peak <= 1 {
		return len(tlDensityBG) - 1
	}
	top := len(tlDensityBG) - 1
	// +(peak-2) rounds the integer division to nearest.
	lvl := ((n-1)*top + (peak-1)/2) / (peak - 1)
	if lvl < 0 {
		lvl = 0
	}
	if lvl > top {
		lvl = top
	}
	return lvl
}

// densityLevelBG is densityLevel as a background color, with the empty track
// standing in for "nothing running".
func densityLevelBG(n, peak int) string {
	lvl := densityLevel(n, peak)
	if lvl < 0 {
		return tlTrackBG
	}
	return tlDensityBG[lvl]
}

// densityLevelFG picks a tick-label color readable on that level's background.
func densityLevelFG(n, peak int) string {
	lvl := densityLevel(n, peak)
	if lvl < 0 {
		return tlMarkFG
	}
	return tlDensityFG[lvl]
}

// cell is one terminal column of a timeline line.
type cell struct {
	bg string
	fg string // "" = inherit; set only for the ASCII markers
	ch byte
}

// renderCells paints cells into a string, merging runs that share a style so a
// 48-column bar costs a handful of escape sequences instead of 48.
func renderCells(cells []cell) string {
	var b strings.Builder
	for i := 0; i < len(cells); {
		j := i
		var run strings.Builder
		for j < len(cells) && cells[j].bg == cells[i].bg && cells[j].fg == cells[i].fg {
			run.WriteByte(cells[j].ch)
			j++
		}
		st := lipgloss.NewStyle().Background(lipgloss.Color(cells[i].bg))
		if cells[i].fg != "" {
			st = st.Foreground(lipgloss.Color(cells[i].fg)).Bold(true)
		}
		b.WriteString(st.Render(run.String()))
		i = j
	}
	return b.String()
}

// nowColumn returns the column the current moment falls in for this day's axis,
// or -1 when now is not inside that day.
func nowColumn(dayStart time.Time, cols int, now time.Time) int {
	if cols <= 0 {
		return -1
	}
	dayEnd := dayStart.AddDate(0, 0, 1)
	if now.Before(dayStart) || !now.Before(dayEnd) {
		return -1
	}
	c := int(float64(now.Sub(dayStart)) / float64(dayEnd.Sub(dayStart)) * float64(cols))
	if c >= cols {
		c = cols - 1
	}
	return c
}

// rowSpan is the interval a row should DRAW: the staged times when a change is
// staged, the saved ones otherwise. It goes through activeSpan so all-day and
// instant-less events get a real interval instead of a zero one.
func (m model) rowSpan(ev *Event) (time.Time, time.Time) {
	if p := m.pendingFor(ev); p != nil {
		return p.start(), p.end()
	}
	return activeSpan(ev)
}

// timelineBar draws one event's span on a cols-wide 24h track for dayStart.
func (m model) timelineBar(ev *Event, dayStart time.Time, cols int, selected bool) string {
	now := time.Now()
	cells := make([]cell, cols)
	for i := range cells {
		cells[i] = cell{bg: tlTrackBG, ch: ' '}
	}
	start, end := m.rowSpan(ev)
	lo, hi, before, after, ok := timelineSpan(start, end, dayStart, cols)
	if ok {
		bg := tlBarBG
		switch {
		case m.pendingFor(ev) != nil:
			bg = tlPendingBG
		case selected:
			bg = tlSelBG
		case ev.AllDay():
			bg = tlAllDayBG
		case isActiveAt(ev, now):
			bg = tlActiveBG
		}
		for c := lo; c <= hi; c++ {
			cells[c] = cell{bg: bg, ch: ' '}
		}
		// An event clipped by midnight says so, so a 21:00–04:30 window is
		// never misread as ending when the day does.
		if before {
			cells[lo] = cell{bg: bg, fg: tlMarkFG, ch: '<'}
		}
		if after {
			cells[hi] = cell{bg: bg, fg: tlMarkFG, ch: '>'}
		}
	}
	// The now marker is the same "past | upcoming" boundary the agenda's
	// horizontal divider draws, rotated onto the axis.
	if c := nowColumn(dayStart, cols, now); c >= 0 {
		fg := tlNowFG
		if cells[c].bg != tlTrackBG {
			fg = tlMarkFG
		}
		cells[c] = cell{bg: cells[c].bg, fg: fg, ch: '|'}
	}
	return renderCells(cells)
}

// dayBuckets groups every loaded event under each calendar day it TOUCHES, not
// just the day it starts on. The agenda lists a window under its start day, but
// a 21:00-04:30 window is genuinely running through the next morning and has to
// count toward that morning's density or the ruler understates the load.
//
// Days are keyed by viewDate — the same day identity the agenda groups its rows
// under — so a bucket and the axis the ruler draws it on cannot disagree, in
// any display timezone.
//
// The per-event day count is capped: a year-long all-day event would otherwise
// fan out into 365 buckets for no readable gain.
func (m model) dayBuckets() map[string][]*Event {
	const maxDaysPerEvent = 14
	out := map[string][]*Event{}
	for i := range m.events {
		ev := &m.events[i]
		_, end := m.rowSpan(ev)
		day := m.viewDate(ev)
		for n := 0; n < maxDaysPerEvent; n++ {
			out[dayKey(day)] = append(out[dayKey(day)], ev)
			day = day.AddDate(0, 0, 1)
			if end.IsZero() || !end.After(m.viewDayStart(day)) {
				break
			}
		}
	}
	return out
}

// dayKey is the bucket key for a day token.
func dayKey(day time.Time) string { return day.Format("2006-01-02") }

// timelineRuler is the per-day header line under the date: an hour ruler whose
// background encodes how many events overlap at each column. This is the line
// that answers "how stacked is this day, and when" at a glance.
func (m model) timelineRuler(day time.Time, evs []*Event, width, cols int, peak int) string {
	dayStart := m.viewDayStart(day)
	density := m.timelineDensity(evs, dayStart, cols)
	dayPeak := 0
	for _, n := range density {
		if n > dayPeak {
			dayPeak = n
		}
	}
	cells := make([]cell, cols)
	for i := range cells {
		cells[i] = cell{bg: densityLevelBG(density[i], peak), ch: ' '}
	}
	// Hour ticks, spaced so a two-digit label never smears into its neighbour:
	// the label needs 2 columns, the tick step needs to leave more than that.
	step := 6
	switch {
	case cols >= timelineMaxCols:
		step = 2
	case cols >= timelineMidCols:
		step = 3
	}
	for h := 0; h < 24; h += step {
		label := fmt.Sprintf("%d", h)
		at := h * cols / 24
		for i := 0; i < len(label) && at+i < cols; i++ {
			cells[at+i] = cell{bg: cells[at+i].bg, fg: densityLevelFG(density[at+i], peak), ch: label[i]}
		}
	}
	if c := nowColumn(dayStart, cols, time.Now()); c >= 0 {
		cells[c] = cell{bg: cells[c].bg, fg: tlNowFG, ch: '|'}
	}
	// The left gutter carries the two numbers the shading cannot: how many
	// events touch this day at all, and the most that ever run at once.
	label := ""
	if len(evs) > 0 {
		label = fmt.Sprintf("%d events · peak %d", len(evs), dayPeak)
	}
	leftW := max(0, width-1-cols)
	return padLeftW(mutedStyle.Render(truncate(label, leftW)), leftW) + " " + renderCells(cells)
}

// timelinePeak is the busiest single column across every loaded day. The
// density ramp is scaled against it so an identical color means an identical
// number of concurrent events on every day on screen.
func (m model) timelinePeak(buckets map[string][]*Event, cols int) int {
	if cols <= 0 {
		return 0
	}
	peak := 0
	for key, evs := range buckets {
		day, err := time.ParseInLocation("2006-01-02", key, time.Local)
		if err != nil {
			continue
		}
		for _, n := range m.timelineDensity(evs, m.viewDayStart(day), cols) {
			if n > peak {
				peak = n
			}
		}
	}
	return peak
}

// padLeftW right-aligns a (possibly styled) string inside width cells.
func padLeftW(s string, width int) string {
	if lipgloss.Width(s) > width {
		return ansiTruncate(s, width)
	}
	if pad := width - lipgloss.Width(s); pad > 0 {
		return strings.Repeat(" ", pad) + s
	}
	return s
}
