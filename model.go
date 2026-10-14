package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// calendarAliases maps short names to a calendar email/id/display-name. It is
// populated from the dotfile at startup (see config.go); defaultAliases seeds a
// fresh config file. Kept as a package var so the existing lookups still work.
var calendarAliases = map[string]string{}

type tzOption struct {
	label string
	zone  string // IANA name; "" means system local
}

// Timezones cycled with the Z shortcut live in settings.timezones (from config).

func (m model) tz() *time.Location {
	tzs := settings.timezones
	if len(tzs) == 0 {
		return time.Local
	}
	opt := tzs[m.tzIndex%len(tzs)]
	if opt.zone == "" {
		return time.Local
	}
	if loc, err := time.LoadLocation(opt.zone); err == nil {
		return loc
	}
	return time.Local
}

func (m model) tzLabel() string {
	tzs := settings.timezones
	if len(tzs) == 0 {
		return "local"
	}
	return tzs[m.tzIndex%len(tzs)].label
}

// timeLabel formats an event's time in the currently-selected timezone.
func (m model) timeLabel(ev *Event) string {
	if ev.AllDay() || ev.StartAt.IsZero() {
		return ev.TimeLabel()
	}
	loc := m.tz()
	s := ev.StartAt.In(loc).Format("15:04")
	if !ev.EndAt.IsZero() {
		return s + "-" + ev.EndAt.In(loc).Format("15:04")
	}
	return s
}

type Event struct {
	ID            string
	StartDate     time.Time
	EndDate       time.Time
	StartTime     string
	EndTime       string
	StartAt       time.Time // zero for all-day; carries the original tz for display conversion
	EndAt         time.Time
	Title         string
	Location      string
	Rooms         []string // meeting-room resources (resource=true attendees)
	Description   string
	Calendar      string
	AttendeeEmail string
	Attendees     []string
	HTMLLink      string
	ConferenceURI string
	Links         []string
	// ReminderMins lists the "minutes before start" for each reminder set on the
	// event (from reminders.overrides, or the calendar's defaults when the event
	// uses reminders.useDefault). Empty means no reminder configured.
	ReminderMins []int
}

func (e Event) AllDay() bool { return e.StartTime == "" }

// attendeeSummary returns a short "a, b +N" summary from the full attendee list,
// falling back to the single TSV email column when the list is unavailable.
func (e Event) attendeeSummary() string {
	names := e.Attendees
	if len(names) == 0 {
		if e.AttendeeEmail == "" {
			return ""
		}
		names = []string{e.AttendeeEmail}
	}
	short := make([]string, 0, len(names))
	for _, n := range names {
		short = append(short, strings.SplitN(n, "@", 2)[0])
	}
	if len(short) <= 3 {
		return fmt.Sprintf("%d | %s", len(names), strings.Join(short, ", "))
	}
	return fmt.Sprintf("%d | %s +%d", len(names), strings.Join(short[:3], ", "), len(short)-3)
}

// otherLinks returns links other than the Google Calendar event page (which is
// opened by Enter). These are the ones surfaced through the L picker.
func (e Event) otherLinks() []string {
	var out []string
	for _, l := range e.Links {
		if strings.Contains(l, "google.com/calendar/event") {
			continue
		}
		out = append(out, l)
	}
	return out
}

func (e Event) otherLinkCount() int { return len(e.otherLinks()) }

func (e Event) TimeLabel() string {
	if e.AllDay() {
		return "all-day"
	}
	if e.EndTime != "" {
		return e.StartTime + "-" + e.EndTime
	}
	return e.StartTime
}

type eventsMsg struct {
	events []Event
	err    error
	start  time.Time
	end    time.Time
	reqID  int
}

type fetchTickMsg struct{ reqID int }

// notifyTickMsg fires the in-app reminder watcher on an interval.
type notifyTickMsg struct{}

// notifyResultMsg reports one in-app reminder scan. shown carries reminders that
// had no toast sink and must be surfaced in the status line instead.
type notifyResultMsg struct {
	fired int
	shown []string
	err   error
}

// clockTickMsg is a pure repaint trigger for time-relative UI (active count,
// "now" divider, countdowns). It carries no data and changes no state.
type clockTickMsg struct{}

type statusMsg string

// recentsMsg delivers the recents list read at startup.
type recentsMsg struct{ list []string }

// recentsSavedMsg reports the write of the recents list after a pick.
type recentsSavedMsg struct{ err error }

type viewMode int

const (
	viewList  viewMode = iota // continuous agenda around the focus date
	viewWeek                  // week grid, one row per week
	viewMonth                 // month grid, one row per week
)

type paneFocus int

const (
	focusMain   paneFocus = iota
	focusDetail           // grid: the right/bottom day-detail pane
	focusActive           // the docked "active now" panel (only when it is open)
)

type model struct {
	calendarKey string
	calendar    string
	view        viewMode
	anchor      time.Time // focus date
	loadedStart time.Time // inclusive loaded event window
	loadedEnd   time.Time // exclusive loaded event window
	events      []Event
	selected    int
	loading     bool
	status      string
	err         error
	width       int
	height      int
	mode        inputMode
	input       string
	searchIndex int
	picker      pickerState
	tzIndex     int
	create      createState
	jumpUnit    string    // list view h/l step: "day" | "week" | "month"
	gridTop     time.Time // grid view: first visible week (Sunday); stable while navigating
	focusPane   paneFocus // grid: whether navigation targets calendar or right/bottom detail pane
	gridDetail  int       // grid detail selection within the focused day
	nextReqID   int       // monotonically increasing fetch request id
	inflightReq int       // last fetch request id actually issued
	// lastUndo holds a single level of undo for the most recent
	// create/patch/delete, so a mistyped submit or accidental delete can be
	// reversed with `u` without leaving the TUI.
	lastUndo *undoEntry
	// preEdit snapshots the event's fields when an edit form opens, so a
	// successful patch can be reversed back to those values.
	preEdit *undoEntry
	// pending holds a staged (not yet saved) time change from the nudge/resize/
	// day-move keys. The views render the staged time; only `s` writes it to
	// Google. nil means nothing is staged.
	pending *pendingShift
	// activeOpen toggles the docked "active now" panel. It is NOT a mode: the
	// panel appears alongside the schedule and normal navigation keeps working
	// until you deliberately move focus into it with `tab`.
	activeOpen bool
	// activeIndex is the cursor inside the active panel (used once focused).
	activeIndex int
	// yankPending means `y` was pressed and the next key chooses which event
	// field is copied. It is a prefix, not a modal overlay.
	yankPending bool
	// timelineHidden turns OFF the per-row 24h timeline bars and the per-day
	// density ruler in the list agenda (`t` toggles). Overlap is the whole point
	// of a maintenance-window calendar, so the zero value -- and the default --
	// is to SHOW them; narrow panes drop them automatically regardless.
	timelineHidden bool
	// find drives the "find a time" flow (`f`): pick people, read their
	// free/busy, and prefill the create form from a mutually-open slot.
	find findState
	// overlay drives multi-calendar viewing (`O`): several people's events are
	// merged into m.events and tagged by owner, so every existing view works on
	// them unchanged. Empty active set == no overlay.
	overlay overlayState
	// pendingNote is a status message that must survive the "loading~" a fetch
	// installs, to be shown once that fetch lands. Without it, anything worth
	// saying at the moment a reload is triggered is immediately overwritten.
	pendingNote string
	// recents is the recently-picked calendar list, most recent first. It is
	// read from disk by a Cmd at startup (recentsMsg) and written back by a Cmd
	// on every pick, so Update never touches the file itself.
	recents []string
}

// createState holds the step-by-step new-event form.
type createState struct {
	step         createStep
	title        string
	date         string // YYYY-MM-DD (accepts flexible input, normalized on submit)
	start        string // HH:MM (accepts flexible input, normalized on submit)
	durationStr  string // raw duration input ("30", "1h", "1h30m")
	duration     int    // parsed minutes
	location     string // event location (free text)
	description  string // event description / notes
	repeat       string // repeat shorthand: "", daily, weekly, biweekly, monthly, weekdays (optionally "weekly x4")
	attendees    []string
	attInput     string       // fuzzy filter text in attendee step
	attCandidx   int          // highlighted candidate index
	attCands     []pickerItem // attendee candidate pool (fuzzy-filtered against attInput)
	locCands     []pickerItem // location suggestions drawn from recently-seen events
	locCandidx   int          // highlighted location suggestion
	locSuggest   bool         // true while the location suggestion list is open
	selected     map[string]bool
	err          string
	submitting   bool
	editingField bool   // false=navigate fields with j/k, true=edit current field / attendee filter
	editing      bool   // true = patch existing event, false = insert new
	eventID      string // target event id when editing
	// calendar is the calendar this form writes to. It is normally m.calendar,
	// but an edit opened from an OVERLAY row must patch that row's own
	// calendar — patching someone else's event against your own calendar id
	// either 404s or hits a same-titled event on the wrong calendar.
	calendar string
	// orig snapshots the event's values when an edit form opens, so the confirm
	// step can show what actually CHANGES rather than restating the whole form.
	// "Which fields am I about to touch?" is the question that catches a
	// mis-typed edit, and a summary of every field cannot answer it.
	orig *editSnapshot
}

// editSnapshot is the pre-edit value of every field the form can change. Only
// set when editing an existing event.
type editSnapshot struct {
	title       string
	date        string // YYYY-MM-DD in the display tz
	start       string // HH:MM in the display tz
	duration    int    // minutes
	location    string
	description string
	attendees   []string // sorted
}

type createStep int

const (
	stepTitle createStep = iota
	stepDate
	stepStart
	stepDuration
	stepLocation
	stepRepeat
	stepDescription
	stepAttendees
	stepConfirm
)

// textSteps are the free-text fields, in form order. stepAttendees has its own
// candidate-picker handling and is deliberately excluded.
var textSteps = []createStep{stepTitle, stepDate, stepStart, stepDuration, stepLocation, stepRepeat, stepDescription}

type inputMode int

const (
	modeNormal inputMode = iota
	modeCalendarPicker
	modeLinkPicker
	modeAttendeePicker
	modeHelp
	modeFormHelp
	modeConfirmSubmit
	modeSearch
	modeCreate
	modeConfirmDelete
	// modeFindTime is the two-step "find a time" overlay: pick participants,
	// then pick from the slots that are mutually open.
	modeFindTime
	// modeOverlayPicker chooses which calendars are merged into the agenda.
	modeOverlayPicker
)

type pickerKind int

const (
	pickerLinks pickerKind = iota
	pickerCalendar
	pickerAttendee
)

type pickerItem struct {
	label string
	value string
}

type pickerState struct {
	title string
	kind  pickerKind
	items []pickerItem
	index int
}

func (p pickerState) filtered(query string) []pickerItem {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return p.items
	}
	type scored struct {
		it    pickerItem
		score int
	}
	var out []scored
	for _, it := range p.items {
		s := fuzzyScore(query, strings.ToLower(it.label+" "+it.value))
		if s > 0 {
			out = append(out, scored{it, s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	res := make([]pickerItem, len(out))
	for i, s := range out {
		res[i] = s.it
	}
	return res
}

type searchMatch struct {
	eventIndex int
	score      int
	label      string
}

type createdMsg struct {
	link     string
	err      error
	edited   bool   // true when this was a patch of an existing event
	eventID  string // id of the created/updated event (for undo)
	calendar string // calendar the event landed on (for undo)
}

type deletedMsg struct {
	err error
	// Snapshot of the deleted event so `u` can recreate it.
	restore *undoEntry
}

// undoKind distinguishes what the last mutating action was, so `u` knows how to
// reverse it.
type undoKind int

const (
	undoNone   undoKind = iota
	undoCreate          // reverse by deleting the created event
	undoDelete          // reverse by re-creating the deleted event
	undoPatch           // reverse by patching the event back to its previous field values
)

// undoEntry captures enough state to reverse the last create/patch/delete.
// Only one level of undo is kept — it exists to recover from a mistyped submit
// or an accidental delete, not as a full history.
type undoEntry struct {
	kind     undoKind
	calendar string
	eventID  string
	label    string // human-readable summary for the status line
	// Snapshot of the event's fields, used by undoDelete (recreate) and
	// undoPatch (restore previous values).
	title       string
	start       time.Time
	end         time.Time
	location    string
	description string
	attendees   []string
}

type undoneMsg struct {
	err   error
	label string
}

// eventShiftedMsg reports the result of a quick action (duplicate) or of saving
// a staged time change, performed directly on the selected event without
// opening the form.
type eventShiftedMsg struct {
	err     error
	label   string
	restore *undoEntry
	// committed marks this as the result of saving a staged change (`s`), so the
	// handler knows to clear the staged state. Duplicates leave it false.
	committed bool
}

// statusText is the status line's text. Tests read it instead of the field so
// they keep working when the status is a toast queue.
func (m model) statusText() string { return m.status }
