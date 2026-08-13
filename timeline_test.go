package main

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// The timeline is drawn entirely with background colors, and lipgloss strips
// every color when it cannot see a TTY — which is exactly the case under `go
// test`. Pin a profile so the bars actually render and the assertions below
// have something to measure.
func init() { lipgloss.SetColorProfile(termenv.ANSI256) }

// bgRunes renders a line's background colors as one character per terminal
// column, so a test can assert WHERE a bar sits without depending on which
// palette entry it uses. '.' is the empty track, ' ' is unstyled, and every
// distinct background gets its own letter in first-seen order.
func bgRunes(line string) string {
	ansi := regexp.MustCompile(`\x1b\[([0-9;]*)m`)
	seen := map[string]rune{}
	next := 'a'
	var out strings.Builder
	bg := ""
	i := 0
	emit := func(text string) {
		for _, ch := range text {
			switch {
			case bg == "":
				out.WriteRune(' ')
			case ch != ' ':
				out.WriteRune(ch)
			case bg == tlTrackBG:
				out.WriteRune('.')
			default:
				r, ok := seen[bg]
				if !ok {
					r, next = next, next+1
					seen[bg] = r
				}
				out.WriteRune(r)
			}
		}
	}
	for _, mo := range ansi.FindAllStringSubmatchIndex(line, -1) {
		emit(line[i:mo[0]])
		i = mo[1]
		codes := line[mo[2]:mo[3]]
		if codes == "" || codes == "0" {
			bg = ""
			continue
		}
		parts := strings.Split(codes, ";")
		for j, p := range parts {
			if p == "48" && j+2 < len(parts) && parts[j+1] == "5" {
				bg = parts[j+2]
			}
		}
	}
	emit(line[i:])
	return out.String()
}

// barChars are the characters bgRunes can emit inside a timeline: the track,
// the per-background bar letters, and the ASCII markers drawn on top.
const barChars = ".abcdefghijklmnop<>|0123456789"

// barOf returns the timeline segment of a rendered row: everything from the
// first track/bar column to the end.
func barOf(line string) string {
	s := bgRunes(line)
	if i := strings.IndexAny(s, barChars); i >= 0 {
		return s[i:]
	}
	return ""
}

func timelineModel(width int, evs ...Event) model {
	m := model{
		calendar: "me", view: viewList, jumpUnit: "day",
		width: width, height: 40,
		events: evs,
	}
	if len(evs) > 0 {
		m.anchor = evs[0].StartDate
	}
	sortEvents(m.events)
	return m
}

func timedEvent(id string, day time.Time, sh, sm, eh, em int, daysLater int) Event {
	start := day.Add(time.Duration(sh)*time.Hour + time.Duration(sm)*time.Minute)
	endDay := day.AddDate(0, 0, daysLater)
	end := endDay.Add(time.Duration(eh)*time.Hour + time.Duration(em)*time.Minute)
	return Event{
		ID: id, Title: id, StartDate: day, EndDate: endDay,
		StartTime: start.Format("15:04"), EndTime: end.Format("15:04"),
		StartAt: start, EndAt: end,
	}
}

// The whole point of the feature: two events that overlap must have bars that
// overlap in the same columns, and one that ends before another starts must not.
func TestTimelineBarsAlignWithTheClock(t *testing.T) {
	day := time.Date(2026, 8, 13, 0, 0, 0, 0, time.Local)
	m := timelineModel(160,
		timedEvent("morning", day, 2, 0, 8, 0, 0),
		timedEvent("overlapping", day, 6, 0, 12, 0, 0),
		timedEvent("evening", day, 20, 0, 22, 0, 0),
	)
	cols := timelineCols(max(20, m.schedulePaneWidth()-1))
	if cols == 0 {
		t.Fatal("a 160-col terminal must be able to draw a timeline")
	}
	dayStart := dayStartIn(day, m.tz())
	span := func(id string, sh, eh int) (int, int) {
		t.Helper()
		for i := range m.events {
			if m.events[i].ID == id {
				bar := barOf(m.timelineBar(&m.events[i], dayStart, cols, false))
				lo := strings.IndexAny(bar, "abcdefghijklmnop")
				hi := strings.LastIndexAny(bar, "abcdefghijklmnop")
				if lo < 0 {
					t.Fatalf("%s drew no bar: %q", id, bar)
				}
				// The bar has to land on the hours the label claims.
				if want := sh * cols / 24; lo != want {
					t.Errorf("%s starts at column %d, want %d (%02d:00)", id, lo, want, sh)
				}
				if want := eh*cols/24 - 1; hi != want {
					t.Errorf("%s ends at column %d, want %d (%02d:00 exclusive)", id, hi, want, eh)
				}
				return lo, hi
			}
		}
		t.Fatalf("event %s not found", id)
		return 0, 0
	}
	mLo, mHi := span("morning", 2, 8)
	oLo, oHi := span("overlapping", 6, 12)
	eLo, _ := span("evening", 20, 22)
	if !(oLo <= mHi && mLo <= oHi) {
		t.Errorf("overlapping events must share columns: morning=[%d,%d] overlapping=[%d,%d]", mLo, mHi, oLo, oHi)
	}
	if eLo <= oHi {
		t.Errorf("a later, non-overlapping event must start after the earlier one ends: %d <= %d", eLo, oHi)
	}
}

// A maintenance window running 21:00-04:30 is the shape this calendar is full
// of. Clipping it silently at midnight would read as "ends at midnight".
func TestTimelineMarksMidnightOverflow(t *testing.T) {
	day := time.Date(2026, 8, 13, 0, 0, 0, 0, time.Local)
	ev := timedEvent("[ap9]", day, 21, 0, 4, 30, 1)
	m := timelineModel(160, ev)
	cols := timelineCols(max(20, m.schedulePaneWidth()-1))

	// On its own day it runs off the right edge.
	first := barOf(m.timelineBar(&m.events[0], dayStartIn(day, m.tz()), cols, false))
	if !strings.HasSuffix(first, ">") {
		t.Errorf("a window crossing midnight must end with an overflow arrow: %q", first)
	}
	// On the following day it arrives from the left edge.
	next := barOf(m.timelineBar(&m.events[0], dayStartIn(day.AddDate(0, 0, 1), m.tz()), cols, false))
	lo := strings.IndexAny(next, "abcdefghijklmnop<")
	if lo != 0 || !strings.HasPrefix(next, "<") {
		t.Errorf("the continuation must start at column 0 with an inflow arrow: %q", next)
	}
}

// A window that started yesterday is still running this morning; the ruler has
// to count it, or a fully-booked morning reads as empty.
func TestDensityCountsWindowsInheritedFromYesterday(t *testing.T) {
	day := time.Date(2026, 8, 13, 0, 0, 0, 0, time.Local)
	m := timelineModel(160, timedEvent("[ap9]", day, 21, 0, 4, 30, 1))
	buckets := m.dayBuckets()
	nextKey := day.AddDate(0, 0, 1).Format("2006-01-02")
	if len(buckets[nextKey]) != 1 {
		t.Fatalf("the next day's bucket has %d events, want the inherited window", len(buckets[nextKey]))
	}
	density := m.timelineDensity(buckets[nextKey], dayStartIn(day.AddDate(0, 0, 1), m.tz()), 24)
	if density[0] != 1 {
		t.Errorf("00:00 on the following day must count the running window, got %d", density[0])
	}
	if density[6] != 0 {
		t.Errorf("06:00 is after the window ended, want 0, got %d", density[6])
	}
	// Its own day is counted from 21:00 on.
	own := m.timelineDensity(buckets[day.Format("2006-01-02")], dayStartIn(day, m.tz()), 24)
	if own[21] != 1 || own[20] != 0 {
		t.Errorf("own-day density wrong at 20/21h: %d/%d", own[20], own[21])
	}
}

// The agenda's row width is fixed, so a bar must never push the row past it —
// that is how a frame desyncs.
func TestTimelineRowsStayWithinWidth(t *testing.T) {
	day := time.Date(2026, 8, 13, 0, 0, 0, 0, time.Local)
	long := timedEvent("x", day, 9, 0, 18, 0, 0)
	long.Title = strings.Repeat("very long event title ", 8)
	m := timelineModel(200, long, timedEvent("[ap9]", day, 21, 0, 4, 30, 1))
	for _, width := range []int{40, 60, 74, 80, 100, 140, 198} {
		cols := timelineCols(width)
		for i := range m.events {
			row := m.eventRow(&m.events[i], i == 0, width, dayStartIn(day, m.tz()), cols)
			if got := lipgloss.Width(row); got > width {
				t.Errorf("width=%d cols=%d: row is %d cells wide", width, cols, got)
			}
		}
		ruler := m.timelineRuler(day, m.dayBuckets()[day.Format("2006-01-02")], width, cols, 2)
		if cols > 0 && lipgloss.Width(ruler) != width {
			t.Errorf("width=%d: ruler is %d cells, want exactly %d", width, lipgloss.Width(ruler), width)
		}
	}
}

// Narrow panes must drop the timeline rather than crush the title into a stub.
func TestTimelineDropsOutOnNarrowPanes(t *testing.T) {
	if got := timelineCols(60); got != 0 {
		t.Errorf("a 60-col pane should get no timeline, got %d columns", got)
	}
	if got := timelineCols(200); got != timelineMaxCols {
		t.Errorf("a 200-col pane should get the finest timeline, got %d", got)
	}
	day := time.Date(2026, 8, 13, 0, 0, 0, 0, time.Local)
	m := timelineModel(70, timedEvent("standup", day, 10, 0, 10, 30, 0))
	row := m.eventRow(&m.events[0], false, 60, dayStartIn(day, m.tz()), timelineCols(60))
	if strings.Contains(bgRunes(row), ".") {
		t.Errorf("no timeline track should be drawn on a narrow row: %q", row)
	}
	if !strings.Contains(stripANSI(row), "standup") {
		t.Errorf("the title must survive: %q", stripANSI(row))
	}
}

// t is a toggle, and toggling it off has to restore exactly the old agenda.
func TestTimelineToggleOwnsOnlyItsKey(t *testing.T) {
	day := time.Date(2026, 8, 13, 0, 0, 0, 0, time.Local)
	m := timelineModel(160, timedEvent("standup", day, 10, 0, 10, 30, 0))
	m.selected = 0

	on := m.viewAgendaCards(150, 20)
	if !strings.Contains(stripANSI(on), "peak") {
		t.Fatalf("the density ruler should be on by default:\n%s", stripANSI(on))
	}

	mm, _ := m.handleKey(keyMsg("t"))
	off := mm.(model)
	if !off.timelineHidden {
		t.Fatal("t did not toggle the timeline off")
	}
	if got := stripANSI(off.viewAgendaCards(150, 20)); strings.Contains(got, "peak") {
		t.Errorf("the ruler survived the toggle:\n%s", got)
	}

	// Toggling must not disturb the cursor, the view, or the anchor — the same
	// contract the `a` panel toggle holds to.
	if off.selected != m.selected || off.view != m.view || !off.anchor.Equal(m.anchor) {
		t.Errorf("t moved something it shouldn't: selected=%d view=%v anchor=%v", off.selected, off.view, off.anchor)
	}

	back, _ := off.handleKey(keyMsg("t"))
	if back.(model).timelineHidden {
		t.Error("t did not toggle back on")
	}
}

// t must not swallow keys that already mean something.
func TestTimelineToggleLeavesOtherKeysAlone(t *testing.T) {
	day := time.Date(2026, 8, 13, 0, 0, 0, 0, time.Local)
	m := timelineModel(160,
		timedEvent("a", day, 9, 0, 10, 0, 0),
		timedEvent("b", day, 11, 0, 12, 0, 0),
	)
	for _, key := range []string{"j", "k", "d", "w", "m", "n", "a", "g"} {
		mm, _ := m.handleKey(keyMsg(key))
		if mm.(model).timelineHidden != m.timelineHidden {
			t.Errorf("%q changed the timeline toggle", key)
		}
	}
}

// A staged (unsaved) nudge must move the BAR too, not just the text label —
// otherwise the picture and the number disagree about the same event.
func TestTimelineBarFollowsStagedTime(t *testing.T) {
	day := time.Date(2026, 8, 13, 0, 0, 0, 0, time.Local)
	m := timelineModel(160, timedEvent("standup", day, 10, 0, 11, 0, 0))
	m.selected = 0
	cols := timelineCols(150)
	dayStart := dayStartIn(day, m.tz())
	before := barOf(m.timelineBar(&m.events[0], dayStart, cols, false))

	// > stages a whole-day move; the bar must leave this day entirely.
	mm, _, _ := m.handleQuickAction(">")
	moved := mm.(model)
	after := barOf(moved.timelineBar(&moved.events[0], dayStart, cols, false))
	if after == before {
		t.Errorf("the bar ignored the staged day move:\nbefore %q\nafter  %q", before, after)
	}
	if strings.ContainsAny(after, "abcdefghijklmnop") {
		t.Errorf("an event staged onto the next day must draw nothing on this day's axis: %q", after)
	}
	// And it must appear on the day it was moved to.
	next := barOf(moved.timelineBar(&moved.events[0], dayStartIn(day.AddDate(0, 0, 1), m.tz()), cols, false))
	if !strings.ContainsAny(next, "abcdefghijklmnop") {
		t.Errorf("the staged event drew no bar on its new day: %q", next)
	}
}

// A multi-day all-day event (PTO, an on-call rotation) carries dates, not
// times. It has to fill every day it covers — and stop on Google's EXCLUSIVE
// end date, not a day late.
func TestAllDayEventsFillTheirWholeSpan(t *testing.T) {
	d := time.Date(2026, 8, 13, 0, 0, 0, 0, time.Local)
	m := timelineModel(160,
		Event{ID: "pto", Title: "PTO", StartDate: d, EndDate: d.AddDate(0, 0, 3)},
		Event{ID: "solo", Title: "one day", StartDate: d},
	)
	full := strings.Repeat("a", 24)
	bar := func(id string, off int) string {
		t.Helper()
		for i := range m.events {
			if m.events[i].ID == id {
				return barOf(m.timelineBar(&m.events[i], dayStartIn(d.AddDate(0, 0, off), m.tz()), 24, false))
			}
		}
		t.Fatalf("no event %s", id)
		return ""
	}
	// Days 0..2 are covered; the exclusive end date (day 3) is not.
	for off := 0; off < 3; off++ {
		got := strings.NewReplacer("<", "a", ">", "a", "|", "a").Replace(bar("pto", off))
		if got != full {
			t.Errorf("PTO on day+%d = %q, want a full bar", off, got)
		}
	}
	if got := bar("pto", 3); strings.ContainsAny(got, "abcdefghijklmnop") {
		t.Errorf("PTO drew on its exclusive end date: %q", got)
	}
	// A dateless single-day event still fills its one day.
	if got := strings.NewReplacer("|", "a").Replace(bar("solo", 0)); got != full {
		t.Errorf("single all-day = %q, want a full bar", got)
	}
	if got := bar("solo", 1); strings.ContainsAny(got, "abcdefghijklmnop") {
		t.Errorf("a single all-day event bled into the next day: %q", got)
	}
}

// The density ramp is relative to the busiest column on screen, so a day that
// stacks 9 windows has to shade differently from one that has 1.
func TestDensityDistinguishesQuietAndStackedHours(t *testing.T) {
	day := time.Date(2026, 8, 13, 0, 0, 0, 0, time.Local)
	evs := []Event{timedEvent("solo", day, 1, 0, 2, 0, 0)}
	for i := 0; i < 8; i++ {
		evs = append(evs, timedEvent(string(rune('a'+i)), day, 10, 0, 12, 0, 0))
	}
	m := timelineModel(160, evs...)
	buckets := m.dayBuckets()
	cols := 24
	peak := m.timelinePeak(buckets, cols)
	if peak != 8 {
		t.Fatalf("peak = %d, want 8", peak)
	}
	density := m.timelineDensity(buckets[day.Format("2006-01-02")], dayStartIn(day, m.tz()), cols)
	if densityLevelBG(density[1], peak) == densityLevelBG(density[10], peak) {
		t.Error("1 concurrent event and 8 must not shade the same")
	}
	if densityLevelBG(density[5], peak) != tlTrackBG {
		t.Error("an hour with nothing running must show the empty track")
	}
}
