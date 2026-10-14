package main

import (
	"slices"
	"strings"

	"github.com/keyolk/tuikit/keys"
)

// The `?` pages. The key lists come from the binding table (bindings.go), so
// they cannot drift from what the keys do; only the notes below — what the
// timeline draws, what an overlay changes, what the form accepts — are prose,
// because they describe behaviour, not keys.

// helpStacks are the stacks the main help documents, in reading order: the
// list view first, then what only the grid, a staged change, the panel, the
// y prefix and the slot list add.
var helpStacks = [][]keys.Ctx{
	{ctxList, ctxNudge, ctxSchedule, ctxGlobal, ctxAbort},
	{ctxDetail, ctxGrid},
	{ctxPending},
	{ctxPanel},
	{ctxYank},
	{ctxSlots},
}

// formHelpStacks are the form's: moving between fields, typing in one, the
// attendee field and the confirm step.
var formHelpStacks = [][]keys.Ctx{
	{ctxForm, ctxAbort},
	{ctxSuggest, ctxField},
	{ctxInvite},
}

func note(title string, lines ...string) keys.Section {
	return keys.Section{Title: title, Notes: lines}
}

// helpNotes is the main help's prose. ASCII separators only: these lines
// are drawn in a bordered popup on terminals that may draw ambiguous-width
// glyphs two cells wide.
func helpNotes() []keys.Section {
	tzLabels := make([]string, 0, len(settings.timezones))
	for _, t := range settings.timezones {
		tzLabels = append(tzLabels, t.label)
	}
	tzHint := strings.Join(tzLabels, "/")
	if tzHint == "" {
		tzHint = "local"
	}
	return []keys.Section{
		note("About the bar",
			"The bottom bar names only keys that would do something right now; this page lists them all.",
			"Shortcuts also work under a Korean (2-beolsik) input source."),
		note("Timeline (t, list view)",
			"Every row gets a 00-24h bar on one shared axis, so events that overlap line up vertically.",
			"Under each date header a ruler shades how many events run at each hour (grey light, amber/red stacked, scaled across the loaded days) and prints the day's peak.",
			"| is right now; < and > mean the window continues past midnight. Bars are blue when active now, amber when unsaved."),
		note("Active-now panel (a)",
			"Every event in effect at this moment, soonest-ending first. Multi-day windows (maintenance, on-call, PTO) show up even when the agenda has scrolled past them.",
			"a only toggles the panel and never moves the cursor; tab steps into it. The header shows the live count."),
		note("Overlay (O)",
			"Every row gains a color dot and the owner's name, and timeline bars are tinted per person, so the axis shows WHOSE events overlap.",
			"The legend maps color to person with each one's count; a calendar that could not be read is drawn hollow with the reason, never as an empty one.",
			"Edit, delete, duplicate and time moves act on the selected row's calendar (named in the confirm step); N creates on the calendar you have open.",
			"Picking a single calendar with e turns the overlay off."),
		note("Changing events",
			"N, E and X go through a confirm step before anything reaches Google. Editing shows only what changes (old -> new), with time moves and attendee changes marked !.",
			"A start in the past, a duration of 8h or more or under 5m, and an unchanged edit are called out but not blocked.",
			"Time moves only change the view: the new time shows with a ! marker and the bar turns amber until s saves it. Repeated moves compose into one save; q refuses to quit while one is unsaved.",
			"Quick saves and duplicates never mail attendees (use E to notify)."),
		note("Find a time (f)",
			"Pick who is coming (space toggles; type a full email to add someone not on screen), then enter reads everyone's free/busy and ranks the open slots.",
			"Everyone-free slots come first (green 4/4); partial ones (amber 3/4) name who is blocked. ? after a badge counts people whose free/busy is not readable.",
			"Enter on a slot prefills the new-event form; nothing is mailed until you submit it."),
		note("Files",
			"Timezones cycled by Z: "+tzHint,
			"Calendar aliases: "+configPath()+" ([aliases])",
			"Key overrides: "+keys.ConfigPath(appName)+" (action: [keys], e.g. quit: [x])"),
	}
}

// formHelpNotes is the form help's prose: what each field accepts.
func formHelpNotes() []keys.Section {
	return []keys.Section{
		note("Field syntax",
			"Date: today, tmr, yst, +3d, -1d, 2w, +1m, mon..sun, next fri, 7/20, 07-20, 0720, 20, 2026-07-20, 오늘, 내일, 어제",
			"Start: 15:00, 3pm, 3:30pm, 11am, 12am, 12pm, 1530, 930, 15, 15시, 9시30분",
			"Duration: 30, 45m, 90m, 1h, 1.5h, 1h30m, 1일, 30분, 1시간30분",
			"Repeat: daily, weekly, biweekly, monthly, yearly, weekdays (+ x4 for 4 times), 매일, 매주, 격주, 매월, 평일. Unrecognized text is rejected, so no wrong recurrence is created.",
			"Notes: free text (agenda / body)."),
		note("After saving",
			"Time moves on the agenda: ) ( shift 15m, } { resize 15m, > < move a day. They only stage the change: s saves it, esc discards.",
			"Quick actions never mail attendees (use E to notify)."),
	}
}

// helpNames spells keys as words on the help page.
var helpNames = keys.Names{}

// helpPage renders the open help page into the popup's content area and
// returns the offset RenderHelp clamped. Update stores that offset back
// (help.down / help.up), so a held j cannot run the stored offset past the end.
func (m model) helpPage() (lines []string, offset int) {
	w, rows := m.helpBodySize()
	o := keys.Opts{State: m.keyState}
	km := m.keymap()
	var secs, notes []keys.Section
	if m.mode == modeFormHelp {
		secs, notes = km.Help(o, formHelpStacks...), formHelpNotes()
	} else {
		secs, notes = km.Help(o, helpStacks...), helpNotes()
	}
	secs = inReadingOrder(secs)
	st := ui.HelpStyle()
	st.Names = helpNames
	st.Ellipsis = cutMarker
	return keys.RenderHelp(append(secs, notes...), w, rows, m.helpOff, st)
}

// helpOrder is the order sections are read in. Help orders them by where the
// stack walk first meets them, which puts the list's movement keys before
// quit and help.
var helpOrder = []string{grpGeneral, grpList, grpGrid, grpEvent, grpTime, grpViews,
	grpPanel, grpCopy, grpFind, grpForm, grpField, grpInvite}

func inReadingOrder(secs []keys.Section) []keys.Section {
	rank := func(title string) int {
		if i := slices.Index(helpOrder, title); i >= 0 {
			return i
		}
		return len(helpOrder)
	}
	out := slices.Clone(secs)
	slices.SortStableFunc(out, func(a, b keys.Section) int { return rank(a.Title) - rank(b.Title) })
	return out
}

// helpSize is the help popup: wider than the pickers (the descriptions are
// sentences) and as tall as the body allows.
func (m model) helpSize() (w, h int) {
	bodyWidth := max(20, m.width-2)
	w = min(bodyWidth, max(m.preferredOverlayWidth(), 80))
	h = max(5, m.height-4)
	return w, h
}

// helpBodySize is the part of the help popup the key list scrolls in: the
// popup minus border, padding, the title and its blank line, and the hint.
func (m model) helpBodySize() (w, rows int) {
	pw, ph := m.helpSize()
	return max(10, pw-4), max(1, ph-4)
}
