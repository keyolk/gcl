package main

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/toast"
)

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// ctrl+c quits from anywhere, ahead of every mode — including the create
	// form and the pickers, which otherwise swallow keys. It bypasses the
	// unsaved-nudge warning that `q` goes through: ctrl+c is the abort, and a
	// staged nudge lives in memory only, so nothing on the calendar changes.
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	// A key press is how a sticky error is acknowledged; done before the
	// handler so an error this key causes is not cleared by the same key.
	m.toasts = toast.OnKey(m.toasts)

	// Under a Korean input source the shortcut keys arrive as jamo (`q` → `ㅂ`).
	// Rewrite them to the Latin key at the same physical position so shortcuts
	// fire without switching the input source back. Skipped in text-entry modes,
	// where the jamo is the intended input.
	if !m.isInTextInput() {
		msg = normalizeCJKKey(msg)
	}
	key := msg.String()

	if m.mode == modeCreate {
		return m.handleCreateKey(msg)
	}

	if m.mode == modeFindTime {
		return m.handleFindKey(msg)
	}

	if m.mode == modeOverlayPicker {
		return m.handleOverlayKey(msg)
	}

	if m.mode == modeConfirmSubmit {
		switch key {
		case "y", "Y", "enter":
			c := &m.create
			// Finalize the attendee set now (was deferred from the form
			// Enter) so the confirmation summary shows the real count.
			c.finalizeAttendees()
			c.submitting = true
			c.err = ""
			m.say("creating~")
			return m, m.submitCreateCmd()
		case "n", "N", "esc":
			// Return to the form, not to normal mode, so an in-progress
			// edit isn't lost. The form's field text and cursor are preserved.
			m.mode = modeCreate
			return m, nil
		}
		return m, nil
	}

	if m.mode == modeConfirmDelete {
		switch key {
		case "y", "Y", "enter":
			ev := m.selectedEvent()
			if ev == nil || ev.ID == "" {
				m.mode = modeNormal
				return m, nil
			}
			// In an overlay the row may belong to someone else's calendar;
			// deleting against m.calendar would 404 or, worse, hit a
			// same-titled event on the wrong calendar.
			cal := m.eventCalendar(ev)
			id := ev.ID
			notify := len(ev.Attendees) > 0
			// Snapshot before deleting so `u` can recreate the event.
			restore := undoSnapshot(ev, cal, undoDelete)
			m.say("deleting~")
			return m, func() tea.Msg {
				err := deleteEvent(cal, id, notify)
				if err != nil {
					return deletedMsg{err: err}
				}
				return deletedMsg{restore: restore}
			}
		default: // n / N / esc / anything else cancels
			m.mode = modeNormal
			m.say("delete cancelled")
		}
		return m, nil
	}

	// Search overlay: fuzzy-jump across the loaded events.
	if m.mode == modeSearch {
		switch key {
		case "esc":
			m.mode = modeNormal
			m.input = ""
			m.searchIndex = 0
		case "enter":
			matches := m.searchMatches()
			if len(matches) > 0 {
				idx := matches[max(0, min(m.searchIndex, len(matches)-1))].eventIndex
				m.selected = idx
				m.anchor = m.viewDate(&m.events[idx])
				if m.view != viewList {
					m.focusPane = focusMain
					m.gridDetail = 0
					m.keepGridStable()
				}
				m.say("jumped to " + m.events[m.selected].Title)
			}
			m.mode = modeNormal
			m.input = ""
			m.searchIndex = 0
		case "backspace", "ctrl+h":
			if m.input != "" {
				_, size := utf8.DecodeLastRuneInString(m.input)
				m.input = m.input[:len(m.input)-size]
				m.searchIndex = 0
			}
		case "down", "ctrl+n", "ctrl+j":
			m.searchIndex = min(m.searchIndex+1, max(0, len(m.searchMatches())-1))
		case "up", "ctrl+p", "ctrl+k":
			m.searchIndex = max(0, m.searchIndex-1)
		default:
			if key == " " || key == "space" {
				m.input += " "
				m.searchIndex = 0
			} else if isTypedRune(msg) {
				m.input += msg.String()
				m.searchIndex = 0
			}
		}
		return m, nil
	}

	// Fuzzy picker overlays: links / calendars / attendees.
	if m.mode == modeCalendarPicker || m.mode == modeLinkPicker || m.mode == modeAttendeePicker {
		items := m.picker.filtered(m.input)
		switch key {
		case "esc":
			m.mode = modeNormal
			m.input = ""
			return m, nil
		case "enter":
			if len(items) > 0 {
				return m.choosePicker(items[max(0, min(m.picker.index, len(items)-1))])
			}
			// Calendar picker: no candidate matched, but the user typed a raw
			// email/calendar id - open it directly instead of doing nothing.
			if m.mode == modeCalendarPicker {
				raw := strings.TrimSpace(m.input)
				if strings.Contains(raw, "@") {
					return m.choosePicker(pickerItem{label: raw, value: raw})
				}
			}
			return m, nil
		case "backspace", "ctrl+h":
			if m.input != "" {
				_, size := utf8.DecodeLastRuneInString(m.input)
				m.input = m.input[:len(m.input)-size]
				m.picker.index = 0
			}
			return m, nil
		case "down", "ctrl+n", "ctrl+j":
			m.picker.index = min(m.picker.index+1, max(0, len(items)-1))
			return m, nil
		case "up", "ctrl+p", "ctrl+k":
			m.picker.index = max(0, m.picker.index-1)
			return m, nil
		default:
			if key == " " || key == "space" {
				m.input += " "
				m.picker.index = 0
			} else if isTypedRune(msg) {
				m.input += msg.String()
				m.picker.index = 0
			}
			return m, nil
		}
	}

	// Help overlay: close with esc / enter / ? (q is reserved for quitting).
	if m.mode == modeHelp {
		if key == "esc" || key == "enter" || key == "?" {
			m.mode = modeNormal
		}
		return m, nil
	}
	// Form help overlay: close back to the form (not to normal mode, so
	// an in-progress edit isn't lost). ? toggles back to the form too.
	if m.mode == modeFormHelp {
		if key == "esc" || key == "enter" || key == "?" {
			m.mode = modeCreate
		}
		return m, nil
	}
	// A pending yank prefix owns the next key before quick actions get a chance
	// to interpret it (notably, `ys` copies start instead of saving a staged move).
	if m.yankPending {
		return m.handleYankKey(key)
	}

	// Active-now panel, while focus is inside it. The panel is docked, not modal:
	// it only reaches this block after `tab` moved focus into it, and only the
	// keys that mean something *in the list* are claimed. Everything else
	// (view switches, `n`, `/`, `R`, `Z`, `N`, quick actions) falls through to
	// the normal dispatch and keeps working with the panel still on screen.
	if m.activePanelFocused() {
		evs := m.activeEvents(time.Now())
		switch key {
		case "esc":
			// Leave the panel but keep it open — esc backs out of the focus, not
			// out of the view. `a` is what closes it.
			m.focusPane = focusMain
			m.say("focus: schedule (panel still open, a closes it)")
			return m, nil
		case "down", "j", "ctrl+n":
			m.activeIndex = min(m.activeIndex+1, max(0, len(evs)-1))
			m.syncSelectionToActive()
			return m, nil
		case "up", "k", "ctrl+p":
			m.activeIndex = max(0, m.activeIndex-1)
			m.syncSelectionToActive()
			return m, nil
		case "g", "G", "home", "end":
			if key == "g" || key == "home" {
				m.activeIndex = 0
			} else {
				m.activeIndex = max(0, len(evs)-1)
			}
			m.syncSelectionToActive()
			return m, nil
		case "enter":
			// Jump the schedule to this event and hand focus back, so the panel
			// acts as a launcher rather than a place you get stuck in.
			if len(evs) == 0 {
				return m, nil
			}
			m.syncSelectionToActive()
			m.focusPane = focusMain
			m.say("jumped to " + evs[max(0, min(m.activeIndex, len(evs)-1))].Title)
			return m, nil
		}
		// Not a panel-list key: the schedule selection already tracks the panel
		// cursor (syncSelectionToActive), so E/X/L/A/o and the quick actions act
		// on the highlighted event via the normal dispatch below.
	}

	// Quick actions (nudge/resize/duplicate/save/discard/undo) act on the
	// selected event without opening the form. Checked before the main dispatch
	// so they can own their keys, and falling through when the key isn't one of
	// theirs.
	if mm, cmd, handled := m.handleQuickAction(key); handled {
		return mm, cmd
	}

	switch key {
	case "q":
		// Quitting with an unsaved nudge would silently throw it away. Ask for
		// an explicit discard (esc) or save (s) first. ctrl+c is the abort and
		// does not ask -- it is handled at the top of handleKey.
		if m.pending != nil {
			m.warn("unsaved " + m.pending.label() + " on \"" + m.pending.title + "\" — s to save, esc to discard, then q")
			return m, nil
		}
		return m, tea.Quit
	case "down", "j":
		if m.view == viewList {
			m.selected = min(m.selected+1, max(0, len(m.events)-1))
			m.syncAnchorToSelected()
		} else if m.focusPane == focusDetail {
			m.gridDetail++
			m.clampGridDetail()
		} else {
			return m, m.moveFocusByDays(7)
		}
	case "up", "k":
		if m.view == viewList {
			m.selected = max(0, m.selected-1)
			m.syncAnchorToSelected()
		} else if m.focusPane == focusDetail {
			m.gridDetail--
			m.clampGridDetail()
		} else {
			return m, m.moveFocusByDays(-7)
		}
	case "G", "end":
		if m.view == viewList {
			m.selected = max(0, len(m.events)-1)
			m.syncAnchorToSelected()
		}
	case "right", "l":
		return m, m.moveFocus(1)
	case "left", "h":
		return m, m.moveFocus(-1)
	case "n":
		// Jump to now: select the next upcoming event (the one just below the
		// "now" divider), falling back to the last loaded event when all past.
		m.anchor = today()
		m.selected = m.firstEventIndexAtOrAfterNow()
		if m.view != viewList {
			m.resetGridTop()
		}
		if !m.anchorLoaded() {
			return m, m.reload()
		}
		return m, nil
	case "d":
		m.jumpUnit = "day"
		m.say("step: day")
	case "w":
		m.jumpUnit = "week"
		m.say("step: week")
	case "m":
		m.jumpUnit = "month"
		m.say("step: month")
	case "g", "v":
		// Toggle grid view. g -> week grid; from grid -> back to list.
		if m.view == viewList {
			m.view = viewWeek
			m.resetGridTop()
		} else {
			m.view = viewList
		}
		return m, m.reload()
	case "M":
		m.view = viewMonth
		m.resetGridTop()
		return m, m.reload()
	case "X":
		if ev := m.currentActionEvent(); ev != nil && ev.ID != "" {
			m.mode = modeConfirmDelete
		} else {
			m.warn("no event selected")
		}
	case "tab":
		m.cycleFocus()
	case "R":
		return m, m.reload()
	case "Z":
		if n := len(settings.timezones); n > 0 {
			m.tzIndex = (m.tzIndex + 1) % n
		}
		m.say("timezone: " + m.tzLabel())
	case "enter", "o":
		cmd := m.openPrimary()
		return m, cmd
	case "L":
		if ev := m.currentActionEvent(); ev != nil && len(ev.otherLinks()) > 0 {
			links := ev.otherLinks()
			items := make([]pickerItem, len(links))
			for i, link := range links {
				items[i] = pickerItem{label: link, value: link}
			}
			m.mode = modeLinkPicker
			m.picker = pickerState{title: "Links - Enter opens", kind: pickerLinks, items: items}
			m.input = ""
		} else {
			m.warn("no extra links (Enter opens the calendar event)")
		}
	case "A":
		if ev := m.currentActionEvent(); ev != nil && len(ev.Attendees) > 0 {
			items := make([]pickerItem, 0, len(ev.Attendees))
			for _, a := range ev.Attendees {
				email := attendeeEmail(a)
				items = append(items, pickerItem{label: a, value: email})
			}
			m.mode = modeAttendeePicker
			m.picker = pickerState{title: "Attendees - Enter opens their calendar", kind: pickerAttendee, items: items}
			m.input = ""
		} else {
			m.warn("selected event has no attendees")
		}
	case "/":
		m.mode = modeSearch
		m.input = ""
		m.searchIndex = 0
	case "e":
		m.mode = modeCalendarPicker
		m.picker = pickerState{title: "Calendar - type to filter, Enter to open", kind: pickerCalendar, items: calendarPickerItems(m.recents)}
		m.input = ""
	case "N":
		m.mode = modeCreate
		m.create = m.newCreateState()
	case "f":
		// Find a time across several people. Opens on the participant picker;
		// the slot search only runs once you have chosen who is coming.
		m.mode = modeFindTime
		m.find = m.newFindState()
		m.say("who is coming? space toggles · Enter finds open slots")
	case "O":
		// Overlay several calendars into one agenda. Re-opening while an
		// overlay is on starts from the current set, and confirming an empty
		// set turns it back off.
		m.mode = modeOverlayPicker
		m.overlay = m.newOverlayState()
		if m.overlay.on() {
			m.say("overlay: space toggles · Enter applies · empty = off")
		} else {
			m.say("whose calendars? space toggles · Enter overlays them")
		}
	case "E":
		if ev := m.currentActionEvent(); ev != nil {
			if ev.StartAt.IsZero() {
				m.warn("can't edit all-day events here yet")
			} else {
				m.mode = modeCreate
				m.create = m.editCreateState(ev)
				// Snapshot the pre-edit values so a successful patch can be
				// reversed with `u`.
				m.preEdit = undoSnapshot(ev, m.eventCalendar(ev), undoPatch)
			}
		}
	case "a":
		// Toggle the docked panel. This deliberately does NOT move focus or the
		// selection: you glance at what is in effect while staying exactly where
		// you were in the agenda. `tab` is what steps into it.
		m.activeOpen = !m.activeOpen
		if !m.activeOpen {
			if m.focusPane == focusActive {
				m.focusPane = focusMain
			}
			m.say("active panel closed")
			return m, nil
		}
		m.activeIndex = 0
		if n := len(m.activeEvents(time.Now())); n == 0 {
			m.say("nothing active right now")
		} else {
			m.say(fmt.Sprintf("%d active · tab to focus the panel", n))
		}
	case "y":
		if m.currentActionEvent() == nil {
			m.warn("no event selected")
			return m, nil
		}
		m.yankPending = true
		// The hint bar already spells out the y-combinations; repeating them in
		// the status line said the same thing twice on adjacent rows.
		m.say("copy what?")
	case "t":
		// The timeline is the only thing on the row that costs horizontal
		// space, so it has to be switchable off when a long title matters more
		// than the overlap picture.
		m.timelineHidden = !m.timelineHidden
		switch {
		case m.timelineHidden:
			m.say("timeline off (t shows the overlap bars again)")
		case m.view != viewList:
			m.say("timeline on — it draws in the list view (g)")
		case timelineColsWithReserve(max(20, m.schedulePaneWidth()-1), m.overlayRowReserve()) == 0:
			m.say("timeline on — but this pane is too narrow to draw it")
		default:
			m.say("timeline on: bars share a 00-24h axis, the ruler shades how many run at once")
		}
	case "?":
		m.mode = modeHelp
	}
	return m, nil
}
