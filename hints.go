package main

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// The hint bar, built from what is actually available right now.
//
// The bar used to name every key the app has — 238 columns of them. That does
// not fit in any terminal, so it was ALWAYS truncated, and what fell off the
// right edge was the tail: `?` and `q`. The two keys a lost user needs most
// were the two the bar reliably hid, while twenty keys that did nothing in the
// current state took the space.
//
// So the bar is assembled per state instead. A key appears only when pressing
// it would do something: E/X/y need a selected event, A and L need that event
// to have attendees or links, t only draws in the list view. What is left fits,
// and the keys that survive are the ones that work.
//
// Two rules hold regardless:
//
//   - `? help` and `q` are pinned to the end and are never dropped. They are
//     the escape hatches; a bar that truncates them is worse than no bar.
//   - when everything still does not fit, whole hints are dropped from the
//     LOWEST priority up rather than the string being cut mid-word. A hint cut
//     to "E ed" teaches nothing and looks broken.
//
// Separators are ASCII. `·` (U+00B7) and `│` (U+2502) both render TWO cells on
// terminals that treat East-Asian-ambiguous glyphs as wide, and a separator
// repeated once per hint would put the bar's real width far past what the fit
// loop measured — the same constraint that keeps detailStyle borderless.

const (
	// hintSep separates hints; hintPinSep sets off the pinned escape keys.
	// ASCII only — see the note above on ambiguous-width separators.
	hintSep    = "  "
	hintPinSep = "   |  "
)

// hint is one key and what it does, with the priority that decides what
// survives a narrow pane. Higher priority is kept longer.
type hint struct {
	key   string
	label string
	prio  int
}

// Priority tiers. The ordering encodes what a user is most likely to be unable
// to proceed without: moving around beats acting on an event, which beats
// switching what is loaded, which beats the conveniences.
const (
	prioPinned = 120 // context that must not be dropped (e.g. WHAT is unsaved)
	prioEscape = 100 // ? and q — pinned, never dropped
	prioNav    = 80  // moving the cursor / the date
	prioAct    = 60  // acting on the selected event
	prioLoad   = 40  // changing what is on screen at all
	prioExtra  = 20  // conveniences that have an alternative
)

// render joins hints into a bar that fits width, dropping whole low-priority
// hints until it does. Pinned hints (prioEscape) are kept even if that means
// dropping everything else.
func renderHints(hints []hint, width int) string {
	// A model that has not had its first WindowSizeMsg yet has width 0, and so
	// does a degenerate resize. Returning nothing there would blank the bar at
	// exactly the moment a user has learned nothing else about the app, so fall
	// back to a width that at least fits the pinned escape keys.
	if width <= 0 {
		width = 40
	}
	// Separate the pinned tail so it can be reserved before anything else is
	// measured — it is the part that must never be the thing that falls off.
	// prioPinned entries stay in the BODY (they are context, and belong where
	// they were written) but are never dropped by the fit loop.
	var body, pinned []hint
	for _, h := range hints {
		if h.prio == prioEscape {
			pinned = append(pinned, h)
		} else {
			body = append(body, h)
		}
	}

	format := func(hs []hint) string {
		parts := make([]string, 0, len(hs))
		for _, h := range hs {
			if h.label == "" {
				parts = append(parts, h.key)
				continue
			}
			parts = append(parts, h.key+" "+h.label)
		}
		return strings.Join(parts, hintSep)
	}

	pinnedStr := format(pinned)
	reserve := lipgloss.Width(pinnedStr) + len(hintPinSep) + 2 // + leading/trailing space
	budget := width - reserve

	// Drop the lowest-priority hints until the rest fits. Equal priorities are
	// dropped from the right, which is where the least-used ones already sit.
	// prioPinned entries are never candidates: on the UNSAVED bar that is WHAT
	// is about to be written to someone else's calendar, and a bar naming `s
	// SAVE` without saying what it saves is worse than no bar.
	for lipgloss.Width(format(body)) > budget {
		lowest, at := 0, -1
		for i, h := range body {
			if h.prio >= prioPinned {
				continue
			}
			if at < 0 || h.prio <= lowest {
				lowest, at = h.prio, i
			}
		}
		if at < 0 {
			break // only unfittable pinned context left; let it overflow
		}
		body = append(body[:at], body[at+1:]...)
	}

	if len(body) == 0 {
		return " " + pinnedStr + " "
	}
	return " " + format(body) + hintPinSep + pinnedStr + " "
}

// escapeHints are the two keys pinned to every bar.
func escapeHints() []hint {
	return []hint{
		{key: "?", label: "help", prio: prioEscape},
		{key: "q", label: "quit", prio: prioEscape},
	}
}

// normalHints is the hint set for the ordinary schedule view: only the keys
// that would actually do something in the current state.
func (m model) normalHints() []hint {
	var hs []hint

	// Navigation is what you need before anything else, and it is the one group
	// that is always applicable.
	if m.view == viewList {
		hs = append(hs,
			hint{key: "h/l", label: m.jumpUnit, prio: prioNav},
			hint{key: "j/k", label: "select", prio: prioNav},
			hint{key: "d/w/m", label: "step", prio: prioExtra},
		)
	} else {
		hs = append(hs,
			hint{key: "h/l", label: "day", prio: prioNav},
			hint{key: "j/k", label: "week", prio: prioNav},
		)
	}
	hs = append(hs, hint{key: "n", label: "now", prio: prioNav})

	// Acting on an event is only offered when there IS one. Half these keys
	// used to sit on the bar over an empty calendar.
	ev := m.currentActionEvent()
	if ev != nil {
		hs = append(hs,
			hint{key: "ret", label: "open", prio: prioAct},
			hint{key: "E", label: "edit", prio: prioAct},
			hint{key: "X", label: "del", prio: prioExtra},
			hint{key: "y", label: "copy", prio: prioExtra},
		)
		// A and L are dead keys on an event that has neither, and they are the
		// two most often dead.
		if len(ev.Attendees) > 0 {
			hs = append(hs, hint{key: "A", label: "attendees", prio: prioExtra})
		}
		if ev.otherLinkCount() > 0 {
			hs = append(hs, hint{key: "L", label: "links", prio: prioExtra})
		}
		// The staged-move keys are a group, not six separate hints — naming
		// each one costs more than the whole group is worth at this priority.
		if m.view == viewList && !ev.AllDay() {
			hs = append(hs, hint{key: ")(}{><", label: "move/resize", prio: prioExtra})
		}
	}

	hs = append(hs, hint{key: "N", label: "new", prio: prioAct})
	if m.lastUndo != nil {
		// Undo is only worth a slot when there is something to undo, but then
		// it is worth a high one: it is how a mistake gets taken back.
		hs = append(hs, hint{key: "u", label: "undo", prio: prioAct})
	}

	// What is loaded / how it is shown.
	if m.overlay.on() {
		hs = append(hs, hint{key: "O", label: "overlay set", prio: prioLoad})
	} else {
		hs = append(hs,
			hint{key: "e", label: "calendar", prio: prioLoad},
			hint{key: "O", label: "overlay", prio: prioExtra},
		)
	}
	hs = append(hs, hint{key: "f", label: "find-a-time", prio: prioLoad})
	if m.view == viewList {
		hs = append(hs,
			hint{key: "g", label: "grid", prio: prioExtra},
			hint{key: "t", label: "timeline", prio: prioExtra},
		)
	} else {
		hs = append(hs, hint{key: "g", label: "list", prio: prioLoad})
	}

	// The active panel advertises itself only when it has something to show, or
	// when it is open and `tab` is the non-obvious next key.
	switch {
	case m.activeOpen:
		hs = append(hs, hint{key: "tab", label: "panel", prio: prioAct})
	case len(m.activeEvents(time.Now())) > 0:
		hs = append(hs, hint{key: "a", label: "active now", prio: prioExtra})
	}

	hs = append(hs, hint{key: "/", label: "search", prio: prioLoad})
	return append(hs, escapeHints()...)
}
