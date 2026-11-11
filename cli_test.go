package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWithReplyChangesOnlySelfAndKeepsEveryGuest(t *testing.T) {
	atts := []map[string]any{
		{"email": "boss@x.com", "organizer": true, "responseStatus": "accepted"},
		{"email": "me@x.com", "self": true, "responseStatus": "needsAction", "optional": true, "comment": "hi"},
		{"email": "room@resource.calendar.google.com", "resource": true, "responseStatus": "accepted"},
	}
	out, before, err := withReply(atts, "declined")
	if err != nil {
		t.Fatal(err)
	}
	if before != "needsAction" {
		t.Fatalf("before = %q", before)
	}
	if len(out) != 3 {
		t.Fatalf("a patch replaces the list: all %d guests must go back, got %d", len(atts), len(out))
	}
	if out[1]["responseStatus"] != "declined" || out[1]["optional"] != true || out[1]["comment"] != "hi" {
		t.Fatalf("self = %v", out[1])
	}
	if out[0]["responseStatus"] != "accepted" || out[2]["resource"] != true {
		t.Fatalf("others changed: %v", out)
	}
	// The caller's maps are not written.
	if atts[1]["responseStatus"] != "needsAction" {
		t.Fatalf("input mutated: %v", atts[1])
	}
}

func TestWithReplyRefusesAnEventWithoutMe(t *testing.T) {
	if _, _, err := withReply([]map[string]any{{"email": "a@x.com"}}, "accepted"); err != errNotInvited {
		t.Fatalf("err = %v, want errNotInvited", err)
	}
	if _, _, err := withReply(nil, "accepted"); err != errNotInvited {
		t.Fatalf("no guests: err = %v", err)
	}
}

func TestRSVPArgsAreCheckedBeforeAnyCall(t *testing.T) {
	for _, args := range [][]string{{"rsvp"}, {"rsvp", "id"}, {"rsvp", "id", "sure"}, {"rsvp", "id", "yes", "extra"}} {
		var out, errb bytes.Buffer
		ok, code := runSubcommand(args, &out, &errb)
		if !ok || code != 2 || out.Len() != 0 {
			t.Fatalf("%v: ok=%v code=%d out=%q", args, ok, code, out.String())
		}
	}
	if ok, _ := runSubcommand([]string{"-json"}, nil, nil); ok {
		t.Fatal("a flag is not a subcommand")
	}
}

func TestFindOutputNamesWhoIsBlockedAndWhoIsUnreadable(t *testing.T) {
	loc := time.FixedZone("KST", 9*3600)
	from := time.Date(2026, 11, 12, 9, 0, 0, 0, loc)
	search := slotSearch{duration: 30 * time.Minute, step: 30 * time.Minute, from: from, to: from.Add(2 * time.Hour), dayStartHour: 0, dayEndHour: 24, loc: loc, maxResults: 10}
	results := []freeBusyResult{
		{calendar: "a@x.com", busy: []busyInterval{{start: from, end: from.Add(time.Hour)}}},
		{calendar: "b@x.com"},
		{calendar: "c@x.com", err: "notFound"},
	}
	out := findOutput([]string{"a@x.com", "b@x.com", "c@x.com"}, results, search)
	if len(out.Errors) != 1 || out.Errors[0].Calendar != "c@x.com" {
		t.Fatalf("errors = %+v", out.Errors)
	}
	if len(out.Slots) == 0 || !out.Slots[0].AllFree || !strings.HasPrefix(out.Slots[0].Start, "2026-11-12T10:00") {
		t.Fatalf("first slot = %+v, want the all-free 10:00", out.Slots)
	}
	var buf bytes.Buffer
	if writeJSON(&buf, &buf, out) != 0 {
		t.Fatal("write")
	}
	var back map[string]any
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if _, ok := back["slots"].([]any); !ok {
		t.Fatalf("slots = %#v", back["slots"])
	}
}

func TestParseHours(t *testing.T) {
	for in, want := range map[string][2]int{"9-19": {9, 19}, "0-24": {0, 24}} {
		a, b, ok := parseHours(in)
		if !ok || a != want[0] || b != want[1] {
			t.Fatalf("%q = %d %d %v", in, a, b, ok)
		}
	}
	for _, in := range []string{"", "9", "19-9", "9-25", "-1-5", "9-19x", "09-19"} {
		if _, _, ok := parseHours(in); ok {
			t.Fatalf("%q accepted", in)
		}
	}
}
