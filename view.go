package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	// be height-3 rows so the whole thing fills exactly m.height.
	contentHeight := max(4, m.height-3)
	bodyWidth := max(20, m.width-2)
	// The docked active panel takes rows off the TOP of the body: "what is in
	// effect now" belongs above "what is coming", and a top dock keeps the
	// schedule's own scroll position undisturbed when the panel toggles.
	activeH := m.activePanelHeight(contentHeight)
	scheduleHeight := max(3, contentHeight-activeH)
	// The overlay legend is the only thing that maps a dot color to a person,
	// so it costs a permanent row while an overlay is on — dots without a key
	// are decoration.
	legendH := 0
	if m.overlay.on() && !m.loading {
		legendH = 1
		scheduleHeight = max(3, scheduleHeight-1)
	}
	var body string
	// Only an agenda that has never loaded is replaced by a placeholder. Once
	// events are on screen, a reload (nudge, save, move) keeps them and the
	// header shows a loading pill instead, so the body never flashes empty; a
	// failed reload likewise keeps them and reports in the status line.
	if m.loading && !m.hasLoaded() {
		body = cardStyle.Width(max(20, bodyWidth-4)).Render("loading calendar events~")
	} else if m.err != nil && !m.hasLoaded() {
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
		switch m.splitMode() {
		case splitRight:
			rightW := max(34, bodyWidth/3)
			leftW := m.schedulePaneWidth()
			left := fitPane(schedule(leftW, scheduleHeight), leftW, scheduleHeight)
			right := fitPane(m.viewDetailCard(rightW, scheduleHeight), rightW, scheduleHeight)
			body = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
		case splitBottom:
			detailH := max(6, scheduleHeight/3)
			topH := max(4, scheduleHeight-detailH)
			top := fitPane(schedule(bodyWidth, topH), bodyWidth, topH)
			bottom := fitPane(m.viewDetailCard(bodyWidth, detailH), bodyWidth, detailH)
			body = top + "\n" + bottom
		default: // no split - too small for a detail pane
			body = schedule(bodyWidth, scheduleHeight)
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
	if m.mode == modeCreate {
		body = overlayBottomRight(body, m.viewCreate(), bodyWidth, contentHeight)
	} else if m.mode == modeFindTime {
		body = overlayBottomRight(body, m.viewFindTime(), bodyWidth, contentHeight)
	} else if m.mode == modeOverlayPicker {
		body = overlayBottomRight(body, m.viewOverlayPicker(), bodyWidth, contentHeight)
	} else if m.mode == modeConfirmDelete {
		body = overlayBottomRight(body, m.viewConfirmDelete(), bodyWidth, contentHeight)
	} else if m.mode != modeNormal {
		body = overlayBottomRight(body, m.viewPopup(), bodyWidth, contentHeight)
	}

	statusLine := statusStyle.Render(truncate("  "+m.status, m.width))
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

// ansiTruncateTail is ansiTruncate with a "~" marker when something was cut, for
// styled content (pills, headers) where the plain truncate() would slice through
// an escape sequence and bleed color into the rest of the frame.
func ansiTruncateTail(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "~")
}

func (m model) shortcutHint() string {
	if m.yankPending && m.mode == modeNormal {
		return renderHints(yankHints(), m.width)
	}
	// An unsaved change owns the hint bar: it is the only thing that needs
	// doing, and the keys that resolve it must not compete with 20 others. The
	// resulting time rides along because the delta alone ("+1d") does not say
	// what the event ENDS UP as, and the before -> after line lives in the
	// detail pane, which a narrow window does not show.
	if m.pending != nil {
		return renderHints([]hint{
			{key: "UNSAVED", label: m.pending.label() + ": " + m.pending.diffLine(m.tz()), prio: prioPinned},
			{key: "s", label: "SAVE to Google", prio: prioEscape},
			{key: "esc", label: "discard", prio: prioEscape},
			{key: ")(}{><", label: "keep adjusting", prio: prioExtra},
		}, m.width)
	}
	// Focus inside the docked active panel: show the panel's own keys, but keep
	// naming tab/a so it never feels like a trap.
	if m.activePanelFocused() && m.mode == modeNormal {
		return renderHints([]hint{
			{key: "ACTIVE", label: "panel", prio: prioNav},
			{key: "j/k", label: "move", prio: prioNav},
			{key: "ret", label: "jump to it", prio: prioAct},
			{key: "E/X/L", label: "act on it", prio: prioExtra},
			{key: "tab", label: "next pane", prio: prioExtra},
			{key: "esc", label: "back", prio: prioEscape},
			{key: "a", label: "close", prio: prioEscape},
		}, m.width)
	}
	// Modal hints are already short and every key in them applies, so they are
	// not filtered — only fitted, so a narrow pane drops a whole hint instead
	// of slicing one in half.
	fit := func(hs ...hint) string { return renderHints(hs, m.width) }
	filter := hint{key: "type", label: "filter", prio: prioExtra}
	move := hint{key: "up/dn", label: "move", prio: prioNav}
	cancel := hint{key: "esc", label: "cancel", prio: prioEscape}
	switch m.mode {
	case modeSearch:
		return fit(hint{key: "/", label: "search", prio: prioNav}, move,
			hint{key: "ret", label: "jump", prio: prioAct}, cancel)
	case modeCalendarPicker:
		return fit(hint{key: "e", label: "calendar", prio: prioNav}, filter, move,
			hint{key: "ret", label: "open", prio: prioAct}, cancel)
	case modeLinkPicker:
		return fit(hint{key: "L", label: "links", prio: prioNav}, filter, move,
			hint{key: "ret", label: "open", prio: prioAct}, cancel)
	case modeAttendeePicker:
		return fit(hint{key: "A", label: "attendees", prio: prioNav}, move,
			hint{key: "ret", label: "open their calendar", prio: prioAct}, cancel)
	case modeHelp:
		return fit(hint{key: "help", prio: prioNav},
			hint{key: "esc/?", label: "close", prio: prioEscape})
	case modeFormHelp:
		return fit(hint{key: "form help", prio: prioNav},
			hint{key: "esc/?", label: "close", prio: prioEscape})
	default:
		// Built from what is actually usable right now, and fitted to the pane
		// by dropping whole low-priority hints — see hints.go. The old bar named
		// every key the app has (238 columns), so it was always truncated and
		// what fell off was `?` and `q`.
		return renderHints(m.normalHints(), m.width)
	case modeCreate:
		return fit(
			hint{key: "form", prio: prioNav},
			hint{key: "ret", label: "save", prio: prioAct},
			hint{key: "tab", label: "field", prio: prioNav},
			hint{key: "space", label: "toggle attendee", prio: prioExtra},
			hint{key: "?", label: "help", prio: prioEscape},
			hint{key: "esc", label: "back", prio: prioEscape},
		)
	case modeConfirmSubmit:
		return fit(hint{key: "confirm?", prio: prioNav},
			hint{key: "y/ret", label: "yes", prio: prioEscape},
			hint{key: "n/esc", label: "back to form", prio: prioEscape})
	case modeConfirmDelete:
		return fit(hint{key: "delete?", prio: prioNav},
			hint{key: "y/ret", label: "confirm", prio: prioEscape},
			hint{key: "n/esc", label: "cancel", prio: prioEscape})
	case modeOverlayPicker:
		return fit(hint{key: "overlay", prio: prioNav}, filter, move,
			hint{key: "space", label: "toggle", prio: prioAct},
			hint{key: "ret", label: "apply (none = off)", prio: prioAct}, cancel)
	case modeFindTime:
		if m.find.step == findPeople {
			return fit(hint{key: "who?", prio: prioNav}, filter, move,
				hint{key: "space", label: "toggle", prio: prioAct},
				hint{key: "ret", label: "find slots", prio: prioAct}, cancel)
		}
		return fit(
			hint{key: "slots", prio: prioNav},
			hint{key: "j/k", label: "move", prio: prioNav},
			hint{key: "ret", label: "prefill new event", prio: prioAct},
			hint{key: "d", label: "length", prio: prioExtra},
			hint{key: "w", label: "weekends", prio: prioExtra},
			hint{key: "H", label: "hours", prio: prioExtra},
			hint{key: "R", label: "refresh", prio: prioExtra},
			hint{key: "esc", label: "back to who", prio: prioEscape},
		)
	}
}
