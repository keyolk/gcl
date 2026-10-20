package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// viewGrid renders week/month as one row per week (Sun..Sat), each day a small
// cell with a count; the focused day is highlighted and its events show in the
// toggleable detail modal.
// gridLayout returns how many weeks and event-rows-per-week fit in `height`.
// Deterministic so the renderer and the scroll logic agree.
func (m model) gridLayout(height int) (weeks, evRows int) {
	avail := max(3, height-1) // minus weekday header
	maxWeeks := 6
	if m.view == viewWeek {
		maxWeeks = 10
	}
	weeks = maxWeeks
	for weeks > 1 && (avail/weeks) < 2 {
		weeks--
	}
	evRows = max(1, avail/max(1, weeks)-1)
	if evRows > 6 {
		evRows = 6
	}
	return weeks, evRows
}

// gridGeom is the grid's row structure: which week starts the window, how wide
// a day cell is, and how many event rows each week gets. viewGrid draws from it
// and the mouse handler maps a click to a day with it.
type gridGeom struct {
	firstWeek time.Time
	colW      int
	rowsFor   []int // event rows per week, under that week's date row
}

func (m model) gridGeometry(width, height int) gridGeom {
	firstWeek := m.gridTop
	if firstWeek.IsZero() {
		firstWeek = weekStart(m.anchor)
		if m.view == viewMonth {
			firstWeek = weekStart(monthStart(m.anchor))
		}
	}
	colW := max(12, width/7)

	// events grouped by day
	byDay := map[string][]*Event{}
	for i := range m.events {
		k := dayKey(m.viewDate(&m.events[i]))
		byDay[k] = append(byDay[k], &m.events[i])
	}

	weeks, _ := m.gridLayout(height)

	// Per-week event-row counts are VARIABLE: a week only takes as many rows as
	// its busiest day needs (capped), so empty weeks stay a single date row and
	// never balloon into big blank blocks.
	weekMax := make([]int, weeks)
	for w := 0; w < weeks; w++ {
		mx := 0
		for d := 0; d < 7; d++ {
			day := firstWeek.AddDate(0, 0, w*7+d)
			if n := len(byDay[day.Format("2006-01-02")]); n > mx {
				mx = n
			}
		}
		weekMax[w] = mx
	}
	// Distribute the available body height (minus header + one date row/week).
	budget := max(0, (height-1)-weeks) // rows left for events after date rows
	perCap := 6
	rowsFor := make([]int, weeks)
	// First pass: give each week min(need, perCap) greedily until budget runs out.
	for w := 0; w < weeks && budget > 0; w++ {
		want := min(weekMax[w], perCap)
		give := min(want, budget)
		rowsFor[w] = give
		budget -= give
	}
	return gridGeom{firstWeek: firstWeek, colW: colW, rowsFor: rowsFor}
}

func (m model) viewGrid(width, height int) string {
	g := m.gridGeometry(width, height)
	firstWeek, colW, rowsFor := g.firstWeek, g.colW, g.rowsFor
	weeks := len(rowsFor)
	inner := max(6, colW-1)

	// events grouped by day
	byDay := map[string][]*Event{}
	for i := range m.events {
		k := dayKey(m.viewDate(&m.events[i]))
		byDay[k] = append(byDay[k], &m.events[i])
	}

	var out []string
	var hdr []string
	for _, wd := range []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"} {
		hdr = append(hdr, dayStyle.Render(padCenter(wd, colW)))
	}
	out = append(out, strings.Join(hdr, ""))

	for w := 0; w < weeks; w++ {
		// date row
		var dateCells []string
		for d := 0; d < 7; d++ {
			day := firstWeek.AddDate(0, 0, w*7+d)
			focused := sameDay(day, m.anchor)
			isToday := sameDay(day, time.Now())
			label := day.Format("1/2")
			if isToday {
				label = "*" + label
			}
			cnt := len(byDay[day.Format("2006-01-02")])
			if cnt > 0 {
				label += fmt.Sprintf(" (%d)", cnt)
			}
			dim := m.view == viewMonth && day.Month() != m.anchor.Month()
			cell := padRightW(" "+label, colW)
			switch {
			case focused:
				dateCells = append(dateCells, timeSlotSelStyle.Render(cell))
			case dim:
				dateCells = append(dateCells, mutedStyle.Render(cell))
			default:
				dateCells = append(dateCells, dayStyle.Render(cell))
			}
		}
		out = append(out, strings.Join(dateCells, ""))

		// event rows (variable per week)
		evRows := rowsFor[w]
		for r := 0; r < evRows; r++ {
			var cells []string
			for d := 0; d < 7; d++ {
				day := firstWeek.AddDate(0, 0, w*7+d)
				evs := byDay[day.Format("2006-01-02")]
				txt := ""
				if r == evRows-1 && len(evs) > evRows {
					txt = mutedStyle.Render(fmt.Sprintf("  +%d more", len(evs)-(evRows-1)))
				} else if r < len(evs) {
					ev := evs[r]
					tm := ""
					if !ev.AllDay() && !ev.StartAt.IsZero() {
						tm = ev.StartAt.In(m.tz()).Format("15:04") + " "
					}
					mark := ""
					if m.pendingFor(ev) != nil {
						mark = "!"
					} else if isActiveAt(ev, time.Now()) {
						mark = "◉"
					}
					txt = " " + truncate(mark+tm+ev.Title, inner-1)
					if mark == "!" {
						txt = pendingStyle.Render(txt)
					} else if c := m.overlayColorFor(ev.Calendar); c != "" {
						// The grid has no room for a name, so the owner color IS
						// the identification — without it a merged month grid is
						// an undifferentiated wall of titles.
						txt = lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render(txt)
					}
				}
				cell := padRightW(txt, colW)
				if sameDay(day, m.anchor) {
					cell = selectedRowStyle.Render(cell)
				}
				cells = append(cells, cell)
			}
			out = append(out, strings.Join(cells, ""))
		}
	}
	return strings.Join(out, "\n")
}

// padRightW pads/truncates a (possibly styled) string to exactly width cells.
func padRightW(s string, width int) string {
	if lipgloss.Width(s) > width {
		return ansiTruncate(s, width)
	}
	if pad := width - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

func padCenter(s string, width int) string {
	sw := lipgloss.Width(s)
	if sw >= width {
		return truncate(s, width)
	}
	left := (width - sw) / 2
	right := width - sw - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}
