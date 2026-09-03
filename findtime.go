package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// "Find a time" — pick people, then pick a slot that works for all of them.
//
// Scheduling a meeting from a calendar viewer normally means opening five
// calendars in five tabs and diffing them by eye. The whole flow lives in two
// steps here:
//
//	f          open the people picker (fuzzy, space toggles, Enter searches)
//	           -> slot list, ranked: everyone-free first, then partial
//	Enter      prefill the New-event form with that slot and those attendees
//
// Enter deliberately hands off to the existing create form rather than
// inserting straight away: the form is where the title, location and the
// "invitation emails will be sent to N people" warning already live, and a
// mis-picked slot that has already mailed five people is not recoverable in a
// way `u` makes comfortable.
//
// The slot list is an overlay, not a docked panel like `a`: unlike "active
// now", it is a task you finish and leave, and it needs the width.

// findStep is which half of the flow is on screen.
type findStep int

const (
	findPeople   findStep = iota // fuzzy-pick the participants
	findSlotList                 // browse the ranked candidate slots
)

// findState drives the whole flow. It is only meaningful while
// m.mode == modeFindTime.
type findState struct {
	step findStep

	// People step.
	input    string       // fuzzy filter
	cands    []pickerItem // candidate pool (colleagues seen on loaded events, recent calendars)
	candIdx  int          // highlighted candidate
	selected map[string]bool

	// Slot step.
	search  slotSearch
	results []freeBusyResult
	slots   []candidateSlot
	slotIdx int
	loading bool
	err     string
}

// unreadable lists the participants whose free/busy could not be read, with the
// reason. Surfacing them matters most when the search finds nothing: "no slot"
// and "no slot that we could actually see" are different answers.
func (f findState) unreadable() []string {
	var out []string
	for _, r := range f.results {
		if r.err != "" {
			out = append(out, strings.SplitN(r.calendar, "@", 2)[0]+" ("+r.err+")")
		}
	}
	return out
}

// findTimeMsg carries a completed free/busy search back to Update.
type findTimeMsg struct {
	results []freeBusyResult
	slots   []candidateSlot
	err     error
}

// participants returns the chosen calendar ids, sorted for a stable display.
func (f findState) participants() []string {
	out := make([]string, 0, len(f.selected))
	for e := range f.selected {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// toggle flips one participant. Mirrors createState.toggle so the two pickers
// behave identically.
func (f *findState) toggle(email string) {
	if email == "" {
		return
	}
	if f.selected == nil {
		f.selected = map[string]bool{}
	}
	f.selected[email] = !f.selected[email]
	if !f.selected[email] {
		delete(f.selected, email)
	}
}

// newFindState seeds the flow. The user's own calendar is pre-selected: a
// meeting you are scheduling is almost always one you attend, and forgetting to
// include yourself produces slots you cannot actually make.
func (m model) newFindState() findState {
	f := findState{
		step:     findPeople,
		cands:    m.participantCandidatePool(),
		selected: map[string]bool{},
		search:   defaultSlotSearch(time.Now(), m.tz()),
	}
	if me := myIdentity(); me != "" {
		f.selected[me] = true
	}
	return f
}

// participantCandidatePool gathers people worth inviting: the attendees of
// every loaded event, recent calendars, and the calendar currently open. It
// reuses attendeeCandidatePool and adds the current calendar, which is often
// exactly the person whose time you are trying to find.
func (m model) participantCandidatePool() []pickerItem {
	items := m.attendeeCandidatePool()
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		seen[strings.ToLower(it.value)] = true
	}
	add := func(email string) {
		email = strings.TrimSpace(email)
		if email == "" || !strings.Contains(email, "@") || seen[strings.ToLower(email)] {
			return
		}
		seen[strings.ToLower(email)] = true
		// Prepend: the calendar you are looking at is the most likely invitee.
		items = append([]pickerItem{{label: email, value: email}}, items...)
	}
	if me := myIdentity(); me != "" {
		add(me)
	}
	add(m.calendar)
	return items
}

// runFindSlotsCmd performs the free/busy query off the update loop. The search
// is captured by value so a later parameter change cannot mutate an in-flight
// request.
func (m model) runFindSlotsCmd() tea.Cmd {
	people := m.find.participants()
	search := m.find.search
	return func() tea.Msg {
		results, err := queryFreeBusy(people, search.from, search.to)
		if err != nil {
			return findTimeMsg{err: err}
		}
		return findTimeMsg{results: results, slots: findSlots(results, search)}
	}
}

// startFindSearch moves to the slot step and kicks off the query.
func (m *model) startFindSearch() tea.Cmd {
	if len(m.find.selected) == 0 {
		m.find.err = "pick at least one person (space toggles)"
		return nil
	}
	m.find.step = findSlotList
	m.find.loading = true
	m.find.err = ""
	m.find.slotIdx = 0
	// Re-anchor on every search: a picker left open for ten minutes must not
	// propose a slot that has already started.
	m.find.search.from = time.Now()
	m.find.search.to = time.Now().AddDate(0, 0, settings.slotSearchDays)
	m.find.search.loc = m.tz()
	return m.runFindSlotsCmd()
}

// handleFindKey owns every key while the find-a-time overlay is open.
func (m model) handleFindKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	f := &m.find

	if key == "ctrl+c" {
		m.mode = modeNormal
		return m, nil
	}

	if f.step == findPeople {
		cands := filterPickerItems(f.cands, f.input)
		switch key {
		case "esc":
			m.mode = modeNormal
			return m, nil
		case "down", "ctrl+n", "ctrl+j":
			if len(cands) > 0 {
				f.candIdx = (f.candIdx + 1) % len(cands)
			}
		case "up", "ctrl+p", "ctrl+k":
			if len(cands) > 0 {
				f.candIdx = (f.candIdx - 1 + len(cands)) % len(cands)
			}
		case " ", "space":
			// Space toggles the highlighted candidate; a typed-out email with no
			// match is added directly, so someone outside the loaded events can
			// still be included.
			if len(cands) > 0 {
				f.toggle(cands[max(0, min(f.candIdx, len(cands)-1))].value)
			} else if strings.Contains(strings.TrimSpace(f.input), "@") {
				f.toggle(strings.TrimSpace(f.input))
				f.input = ""
			}
			f.err = ""
		case "enter":
			return m, m.startFindSearch()
		case "backspace", "ctrl+h":
			if f.input != "" {
				f.input = trimLastRune(f.input)
				f.candIdx = 0
			}
		default:
			if len(key) == 1 {
				f.input += msg.String()
				f.candIdx = 0
			}
		}
		return m, nil
	}

	// Slot step.
	switch key {
	case "esc":
		// Back to the people picker rather than out of the flow: adjusting the
		// participant set is the most common reason the slot list disappoints.
		f.step = findPeople
		f.err = ""
		return m, nil
	case "down", "j", "ctrl+n":
		f.slotIdx = min(f.slotIdx+1, max(0, len(f.slots)-1))
	case "up", "k", "ctrl+p":
		f.slotIdx = max(0, f.slotIdx-1)
	case "g", "home":
		f.slotIdx = 0
	case "G", "end":
		f.slotIdx = max(0, len(f.slots)-1)
	case "d":
		// Cycle the meeting length and re-run. Duration is the parameter people
		// actually renegotiate ("can we do 30 instead of an hour?").
		f.search.duration = nextSlotDuration(f.search.duration)
		f.loading = true
		return m, m.runFindSlotsCmd()
	case "w":
		f.search.skipWeekends = !f.search.skipWeekends
		f.loading = true
		return m, m.runFindSlotsCmd()
	case "H":
		// Toggle between the configured hours and a fully open day, so a search
		// that finds nothing has one obvious thing left to try.
		if f.search.dayStartHour == 0 && f.search.dayEndHour == 24 {
			f.search.dayStartHour = settings.slotDayStart
			f.search.dayEndHour = settings.slotDayEnd
		} else {
			f.search.dayStartHour, f.search.dayEndHour = 0, 24
		}
		f.loading = true
		return m, m.runFindSlotsCmd()
	case "R":
		f.loading = true
		return m, m.runFindSlotsCmd()
	case "enter":
		if f.loading || len(f.slots) == 0 {
			return m, nil
		}
		slot := f.slots[max(0, min(f.slotIdx, len(f.slots)-1))]
		m.mode = modeCreate
		m.create = m.createStateFromSlot(slot)
		m.status = fmt.Sprintf("prefilled %s · title it and press Enter to create",
			m.slotRangeLabel(slot))
		return m, nil
	}
	return m, nil
}

// nextSlotDuration steps to the next option in slotDurationOptions, wrapping.
// An unlisted duration (from config) enters the cycle at the next larger value.
func nextSlotDuration(d time.Duration) time.Duration {
	mins := int(d / time.Minute)
	for _, opt := range slotDurationOptions {
		if opt > mins {
			return time.Duration(opt) * time.Minute
		}
	}
	return time.Duration(slotDurationOptions[0]) * time.Minute
}

// createStateFromSlot builds a prefilled create form: the chosen slot's date,
// start and duration, with every participant already selected as an attendee.
// The title is deliberately left empty — it is the one thing only the organizer
// knows, and an empty title is what the form's own validation catches.
func (m model) createStateFromSlot(slot candidateSlot) createState {
	loc := m.tz()
	start := slot.start.In(loc)
	dur := int(slot.end.Sub(slot.start) / time.Minute)
	c := createState{
		step:         stepTitle,
		date:         start.Format("2006-01-02"),
		start:        start.Format("15:04"),
		durationStr:  fmt.Sprintf("%d", dur),
		duration:     dur,
		selected:     map[string]bool{},
		attCands:     m.attendeeCandidatePool(),
		locCands:     m.locationCandidatePool(),
		editingField: true,
	}
	me := myIdentity()
	for _, p := range m.find.participants() {
		// The organizer is the calendar owner; adding yourself as an attendee
		// makes Google mail you your own invitation.
		if me != "" && strings.EqualFold(p, me) {
			continue
		}
		c.selected[p] = true
	}
	return c
}

// slotRangeLabel renders a slot as "Thu 09-04 10:00-11:00" in the display tz.
func (m model) slotRangeLabel(slot candidateSlot) string {
	loc := m.tz()
	s := slot.start.In(loc)
	e := slot.end.In(loc)
	return s.Format("Mon 01-02 15:04") + "-" + e.Format("15:04")
}

// viewFindTime renders whichever step is active.
func (m model) viewFindTime() string {
	if m.find.step == findPeople {
		return m.viewFindPeople()
	}
	return m.viewFindSlots()
}

func (m model) viewFindPeople() string {
	f := m.find
	// Same box width as the slot list, so stepping between the two halves of
	// the flow does not resize the overlay under the cursor.
	_, h := m.popupSize(12)
	w := min(max(58, m.preferredOverlayWidth()), max(40, m.width-4))
	inner := max(16, w-4)
	cands := filterPickerItems(f.cands, f.input)
	chosen := f.participants()

	var lines []string
	lines = append(lines, sectionTitleStyle.Render(" Find a time · who? "))
	lines = append(lines, selectedStyle.Width(inner).Render("› "+f.input+"|"))
	if len(chosen) == 0 {
		lines = append(lines, mutedStyle.Render("  nobody picked yet — space toggles the highlighted person"))
	} else {
		lines = append(lines, pillStyle.Render(fmt.Sprintf("%d picked", len(chosen)))+" "+
			mutedStyle.Render(truncate(shortNames(chosen), max(8, inner-14))))
	}
	rows := max(1, h-6)
	if len(cands) == 0 {
		if strings.Contains(f.input, "@") {
			lines = append(lines, linkStyle.Render("space adds this address directly: "+truncate(strings.TrimSpace(f.input), inner-4)))
		} else {
			lines = append(lines, mutedStyle.Render("  no match (type a full email to add someone new)"))
		}
	} else {
		idx := max(0, min(f.candIdx, len(cands)-1))
		start := 0
		if idx >= rows {
			start = idx - rows + 1
		}
		for i := start; i < min(len(cands), start+rows); i++ {
			mark := "  "
			if f.selected[cands[i].value] {
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
	if f.err != "" {
		lines = append(lines, errorStyle.Render("x "+f.err))
	}
	lines = append(lines, mutedStyle.Render("space toggle · Enter find slots · ESC cancel"))
	return modalStyle.Width(w).Height(min(len(lines)+2, max(8, m.height-4))).Render(strings.Join(lines, "\n"))
}

func (m model) viewFindSlots() string {
	f := m.find
	// The slot list carries a time range, a badge, and a keys footer on one
	// line each. popupSize is tuned for the narrow fuzzy pickers, and at that
	// width the footer wraps to three rows — so this overlay asks for its own,
	// wider box, still bounded by the pane.
	_, h := m.popupSize(14)
	w := min(max(58, m.preferredOverlayWidth()), max(40, m.width-4))
	inner := max(20, w-4)
	people := f.participants()

	var lines []string
	lines = append(lines, sectionTitleStyle.Render(fmt.Sprintf(" Find a time · %s for %d ",
		humanDuration(f.search.duration), len(people))))
	lines = append(lines, mutedStyle.Render("  "+truncate(describeSlotWindow(f.search), inner-2)))

	switch {
	case f.loading:
		lines = append(lines, "", statusStyle.Render("  reading free/busy~"))
	case f.err != "":
		lines = append(lines, "", errorStyle.Render("  x "+truncate(f.err, inner-4)))
	case len(f.slots) == 0:
		lines = append(lines, "", mutedStyle.Render("  no slot fits anyone in this window"))
		lines = append(lines, mutedStyle.Render("  d shorter meeting · H open up the hours · esc change who"))
		// "Nothing found" and "nothing found, and two calendars were opaque"
		// call for different next moves, so the unreadable ones are named here
		// even though no slot row exists to hang them off.
		if bad := f.unreadable(); len(bad) > 0 {
			lines = append(lines, mutedStyle.Render("  couldn't read: "+truncate(strings.Join(bad, ", "), inner-16)))
		}
	default:
		rows := max(1, h-6)
		idx := max(0, min(f.slotIdx, len(f.slots)-1))
		start := 0
		if idx >= rows {
			start = idx - rows + 1
		}
		for i := start; i < min(len(f.slots), start+rows); i++ {
			lines = append(lines, m.slotRow(f.slots[i], i == idx, inner))
		}
		if rest := len(f.slots) - (start + rows); rest > 0 {
			lines = append(lines, mutedStyle.Render(fmt.Sprintf("  ~ +%d more", rest)))
		}
		// Name who is blocked in the highlighted slot. The count alone does not
		// answer the question that actually decides it: is the missing person
		// the one the meeting is for?
		if sel := f.slots[idx]; len(sel.busy) > 0 || len(sel.unknown) > 0 {
			lines = append(lines, "")
			if len(sel.busy) > 0 {
				lines = append(lines, errorStyle.Render("  busy: ")+
					mutedStyle.Render(truncate(shortNames(sel.busy), max(8, inner-10))))
			}
			if len(sel.unknown) > 0 {
				lines = append(lines, mutedStyle.Render("  unknown (no free/busy access): "+
					truncate(shortNames(sel.unknown), max(8, inner-34))))
			}
		}
	}

	lines = append(lines, mutedStyle.Render("j/k move · Enter prefill form · d length · w weekends · H hours · esc who"))
	return modalStyle.Width(w).Height(min(len(lines)+2, max(8, m.height-4))).Render(strings.Join(lines, "\n"))
}

// slotRow renders one candidate: when it is, and how many of the group can make
// it. The availability badge leads on color so a partial slot never reads as a
// full one at a glance.
//
// The denominator is derived from the slot itself (free + busy) rather than from
// a caller-supplied participant count: the two can drift apart, and "3/2" is
// worse than useless. Unknown participants are outside that count entirely —
// "3/3 +1?" is honest, "3/4" would claim a conflict nobody observed.
func (m model) slotRow(slot candidateSlot, selected bool, width int) string {
	when := m.slotRangeLabel(slot)
	known := len(slot.free) + len(slot.busy)
	badge := fmt.Sprintf("%d/%d", len(slot.free), known)
	var badgeStyled string
	if slot.allFree() {
		badgeStyled = slotAllFreeStyle.Render(" ✓" + badge + " ")
	} else {
		badgeStyled = slotPartialStyle.Render(" " + badge + " ")
	}
	row := when + "  " + badgeStyled
	if n := len(slot.unknown); n > 0 {
		row += mutedStyle.Render(fmt.Sprintf(" +%d?", n))
	}
	if selected {
		return selectedRowStyle.Render("▸ ") + ansiTruncate(row, max(4, width-2))
	}
	return "  " + ansiTruncate(row, max(4, width-2))
}

// shortNames renders a participant list compactly: local parts only, capped
// with "+N" so a ten-person list does not wrap the overlay.
func shortNames(emails []string) string {
	short := make([]string, 0, len(emails))
	for _, e := range emails {
		short = append(short, strings.SplitN(e, "@", 2)[0])
	}
	if len(short) <= 4 {
		return strings.Join(short, ", ")
	}
	return strings.Join(short[:4], ", ") + fmt.Sprintf(" +%d", len(short)-4)
}

// trimLastRune drops the final rune, for backspace handling.
func trimLastRune(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return string(r[:len(r)-1])
}

var (
	// A fully-available slot is the answer; a partial one is a compromise. The
	// two must not be told apart by reading digits.
	slotAllFreeStyle = lipgloss.NewStyle().Bold(true).
				Foreground(lipgloss.Color("232")).Background(lipgloss.Color("77"))
	slotPartialStyle = lipgloss.NewStyle().Bold(true).
				Foreground(lipgloss.Color("232")).Background(lipgloss.Color("214"))
)
