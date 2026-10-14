package main

import (
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

func (m *model) push(src string, level toast.Level, text string) {
	m.pushTTL(src, level, text, 0)
}

func (m *model) pushTTL(src string, level toast.Level, text string, ttl time.Duration) {
	// The expiry tick is not returned here: helpers that report are called
	// from code that already returns its own Cmd. Update schedules the ticks
	// for every toast that is new after the handler ran (toastTicks).
	m.toasts, _ = toast.Push(m.toasts, toast.Toast{Source: src, Level: level, Text: text, TTL: ttl})
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

// toastTicks starts the expiry timer of every toast in after that was not in
// before. Push returns that timer, but the say/fail helpers run deep inside
// handlers that return their own Cmd, so threading it out of each of ~100
// call sites would be noise. The tick is rebuilt here from the kit's own
// ExpiredMsg, so the id/arm guard still applies: a toast replaced before its
// timer fires is not removed by that timer.
func toastTicks(before, after toast.Queue) tea.Cmd {
	var old []uint64
	for _, t := range before.Items() {
		old = append(old, t.ID())
	}
	var cmds []tea.Cmd
	for _, t := range after.Items() {
		if slices.Contains(old, t.ID()) || t.Level == toast.Error {
			continue
		}
		msg, ok := after.ExpiredMsg(t.ID())
		if !ok {
			continue
		}
		ttl := t.TTL
		if ttl <= 0 {
			ttl = toast.DefaultTTL
		}
		cmds = append(cmds, tea.Tick(ttl, func(time.Time) tea.Msg { return msg }))
	}
	return tea.Batch(cmds...)
}

// statusView draws the toasts, one line each, in exactly statusRows rows: an
// empty queue still keeps its row, so the hint bar does not jump when the last
// toast goes. When the terminal is too short for every toast, the newest win.
func (m model) statusView() string {
	lines := strings.Split(asciiEllipsis(toast.View(m.toasts, m.width, ui.ToastStyles())), "\n")
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

// asciiEllipsis swaps the kit's "…" cut marker for "~". U+2026 is East Asian
// ambiguous: KR/JP terminals draw it two cells wide, so a line measured as
// fitting would overflow by one and wrap, stealing a row (the same reason
// detailStyle has no border and the hint bar uses ASCII separators). "~" is
// what gcl's own truncate already uses. Both measure one cell, so widths
// computed before the swap stay right.
func asciiEllipsis(s string) string {
	return strings.ReplaceAll(s, "…", "~")
}
