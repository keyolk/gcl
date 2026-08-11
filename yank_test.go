package main

import (
	"strings"
	"testing"
	"time"
)

func yankTestModel() model {
	start := time.Date(2026, 8, 11, 9, 30, 0, 0, time.FixedZone("KST", 9*60*60))
	return model{
		calendar: "me",
		view:     viewList,
		events: []Event{{
			ID:          "evt1",
			Title:       "Release review",
			StartDate:   start,
			StartTime:   "09:30",
			StartAt:     start,
			EndAt:       start.Add(45 * time.Minute),
			Location:    "Zoom",
			Description: "Review the rollout plan.",
			HTMLLink:    "https://calendar.google.com/calendar/event?eid=evt1",
		}},
	}
}

func TestYankValueCopiesEventFields(t *testing.T) {
	oldSettings := settings
	settings.timezones = []tzOption{{label: "UTC", zone: "UTC"}}
	t.Cleanup(func() { settings = oldSettings })

	m := yankTestModel()
	tests := []struct {
		key   string
		label string
		want  string
	}{
		{key: "u", label: "calendar URL", want: "https://calendar.google.com/calendar/event?eid=evt1"},
		{key: "s", label: "start timestamp", want: "2026-08-11T00:30:00Z"},
		{key: "e", label: "end timestamp", want: "2026-08-11T01:15:00Z"},
		{key: "d", label: "description", want: "Review the rollout plan."},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			label, got, ok := m.yankValue(tt.key)
			if !ok {
				t.Fatalf("yankValue(%q) was not handled", tt.key)
			}
			if label != tt.label || got != tt.want {
				t.Errorf("yankValue(%q) = (%q, %q), want (%q, %q)", tt.key, label, got, tt.label, tt.want)
			}
		})
	}
}

func TestYankSummaryIncludesShareableEventDetails(t *testing.T) {
	oldSettings := settings
	settings.timezones = []tzOption{{label: "UTC", zone: "UTC"}}
	t.Cleanup(func() { settings = oldSettings })

	m := yankTestModel()
	label, got, ok := m.yankValue("y")
	if !ok || label != "event summary" {
		t.Fatalf("yy = (%q, handled=%v), want event summary", label, ok)
	}
	for _, want := range []string{
		"Release review",
		"Time: 2026-08-11T00:30:00Z — 2026-08-11T01:15:00Z",
		"Location: Zoom",
		"Description: Review the rollout plan.",
		"URL: https://calendar.google.com/calendar/event?eid=evt1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("yy summary missing %q:\n%s", want, got)
		}
	}
}

func TestYankAllDayEndUsesLastCoveredDate(t *testing.T) {
	m := model{
		view: viewList,
		events: []Event{{
			ID:        "window",
			Title:     "Maintenance",
			StartDate: time.Date(2026, 8, 11, 0, 0, 0, 0, time.Local),
			EndDate:   time.Date(2026, 8, 14, 0, 0, 0, 0, time.Local),
		}},
	}
	if got := m.yankTimestamp(&m.events[0], true); got != "2026-08-11" {
		t.Errorf("all-day start = %q, want 2026-08-11", got)
	}
	if got := m.yankTimestamp(&m.events[0], false); got != "2026-08-13" {
		t.Errorf("all-day end = %q, want last covered date 2026-08-13", got)
	}
}

func TestYankUsesStagedTimesWithoutSaving(t *testing.T) {
	oldSettings := settings
	settings.timezones = []tzOption{{label: "UTC", zone: "UTC"}}
	t.Cleanup(func() { settings = oldSettings })

	m := yankTestModel()
	ev := &m.events[0]
	m.pending = &pendingShift{
		calendar:   m.calendar,
		eventID:    ev.ID,
		title:      ev.Title,
		origStart:  ev.StartAt,
		origEnd:    ev.EndAt,
		startDelta: 30 * time.Minute,
		endDelta:   30 * time.Minute,
	}

	started, cmd := m.handleKey(keyMsg("y"))
	m = started.(model)
	if cmd != nil || !m.yankPending {
		t.Fatalf("first y should only start the prefix; pending=%v cmd=%v", m.yankPending, cmd != nil)
	}
	copied, cmd := m.handleKey(keyMsg("s"))
	m = copied.(model)
	if cmd == nil {
		t.Fatal("ys should construct a clipboard command")
	}
	if m.yankPending {
		t.Error("ys should clear the yank prefix")
	}
	if m.pending == nil || m.pending.saving {
		t.Error("ys must copy the staged start without saving it")
	}
	if got := m.yankTimestamp(ev, true); got != "2026-08-11T01:00:00Z" {
		t.Errorf("staged start = %q, want 2026-08-11T01:00:00Z", got)
	}
	if summary := m.yankSummary(ev); !strings.Contains(summary, "(UNSAVED)") {
		t.Errorf("staged summary must be marked UNSAVED:\n%s", summary)
	}
}

func TestYankPrefixCancelAndUnknownKeyAreConsumed(t *testing.T) {
	m := yankTestModel()
	m.selected = 0

	started, _ := m.handleKey(keyMsg("y"))
	m = started.(model)
	cancelled, cmd := m.handleKey(keyMsg("esc"))
	m = cancelled.(model)
	if cmd != nil || m.yankPending || m.status != "yank cancelled" {
		t.Errorf("y esc = pending=%v status=%q cmd=%v", m.yankPending, m.status, cmd != nil)
	}

	started, _ = m.handleKey(keyMsg("y"))
	m = started.(model)
	unknown, cmd := m.handleKey(keyMsg("j"))
	m = unknown.(model)
	if cmd != nil || m.yankPending {
		t.Errorf("unknown yank key should be consumed and clear prefix; pending=%v cmd=%v", m.yankPending, cmd != nil)
	}
	if m.selected != 0 {
		t.Errorf("yj moved selection to %d; the suffix must not fall through", m.selected)
	}
	if m.status != "unknown yank key: j" {
		t.Errorf("status = %q", m.status)
	}
}

func TestYankPrefixHandlesSelectionDisappearing(t *testing.T) {
	m := model{yankPending: true}
	updated, cmd := m.handleKey(keyMsg("u"))
	got := updated.(model)
	if cmd != nil || got.yankPending {
		t.Errorf("missing selection should clear prefix without a command; pending=%v cmd=%v", got.yankPending, cmd != nil)
	}
	if got.status != "no event selected" {
		t.Errorf("status = %q, want no event selected", got.status)
	}
}

func TestClipboardResultUpdatesStatus(t *testing.T) {
	m := model{}
	updated, cmd := m.Update(clipboardCopiedMsg{label: "description"})
	if cmd != nil || updated.(model).status != "copied description" {
		t.Errorf("success status = %q, cmd=%v", updated.(model).status, cmd != nil)
	}
}
