package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadCmd(m.inflightReq), clockTickCmd()}
	if settings.notify {
		cmds = append(cmds, notifyTickCmd())
	}
	return tea.Batch(cmds...)
}

// clockTickCmd re-renders on a slow interval so time-relative UI stays honest
// without input: the header's active count, the "now" divider, the ◉now badges,
// and the "ends in ~" countdowns all move on their own.
func clockTickCmd() tea.Cmd {
	return tea.Tick(30*time.Second, func(time.Time) tea.Msg { return clockTickMsg{} })
}

// notifyTickCmd schedules the next in-app reminder scan.
func notifyTickCmd() tea.Cmd {
	interval := time.Duration(max(5, settings.notifyInterval)) * time.Second
	return tea.Tick(interval, func(time.Time) tea.Msg { return notifyTickMsg{} })
}

// notifyScanCmd runs one scan-and-toast pass for the current calendar off the
// UI thread, then reports how many reminders fired via a status message.
//
// It deliberately scans m.calendar even while an OVERLAY is on: reminders are
// for events you have to attend, and toasting a colleague's 1:1 because their
// calendar happens to be on screen would be noise, not information.
func (m model) notifyScanCmd() tea.Cmd {
	calendar := m.calendar
	return func() tea.Msg {
		// This runs on a Cmd goroutine while the alt screen is up: nothing here
		// may print. Reminders without a toast sink are handed back to Update
		// and shown in the status line instead.
		var shown []string
		deliver := func(msg string) error {
			err := sendToast(msg)
			if errors.Is(err, errNoToastSink) {
				shown = append(shown, msg)
				return nil
			}
			return err
		}
		fired, err := notifyUpcoming(calendar, settings.notifyWindow, deliver)
		return notifyResultMsg{fired: fired, shown: shown, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Clear on resize so stale cells from the previous size don't linger
		// (alt-screen terminals otherwise keep old glyphs on shrink/grow).
		return m, tea.ClearScreen
	case fetchTickMsg:
		// debounce currently disabled: ignore stray ticks from older binaries/flows
		return m, nil
	case notifyTickMsg:
		// Fire a background scan and re-arm the watcher.
		return m, tea.Batch(m.notifyScanCmd(), notifyTickCmd())
	case notifyResultMsg:
		switch {
		case msg.err != nil:
			m.status = "reminder failed: " + msg.err.Error()
		case len(msg.shown) > 0:
			m.status = strings.Join(msg.shown, " · ")
		case msg.fired > 0:
			m.status = fmt.Sprintf("%d reminder(s) sent", msg.fired)
		}
		return m, nil
	case clockTickMsg:
		// Nothing to update: View() reads time.Now() directly. Just re-arm so
		// the next repaint happens.
		return m, clockTickCmd()
	case eventsMsg:
		// Drop stale responses from superseded requests.
		if msg.reqID != m.inflightReq {
			return m, nil
		}
		m.loading = false
		m.err = msg.err
		if msg.err != nil {
			// Keep the last good events (and their loaded window) on screen: a
			// transient failure after a nudge or save used to blank the whole
			// agenda. loadedStart/End stay with those events, so anchorLoaded()
			// still reports the gap and the next move retries the fetch.
			m.status = "load failed: " + msg.err.Error()
			if m.hasLoaded() {
				m.status += " · showing last loaded"
			}
		} else {
			m.events = msg.events
			m.loadedStart = msg.start
			m.loadedEnd = msg.end
			// If the current selection no longer points at a visible event in the
			// loaded window, snap to the first event on/after the focus date.
			if m.selected < 0 || m.selected >= len(m.events) || m.selectedEvent() == nil {
				m.selected = m.firstEventIndexOnOrAfter(m.anchor)
			}
			m.status = fmt.Sprintf("loaded %d events", len(m.events))
		}
		if m.pendingNote != "" {
			m.status = m.pendingNote
			m.pendingNote = ""
		}
		return m, nil
	case overlayLoadedMsg:
		if msg.reqID != m.inflightReq {
			return m, nil
		}
		m.loading = false
		m.err = nil
		m.events = msg.events
		m.loadedStart = msg.start
		m.loadedEnd = msg.end
		m.overlay.failed = msg.failed
		if m.selected < 0 || m.selected >= len(m.events) || m.selectedEvent() == nil {
			m.selected = m.firstEventIndexOnOrAfter(m.anchor)
		}
		// A calendar that failed to load looks exactly like an empty one in the
		// agenda, which is the most dangerous way this can be wrong — say it.
		if n := len(msg.failed); n > 0 {
			var names []string
			for cal := range msg.failed {
				names = append(names, overlayShortName(cal))
			}
			sort.Strings(names)
			m.status = fmt.Sprintf("%d events · %d calendar(s) NOT loaded: %s",
				len(m.events), n, strings.Join(names, ", "))
		} else {
			m.status = fmt.Sprintf("%d events across %d calendars",
				len(m.events), len(m.overlay.active))
		}
		return m, nil
	case statusMsg:
		m.status = string(msg)
		return m, nil
	case clipboardCopiedMsg:
		if msg.err != nil {
			m.status = "copy failed: " + msg.err.Error()
		} else {
			m.status = "copied " + msg.label
		}
		return m, nil
	case findTimeMsg:
		m.find.loading = false
		if msg.err != nil {
			m.find.err = msg.err.Error()
			m.find.slots = nil
			return m, nil
		}
		m.find.err = ""
		m.find.results = msg.results
		m.find.slots = msg.slots
		m.find.slotIdx = max(0, min(m.find.slotIdx, len(msg.slots)-1))
		switch {
		case len(msg.slots) == 0:
			m.status = "no slot fits — try d (shorter) or H (open up the hours)"
		case msg.slots[0].allFree():
			m.status = fmt.Sprintf("%d slots · best: %s works for everyone",
				len(msg.slots), m.slotRangeLabel(msg.slots[0]))
		default:
			m.status = fmt.Sprintf("%d slots · nobody is free for all of it — best is %d/%d",
				len(msg.slots), len(msg.slots[0].free), len(msg.slots[0].free)+len(msg.slots[0].busy))
		}
		return m, nil
	case createdMsg:
		m.create.submitting = false
		if msg.err != nil {
			m.create.err = msg.err.Error()
			return m, nil
		}
		m.mode = modeNormal
		verb := "created"
		if msg.edited {
			verb = "updated"
		}
		n := len(m.create.attendees)
		if n > 0 {
			m.status = fmt.Sprintf("%s \"%s\" | invited %d · u to undo", verb, m.create.title, n)
		} else {
			m.status = fmt.Sprintf("%s \"%s\" · u to undo", verb, m.create.title)
		}
		// Record undo. A create is reversed by deleting; a patch is reversed by
		// restoring the pre-edit field values captured when the form opened.
		if msg.edited {
			if m.preEdit != nil {
				snap := *m.preEdit
				snap.kind = undoPatch
				snap.label = m.create.title
				m.lastUndo = &snap
			}
		} else if msg.eventID != "" {
			m.lastUndo = &undoEntry{
				kind:     undoCreate,
				calendar: msg.calendar,
				eventID:  msg.eventID,
				label:    m.create.title,
			}
		}
		m.loading = true
		return m, m.reload()
	case deletedMsg:
		m.mode = modeNormal
		if msg.err != nil {
			m.err = msg.err
			m.status = "delete failed"
			return m, nil
		}
		if msg.restore != nil {
			m.lastUndo = msg.restore
			m.status = fmt.Sprintf("deleted \"%s\" · u to undo", msg.restore.label)
		} else {
			m.status = "event deleted"
		}
		return m, m.reload()
	case undoneMsg:
		if msg.err != nil {
			m.err = msg.err
			m.status = "undo failed: " + msg.err.Error()
			return m, nil
		}
		m.lastUndo = nil
		m.status = "undid: " + msg.label
		m.loading = true
		return m, m.reload()
	case eventShiftedMsg:
		if msg.err != nil {
			m.err = msg.err
			if msg.committed && m.pending != nil {
				// The patch failed; keep the staged change so `s` can retry
				// rather than making the user redo the nudges.
				p := *m.pending
				p.saving = false
				m.pending = &p
				m.status = "save failed: " + msg.err.Error() + " · s retries, esc discards"
				return m, nil
			}
			m.status = "shift failed: " + msg.err.Error()
			return m, nil
		}
		if msg.committed {
			m.pending = nil
		}
		m.lastUndo = msg.restore
		m.status = msg.label + " · u to undo"
		m.loading = true
		return m, m.reload()
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}
