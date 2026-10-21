package main

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/link"
	"github.com/keyolk/tuikit/toast"
)

// handleKey resolves a key through the binding table (bindings.go) and runs
// the action. Which contexts are live is activeStack's job; this file only
// says what each action does.
func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A key press is how a sticky error is acknowledged; done before the
	// handler so an error this key causes is not cleared by the same key.
	m.toasts = toast.OnKey(m.toasts)

	km := m.keymap()
	stack := m.activeStack()
	opts := m.keyOpts()
	// Under a Korean input source the shortcut keys arrive as jamo (`q` → `ㅂ`).
	// Resolve maps them to the Latin key at the same physical position, so
	// shortcuts fire without switching the input source back. While a text
	// field owns the keyboard the jamo is the intended input: ResolveRaw.
	// Visible narrows the footer only, so it is not passed to dispatch.
	opts.Visible = nil
	resolve := km.Resolve
	if m.isInTextInput() {
		resolve = km.ResolveRaw
	}
	bnd, ok := resolve(stack, msg, opts)
	if !ok {
		return m.unbound(msg)
	}
	return m.run(bnd.Action, msg)
}

// unbound handles a key no live binding claims: typed text in the fields and
// filters, and the "anything else" rules of the yank prefix and the delete
// confirm.
func (m model) unbound(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	text, typed := typedText(msg)
	switch {
	case m.mode == modeConfirmDelete:
		// Anything but a yes cancels, so a stray key never deletes.
		m.mode = modeNormal
		m.say("delete cancelled")
	case m.mode == modeSearch && typed:
		m.input += text
		m.searchIndex = 0
	case (m.mode == modeCalendarPicker || m.mode == modeLinkPicker || m.mode == modeAttendeePicker) && typed:
		m.input += text
		m.picker.index = 0
	case m.mode == modeOverlayPicker && typed:
		m.overlay.input += text
		m.overlay.candIdx = 0
	case m.mode == modeFindTime && m.find.step == findPeople && typed:
		m.find.input += text
		m.find.candIdx = 0
	case m.mode == modeCreate && m.create.editingField && typed:
		c := &m.create
		if c.step == stepAttendees {
			if text != " " {
				c.attInput += text
				c.attCandidx = 0
			}
			break
		}
		*c.fieldPtr() += text
		if c.step == stepLocation {
			c.locCandidx = 0
		}
	case m.mode == modeNormal && m.yankPending:
		// Unknown keys cancel the prefix instead of falling through to another
		// action, like other two-key command families.
		m.yankPending = false
		if m.currentActionEvent() == nil {
			m.warn("no event selected")
			break
		}
		m.warn("unknown yank key: " + msg.String())
	}
	return m, nil
}

// typedText is the text a key types into a field: one printable rune, or the
// space bar (tea reports it as its own key type).
func typedText(msg tea.KeyMsg) (string, bool) {
	if msg.Type == tea.KeySpace {
		return " ", true
	}
	if isTypedRune(msg) {
		return msg.String(), true
	}
	return "", false
}

// isTypedRune reports whether a key message is a single printable character the
// user typed, and should therefore be appended to a text field.
//
// The obvious `len(key) == 1` is a BYTE length: it silently dropped every
// non-ASCII character, so Korean could not be typed into any field even though
// backspace already decoded runes correctly on the way out. Named chords
// ("enter", "ctrl+n") arrive as multi-rune strings and are excluded by the
// KeyRunes check rather than by length.
func isTypedRune(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyRunes && !msg.Alt && len(msg.Runes) == 1
}

// isInTextInput reports whether the keyboard belongs to a text field, where a
// jamo is input rather than a shortcut. The form's field-navigation state and
// the find flow's slot list are all shortcuts, so they are not text input.
func (m model) isInTextInput() bool {
	switch m.mode {
	case modeCreate:
		return m.create.editingField
	case modeSearch, modeCalendarPicker, modeLinkPicker, modeAttendeePicker, modeOverlayPicker:
		return true
	case modeFindTime:
		return m.find.step == findPeople
	}
	return false
}

// run executes one action. Actions are grouped by the context that binds them.
func (m model) run(action string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case action == "abort":
		// ctrl+c quits from anywhere, ahead of every mode — including the
		// create form and the pickers. It bypasses the unsaved-nudge warning
		// that `q` goes through: a staged nudge lives in memory only, so
		// nothing on the calendar changes.
		return m.quit()
	case strings.HasPrefix(action, "yank."):
		return m.runYank(action)
	case m.mode == modeCreate || m.mode == modeFormHelp || m.mode == modeConfirmSubmit:
		return m.runForm(action)
	case m.mode == modeFindTime:
		return m.runFind(action)
	case m.mode == modeOverlayPicker:
		return m.runOverlay(action)
	case m.mode == modeConfirmDelete:
		return m.runDelete(action)
	case m.mode == modeSearch:
		return m.runSearch(action)
	case m.mode == modeCalendarPicker || m.mode == modeLinkPicker || m.mode == modeAttendeePicker:
		return m.runPicker(action)
	case m.mode == modeHelp:
		return m.runHelp(action)
	}
	if mm, cmd, ok := m.runQuick(action); ok {
		return mm, cmd
	}
	return m.runSchedule(action)
}

// runSchedule handles the agenda/grid keys, the active panel and q/?.
func (m model) runSchedule(action string) (tea.Model, tea.Cmd) {
	switch action {
	case "quit":
		// Quitting with an unsaved nudge would silently throw it away. Ask for
		// an explicit discard (esc) or save (s) first. ctrl+c is the abort and
		// does not ask.
		if m.pending != nil {
			m.warn("unsaved " + m.pending.label() + " on \"" + m.pending.title + "\" — " + m.resolveHint() + ", then " + m.keyName(ctxGlobal, "quit"))
			return m, nil
		}
		return m.quit()
	case "help":
		m.mode = modeHelp
		m.helpOff = 0

	// Active-now panel, while focus is inside it. The panel is docked, not
	// modal: keys it does not bind fall through to the schedule and act on
	// the highlighted event (syncSelectionToActive keeps them aligned).
	case "panel.back":
		// Leave the panel but keep it open — esc backs out of the focus, not
		// out of the view. `a` is what closes it.
		m.focusPane = focusMain
		m.say("focus: schedule (panel still open, a closes it)")
	case "panel.down", "panel.up", "panel.first", "panel.last":
		evs := m.activeEvents(time.Now())
		switch action {
		case "panel.down":
			m.activeIndex = min(m.activeIndex+1, max(0, len(evs)-1))
		case "panel.up":
			m.activeIndex = max(0, m.activeIndex-1)
		case "panel.first":
			m.activeIndex = 0
		default:
			m.activeIndex = max(0, len(evs)-1)
		}
		m.syncSelectionToActive()
	case "panel.jump":
		// Jump the schedule to this event and hand focus back, so the panel
		// acts as a launcher rather than a place you get stuck in.
		evs := m.activeEvents(time.Now())
		if len(evs) == 0 {
			return m, nil
		}
		m.syncSelectionToActive()
		m.focusPane = focusMain
		m.say("jumped to " + evs[max(0, min(m.activeIndex, len(evs)-1))].Title)

	case "list.down":
		m.selectEvent(m.selected + 1)
	case "list.up":
		m.selectEvent(m.selected - 1)
	case "list.last":
		m.selectEvent(len(m.events) - 1)
	case "detail.down":
		m.gridDetail++
		m.clampGridDetail()
	case "detail.up":
		m.gridDetail--
		m.clampGridDetail()
	case "grid.down":
		cmd := m.moveFocusByDays(7)
		return m, cmd
	case "grid.up":
		cmd := m.moveFocusByDays(-7)
		return m, cmd
	case "list.next", "grid.next":
		cmd := m.moveFocus(1)
		return m, cmd
	case "list.prev", "grid.prev":
		cmd := m.moveFocus(-1)
		return m, cmd
	case "step.day", "step.week", "step.month":
		m.jumpUnit = strings.TrimPrefix(action, "step.")
		m.say("step: " + m.jumpUnit)
	case "now":
		// Jump to now: select the next upcoming event (the one just below the
		// "now" divider), falling back to the last loaded event when all past.
		m.anchor = today()
		m.selected = m.firstEventIndexAtOrAfterNow()
		if m.view != viewList {
			m.resetGridTop()
		}
		if !m.anchorLoaded() {
			cmd := m.reload()
			return m, cmd
		}
	case "view.grid":
		// Toggle grid view. g -> week grid; from grid -> back to list.
		if m.view == viewList {
			m.view = viewWeek
			m.resetGridTop()
		} else {
			m.view = viewList
		}
		cmd := m.reload()
		return m, cmd
	case "view.month":
		m.view = viewMonth
		m.resetGridTop()
		cmd := m.reload()
		return m, cmd
	case "delete":
		if ev := m.currentActionEvent(); ev != nil && ev.ID != "" {
			m.mode = modeConfirmDelete
		} else {
			m.warn("no event selected")
		}
	case "focus":
		m.cycleFocus()
	case "refresh":
		cmd := m.reload()
		return m, cmd
	case "timezone":
		if n := len(settings.timezones); n > 0 {
			m.tzIndex = (m.tzIndex + 1) % n
		}
		m.say("timezone: " + m.tzLabel())
	case "open", link.Action:
		cmd := m.openPrimary()
		return m, cmd
	case "links":
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
	case "attendees":
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
	case "search":
		m.mode = modeSearch
		m.input = ""
		m.searchIndex = 0
	case "calendar":
		m.mode = modeCalendarPicker
		same := func(s string) string { return s }
		m.picker = pickerState{title: "Calendar - type to filter, Enter to open", kind: pickerCalendar,
			items: calendarPickerItems(m.recents, nil, same)}
		m.input = ""
		return m, calendarItemsCmd(m.recents)
	case "new":
		m.mode = modeCreate
		m.create = m.newCreateState()
	case "find":
		// Find a time across several people. Opens on the participant picker;
		// the slot search only runs once you have chosen who is coming.
		m.mode = modeFindTime
		m.find = m.newFindState()
		m.say("who is coming? space toggles · Enter finds open slots")
	case "overlay":
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
	case "edit":
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
	case "active":
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
	case "yank":
		if m.currentActionEvent() == nil {
			m.warn("no event selected")
			return m, nil
		}
		m.yankPending = true
		// The hint bar already spells out the y-combinations; repeating them in
		// the status line said the same thing twice on adjacent rows.
		m.say("copy what?")
	case "timeline":
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
	}
	return m, nil
}

// trimLastRune drops the final rune, for backspace handling.
func trimLastRune(s string) string {
	if s == "" {
		return s
	}
	_, size := utf8.DecodeLastRuneInString(s)
	return s[:len(s)-size]
}
