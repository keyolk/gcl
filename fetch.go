package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"html"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

var (
	urlRe = regexp.MustCompile(`https?://[^\s<>'")]+`)
	tagRe = regexp.MustCompile(`<[^>]+>`)
)

func (m model) loadCmd(reqID int) tea.Cmd {
	// An overlay loads several calendars into the same list; everything
	// downstream (views, search, timeline) reads m.events either way.
	if m.overlay.on() {
		return m.loadOverlayCmd(reqID)
	}
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

// sortEvents orders the agenda chronologically. It sorts on the ABSOLUTE
// instant (eventSortInstant) rather than the local date/clock columns, so the
// order is a property of the events themselves and does not depend on which
// timezone the view happens to be displaying. All-day events sort at their
// day's local midnight, keeping them at the head of their day.
func sortEvents(events []Event) {
	sort.Slice(events, func(i, j int) bool {
		si, sj := eventSortInstant(&events[i]), eventSortInstant(&events[j])
		if !si.Equal(sj) {
			return si.Before(sj)
		}
		// All-day events come before timed ones starting at the same instant:
		// the day-long context reads first.
		if events[i].AllDay() != events[j].AllDay() {
			return events[i].AllDay()
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
		// The TSV carries dates and wall-clock times as separate columns, but the
		// rest of the app reasons about timed events through StartAt/EndAt — the
		// "now" divider, the active-window test, quick-action nudges and the
		// duplicate payload all read them. Leaving them zero here made a
		// gcalcli-sourced day look like a set of all-day events: every timed event
		// reported itself active, and the divider sank below the whole day.
		ev.StartAt, ev.EndAt = tsvInstants(&ev)
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
	sortEvents(events)
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

// tsvInstants derives the absolute start/end of a timed event from the separate
// date and wall-clock columns the gcalcli TSV provides. All-day events keep zero
// instants, which is what AllDay()-aware code expects; an unparseable or missing
// time also yields zero, so callers that already guard on IsZero() are unchanged.
func tsvInstants(ev *Event) (start, end time.Time) {
	if ev.AllDay() {
		return time.Time{}, time.Time{}
	}
	at := func(date time.Time, clock string) time.Time {
		if date.IsZero() || clock == "" {
			return time.Time{}
		}
		t, err := time.ParseInLocation("2006-01-02 15:04", date.Format("2006-01-02")+" "+clock, time.Local)
		if err != nil {
			return time.Time{}
		}
		return t
	}
	start = at(ev.StartDate, ev.StartTime)
	// A missing end_date means the event ends on the day it starts.
	endDate := ev.EndDate
	if endDate.IsZero() {
		endDate = ev.StartDate
	}
	end = at(endDate, ev.EndTime)
	return start, end
}

func cleanText(value string) string {
	value = html.UnescapeString(value)
	value = tagRe.ReplaceAllString(value, " ")
	value = sanitizeWidth(value)
	value = strings.Join(strings.Fields(value), " ")
	return value
}

// sanitizeWidth strips characters whose display width is unreliable across
// terminals — variation selectors (an emoji like 🎙️ is U+1F399 + VS16: some
// terminals draw 1 cell, others 2, and width libraries disagree), zero-width
// joiners/marks, and any rune whose width differs between the default and
// East-Asian ("ambiguous chars are double width") terminal modes. Event text
// (titles, locations) is user-authored, so anything unstable here desyncs
// every row after it.
func sanitizeWidth(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		switch {
		case r == 0xFE0E || r == 0xFE0F: // variation selectors (emoji/text style)
			continue
		case r == 0x200B || r == 0x200C || r == 0x200D || r == 0xFEFF: // zero-widths
			continue
		case r >= 0x80 && unstableWidth(r):
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// eawCond mirrors a KR/JP terminal that renders East-Asian-ambiguous glyphs
// double-width; comparing against the default condition finds every rune whose
// on-screen width can differ from what lipgloss computes.
var eawCond = runewidth.Condition{EastAsianWidth: true}

// unstableWidth reports whether the rune's display width differs between the
// default and East-Asian-width terminal modes (e.g. ‼ ★ ○ → arrows, and
// text-presentation emoji like 🎙 that some terminals draw 2 cells wide).
func unstableWidth(r rune) bool {
	return runewidth.RuneWidth(r) != eawCond.RuneWidth(r)
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
