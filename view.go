package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/render"
)

type splitKind int

const (
	splitNone splitKind = iota
	splitRight
	splitBottom
)

// splitMode picks where the detail pane goes based on pane geometry: wide panes
// split right, tall/narrow panes split bottom, tiny panes show no detail.
func (m model) splitMode() splitKind {
	// Terminal cells are ~2x taller than wide, so weight width to compare shape.
	if m.width >= 100 && m.width >= m.height*2 {
		return splitRight
	}
	if m.width >= 90 {
		return splitRight
	}
	if m.height >= 18 {
		return splitBottom
	}
	return splitNone
}

// fitPane clamps a rendered pane to an exact width/height so split layouts pin
// cleanly to the right/bottom edges instead of collapsing to content width.
func fitPane(s string, width, height int) string {
	s = clampLineWidth(s, width)
	s = clampToHeight(s, height)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if pad := width - lipgloss.Width(l); pad > 0 {
			lines[i] = l + strings.Repeat(" ", pad)
		}
	}
	return strings.Join(lines, "\n")
}

// schedulePaneWidth is the width the list agenda actually gets, after the
// detail pane takes its share. The `t` toggle reports whether a timeline fits,
// so it has to ask about the same width the renderer will use.
func (m model) schedulePaneWidth() int {
	bodyWidth := max(20, m.width-2)
	if m.splitMode() == splitRight {
		rightW := max(34, bodyWidth/3)
		return max(30, bodyWidth-rightW-1)
	}
	return bodyWidth
}

func (m model) View() string {
	if m.width <= 0 {
		return "loading~"
	}
	viewName := map[viewMode]string{viewList: "LIST", viewWeek: "WEEK", viewMonth: "MONTH"}[m.view]
	calPill := calPillStyle.Render("📅 " + m.calendarDisplayName() + "  e:switch")
	// While an overlay is on, the calendar pill would otherwise still name a
	// single calendar and quietly misdescribe what is on screen.
	if m.overlay.on() {
		calPill = calPillStyle.Render(fmt.Sprintf("📅 %d calendars  O:edit", len(m.overlay.active)))
	}
	tzPill := tzPillStyle.Render("🕓 " + m.tzLabel())
	metaText := fmt.Sprintf("%s | %s", viewName, m.anchor.Format("2006-01-02"))
	if m.view == viewList {
		metaText = fmt.Sprintf("%s | %s | step:%s", viewName, m.anchor.Format("2006-01-02"), m.jumpUnit)
	}
	meta := metaStyle.Render(" " + metaText + " ")
	headerContent := calPill
	// The reload indicator sits right after the calendar pill so the
	// tail-truncation of a narrow header never cuts it off.
	if m.loading && m.hasLoaded() {
		headerContent += loadingPillStyle.Render("loading~")
	}
	headerContent += meta + tzPill
	// An in-effect event is the thing you most need to know without asking, so
	// the count lives in the header and doubles as the advert for `a`.
	if label := m.activeCountLabel(time.Now()); label != "" {
		headerContent += activePillStyle.Render(label + " a")
	}
	// The header is built from styled pills, so it must be cut with the
	// ANSI-aware truncator: the plain one slices runes and would eat a reset
	// sequence, bleeding style into the rest of the frame. topBarStyle also pads
	// 1 cell on each side, and lipgloss counts that inside Width(), so the
	// content has to fit in width-4 or the bar wraps and steals a body row.
	headerWidth := max(1, m.width-2)
	headerLine := topBarStyle.Width(headerWidth).Render(ansiTruncateTail(headerContent, max(1, headerWidth-2)))

	// The frame is header (1) + body + status (1) + hint (1); body must therefore
	// be height-3 rows so the whole thing fills exactly m.height. The pane sizes
	// come from paneLayout, the same numbers the mouse handler hit-tests with.
	l := m.paneLayout()
	contentHeight, bodyWidth, activeH, scheduleHeight, legendH := l.contentH, l.bodyW, l.activeH, l.scheduleH, l.legendH
	var body string
	// Only an agenda that has never loaded is replaced by a placeholder. Once
	// events are on screen, a reload (nudge, save, move) keeps them and the
	// header shows a loading pill instead, so the body never flashes empty; a
	// failed reload likewise keeps them and reports in the status line.
	if m.bodyIsPlaceholder() && m.loading {
		body = cardStyle.Width(max(20, bodyWidth-4)).Render("loading calendar events~")
	} else if m.bodyIsPlaceholder() {
		body = errorStyle.Render(wrap(m.err.Error(), bodyWidth))
	} else {
		// The schedule (list agenda or grid) plus a detail pane. The detail pane
		// splits to the RIGHT on wide panes and to the BOTTOM on tall/narrow ones.
		schedule := func(w, h int) string {
			if m.view == viewList {
				return m.viewScheduleCards(w, h)
			}
			return m.viewGrid(w, h)
		}
		switch l.split {
		case splitRight:
			left := fitPane(schedule(l.schedule.W, l.schedule.H), l.schedule.W, l.schedule.H)
			right := fitPane(m.viewDetailCard(l.detail.W, l.detail.H), l.detail.W, l.detail.H)
			body = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
		case splitBottom:
			top := fitPane(schedule(l.schedule.W, l.schedule.H), l.schedule.W, l.schedule.H)
			bottom := fitPane(m.viewDetailCard(l.detail.W, l.detail.H), l.detail.W, l.detail.H)
			body = top + "\n" + bottom
		default: // no split - too small for a detail pane
			body = schedule(l.schedule.W, l.schedule.H)
		}
		if activeH > 0 {
			panel := fitPane(m.viewActivePanel(bodyWidth, activeH), bodyWidth, activeH)
			body = panel + "\n" + clampToHeight(body, scheduleHeight)
		}
		if legendH > 0 {
			body = clampToHeight(body, scheduleHeight) + "\n" + m.overlayLegend(bodyWidth)
		}
	}

	body = clampLineWidth(body, bodyWidth)
	body = clampToHeight(body, contentHeight)

	// Modal overlays (create/edit, delete, pickers) still float bottom-right.
	if modal := m.modalView(); modal != "" {
		body = overlayBottomRight(body, modal, bodyWidth, contentHeight)
	}

	statusLine := m.statusView()
	// The hint bar turns amber while a change is staged, so an unsaved nudge is
	// impossible to mistake for a saved one even at a glance.
	barStyle := hintBarStyle
	if m.pending != nil {
		barStyle = pendingBarStyle
	}
	hintLine := barStyle.Width(max(0, m.width)).Render(truncate(m.shortcutHint(), max(1, m.width)))

	var b strings.Builder
	b.WriteString(headerLine)
	b.WriteByte('\n')
	b.WriteString(body)
	b.WriteByte('\n')
	b.WriteString(statusLine)
	b.WriteByte('\n')
	b.WriteString(hintLine)
	return b.String()
}

// clampToHeight makes s occupy exactly n rows: extra rows are dropped, missing
// rows are padded with blanks. Keeps the bottom status/hint bars pinned.
func clampToHeight(s string, n int) string {
	if n < 0 {
		n = 0
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// clampLineWidth truncates every line to at most width display cells so a wide
// line never wraps and pushes the layout down.
func clampLineWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if lipgloss.Width(l) > width {
			lines[i] = ansiTruncate(l, width)
		}
	}
	return strings.Join(lines, "\n")
}

// overlayBottomRight draws `top` (the modal) over `base` (the schedule/detail
// content) anchored to the bottom-right of a totalWidth x totalHeight area, so
// the content behind the modal stays visible instead of being wiped.
func overlayBottomRight(base, top string, totalWidth, totalHeight int) string {
	return overlayCorner(base, top, totalWidth, totalHeight, false)
}

// overlayCorner draws `top` over `base` at the right edge, anchored to the top
// (atTop=true) or bottom (atTop=false) of the area. Content behind stays visible.
func overlayCorner(base, top string, totalWidth, totalHeight int, atTop bool) string {
	baseLines := strings.Split(base, "\n")
	for len(baseLines) < totalHeight {
		baseLines = append(baseLines, "")
	}
	topLines := strings.Split(top, "\n")
	th := len(topLines)
	tw := 0
	for _, l := range topLines {
		if w := lipgloss.Width(l); w > tw {
			tw = w
		}
	}
	rowStart := max(0, totalHeight-th)
	if atTop {
		rowStart = 0
	}
	colStart := max(0, totalWidth-tw-1)

	for i, tl := range topLines {
		row := rowStart + i
		if row >= len(baseLines) {
			break
		}
		left := ansiTruncate(baseLines[row], colStart)
		if pad := colStart - lipgloss.Width(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		baseLines[row] = left + tl
	}
	return strings.Join(baseLines, "\n")
}

// ansiTruncate cuts an ANSI-styled string to at most `width` display cells.
func ansiTruncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "")
}

// ansiTruncateTail is ansiTruncate with cutMarker when something was cut, for
// styled content (pills, headers).
func ansiTruncateTail(s string, width int) string {
	return render.TruncateMarker(s, width, cutMarker)
}
