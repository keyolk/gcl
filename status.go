package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"time"

	"github.com/keyolk/tuikit/toast"
)

// The status line is a toast queue (engine principle E6).
//
// It used to be one string that every handler overwrote. That failed three
// ways: nothing expired, so "copied description" sat there for an hour; there
// was no severity, so "load failed" was drawn in the same green as "loaded";
// and anything said just before a reload was erased by the reload's own
// "loading~" — which is why a pendingNote field existed to carry it past the
// fetch. Now each message is a toast with a level and a source: a newer toast
// from the same source replaces the older one, informational toasts expire on
// their own timer, and errors stay until a key is pressed after they were seen.
// A reload says nothing in the status line at all (the header has a loading
// pill), so it can no longer erase a note.

// Toast sources. A source groups messages that supersede each other: the next
// load result replaces the last one, error or not.
const (
	srcStatus = "status" // answers to a key press
	srcLoad   = "load"   // fetch results
	srcNotify = "notify" // reminder scans
	srcAction = "action" // writes to Google: create, delete, save, undo, copy
	srcFiles  = "files"  // recents list and UI state on disk
)

// say shows an informational answer to what the user just did.
func (m *model) say(text string) { m.push(srcStatus, toast.Info, text) }

// warn shows something legal but probably unintended.
func (m *model) warn(text string) { m.push(srcStatus, toast.Warn, text) }

// done reports a completed write.
func (m *model) done(src, text string) { m.push(src, toast.Success, text) }

// fail reports an error. It stays until a key is pressed after it was shown,
// or until the same source reports again.
func (m *model) fail(src, text string) { m.push(src, toast.Error, text) }

// running shows text as a progress toast while cmd writes to Google, and
// returns cmd. Every write answers on srcAction (done or fail), which replaces
// the progress toast, so "saving~" no longer lingers next to "saved" until its
// timer runs out. No cmd, no toast: a Progress toast nothing answers would
// stay forever.
func (m *model) running(text string, cmd tea.Cmd) tea.Cmd {
	if cmd != nil {
		m.push(srcAction, toast.Progress, text)
	}
	return cmd
}

func (m *model) push(src string, level toast.Level, text string) {
	m.pushTTL(src, level, text, 0)
}

func (m *model) pushTTL(src string, level toast.Level, text string, ttl time.Duration) {
	// Add returns no expiry tick: helpers that report are called from code
	// that already returns its own Cmd. Update starts the ticks owed for every
	// toast added while the handler ran (toast.Ticks).
	m.toasts = toast.Add(m.toasts, toast.Toast{Source: src, Level: level, Text: text, TTL: ttl})
}

// statusText is every toast's text, oldest first, joined for tests and for
// anything that needs the status as one string.
func (m model) statusText() string {
	var parts []string
	for _, t := range m.toasts.Items() {
		parts = append(parts, t.Text)
	}
	return strings.Join(parts, " | ")
}

// statusView draws the toasts, one line each, in exactly statusRows rows: an
// empty queue still keeps its row, so the hint bar does not jump when the last
// toast goes. When the terminal is too short for every toast, the newest win.
func (m model) statusView() string {
	st := ui.ToastStyles()
	st.Ellipsis = cutMarker
	lines := strings.Split(toast.View(m.toasts, m.width, st), "\n")
	if n := m.statusRows(); len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// statusRows is how many rows the status area takes: one per toast, at least
// one, and never so many that the body drops under its 4-row floor.
func (m model) statusRows() int {
	return max(1, min(m.toasts.Len(), m.height-6))
}

// cutMarker marks text cut to fit, in gcl's own truncation and in the kit's
// footer, help and toasts. Not "…": U+2026 is East Asian ambiguous, so KR/JP
// terminals draw it two cells wide and a line measured as fitting overflows
// by one and wraps, stealing a row (the same reason detailStyle has no border
// and the hint bar uses ASCII separators). The kit only switches to "~" under
// a CJK locale; the cell width is the terminal's setting, not the locale's,
// so gcl pins it.
const cutMarker = "~"
