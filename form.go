package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

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
		step:        stepTitle,
		title:       ev.Title,
		date:        ev.StartAt.In(loc).Format("2006-01-02"),
		start:       ev.StartAt.In(loc).Format("15:04"),
		durationStr: fmt.Sprintf("%d", dur),
		duration:    dur,
		location:    ev.Location,
		description: ev.Description,
		selected:    sel,
		orig: &editSnapshot{
			title:       ev.Title,
			date:        ev.StartAt.In(loc).Format("2006-01-02"),
			start:       ev.StartAt.In(loc).Format("15:04"),
			duration:    dur,
			location:    ev.Location,
			description: ev.Description,
			attendees:   sortedKeys(sel),
		},
		attCands:     m.attendeeCandidatePool(),
		locCands:     m.locationCandidatePool(),
		editingField: true,
		editing:      true,
		eventID:      ev.ID,
		calendar:     m.eventCalendar(ev),
	}
}

func (m model) handleCreateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	c := &m.create

	// `?` inside the form opens the form-specific help overlay (not the global
	// one), so the user can discover field navigation, flexible input, and
	// quick actions while composing.
	if key == "?" {
		m.mode = modeFormHelp
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
	case stepTitle, stepDate, stepStart, stepDuration, stepLocation, stepRepeat, stepDescription:
		field := c.fieldPtr()
		if !c.editingField {
			switch key {
			case "j", "down", "tab":
				nextField(1)
			case "k", "up", "shift+tab":
				nextField(-1)
			case "enter", "i":
				c.editingField = true
			case " ", "space":
				c.editingField = true
				*field += " "
			}
			return m, nil
		}
		// Location field: ctrl+n/ctrl+p cycle suggestions, ctrl+y accepts the
		// highlighted one. Handled before the generic text keys so they don't
		// get typed into the field.
		if c.step == stepLocation {
			cands := filterPickerItems(c.locCands, c.location)
			switch key {
			case "ctrl+n":
				if len(cands) > 0 {
					c.locCandidx = (c.locCandidx + 1) % len(cands)
				}
				return m, nil
			case "ctrl+p":
				if len(cands) > 0 {
					c.locCandidx = (c.locCandidx - 1 + len(cands)) % len(cands)
				}
				return m, nil
			case "ctrl+y":
				if len(cands) > 0 {
					c.location = cands[max(0, min(c.locCandidx, len(cands)-1))].value
					c.locCandidx = 0
				}
				return m, nil
			}
		}

		switch key {
		// While editing a text field, j/k must TYPE - only Tab/S-Tab (and arrows,
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
				// Route through a confirmation prompt so a mistyped
				// submit isn't created outright — mirroring the delete
				// confirmation (modeConfirmDelete). y/Enter here
				// actually submits; n/ESC returns to the form.
				m.mode = modeConfirmSubmit
			}
		case "backspace", "ctrl+h":
			if len(*field) > 0 {
				_, size := utf8.DecodeLastRuneInString(*field)
				*field = (*field)[:len(*field)-size]
				if c.step == stepLocation {
					c.locCandidx = 0
				}
			}
		default:
			if key == " " || key == "space" {
				*field += " "
			} else if isTypedRune(msg) {
				*field += msg.String()
				if c.step == stepLocation {
					c.locCandidx = 0
				}
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
		case " ", "space":
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
				m.mode = modeConfirmSubmit
			}
		case "backspace", "ctrl+h":
			if len(c.attInput) > 0 {
				_, size := utf8.DecodeLastRuneInString(c.attInput)
				c.attInput = c.attInput[:len(c.attInput)-size]
				c.attCandidx = 0
			}
		default:
			if isTypedRune(msg) {
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
	case stepLocation:
		return &c.location
	case stepRepeat:
		return &c.repeat
	case stepDescription:
		return &c.description
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
		if _, err := parseFlexibleDate(c.date, time.Now()); err != nil {
			return "date: try 2026-07-20, tmr, mon, or +3d"
		}
	case stepStart:
		if _, err := parseFlexibleTime(c.start); err != nil {
			return "start: try 15:00, 3pm, 1530, or 15시"
		}
	case stepDuration:
		if _, err := parseDurationMinutes(c.durationStr); err != nil {
			return "duration: try 30, 1h, 1h30m, or 90m"
		}
	}
	return ""
}

// validateAll checks the whole form for the single-screen create/edit flow and
// rewrites the flexible date/time/duration input into canonical text, so what
// the form shows after submit-validation is exactly what gets created.
func (c *createState) validateAll() string {
	if strings.TrimSpace(c.title) == "" {
		return "title is required"
	}
	return c.normalize(time.Now())
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
		c.step = stepLocation
	case stepLocation:
		c.step = stepRepeat
	case stepRepeat:
		c.step = stepDescription
	case stepDescription:
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

// parseDurationMinutes now lives in datetime.go alongside the flexible
// date/time parsers, so all form-input parsing sits in one place.

func (m model) submitCreateCmd() tea.Cmd {
	c := m.create
	// An edit carries its target calendar (an overlay row may not be ours);
	// a new event always lands on the calendar currently open.
	cal := m.calendar
	if c.editing && c.calendar != "" {
		cal = c.calendar
	}
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
				Calendar:    cal,
				EventID:     c.eventID,
				Title:       strings.TrimSpace(c.title),
				Start:       start,
				End:         end,
				Location:    c.location,
				Description: c.description,
				Attendees:   c.attendees,
				Notify:      len(c.attendees) > 0,
			})
			return createdMsg{link: link, err: err, edited: true, eventID: c.eventID, calendar: cal}
		}
		created, err := createEvent(createEventInput{
			Calendar:    cal,
			Title:       strings.TrimSpace(c.title),
			Start:       start,
			End:         end,
			Location:    c.location,
			Description: c.description,
			Recurrence:  c.recurrenceRules(),
			Attendees:   c.attendees,
			Notify:      len(c.attendees) > 0,
		})
		return createdMsg{link: created.Link, err: err, eventID: created.ID, calendar: cal}
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
		locCands:     m.locationCandidatePool(),
		editingField: true,
	}
}

// locationCandidatePool gathers location suggestions from the events currently
// loaded: real meeting rooms and physical locations you already use show up
// first, so recurring places don't have to be retyped.
func (m model) locationCandidatePool() []pickerItem {
	seen := map[string]bool{}
	var out []pickerItem
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			return
		}
		seen[strings.ToLower(s)] = true
		out = append(out, pickerItem{label: s, value: s})
	}
	for i := range m.events {
		add(m.events[i].Location)
		for _, r := range m.events[i].Rooms {
			add(r)
		}
	}
	return out
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
