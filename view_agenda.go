package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func (m model) viewScheduleCards(width, height int) string {
	if len(m.events) == 0 {
		// An empty calendar is the moment "wrong calendar?" is most likely, and
		// the moment there is nothing else on screen to learn from — so the
		// calendar switcher gets named here rather than only in the hint bar.
		msg := "No events in this range\n\n" +
			mutedStyle.Render("e  switch calendar (fuzzy: recent, subscribed, aliases)") + "\n" +
			mutedStyle.Render("N  new event    n  jump to now    R  refresh    ?  help")
		return cardStyle.Width(max(20, width-2)).Render(msg)
	}
	return m.viewAgendaCards(width, height)
}

// viewAgendaCards renders a COMPACT one-line-per-event agenda so many events
// fit on screen. Each row costs 1 line; day headers cost 1. A viewport keeps
// the selected event visible.
func (m model) viewAgendaCards(width, height int) string {
	lines, _ := m.agendaLines(width, height)
	return strings.Join(lines, "\n")
}

// agendaLines is the agenda as drawn, one entry per screen row, plus owner:
// the index into m.events each row shows, or -1 for a day header, ruler, "now"
// divider or "~ more" row. The mouse handler maps a clicked row to an event
// through owner, so it reads the very rows View draws rather than recounting
// the headers.
func (m model) agendaLines(width, height int) (lines []string, owner []int) {
	if height <= 0 {
		return nil, nil
	}
	innerWidth := max(20, width-1)
	// The mini timeline only appears when the pane can spare the columns; below
	// that the agenda falls back to exactly what it rendered before.
	cols := 0
	var buckets map[string][]*Event
	peak := 0
	if !m.timelineHidden {
		cols = timelineColsWithReserve(innerWidth, m.overlayRowReserve())
		if cols > 0 {
			buckets = m.dayBuckets()
			peak = m.timelinePeak(buckets, cols)
		}
	}
	start := m.agendaStartIndex(height)
	current := ""
	selected := m.selectedEvent()
	now := time.Now()
	nowShown := false
	nowLine := func() string {
		return nowLineStyle.Render(" -- now " + now.In(m.tz()).Format("15:04") + " " + strings.Repeat("-", max(2, innerWidth-14)))
	}
	// truncated marks that not everything fit; the last row is replaced with a
	// "~ more" hint. Returns true when the caller should stop emitting.
	truncated := func() bool {
		if len(lines) >= height {
			if height > 0 {
				lines[height-1] = mutedStyle.Render("  ~ more")
				owner[height-1] = -1
			}
			return true
		}
		return false
	}
	add := func(line string, event int) {
		lines = append(lines, line)
		owner = append(owner, event)
	}
	// The "now" divider marks the boundary between past and upcoming events. It
	// is only meaningful when that boundary actually falls within the rendered
	// range: the previous event must be in the past and the current one in the
	// future. If the first visible event is already upcoming (we scrolled past
	// "now") or every event is in the past, we don't draw a floating divider.
	prevPast := false
	dayStart := time.Time{}
	for i := start; i < len(m.events); i++ {
		ev := &m.events[i]
		if !nowShown && prevPast && !eventSortInstant(ev).Before(now) {
			if truncated() {
				break
			}
			add(nowLine(), -1)
			nowShown = true
		}
		// Rows group by the day the event falls on IN THE DISPLAY TIMEZONE, so
		// the header, the clock label and the timeline axis always name the
		// same day (see viewDate).
		evDay := m.viewDate(ev)
		day := evDay.Format("Mon Jan 02")
		if day != current {
			current = day
			dayStart = m.viewDayStart(evDay)
			if truncated() {
				break
			}
			hdr := " " + day
			if sameDay(evDay, m.viewToday()) {
				hdr = " * " + day + "  (today)"
			}
			add(dayHeaderStyle.Render(hdr), -1)
			// The density ruler belongs directly under its date header: it is
			// the axis every bar below it is read against.
			if cols > 0 {
				if truncated() {
					break
				}
				add(m.timelineRuler(evDay, buckets[dayKey(evDay)], innerWidth, cols, peak), -1)
			}
		}
		if truncated() {
			break
		}
		add(m.eventRow(ev, selected == ev, innerWidth, dayStart, cols), i)
		prevPast = eventSortInstant(ev).Before(now)
	}
	return lines, owner
}

// locationWithoutRooms returns the event location with any comma-separated parts
// that merely repeat a meeting room removed, so 📍 shows the real place/link and
// 🏛 shows the rooms without duplication. Falls back to the raw location.
func locationWithoutRooms(ev *Event) string {
	if ev.Location == "" {
		return ""
	}
	if len(ev.Rooms) == 0 {
		return ev.Location
	}
	roomSet := make(map[string]bool, len(ev.Rooms))
	for _, r := range ev.Rooms {
		roomSet[strings.TrimSpace(strings.ToLower(r))] = true
	}
	var kept []string
	for _, part := range strings.Split(ev.Location, ",") {
		p := strings.TrimSpace(part)
		if p == "" || roomSet[strings.ToLower(p)] {
			continue
		}
		kept = append(kept, p)
	}
	return strings.Join(kept, ", ")
}

// eventRow is a single compact line: [time] title  badges  [24h timeline]. The
// time is a filled pill so consecutive events are visually separated even
// without blank lines. When cols > 0 the row ends with a mini timeline bar
// drawn on a shared 00:00-24:00 axis for dayStart, so overlapping events line
// up vertically and the concurrency is readable at a glance.
func (m model) eventRow(ev *Event, selected bool, width int, dayStart time.Time, cols int) string {
	// A staged change shows the time it WOULD have, marked so it never passes
	// for a saved one.
	pending := m.pendingFor(ev)
	label := m.timeLabel(ev)
	if pending != nil {
		loc := m.tz()
		label = pending.start().In(loc).Format("15:04") + "-" + pending.end().In(loc).Format("15:04")
	}
	tl := fmt.Sprintf("%-11s", label)
	badges := ""
	if pending != nil {
		badges += " " + pendingBadge(pending)
	}
	// An event straddling this moment is what a maintenance-window calendar is
	// read for; mark it in the agenda too, not only in the `a` panel.
	if isActiveAt(ev, time.Now()) {
		badges += " ◉now"
	}
	if n := len(ev.Attendees); n > 0 {
		badges += fmt.Sprintf(" 👥%d", n)
	}
	if ev.Location != "" || len(ev.Rooms) > 0 {
		badges += " 📍"
	}
	if ev.otherLinkCount() > 0 {
		badges += " >"
	}
	// The timeline is a styled trailing segment, so the text part has to be
	// padded to an exact width or the bars of different rows won't align.
	bar := ""
	textW := width
	if cols > 0 {
		bar = m.timelineBar(ev, dayStart, cols, selected)
		// timelineCols only returns a non-zero width when this subtraction
		// still leaves timelineReserve cells for the text, so no clamp here.
		textW = width - cols - 1
	}
	titleRoom := max(6, textW-lipgloss.Width(tl)-lipgloss.Width(badges)-4)
	// While an overlay is on, every row has to say WHOSE event it is — a merged
	// agenda where the rows are indistinguishable is worse than two separate
	// ones. The tag is styled separately and prepended to the title so the row
	// state styles (selected/pending) don't overwrite the owner color.
	tag := ""
	if m.overlay.on() {
		// A zero name width is the narrow-pane fallback (dot only), not "no
		// tag": the row still has to say whose event it is.
		tag = m.overlayTag(ev, m.overlayNameWidth())
		if tag != "" {
			titleRoom = max(6, titleRoom-lipgloss.Width(tag))
		}
	}
	title := linkSpan(ev.eventLink(), truncate(ev.Title, titleRoom), "", "")
	// text is the styled left part; it is padded and the bar appended after,
	// because embedding the bar inside a single Render would inject the row
	// style's reset into the middle of the bar's own escape sequences.
	var text string
	switch {
	case selected && pending != nil:
		text = pendingStyle.Render(fmt.Sprintf("▸ %s ", tl)) + tag + pendingStyle.Render(title+badges)
	case selected:
		text = selectedRowStyle.Render(fmt.Sprintf("▸ %s ", tl)) + tag + selectedRowStyle.Render(title+badges)
	case pending != nil:
		text = "  " + pendingStyle.Render(tl+" ") + tag + pendingStyle.Render(title+badges)
	default:
		text = "  " + timePillMini.Render(tl) + " " + tag + title + mutedStyle.Render(badges)
	}
	if bar == "" {
		return text
	}
	return padRightW(text, textW) + " " + bar
}

func (m model) agendaStartIndex(height int) int {
	if len(m.events) == 0 {
		return 0
	}
	selected := max(0, min(m.selected, len(m.events)-1))
	// Each event = 1 row; a day boundary costs a header, plus the density ruler
	// when the timeline is drawn. Miscounting the header cost pushes the
	// selection off its intended position as days scroll past.
	headerCost := 1
	if !m.timelineHidden && timelineColsWithReserve(max(20, m.schedulePaneWidth()-1), m.overlayRowReserve()) > 0 {
		headerCost = 2
	}
	// Keep selected around the upper-middle so there's context above and room
	// below.
	budgetAbove := max(2, height/3)
	used := 0
	start := selected
	for start > 0 {
		prev := start - 1
		cost := 1
		if prev == 0 || !sameDay(m.viewDate(&m.events[prev-1]), m.viewDate(&m.events[prev])) {
			cost += headerCost
		}
		if used+cost > budgetAbove {
			break
		}
		used += cost
		start = prev
	}
	return start
}
