package main

import (
	"fmt"
	"strings"
)

func (m model) viewConfirmDelete() string {
	ev := m.selectedEvent()
	w := min(max(44, m.width/2), max(36, m.width-4))
	var lines []string
	lines = append(lines, errorStyle.Render(" Delete event? "))
	lines = append(lines, "")
	if ev != nil {
		lines = append(lines, pillStyle.Render(m.timeLabel(ev))+" "+truncate(ev.Title, max(10, w-16)))
		// Name the calendar the delete will actually hit, not the one that
		// happens to be open: in an overlay these differ, and showing your own
		// name while deleting a colleague's event is exactly backwards.
		owner := displayNameForCalendar(m.eventCalendar(ev))
		ownerLine := m.viewDate(ev).Format("Mon Jan 02") + " | " + owner
		if m.overlay.on() {
			lines = append(lines, mutedStyle.Render(m.viewDate(ev).Format("Mon Jan 02")+" | on ")+
				errorStyle.Render(owner))
		} else {
			lines = append(lines, mutedStyle.Render(ownerLine))
		}
		if n := len(ev.Attendees); n > 0 {
			lines = append(lines, errorStyle.Render(fmt.Sprintf("⚠ cancellation notice will be sent to %d people", n)))
		}
	}
	lines = append(lines, "")
	lines = append(lines, mutedStyle.Render("y/Enter delete | n/ESC cancel"))
	h := min(len(lines)+2, max(6, m.height-4))
	return modalStyle.Width(w).Height(h).Render(strings.Join(lines, "\n"))
}

// popupSize returns a right-bottom modal's inner dimensions.
// popupSize returns a right-bottom modal's inner dimensions. wantRows is the
// number of list rows the caller would like to show; the modal grows to fit
// them (title + input + hint = 4 chrome rows) up to the available height.
// preferredOverlayWidth keeps picker/create/delete overlays aligned with the
// detail split. When the detail is on the right, overlays use that same width.
func (m model) preferredOverlayWidth() int {
	bodyWidth := max(20, m.width-2)
	switch m.splitMode() {
	case splitRight:
		return max(34, bodyWidth/3)
	case splitBottom:
		return bodyWidth
	default:
		return min(max(48, bodyWidth/2), max(36, bodyWidth-4))
	}
}

func (m model) popupSize(wantRows int) (int, int) {
	// Width follows the detail split so overlays feel attached to it.
	w := m.preferredOverlayWidth()
	// Height must stay within the content area (m.height-4) so the modal never
	// pushes the status/hint bars off-screen on resize.
	contentH := max(3, m.height-4)
	desired := wantRows + 4 // title + input + hint + borders
	h := min(desired, contentH)
	if h < 5 {
		h = min(5, contentH)
	}
	return w, h
}

func (m model) viewPopup() string {
	if m.mode == modeHelp {
		w, h := m.popupSize(len(helpLines()) + 1)
		inner := max(10, w-4)
		var lines []string
		lines = append(lines, sectionTitleStyle.Render(" Help "))
		lines = append(lines, "")
		for _, l := range helpLines() {
			lines = append(lines, truncate(l, inner))
		}
		lines = append(lines, "")
		lines = append(lines, mutedStyle.Render("ESC / ? close"))
		return modalStyle.Width(w).Height(h).Render(strings.Join(lines, "\n"))
	}

	if m.mode == modeFormHelp {
		w, h := m.popupSize(len(formHelpLines()) + 1)
		inner := max(10, w-4)
		var lines []string
		lines = append(lines, sectionTitleStyle.Render(" Form Help "))
		lines = append(lines, "")
		for _, l := range formHelpLines() {
			lines = append(lines, truncate(l, inner))
		}
		lines = append(lines, "")
		lines = append(lines, mutedStyle.Render("ESC / ? close  |  any field: Enter saves"))
		return modalStyle.Width(w).Height(h).Render(strings.Join(lines, "\n"))
	}

	if m.mode == modeConfirmSubmit {
		return m.viewConfirmSubmit()
	}

	if m.mode == modeSearch {
		matches := m.searchMatches()
		w, h := m.popupSize(len(matches))
		inner := max(10, w-4)
		rows := max(1, h-4)
		var lines []string
		lines = append(lines, sectionTitleStyle.Render(" Search "))
		lines = append(lines, selectedStyle.Width(inner).Render("/ "+m.input+"|"))
		if len(matches) == 0 {
			lines = append(lines, mutedStyle.Render("No matches"))
		} else {
			start := 0
			if m.searchIndex >= rows {
				start = m.searchIndex - rows + 1
			}
			for i := start; i < min(len(matches), start+rows); i++ {
				line := truncate(matches[i].label, inner-2)
				if i == m.searchIndex {
					lines = append(lines, selectedStyle.Width(inner-2).Render(line))
				} else {
					lines = append(lines, "  "+line)
				}
			}
		}
		lines = append(lines, mutedStyle.Render("up/dn move | Enter jump | ESC cancel"))
		return modalStyle.Width(w).Height(h).Render(strings.Join(lines, "\n"))
	}

	// Fuzzy pickers (links / calendars / attendees).
	items := m.picker.filtered(m.input)
	w, h := m.popupSize(len(items))
	inner := max(10, w-4)
	rows := max(1, h-4)
	var lines []string
	lines = append(lines, sectionTitleStyle.Render(" "+m.picker.title+" "))
	lines = append(lines, selectedStyle.Width(inner).Render("› "+m.input+"|"))
	if len(items) == 0 {
		if m.mode == modeCalendarPicker && strings.Contains(m.input, "@") {
			lines = append(lines, linkStyle.Render("↵ open this calendar directly: "+truncate(strings.TrimSpace(m.input), inner-4)))
		} else {
			lines = append(lines, mutedStyle.Render("No matches"))
		}
	} else {
		idx := max(0, min(m.picker.index, len(items)-1))
		start := 0
		if idx >= rows {
			start = idx - rows + 1
		}
		for i := start; i < min(len(items), start+rows); i++ {
			line := truncate(items[i].label, inner-2)
			if i == idx {
				lines = append(lines, selectedStyle.Width(inner-2).Render(line))
			} else {
				lines = append(lines, "  "+line)
			}
		}
	}
	lines = append(lines, mutedStyle.Render("type filter | up/dn move | Enter select | ESC cancel"))
	return modalStyle.Width(w).Height(h).Render(strings.Join(lines, "\n"))
}
