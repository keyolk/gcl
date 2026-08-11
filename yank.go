package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	osc52 "github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
)

const yankHint = "yank: y summary · u calendar URL · s start · e end · d description · esc cancel"

type clipboardCopiedMsg struct {
	label string
	err   error
}

// handleYankKey consumes the key following `y`. Unknown keys cancel the prefix
// instead of falling through to another action, matching the behavior of other
// two-key command families.
func (m model) handleYankKey(key string) (tea.Model, tea.Cmd) {
	m.yankPending = false
	if key == "esc" || key == "ctrl+c" {
		m.status = "yank cancelled"
		return m, nil
	}
	if m.currentActionEvent() == nil {
		m.status = "no event selected"
		return m, nil
	}

	label, value, ok := m.yankValue(key)
	if !ok {
		m.status = "unknown yank key: " + key
		return m, nil
	}
	if value == "" {
		m.status = "selected event has no " + label
		return m, nil
	}

	m.status = "copying " + label + "~"
	return m, copyToClipboardCmd(label, value)
}

func (m model) yankValue(key string) (label, value string, ok bool) {
	ev := m.currentActionEvent()
	if ev == nil {
		return "event", "", true
	}

	switch key {
	case "y":
		return "event summary", m.yankSummary(ev), true
	case "u":
		return "calendar URL", eventCalendarURL(ev), true
	case "s":
		return "start timestamp", m.yankTimestamp(ev, true), true
	case "e":
		return "end timestamp", m.yankTimestamp(ev, false), true
	case "d":
		return "description", ev.Description, true
	default:
		return "", "", false
	}
}

func eventCalendarURL(ev *Event) string {
	if ev == nil {
		return ""
	}
	if ev.HTMLLink != "" {
		return ev.HTMLLink
	}
	for _, link := range ev.Links {
		if strings.Contains(link, "google.com/calendar/event") {
			return link
		}
	}
	return ""
}

// yankTimestamp uses the timezone currently selected in the TUI. For all-day
// events it returns a date; the end date is the last covered day rather than the
// Calendar API's exclusive boundary.
func (m model) yankTimestamp(ev *Event, start bool) string {
	if ev == nil {
		return ""
	}
	if ev.AllDay() {
		date := ev.StartDate
		if !start {
			date = ev.EndDate
			if date.After(ev.StartDate) {
				date = date.AddDate(0, 0, -1)
			}
		}
		if date.IsZero() {
			return ""
		}
		return date.Format("2006-01-02")
	}

	startAt, endAt, _ := m.effectiveTimes(ev)
	value := startAt
	if !start {
		value = endAt
	}
	if value.IsZero() {
		return ""
	}
	return value.In(m.tz()).Format(time.RFC3339)
}

func (m model) yankSummary(ev *Event) string {
	if ev == nil {
		return ""
	}
	title := strings.TrimSpace(ev.Title)
	if title == "" {
		title = "(untitled)"
	}

	var lines []string
	lines = append(lines, title)
	start := m.yankTimestamp(ev, true)
	end := m.yankTimestamp(ev, false)
	if start != "" {
		span := start
		if end != "" && end != start {
			span += " — " + end
		}
		if ev.AllDay() {
			span += " (all-day)"
		} else if _, _, unsaved := m.effectiveTimes(ev); unsaved {
			span += " (UNSAVED)"
		}
		lines = append(lines, "Time: "+span)
	}
	if ev.Location != "" {
		lines = append(lines, "Location: "+ev.Location)
	}
	if ev.Description != "" {
		lines = append(lines, "Description: "+ev.Description)
	}
	if link := eventCalendarURL(ev); link != "" {
		lines = append(lines, "URL: "+link)
	}
	return strings.Join(lines, "\n")
}

func copyToClipboardCmd(label, value string) tea.Cmd {
	return func() tea.Msg {
		// stderr remains attached to the terminal while Bubble Tea owns stdout's
		// alt-screen renderer. OSC 52 works locally and over SSH; modern tmux
		// handles the unwrapped sequence when set-clipboard is enabled.
		if _, err := osc52.New(value).WriteTo(os.Stderr); err != nil {
			return clipboardCopiedMsg{label: label, err: fmt.Errorf("write OSC 52 clipboard: %w", err)}
		}
		return clipboardCopiedMsg{label: label}
	}
}
