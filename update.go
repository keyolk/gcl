package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/prefs"
	"github.com/keyolk/tuikit/selection"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/toast"
)

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadCmd(m.inflightReq), clockTickCmd(), loadRecentsCmd}
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

// reminderTTL keeps a reminder that could not be toasted by tmux/osascript on
// screen longer than an ordinary note: it is the only place it appears.
const reminderTTL = 30 * time.Second

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	before, state := m.toasts, m.uiState()
	next, cmd := m.update(msg)
	nm, ok := next.(model)
	if !ok {
		return next, cmd
	}
	nm, save := nm.saveUIState(state)
	return nm, tea.Batch(cmd, save, toast.Ticks(before, nm.toasts))
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case toast.Expired:
		m.toasts = toast.Update(m.toasts, msg)
		return m, nil
	case prefs.SaveTick:
		var cmd tea.Cmd
		m.saver, cmd = m.saver.Update(msg)
		return m, cmd
	case prefs.Saved:
		if msg.Final {
			// Quitting: the latest state is on disk (or failed to get there,
			// which must not keep the user from leaving).
			return m, tea.Quit
		}
		if msg.Err != nil {
			m.fail(srcFiles, "save UI state: "+msg.Err.Error())
		}
		return m, nil
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
			m.fail(srcNotify, "reminder failed: "+msg.err.Error())
		case len(msg.shown) > 0:
			m.pushTTL(srcNotify, toast.Info, strings.Join(msg.shown, " · "), reminderTTL)
		case msg.fired > 0:
			m.push(srcNotify, toast.Info, fmt.Sprintf("%d reminder(s) sent", msg.fired))
		}
		return m, nil
	case calendarItemsMsg:
		return m.withCalendarItems(msg), nil
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
			text := "load failed: " + msg.err.Error()
			if m.hasLoaded() {
				text += " · showing last loaded"
			}
			m.fail(srcLoad, text)
		} else {
			m.events = msg.events
			m.loadedStart = msg.start
			m.loadedEnd = msg.end
			// If the current selection no longer points at a visible event in the
			// loaded window, snap to the first event on/after the focus date.
			if m.selected < 0 || m.selected >= len(m.events) || m.selectedEvent() == nil {
				m.selected = m.firstEventIndexOnOrAfter(m.anchor)
			}
			// Same source as the failure, so a good load replaces a stale error.
			m.push(srcLoad, toast.Info, fmt.Sprintf("loaded %d events", len(m.events)))
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
			m.fail(srcLoad, fmt.Sprintf("%d events · %d calendar(s) NOT loaded: %s",
				len(m.events), n, strings.Join(names, ", ")))
		} else {
			m.push(srcLoad, toast.Info, fmt.Sprintf("%d events across %d calendars",
				len(m.events), len(m.overlay.active)))
		}
		return m, nil
	case recentsMsg:
		// A pick made before the file was read stays in front.
		m.recents = mergeRecents(m.recents, msg.list)
		return m, nil
	case recentsSavedMsg:
		if msg.err != nil {
			// The switch itself worked; only the recents list is stale.
			m.fail(srcFiles, msg.err.Error())
		}
		return m, nil
	case statusMsg:
		m.say(string(msg))
		return m, nil
	case actionFailedMsg:
		m.fail(srcAction, string(msg))
		return m, nil
	case clipboardCopiedMsg:
		if msg.err != nil {
			m.fail(srcAction, "copy failed: "+msg.err.Error())
		} else {
			m.done(srcAction, "copied "+msg.label)
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
			m.say("no slot fits — try d (shorter) or H (open up the hours)")
		case msg.slots[0].allFree():
			m.say(fmt.Sprintf("%d slots · best: %s works for everyone",
				len(msg.slots), m.slotRangeLabel(msg.slots[0])))
		default:
			m.say(fmt.Sprintf("%d slots · nobody is free for all of it — best is %d/%d",
				len(msg.slots), len(msg.slots[0].free), len(msg.slots[0].free)+len(msg.slots[0].busy)))
		}
		return m, nil
	case createdMsg:
		m.create.submitting = false
		if msg.err != nil {
			// The form shows the error; the "creating~" progress has nothing
			// left to wait for.
			m.create.err = msg.err.Error()
			m.toasts = toast.Dismiss(m.toasts, srcAction)
			return m, nil
		}
		m.mode = modeNormal
		verb := "created"
		if msg.edited {
			verb = "updated"
		}
		n := len(m.create.attendees)
		if n > 0 {
			m.done(srcAction, fmt.Sprintf("%s \"%s\" | invited %d · %s to undo", verb, m.create.title, n, m.keyName(ctxSchedule, "undo")))
		} else {
			m.done(srcAction, fmt.Sprintf("%s \"%s\" · %s to undo", verb, m.create.title, m.keyName(ctxSchedule, "undo")))
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
			m.fail(srcAction, "delete failed: "+msg.err.Error())
			return m, nil
		}
		if msg.restore != nil {
			m.lastUndo = msg.restore
			m.done(srcAction, fmt.Sprintf("deleted \"%s\" · %s to undo", msg.restore.label, m.keyName(ctxSchedule, "undo")))
		} else {
			m.done(srcAction, "event deleted")
		}
		return m, m.reload()
	case undoneMsg:
		if msg.err != nil {
			m.err = msg.err
			m.fail(srcAction, "undo failed: "+msg.err.Error())
			return m, nil
		}
		m.lastUndo = nil
		m.done(srcAction, "undid: "+msg.label)
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
				m.fail(srcAction, "save failed: "+msg.err.Error()+" · "+m.keyName(ctxPending, "save")+" retries, "+m.keyName(ctxPending, "discard")+" discards")
				return m, nil
			}
			m.fail(srcAction, "shift failed: "+msg.err.Error())
			return m, nil
		}
		if msg.committed {
			m.pending = nil
		}
		m.lastUndo = msg.restore
		m.done(srcAction, msg.label+" · "+m.keyName(ctxSchedule, "undo")+" to undo")
		m.loading = true
		return m, m.reload()
	case tea.KeyMsg:
		// A key between two clicks breaks the pair: click, move away and back
		// by keyboard, click again must not count as a double click.
		m.clicks = mouse.Clicks{}
		// A key is the "next action" that ends a selection, as in the mod
		// engine; cleared here, not in handleKey, for the same reason.
		m.textSel.Clear()
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.selectionMouse(msg)
	case selection.CopiedMsg:
		return m.copied(msg)
	}
	return m, nil
}
