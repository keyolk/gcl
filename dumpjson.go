package main

import (
	"encoding/json"
	"io"
	"time"
)

// jsonEvent is one event as `gcl -json` prints it: a stable shape for other
// tools (the cc-mods office reads it), not the TUI's Event. Times are RFC3339
// instants; an all-day event has date/endDate (YYYY-MM-DD, end exclusive as
// the API gives it) and no start/end.
type jsonEvent struct {
	ID         string   `json:"id"`
	Calendar   string   `json:"calendar"`
	Title      string   `json:"title"`
	Start      string   `json:"start,omitempty"`
	End        string   `json:"end,omitempty"`
	Date       string   `json:"date,omitempty"`
	EndDate    string   `json:"endDate,omitempty"`
	AllDay     bool     `json:"allDay"`
	Location   string   `json:"location,omitempty"`
	Rooms      []string `json:"rooms,omitempty"`
	Link       string   `json:"link,omitempty"`       // htmlLink: the event in Google Calendar
	Conference string   `json:"conference,omitempty"` // the meeting's video link
	Organizer  string   `json:"organizer,omitempty"`
	// MyResponse is the self attendee's reply; empty when the calendar's owner
	// is not on the guest list (an event of their own with no guests).
	MyResponse string   `json:"myResponse,omitempty"`
	Attendees  []Person `json:"attendees"`
	Reminders  []int    `json:"reminders,omitempty"`
}

type jsonDump struct {
	Calendar string      `json:"calendar"`
	From     string      `json:"from"`
	To       string      `json:"to"`
	Events   []jsonEvent `json:"events"`
}

func toJSONEvent(ev Event) jsonEvent {
	j := jsonEvent{
		ID:         ev.ID,
		Calendar:   ev.Calendar,
		Title:      ev.Title,
		AllDay:     ev.AllDay(),
		Location:   ev.Location,
		Rooms:      ev.Rooms,
		Link:       ev.HTMLLink,
		Conference: ev.ConferenceURI,
		Organizer:  ev.Organizer,
		Attendees:  ev.People,
		Reminders:  ev.ReminderMins,
	}
	if j.Attendees == nil {
		j.Attendees = []Person{}
	}
	if j.AllDay {
		j.Date = ev.StartDate.Format("2006-01-02")
		if !ev.EndDate.IsZero() {
			j.EndDate = ev.EndDate.Format("2006-01-02")
		}
	} else {
		if !ev.StartAt.IsZero() {
			j.Start = ev.StartAt.Format(time.RFC3339)
		}
		if !ev.EndAt.IsZero() {
			j.End = ev.EndAt.Format(time.RFC3339)
		}
	}
	for _, p := range ev.People {
		if p.Self {
			j.MyResponse = p.Response
			break
		}
	}
	return j
}

// writeJSONDump prints the events of [from, to) as one JSON object.
func writeJSONDump(w io.Writer, calendar string, from, to time.Time, events []Event) error {
	out := jsonDump{Calendar: calendar, From: from.Format(time.RFC3339), To: to.Format(time.RFC3339), Events: make([]jsonEvent, 0, len(events))}
	for _, ev := range events {
		out.Events = append(out.Events, toJSONEvent(ev))
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}
