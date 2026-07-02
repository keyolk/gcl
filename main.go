package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"html"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

var (
	urlRe = regexp.MustCompile(`https?://[^\s<>'")]+`)
	tagRe = regexp.MustCompile(`<[^>]+>`)
)

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
		return fmt.Sprintf("%d · %s", len(names), strings.Join(short, ", "))
	}
	return fmt.Sprintf("%d · %s +%d", len(names), strings.Join(short[:3], ", "), len(short)-3)
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

type statusMsg string

type viewMode int

const (
	viewList  viewMode = iota // continuous agenda around the focus date
	viewWeek                  // week grid, one row per week
	viewMonth                 // month grid, one row per week
)

type paneFocus int

const (
	focusMain paneFocus = iota
	focusDetail
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
}

// createState holds the step-by-step new-event form.
type createState struct {
	step         createStep
	title        string
	date         string // YYYY-MM-DD
	start        string // HH:MM
	durationStr  string // raw duration input ("30", "1h")
	duration     int    // parsed minutes
	attendees    []string
	attInput     string       // fuzzy filter text in attendee step
	attCandidx   int          // highlighted candidate index
	attCands     []pickerItem // attendee candidate pool (fuzzy-filtered against attInput)
	selected     map[string]bool
	err          string
	submitting   bool
	editingField bool   // false=navigate fields with j/k, true=edit current field / attendee filter
	editing      bool   // true = patch existing event, false = insert new
	eventID      string // target event id when editing
}

type createStep int

const (
	stepTitle createStep = iota
	stepDate
	stepStart
	stepDuration
	stepAttendees
	stepConfirm
)

type inputMode int

const (
	modeNormal inputMode = iota
	modeCalendarPicker
	modeLinkPicker
	modeAttendeePicker
	modeHelp
	modeSearch
	modeCreate
	modeConfirmDelete
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

var (
	topBarStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("62")).
			Padding(0, 1)
	helpStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	dayStyle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	dayHeaderStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	nowLineStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
	sectionTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("230")).
				Background(lipgloss.Color("63")).
				Padding(0, 1)
	selectedStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("75")).
			Background(lipgloss.Color("236")).
			Padding(0, 1)
	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)
	// detailStyle intentionally uses NO border of any kind: many terminals treat
	// box-drawing and other ambiguous-width glyphs as double-width, which desyncs
	// the frame and clips the bottom edge. Plain-space padding (always width 1)
	// gives separation with zero ambiguous-width characters.
	detailStyle = lipgloss.NewStyle().
			PaddingLeft(2).
			PaddingRight(1)
	// modalStyle is for floating overlays (create/edit, pickers, confirm) where a
	// visible frame helps them stand out over the body. It keeps a rounded border;
	// overlays are short so the occasional double-width terminal quirk is minor.
	modalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 1)
	pillStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("60")).Padding(0, 1)
	// Compact agenda row styles.
	selectedRowStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	timePillMini     = lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	timeSlotSelStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("232")).Background(lipgloss.Color("81"))
	mutedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	linkStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	statusStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	errorStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	hintBarStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("237"))
	calPillStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("232")).Background(lipgloss.Color("212")).Padding(0, 1)
	metaStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("189")).Background(lipgloss.Color("62")).Padding(0, 1)
	tzPillStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("232")).Background(lipgloss.Color("150")).Padding(0, 1)
)

func main() {
	calendarFlag := flag.String("calendar", "", "calendar alias/email/name (default: config default_calendar)")
	calendarShort := flag.String("c", "", "calendar alias/email/name")
	dateFlag := flag.String("date", "", "anchor date YYYY-MM-DD")
	dumpFlag := flag.Bool("dump", false, "print events and exit")
	notifyFlag := flag.Bool("notify", false, "send tmux notifications for upcoming events in the selected calendar")
	notifyWindowFlag := flag.Int("notify-window", 15, "fallback minutes-before-start for events with no reminder set (events with reminders use their own)")
	flag.Parse()

	// Calendar aliases are user-managed in a dotfile (~/.config/<app>/config).
	// A missing file is seeded with sensible defaults on first run.
	if _, err := loadConfig(); err != nil {
		fmt.Fprintf(os.Stderr, appName+": config warning: %v\n", err)
	}

	// Flags win over the config default; -c is shorthand for --calendar.
	calendar := settings.defaultCalendar
	if *calendarFlag != "" {
		calendar = *calendarFlag
	}
	if *calendarShort != "" {
		calendar = *calendarShort
	}
	if calendar == "" {
		calendar = "me"
	}

	anchor := today()
	if *dateFlag != "" {
		parsed, err := time.ParseInLocation("2006-01-02", *dateFlag, time.Local)
		if err != nil {
			fatalf("invalid --date: %v", err)
		}
		anchor = parsed
	}

	// We reach the Calendar API directly using gcalcli's stored OAuth. gcalcli
	// itself is only needed as a fallback, so just ensure the oauth exists.
	if _, err := os.Stat(gcalcliOAuthPath()); err != nil {
		fatalf("gcalcli oauth not found at %s. Run gcal.sh init (gcalcli init) first", gcalcliOAuthPath())
	}

	if *dumpFlag {
		events, err := fetchEvents(resolveCalendar(calendar), anchor, anchor.AddDate(0, 0, 7))
		if err != nil {
			fatalf("%v", err)
		}
		for _, ev := range events {
			fmt.Printf("%s %-13s %s\n", ev.StartDate.Format("2006-01-02"), ev.TimeLabel(), ev.Title)
			if ev.Location != "" {
				fmt.Printf("  location: %s\n", ev.Location)
			}
			if len(ev.Rooms) > 0 {
				fmt.Printf("  rooms: %s\n", strings.Join(ev.Rooms, ", "))
			}
			if s := ev.attendeeSummary(); s != "" {
				fmt.Printf("  attendees: %s\n", s)
			}
			if len(ev.ReminderMins) > 0 {
				parts := make([]string, len(ev.ReminderMins))
				for i, mmin := range ev.ReminderMins {
					parts[i] = fmt.Sprintf("%dm", mmin)
				}
				fmt.Printf("  reminders: %s\n", strings.Join(parts, ", "))
			}
			for i, link := range ev.Links {
				if i >= 3 {
					break
				}
				fmt.Printf("  link: %s\n", link)
			}
		}
		return
	}

	if *notifyFlag {
		if err := notifyUpcoming(resolveCalendar(calendar), *notifyWindowFlag); err != nil {
			fatalf("notify: %v", err)
		}
		return
	}

	m := model{
		calendarKey: calendar,
		calendar:    resolveCalendar(calendar),
		view:        viewList,
		jumpUnit:    settings.defaultStep,
		anchor:      anchor,
		loading:     true,
		status:      "loading…",
		nextReqID:   1,
		inflightReq: 1,
	}
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadCmd(m.inflightReq)}
	if settings.notify {
		cmds = append(cmds, notifyTickCmd())
	}
	return tea.Batch(cmds...)
}

// notifyTickCmd schedules the next in-app reminder scan.
func notifyTickCmd() tea.Cmd {
	interval := time.Duration(max(5, settings.notifyInterval)) * time.Second
	return tea.Tick(interval, func(time.Time) tea.Msg { return notifyTickMsg{} })
}

// notifyScanCmd runs one scan-and-toast pass for the current calendar off the
// UI thread, then reports how many reminders fired via a status message.
func (m model) notifyScanCmd() tea.Cmd {
	calendar := m.calendar
	return func() tea.Msg {
		_ = notifyUpcoming(calendar, settings.notifyWindow)
		return nil
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
	case eventsMsg:
		// Drop stale responses from superseded requests.
		if msg.reqID != m.inflightReq {
			return m, nil
		}
		m.loading = false
		m.err = msg.err
		if msg.err != nil {
			m.events = nil
			m.selected = 0
			m.status = "load failed"
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
		return m, nil
	case statusMsg:
		m.status = string(msg)
		return m, nil
	case createdMsg:
		m.create.submitting = false
		if msg.err != nil {
			m.create.err = msg.err.Error()
			return m, nil
		}
		m.mode = modeNormal
		n := len(m.create.attendees)
		if n > 0 {
			m.status = fmt.Sprintf("created “%s” · invited %d", m.create.title, n)
		} else {
			m.status = fmt.Sprintf("created “%s”", m.create.title)
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
		m.status = "event deleted"
		return m, m.reload()
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.mode == modeCreate {
		return m.handleCreateKey(msg)
	}

	if m.mode == modeConfirmDelete {
		switch key {
		case "y", "Y", "enter":
			ev := m.selectedEvent()
			if ev == nil || ev.ID == "" {
				m.mode = modeNormal
				return m, nil
			}
			cal := m.calendar
			id := ev.ID
			notify := len(ev.Attendees) > 0
			m.status = "deleting…"
			return m, func() tea.Msg {
				err := deleteEvent(cal, id, notify)
				return deletedMsg{err: err}
			}
		default: // n / N / esc / anything else cancels
			m.mode = modeNormal
			m.status = "delete cancelled"
		}
		return m, nil
	}

	// Search overlay: fuzzy-jump across the loaded events.
	if m.mode == modeSearch {
		switch key {
		case "esc", "ctrl+c":
			m.mode = modeNormal
			m.input = ""
			m.searchIndex = 0
		case "enter":
			matches := m.searchMatches()
			if len(matches) > 0 {
				idx := matches[max(0, min(m.searchIndex, len(matches)-1))].eventIndex
				m.selected = idx
				m.anchor = m.events[idx].StartDate
				if m.view != viewList {
					m.focusPane = focusMain
					m.gridDetail = 0
					m.keepGridStable()
				}
				m.status = "jumped to " + m.events[m.selected].Title
			}
			m.mode = modeNormal
			m.input = ""
			m.searchIndex = 0
		case "backspace", "ctrl+h":
			if m.input != "" {
				_, size := utf8.DecodeLastRuneInString(m.input)
				m.input = m.input[:len(m.input)-size]
				m.searchIndex = 0
			}
		case "down", "ctrl+n", "ctrl+j":
			m.searchIndex = min(m.searchIndex+1, max(0, len(m.searchMatches())-1))
		case "up", "ctrl+p", "ctrl+k":
			m.searchIndex = max(0, m.searchIndex-1)
		default:
			if key == "space" {
				m.input += " "
				m.searchIndex = 0
			} else if len(key) == 1 {
				m.input += msg.String()
				m.searchIndex = 0
			}
		}
		return m, nil
	}

	// Fuzzy picker overlays: links / calendars / attendees.
	if m.mode == modeCalendarPicker || m.mode == modeLinkPicker || m.mode == modeAttendeePicker {
		items := m.picker.filtered(m.input)
		switch key {
		case "esc", "ctrl+c":
			m.mode = modeNormal
			m.input = ""
			return m, nil
		case "enter":
			if len(items) > 0 {
				return m.choosePicker(items[max(0, min(m.picker.index, len(items)-1))])
			}
			// Calendar picker: no candidate matched, but the user typed a raw
			// email/calendar id — open it directly instead of doing nothing.
			if m.mode == modeCalendarPicker {
				raw := strings.TrimSpace(m.input)
				if strings.Contains(raw, "@") {
					return m.choosePicker(pickerItem{label: raw, value: raw})
				}
			}
			return m, nil
		case "backspace", "ctrl+h":
			if m.input != "" {
				_, size := utf8.DecodeLastRuneInString(m.input)
				m.input = m.input[:len(m.input)-size]
				m.picker.index = 0
			}
			return m, nil
		case "down", "ctrl+n", "ctrl+j":
			m.picker.index = min(m.picker.index+1, max(0, len(items)-1))
			return m, nil
		case "up", "ctrl+p", "ctrl+k":
			m.picker.index = max(0, m.picker.index-1)
			return m, nil
		default:
			if key == "space" {
				m.input += " "
				m.picker.index = 0
			} else if len(key) == 1 {
				m.input += msg.String()
				m.picker.index = 0
			}
			return m, nil
		}
	}

	// Help overlay: close with esc / enter / ? (q is reserved for quitting).
	if m.mode == modeHelp {
		if key == "esc" || key == "enter" || key == "?" {
			m.mode = modeNormal
		}
		return m, nil
	}

	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "down", "j":
		if m.view == viewList {
			m.selected = min(m.selected+1, max(0, len(m.events)-1))
			m.syncAnchorToSelected()
		} else if m.focusPane == focusDetail {
			m.gridDetail++
			m.clampGridDetail()
		} else {
			return m, m.moveFocusByDays(7)
		}
	case "up", "k":
		if m.view == viewList {
			m.selected = max(0, m.selected-1)
			m.syncAnchorToSelected()
		} else if m.focusPane == focusDetail {
			m.gridDetail--
			m.clampGridDetail()
		} else {
			return m, m.moveFocusByDays(-7)
		}
	case "G", "end":
		if m.view == viewList {
			m.selected = max(0, len(m.events)-1)
			m.syncAnchorToSelected()
		}
	case "right", "l":
		return m, m.moveFocus(1)
	case "left", "h":
		return m, m.moveFocus(-1)
	case "n":
		// Jump to now: select the next upcoming event (the one just below the
		// "now" divider), falling back to the last loaded event when all past.
		m.anchor = today()
		m.selected = m.firstEventIndexAtOrAfterNow()
		if m.view != viewList {
			m.resetGridTop()
		}
		if !m.anchorLoaded() {
			return m, m.reload()
		}
		return m, nil
	case "d":
		m.jumpUnit = "day"
		m.status = "step: day"
	case "w":
		m.jumpUnit = "week"
		m.status = "step: week"
	case "m":
		m.jumpUnit = "month"
		m.status = "step: month"
	case "g", "v":
		// Toggle grid view. g → week grid; from grid → back to list.
		if m.view == viewList {
			m.view = viewWeek
			m.resetGridTop()
		} else {
			m.view = viewList
		}
		return m, m.reload()
	case "M":
		m.view = viewMonth
		m.resetGridTop()
		return m, m.reload()
	case "X":
		if ev := m.currentActionEvent(); ev != nil && ev.ID != "" {
			m.mode = modeConfirmDelete
		} else {
			m.status = "no event selected"
		}
	case "tab":
		if m.view != viewList {
			if m.focusPane == focusMain {
				m.focusPane = focusDetail
				m.clampGridDetail()
			} else {
				m.focusPane = focusMain
			}
		}
	case "r":
		return m, m.reload()
	case "Z":
		if n := len(settings.timezones); n > 0 {
			m.tzIndex = (m.tzIndex + 1) % n
		}
		m.status = "timezone: " + m.tzLabel()
	case "enter", "o":
		return m, m.openPrimary()
	case "L":
		if ev := m.currentActionEvent(); ev != nil && len(ev.otherLinks()) > 0 {
			links := ev.otherLinks()
			items := make([]pickerItem, len(links))
			for i, link := range links {
				items[i] = pickerItem{label: link, value: link}
			}
			m.mode = modeLinkPicker
			m.picker = pickerState{title: "Links — Enter opens", kind: pickerLinks, items: items}
			m.input = ""
		} else {
			m.status = "no extra links (Enter opens the calendar event)"
		}
	case "A":
		if ev := m.currentActionEvent(); ev != nil && len(ev.Attendees) > 0 {
			items := make([]pickerItem, 0, len(ev.Attendees))
			for _, a := range ev.Attendees {
				email := attendeeEmail(a)
				items = append(items, pickerItem{label: a, value: email})
			}
			m.mode = modeAttendeePicker
			m.picker = pickerState{title: "Attendees — Enter opens their calendar", kind: pickerAttendee, items: items}
			m.input = ""
		} else {
			m.status = "selected event has no attendees"
		}
	case "/":
		m.mode = modeSearch
		m.input = ""
		m.searchIndex = 0
	case "e":
		m.mode = modeCalendarPicker
		m.picker = pickerState{title: "Calendar — type to filter, Enter to open", kind: pickerCalendar, items: calendarPickerItems()}
		m.input = ""
	case "N":
		m.mode = modeCreate
		m.create = m.newCreateState()
	case "E":
		if ev := m.currentActionEvent(); ev != nil {
			if ev.StartAt.IsZero() {
				m.status = "can't edit all-day events here yet"
			} else {
				m.mode = modeCreate
				m.create = m.editCreateState(ev)
			}
		}
	case "?":
		m.mode = modeHelp
	}
	return m, nil
}

// reload marks loading and returns the fetch command.
func (m *model) reload() tea.Cmd {
	return m.scheduleFetch()
}

// scheduleFetch debounces bursty navigation. The request id is bumped on every
// call; only the latest tick is allowed to issue an actual loadCmd.
func (m *model) scheduleFetch() tea.Cmd {
	m.nextReqID++
	m.inflightReq = m.nextReqID
	m.loading = true
	m.status = "loading…"
	return m.loadCmd(m.inflightReq)
}

// moveFocus shifts the focus date by the current jump unit (list) or by a day
// (grid), reloading if the new date leaves the loaded window.
func (m *model) moveFocus(direction int) tea.Cmd {
	switch m.view {
	case viewWeek, viewMonth:
		m.anchor = m.anchor.AddDate(0, 0, direction)
	default:
		switch m.jumpUnit {
		case "week":
			m.anchor = m.anchor.AddDate(0, 0, 7*direction)
		case "month":
			m.anchor = m.anchor.AddDate(0, direction, 0)
		default:
			m.anchor = m.anchor.AddDate(0, 0, direction)
		}
	}
	// Snap list selection to the first event on/after the new focus date.
	m.selected = m.firstEventIndexOnOrAfter(m.anchor)
	moved := m.keepGridStable()
	if moved || !m.anchorLoaded() {
		return m.reload()
	}
	return nil
}

// moveFocusByDays shifts the focus date by a fixed number of days, reloading
// only when the new date leaves the loaded window (avoids grid flicker on j/k).
func (m *model) moveFocusByDays(days int) tea.Cmd {
	m.anchor = m.anchor.AddDate(0, 0, days)
	m.selected = m.firstEventIndexOnOrAfter(m.anchor)
	moved := m.keepGridStable()
	if moved || !m.anchorLoaded() {
		return m.reload()
	}
	return nil
}

// resetGridTop anchors the visible grid window to the week (week view) or month
// (month view) containing the focus date.
func (m *model) resetGridTop() {
	if m.view == viewMonth {
		m.gridTop = weekStart(monthStart(m.anchor))
	} else {
		m.gridTop = weekStart(m.anchor)
	}
}

// keepGridStable keeps the visible grid window fixed while the focus moves; it
// only re-anchors (scrolls) when the focus leaves the window. Returns true when
// the window (gridTop) actually moved, so the caller can trigger a reload.
func (m *model) keepGridStable() bool {
	if m.view == viewList {
		return false
	}
	if m.gridTop.IsZero() {
		m.resetGridTop()
		return true
	}
	weeks, _ := m.gridLayout(max(4, m.height-4))
	fw := weekStart(m.anchor)
	old := m.gridTop
	delta := int(fw.Sub(m.gridTop).Hours()) / (24 * 7)
	if delta < 0 {
		m.gridTop = fw
	} else if delta >= weeks {
		m.gridTop = fw.AddDate(0, 0, -7*(weeks-1))
	}
	return !m.gridTop.Equal(old)
}

// syncAnchorToSelected keeps the focus date aligned with the selected event so
// the header date reflects what's highlighted.
func (m *model) syncAnchorToSelected() {
	if ev := m.selectedEvent(); ev != nil {
		m.anchor = ev.StartDate
	}
}

func (m model) firstEventIndexOnOrAfter(day time.Time) int {
	for i := range m.events {
		if !m.events[i].StartDate.Before(day) {
			return i
		}
	}
	return max(0, len(m.events)-1)
}

// eventSortInstant is the moment used to order an event against "now": the real
// start for timed events, midnight for all-day ones. Must match the divider
// placement in viewAgendaCards so `n` lands exactly on the "now" line.
func eventSortInstant(ev *Event) time.Time {
	if ev.AllDay() {
		return ev.StartDate
	}
	return ev.StartAt
}

// firstEventIndexAtOrAfterNow returns the first event starting at/after the
// current instant — the event just below the "now" divider. Falls back to the
// last event when everything is in the past.
func (m model) firstEventIndexAtOrAfterNow() int {
	now := time.Now()
	for i := range m.events {
		if !eventSortInstant(&m.events[i]).Before(now) {
			return i
		}
	}
	return max(0, len(m.events)-1)
}

// editCreateState pre-fills the form from an existing event for editing.
func (m model) editCreateState(ev *Event) createState {
	loc := m.tz()
	sel := map[string]bool{}
	for _, a := range ev.Attendees {
		if e := attendeeEmail(a); e != "" {
			sel[e] = true
		}
	}
	dur := 30
	if !ev.EndAt.IsZero() {
		dur = int(ev.EndAt.Sub(ev.StartAt).Minutes())
		if dur <= 0 {
			dur = 30
		}
	}
	return createState{
		step:         stepTitle,
		title:        ev.Title,
		date:         ev.StartAt.In(loc).Format("2006-01-02"),
		start:        ev.StartAt.In(loc).Format("15:04"),
		durationStr:  fmt.Sprintf("%d", dur),
		duration:     dur,
		selected:     sel,
		attCands:     m.attendeeCandidatePool(),
		editingField: true,
		editing:      true,
		eventID:      ev.ID,
	}
}

type createdMsg struct {
	link string
	err  error
}

type deletedMsg struct {
	err error
}

func (m model) handleCreateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	c := &m.create

	if key == "ctrl+c" {
		m.mode = modeNormal
		return m, nil
	}

	nextField := func(delta int) {
		step := int(c.step) + delta
		if step < int(stepTitle) {
			step = int(stepAttendees)
		}
		if step > int(stepAttendees) {
			step = int(stepTitle)
		}
		c.step = createStep(step)
		c.err = ""
	}

	// First ESC exits text-entry mode into field-navigation mode; second ESC closes.
	if key == "esc" {
		if c.editingField {
			c.editingField = false
			c.err = ""
			return m, nil
		}
		m.mode = modeNormal
		return m, nil
	}

	switch c.step {
	case stepTitle, stepDate, stepStart, stepDuration:
		field := c.fieldPtr()
		if !c.editingField {
			switch key {
			case "j", "down", "tab":
				nextField(1)
			case "k", "up", "shift+tab":
				nextField(-1)
			case "enter", "i":
				c.editingField = true
			case "space":
				c.editingField = true
				*field += " "
			}
			return m, nil
		}
		switch key {
		// While editing a text field, j/k must TYPE — only Tab/⇧Tab (and arrows,
		// which are inert in a single-line field) move between fields.
		case "tab", "down":
			c.editingField = false
			nextField(1)
		case "shift+tab", "up":
			c.editingField = false
			nextField(-1)
		case "enter":
			if err := c.validateAll(); err != "" {
				c.err = err
			} else {
				c.finalizeAttendees()
				c.submitting = true
				c.err = ""
				return m, m.submitCreateCmd()
			}
		case "backspace", "ctrl+h":
			if len(*field) > 0 {
				_, size := utf8.DecodeLastRuneInString(*field)
				*field = (*field)[:len(*field)-size]
			}
		default:
			if key == "space" {
				*field += " "
			} else if len(key) == 1 {
				*field += msg.String()
			}
		}
		return m, nil

	case stepAttendees:
		cands := filterPickerItems(c.attCands, c.attInput)
		if !c.editingField {
			switch key {
			case "j", "down", "tab":
				nextField(1)
			case "k", "up", "shift+tab":
				nextField(-1)
			case "enter", "i":
				c.editingField = true
			}
			return m, nil
		}
		switch key {
		case "tab", "down", "ctrl+n", "ctrl+j":
			c.editingField = false
			nextField(1)
		case "shift+tab", "up", "ctrl+p", "ctrl+k":
			c.editingField = false
			nextField(-1)
		case "space":
			if len(cands) > 0 {
				it := cands[max(0, min(c.attCandidx, len(cands)-1))]
				c.toggle(it.value)
			} else if strings.Contains(strings.TrimSpace(c.attInput), "@") {
				c.toggle(strings.TrimSpace(c.attInput))
				c.attInput = ""
			}
		case "enter":
			if err := c.validateAll(); err != "" {
				c.err = err
			} else {
				c.finalizeAttendees()
				c.submitting = true
				c.err = ""
				return m, m.submitCreateCmd()
			}
		case "backspace", "ctrl+h":
			if len(c.attInput) > 0 {
				_, size := utf8.DecodeLastRuneInString(c.attInput)
				c.attInput = c.attInput[:len(c.attInput)-size]
				c.attCandidx = 0
			}
		default:
			if len(key) == 1 {
				c.attInput += msg.String()
				c.attCandidx = 0
			}
		}
		return m, nil
	}
	return m, nil
}

// fieldPtr returns a pointer to the text field for the current text step.
func (c *createState) fieldPtr() *string {
	switch c.step {
	case stepTitle:
		return &c.title
	case stepDate:
		return &c.date
	case stepStart:
		return &c.start
	case stepDuration:
		return &c.durationStr
	}
	return &c.title
}

func (c *createState) validateStep() string {
	switch c.step {
	case stepTitle:
		if strings.TrimSpace(c.title) == "" {
			return "title is required"
		}
	case stepDate:
		if _, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(c.date), time.Local); err != nil {
			return "date must be YYYY-MM-DD"
		}
	case stepStart:
		if _, err := time.Parse("15:04", strings.TrimSpace(c.start)); err != nil {
			return "start must be HH:MM (24h)"
		}
	case stepDuration:
		if _, err := parseDurationMinutes(c.durationStr); err != nil {
			return "duration must be minutes, e.g. 30 or 1h"
		}
	}
	return ""
}

// validateAll checks the whole form for the single-screen create/edit flow.
func (c *createState) validateAll() string {
	for _, step := range []createStep{stepTitle, stepDate, stepStart, stepDuration} {
		prev := c.step
		c.step = step
		if err := c.validateStep(); err != "" {
			c.step = prev
			return err
		}
		c.step = prev
	}
	if d, err := parseDurationMinutes(c.durationStr); err == nil {
		c.duration = d
	}
	return ""
}

func (c *createState) advance(m model) {
	switch c.step {
	case stepTitle:
		c.step = stepDate
	case stepDate:
		c.step = stepStart
	case stepStart:
		c.step = stepDuration
	case stepDuration:
		if d, err := parseDurationMinutes(c.durationStr); err == nil {
			c.duration = d
		}
		c.step = stepAttendees
	}
}

func (c *createState) toggle(email string) {
	if email == "" {
		return
	}
	if c.selected == nil {
		c.selected = map[string]bool{}
	}
	c.selected[email] = !c.selected[email]
	if !c.selected[email] {
		delete(c.selected, email)
	}
}

func (c *createState) finalizeAttendees() {
	c.attendees = c.attendees[:0]
	for e := range c.selected {
		c.attendees = append(c.attendees, e)
	}
	sort.Strings(c.attendees)
}

func filterPickerItems(items []pickerItem, query string) []pickerItem {
	ps := pickerState{items: items}
	return ps.filtered(query)
}

func parseDurationMinutes(s string) (int, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	if strings.HasSuffix(s, "h") {
		var h float64
		if _, err := fmt.Sscanf(s, "%fh", &h); err == nil && h > 0 {
			return int(h * 60), nil
		}
	}
	s = strings.TrimSuffix(s, "m")
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil && n > 0 {
		return n, nil
	}
	return 0, fmt.Errorf("invalid duration")
}

func (m model) submitCreateCmd() tea.Cmd {
	c := m.create
	cal := m.calendar
	loc := m.tz()
	return func() tea.Msg {
		day, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(c.date), loc)
		if err != nil {
			return createdMsg{err: err}
		}
		hm, err := time.Parse("15:04", strings.TrimSpace(c.start))
		if err != nil {
			return createdMsg{err: err}
		}
		start := time.Date(day.Year(), day.Month(), day.Day(), hm.Hour(), hm.Minute(), 0, 0, loc)
		end := start.Add(time.Duration(c.duration) * time.Minute)
		if c.editing {
			link, err := patchEvent(patchEventInput{
				Calendar:  cal,
				EventID:   c.eventID,
				Title:     strings.TrimSpace(c.title),
				Start:     start,
				End:       end,
				Attendees: c.attendees,
				Notify:    len(c.attendees) > 0,
			})
			return createdMsg{link: link, err: err}
		}
		link, err := createEvent(createEventInput{
			Calendar:  cal,
			Title:     strings.TrimSpace(c.title),
			Start:     start,
			End:       end,
			Attendees: c.attendees,
			Notify:    len(c.attendees) > 0,
		})
		return createdMsg{link: link, err: err}
	}
}

// newCreateState seeds the new-event form from current context: the anchor date
// and a sensible default time/duration.
func (m model) newCreateState() createState {
	start := settings.eventTime
	now := time.Now().In(m.tz())
	if sameDay(m.anchor, time.Now()) {
		// round up to next 30 min for "today"
		mm := now.Minute()
		if mm < 30 {
			start = now.Format("15") + ":30"
		} else {
			start = now.Add(time.Hour).Format("15") + ":00"
		}
	}
	dur := settings.eventDuration
	return createState{
		step:         stepTitle,
		date:         m.anchor.Format("2006-01-02"),
		start:        start,
		durationStr:  fmt.Sprintf("%d", dur),
		duration:     dur,
		selected:     map[string]bool{},
		attCands:     m.attendeeCandidatePool(),
		editingField: true,
	}
}

// attendeeCandidatePool gathers likely invitees: recent calendars (emails) and
// every attendee email seen across currently-loaded events.
func (m model) attendeeCandidatePool() []pickerItem {
	seen := map[string]bool{}
	var out []pickerItem
	add := func(email string) {
		email = strings.TrimSpace(email)
		if email == "" || !strings.Contains(email, "@") || seen[strings.ToLower(email)] {
			return
		}
		seen[strings.ToLower(email)] = true
		out = append(out, pickerItem{label: email, value: email})
	}
	for _, r := range loadRecentCalendars() {
		add(r)
	}
	for i := range m.events {
		for _, a := range m.events[i].Attendees {
			add(attendeeEmail(a))
		}
	}
	return out
}

func (m model) choosePicker(it pickerItem) (tea.Model, tea.Cmd) {
	switch m.picker.kind {
	case pickerLinks:
		m.mode = modeNormal
		m.input = ""
		return m, openCmd(it.value)
	case pickerCalendar, pickerAttendee:
		if it.value == "" {
			m.status = "no calendar id for selection"
			m.mode = modeNormal
			m.input = ""
			return m, nil
		}
		m.mode = modeNormal
		m.input = ""
		m.err = nil
		m.calendarKey = it.value
		m.calendar = it.value
		m.loading = true
		m.status = "loading…"
		rememberCalendar(it.value)
		return m, m.reload()
	}
	m.mode = modeNormal
	m.input = ""
	return m, nil
}

type splitKind int

const (
	splitNone splitKind = iota
	splitRight
	splitBottom
)

// splitMode picks where the detail pane goes based on pane geometry: wide panes
// split right, tall/narrow panes split bottom, tiny panes show no detail.
func (m model) splitMode() splitKind {
	// Terminal cells are ~2x taller than wide, so weight width to compare shape.
	if m.width >= 100 && m.width >= m.height*2 {
		return splitRight
	}
	if m.width >= 90 {
		return splitRight
	}
	if m.height >= 18 {
		return splitBottom
	}
	return splitNone
}

// fitPane clamps a rendered pane to an exact width/height so split layouts pin
// cleanly to the right/bottom edges instead of collapsing to content width.
func fitPane(s string, width, height int) string {
	s = clampLineWidth(s, width)
	s = clampToHeight(s, height)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if pad := width - lipgloss.Width(l); pad > 0 {
			lines[i] = l + strings.Repeat(" ", pad)
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) View() string {
	if m.width <= 0 {
		return "loading…"
	}
	viewName := map[viewMode]string{viewList: "LIST", viewWeek: "WEEK", viewMonth: "MONTH"}[m.view]
	calPill := calPillStyle.Render("📅 " + m.calendarDisplayName())
	tzPill := tzPillStyle.Render("🕓 " + m.tzLabel())
	metaText := fmt.Sprintf("%s · %s", viewName, m.anchor.Format("2006-01-02"))
	if m.view == viewList {
		metaText = fmt.Sprintf("%s · %s · step:%s", viewName, m.anchor.Format("2006-01-02"), m.jumpUnit)
	}
	meta := metaStyle.Render(" " + metaText + " ")
	headerContent := calPill + meta + tzPill
	headerLine := topBarStyle.Width(max(0, m.width-2)).Render(truncate(headerContent, max(1, m.width-2)))

	// The frame is header (1) + body + status (1) + hint (1); body must therefore
	// be height-3 rows so the whole thing fills exactly m.height.
	contentHeight := max(4, m.height-3)
	bodyWidth := max(20, m.width-2)
	var body string
	if m.loading {
		body = cardStyle.Width(max(20, bodyWidth-4)).Render("loading calendar events…")
	} else if m.err != nil {
		body = errorStyle.Render(wrap(m.err.Error(), bodyWidth))
	} else {
		// The schedule (list agenda or grid) plus a detail pane. The detail pane
		// splits to the RIGHT on wide panes and to the BOTTOM on tall/narrow ones.
		schedule := func(w, h int) string {
			if m.view == viewList {
				return m.viewScheduleCards(w, h)
			}
			return m.viewGrid(w, h)
		}
		switch m.splitMode() {
		case splitRight:
			rightW := max(34, bodyWidth/3)
			leftW := max(30, bodyWidth-rightW-1)
			left := fitPane(schedule(leftW, contentHeight), leftW, contentHeight)
			right := fitPane(m.viewDetailCard(rightW, contentHeight), rightW, contentHeight)
			body = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
		case splitBottom:
			detailH := max(6, contentHeight/3)
			topH := max(4, contentHeight-detailH)
			top := fitPane(schedule(bodyWidth, topH), bodyWidth, topH)
			bottom := fitPane(m.viewDetailCard(bodyWidth, detailH), bodyWidth, detailH)
			body = top + "\n" + bottom
		default: // no split — too small for a detail pane
			body = schedule(bodyWidth, contentHeight)
		}
	}

	body = clampLineWidth(body, bodyWidth)
	body = clampToHeight(body, contentHeight)

	// Modal overlays (create/edit, delete, pickers) still float bottom-right.
	if m.mode == modeCreate {
		body = overlayBottomRight(body, m.viewCreate(), bodyWidth, contentHeight)
	} else if m.mode == modeConfirmDelete {
		body = overlayBottomRight(body, m.viewConfirmDelete(), bodyWidth, contentHeight)
	} else if m.mode != modeNormal {
		body = overlayBottomRight(body, m.viewPopup(), bodyWidth, contentHeight)
	}

	statusLine := statusStyle.Render(truncate("  "+m.status, m.width))
	hintLine := hintBarStyle.Width(max(0, m.width)).Render(truncate(m.shortcutHint(), max(1, m.width)))

	var b strings.Builder
	b.WriteString(headerLine)
	b.WriteByte('\n')
	b.WriteString(body)
	b.WriteByte('\n')
	b.WriteString(statusLine)
	b.WriteByte('\n')
	b.WriteString(hintLine)
	return b.String()
}

// clampToHeight makes s occupy exactly n rows: extra rows are dropped, missing
// rows are padded with blanks. Keeps the bottom status/hint bars pinned.
func clampToHeight(s string, n int) string {
	if n < 0 {
		n = 0
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// clampLineWidth truncates every line to at most width display cells so a wide
// line never wraps and pushes the layout down.
func clampLineWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if lipgloss.Width(l) > width {
			lines[i] = ansiTruncate(l, width)
		}
	}
	return strings.Join(lines, "\n")
}

// overlayBottomRight draws `top` (the modal) over `base` (the schedule/detail
// content) anchored to the bottom-right of a totalWidth x totalHeight area, so
// the content behind the modal stays visible instead of being wiped.
func overlayBottomRight(base, top string, totalWidth, totalHeight int) string {
	return overlayCorner(base, top, totalWidth, totalHeight, false)
}

// overlayCorner draws `top` over `base` at the right edge, anchored to the top
// (atTop=true) or bottom (atTop=false) of the area. Content behind stays visible.
func overlayCorner(base, top string, totalWidth, totalHeight int, atTop bool) string {
	baseLines := strings.Split(base, "\n")
	for len(baseLines) < totalHeight {
		baseLines = append(baseLines, "")
	}
	topLines := strings.Split(top, "\n")
	th := len(topLines)
	tw := 0
	for _, l := range topLines {
		if w := lipgloss.Width(l); w > tw {
			tw = w
		}
	}
	rowStart := max(0, totalHeight-th)
	if atTop {
		rowStart = 0
	}
	colStart := max(0, totalWidth-tw-1)

	for i, tl := range topLines {
		row := rowStart + i
		if row >= len(baseLines) {
			break
		}
		left := ansiTruncate(baseLines[row], colStart)
		if pad := colStart - lipgloss.Width(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		baseLines[row] = left + tl
	}
	return strings.Join(baseLines, "\n")
}

// ansiTruncate cuts an ANSI-styled string to at most `width` display cells.
func ansiTruncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "")
}

func (m model) shortcutHint() string {
	switch m.mode {
	case modeSearch:
		return " / fuzzy search  ·  ↑/↓ move  ·  Enter jump  ·  ESC cancel "
	case modeCalendarPicker:
		return " e calendar  ·  type to filter  ·  ↑/↓ move  ·  Enter open  ·  ESC cancel "
	case modeLinkPicker:
		return " L links  ·  type to filter  ·  ↑/↓ move  ·  Enter open  ·  ESC cancel "
	case modeAttendeePicker:
		return " A attendees  ·  Enter → open their calendar  ·  ↑/↓ move  ·  ESC cancel "
	case modeHelp:
		return " help  ·  ESC / ? close "
	default:
		if m.view != viewList {
			return " h/l ±day · j/k ±week · g→list · M month-grid · n now · A attendees · L links · E edit · X del · N new · e cal · Z tz · / · ? · q "
		}
		return " h/l move(step) · d/w/m step · j/k select · n now · g grid · Z tz · N new · E edit · X del · ↵ open · L links · A attendees · e cal · / search · ? · q "
	case modeCreate:
		return " new/edit event  ·  type to edit  ·  Enter next/save  ·  ESC back  ·  Space toggle attendee "
	case modeConfirmDelete:
		return " delete event?  ·  y/Enter confirm  ·  N/ESC cancel "
	}
}

func (m model) viewScheduleCards(width, height int) string {
	if len(m.events) == 0 {
		return cardStyle.Width(max(20, width-2)).Render("No events in this range")
	}
	return m.viewAgendaCards(width, height)
}

// viewGrid renders week/month as one row per week (Sun..Sat), each day a small
// cell with a count; the focused day is highlighted and its events show in the
// toggleable detail modal.
// gridLayout returns how many weeks and event-rows-per-week fit in `height`.
// Deterministic so the renderer and the scroll logic agree.
func (m model) gridLayout(height int) (weeks, evRows int) {
	avail := max(3, height-1) // minus weekday header
	maxWeeks := 6
	if m.view == viewWeek {
		maxWeeks = 10
	}
	weeks = maxWeeks
	for weeks > 1 && (avail/weeks) < 2 {
		weeks--
	}
	evRows = max(1, avail/max(1, weeks)-1)
	if evRows > 6 {
		evRows = 6
	}
	return weeks, evRows
}

func (m model) viewGrid(width, height int) string {
	firstWeek := m.gridTop
	if firstWeek.IsZero() {
		firstWeek = weekStart(m.anchor)
		if m.view == viewMonth {
			firstWeek = weekStart(monthStart(m.anchor))
		}
	}
	colW := max(12, width/7)
	inner := max(6, colW-1)

	// events grouped by day
	byDay := map[string][]*Event{}
	for i := range m.events {
		k := m.events[i].StartDate.Format("2006-01-02")
		byDay[k] = append(byDay[k], &m.events[i])
	}

	weeks, _ := m.gridLayout(height)

	// Per-week event-row counts are VARIABLE: a week only takes as many rows as
	// its busiest day needs (capped), so empty weeks stay a single date row and
	// never balloon into big blank blocks.
	weekMax := make([]int, weeks)
	for w := 0; w < weeks; w++ {
		mx := 0
		for d := 0; d < 7; d++ {
			day := firstWeek.AddDate(0, 0, w*7+d)
			if n := len(byDay[day.Format("2006-01-02")]); n > mx {
				mx = n
			}
		}
		weekMax[w] = mx
	}
	// Distribute the available body height (minus header + one date row/week).
	budget := max(0, (height-1)-weeks) // rows left for events after date rows
	perCap := 6
	rowsFor := make([]int, weeks)
	// First pass: give each week min(need, perCap) greedily until budget runs out.
	for w := 0; w < weeks && budget > 0; w++ {
		want := min(weekMax[w], perCap)
		give := min(want, budget)
		rowsFor[w] = give
		budget -= give
	}

	var out []string
	var hdr []string
	for _, wd := range []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"} {
		hdr = append(hdr, dayStyle.Render(padCenter(wd, colW)))
	}
	out = append(out, strings.Join(hdr, ""))

	for w := 0; w < weeks; w++ {
		// date row
		var dateCells []string
		for d := 0; d < 7; d++ {
			day := firstWeek.AddDate(0, 0, w*7+d)
			focused := sameDay(day, m.anchor)
			isToday := sameDay(day, time.Now())
			label := day.Format("1/2")
			if isToday {
				label = "•" + label
			}
			cnt := len(byDay[day.Format("2006-01-02")])
			if cnt > 0 {
				label += fmt.Sprintf(" (%d)", cnt)
			}
			dim := m.view == viewMonth && day.Month() != m.anchor.Month()
			cell := padRightW(" "+label, colW)
			switch {
			case focused:
				dateCells = append(dateCells, timeSlotSelStyle.Render(cell))
			case dim:
				dateCells = append(dateCells, mutedStyle.Render(cell))
			default:
				dateCells = append(dateCells, dayStyle.Render(cell))
			}
		}
		out = append(out, strings.Join(dateCells, ""))

		// event rows (variable per week)
		evRows := rowsFor[w]
		for r := 0; r < evRows; r++ {
			var cells []string
			for d := 0; d < 7; d++ {
				day := firstWeek.AddDate(0, 0, w*7+d)
				evs := byDay[day.Format("2006-01-02")]
				txt := ""
				if r == evRows-1 && len(evs) > evRows {
					txt = mutedStyle.Render(fmt.Sprintf("  +%d more", len(evs)-(evRows-1)))
				} else if r < len(evs) {
					ev := evs[r]
					tm := ""
					if !ev.AllDay() && !ev.StartAt.IsZero() {
						tm = ev.StartAt.In(m.tz()).Format("15:04") + " "
					}
					txt = " " + truncate(tm+ev.Title, inner-1)
				}
				cell := padRightW(txt, colW)
				if sameDay(day, m.anchor) {
					cell = selectedRowStyle.Render(cell)
				}
				cells = append(cells, cell)
			}
			out = append(out, strings.Join(cells, ""))
		}
	}
	return strings.Join(out, "\n")
}

// padRightW pads/truncates a (possibly styled) string to exactly width cells.
func padRightW(s string, width int) string {
	if lipgloss.Width(s) > width {
		return ansiTruncate(s, width)
	}
	if pad := width - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

func padCenter(s string, width int) string {
	sw := lipgloss.Width(s)
	if sw >= width {
		return truncate(s, width)
	}
	left := (width - sw) / 2
	right := width - sw - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

// viewAgendaCards renders a COMPACT one-line-per-event agenda so many events
// fit on screen. Each row costs 1 line; day headers cost 1. A viewport keeps
// the selected event visible.
func (m model) viewAgendaCards(width, height int) string {
	if height <= 0 {
		return ""
	}
	var lines []string
	innerWidth := max(20, width-1)
	start := m.agendaStartIndex(height)
	current := ""
	selected := m.selectedEvent()
	now := time.Now()
	nowShown := false
	nowLine := func() string {
		return nowLineStyle.Render(" ── now " + now.In(m.tz()).Format("15:04") + " " + strings.Repeat("─", max(2, innerWidth-14)))
	}
	// truncated marks that not everything fit; the last row is replaced with a
	// "… more" hint. Returns true when the caller should stop emitting.
	truncated := func() bool {
		if len(lines) >= height {
			if height > 0 {
				lines[height-1] = mutedStyle.Render("  … more")
			}
			return true
		}
		return false
	}
	// The "now" divider marks the boundary between past and upcoming events. It
	// is only meaningful when that boundary actually falls within the rendered
	// range: the previous event must be in the past and the current one in the
	// future. If the first visible event is already upcoming (we scrolled past
	// "now") or every event is in the past, we don't draw a floating divider.
	prevPast := false
	for i := start; i < len(m.events); i++ {
		ev := &m.events[i]
		if !nowShown && prevPast && !eventSortInstant(ev).Before(now) {
			if truncated() {
				break
			}
			lines = append(lines, nowLine())
			nowShown = true
		}
		day := ev.StartDate.Format("Mon Jan 02")
		if day != current {
			current = day
			if truncated() {
				break
			}
			hdr := " " + day
			if sameDay(ev.StartDate, now) {
				hdr = " • " + day + "  (today)"
			}
			lines = append(lines, dayHeaderStyle.Render(hdr))
		}
		if truncated() {
			break
		}
		lines = append(lines, m.eventRow(ev, selected == ev, innerWidth))
		prevPast = eventSortInstant(ev).Before(now)
	}
	return strings.Join(lines, "\n")
}

// locationWithoutRooms returns the event location with any comma-separated parts
// that merely repeat a meeting room removed, so 📍 shows the real place/link and
// 🏛 shows the rooms without duplication. Falls back to the raw location.
func locationWithoutRooms(ev *Event) string {
	if ev.Location == "" {
		return ""
	}
	if len(ev.Rooms) == 0 {
		return ev.Location
	}
	roomSet := make(map[string]bool, len(ev.Rooms))
	for _, r := range ev.Rooms {
		roomSet[strings.TrimSpace(strings.ToLower(r))] = true
	}
	var kept []string
	for _, part := range strings.Split(ev.Location, ",") {
		p := strings.TrimSpace(part)
		if p == "" || roomSet[strings.ToLower(p)] {
			continue
		}
		kept = append(kept, p)
	}
	return strings.Join(kept, ", ")
}

// eventRow is a single compact line: [time] title  badges. The time is a filled
// pill so consecutive events are visually separated even without blank lines.
func (m model) eventRow(ev *Event, selected bool, width int) string {
	tl := fmt.Sprintf("%-11s", m.timeLabel(ev))
	badges := ""
	if n := len(ev.Attendees); n > 0 {
		badges += fmt.Sprintf(" 👥%d", n)
	}
	if ev.Location != "" || len(ev.Rooms) > 0 {
		badges += " 📍"
	}
	if ev.otherLinkCount() > 0 {
		badges += " ↗"
	}
	titleRoom := max(6, width-lipgloss.Width(tl)-lipgloss.Width(badges)-4)
	title := truncate(ev.Title, titleRoom)
	if selected {
		line := fmt.Sprintf("▸ %s %s%s", tl, title, badges)
		return selectedRowStyle.Render(line)
	}
	return "  " + timePillMini.Render(tl) + " " + title + mutedStyle.Render(badges)
}

func (m model) agendaStartIndex(height int) int {
	if len(m.events) == 0 {
		return 0
	}
	selected := max(0, min(m.selected, len(m.events)-1))
	// Each event = 1 row; count day headers too. Keep selected around the
	// upper-middle so there's context above and room below.
	budgetAbove := max(2, height/3)
	used := 0
	start := selected
	for start > 0 {
		prev := start - 1
		cost := 1
		if prev == 0 || !sameDay(m.events[prev-1].StartDate, m.events[prev].StartDate) {
			cost++ // day header
		}
		if used+cost > budgetAbove {
			break
		}
		used += cost
		start = prev
	}
	return start
}

// viewDayDetail lists every event on the focused day (grid detail pane).
func (m model) viewDayDetail(width, height int) string {
	iw := max(16, width-3)
	inner := max(3, height)
	evs := m.focusDayEvents()
	var lines []string
	lines = append(lines, sectionTitleStyle.Render(truncate("  "+m.anchor.Format("Mon Jan 02")+"  ", max(10, iw))))
	lines = append(lines, "")
	if len(evs) == 0 {
		lines = append(lines, mutedStyle.Render("no events"))
	}
	for i, ev := range evs {
		tm := "all-day"
		if !ev.AllDay() && !ev.StartAt.IsZero() {
			tm = m.timeLabel(ev)
		}
		badges := ""
		if n := len(ev.Attendees); n > 0 {
			badges += fmt.Sprintf(" 👥%d", n)
		}
		if ev.otherLinkCount() > 0 {
			badges += " ↗"
		}
		line := pillStyle.Render(tm) + " " + truncate(ev.Title, max(6, iw-lipgloss.Width(tm)-lipgloss.Width(badges)-2)) + mutedStyle.Render(badges)
		if m.focusPane == focusDetail && i == max(0, min(m.gridDetail, len(evs)-1)) {
			line = selectedRowStyle.Render(truncate("▸ "+tm+" "+ev.Title+badges, iw))
		}
		lines = append(lines, line)
		if loc := locationWithoutRooms(ev); loc != "" {
			lines = append(lines, mutedStyle.Render("     @ "+truncate(loc, iw-7)))
		}
		if len(ev.Rooms) > 0 {
			lines = append(lines, mutedStyle.Render("     room: "+truncate(strings.Join(ev.Rooms, ", "), iw-11)))
		}
	}
	lines = append(lines, "")
	lines = append(lines, mutedStyle.Render("tab detail focus | j/k event | A/L/E/X act | enter open calendar"))
	content := truncateLines(strings.Join(lines, "\n"), inner, iw)
	return detailStyle.Width(max(28, width)).Height(inner).Render(content)
}

func (m model) viewDetailCard(width, height int) string {
	// Grid views: show ALL of the focused day's events, not just one.
	if m.view != viewList {
		return m.viewDayDetail(width, height)
	}
	ev := m.selectedEvent()
	if ev == nil {
		return detailStyle.Width(max(28, width)).Height(max(3, height)).Render("No event selected")
	}
	var lines []string
	lines = append(lines, sectionTitleStyle.Render(truncate("  "+ev.Title+"  ", max(10, width-4))))
	lines = append(lines, "")
	lines = append(lines, pillStyle.Render(ev.StartDate.Format("Mon Jan 02"))+" "+pillStyle.Render(m.timeLabel(ev)))
	if loc := locationWithoutRooms(ev); loc != "" {
		lines = append(lines, linkStyle.Render("@ ")+wrap(loc, max(10, width-4)))
	}
	if len(ev.Rooms) > 0 {
		lines = append(lines, mutedStyle.Render("room: ")+wrap(strings.Join(ev.Rooms, ", "), max(10, width-7)))
	}
	if ev.Calendar != "" {
		lines = append(lines, mutedStyle.Render("Calendar: "+displayNameForCalendar(ev.Calendar)))
	}
	attendees := ev.Attendees
	if len(attendees) == 0 && ev.AttendeeEmail != "" {
		attendees = []string{ev.AttendeeEmail}
	}
	if len(attendees) > 0 {
		lines = append(lines, "")
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("Attendees (%d)", len(attendees))))
		for i, a := range attendees {
			if i >= 12 {
				lines = append(lines, mutedStyle.Render(fmt.Sprintf("… +%d more", len(attendees)-12)))
				break
			}
			lines = append(lines, "- "+truncate(prettyAttendee(a), max(10, width-5)))
		}
	}
	if ev.Description != "" {
		lines = append(lines, "")
		lines = append(lines, mutedStyle.Render("Description"))
		lines = append(lines, wrap(ev.Description, max(10, width-4)))
	}
	if len(ev.Links) > 0 {
		lines = append(lines, "")
		lines = append(lines, linkStyle.Render("Links"))
		for i, link := range ev.Links {
			if i >= 6 {
				lines = append(lines, mutedStyle.Render("… more links"))
				break
			}
			lines = append(lines, linkStyle.Render(fmt.Sprintf("%d. %s", i+1, truncate(link, max(10, width-7)))))
		}
	}
	content := strings.Join(lines, "\n")
	// No border: the card occupies exactly `height` rows. Left+right padding is
	// 3 cells, so the content width is width-3. Pre-truncate to that width so
	// lipgloss never re-wraps (which would grow the block past `height`).
	inner := max(3, height)
	return detailStyle.Width(max(28, width)).Height(inner).Render(truncateLines(content, inner, max(20, width-3)))
}

func (m model) searchMatches() []searchMatch {
	query := strings.ToLower(strings.TrimSpace(m.input))
	onlyMine := false
	for _, prefix := range []string{"mine ", "mine", "@me ", "@me", "owner:me ", "owner:me"} {
		if strings.HasPrefix(query, prefix) {
			onlyMine = true
			query = strings.TrimSpace(strings.TrimPrefix(query, prefix))
			break
		}
	}
	matches := make([]searchMatch, 0, len(m.events))
	for i, ev := range m.events {
		if onlyMine {
			me := myIdentity()
			mine := me != "" && (strings.Contains(strings.ToLower(ev.Calendar), me) ||
				strings.Contains(strings.ToLower(strings.Join(ev.Attendees, " ")), me) ||
				strings.Contains(strings.ToLower(ev.AttendeeEmail), me))
			if !mine {
				continue
			}
		}
		corpus := strings.ToLower(strings.Join([]string{
			ev.StartDate.Format("2006-01-02"),
			ev.StartDate.Format("Jan 02"),
			ev.StartDate.Format("Mon"),
			ev.TimeLabel(),
			ev.Title,
			ev.Location,
			strings.Join(ev.Rooms, " "),
			ev.Description,
			ev.Calendar,
			ev.AttendeeEmail,
			strings.Join(ev.Attendees, " "),
			strings.Join(ev.Links, " "),
		}, " "))
		score := fuzzyScore(query, corpus)
		if query == "" || score > 0 {
			label := fmt.Sprintf("%s %-13s %s", ev.StartDate.Format("01/02 Mon"), ev.TimeLabel(), ev.Title)
			if ev.Location != "" {
				label += " @ " + ev.Location
			}
			matches = append(matches, searchMatch{eventIndex: i, score: score, label: label})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if query == "" {
			return matches[i].eventIndex < matches[j].eventIndex
		}
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].eventIndex < matches[j].eventIndex
	})
	return matches
}

func fuzzyScore(query, text string) int {
	query = strings.TrimSpace(strings.ToLower(query))
	text = strings.ToLower(text)
	if query == "" {
		return 1
	}
	if strings.Contains(text, query) {
		return 1000 + len(query)*10
	}
	qi := 0
	score := 0
	streak := 0
	for _, r := range text {
		if qi >= len(query) {
			break
		}
		if byte(r) == query[qi] {
			score += 10 + streak*5
			streak++
			qi++
		} else {
			streak = 0
		}
	}
	if qi == len(query) {
		return score
	}
	return 0
}

// attendeeEmail strips response-status prefixes ([y]/[n]/[?]) from an attendee
// label to recover the raw email for calendar switching.
func attendeeEmail(label string) string {
	for _, p := range []string{"[y] ", "[n] ", "[?] "} {
		label = strings.TrimPrefix(label, p)
	}
	label = strings.TrimSpace(label)
	if !strings.Contains(label, "@") {
		return ""
	}
	return label
}

// calendarPickerItems builds the `e` picker list: recently-used calendars first,
// then subscribed calendars from calendarList (with ids), then static aliases.
func calendarPickerItems() []pickerItem {
	var items []pickerItem
	seen := map[string]bool{}
	add := func(label, value string) {
		if value == "" || seen[strings.ToLower(value)] {
			return
		}
		seen[strings.ToLower(value)] = true
		items = append(items, pickerItem{label: label, value: value})
	}

	for _, r := range loadRecentCalendars() {
		label := "★ " + r
		if name := displayNameForCalendar(r); name != "" && name != r {
			label = "★ " + name
		}
		add(label, r)
	}
	if cals, err := listCalendars(); err == nil {
		for _, c := range cals {
			label := c.Name
			if c.AccessRole != "" {
				label = c.Name + "  (" + c.AccessRole + ")"
			}
			add(label, c.ID)
		}
	}
	for _, key := range sortedAliasKeys() {
		add(key+": "+calendarAliases[key], calendarAliases[key])
	}
	return items
}

// calendarDisplayName renders the currently-viewed calendar in a human-friendly
// way: personal emails become "Jace Son" (title-cased local part), group ids
// resolve to their calendarList name, and aliases keep their display name.
func (m model) viewCreate() string {
	c := m.create
	w := min(max(52, m.width*3/5), max(40, m.width-4))
	inner := max(16, w-4)
	stepName := []string{"Title", "Date", "Start", "Duration", "Attendees", "Confirm"}[c.step]

	var lines []string
	formTitle := "New event"
	if c.editing {
		formTitle = "Edit event"
	}
	lines = append(lines, sectionTitleStyle.Render(fmt.Sprintf(" %s · %s ", formTitle, stepName)))
	lines = append(lines, mutedStyle.Render("on "+m.calendarDisplayName()))
	lines = append(lines, "")

	field := func(label, val string, active bool) string {
		if val == "" {
			val = "—"
		}
		line := fmt.Sprintf("%-10s %s", label, truncate(val, max(4, inner-lipgloss.Width(label)-3)))
		if active {
			if c.editingField {
				return selectedStyle.Width(inner).Render(line + "▏")
			}
			return selectedRowStyle.Render("▸ " + line)
		}
		return line
	}

	lines = append(lines, field("Title", c.title, c.step == stepTitle))
	lines = append(lines, field("Date", c.date, c.step == stepDate))
	lines = append(lines, field("Start", c.start, c.step == stepStart))
	lines = append(lines, field("Duration", c.durationStr+"m", c.step == stepDuration))

	// Attendees block — rendered through the same field() helper so its label
	// lines up with Title/Date/Start/Duration. field() appends the edit cursor.
	atts := sortedKeys(c.selected)
	attVal := fmt.Sprintf("(%d)", len(atts))
	if c.step == stepAttendees && c.editingField {
		attVal = fmt.Sprintf("(%d) · filter: %s", len(atts), c.attInput)
	}
	lines = append(lines, field("Attendees", attVal, c.step == stepAttendees))
	if c.step == stepAttendees {
		cands := filterPickerItems(c.attCands, c.attInput)
		rows := max(1, min(6, len(cands)))
		start := 0
		if c.attCandidx >= rows {
			start = c.attCandidx - rows + 1
		}
		for i := start; i < min(len(cands), start+rows); i++ {
			mark := "  "
			if c.selected[cands[i].value] {
				mark = "* "
			}
			line := mark + truncate(cands[i].label, max(4, inner-4))
			if i == c.attCandidx && c.editingField {
				lines = append(lines, selectedStyle.Width(inner-2).Render(line))
			} else {
				lines = append(lines, line)
			}
		}
	} else if len(atts) > 0 {
		for i, a := range atts {
			if i >= 4 {
				lines = append(lines, mutedStyle.Render(fmt.Sprintf("  … +%d more", len(atts)-4)))
				break
			}
			lines = append(lines, "  · "+truncate(a, inner-4))
		}
	}

	lines = append(lines, "")
	day, _ := time.ParseInLocation("2006-01-02", c.date, m.tz())
	lines = append(lines, pillStyle.Render(strings.TrimSpace(c.title)))
	lines = append(lines, mutedStyle.Render(fmt.Sprintf("%s  %s (%dm)  %s", day.Format("Mon Jan 02"), c.start, c.duration, m.tzLabel())))
	if len(atts) > 0 {
		lines = append(lines, errorStyle.Render(fmt.Sprintf("⚠ invitation emails will be sent to %d people", len(atts))))
	}
	if c.submitting {
		lines = append(lines, statusStyle.Render("creating…"))
	} else {
		if c.editingField {
			lines = append(lines, mutedStyle.Render("ESC blur field · Enter save · Tab/⇧Tab next field · Space toggle attendee"))
		} else {
			lines = append(lines, mutedStyle.Render("j/k move field · Enter/i edit field · Enter on any field saves · ESC close"))
		}
	}
	if c.err != "" {
		lines = append(lines, errorStyle.Render("x "+c.err))
	}

	h := min(len(lines)+2, max(8, m.height-4))
	return modalStyle.Width(w).Height(h).Render(strings.Join(lines, "\n"))
}

func (m model) viewConfirmDelete() string {
	ev := m.selectedEvent()
	w := min(max(44, m.width/2), max(36, m.width-4))
	var lines []string
	lines = append(lines, errorStyle.Render(" Delete event? "))
	lines = append(lines, "")
	if ev != nil {
		lines = append(lines, pillStyle.Render(m.timeLabel(ev))+" "+truncate(ev.Title, max(10, w-16)))
		lines = append(lines, mutedStyle.Render(ev.StartDate.Format("Mon Jan 02")+" · "+m.calendarDisplayName()))
		if n := len(ev.Attendees); n > 0 {
			lines = append(lines, errorStyle.Render(fmt.Sprintf("⚠ cancellation notice will be sent to %d people", n)))
		}
	}
	lines = append(lines, "")
	lines = append(lines, mutedStyle.Render("y/Enter delete · n/ESC cancel"))
	h := min(len(lines)+2, max(6, m.height-4))
	return modalStyle.Width(w).Height(h).Render(strings.Join(lines, "\n"))
}

func sortedKeys(mp map[string]bool) []string {
	out := make([]string, 0, len(mp))
	for k := range mp {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (m model) calendarDisplayName() string {
	return displayNameForCalendar(m.calendar)
}

// identityCache memoizes the resolved user email so we hit calendarList once.
var identityCache = struct {
	resolved bool
	email    string
}{}

// myIdentity returns the user's own email (lowercased) for "mine" filters and
// self display. It prefers the explicit [settings] email, then falls back to
// the primary calendar's id (which is the account email for personal Google
// accounts). Empty when it cannot be determined.
func myIdentity() string {
	if settings.identity != "" {
		return settings.identity
	}
	if identityCache.resolved {
		return identityCache.email
	}
	email := strings.ToLower(strings.TrimSpace(primaryCalendarEmail()))
	identityCache.email = email
	identityCache.resolved = true
	return email
}

// displayNameForCalendar renders any calendar id/email in a human-friendly way:
// group/resource ids resolve to their calendarList name, personal emails become
// a title-cased name, and anything unresolved falls back to the raw value.
func displayNameForCalendar(cal string) string {
	cal = strings.TrimSpace(cal)
	if cal == "" {
		return cal
	}
	// group/resource ids → look up the real name from calendarList cache
	if strings.Contains(cal, "group.calendar.google.com") || strings.Contains(cal, "resource.calendar.google.com") {
		if cals, err := listCalendars(); err == nil {
			for _, c := range cals {
				if c.ID == cal && c.Name != "" {
					return c.Name
				}
			}
		}
		return shortCalendarID(cal)
	}
	if strings.Contains(cal, "@") {
		local := strings.SplitN(cal, "@", 2)[0]
		if me := myIdentity(); me != "" && strings.EqualFold(cal, me) {
			return "Me (" + strings.SplitN(me, "@", 2)[0] + ")"
		}
		// A bare group/resource local part with no readable name: shorten it.
		if strings.HasPrefix(local, "c_") && len(local) > 20 {
			return shortCalendarID(cal)
		}
		// jace.son → Jace Son
		parts := strings.FieldsFunc(local, func(r rune) bool { return r == '.' || r == '_' || r == '-' })
		for i, p := range parts {
			if p != "" {
				parts[i] = strings.ToUpper(p[:1]) + p[1:]
			}
		}
		return strings.Join(parts, " ")
	}
	return cal
}

// shortCalendarID collapses an opaque group id into a compact, still-copyable
// hint like "group:c_abcd1234…" so the UI never shows a full 60-char id.
func shortCalendarID(cal string) string {
	local := cal
	if i := strings.IndexByte(cal, '@'); i > 0 {
		local = cal[:i]
	}
	if len(local) > 12 {
		local = local[:12] + "…"
	}
	return "group:" + local
}

// prettyAttendee renders an attendee entry (which may carry a leading response
// marker like "[y] ") with group/resource calendar ids resolved to readable names.
func prettyAttendee(a string) string {
	for _, mark := range []string{"[y] ", "[n] ", "[?] "} {
		if strings.HasPrefix(a, mark) {
			return mark + displayNameForCalendar(strings.TrimPrefix(a, mark))
		}
	}
	return displayNameForCalendar(a)
}

func (m model) calendarLabel() string {
	if m.calendarKey == m.calendar {
		return m.calendar
	}
	return m.calendarKey + " → " + m.calendar
}

// popupSize returns a right-bottom modal's inner dimensions.
// popupSize returns a right-bottom modal's inner dimensions. wantRows is the
// number of list rows the caller would like to show; the modal grows to fit
// them (title + input + hint = 4 chrome rows) up to the available height.
// preferredOverlayWidth keeps picker/create/delete overlays aligned with the
// detail split. When the detail is on the right, overlays use that same width.
func (m model) preferredOverlayWidth() int {
	bodyWidth := max(20, m.width-2)
	switch m.splitMode() {
	case splitRight:
		return max(34, bodyWidth/3)
	case splitBottom:
		return bodyWidth
	default:
		return min(max(48, bodyWidth/2), max(36, bodyWidth-4))
	}
}

func (m model) popupSize(wantRows int) (int, int) {
	// Width follows the detail split so overlays feel attached to it.
	w := m.preferredOverlayWidth()
	// Height must stay within the content area (m.height-4) so the modal never
	// pushes the status/hint bars off-screen on resize.
	contentH := max(3, m.height-4)
	desired := wantRows + 4 // title + input + hint + borders
	h := min(desired, contentH)
	if h < 5 {
		h = min(5, contentH)
	}
	return w, h
}

func (m model) viewPopup() string {
	if m.mode == modeHelp {
		w, h := m.popupSize(len(helpLines()) + 1)
		inner := max(10, w-4)
		var lines []string
		lines = append(lines, sectionTitleStyle.Render(" Help "))
		lines = append(lines, "")
		for _, l := range helpLines() {
			lines = append(lines, truncate(l, inner))
		}
		lines = append(lines, "")
		lines = append(lines, mutedStyle.Render("ESC / ? close"))
		return modalStyle.Width(w).Height(h).Render(strings.Join(lines, "\n"))
	}

	if m.mode == modeSearch {
		matches := m.searchMatches()
		w, h := m.popupSize(len(matches))
		inner := max(10, w-4)
		rows := max(1, h-4)
		var lines []string
		lines = append(lines, sectionTitleStyle.Render(" Search "))
		lines = append(lines, selectedStyle.Width(inner).Render("/ "+m.input+"▏"))
		if len(matches) == 0 {
			lines = append(lines, mutedStyle.Render("No matches"))
		} else {
			start := 0
			if m.searchIndex >= rows {
				start = m.searchIndex - rows + 1
			}
			for i := start; i < min(len(matches), start+rows); i++ {
				line := truncate(matches[i].label, inner-2)
				if i == m.searchIndex {
					lines = append(lines, selectedStyle.Width(inner-2).Render(line))
				} else {
					lines = append(lines, "  "+line)
				}
			}
		}
		lines = append(lines, mutedStyle.Render("↑/↓ move · Enter jump · ESC cancel"))
		return modalStyle.Width(w).Height(h).Render(strings.Join(lines, "\n"))
	}

	// Fuzzy pickers (links / calendars / attendees).
	items := m.picker.filtered(m.input)
	w, h := m.popupSize(len(items))
	inner := max(10, w-4)
	rows := max(1, h-4)
	var lines []string
	lines = append(lines, sectionTitleStyle.Render(" "+m.picker.title+" "))
	lines = append(lines, selectedStyle.Width(inner).Render("› "+m.input+"▏"))
	if len(items) == 0 {
		if m.mode == modeCalendarPicker && strings.Contains(m.input, "@") {
			lines = append(lines, linkStyle.Render("↵ open this calendar directly: "+truncate(strings.TrimSpace(m.input), inner-4)))
		} else {
			lines = append(lines, mutedStyle.Render("No matches"))
		}
	} else {
		idx := max(0, min(m.picker.index, len(items)-1))
		start := 0
		if idx >= rows {
			start = idx - rows + 1
		}
		for i := start; i < min(len(items), start+rows); i++ {
			line := truncate(items[i].label, inner-2)
			if i == idx {
				lines = append(lines, selectedStyle.Width(inner-2).Render(line))
			} else {
				lines = append(lines, "  "+line)
			}
		}
	}
	lines = append(lines, mutedStyle.Render("type filter · ↑/↓ move · Enter select · ESC cancel"))
	return modalStyle.Width(w).Height(h).Render(strings.Join(lines, "\n"))
}

func (m model) selectedEvent() *Event {
	if len(m.events) == 0 {
		return nil
	}
	// Grid views have no per-event cursor; the "current" event is the first one
	// on the focused day (so A/L/E/x/detail act on that day).
	if m.view != viewList {
		for i := range m.events {
			if sameDay(m.events[i].StartDate, m.anchor) {
				return &m.events[i]
			}
		}
		return nil
	}
	idx := max(0, min(m.selected, len(m.events)-1))
	return &m.events[idx]
}

// focusDayEvents returns all events on the focused day (grid detail pane).
func (m model) focusDayEvents() []*Event {
	var out []*Event
	for i := range m.events {
		if sameDay(m.events[i].StartDate, m.anchor) {
			out = append(out, &m.events[i])
		}
	}
	return out
}

// currentActionEvent is the event that A/L/E/x/Enter operate on.
// List view → the selected agenda event.
// Grid view → the selected event inside the detail pane when it has focus,
// otherwise the first event of the focused day.
func (m model) currentActionEvent() *Event {
	if m.view == viewList {
		return m.selectedEvent()
	}
	evs := m.focusDayEvents()
	if len(evs) == 0 {
		return nil
	}
	idx := 0
	if m.focusPane == focusDetail {
		idx = max(0, min(m.gridDetail, len(evs)-1))
	}
	return evs[idx]
}

// clampGridDetail keeps the detail selection inside the focused day's range.
func (m *model) clampGridDetail() {
	n := len(m.focusDayEvents())
	if n == 0 {
		m.gridDetail = 0
		return
	}
	m.gridDetail = max(0, min(m.gridDetail, n-1))
}

// openPrimary always opens the Google Calendar event page. Other links (zoom,
// docs, …) are only reachable via the L links picker.
func (m model) openPrimary() tea.Cmd {
	ev := m.currentActionEvent()
	if ev == nil {
		m.status = "no event selected"
		return nil
	}
	if ev.HTMLLink != "" {
		return openCmd(ev.HTMLLink)
	}
	// Fall back to any Google Calendar link found among the parsed links.
	for _, l := range ev.Links {
		if strings.Contains(l, "google.com/calendar/event") {
			return openCmd(l)
		}
	}
	m.status = "no Google Calendar link for this event (use L for other links)"
	return nil
}

func (m model) loadCmd(reqID int) tea.Cmd {
	calendar := m.calendar
	start, end := m.loadRange()
	return func() tea.Msg {
		events, err := fetchEvents(calendar, start, end)
		return eventsMsg{events: events, err: err, start: start, end: end, reqID: reqID}
	}
}

func openCmd(link string) tea.Cmd {
	return func() tea.Msg {
		opener := "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}
		if err := exec.Command(opener, link).Start(); err != nil {
			return statusMsg("open failed: " + err.Error())
		}
		return statusMsg("opened: " + link)
	}
}

func stripANSI(s string) string {
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansi.ReplaceAllString(s, "")
}

// fetchEvents queries the Google Calendar REST API directly (reusing gcalcli's
// stored OAuth). This reaches readable-but-unsubscribed calendars that gcalcli
// silently skips. If the API path fails to initialize (e.g. no oauth file), it
// falls back to the gcalcli TSV path.
func fetchEvents(calendar string, start, end time.Time) ([]Event, error) {
	events, err := fetchEventsAPI(calendar, start, end)
	if err == nil {
		sortEvents(events)
		return events, nil
	}
	// Only fall back when the failure is about credentials/setup, not a real
	// access error we want to surface (404/403).
	msg := err.Error()
	if strings.Contains(msg, "not found") || strings.Contains(msg, "no permission") || strings.Contains(msg, "no access") {
		return nil, err
	}
	return fetchEventsGcalcli(calendar, start, end)
}

func sortEvents(events []Event) {
	sort.Slice(events, func(i, j int) bool {
		if !events[i].StartDate.Equal(events[j].StartDate) {
			return events[i].StartDate.Before(events[j].StartDate)
		}
		if events[i].StartTime != events[j].StartTime {
			return events[i].StartTime < events[j].StartTime
		}
		return events[i].Title < events[j].Title
	})
}

func fetchEventsGcalcli(calendar string, start, end time.Time) ([]Event, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := []string{
		"--calendar", calendar,
		"agenda",
		"--tsv",
		"--nodeclined",
		"--details", "all",
		start.Format("2006-01-02"),
		end.Format("2006-01-02"),
	}
	cmd := exec.CommandContext(ctx, "gcalcli", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, errors.New(strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	reader := csv.NewReader(bytes.NewReader(stdout.Bytes()))
	reader.Comma = '\t'
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	headers := rows[0]
	idx := map[string]int{}
	for i, h := range headers {
		idx[h] = i
	}
	var events []Event
	for _, row := range rows[1:] {
		get := func(name string) string {
			if i, ok := idx[name]; ok && i < len(row) {
				return row[i]
			}
			return ""
		}
		startDate, err := parseDate(get("start_date"))
		if err != nil {
			continue
		}
		endDate, _ := parseDate(get("end_date"))
		ev := Event{
			ID:            get("id"),
			StartDate:     startDate,
			EndDate:       endDate,
			StartTime:     get("start_time"),
			EndTime:       get("end_time"),
			Title:         cleanText(get("title")),
			Location:      cleanText(get("location")),
			Description:   cleanText(get("description")),
			Calendar:      get("calendar"),
			AttendeeEmail: cleanText(get("email")),
			HTMLLink:      get("html_link"),
			ConferenceURI: get("conference_uri"),
		}
		if ev.Title == "" {
			ev.Title = "(no title)"
		}
		ev.Links = uniqueLinks([]string{
			ev.ConferenceURI,
			get("hangout_link"),
			ev.HTMLLink,
			ev.Location,
			ev.Description,
			ev.Title,
		})
		events = append(events, ev)
	}
	attendees := fetchAttendees(calendar, start, end)
	if len(attendees) > 0 {
		for i := range events {
			key := attendeeKey(events[i].StartTime, events[i].Title)
			if list, ok := attendees[key]; ok {
				events[i].Attendees = list
			}
		}
	}
	sort.Slice(events, func(i, j int) bool {
		if !events[i].StartDate.Equal(events[j].StartDate) {
			return events[i].StartDate.Before(events[j].StartDate)
		}
		if events[i].StartTime != events[j].StartTime {
			return events[i].StartTime < events[j].StartTime
		}
		return events[i].Title < events[j].Title
	})
	return events, nil
}

func attendeeKey(startTime, title string) string {
	return normalizeTime(startTime) + "\x00" + strings.TrimSpace(strings.ToLower(title))
}

// normalizeTime turns "9:00" / "09:00" into a canonical "09:00" so keys from
// the TSV (zero-padded) and the plain agenda (not padded) match.
func normalizeTime(t string) string {
	t = strings.TrimSpace(t)
	parts := strings.SplitN(t, ":", 2)
	if len(parts) != 2 {
		return t
	}
	if len(parts[0]) == 1 {
		parts[0] = "0" + parts[0]
	}
	return parts[0] + ":" + parts[1]
}

// fetchAttendees parses `gcalcli agenda --details attendees` plain output, which
// (unlike --tsv) includes the full attendee list per event. Keyed by start
// time + title so it can be joined back onto the TSV events. Best-effort: on
// any error it returns an empty map and the caller falls back to the single
// email column.
func fetchAttendees(calendar string, start, end time.Time) map[string][]string {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gcalcli",
		"--calendar", calendar,
		"agenda", "--details", "attendees", "--military",
		start.Format("2006-01-02"), end.Format("2006-01-02"),
	)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	result := map[string][]string{}
	emailRe := regexp.MustCompile(`<([^>]+@[^>]+)>`)
	timeRe := regexp.MustCompile(`\b(\d{1,2}:\d{2})\b`)

	var curKey string
	inAttendees := false
	for _, raw := range strings.Split(string(out), "\n") {
		line := stripANSI(raw)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "Attendees:") {
			inAttendees = true
			continue
		}
		if inAttendees && emailRe.MatchString(trimmed) {
			email := emailRe.FindStringSubmatch(trimmed)[1]
			if strings.Contains(email, "resource.calendar.google.com") {
				continue // meeting rooms
			}
			if curKey != "" {
				result[curKey] = appendUnique(result[curKey], email)
			}
			continue
		}
		// A non-attendee content line starts a new event row. Extract its time
		// (if any) and title to build the join key.
		inAttendees = false
		title := trimmed
		startTime := ""
		if loc := timeRe.FindStringIndex(trimmed); loc != nil {
			startTime = timeRe.FindString(trimmed)
			title = strings.TrimSpace(trimmed[loc[1]:])
		} else {
			// All-day / date-header lines: title is everything after the date.
			fields := strings.Fields(trimmed)
			if len(fields) >= 4 && len(fields[0]) == 3 {
				title = strings.TrimSpace(strings.Join(fields[3:], " "))
			}
		}
		title = strings.TrimSpace(tagRe.ReplaceAllString(html.UnescapeString(title), " "))
		if title == "" {
			curKey = ""
			continue
		}
		curKey = attendeeKey(startTime, title)
	}
	return result
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func parseDate(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, errors.New("empty date")
	}
	return time.ParseInLocation("2006-01-02", value, time.Local)
}

func cleanText(value string) string {
	value = html.UnescapeString(value)
	value = tagRe.ReplaceAllString(value, " ")
	value = strings.Join(strings.Fields(value), " ")
	return value
}

func uniqueLinks(parts []string) []string {
	seen := map[string]bool{}
	var links []string
	for _, part := range parts {
		part = html.UnescapeString(part)
		for _, match := range urlRe.FindAllString(part, -1) {
			match = strings.TrimRight(match, ".,;]")
			if _, err := url.ParseRequestURI(match); err == nil && !seen[match] {
				seen[match] = true
				links = append(links, match)
			}
		}
	}
	return links
}

func resolveCalendar(value string) string {
	if cal, ok := calendarAliases[value]; ok {
		return cal
	}
	return value
}

// loadRange returns the [start, end) date window to fetch for the current view.
func (m model) loadRange() (time.Time, time.Time) {
	switch m.view {
	case viewWeek, viewMonth:
		// Anchor the loaded window to the visible grid top with generous padding
		// so scrolling within the grid rarely triggers a reload.
		top := m.gridTop
		if top.IsZero() {
			top = weekStart(m.anchor)
			if m.view == viewMonth {
				top = weekStart(monthStart(m.anchor))
			}
		}
		s := top.AddDate(0, 0, -7*4)
		return s, s.AddDate(0, 0, 7*20) // ~20 weeks window
	default: // viewList
		return m.anchor.AddDate(0, 0, -2), m.anchor.AddDate(0, 0, 45)
	}
}

// anchorLoaded reports whether the focus date is already covered by the loaded
// event window. Prefer this over recomputing from the current anchor after the
// anchor changes, which can lead to false negatives/positives when switching views.
func (m model) anchorLoaded() bool {
	return !m.loadedStart.IsZero() && !m.anchor.Before(m.loadedStart) && m.anchor.Before(m.loadedEnd)
}

// weekStart returns the Sunday on/before t.
func weekStart(t time.Time) time.Time {
	t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	return t.AddDate(0, 0, -int(t.Weekday()))
}

func monthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.Local)
}

func today() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
}

func sameDay(a, b time.Time) bool {
	y1, m1, d1 := a.Date()
	y2, m2, d2 := b.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}

func sortedAliasKeys() []string {
	keys := make([]string, 0, len(calendarAliases))
	for key := range calendarAliases {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func helpLines() []string {
	tzLabels := make([]string, 0, len(settings.timezones))
	for _, t := range settings.timezones {
		tzLabels = append(tzLabels, t.label)
	}
	tzHint := strings.Join(tzLabels, "/")
	if tzHint == "" {
		tzHint = "local"
	}
	return []string{
		"Views   list (agenda) · g week-grid · M month-grid · g back to list",
		"List    h/l move by step · d/w/m set step (day/week/month) · j/k select",
		"Grid    h/l ±day · j/k ±week · focus cursor only; calendar stays put",
		"        detail pane splits right (wide) or bottom (tall); A/L/E/X act on focus day",
		"Common  n jump to now (today, else nearest upcoming) · r refresh · Z timezone (" + tzHint + ")",
		"Open    Enter opens the Google Calendar event page",
		"L       other links picker (zoom, docs, …) (fuzzy)",
		"A       attendees picker → open that person's calendar",
		"e       calendar picker (recent ★, subscribed, aliases; fuzzy)",
		"N       new event · E edit selected · X delete (confirm)",
		"/       fuzzy search across loaded events, Enter jumps",
		"q       quit  ·  ESC backs out of any overlay",
		"",
		"Aliases defined in " + configPath() + " ([aliases] section)",
	}
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

func wrap(s string, width int) string {
	return strings.Join(textwrap(s, width), "\n")
}

func textwrap(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		for lipgloss.Width(para) > width {
			cut := min(len(para), width)
			lines = append(lines, para[:cut])
			para = para[cut:]
		}
		lines = append(lines, para)
	}
	return lines
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func truncateLines(s string, maxLines int, width int) string {
	if maxLines <= 0 {
		return ""
	}
	var out []string
	for _, raw := range strings.Split(s, "\n") {
		wrapped := textwrap(raw, width)
		for _, line := range wrapped {
			if len(out) >= maxLines {
				if len(out) > 0 {
					out[len(out)-1] = truncate(out[len(out)-1], max(1, width-1)) + "…"
				}
				return strings.Join(out, "\n")
			}
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
