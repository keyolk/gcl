package main

import (
	"slices"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) choosePicker(it pickerItem) (tea.Model, tea.Cmd) {
	switch m.picker.kind {
	case pickerLinks:
		m.mode = modeNormal
		m.input = ""
		return m, openCmd(it.value)
	case pickerCalendar, pickerAttendee:
		if it.value == "" {
			m.say("no calendar id for selection")
			m.mode = modeNormal
			m.input = ""
			return m, nil
		}
		m.mode = modeNormal
		m.input = ""
		m.err = nil
		m.calendarKey = it.value
		m.calendar = it.value
		m.dropLoadedEvents()
		m.loading = true
		// Switching calendars while an overlay is on would otherwise change
		// only which calendar new events land on, leaving the agenda showing
		// the same merged set — the keypress would look like it did nothing.
		// Picking a single calendar means you want that calendar.
		if m.overlay.on() {
			m.overlay.active = nil
			m.overlay.failed = nil
			// Its own source, so the load result that follows does not replace it.
			m.say("overlay off · " + displayNameForCalendar(it.value))
		}
		// The pick is remembered in memory now and written by a Cmd; a write
		// failure arrives as recentsSavedMsg.
		m.recents = withRecent(m.recents, it.value)
		return m, tea.Batch(m.reload(), saveRecentsCmd(m.recents))
	}
	m.mode = modeNormal
	m.input = ""
	return m, nil
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
// It does no I/O itself: Update opens the picker with cals nil and name a
// pass-through, and calendarItemsCmd fills in the subscribed calendars and
// readable names off the UI loop.
func calendarPickerItems(recents []string, cals []CalendarEntry, name func(string) string) []pickerItem {
	var items []pickerItem
	seen := map[string]bool{}
	add := func(label, value string) {
		if value == "" || seen[strings.ToLower(value)] {
			return
		}
		seen[strings.ToLower(value)] = true
		items = append(items, pickerItem{label: label, value: value})
	}

	for _, r := range recents {
		label := "* " + r
		if n := name(r); n != "" && n != r {
			label = "* " + n
		}
		add(label, r)
	}
	for _, c := range cals {
		label := c.Name
		if c.AccessRole != "" {
			label = c.Name + "  (" + c.AccessRole + ")"
		}
		add(label, c.ID)
	}
	for _, key := range sortedAliasKeys() {
		add(key+": "+calendarAliases[key], calendarAliases[key])
	}
	return items
}

// calendarItemsMsg carries the full `e` picker list, with the subscribed
// calendars, from calendarItemsCmd.
type calendarItemsMsg struct {
	items []pickerItem
	err   error
}

// calendarItemsCmd reads the calendar list (an HTTP call the first time,
// cached after) and builds the full picker list. It used to run inside Update
// on `e`, so the first press froze the UI for up to the 30s request timeout.
func calendarItemsCmd(recents []string) tea.Cmd {
	recents = slices.Clone(recents)
	return func() tea.Msg {
		cals, err := listCalendars()
		return calendarItemsMsg{items: calendarPickerItems(recents, cals, displayNameForCalendar), err: err}
	}
}

// withCalendarItems swaps in the full picker list if the calendar picker is
// still open, keeping the highlighted entry on the same calendar.
func (m model) withCalendarItems(msg calendarItemsMsg) model {
	if msg.err != nil {
		m.warn("subscribed calendars not listed: " + msg.err.Error())
	}
	if m.mode != modeCalendarPicker || m.picker.kind != pickerCalendar {
		return m
	}
	var cur string
	if items := m.picker.filtered(m.input); len(items) > 0 {
		cur = items[max(0, min(m.picker.index, len(items)-1))].value
	}
	m.picker.items = msg.items
	for i, it := range m.picker.filtered(m.input) {
		if it.value == cur {
			m.picker.index = i
			break
		}
	}
	return m
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
	// group/resource ids -> look up the real name from calendarList cache
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
		// jace.son -> Jace Son
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
// hint like "group:c_abcd1234~" so the UI never shows a full 60-char id.
func shortCalendarID(cal string) string {
	local := cal
	if i := strings.IndexByte(cal, '@'); i > 0 {
		local = cal[:i]
	}
	if len(local) > 12 {
		local = local[:12] + cutMarker
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
	return m.calendarKey + " -> " + m.calendar
}

func sortedAliasKeys() []string {
	keys := make([]string, 0, len(calendarAliases))
	for key := range calendarAliases {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
