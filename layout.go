package main

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/mouse"
)

// paneLayout is where View puts the body panes. View draws from it and the
// mouse handler hit-tests against it, so a click lands on what is on screen
// and the two cannot drift apart.
//
// Rects are in screen cells: the header takes row 0, so the body starts at
// row 1. The rects keep the size View renders each pane at, so a pane that
// overruns the body (a tiny terminal) is not clipped here; the body clamp in
// View cuts it, and bodyRect lets a hit test do the same.
type paneLayout struct {
	bodyW, contentH int
	activeH         int // rows the docked active panel takes off the top
	scheduleH       int // rows left for the schedule (and its detail pane)
	legendH         int // the overlay legend row under the schedule
	split           splitKind
	splitArea       mouse.Rect // the schedule and detail panes together, as the split cuts it
	divider         mouse.Rect // the split's divider; zero when there is none
	active          mouse.Rect // the docked active-now panel; zero when closed
	schedule        mouse.Rect // the list agenda or grid
	detail          mouse.Rect // the detail pane; zero when there is none
}

func (m model) paneLayout() paneLayout {
	var l paneLayout
	l.contentH = max(4, m.height-2-m.statusRows())
	l.bodyW = max(20, m.width-2)
	// The docked active panel takes rows off the TOP of the body: "what is in
	// effect now" belongs above "what is coming", and a top dock keeps the
	// schedule's own scroll position undisturbed when the panel toggles.
	l.activeH = m.activePanelHeight(l.contentH)
	l.scheduleH = max(3, l.contentH-l.activeH)
	// The overlay legend is the only thing that maps a dot color to a person,
	// so it costs a permanent row while an overlay is on — dots without a key
	// are decoration.
	if m.overlay.on() && !m.loading {
		l.legendH = 1
		l.scheduleH = max(3, l.scheduleH-1)
	}
	l.split = m.splitMode()
	top := 1 + l.activeH
	if l.activeH > 0 {
		l.active = mouse.Rect{X: 0, Y: 1, W: l.bodyW, H: l.activeH}
	}
	// The schedule and detail panes. The detail pane splits to the RIGHT on
	// wide panes and to the BOTTOM on tall/narrow ones.
	// The boundary between them is the split's (panesplit.go): View draws
	// these rects and the mouse, selection and resize keys use the same ones.
	l.splitArea = mouse.Rect{X: 0, Y: top, W: l.bodyW, H: l.scheduleH}
	if l.split == splitNone {
		l.schedule = l.splitArea
		return l
	}
	l.schedule, l.divider, l.detail = m.paneSplit(l.split).Layout(l.splitArea)
	return l
}

// bodyRect is the screen area View's final clamp keeps: everything below it is
// the status and hint bars.
func (l paneLayout) bodyRect() mouse.Rect {
	return mouse.Rect{X: 0, Y: 1, W: l.bodyW, H: l.contentH}
}

// bodyIsPlaceholder reports that View replaces the body with the loading card
// or the first-load error: nothing clickable is on screen then.
func (m model) bodyIsPlaceholder() bool {
	return !m.hasLoaded() && (m.loading || m.err != nil)
}

// modalView is the floating overlay for the current mode, or "" in the
// schedule. View draws it and modalRect measures it, so the mouse handler
// knows the overlay's box without recomputing its size.
func (m model) modalView() string {
	switch m.mode {
	case modeNormal:
		return ""
	case modeCreate:
		return m.viewCreate()
	case modeFindTime:
		return m.viewFindTime()
	case modeOverlayPicker:
		return m.viewOverlayPicker()
	case modeConfirmDelete:
		return m.viewConfirmDelete()
	}
	return m.viewPopup()
}

// modalRect is the screen rect overlayBottomRight gives the modal: anchored to
// the body's bottom-right corner, one cell in from the right edge.
func (m model) modalRect() mouse.Rect {
	modal := m.modalView()
	if modal == "" {
		return mouse.Rect{}
	}
	l := m.paneLayout()
	w, h := lipgloss.Size(modal)
	col := max(0, l.bodyW-w-1)
	row := max(0, l.contentH-h)
	return mouse.Rect{X: col, Y: 1 + row, W: w, H: min(h, l.contentH)}
}
