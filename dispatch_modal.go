package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Action runners for the modal pages: help, delete confirm, search, pickers.

func (m model) runHelp(action string) (tea.Model, tea.Cmd) {
	switch action {
	case "help.close":
		m.mode = modeNormal
	case "help.down":
		m.helpOff++
		_, m.helpOff = m.helpPage()
	case "help.up":
		m.helpOff = max(0, m.helpOff-1)
	case "quit":
		return m.runSchedule(action)
	}
	return m, nil
}

func (m model) runDelete(action string) (tea.Model, tea.Cmd) {
	if action != "delete.yes" {
		m.mode = modeNormal
		m.say("delete cancelled")
		return m, nil
	}
	ev := m.selectedEvent()
	if ev == nil || ev.ID == "" {
		m.mode = modeNormal
		return m, nil
	}
	// In an overlay the row may belong to someone else's calendar; deleting
	// against m.calendar would 404 or, worse, hit a same-titled event on the
	// wrong calendar.
	cal := m.eventCalendar(ev)
	id := ev.ID
	notify := len(ev.Attendees) > 0
	// Snapshot before deleting so `u` can recreate the event.
	restore := undoSnapshot(ev, cal, undoDelete)
	cmd := m.running("deleting~", func() tea.Msg {
		err := deleteEvent(cal, id, notify)
		if err != nil {
			return deletedMsg{err: err}
		}
		return deletedMsg{restore: restore}
	})
	return m, cmd
}

// runSearch drives the search overlay: fuzzy-jump across the loaded events.
func (m model) runSearch(action string) (tea.Model, tea.Cmd) {
	switch action {
	case "search.cancel":
		m.mode = modeNormal
		m.input = ""
		m.searchIndex = 0
	case "search.jump":
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
	case "search.erase":
		if m.input != "" {
			m.input = trimLastRune(m.input)
			m.searchIndex = 0
		}
	case "search.down":
		m.searchIndex = min(m.searchIndex+1, max(0, len(m.searchMatches())-1))
	case "search.up":
		m.searchIndex = max(0, m.searchIndex-1)
	}
	return m, nil
}

// runPicker drives the fuzzy pickers: links / calendars / attendees.
func (m model) runPicker(action string) (tea.Model, tea.Cmd) {
	items := m.picker.filtered(m.input)
	switch action {
	case "picker.cancel":
		m.mode = modeNormal
		m.input = ""
	case "picker.choose":
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
	case "picker.erase":
		if m.input != "" {
			m.input = trimLastRune(m.input)
			m.picker.index = 0
		}
	case "picker.down":
		m.picker.index = min(m.picker.index+1, max(0, len(items)-1))
	case "picker.up":
		m.picker.index = max(0, m.picker.index-1)
	}
	return m, nil
}
