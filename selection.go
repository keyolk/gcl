package main

import (
	"fmt"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/selection"
)

// Drag selection (E14): a drag selects text inside the pane under the press
// and the release copies it, the way the mod engine selects. Mouse capture
// turned the terminal's own drag-select off, and a Meet link, a room or an
// attendee address is what users most want to copy out of a calendar.
//
//   - the active panel, the agenda or grid, and the grid's day-detail list are
//     List panes: a second click on the selected item stays Enter.
//   - the list view's detail card is read-only text, so it is a Text pane:
//     double click picks a word, triple click a line. So is the first-load
//     error that replaces the body.
//   - an open overlay is the only thing selectable. Its content is a Text
//     pane; search and picker entries are a List pane on top (second click
//     chooses). Help is a Text pane with all of its lines, so a drag past its
//     edge scrolls it through help.down / help.up, as the wheel does.
//   - a drag in the form or a query never moves the cursor or edits: the
//     selection consumes it before the overlay sees anything.

// selectionMouse feeds msg to the selection before the click handler. A press
// that does not move is left to handleMouse, so click-to-select still works;
// only what the selection consumed (a drag, its release, a word pick) skips it.
func (m model) selectionMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	r := m.textSel.Mouse(msg, m.selPanes(), m.View(), m.clock())
	var cmds []tea.Cmd
	next := tea.Model(m)
	if r.Scroll != 0 && r.ScrollPane == helpPaneID {
		var c tea.Cmd
		if r.Scroll < 0 {
			next, c = m.runN("help.up", -r.Scroll)
		} else {
			next, c = m.runN("help.down", r.Scroll)
		}
		cmds = append(cmds, c)
	}
	if r.Copy != "" {
		cmds = append(cmds, selection.Copy(r.Copy))
	}
	if r.Consumed {
		return next, tea.Batch(cmds...)
	}
	next, cmd := next.(model).handleMouse(msg)
	return next, tea.Batch(append(cmds, cmd)...)
}

// copied reports the clipboard write: a copy is silent otherwise, and a
// failed one (no pbcopy, no /dev/tty) must not look like a success.
func (m model) copied(msg selection.CopiedMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.fail(srcStatus, "copy failed: "+msg.Err.Error())
	} else {
		m.say(fmt.Sprintf("copied %d chars", utf8.RuneCountInString(msg.Text)))
	}
	return m, nil
}

const helpPaneID = "help"

// selPanes lists the selectable regions in drawing order, from the numbers
// View and the click handlers use (paneLayout, modalRect, popupRows).
func (m model) selPanes() []selection.Pane {
	if m.width <= 0 || m.height <= 0 {
		return nil
	}
	l := m.paneLayout()
	body := l.bodyRect()
	if m.mode != modeNormal {
		return m.modalPanes(body)
	}
	if m.bodyIsPlaceholder() {
		return []selection.Pane{{ID: "body", Rect: body, Kind: selection.Text}}
	}
	var panes []selection.Pane
	add := func(id string, r mouse.Rect, k selection.Kind) {
		if r = clipRect(r, body); r.W > 0 && r.H > 0 {
			panes = append(panes, selection.Pane{ID: id, Rect: r, Kind: k})
		}
	}
	add("active", l.active, selection.List)
	add("schedule", l.schedule, selection.List)
	detail := selection.List // the grid's day detail: rows open on a second click
	if m.view == viewList {
		detail = selection.Text // the agenda's detail card is read-only
	}
	add("detail", l.detail, detail)
	return panes
}

// modalPanes is the open overlay's content, then its entries (search and
// pickers) or its scrolling help page, listed last so they own their area.
func (m model) modalPanes(body mouse.Rect) []selection.Pane {
	rect := m.modalRect()
	if rect.W == 0 {
		return nil
	}
	c := clipRect(modalContent(rect), body)
	panes := []selection.Pane{{ID: "modal", Rect: c, Kind: selection.Text}}
	switch m.mode {
	case modeHelp, modeFormHelp:
		panes = append(panes, m.helpPane(c))
	case modeSearch, modeCalendarPicker, modeLinkPicker, modeAttendeePicker:
		if area, _, heights, _ := m.popupRows(rect); len(heights) > 0 {
			if area = clipRect(area, c); area.H > 0 {
				panes = append(panes, selection.Pane{ID: "modal-rows", Rect: area, Kind: selection.List})
			}
		}
	}
	return panes
}

// helpLinesAll is a page height no help reaches: renderHelpPage then returns
// every line, with no "… N more" marker.
const helpLinesAll = 1 << 16

// helpPane is the help page inside the modal content c, with every help line.
// viewPopup draws a title and a blank line above the page, then the page
// (renderHelpPage from helpOff), so the full list at offset 0 lines up with
// it row for row.
func (m model) helpPane(c mouse.Rect) selection.Pane {
	w, rows := m.helpBodySize()
	all, _ := m.renderHelpPage(w, helpLinesAll, 0)
	if len(all)-m.helpOff > rows {
		// RenderHelp spends the last row on "… N more" while lines remain
		// below. That row is not a help line, so it is left out: a drag onto
		// it scrolls instead of selecting the hidden line under the marker.
		rows--
	}
	const titleRows = 2 // the title and the blank line under it
	r := clipRect(mouse.Rect{X: c.X, Y: c.Y + titleRows, W: w, H: rows}, c)
	return selection.Pane{ID: helpPaneID, Rect: r, Kind: selection.Text, Lines: all, Offset: m.helpOff}
}

// selectionStyle is reverse video on the theme's renderer, which keeps
// attributes under NO_COLOR, so the selection never disappears there.
func selectionStyle() lipgloss.Style {
	if r := ui.Renderer(); r != nil {
		return r.NewStyle().Reverse(true)
	}
	return lipgloss.NewStyle().Reverse(true)
}
