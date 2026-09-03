package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// busyAt is a small builder for free/busy fixtures: hours are local wall-clock
// on a fixed test day so the assertions read as times, not as instants.
func busyAt(cal string, spans ...[2]int) freeBusyResult {
	r := freeBusyResult{calendar: cal}
	for _, s := range spans {
		r.busy = append(r.busy, busyInterval{
			start: testDay(s[0]),
			end:   testDay(s[1]),
		})
	}
	return r
}

// testDay is 2026-09-03 (a Thursday) at the given local hour.
func testDay(hour int) time.Time {
	return time.Date(2026, 9, 3, hour, 0, 0, 0, time.Local)
}

func testSearch(dur time.Duration, fromHour, toHour int) slotSearch {
	return slotSearch{
		duration:     dur,
		step:         30 * time.Minute,
		from:         testDay(fromHour),
		to:           testDay(toHour),
		dayStartHour: 0,
		dayEndHour:   24,
		loc:          time.Local,
		maxResults:   slotMaxResults,
	}
}

func TestFindSlotsRanksFullyFreeFirst(t *testing.T) {
	// a is free all afternoon; b is busy 13-15. A 14:00 slot is therefore only
	// half-available and must rank below the 15:00 one that suits both.
	results := []freeBusyResult{
		busyAt("a@x.com"),
		busyAt("b@x.com", [2]int{13, 15}),
	}
	slots := findSlots(results, testSearch(time.Hour, 13, 18))
	if len(slots) == 0 {
		t.Fatal("expected slots")
	}
	if !slots[0].allFree() {
		t.Fatalf("expected a fully-free slot first, got busy=%v at %s", slots[0].busy, slots[0].start)
	}
	if got := slots[0].start; !got.Equal(testDay(15)) {
		t.Errorf("first free slot = %s, want 15:00", got.Format("15:04"))
	}
	// The partial slots must survive — the user asked to see them.
	var sawPartial bool
	for _, s := range slots {
		if len(s.busy) > 0 {
			sawPartial = true
			break
		}
	}
	if !sawPartial {
		t.Error("partial slots were dropped; they must stay in the list")
	}
}

func TestFindSlotsTreatsBackToBackAsFree(t *testing.T) {
	// A meeting ending exactly at 14:00 does not block a 14:00 start. Half-open
	// intervals are the whole reason this holds — an inclusive comparison would
	// silently lose every slot that butts up against an existing meeting.
	results := []freeBusyResult{busyAt("a@x.com", [2]int{13, 14})}
	slots := findSlots(results, testSearch(time.Hour, 13, 16))
	for _, s := range slots {
		if s.start.Equal(testDay(14)) {
			if !s.allFree() {
				t.Fatal("14:00 start after a 13-14 meeting should be free")
			}
			return
		}
	}
	t.Fatal("no 14:00 slot produced")
}

func TestFindSlotsExcludesSmallHours(t *testing.T) {
	// The default window starts at 07:00: a 03:00 proposal is never useful, and
	// the point of the day-hour clip is that it never appears.
	search := testSearch(time.Hour, 0, 12)
	search.dayStartHour = 7
	search.dayEndHour = 24
	slots := findSlots([]freeBusyResult{busyAt("a@x.com")}, search)
	if len(slots) == 0 {
		t.Fatal("expected slots")
	}
	for _, s := range slots {
		if h := s.start.In(time.Local).Hour(); h < 7 {
			t.Errorf("slot at %02d:00 is inside the excluded small hours", h)
		}
	}
}

func TestFindSlotsRejectsSlotsSpillingPastDayEnd(t *testing.T) {
	// A 1h meeting starting at 17:30 does not fit an 18:00 cutoff. Checking only
	// the start would book half the meeting outside the window.
	search := testSearch(time.Hour, 16, 23)
	search.dayStartHour = 9
	search.dayEndHour = 18
	slots := findSlots([]freeBusyResult{busyAt("a@x.com")}, search)
	for _, s := range slots {
		if s.end.In(time.Local).Hour() > 18 || (s.end.In(time.Local).Hour() == 18 && s.end.In(time.Local).Minute() > 0) {
			t.Errorf("slot %s-%s runs past the 18:00 cutoff",
				s.start.Format("15:04"), s.end.Format("15:04"))
		}
	}
}

func TestFindSlotsSeparatesUnknownFromBusy(t *testing.T) {
	// A calendar we cannot read must not be reported as free (that would propose
	// a time they may not have) nor as busy (that would hide good slots). It is
	// its own category, and it stays out of the availability denominator.
	results := []freeBusyResult{
		busyAt("a@x.com"),
		{calendar: "opaque@x.com", err: "notFound"},
	}
	slots := findSlots(results, testSearch(time.Hour, 13, 16))
	if len(slots) == 0 {
		t.Fatal("expected slots")
	}
	s := slots[0]
	if len(s.unknown) != 1 || s.unknown[0] != "opaque@x.com" {
		t.Errorf("unknown = %v, want [opaque@x.com]", s.unknown)
	}
	if len(s.free) != 1 || len(s.busy) != 0 {
		t.Errorf("free=%v busy=%v, want the unreadable calendar in neither", s.free, s.busy)
	}
	if !s.allFree() {
		t.Error("a slot with only unknown participants blocked should still count as all-free")
	}
}

func TestFindSlotsCollapsesAdjacentSlotsInOneGap(t *testing.T) {
	// A wide-open afternoon must not fill the list with 13:00, 13:30, 14:00…
	// Each contiguous run of identical availability contributes one entry.
	results := []freeBusyResult{busyAt("a@x.com", [2]int{15, 16})}
	slots := findSlots(results, testSearch(time.Hour, 13, 19))
	var freeStarts []string
	for _, s := range slots {
		if s.allFree() {
			freeStarts = append(freeStarts, s.start.Format("15:04"))
		}
	}
	// Two runs: before the 15-16 meeting and after it. One entry each.
	if len(freeStarts) != 2 {
		t.Errorf("free slot starts = %v, want exactly one per contiguous gap", freeStarts)
	}
}

func TestFindSlotsSkipsWeekendsWhenAsked(t *testing.T) {
	// 2026-09-05 is a Saturday.
	search := slotSearch{
		duration:     time.Hour,
		step:         30 * time.Minute,
		from:         time.Date(2026, 9, 5, 9, 0, 0, 0, time.Local),
		to:           time.Date(2026, 9, 7, 18, 0, 0, 0, time.Local),
		dayStartHour: 9,
		dayEndHour:   18,
		skipWeekends: true,
		loc:          time.Local,
		maxResults:   slotMaxResults,
	}
	slots := findSlots([]freeBusyResult{busyAt("a@x.com")}, search)
	for _, s := range slots {
		switch s.start.In(time.Local).Weekday() {
		case time.Saturday, time.Sunday:
			t.Errorf("weekend slot %s survived skipWeekends", s.start.Format("Mon 15:04"))
		}
	}
	if len(slots) == 0 {
		t.Error("Monday slots should still be produced")
	}
}

func TestAlignUpSnapsToGrid(t *testing.T) {
	// 14:07 with a 30m grid becomes 14:30 — an off-grid proposal reads as a bug
	// even when the arithmetic is right.
	got := alignUp(time.Date(2026, 9, 3, 14, 7, 0, 0, time.Local), 30*time.Minute, time.Local)
	if want := time.Date(2026, 9, 3, 14, 30, 0, 0, time.Local); !got.Equal(want) {
		t.Errorf("alignUp = %s, want %s", got.Format("15:04"), want.Format("15:04"))
	}
	// Already on the grid: unchanged.
	on := time.Date(2026, 9, 3, 14, 30, 0, 0, time.Local)
	if got := alignUp(on, 30*time.Minute, time.Local); !got.Equal(on) {
		t.Errorf("alignUp moved an already-aligned time to %s", got.Format("15:04"))
	}
}

func TestNextSlotDurationCycles(t *testing.T) {
	if got := nextSlotDuration(30 * time.Minute); got != 45*time.Minute {
		t.Errorf("30m -> %s, want 45m", got)
	}
	// Past the last option it wraps to the first rather than dead-ending.
	if got := nextSlotDuration(120 * time.Minute); got != 15*time.Minute {
		t.Errorf("120m -> %s, want 15m", got)
	}
	// An unlisted config duration enters at the next larger option.
	if got := nextSlotDuration(20 * time.Minute); got != 30*time.Minute {
		t.Errorf("20m -> %s, want 30m", got)
	}
}

func TestFindTimeKeyOpensPeoplePicker(t *testing.T) {
	m := model{width: 100, height: 40, calendar: "me@x.com"}
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	got := updated.(model)
	if got.mode != modeFindTime {
		t.Fatalf("mode = %v, want modeFindTime", got.mode)
	}
	if got.find.step != findPeople {
		t.Errorf("step = %v, want findPeople", got.find.step)
	}
}

func TestFindPeopleSpaceTogglesAndEnterNeedsSomeone(t *testing.T) {
	m := model{width: 100, height: 40, mode: modeFindTime}
	m.find = findState{
		step:     findPeople,
		cands:    []pickerItem{{label: "a@x.com", value: "a@x.com"}},
		selected: map[string]bool{},
	}
	// Enter with nobody picked must refuse rather than query for zero people.
	updated, cmd := m.handleFindKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(model)
	if cmd != nil {
		t.Error("Enter with no participants should not start a search")
	}
	if got.find.err == "" {
		t.Error("expected an error message explaining that someone must be picked")
	}

	got.find.err = ""
	updated, _ = got.handleFindKey(tea.KeyMsg{Type: tea.KeySpace})
	got = updated.(model)
	if !got.find.selected["a@x.com"] {
		t.Fatal("space should toggle the highlighted candidate on")
	}
	updated, _ = got.handleFindKey(tea.KeyMsg{Type: tea.KeySpace})
	if updated.(model).find.selected["a@x.com"] {
		t.Error("space should toggle the same candidate back off")
	}
}

func TestFindPeopleAddsTypedEmailWithNoMatch(t *testing.T) {
	// Someone who never appears on a loaded event still has to be invitable.
	m := model{width: 100, height: 40, mode: modeFindTime}
	m.find = findState{step: findPeople, input: "new@x.com", selected: map[string]bool{}}
	updated, _ := m.handleFindKey(tea.KeyMsg{Type: tea.KeySpace})
	got := updated.(model)
	if !got.find.selected["new@x.com"] {
		t.Fatalf("typed address was not added: %v", got.find.selected)
	}
	if got.find.input != "" {
		t.Errorf("input should clear after adding, got %q", got.find.input)
	}
}

func TestFindSlotEnterPrefillsCreateForm(t *testing.T) {
	m := model{width: 100, height: 40, mode: modeFindTime}
	slot := candidateSlot{
		start: testDay(14),
		end:   testDay(15),
		free:  []string{"a@x.com", "b@x.com"},
	}
	m.find = findState{
		step:     findSlotList,
		selected: map[string]bool{"a@x.com": true, "b@x.com": true},
		slots:    []candidateSlot{slot},
	}
	updated, _ := m.handleFindKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(model)
	if got.mode != modeCreate {
		t.Fatalf("mode = %v, want modeCreate", got.mode)
	}
	c := got.create
	if c.date != "2026-09-03" || c.start != "14:00" {
		t.Errorf("prefilled date/start = %q %q, want 2026-09-03 14:00", c.date, c.start)
	}
	if c.duration != 60 {
		t.Errorf("prefilled duration = %d, want 60", c.duration)
	}
	if !c.selected["a@x.com"] || !c.selected["b@x.com"] {
		t.Errorf("participants not carried into the form: %v", c.selected)
	}
	// Nothing is created yet: the form is where the title and the invitation
	// warning live, so Enter here must not have submitted anything.
	if c.title != "" {
		t.Errorf("title should be left empty for the organizer, got %q", c.title)
	}
	if c.submitting {
		t.Error("prefill must not submit")
	}
}

func TestFindSlotEscGoesBackToPeople(t *testing.T) {
	// esc in the slot list is a step back, not an exit — adjusting who is coming
	// is the usual fix for a disappointing slot list.
	m := model{width: 100, height: 40, mode: modeFindTime}
	m.find = findState{step: findSlotList, selected: map[string]bool{"a@x.com": true}}
	updated, _ := m.handleFindKey(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(model)
	if got.mode != modeFindTime {
		t.Errorf("esc from the slot list should stay in the flow, got mode %v", got.mode)
	}
	if got.find.step != findPeople {
		t.Errorf("step = %v, want findPeople", got.find.step)
	}
}

func TestFindSlotHourToggleWidensTheDay(t *testing.T) {
	m := model{width: 100, height: 40, mode: modeFindTime}
	m.find = findState{
		step:     findSlotList,
		selected: map[string]bool{"a@x.com": true},
		search:   slotSearch{dayStartHour: 7, dayEndHour: 24, duration: time.Hour, step: 30 * time.Minute},
	}
	updated, _ := m.handleFindKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")})
	got := updated.(model)
	if got.find.search.dayStartHour != 0 || got.find.search.dayEndHour != 24 {
		t.Errorf("H should open the day fully, got %d-%d",
			got.find.search.dayStartHour, got.find.search.dayEndHour)
	}
	// And back again to the configured hours.
	updated, _ = got.handleFindKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")})
	if h := updated.(model).find.search.dayStartHour; h != settings.slotDayStart {
		t.Errorf("second H should restore the configured start hour, got %d", h)
	}
}

func TestFindTimeKeysDoNotLeakToTheSchedule(t *testing.T) {
	// The flow owns its keys while open: `d`, `w` and `g` mean length/weekends/
	// top-of-list here, and must NOT also set the agenda's day/week jump step.
	m := model{width: 100, height: 40, mode: modeFindTime, jumpUnit: "day"}
	m.find = findState{
		step:     findSlotList,
		selected: map[string]bool{"a@x.com": true},
		search:   slotSearch{dayStartHour: 7, dayEndHour: 24, duration: time.Hour, step: 30 * time.Minute},
	}
	for _, key := range []string{"d", "w", "g"} {
		updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		got := updated.(model)
		if got.jumpUnit != "day" {
			t.Errorf("%q changed the agenda jump step to %q while the find overlay was open", key, got.jumpUnit)
		}
		if got.mode != modeFindTime {
			t.Errorf("%q left the find overlay (mode %v)", key, got.mode)
		}
	}
}

func TestSlotRowDenominatorComesFromTheSlot(t *testing.T) {
	// The badge counts the slot's own participants, never a separately-tracked
	// total. An earlier version took the group size as an argument, and when the
	// two drifted apart it rendered "✓3/2" — a badge that cannot be true.
	m := model{width: 100, height: 40}
	slot := candidateSlot{
		start: testDay(14), end: testDay(15),
		free:    []string{"a", "b", "c"},
		unknown: []string{"d"},
	}
	got := stripANSI(m.slotRow(slot, false, 60))
	if !strings.Contains(got, "3/3") {
		t.Errorf("row = %q, want 3/3 derived from the slot itself", got)
	}
}

func TestSlotRowMarksPartialAvailability(t *testing.T) {
	m := model{width: 100, height: 40}
	full := candidateSlot{start: testDay(14), end: testDay(15), free: []string{"a", "b"}}
	partial := candidateSlot{start: testDay(16), end: testDay(17), free: []string{"a"}, busy: []string{"b"}}
	if got := stripANSI(m.slotRow(full, false, 60)); !strings.Contains(got, "✓2/2") {
		t.Errorf("full slot row = %q, want a ✓2/2 badge", got)
	}
	if got := stripANSI(m.slotRow(partial, false, 60)); !strings.Contains(got, "1/2") || strings.Contains(got, "✓") {
		t.Errorf("partial slot row = %q, want a plain 1/2 badge with no check", got)
	}
}

func TestSlotRowExcludesUnknownFromDenominator(t *testing.T) {
	// Claiming "2/3" when the third calendar is simply unreadable would report a
	// conflict that was never observed. The count is over readable calendars,
	// with the unknowns shown separately as "+N?".
	m := model{width: 100, height: 40}
	slot := candidateSlot{
		start: testDay(14), end: testDay(15),
		free:    []string{"a", "b"},
		unknown: []string{"c"},
	}
	got := stripANSI(m.slotRow(slot, false, 60))
	if !strings.Contains(got, "2/2") {
		t.Errorf("row = %q, want 2/2 (unknown excluded from the denominator)", got)
	}
	if !strings.Contains(got, "+1?") {
		t.Errorf("row = %q, want the unknown participant flagged as +1?", got)
	}
}
