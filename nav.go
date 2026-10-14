package main

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// hasLoaded reports whether a fetch has ever landed for the current source, i.e.
// whether there is a real agenda to keep on screen while reloading.
func (m model) hasLoaded() bool {
	return !m.loadedStart.IsZero()
}

// dropLoadedEvents forgets the loaded agenda. Call it when the event SOURCE
// changes (another calendar, overlay on/off): the old events belong to a
// different calendar, so keeping them through the reload — or through a failed
// reload — would show them under the new calendar's name.
func (m *model) dropLoadedEvents() {
	m.events = nil
	m.selected = 0
	m.loadedStart = time.Time{}
	m.loadedEnd = time.Time{}
}

// joinNote appends note to an existing status note with the usual separator.
func joinNote(existing, note string) string {
	if existing == "" {
		return note
	}
	return existing + " · " + note
}

// reload marks loading and returns the fetch command.
func (m *model) reload() tea.Cmd {
	return m.scheduleFetch()
}

// scheduleFetch debounces bursty navigation. The request id is bumped on every
// call; only the latest tick is allowed to issue an actual loadCmd.
func (m *model) scheduleFetch() tea.Cmd {
	m.nextReqID++
	m.inflightReq = m.nextReqID
	m.loading = true
	m.status = "loading~"
	return m.loadCmd(m.inflightReq)
}

// moveFocus shifts the focus date by the current jump unit (list) or by a day
// (grid), reloading if the new date leaves the loaded window.
func (m *model) moveFocus(direction int) tea.Cmd {
	switch m.view {
	case viewWeek, viewMonth:
		m.anchor = m.anchor.AddDate(0, 0, direction)
	default:
		switch m.jumpUnit {
		case "week":
			m.anchor = m.anchor.AddDate(0, 0, 7*direction)
		case "month":
			m.anchor = m.anchor.AddDate(0, direction, 0)
		default:
			m.anchor = m.anchor.AddDate(0, 0, direction)
		}
	}
	// Snap list selection to the first event on/after the new focus date.
	m.selected = m.firstEventIndexOnOrAfter(m.anchor)
	moved := m.keepGridStable()
	if moved || !m.anchorLoaded() {
		return m.reload()
	}
	return nil
}

// moveFocusByDays shifts the focus date by a fixed number of days, reloading
// only when the new date leaves the loaded window (avoids grid flicker on j/k).
func (m *model) moveFocusByDays(days int) tea.Cmd {
	m.anchor = m.anchor.AddDate(0, 0, days)
	m.selected = m.firstEventIndexOnOrAfter(m.anchor)
	moved := m.keepGridStable()
	if moved || !m.anchorLoaded() {
		return m.reload()
	}
	return nil
}

// resetGridTop anchors the visible grid window to the week (week view) or month
// (month view) containing the focus date.
func (m *model) resetGridTop() {
	if m.view == viewMonth {
		m.gridTop = weekStart(monthStart(m.anchor))
	} else {
		m.gridTop = weekStart(m.anchor)
	}
}

// cycleFocus advances `tab` through the panes that currently exist: the
// schedule, the grid's day-detail pane (grid views only), and the docked active
// panel (only while it is open). Panes that are not on screen are skipped, so
// tab never lands somewhere invisible.
func (m *model) cycleFocus() {
	order := []paneFocus{focusMain}
	if m.view != viewList {
		order = append(order, focusDetail)
	}
	if m.activeOpen {
		order = append(order, focusActive)
	}
	if len(order) == 1 {
		m.status = "nothing else to focus (a opens the active panel)"
		return
	}
	at := 0
	for i, f := range order {
		if f == m.focusPane {
			at = i
			break
		}
	}
	m.focusPane = order[(at+1)%len(order)]
	switch m.focusPane {
	case focusDetail:
		m.clampGridDetail()
		m.status = "focus: day detail"
	case focusActive:
		// Entering the panel puts the schedule selection on the cursor's event
		// straight away, so the acting keys are consistent from the first frame.
		m.clampActiveIndex()
		m.syncSelectionToActive()
		m.status = "focus: active panel · j/k move · Enter jump · esc back"
	default:
		m.status = "focus: schedule"
	}
}

// keepGridStable keeps the visible grid window fixed while the focus moves; it
// only re-anchors (scrolls) when the focus leaves the window. Returns true when
// the window (gridTop) actually moved, so the caller can trigger a reload.
func (m *model) keepGridStable() bool {
	if m.view == viewList {
		return false
	}
	if m.gridTop.IsZero() {
		m.resetGridTop()
		return true
	}
	weeks, _ := m.gridLayout(max(4, m.height-4))
	fw := weekStart(m.anchor)
	old := m.gridTop
	delta := int(fw.Sub(m.gridTop).Hours()) / (24 * 7)
	if delta < 0 {
		m.gridTop = fw
	} else if delta >= weeks {
		m.gridTop = fw.AddDate(0, 0, -7*(weeks-1))
	}
	return !m.gridTop.Equal(old)
}

// syncAnchorToSelected keeps the focus date aligned with the selected event so
// the header date reflects what's highlighted.
func (m *model) syncAnchorToSelected() {
	if ev := m.selectedEvent(); ev != nil {
		m.anchor = m.viewDate(ev)
	}
}

func (m model) firstEventIndexOnOrAfter(day time.Time) int {
	for i := range m.events {
		if !m.viewDate(&m.events[i]).Before(day) {
			return i
		}
	}
	return max(0, len(m.events)-1)
}

// eventSortInstant is the moment used to order an event against "now": the real
// start for timed events, midnight for all-day ones. Must match the divider
// placement in viewAgendaCards so `n` lands exactly on the "now" line.
func eventSortInstant(ev *Event) time.Time {
	if ev.AllDay() {
		return ev.StartDate
	}
	if ev.StartAt.IsZero() {
		// Defensive: a timed event whose absolute instant never got filled in
		// would otherwise sort as year 0001 and drag the "now" divider with it.
		if s, _ := tsvInstants(ev); !s.IsZero() {
			return s
		}
		return ev.StartDate
	}
	return ev.StartAt
}

// firstEventIndexAtOrAfterNow returns the first event starting at/after the
// current instant - the event just below the "now" divider. Falls back to the
// last event when everything is in the past.
func (m model) firstEventIndexAtOrAfterNow() int {
	now := time.Now()
	for i := range m.events {
		if !eventSortInstant(&m.events[i]).Before(now) {
			return i
		}
	}
	return max(0, len(m.events)-1)
}

func (m model) selectedEvent() *Event {
	if len(m.events) == 0 {
		return nil
	}
	// Grid views have no per-event cursor; the "current" event is the first one
	// on the focused day (so A/L/E/x/detail act on that day).
	if m.view != viewList {
		for i := range m.events {
			if sameDay(m.viewDate(&m.events[i]), m.anchor) {
				return &m.events[i]
			}
		}
		return nil
	}
	idx := max(0, min(m.selected, len(m.events)-1))
	return &m.events[idx]
}

// focusDayEvents returns all events on the focused day (grid detail pane).
func (m model) focusDayEvents() []*Event {
	var out []*Event
	for i := range m.events {
		if sameDay(m.viewDate(&m.events[i]), m.anchor) {
			out = append(out, &m.events[i])
		}
	}
	return out
}

// currentActionEvent is the event that A/L/E/x/Enter operate on.
// List view -> the selected agenda event.
// Grid view -> the selected event inside the detail pane when it has focus,
// otherwise the first event of the focused day.
// The active-now panel does not need a branch here: its action keys move the
// real selection onto the panel's event before falling through to the dispatch.
func (m model) currentActionEvent() *Event {
	if m.view == viewList {
		return m.selectedEvent()
	}
	evs := m.focusDayEvents()
	if len(evs) == 0 {
		return nil
	}
	idx := 0
	if m.focusPane == focusDetail {
		idx = max(0, min(m.gridDetail, len(evs)-1))
	}
	return evs[idx]
}

// clampGridDetail keeps the detail selection inside the focused day's range.
func (m *model) clampGridDetail() {
	n := len(m.focusDayEvents())
	if n == 0 {
		m.gridDetail = 0
		return
	}
	m.gridDetail = max(0, min(m.gridDetail, n-1))
}

// openPrimary always opens the Google Calendar event page. Other links (zoom,
// docs, ~) are only reachable via the L links picker.
func (m model) openPrimary() tea.Cmd {
	ev := m.currentActionEvent()
	if ev == nil {
		m.status = "no event selected"
		return nil
	}
	if ev.HTMLLink != "" {
		return openCmd(ev.HTMLLink)
	}
	// Fall back to any Google Calendar link found among the parsed links.
	for _, l := range ev.Links {
		if strings.Contains(l, "google.com/calendar/event") {
			return openCmd(l)
		}
	}
	m.status = "no Google Calendar link for this event (use L for other links)"
	return nil
}

// loadRange returns the [start, end) date window to fetch for the current view.
func (m model) loadRange() (time.Time, time.Time) {
	switch m.view {
	case viewWeek, viewMonth:
		// Anchor the loaded window to the visible grid top with generous padding
		// so scrolling within the grid rarely triggers a reload.
		top := m.gridTop
		if top.IsZero() {
			top = weekStart(m.anchor)
			if m.view == viewMonth {
				top = weekStart(monthStart(m.anchor))
			}
		}
		s := top.AddDate(0, 0, -7*4)
		return s, s.AddDate(0, 0, 7*20) // ~20 weeks window
	default: // viewList
		return m.anchor.AddDate(0, 0, -2), m.anchor.AddDate(0, 0, 45)
	}
}

// anchorLoaded reports whether the focus date is already covered by the loaded
// event window. Prefer this over recomputing from the current anchor after the
// anchor changes, which can lead to false negatives/positives when switching views.
func (m model) anchorLoaded() bool {
	return !m.loadedStart.IsZero() && !m.anchor.Before(m.loadedStart) && m.anchor.Before(m.loadedEnd)
}

// weekStart returns the Sunday on/before t.
func weekStart(t time.Time) time.Time {
	t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	return t.AddDate(0, 0, -int(t.Weekday()))
}

func monthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.Local)
}

func today() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
}

func sameDay(a, b time.Time) bool {
	y1, m1, d1 := a.Date()
	y2, m2, d2 := b.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}
