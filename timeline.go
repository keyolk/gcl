package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/chart"
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
//
// The axis math (span columns with an exclusive end, per-column density,
// overflow arrows, the now marker, tick placement) is tuikit's chart package,
// which was ported from this file; what stays here is the calendar logic and
// the background-color palette.

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
func timelineCols(width int) int { return timelineColsWithReserve(width, 0) }

// timelineColsWithReserve is timelineCols with extra columns spoken for by
// something else on the row — currently the overlay's "● name " owner tag.
// Without accounting for it, a three-person overlay silently loses its bars on
// panes that were wide enough a moment earlier, which reads as a bug.
func timelineColsWithReserve(width, extra int) int {
	switch avail := width - timelineReserve - extra; {
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

// chartSpans converts events to chart spans using the interval each row DRAWS
// (rowSpan). An event with no start instant has nothing to place on an axis.
func (m model) chartSpans(evs []*Event) []chart.Span {
	out := make([]chart.Span, 0, len(evs))
	for _, ev := range evs {
		start, end := m.rowSpan(ev)
		if start.IsZero() {
			continue
		}
		out = append(out, chart.Span{Start: start, End: end})
	}
	return out
}

// timelineDensity counts, per column, how many of evs are in effect somewhere
// inside that column's slice of the day. The axis ends at AddDate(0, 0, 1),
// not Add(24h): a DST day is 23 or 25 hours long and the axis has to cover
// exactly the day the header names.
func (m model) timelineDensity(evs []*Event, dayStart time.Time, cols int) []int {
	return chart.Density(m.chartSpans(evs), dayStart, dayStart.AddDate(0, 0, 1), cols)
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

// renderCells paints the ruler's cells into a string, merging runs that share a
// style so a 48-column ruler costs a handful of escape sequences instead of 48.
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

// barGlyphs is the chart glyph set the timeline draws with: every span and
// track cell is a SPACE, so the bar is carried by the background color alone
// (see the file comment for why block glyphs are off the table here), and the
// only visible characters are the 1-cell ASCII markers.
var barGlyphs = func() chart.Glyphs {
	g := chart.ASCII()
	g.Density = [4]string{" ", " ", " ", " "}
	g.Track, g.Now, g.Before, g.After = " ", "|", "<", ">"
	return g
}()

// bgStyle is a style that paints only a background.
func bgStyle(bg string) lipgloss.Style {
	return lipgloss.NewStyle().Background(lipgloss.Color(bg))
}

// timelineBar draws one event's span on a cols-wide 24h track for dayStart.
func (m model) timelineBar(ev *Event, dayStart time.Time, cols int, selected bool) string {
	if cols <= 0 {
		return ""
	}
	now := time.Now()
	from, to := dayStart, dayStart.AddDate(0, 0, 1)
	spans := m.chartSpans([]*Event{ev})
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
	default:
		// In an overlay the bar is tinted with its owner's color, so the
		// vertical alignment that already shows *that* two events overlap
		// also shows *whose* they are. Row state (selected / staged /
		// active-now) still wins: those say something about this row that
		// matters more than who owns it.
		if c := m.overlayColorFor(ev.Calendar); c != "" {
			bg = c
		}
	}
	// The now marker is the same "past | upcoming" boundary the agenda's
	// horizontal divider draws, rotated onto the axis. It keeps the color of
	// the cell it lands on, so it has to know whether the bar covers it: the
	// chart styles the marker with one style wherever it falls.
	nowBG, nowFG := tlTrackBG, tlNowFG
	if c := nowColumn(dayStart, cols, now); c >= 0 && chart.Density(spans, from, to, cols)[c] > 0 {
		nowBG, nowFG = bg, tlMarkFG
	}
	// Line also styles the overflow arrows: an event clipped by midnight says
	// so, so a 21:00-04:30 window is never misread as ending when the day does.
	look := chart.Look{Glyphs: barGlyphs, Styles: chart.Styles{
		Line:  bgStyle(bg).Foreground(lipgloss.Color(tlMarkFG)).Bold(true),
		Muted: bgStyle(tlTrackBG),
		Now:   bgStyle(nowBG).Foreground(lipgloss.Color(nowFG)).Bold(true),
	}}
	return chart.Timeline(spans, from, to, cols, chart.TimelineOpts{Look: look, Now: now, Peak: 1})
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
	// Hour ticks, spaced so a two-digit label never smears into its neighbour:
	// the label needs 2 columns, the tick step needs to leave more than that.
	step := 6 * time.Hour
	switch {
	case cols >= timelineMaxCols:
		step = 2 * time.Hour
	case cols >= timelineMidCols:
		step = 3 * time.Hour
	}
	// The chart draws the labels and the now marker as plain ASCII (zero
	// Look: no styles, ASCII glyphs), exactly cols cells. The background,
	// which carries the density, is painted under it here: the chart's
	// density is a glyph ramp, and this ruler is a color ramp of spaces.
	axis := ansi.Strip(chart.Ruler(dayStart, dayStart.AddDate(0, 0, 1), cols, step,
		chart.TimelineOpts{Now: time.Now()}))
	cells := make([]cell, cols)
	for i := range cells {
		cells[i] = cell{bg: densityLevelBG(density[i], peak), ch: ' '}
		if i >= len(axis) {
			continue
		}
		switch ch := axis[i]; ch {
		case ' ':
		case '|':
			cells[i].fg, cells[i].ch = tlNowFG, ch
		default:
			cells[i].fg, cells[i].ch = densityLevelFG(density[i], peak), ch
		}
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
