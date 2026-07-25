package main

import (
	"testing"
	"time"
)

// ref is a fixed reference "now" for deterministic relative-date tests:
// Wednesday 2026-07-15 14:23 local.
func ref(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, 7, 15, 14, 23, 0, 0, time.Local)
}

func TestParseFlexibleDate(t *testing.T) {
	now := ref(t)
	cases := []struct {
		in   string
		want string // YYYY-MM-DD, or "" when an error is expected
	}{
		// Absolute forms stay supported.
		{"2026-07-20", "2026-07-20"},
		{" 2026-07-20 ", "2026-07-20"},
		// Short numeric forms.
		{"07-20", "2026-07-20"},
		{"7/20", "2026-07-20"},
		{"0720", "2026-07-20"},
		{"20", "2026-07-20"}, // day-of-month in the current month
		// Keywords.
		{"today", "2026-07-15"},
		{"tod", "2026-07-15"},
		{"tomorrow", "2026-07-16"},
		{"tmr", "2026-07-16"},
		{"tom", "2026-07-16"},
		{"yesterday", "2026-07-14"},
		{"yst", "2026-07-14"},
		// Relative offsets.
		{"+1d", "2026-07-16"},
		{"+3d", "2026-07-18"},
		{"-1d", "2026-07-14"},
		{"+1w", "2026-07-22"},
		{"+2w", "2026-07-29"},
		{"+1m", "2026-08-15"},
		{"3d", "2026-07-18"}, // bare offset without '+'
		// Weekday names resolve to the NEXT occurrence (strictly future).
		{"mon", "2026-07-20"},
		{"monday", "2026-07-20"},
		{"wed", "2026-07-22"}, // today is Wed → next Wed, not today
		{"fri", "2026-07-17"},
		{"sun", "2026-07-19"},
		// "next <weekday>" is the same as the bare weekday form.
		{"next mon", "2026-07-20"},
		{"next fri", "2026-07-17"},
		// Case/space insensitivity.
		{"MON", "2026-07-20"},
		{"  Tomorrow ", "2026-07-16"},
		// Invalid input.
		{"", ""},
		{"notaday", ""},
		{"2026-13-40", ""},
		{"+d", ""},
	}
	for _, tc := range cases {
		got, err := parseFlexibleDate(tc.in, now)
		if tc.want == "" {
			if err == nil {
				t.Errorf("parseFlexibleDate(%q): expected error, got %s", tc.in, got.Format("2006-01-02"))
			}
			continue
		}
		if err != nil {
			t.Errorf("parseFlexibleDate(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if g := got.Format("2006-01-02"); g != tc.want {
			t.Errorf("parseFlexibleDate(%q) = %s, want %s", tc.in, g, tc.want)
		}
	}
}

func TestParseFlexibleTime(t *testing.T) {
	cases := []struct {
		in   string
		want string // HH:MM, or "" when an error is expected
	}{
		// Absolute 24h forms stay supported.
		{"15:04", "15:04"},
		{" 09:30 ", "09:30"},
		{"9:30", "09:30"},
		// Compact digits.
		{"1504", "15:04"},
		{"930", "09:30"},
		{"15", "15:00"},
		{"9", "09:00"},
		// 12h with am/pm.
		{"3pm", "15:00"},
		{"3PM", "15:00"},
		{"3:30pm", "15:30"},
		{"11am", "11:00"},
		{"11:15am", "11:15"},
		{"12pm", "12:00"}, // noon
		{"12am", "00:00"}, // midnight
		{"12:30am", "00:30"},
		{"3 pm", "15:00"},
		// Korean suffix.
		{"15시", "15:00"},
		{"9시", "09:00"},
		{"9시30분", "09:30"},
		// Invalid input.
		{"", ""},
		{"25:00", ""},
		{"12:60", ""},
		{"13pm", ""},
		{"abc", ""},
	}
	for _, tc := range cases {
		got, err := parseFlexibleTime(tc.in)
		if tc.want == "" {
			if err == nil {
				t.Errorf("parseFlexibleTime(%q): expected error, got %s", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseFlexibleTime(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseFlexibleTime(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestParseDurationMinutesFlexible(t *testing.T) {
	cases := []struct {
		in   string
		want int // 0 when an error is expected
	}{
		// Existing behavior preserved.
		{"30", 30},
		{"1h", 60},
		{"1.5h", 90},
		{"45m", 45},
		// New forms.
		{"1h30m", 90},
		{"1h30", 90},
		{"2h15m", 135},
		{"90m", 90},
		{"1일", 1440},
		{"30분", 30},
		{"1시간", 60},
		{"1시간30분", 90},
		// Invalid input.
		{"", 0},
		{"0", 0},
		{"-5", 0},
		{"abc", 0},
	}
	for _, tc := range cases {
		got, err := parseDurationMinutes(tc.in)
		if tc.want == 0 {
			if err == nil {
				t.Errorf("parseDurationMinutes(%q): expected error, got %d", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDurationMinutes(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseDurationMinutes(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestRecurrenceRules(t *testing.T) {
	cases := []struct {
		in   string
		want string // single RRULE line, or "" for no recurrence
	}{
		{"", ""},
		{"  ", ""},
		{"none", ""},
		{"daily", "RRULE:FREQ=DAILY"},
		{"d", "RRULE:FREQ=DAILY"},
		{"매일", "RRULE:FREQ=DAILY"},
		{"weekly", "RRULE:FREQ=WEEKLY"},
		{"w", "RRULE:FREQ=WEEKLY"},
		{"매주", "RRULE:FREQ=WEEKLY"},
		{"biweekly", "RRULE:FREQ=WEEKLY;INTERVAL=2"},
		{"2w", "RRULE:FREQ=WEEKLY;INTERVAL=2"},
		{"monthly", "RRULE:FREQ=MONTHLY"},
		{"m", "RRULE:FREQ=MONTHLY"},
		{"매월", "RRULE:FREQ=MONTHLY"},
		{"yearly", "RRULE:FREQ=YEARLY"},
		{"weekdays", "RRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"},
		{"평일", "RRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"},
		// Occurrence counts.
		{"weekly x4", "RRULE:FREQ=WEEKLY;COUNT=4"},
		{"daily x10", "RRULE:FREQ=DAILY;COUNT=10"},
		{"weekdays x5", "RRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;COUNT=5"},
		{"weekly*3", "RRULE:FREQ=WEEKLY;COUNT=3"},
	}
	for _, tc := range cases {
		c := &createState{repeat: tc.in}
		got := c.recurrenceRules()
		if tc.want == "" {
			if len(got) != 0 {
				t.Errorf("recurrenceRules(%q) = %v, want none", tc.in, got)
			}
			continue
		}
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("recurrenceRules(%q) = %v, want [%s]", tc.in, got, tc.want)
		}
	}
}

func TestRecurrenceRulesRejectsUnknown(t *testing.T) {
	// Unknown text must not silently produce a bogus RRULE.
	c := &createState{repeat: "sometimes"}
	if got := c.recurrenceRules(); len(got) != 0 {
		t.Errorf("expected no rules for unknown repeat, got %v", got)
	}
	if err := c.validateRepeat(); err == "" {
		t.Error("expected validateRepeat to reject unknown repeat text")
	}
	c = &createState{repeat: "weekly"}
	if err := c.validateRepeat(); err != "" {
		t.Errorf("expected weekly to validate, got %q", err)
	}
}

func TestNormalizeFormDateTime(t *testing.T) {
	now := ref(t)
	// normalizeFormDateTime rewrites the raw form fields into canonical
	// YYYY-MM-DD / HH:MM so the submit path and the rendered form agree.
	c := &createState{date: "tmr", start: "3pm", durationStr: "1h30m"}
	if err := c.normalize(now); err != "" {
		t.Fatalf("normalize: unexpected error %q", err)
	}
	if c.date != "2026-07-16" {
		t.Errorf("date = %q, want 2026-07-16", c.date)
	}
	if c.start != "15:00" {
		t.Errorf("start = %q, want 15:00", c.start)
	}
	if c.duration != 90 {
		t.Errorf("duration = %d, want 90", c.duration)
	}
	if c.durationStr != "90" {
		t.Errorf("durationStr = %q, want 90", c.durationStr)
	}
}

func TestNormalizeFormReportsFieldErrors(t *testing.T) {
	now := ref(t)
	c := &createState{date: "notaday", start: "3pm", durationStr: "30"}
	if err := c.normalize(now); err == "" {
		t.Fatal("expected a date error")
	}
	c = &createState{date: "tmr", start: "99", durationStr: "30"}
	if err := c.normalize(now); err == "" {
		t.Fatal("expected a time error")
	}
}
