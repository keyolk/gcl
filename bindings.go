package main

import (
	"time"

	"github.com/keyolk/tuikit/keys"
)

// The one key table (engine principle E3).
//
// Every key gcl reacts to is a named action in one context, and the same
// table drives the dispatcher (dispatch.go), the hint bar and the `?` help.
// The hand-written hint sets (hints.go) and help text (helpLines) are gone:
// they had already drifted once (the help still advertised keys by mode the
// bar no longer named), and with one table they cannot.
//
// What hints.go got right is kept, expressed through the table instead:
//
//   - state-dependent hints: Opts.Visible hides a key that would do nothing
//     right now (E/X/y without an event, A/L on an event with no attendees or
//     links, u with nothing to undo), and Opts.State relabels a key whose
//     meaning flips (g reads "grid" or "list", O "overlay" or "overlay set").
//   - `? help` and `q quit` are Pinned: never dropped, however narrow.
//   - whole hints are dropped from the low-priority end, never sliced.
//   - no East-Asian-ambiguous glyphs: key names are words, separators ASCII,
//     and the kit's "…" marker is swapped for "~" (asciiEllipsis).
//
// User overrides live in ~/.config/gcl/keys.yaml (keys.ConfigPath), checked
// against allStacks so a rebinding cannot hide quit or help.

// Contexts. Each mode activates a stack of them, innermost first (activeStack).
const (
	ctxAbort    keys.Ctx = "abort"    // ctrl+c, in every stack
	ctxGlobal   keys.Ctx = "global"   // q and ?
	ctxSchedule keys.Ctx = "schedule" // the agenda/grid, whatever the view
	ctxList     keys.Ctx = "list"     // list view movement
	ctxGrid     keys.Ctx = "grid"     // week/month grid movement
	ctxDetail   keys.Ctx = "detail"   // grid day-detail pane, once focused
	ctxNudge    keys.Ctx = "nudge"    // staged time changes and duplicates
	ctxPending  keys.Ctx = "pending"  // while a time change is staged
	ctxPanel    keys.Ctx = "panel"    // the active-now panel, once focused
	ctxYank     keys.Ctx = "yank"     // the key after y
	ctxHelp     keys.Ctx = "help"
	ctxFormHelp keys.Ctx = "formhelp"
	ctxSearch   keys.Ctx = "search"
	ctxPicker   keys.Ctx = "picker"  // calendar / link / attendee pickers
	ctxForm     keys.Ctx = "form"    // create/edit form, moving between fields
	ctxField    keys.Ctx = "field"   // typing into a text field
	ctxSuggest  keys.Ctx = "suggest" // location suggestions while typing
	ctxInvite   keys.Ctx = "invite"  // the attendee field
	ctxSubmit   keys.Ctx = "submit"  // confirm a create/edit
	ctxDelete   keys.Ctx = "delete"  // confirm a delete
	ctxOverlay  keys.Ctx = "overlay" // overlay calendar picker
	ctxPeople   keys.Ctx = "people"  // find a time: who
	ctxSlots    keys.Ctx = "slots"   // find a time: when
)

// Footer priorities: lower is kept longer when the bar is narrow. The tiers
// are hints.go's: moving around beats acting on an event, which beats
// switching what is loaded, which beats the conveniences.
const (
	prioNav   = 1
	prioAct   = 2
	prioLoad  = 3
	prioExtra = 4
)

// Help sections, in the order a reader needs them.
const (
	grpGeneral = "General"
	grpList    = "List view"
	grpGrid    = "Grid views"
	grpEvent   = "Selected event"
	grpTime    = "Time changes (staged until s)"
	grpCopy    = "Copy (after y)"
	grpPanel   = "Active-now panel (after a, then tab)"
	grpViews   = "Views and calendars"
	grpFind    = "Find a time (f), slot list"
	grpForm    = "Form: moving between fields"
	grpField   = "Form: typing in a field"
	grpInvite  = "Form: attendees"
)

func b(ctx keys.Ctx, action, group string, ks []string, help string, footer int) keys.Binding {
	return keys.Binding{Action: action, Ctx: ctx, Group: group, Keys: ks, Help: help, Footer: footer}
}

func k(ks ...string) []string { return ks }

func short(x keys.Binding, s string) keys.Binding      { x.Short = s; return x }
func group(x keys.Binding, g string) keys.Binding      { x.FooterGroup = g; return x }
func pinned(x keys.Binding) keys.Binding               { x.Pinned = true; return x }
func shadows(x keys.Binding, a ...string) keys.Binding { x.Shadows = a; return x }
func latinOnly(x keys.Binding) keys.Binding            { x.LatinOnly = true; return x }
func labels(x keys.Binding, l map[string]string) keys.Binding {
	x.Labels = l
	return x
}

// groupLabels relabels x's FooterGroup by state: h/l reads "day", "week" or
// "month" after the list step (keyState).
func groupLabels(x keys.Binding, l map[string]string) keys.Binding {
	x.GroupLabels = l
	return x
}

// stepLabels names the list step on the h/l hint.
var stepLabels = map[string]string{"day": "day", "week": "week", "month": "month"}

// nudgeBindings are the staged-change keys. They are declared twice: in
// ctxNudge, where the bar calls the group "move/resize", and in ctxPending,
// where the same keys read "keep adjusting". Same action names, so a user
// override moves both.
func nudgeBindings(ctx keys.Ctx, footerGroup string) []keys.Binding {
	return []keys.Binding{
		group(b(ctx, "nudge.later", grpTime, k(")"), "start 15m later (keeps the length)", prioExtra), footerGroup),
		group(b(ctx, "nudge.earlier", grpTime, k("("), "start 15m earlier", prioExtra), footerGroup),
		group(b(ctx, "nudge.longer", grpTime, k("}"), "15m longer (keeps the start)", prioExtra), footerGroup),
		group(b(ctx, "nudge.shorter", grpTime, k("{"), "15m shorter", prioExtra), footerGroup),
		group(b(ctx, "nudge.nextday", grpTime, k(">"), "next calendar day, same wall-clock time", prioExtra), footerGroup),
		group(b(ctx, "nudge.prevday", grpTime, k("<"), "previous calendar day", prioExtra), footerGroup),
	}
}

func newKeymap() keys.Keymap {
	bs := []keys.Binding{
		b(ctxAbort, "abort", grpGeneral, k("ctrl+c"), "quit from anywhere, even past an unsaved change", 0),
		pinned(short(b(ctxGlobal, "quit", grpGeneral, k("q"), "quit (refuses while a change is unsaved)", 0), "quit")),
		pinned(b(ctxGlobal, "help", grpGeneral, k("?"), "help", 0)),

		// List view.
		groupLabels(group(b(ctxList, "list.prev", grpList, k("h", "left"), "move back one step (day/week/month)", prioNav), "day"), stepLabels),
		groupLabels(group(b(ctxList, "list.next", grpList, k("l", "right"), "move forward one step", prioNav), "day"), stepLabels),
		group(b(ctxList, "list.down", grpList, k("j", "down"), "select the next event", prioNav), "select"),
		group(b(ctxList, "list.up", grpList, k("k", "up"), "select the previous event", prioNav), "select"),
		b(ctxList, "list.last", grpList, k("G", "end"), "select the last loaded event", 0),
		group(b(ctxList, "step.day", grpList, k("d"), "h/l step by day", prioExtra), "step"),
		group(b(ctxList, "step.week", grpList, k("w"), "h/l step by week", prioExtra), "step"),
		group(b(ctxList, "step.month", grpList, k("m"), "h/l step by month", prioExtra), "step"),

		// Grid views. The calendar stays put while the focus moves.
		group(b(ctxGrid, "grid.prev", grpGrid, k("h", "left"), "previous day", prioNav), "day"),
		group(b(ctxGrid, "grid.next", grpGrid, k("l", "right"), "next day", prioNav), "day"),
		group(b(ctxGrid, "grid.down", grpGrid, k("j", "down"), "same day next week", prioNav), "week"),
		group(b(ctxGrid, "grid.up", grpGrid, k("k", "up"), "same day last week", prioNav), "week"),
		group(b(ctxDetail, "detail.down", grpGrid, k("j", "down"), "day detail (after tab): next event", prioNav), "event"),
		group(b(ctxDetail, "detail.up", grpGrid, k("k", "up"), "day detail: previous event", prioNav), "event"),

		// The selected event. The grid acts on the focused day's event.
		short(b(ctxSchedule, "now", grpGeneral, k("n"), "jump to now (the next upcoming event)", prioNav), "now"),
		short(b(ctxSchedule, "open", grpEvent, k("enter", "o"), "open the event page in Google Calendar", prioAct), "open"),
		short(b(ctxSchedule, "edit", grpEvent, k("E"), "edit (confirm shows only what changes)", prioAct), "edit"),
		short(b(ctxSchedule, "delete", grpEvent, k("X", "x"), "delete (asks first)", prioExtra), "del"),
		short(b(ctxSchedule, "yank", grpEvent, k("y"), "copy a field of the event (then y/u/s/e/d)", prioExtra), "copy"),
		short(b(ctxSchedule, "attendees", grpEvent, k("A", "p"), "attendees (people): open one's calendar", prioExtra), "attendees"),
		// No free lowercase key reads as "links"; enter opens the event page,
		// which carries the same links, so a Korean layout still reaches them.
		latinOnly(short(b(ctxSchedule, "links", grpEvent, k("L"), "other links (zoom, docs); enter opens the event itself", prioExtra), "links")),
		short(b(ctxSchedule, "new", grpEvent, k("N", "c"), "new event (c: create)", prioAct), "new"),
		short(b(ctxSchedule, "undo", grpEvent, k("u"), "undo the last create/edit/delete/save", prioAct), "undo"),
		b(ctxNudge, "dup", grpTime, k("D", "+"), "duplicate (applies at once)", 0),
		b(ctxNudge, "dup.week", grpTime, k("W"), "copy to the same slot next week (applies at once)", 0),
		// s outside a staged change only explains that nothing is staged.
		b(ctxSchedule, "save", grpTime, k("s"), "SAVE the staged change to Google", 0),

		// What is loaded and how it is shown.
		short(b(ctxSchedule, "calendar", grpViews, k("e"), "switch calendar (recent, subscribed, aliases, or a typed email)", prioLoad), "calendar"),
		labels(short(b(ctxSchedule, "overlay", grpViews, k("O"),
			"overlay several calendars into one agenda (enter with none picked turns it off)", prioLoad), "overlay"),
			map[string]string{"on": "overlay set"}),
		short(b(ctxSchedule, "find", grpViews, k("f"), "find a time across several people", prioLoad), "find-a-time"),
		labels(short(b(ctxSchedule, "view.grid", grpViews, k("g", "v"), "toggle list / week grid", prioExtra), "grid"),
			map[string]string{"grid": "list"}),
		// Latin-only: no free lowercase letter reads as "month". The week grid
		// (g) and the list stepping by month (m) stay reachable on a Korean layout.
		latinOnly(b(ctxSchedule, "view.month", grpViews, k("M"), "month grid", 0)),
		labels(short(b(ctxSchedule, "active", grpViews, k("a"),
			"toggle the active-now panel (never moves the cursor)", prioExtra), "active now"),
			map[string]string{"open": "close"}),
		labels(short(b(ctxSchedule, "focus", grpViews, k("tab"),
			"cycle focus: schedule, day detail (grid), active panel", prioAct), "panel"),
			map[string]string{"panel": "next pane"}),
		short(b(ctxSchedule, "timeline", grpViews, k("t"),
			"24h timeline (list view): a 00-24h bar per row and a per-day density ruler", prioExtra), "timeline"),
		b(ctxSchedule, "refresh", grpViews, k("R"), "refresh", 0),
		b(ctxSchedule, "timezone", grpViews, k("Z", "z"), "cycle the display timezone", 0),
		short(b(ctxSchedule, "search", grpViews, k("/"), "fuzzy search the loaded events", prioLoad), "search"),

		// While a change is staged.
		pinned(short(b(ctxPending, "save", grpTime, k("s"), "SAVE the staged change to Google", prioAct), "SAVE")),
		pinned(short(b(ctxPending, "discard", grpTime, k("esc"), "discard the staged change", prioAct), "discard")),

		// Active-now panel, once focused. Keys it does not claim fall through
		// to the schedule and act on the highlighted event.
		shadows(short(b(ctxPanel, "panel.back", grpPanel, k("esc"), "leave the panel (it stays open; a closes it)", prioNav), "back"), "discard"),
		group(b(ctxPanel, "panel.down", grpPanel, k("j", "down", "ctrl+n"), "next active event", prioNav), "move"),
		group(b(ctxPanel, "panel.up", grpPanel, k("k", "up", "ctrl+p"), "previous active event", prioNav), "move"),
		b(ctxPanel, "panel.first", grpPanel, k("g", "home"), "first", 0),
		b(ctxPanel, "panel.last", grpPanel, k("G", "end"), "last", 0),
		short(b(ctxPanel, "panel.jump", grpPanel, k("enter"), "jump the schedule to it and leave the panel", prioAct), "jump to it"),

		// The key after y.
		short(b(ctxYank, "yank.summary", grpCopy, k("y"), "copy a summary (title, time, place, URL)", prioAct), "summary"),
		short(b(ctxYank, "yank.url", grpCopy, k("u"), "copy the calendar URL", prioAct), "calendar URL"),
		short(b(ctxYank, "yank.start", grpCopy, k("s"), "copy the start (RFC 3339, display timezone)", prioExtra), "start"),
		short(b(ctxYank, "yank.end", grpCopy, k("e"), "copy the end", prioExtra), "end"),
		short(b(ctxYank, "yank.desc", grpCopy, k("d"), "copy the description", prioExtra), "description"),
		pinned(short(b(ctxYank, "yank.cancel", grpCopy, k("esc"), "cancel the copy", 0), "cancel")),

		// Help pages.
		shadows(pinned(short(b(ctxHelp, "help.close", grpGeneral, k("esc", "enter", "?"), "close help", 0), "close")), "help"),
		group(b(ctxHelp, "help.down", grpGeneral, k("j", "down"), "scroll help down", prioNav), "scroll"),
		group(b(ctxHelp, "help.up", grpGeneral, k("k", "up"), "scroll help up", prioNav), "scroll"),
		pinned(short(b(ctxFormHelp, "formhelp.close", grpForm, k("esc", "enter", "?"), "close form help", 0), "close")),
		group(b(ctxFormHelp, "help.down", grpForm, k("j", "down"), "scroll help down", prioNav), "scroll"),
		group(b(ctxFormHelp, "help.up", grpForm, k("k", "up"), "scroll help up", prioNav), "scroll"),

		// Search. Printable keys type into the query.
		pinned(short(b(ctxSearch, "search.cancel", "", k("esc"), "cancel", 0), "cancel")),
		short(b(ctxSearch, "search.jump", "", k("enter"), "jump to the match", prioAct), "jump"),
		group(b(ctxSearch, "search.up", "", k("up", "ctrl+p", "ctrl+k"), "previous match", prioNav), "move"),
		group(b(ctxSearch, "search.down", "", k("down", "ctrl+n", "ctrl+j"), "next match", prioNav), "move"),
		b(ctxSearch, "search.erase", "", k("backspace", "ctrl+h"), "delete a character", 0),

		// Pickers (calendar, links, attendees). Printable keys filter.
		pinned(short(b(ctxPicker, "picker.cancel", "", k("esc"), "cancel", 0), "cancel")),
		labels(short(b(ctxPicker, "picker.choose", "", k("enter"), "open the highlighted entry", prioAct), "open"),
			map[string]string{"attendee": "open their calendar"}),
		group(b(ctxPicker, "picker.up", "", k("up", "ctrl+p", "ctrl+k"), "previous entry", prioNav), "move"),
		group(b(ctxPicker, "picker.down", "", k("down", "ctrl+n", "ctrl+j"), "next entry", prioNav), "move"),
		b(ctxPicker, "picker.erase", "", k("backspace", "ctrl+h"), "delete a character", 0),

		// Overlay picker.
		pinned(short(b(ctxOverlay, "overlay.cancel", "", k("esc"), "cancel", 0), "cancel")),
		group(b(ctxOverlay, "overlay.up", "", k("up", "ctrl+p", "ctrl+k"), "previous calendar", prioNav), "move"),
		group(b(ctxOverlay, "overlay.down", "", k("down", "ctrl+n", "ctrl+j"), "next calendar", prioNav), "move"),
		short(b(ctxOverlay, "overlay.toggle", "", k(" "), "toggle the highlighted calendar", prioAct), "toggle"),
		short(b(ctxOverlay, "overlay.apply", "", k("enter"), "apply (none picked turns the overlay off)", prioAct), "apply (none = off)"),
		b(ctxOverlay, "overlay.erase", "", k("backspace", "ctrl+h"), "delete a character", 0),

		// Find a time: who is coming.
		pinned(short(b(ctxPeople, "people.cancel", "", k("esc"), "cancel", 0), "cancel")),
		group(b(ctxPeople, "people.up", "", k("up", "ctrl+p", "ctrl+k"), "previous person", prioNav), "move"),
		group(b(ctxPeople, "people.down", "", k("down", "ctrl+n", "ctrl+j"), "next person", prioNav), "move"),
		short(b(ctxPeople, "people.toggle", "", k(" "), "toggle (or add a typed email)", prioAct), "toggle"),
		short(b(ctxPeople, "people.search", "", k("enter"), "read free/busy and rank open slots", prioAct), "find slots"),
		b(ctxPeople, "people.erase", "", k("backspace", "ctrl+h"), "delete a character", 0),

		// Find a time: the slot list.
		pinned(short(b(ctxSlots, "slots.back", grpFind, k("esc"), "back to the participant picker", 0), "back to who")),
		group(b(ctxSlots, "slots.down", grpFind, k("j", "down", "ctrl+n"), "next slot", prioNav), "move"),
		group(b(ctxSlots, "slots.up", grpFind, k("k", "up", "ctrl+p"), "previous slot", prioNav), "move"),
		b(ctxSlots, "slots.first", grpFind, k("g", "home"), "first slot", 0),
		b(ctxSlots, "slots.last", grpFind, k("G", "end"), "last slot", 0),
		short(b(ctxSlots, "slots.prefill", grpFind, k("enter"), "prefill a new event with the slot and people", prioAct), "prefill new event"),
		short(b(ctxSlots, "slots.length", grpFind, k("d"), "cycle the meeting length", prioExtra), "length"),
		short(b(ctxSlots, "slots.weekends", grpFind, k("w"), "include or skip weekends", prioExtra), "weekends"),
		short(b(ctxSlots, "slots.hours", grpFind, k("H", "h"), "working hours or the whole day", prioExtra), "hours"),
		short(b(ctxSlots, "slots.refresh", grpFind, k("R"), "read free/busy again", prioExtra), "refresh"),

		// Confirm steps. Anything else cancels a delete.
		pinned(short(b(ctxSubmit, "submit.yes", "", k("y", "Y", "enter"), "send it to Google", 0), "yes")),
		pinned(short(b(ctxSubmit, "submit.no", "", k("esc", "n", "N"), "back to the form", 0), "back to form")),
		pinned(short(b(ctxDelete, "delete.yes", "", k("y", "Y", "enter"), "delete it", 0), "confirm")),
		pinned(short(b(ctxDelete, "delete.no", "", k("esc", "n", "N"), "cancel (so does any other key)", 0), "cancel")),

		// Form, moving between fields.
		group(b(ctxForm, "form.next", grpForm, k("j", "down", "tab"), "next field", prioNav), "field"),
		group(b(ctxForm, "form.prev", grpForm, k("k", "up", "shift+tab"), "previous field", prioNav), "field"),
		short(b(ctxForm, "form.edit", grpForm, k("enter", "i"), "edit the field", prioAct), "edit"),
		b(ctxForm, "form.space", grpForm, k(" "), "edit the field, starting with a space", 0),
		pinned(short(b(ctxForm, "form.help", grpForm, k("?"), "form help: field syntax", 0), "help")),
		pinned(short(b(ctxForm, "form.close", grpForm, k("esc"), "close the form (the input is lost)", 0), "close")),

		// Form, typing into a text field. Printable keys (j and k too) type.
		pinned(short(b(ctxField, "form.help", grpField, k("?"), "form help: field syntax", 0), "help")),
		shadows(pinned(short(b(ctxField, "field.blur", grpField, k("esc"), "stop typing (esc again closes)", 0), "back")), "form.close"),
		short(b(ctxField, "field.submit", grpField, k("enter"), "save the event (from any field; confirm comes next)", prioAct), "save"),
		group(b(ctxField, "field.next", grpField, k("tab", "down"), "next field", prioNav), "field"),
		group(b(ctxField, "field.prev", grpField, k("shift+tab", "up"), "previous field", prioNav), "field"),
		b(ctxField, "field.erase", grpField, k("backspace", "ctrl+h"), "delete a character", 0),
		group(b(ctxSuggest, "suggest.next", grpField, k("ctrl+n"), "location: next suggestion", prioLoad), "pick"),
		group(b(ctxSuggest, "suggest.prev", grpField, k("ctrl+p"), "location: previous suggestion", prioLoad), "pick"),
		short(b(ctxSuggest, "suggest.accept", grpField, k("ctrl+y"), "location: accept the suggestion", prioLoad), "accept"),

		// Form, the attendee field. Printable keys filter the candidates.
		pinned(short(b(ctxInvite, "form.help", grpInvite, k("?"), "form help: field syntax", 0), "help")),
		shadows(pinned(short(b(ctxInvite, "field.blur", grpInvite, k("esc"), "stop typing (esc again closes)", 0), "back")), "form.close"),
		short(b(ctxInvite, "field.submit", grpInvite, k("enter"), "save the event (confirm comes next)", prioAct), "save"),
		short(b(ctxInvite, "invite.toggle", grpInvite, k(" "), "toggle the highlighted person (or a typed email)", prioAct), "toggle attendee"),
		group(b(ctxInvite, "invite.next", grpInvite, k("tab", "down", "ctrl+n", "ctrl+j"), "next field", prioNav), "field"),
		group(b(ctxInvite, "invite.prev", grpInvite, k("shift+tab", "up", "ctrl+p", "ctrl+k"), "previous field", prioNav), "field"),
		b(ctxInvite, "field.erase", grpInvite, k("backspace", "ctrl+h"), "delete a character", 0),
	}
	bs = append(bs, nudgeBindings(ctxNudge, "move/resize")...)
	bs = append(bs, nudgeBindings(ctxPending, "keep adjusting")...)
	return keys.New(bs...)
}

// baseKeymap is the table without user overrides. A model built as a literal
// (every test) has a zero km and falls back to it (model.keymap).
var baseKeymap = newKeymap()

// allStacks is every stack activeStack can return, for Conflicts and for
// validating user overrides. TestActiveStackIsAlwaysAKnownStack keeps the two
// in step.
var allStacks = func() [][]keys.Ctx {
	var out [][]keys.Ctx
	views := [][]keys.Ctx{{ctxList}, {ctxGrid}, {ctxDetail, ctxGrid}}
	for _, panel := range []bool{false, true} {
		for _, pending := range []bool{false, true} {
			for _, view := range views {
				var s []keys.Ctx
				if panel {
					s = append(s, ctxPanel)
				}
				if pending {
					s = append(s, ctxPending)
				}
				s = append(s, view...)
				out = append(out, append(s, ctxNudge, ctxSchedule, ctxGlobal, ctxAbort))
			}
		}
	}
	return append(out,
		[]keys.Ctx{ctxYank, ctxAbort},
		[]keys.Ctx{ctxHelp, ctxGlobal, ctxAbort},
		[]keys.Ctx{ctxFormHelp, ctxAbort},
		[]keys.Ctx{ctxSearch, ctxAbort},
		[]keys.Ctx{ctxPicker, ctxAbort},
		[]keys.Ctx{ctxForm, ctxAbort},
		[]keys.Ctx{ctxField, ctxAbort},
		[]keys.Ctx{ctxSuggest, ctxField, ctxAbort},
		[]keys.Ctx{ctxInvite, ctxAbort},
		[]keys.Ctx{ctxSubmit, ctxAbort},
		[]keys.Ctx{ctxDelete, ctxAbort},
		[]keys.Ctx{ctxOverlay, ctxAbort},
		[]keys.Ctx{ctxPeople, ctxAbort},
		[]keys.Ctx{ctxSlots, ctxAbort},
	)
}()

// keymap is the table this model dispatches with: the user's, or the base one
// for a model that was never given one.
func (m model) keymap() keys.Keymap { return m.km.Or(baseKeymap) }

// activeStack is the contexts that receive keys right now, innermost first.
func (m model) activeStack() []keys.Ctx {
	switch m.mode {
	case modeCreate:
		switch {
		case !m.create.editingField:
			return []keys.Ctx{ctxForm, ctxAbort}
		case m.create.step == stepAttendees:
			return []keys.Ctx{ctxInvite, ctxAbort}
		case m.create.step == stepLocation:
			return []keys.Ctx{ctxSuggest, ctxField, ctxAbort}
		}
		return []keys.Ctx{ctxField, ctxAbort}
	case modeFindTime:
		if m.find.step == findPeople {
			return []keys.Ctx{ctxPeople, ctxAbort}
		}
		return []keys.Ctx{ctxSlots, ctxAbort}
	case modeOverlayPicker:
		return []keys.Ctx{ctxOverlay, ctxAbort}
	case modeConfirmSubmit:
		return []keys.Ctx{ctxSubmit, ctxAbort}
	case modeConfirmDelete:
		return []keys.Ctx{ctxDelete, ctxAbort}
	case modeSearch:
		return []keys.Ctx{ctxSearch, ctxAbort}
	case modeCalendarPicker, modeLinkPicker, modeAttendeePicker:
		return []keys.Ctx{ctxPicker, ctxAbort}
	case modeHelp:
		return []keys.Ctx{ctxHelp, ctxGlobal, ctxAbort}
	case modeFormHelp:
		return []keys.Ctx{ctxFormHelp, ctxAbort}
	}
	// A pending y owns the next key, before anything else can read it
	// (notably, ys copies the start instead of saving a staged move).
	if m.yankPending {
		return []keys.Ctx{ctxYank, ctxAbort}
	}
	var s []keys.Ctx
	if m.activePanelFocused() {
		s = append(s, ctxPanel)
	}
	if m.pending != nil {
		s = append(s, ctxPending)
	}
	if m.view == viewList {
		s = append(s, ctxList)
	} else {
		if m.focusPane == focusDetail {
			s = append(s, ctxDetail)
		}
		s = append(s, ctxGrid)
	}
	return append(s, ctxNudge, ctxSchedule, ctxGlobal, ctxAbort)
}

// keyOpts is the model's view of its actions for the footer and help.
// Enabled is left nil: every bound key runs and its handler explains when
// there is nothing to act on, as before. Visible keeps the bar to keys that
// would do something now — the rule hints.go introduced.
func (m model) keyOpts() keys.Opts {
	return keys.Opts{Visible: m.offered, State: m.keyState}
}

func (m model) offered(action string) bool {
	ev := m.currentActionEvent()
	switch action {
	case "open", "edit", "delete", "yank":
		return ev != nil
	case "attendees":
		return ev != nil && len(ev.Attendees) > 0
	case "links":
		return ev != nil && ev.otherLinkCount() > 0
	case "nudge.later", "nudge.earlier", "nudge.longer", "nudge.shorter", "nudge.nextday", "nudge.prevday":
		// Outside a staged change the group is offered only where it applies:
		// the list view, on a timed event.
		return m.pending != nil || (m.view == viewList && ev != nil && !ev.AllDay())
	case "undo":
		return m.lastUndo != nil
	case "calendar":
		return !m.overlay.on()
	case "active":
		// Offered when it has something to show, or as the way out of the
		// focused panel.
		return m.activePanelFocused() || (!m.activeOpen && len(m.activeEvents(time.Now())) > 0)
	case "focus":
		return m.activeOpen
	case "timeline":
		return m.view == viewList
	}
	return true
}

func (m model) keyState(action string) string {
	switch action {
	case "list.prev", "list.next":
		return m.jumpUnit
	case "overlay":
		if m.overlay.on() {
			return "on"
		}
	case "view.grid":
		if m.view != viewList {
			return "grid"
		}
	case "active":
		if m.activeOpen {
			return "open"
		}
	case "focus":
		if m.activePanelFocused() {
			return "panel"
		}
	case "picker.choose":
		if m.mode == modeAttendeePicker {
			return "attendee"
		}
	}
	return ""
}
