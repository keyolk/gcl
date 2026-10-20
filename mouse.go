package main

import (
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/toast"
)

// Mouse is an augmentation of the keyboard, not a second command set: every
// gesture below resolves to an action from the binding table and runs through
// run, so a click does what the key it stands for does.
//
//   - agenda row, grid day, grid day-detail row, active-panel row: focus that
//     pane and select the item; a second click on the selected item is Enter
//     (open, or panel.jump in the active panel).
//   - wheel over a pane: focus it and move the selection three rows (list.down,
//     detail.down, panel.down), or one week per notch over a grid, where the
//     keys have no three-day step (grid.down).
//   - help, pickers, search, confirm steps: they own the mouse while open. The
//     wheel scrolls or moves their cursor, a click outside runs the overlay's
//     own esc action, and a click on a picker or search row selects it (a
//     second click is Enter). Nothing reaches the schedule underneath.
//   - the create/edit form: swallowed. Esc there discards what was typed, so a
//     stray click must not do it.
//
// Staged time changes (pending.go) are never saved or discarded by the mouse:
// there is no gesture for `s` or `esc`. Selecting another event while one is
// staged is what the keys already allow, and the staged change stays put.
func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.width == 0 || m.height == 0 {
		return m, nil
	}
	if m.mode != modeNormal {
		return m.modalMouse(msg)
	}
	// A pending y owns the next input, exactly as it owns the next key.
	if m.yankPending || m.bodyIsPlaceholder() {
		return m, nil
	}
	l := m.paneLayout()
	body := l.bodyRect()
	at := func(r mouse.Rect) mouse.Rect { return clipRect(r, body) }

	if d := mouse.Delta(msg); d != 0 {
		return m.wheel(l, at, msg, d)
	}
	if !mouse.IsLeftPress(msg) {
		return m, nil
	}
	// A click is a keystroke for toasts: an error is acknowledged by it.
	m.toasts = toast.OnKey(m.toasts)

	switch {
	case at(l.active).Contains(msg.X, msg.Y):
		return m.clickActive(l, msg)
	case at(l.detail).Contains(msg.X, msg.Y):
		if m.view == viewList {
			return m, nil // the list view's detail card is read-only
		}
		return m.clickDayDetail(l, msg)
	case at(l.schedule).Contains(msg.X, msg.Y):
		if m.view == viewList {
			return m.clickAgenda(l, at(l.schedule), msg)
		}
		return m.clickGrid(l, at(l.schedule), msg)
	}
	return m, nil
}

// wheel moves the selection of the pane under the pointer, focusing it first.
func (m model) wheel(l paneLayout, at func(mouse.Rect) mouse.Rect, msg tea.MouseMsg, d int) (tea.Model, tea.Cmd) {
	steps, down := d, true
	if d < 0 {
		steps, down = -d, false
	}
	pick := func(downAct, upAct string) string {
		if down {
			return downAct
		}
		return upAct
	}
	switch {
	case at(l.active).Contains(msg.X, msg.Y):
		m.focusPane = focusActive
		m.clampActiveIndex()
		return m.runN(pick("panel.down", "panel.up"), steps)
	case at(l.detail).Contains(msg.X, msg.Y):
		if m.view == viewList {
			return m, nil
		}
		m.focusPane = focusDetail
		m.clampGridDetail()
		return m.runN(pick("detail.down", "detail.up"), steps)
	case at(l.schedule).Contains(msg.X, msg.Y):
		m.focusPane = focusMain
		if m.view == viewList {
			return m.runN(pick("list.down", "list.up"), steps)
		}
		return m.runN(pick("grid.down", "grid.up"), 1)
	}
	return m, nil
}

// runN runs one action n times, as holding the key would.
func (m model) runN(action string, n int) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	next := tea.Model(m)
	for range n {
		var c tea.Cmd
		next, c = next.(model).run(action, tea.KeyMsg{})
		cmd = tea.Batch(cmd, c)
	}
	return next, cmd
}

// clickAgenda selects the agenda row under the pointer. owner maps the rows
// View drew to events; a header, ruler, "now" divider or "~ more" row has none.
func (m model) clickAgenda(l paneLayout, area mouse.Rect, msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	_, owner := m.agendaLines(l.schedule.W, l.schedule.H)
	hit := mouse.RowHit(area, 0, len(owner), msg)
	if !hit.HasItem || owner[hit.Index] < 0 {
		return m.noTarget()
	}
	idx := owner[hit.Index]
	ev := &m.events[idx]
	again := m.focusPane == focusMain && m.selected == idx
	m.focusPane = focusMain
	m.selectEvent(idx)
	if m.pressed("event:"+eventKey(ev)) && again {
		return m.run("open", tea.KeyMsg{})
	}
	return m, nil
}

// clickGrid moves the focus date to the clicked day cell, as the h/j/k/l keys
// would, and reloads when that leaves the loaded window.
func (m model) clickGrid(l paneLayout, area mouse.Rect, msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	g := m.gridGeometry(l.schedule.W, l.schedule.H)
	heights := make([]int, len(g.rowsFor))
	for w, rows := range g.rowsFor {
		heights[w] = 1 + rows // the date row and its event rows
	}
	// Row 0 of the grid is the weekday header.
	weeks := mouse.Rect{X: area.X, Y: area.Y + 1, W: area.W, H: area.H - 1}
	hit := mouse.BlockHit(weeks, 0, heights, msg)
	col := (msg.X - area.X) / g.colW
	if !hit.HasItem || col < 0 || col > 6 {
		return m.noTarget()
	}
	day := g.firstWeek.AddDate(0, 0, hit.Index*7+col)
	again := m.focusPane == focusMain && sameDay(day, m.anchor)
	m.focusPane = focusMain
	var cmd tea.Cmd
	if n := daysBetween(m.anchor, day); n != 0 {
		cmd = m.moveFocusByDays(n)
	}
	if m.pressed("day:"+day.Format("2006-01-02")) && again {
		mm, c := m.run("open", tea.KeyMsg{})
		return mm, tea.Batch(cmd, c)
	}
	return m, cmd
}

// clickDayDetail selects an event in the grid's day-detail pane, as tab and
// j/k do.
func (m model) clickDayDetail(l paneLayout, msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	evs := m.focusDayEvents()
	d := l.detail
	area := clipRect(mouse.Rect{X: d.X, Y: d.Y + dayDetailHeaderRows, W: d.W, H: d.H - dayDetailHeaderRows}, l.bodyRect())
	hit := mouse.BlockHit(area, 0, dayDetailHeights(evs), msg)
	if !hit.HasItem {
		return m.noTarget()
	}
	again := m.focusPane == focusDetail && m.gridDetail == hit.Index
	m.focusPane = focusDetail
	m.gridDetail = hit.Index
	m.clampGridDetail()
	if m.pressed("detail:"+eventKey(evs[hit.Index])) && again {
		return m.run("open", tea.KeyMsg{})
	}
	return m, nil
}

// clickActive selects an event in the docked active-now panel, as tab and j/k
// do; a second click is panel.jump (Enter).
func (m model) clickActive(l paneLayout, msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	evs := m.activeEvents(time.Now())
	if len(evs) == 0 {
		return m.noTarget()
	}
	cur := max(0, min(m.activeIndex, len(evs)-1))
	start, visible := activePanelWindow(l.activeH, cur)
	heights := make([]int, len(evs))
	for i := range heights {
		heights[i] = activePanelRowsPerEvent
	}
	a := l.active
	// Row 0 of the panel is its title line.
	area := clipRect(mouse.Rect{X: a.X, Y: a.Y + 1, W: a.W, H: a.H - 1}, l.bodyRect())
	hit := mouse.BlockHit(area, start, heights, msg)
	// Past the last drawn event is the "+N more active" line, not an event.
	if !hit.HasItem || hit.Index >= start+visible {
		return m.noTarget()
	}
	again := m.activePanelFocused() && cur == hit.Index
	m.focusPane = focusActive
	m.activeIndex = hit.Index
	m.syncSelectionToActive()
	if m.pressed("active:"+eventKey(evs[hit.Index])) && again {
		return m.run("panel.jump", tea.KeyMsg{})
	}
	return m, nil
}

// modalMouse gives the open overlay the mouse: nothing reaches the schedule.
func (m model) modalMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	var down, up, closeAct string
	switch m.mode {
	case modeHelp:
		down, up, closeAct = "help.down", "help.up", "help.close"
	case modeFormHelp:
		down, up, closeAct = "help.down", "help.up", "formhelp.close"
	case modeConfirmDelete:
		closeAct = "delete.no"
	case modeConfirmSubmit:
		closeAct = "submit.no"
	case modeSearch:
		down, up, closeAct = "search.down", "search.up", "search.cancel"
	case modeCalendarPicker, modeLinkPicker, modeAttendeePicker:
		down, up, closeAct = "picker.down", "picker.up", "picker.cancel"
	case modeOverlayPicker:
		down, up, closeAct = "overlay.down", "overlay.up", "overlay.cancel"
	case modeFindTime:
		if m.find.step == findPeople {
			down, up, closeAct = "people.down", "people.up", "people.cancel"
		} else {
			down, up, closeAct = "slots.down", "slots.up", "slots.back"
		}
	default:
		// The create/edit form: see handleMouse for why it is swallowed.
		return m, nil
	}

	if d := mouse.Delta(msg); d != 0 {
		if down == "" {
			return m, nil
		}
		if d < 0 {
			return m.runN(up, -d)
		}
		return m.runN(down, d)
	}
	if !mouse.IsLeftPress(msg) {
		return m, nil
	}
	m.toasts = toast.OnKey(m.toasts)
	rect := m.modalRect()
	if !rect.Contains(msg.X, msg.Y) {
		return m.run(closeAct, tea.KeyMsg{})
	}
	switch m.mode {
	case modeSearch, modeCalendarPicker, modeLinkPicker, modeAttendeePicker:
		return m.clickPopupRow(rect, msg)
	}
	return m, nil
}

// clickPopupRow selects the clicked entry of the search or a picker popup, as
// up/down do; a second click is Enter. Both draw a title and an input line
// above the entries.
func (m model) clickPopupRow(rect mouse.Rect, msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	var n, cur int
	var keyAt func(int) string
	var choose string
	if m.mode == modeSearch {
		matches := m.searchMatches()
		n, cur, choose = len(matches), m.searchIndex, "search.jump"
		keyAt = func(i int) string { return "match:" + strconv.Itoa(matches[i].eventIndex) }
	} else {
		items := m.picker.filtered(m.input)
		n, choose = len(items), "picker.choose"
		cur = max(0, min(m.picker.index, n-1))
		keyAt = func(i int) string { return "item:" + items[i].value + "|" + items[i].label }
	}
	rows := m.popupListRows(n)
	w, _ := m.popupSize(n)
	inner := max(10, w-4)
	start := popupListStart(cur, rows)
	// The popup draws a title line and the query box above the entries, and
	// the selected entry is a taller bordered box: measure what is drawn.
	label := func(i int) string {
		if m.mode == modeSearch {
			return m.searchMatches()[i].label
		}
		return m.picker.filtered(m.input)[i].label
	}
	heights := make([]int, min(n, start+rows))
	for i := start; i < len(heights); i++ {
		heights[i] = lipgloss.Height(popupEntry(label(i), i == cur, inner))
	}
	prompt := "› "
	if m.mode == modeSearch {
		prompt = "/ "
	}
	above := 1 + lipgloss.Height(popupInput(prompt, m.input, inner))
	rowsH := 0
	for _, h := range heights {
		rowsH += h
	}
	area := mouse.Rect{
		X: rect.X + modalStyle.GetBorderLeftSize() + modalStyle.GetPaddingLeft(),
		Y: rect.Y + modalStyle.GetBorderTopSize() + modalStyle.GetPaddingTop() + above,
		W: rect.W - modalStyle.GetHorizontalFrameSize(),
		H: rowsH,
	}
	hit := mouse.BlockHit(area, start, heights, msg)
	if !hit.HasItem {
		return m.noTarget()
	}
	again := cur == hit.Index
	if m.mode == modeSearch {
		m.searchIndex = hit.Index
	} else {
		m.picker.index = hit.Index
	}
	if m.pressed(keyAt(hit.Index)) && again {
		return m.run(choose, tea.KeyMsg{})
	}
	return m, nil
}

// noTarget is a click that landed on nothing selectable: it changes nothing,
// and it breaks a double click in progress.
func (m model) noTarget() (tea.Model, tea.Cmd) {
	m.clicks = mouse.Clicks{}
	return m, nil
}

// pressed records a press on target key and reports whether it completes a
// double click.
func (m *model) pressed(key string) bool {
	var double bool
	m.clicks, double = m.clicks.Press(key, m.clock())
	return double
}

func (m model) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// eventKey identifies an event across reloads for double-click detection: its
// calendar and id, since an overlay merges calendars. An event with no id (a
// TSV row) falls back to its title and start.
func eventKey(ev *Event) string {
	if ev.ID != "" {
		return ev.Calendar + "/" + ev.ID
	}
	return ev.Calendar + "/" + ev.Title + "@" + ev.StartDate.Format("2006-01-02") + ev.StartTime
}

// clipRect cuts r to the part inside bound, so a pane that overruns the body
// on a tiny terminal does not claim the status bars' rows.
func clipRect(r, bound mouse.Rect) mouse.Rect {
	x0, y0 := max(r.X, bound.X), max(r.Y, bound.Y)
	x1, y1 := min(r.X+r.W, bound.X+bound.W), min(r.Y+r.H, bound.Y+bound.H)
	if x1 <= x0 || y1 <= y0 {
		return mouse.Rect{}
	}
	return mouse.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// daysBetween is the whole calendar days from a to b, by date: the two may
// carry different locations or times of day, and an hour difference across a
// DST change must not turn into a day.
func daysBetween(a, b time.Time) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return int(time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC).Sub(time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)) / (24 * time.Hour))
}
