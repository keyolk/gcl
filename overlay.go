package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Overlaying several people's calendars in one agenda.
//
// "When is the team actually free?" and "who is in this meeting slot?" are
// questions about several calendars at once, and reading them one at a time
// answers neither. `O` loads a set of calendars into the SAME event list, so
// every view already built keeps working on it: the list agenda, the week and
// month grids, the 24h overlap timeline, `/` search, and the `a` active panel
// all operate on m.events and gain multi-person data for free.
//
// The only thing an overlay has to add is *whose* event each row is. That is a
// color dot plus a short name in the row, and a legend under the agenda — not a
// separate column-per-person view, which would cost `people × column width` and
// stop fitting after three or four of them. The timeline bar is tinted with the
// same per-person color, so the existing vertical alignment answers "who
// overlaps whom" without any new layout.
//
// Overlay is a LOADING mode, not a view mode: m.calendar still names the
// calendar that new events are created on, while edits and deletes act on the
// calendar of the selected row (see eventCalendar).

// overlayState holds the picker and the active calendar set.
type overlayState struct {
	// active is the calendar set currently loaded, in display order. Empty when
	// no overlay is on — that is what distinguishes overlay mode from normal
	// single-calendar viewing.
	active []string
	// failed records calendars that could not be read, with the reason. An
	// overlay that silently drops a calendar looks like that person has nothing
	// scheduled, which is the most dangerous way for this feature to fail.
	failed map[string]string

	// Picker state (only meaningful while m.mode == modeOverlayPicker).
	input    string
	cands    []pickerItem
	candIdx  int
	selected map[string]bool
}

// on reports whether an overlay is currently loaded.
func (o overlayState) on() bool { return len(o.active) > 0 }

// overlayColors are the per-calendar dot/bar colors, in assignment order.
// Chosen to stay distinguishable on both dark and light terminals and to avoid
// the colors the timeline already gives meaning to (81 selected, 214 pending,
// 68 active-now).
var overlayColors = []string{"212", "114", "180", "117", "175", "150", "223", "146"}

// overlayColorFor returns the color assigned to a calendar in the current
// overlay, or "" when it is not part of one. Assignment is by position in the
// active list, so a calendar keeps its color for as long as the overlay lasts.
func (m model) overlayColorFor(cal string) string {
	for i, c := range m.overlay.active {
		if strings.EqualFold(c, cal) {
			return overlayColors[i%len(overlayColors)]
		}
	}
	return ""
}

// overlayShortName is the name shown in a row: the email local part, or the
// calendar's display name for group calendars.
func overlayShortName(cal string) string {
	if i := strings.IndexByte(cal, '@'); i > 0 {
		local := cal[:i]
		// Group/resource ids have opaque local parts; their display name is the
		// only readable thing about them.
		if strings.HasPrefix(local, "c_") && len(local) > 20 {
			return truncate(displayNameForCalendar(cal), 12)
		}
		return local
	}
	return truncate(displayNameForCalendar(cal), 12)
}

// overlayTag is the "● name " prefix a row carries while an overlay is on.
// Returns "" outside overlay mode, so eventRow can append it unconditionally.
//
// The trailing space is part of the tag: the name is padded to a fixed width so
// titles line up in a column, and without the separator a name that fills that
// width runs straight into the title ("jace.son1:1 with lead").
func (m model) overlayTag(ev *Event, width int) string {
	if !m.overlay.on() {
		return ""
	}
	color := m.overlayColorFor(ev.Calendar)
	if color == "" {
		return ""
	}
	st := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	if width <= 0 {
		// Narrow pane: the dot alone, with the legend carrying the mapping.
		return st.Render("●") + " "
	}
	name := truncate(overlayShortName(ev.Calendar), max(3, width))
	return st.Render("●") + " " + st.Render(padRightW(name, width)) + " "
}

// overlayNameWidth is how many columns the per-row name gets. It is computed
// from the actual names so two-person overlays don't pay for an eight-person
// column, and capped so a long address cannot eat the title.
//
// On a pane too narrow to carry both the name and the timeline, the NAME gives
// way first — down to the dot alone. Overlap is the reason to overlay
// calendars at all, and the dot plus the legend still identify the owner; a
// full name with no bar answers the lesser question.
func (m model) overlayNameWidth() int {
	if !m.overlay.on() {
		return 0
	}
	w := 0
	for _, c := range m.overlay.active {
		w = max(w, lipgloss.Width(overlayShortName(c)))
	}
	w = min(w, 10)
	if m.timelineHidden {
		return w
	}
	// Shrink the name until the timeline fits, or give up on it entirely.
	inner := max(20, m.schedulePaneWidth()-1)
	for ; w > 0; w-- {
		if timelineColsWithReserve(inner, w+3) > 0 {
			return w
		}
	}
	// Even a bare dot has to be affordable; if it is not, the row is so narrow
	// that the timeline is gone anyway and the name is what is left.
	if timelineColsWithReserve(inner, 2) > 0 {
		return 0
	}
	w = 0
	for _, c := range m.overlay.active {
		w = max(w, lipgloss.Width(overlayShortName(c)))
	}
	return min(w, 10)
}

// overlayLegend renders the color key under the agenda. Without it the dots are
// decoration: nobody can map color to person from the rows alone.
func (m model) overlayLegend(width int) string {
	if !m.overlay.on() {
		return ""
	}
	var parts []string
	for i, cal := range m.overlay.active {
		color := overlayColors[i%len(overlayColors)]
		st := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
		label := st.Render("●") + " " + overlayShortName(cal)
		if reason, bad := m.overlay.failed[strings.ToLower(cal)]; bad {
			// A calendar that failed to load must never look like an empty one.
			label = mutedStyle.Render("○ "+overlayShortName(cal)) + errorStyle.Render(" ("+reason+")")
		} else if n := m.overlayEventCount(cal); n >= 0 {
			label += mutedStyle.Render(fmt.Sprintf(" %d", n))
		}
		parts = append(parts, label)
	}
	return ansiTruncate("  "+strings.Join(parts, "  "), max(4, width))
}

// overlayEventCount is how many loaded events belong to a calendar, so the
// legend can say "3" next to a person and make an empty calendar legible as
// genuinely empty rather than as a silent load failure.
func (m model) overlayEventCount(cal string) int {
	n := 0
	for i := range m.events {
		if strings.EqualFold(m.events[i].Calendar, cal) {
			n++
		}
	}
	return n
}

// eventCalendar is the calendar an action should target for ev: the event's own
// calendar while an overlay is on, and the single open calendar otherwise.
//
// This is the whole reason overlay edits are safe. Patching a colleague's event
// against m.calendar would either 404 or, worse, hit a same-titled event on the
// wrong calendar.
func (m model) eventCalendar(ev *Event) string {
	if ev != nil && ev.Calendar != "" && m.overlay.on() {
		return ev.Calendar
	}
	return m.calendar
}

// overlayLoadedMsg carries a completed multi-calendar fetch.
type overlayLoadedMsg struct {
	events []Event
	failed map[string]string
	start  time.Time
	end    time.Time
	reqID  int
}

// loadOverlayCmd fetches every active calendar and merges the results into one
// sorted event list.
//
// Calendars are fetched sequentially rather than in parallel: the token cache
// is shared and a burst of eight refreshes would race on it, and the API is
// fast enough that a handful of calendars is not worth the concurrency bug
// surface. A per-calendar failure is recorded, never fatal — one unreadable
// calendar must not blank the whole agenda.
func (m model) loadOverlayCmd(reqID int) tea.Cmd {
	cals := append([]string(nil), m.overlay.active...)
	start, end := m.loadRange()
	return func() tea.Msg {
		var all []Event
		failed := map[string]string{}
		for _, cal := range cals {
			evs, err := fetchEvents(cal, start, end)
			if err != nil {
				failed[strings.ToLower(cal)] = shortLoadError(err)
				continue
			}
			for i := range evs {
				// fetchEvents stamps the calendar it was asked for, but the
				// gcalcli fallback path does not always; make sure every event
				// can be traced back to its owner, since that is what the row
				// tag and every edit target depend on.
				if evs[i].Calendar == "" {
					evs[i].Calendar = cal
				}
			}
			all = append(all, evs...)
		}
		sortEvents(all)
		return overlayLoadedMsg{events: all, failed: failed, start: start, end: end, reqID: reqID}
	}
}

// shortLoadError compresses an API error into something that fits a legend.
func shortLoadError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no permission"), strings.Contains(msg, "no access"):
		return "no access"
	case strings.Contains(msg, "not found"):
		return "not found"
	}
	if len(msg) > 24 {
		return msg[:24] + "~"
	}
	return msg
}

// newOverlayState seeds the picker. The currently-open calendar is
// pre-selected: an overlay is almost always "my calendar plus these others",
// and starting without yourself produces a picture you are not in.
func (m model) newOverlayState() overlayState {
	o := overlayState{
		cands:    m.participantCandidatePool(),
		selected: map[string]bool{},
		failed:   map[string]string{},
	}
	// Re-opening the picker while an overlay is on starts from that set, so
	// adding a fourth person doesn't mean re-picking the first three.
	if m.overlay.on() {
		for _, c := range m.overlay.active {
			o.selected[c] = true
		}
	} else if m.calendar != "" {
		o.selected[m.calendar] = true
	}
	return o
}

// toggle flips one calendar in the picker.
func (o *overlayState) toggle(cal string) {
	if cal == "" {
		return
	}
	if o.selected == nil {
		o.selected = map[string]bool{}
	}
	o.selected[cal] = !o.selected[cal]
	if !o.selected[cal] {
		delete(o.selected, cal)
	}
}

// picked returns the chosen calendars in a stable display order.
func (o overlayState) picked() []string {
	out := make([]string, 0, len(o.selected))
	for c := range o.selected {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// handleOverlayKey owns every key while the overlay picker is open.
func (m model) handleOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	o := &m.overlay

	if key == "ctrl+c" {
		m.mode = modeNormal
		return m, nil
	}
	cands := filterPickerItems(o.cands, o.input)
	switch key {
	case "esc":
		m.mode = modeNormal
		return m, nil
	case "down", "ctrl+n", "ctrl+j":
		if len(cands) > 0 {
			o.candIdx = (o.candIdx + 1) % len(cands)
		}
	case "up", "ctrl+p", "ctrl+k":
		if len(cands) > 0 {
			o.candIdx = (o.candIdx - 1 + len(cands)) % len(cands)
		}
	case " ", "space":
		if len(cands) > 0 {
			o.toggle(cands[max(0, min(o.candIdx, len(cands)-1))].value)
		} else if strings.Contains(strings.TrimSpace(o.input), "@") {
			o.toggle(strings.TrimSpace(o.input))
			o.input = ""
		}
	case "enter":
		picked := o.picked()
		m.mode = modeNormal
		if len(picked) == 0 {
			// Confirming an empty set is how you turn the overlay off, which is
			// more discoverable than a separate key for it.
			return m, m.clearOverlay()
		}
		if len(picked) > len(overlayColors) {
			// Past the palette every extra person reuses a color, and the dots
			// stop identifying anyone. Refuse rather than render a lie.
			m.mode = modeOverlayPicker
			m.status = fmt.Sprintf("at most %d calendars can be told apart by color", len(overlayColors))
			return m, nil
		}
		o.active = picked
		o.failed = map[string]string{}
		m.loading = true
		m.status = fmt.Sprintf("overlaying %d calendars~", len(picked))
		return m, m.reload()
	case "backspace", "ctrl+h":
		if o.input != "" {
			o.input = trimLastRune(o.input)
			o.candIdx = 0
		}
	default:
		if len(key) == 1 {
			o.input += msg.String()
			o.candIdx = 0
		}
	}
	return m, nil
}

// clearOverlay drops the overlay and reloads the single open calendar.
func (m *model) clearOverlay() tea.Cmd {
	m.overlay.active = nil
	m.overlay.failed = nil
	m.loading = true
	m.status = "overlay off · back to " + m.calendarDisplayName()
	return m.reload()
}

// viewOverlayPicker renders the calendar-set picker.
func (m model) viewOverlayPicker() string {
	o := m.overlay
	_, h := m.popupSize(12)
	w := min(max(58, m.preferredOverlayWidth()), max(40, m.width-4))
	inner := max(16, w-4)
	cands := filterPickerItems(o.cands, o.input)
	picked := o.picked()

	var lines []string
	lines = append(lines, sectionTitleStyle.Render(" Overlay calendars "))
	lines = append(lines, selectedStyle.Width(inner).Render("› "+o.input+"|"))
	if len(picked) == 0 {
		lines = append(lines, mutedStyle.Render("  none picked — Enter now turns the overlay OFF"))
	} else {
		// Show the colors here too, so the mapping is learned before the agenda
		// is full of dots.
		var chips []string
		for i, c := range picked {
			st := lipgloss.NewStyle().Foreground(lipgloss.Color(overlayColors[i%len(overlayColors)]))
			chips = append(chips, st.Render("●")+" "+overlayShortName(c))
		}
		lines = append(lines, pillStyle.Render(fmt.Sprintf("%d picked", len(picked)))+" "+
			ansiTruncate(strings.Join(chips, "  "), max(8, inner-12)))
	}
	rows := max(1, h-6)
	if len(cands) == 0 {
		if strings.Contains(o.input, "@") {
			lines = append(lines, linkStyle.Render("space adds this calendar directly: "+
				truncate(strings.TrimSpace(o.input), inner-4)))
		} else {
			lines = append(lines, mutedStyle.Render("  no match (type a full email/calendar id to add one)"))
		}
	} else {
		idx := max(0, min(o.candIdx, len(cands)-1))
		start := 0
		if idx >= rows {
			start = idx - rows + 1
		}
		for i := start; i < min(len(cands), start+rows); i++ {
			mark := "  "
			if o.selected[cands[i].value] {
				mark = "* "
			}
			line := mark + truncate(cands[i].label, max(4, inner-4))
			if i == idx {
				lines = append(lines, selectedStyle.Width(inner-2).Render(line))
			} else {
				lines = append(lines, line)
			}
		}
	}
	lines = append(lines, mutedStyle.Render("space toggle · Enter apply (none = off) · ESC cancel"))
	return modalStyle.Width(w).Height(min(len(lines)+2, max(8, m.height-4))).
		Render(strings.Join(lines, "\n"))
}

// overlayRowReserve is how many columns the owner tag takes off a row: the dot,
// a space, the padded name, and the separator before the title. The timeline
// has to know about it or it will size its bars against width the row no longer
// has, and every bar would be pushed off the right edge.
//
// A zero name width still costs the dot and its space — that is the narrow-pane
// fallback, not the absence of a tag.
func (m model) overlayRowReserve() int {
	if !m.overlay.on() {
		return 0
	}
	if w := m.overlayNameWidth(); w > 0 {
		return w + 3 // "● " + name + " "
	}
	return 2 // "● "
}
