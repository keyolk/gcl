package main

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/prefs"
)

// UI state that survives a restart (engine principle E12), in
// $XDG_STATE_HOME/gcl/ui.json (~/.local/state/gcl/ui.json).
//
// Kept: the view, the list step, whether the timeline is hidden, and the
// overlay set — the choices that take several keys to rebuild. Not kept: the
// date (a calendar opens on today), the selection (events change between
// runs) and the calendar (-c and default_calendar decide that).
//
// The saved state wins over the config defaults (default_step, timeline):
// those describe a first run; the file records what the user chose since.
//
// Writes go through prefs.Saver: debounced, off the UI loop, temp file plus
// rename, ordered so an older write never lands after a newer one. Quitting
// flushes the latest state and exits once it is on disk.
type uiState struct {
	View           string   `json:"view"`      // list | week | month
	JumpUnit       string   `json:"jump_unit"` // day | week | month
	TimelineHidden bool     `json:"timeline_hidden"`
	Overlay        []string `json:"overlay,omitempty"` // calendars merged into the agenda
}

var viewNames = map[viewMode]string{viewList: "list", viewWeek: "week", viewMonth: "month"}

func (m model) uiState() uiState {
	return uiState{
		View:           viewNames[m.view],
		JumpUnit:       m.jumpUnit,
		TimelineHidden: m.timelineHidden,
		Overlay:        slices.Clone(m.overlay.active),
	}
}

func (s uiState) equal(o uiState) bool {
	return s.View == o.View && s.JumpUnit == o.JumpUnit &&
		s.TimelineHidden == o.TimelineHidden && slices.Equal(s.Overlay, o.Overlay)
}

// defaultUIState is the state of a first run, from the config.
func defaultUIState() uiState {
	return uiState{View: "list", JumpUnit: settings.defaultStep, TimelineHidden: !settings.timeline}
}

// apply restores s onto m. Values a hand-edited or older file could get wrong
// are checked, and anything unknown keeps the model's current value.
func (s uiState) apply(m *model) {
	for v, name := range viewNames {
		if name == s.View {
			m.view = v
		}
	}
	if m.view != viewList {
		m.resetGridTop()
	}
	switch s.JumpUnit {
	case "day", "week", "month":
		m.jumpUnit = s.JumpUnit
	}
	m.timelineHidden = s.TimelineHidden
	var cals []string
	for _, c := range s.Overlay {
		if c = strings.TrimSpace(c); c != "" && !slices.Contains(cals, c) {
			cals = append(cals, c)
		}
	}
	// More calendars than colors cannot be told apart; the picker refuses that
	// too (runOverlay), so such a file was not written by gcl.
	if len(cals) > 0 && len(cals) <= len(overlayColors) {
		m.overlay.active = cals
		m.overlay.failed = map[string]string{}
	}
}

// loadUIState reads the saved state for main. A corrupt file is reported, and
// the defaults are used: losing UI state must never stop the app.
func loadUIState() (uiState, error) {
	return prefs.Load(appName, defaultUIState())
}

// saveUIState queues a debounced write when the state changed during this
// Update. A model that was not given a Saver (persist false: every test that
// builds a model literal) writes nothing.
func (m model) saveUIState(before uiState) (model, tea.Cmd) {
	if !m.persist {
		return m, nil
	}
	now := m.uiState()
	if now.equal(before) {
		return m, nil
	}
	var cmd tea.Cmd
	m.saver, cmd = m.saver.Save(now)
	return m, cmd
}

// quit leaves the program. With persistence on it first flushes the latest
// UI state and quits on the Saved{Final} that reports it written (Update).
func (m model) quit() (tea.Model, tea.Cmd) {
	if !m.persist {
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.saver, cmd = m.saver.Flush()
	return m, cmd
}
