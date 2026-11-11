package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestAPIEventKeepsAttendeesForJSON(t *testing.T) {
	ev := apiEventToEvent("me@x.com", apiEvent{
		ID:        "e1",
		Summary:   "Sync",
		Start:     apiTime{DateTime: "2026-11-11T10:00:00+09:00"},
		End:       apiTime{DateTime: "2026-11-11T10:30:00+09:00"},
		Organizer: apiPerson{Email: "boss@x.com"},
		Attendees: []apiPerson{
			{Email: "me@x.com", ResponseStatus: "needsAction", Self: true},
			{Email: "boss@x.com", DisplayName: "Boss", ResponseStatus: "accepted", Organizer: true},
			{Email: "room@resource.calendar.google.com", DisplayName: "Room 1", Resource: true},
		},
	})
	if ev == nil {
		t.Fatal("event not converted")
	}
	j := toJSONEvent(*ev)
	if len(j.Attendees) != 2 {
		t.Fatalf("attendees = %+v, want the two people (no room)", j.Attendees)
	}
	if j.MyResponse != "needsAction" || j.Organizer != "boss@x.com" {
		t.Fatalf("myResponse=%q organizer=%q", j.MyResponse, j.Organizer)
	}
	if j.Start != "2026-11-11T10:00:00+09:00" || j.AllDay {
		t.Fatalf("start=%q allDay=%v", j.Start, j.AllDay)
	}
	if len(j.Rooms) != 1 || j.Rooms[0] != "Room 1" {
		t.Fatalf("rooms = %v", j.Rooms)
	}
}

func TestJSONDumpAllDayAndEmptyAttendees(t *testing.T) {
	ev := apiEventToEvent("me@x.com", apiEvent{ID: "d", Summary: "Home", Start: apiTime{Date: "2026-11-11"}, End: apiTime{Date: "2026-11-12"}})
	var buf bytes.Buffer
	from := time.Date(2026, 11, 11, 0, 0, 0, 0, time.Local)
	if err := writeJSONDump(&buf, "me@x.com", from, from.AddDate(0, 0, 1), []Event{*ev}); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	e := out["events"].([]any)[0].(map[string]any)
	if e["allDay"] != true || e["date"] != "2026-11-11" || e["endDate"] != "2026-11-12" {
		t.Fatalf("all-day fields: %v", e)
	}
	if _, ok := e["start"]; ok {
		t.Fatalf("all-day event has start: %v", e)
	}
	// attendees is always a list, so a reader need not test for null.
	if a, ok := e["attendees"].([]any); !ok || len(a) != 0 {
		t.Fatalf("attendees = %#v, want []", e["attendees"])
	}
}
