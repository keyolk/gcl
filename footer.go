package main

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/keys"
)

// The hint bar and the key lines inside popups, derived from the key table.
//
// The bar used to name every key the app has — 238 columns of them — so it
// was always truncated, and what fell off was `?` and `q`. hints.go fixed that
// with per-state hint lists; those lists are now the table's Visible/State
// rules (bindings.go), and the fitting is the kit's Footer: whole hints are
// dropped from the low-priority end, `? help` and `q quit` are pinned.

// footerNames spells keys as short words: no glyphs, since ↵ and friends are
// not guaranteed one cell everywhere, and "ret"/"dn" keep the bar compact.
var footerNames = keys.Names{}.With("enter", "ret").With("down", "dn")

// hintKeyStyle is plain on purpose: the hint bar is drawn inside one background
// style, and per-hint styling would reset that background mid-line.
var hintKeyStyle = keys.FooterStyle{Names: footerNames, Ellipsis: cutMarker}

// shortcutHint is the hint bar's text for the current state.
func (m model) shortcutHint() string {
	width := m.width
	if width <= 0 {
		// Before the first WindowSizeMsg the width is 0. Blanking the bar there
		// would hide every key at the moment a user knows least, so fit at
		// least the pinned keys.
		width = 40
	}
	title, stack := m.footerContext(width)
	return m.bar(width, title, stack...)
}

// bar renders " title  hints" in width cells. The title is context the hints
// cannot carry (which mode, what is unsaved) and is never dropped.
func (m model) bar(width int, title string, stack ...keys.Ctx) string {
	prefix := " "
	if title != "" {
		prefix += title + "  "
	}
	rest := max(0, width-lipgloss.Width(prefix))
	return prefix + m.keymap().Footer(stack, rest, m.keyOpts(), hintKeyStyle)
}

// footerContext picks the bar's title and the contexts it lists. It follows
// activeStack, except while a change is staged: then the bar is about that
// change only — the keys that resolve it must not compete with twenty others.
func (m model) footerContext(width int) (string, []keys.Ctx) {
	stack := m.activeStack()
	switch m.mode {
	case modeSearch:
		return "search", stack
	case modeCalendarPicker:
		return "calendar", stack
	case modeLinkPicker:
		return "links", stack
	case modeAttendeePicker:
		return "attendees", stack
	case modeHelp:
		return "help", stack
	case modeFormHelp:
		return "form help", stack
	case modeCreate:
		return "form", stack
	case modeConfirmSubmit:
		return "confirm?", stack
	case modeConfirmDelete:
		return "delete?", stack
	case modeOverlayPicker:
		return "overlay", stack
	case modeFindTime:
		if m.find.step == findPeople {
			return "who?", stack
		}
		return "slots", stack
	}
	if m.yankPending {
		return "yank", stack
	}
	if m.pending != nil {
		return m.unsavedTitle(width), []keys.Ctx{ctxPending}
	}
	if m.activePanelFocused() {
		return "ACTIVE", stack
	}
	return "", stack
}

// unsavedTitle names the staged change. The resulting time rides along
// because the delta alone ("+1d") does not say what the event ENDS UP as, and
// the before -> after line lives in the detail pane, which a narrow window
// does not show. When that does not leave room for `s` and `esc`, the time is
// dropped before the keys are: a bar naming the change but not how to resolve
// it is no better than one naming the keys but not the change.
func (m model) unsavedTitle(width int) string {
	short := "UNSAVED " + m.pending.label()
	full := short + ": " + m.pending.diffLine(m.tz())
	resolve := keys.Opts{Visible: func(a string) bool { return a == "save" || a == "discard" }}
	need := lipgloss.Width(m.keymap().Footer([]keys.Ctx{ctxPending}, 1000, resolve, hintKeyStyle))
	if width-lipgloss.Width(" "+full+"  ") >= need {
		return full
	}
	return short
}

// keyHint is a muted key line for inside a popup, from the same table as the
// bar, so the popup cannot advertise a key that does something else.
func (m model) keyHint(width int, stack ...keys.Ctx) string {
	return mutedStyle.Render(m.keymap().Footer(stack, width, m.keyOpts(), hintKeyStyle))
}
