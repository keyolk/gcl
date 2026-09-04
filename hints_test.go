package main

import (
	"strings"
	"testing"
	"time"
)

// hintModel is a list-view model at a given width, with the given events.
func hintModel(width int, evs ...Event) model {
	return model{
		width: width, height: 30, view: viewList, jumpUnit: "day",
		calendar: "me@x.com",
		anchor:   time.Date(2099, 9, 4, 0, 0, 0, 0, time.Local),
		events:   evs,
	}
}

// hintEvent is a timed event far enough in the future that "active now" (which
// outranks other row state) can never fire and make an assertion time-dependent.
func hintEvent(atts, links []string) Event {
	s := time.Date(2099, 9, 4, 10, 0, 0, 0, time.Local)
	return Event{
		ID: "e1", Calendar: "me@x.com", Title: "Standup",
		StartDate: s, StartAt: s, EndAt: s.Add(time.Hour),
		StartTime: "10:00", EndTime: "11:00",
		Attendees: atts, Links: links,
	}
}

func TestHintBarNeverExceedsTheWindow(t *testing.T) {
	// The old bar named every key the app has — 238 columns — so it was
	// truncated at EVERY width, including 200. This is the regression.
	ev := hintEvent([]string{"a@x.com"}, []string{"https://zoom.us/j/1"})
	for _, w := range []int{24, 40, 60, 80, 100, 120, 160, 200, 300} {
		m := hintModel(w, ev)
		got := stripANSI(m.shortcutHint())
		if n := len([]rune(got)); n > w {
			t.Errorf("width %d: hint is %d columns: %q", w, n, got)
		}
	}
}

func TestHintBarAlwaysKeepsTheEscapeKeys(t *testing.T) {
	// `?` and `q` fell off the right edge of the old bar at every width — the
	// two keys a lost user needs most were the two reliably hidden.
	ev := hintEvent([]string{"a@x.com"}, []string{"https://z/1"})
	for _, w := range []int{16, 24, 40, 80, 200} {
		got := stripANSI(hintModel(w, ev).shortcutHint())
		if !strings.Contains(got, "? help") || !strings.Contains(got, "q quit") {
			t.Errorf("width %d dropped an escape key: %q", w, got)
		}
	}
}

func TestHintBarOmitsKeysWithNothingToActOn(t *testing.T) {
	// Half the bar used to advertise event actions over an empty calendar.
	empty := stripANSI(hintModel(200).shortcutHint())
	for _, gone := range []string{"E edit", "X del", "y copy", "ret open", "move/resize"} {
		if strings.Contains(empty, gone) {
			t.Errorf("empty calendar still offers %q: %q", gone, empty)
		}
	}
	// With an event selected they come back.
	withEv := stripANSI(hintModel(200, hintEvent(nil, nil)).shortcutHint())
	for _, want := range []string{"ret open", "E edit", "X del"} {
		if !strings.Contains(withEv, want) {
			t.Errorf("a selected event does not offer %q: %q", want, withEv)
		}
	}
}

func TestHintBarOmitsAttendeeAndLinkKeysWhenTheEventHasNeither(t *testing.T) {
	// A and L are the two keys most often dead: most events have neither.
	plain := stripANSI(hintModel(200, hintEvent(nil, nil)).shortcutHint())
	if strings.Contains(plain, "A attendees") {
		t.Errorf("an event with no attendees still offers A: %q", plain)
	}
	if strings.Contains(plain, "L links") {
		t.Errorf("an event with no links still offers L: %q", plain)
	}

	rich := stripANSI(hintModel(200, hintEvent([]string{"a@x.com"}, []string{"https://z/1"})).shortcutHint())
	if !strings.Contains(rich, "A attendees") || !strings.Contains(rich, "L links") {
		t.Errorf("an event with attendees and links does not offer them: %q", rich)
	}
}

func TestHintBarOffersUndoOnlyWhenThereIsSomethingToUndo(t *testing.T) {
	m := hintModel(200, hintEvent(nil, nil))
	if got := stripANSI(m.shortcutHint()); strings.Contains(got, "u undo") {
		t.Errorf("undo offered with nothing to undo: %q", got)
	}
	m.lastUndo = &undoEntry{kind: undoCreate, label: "x"}
	if got := stripANSI(m.shortcutHint()); !strings.Contains(got, "u undo") {
		t.Errorf("undo not offered after an undoable action: %q", got)
	}
}

func TestHintBarDropsWholeHintsRatherThanSlicingOne(t *testing.T) {
	// A hint cut to "E ed" teaches nothing and looks broken, so the fit loop
	// removes whole entries. Every "key label" pair that survives is intact.
	got := stripANSI(hintModel(70, hintEvent([]string{"a@x.com"}, nil)).shortcutHint())
	if strings.HasSuffix(strings.TrimSpace(got), "-") {
		t.Errorf("bar ends mid-hint: %q", got)
	}
	for _, pair := range []string{"j/k select", "? help", "q quit"} {
		if strings.Contains(got, strings.Fields(pair)[0]+" ") &&
			!strings.Contains(got, pair) {
			t.Errorf("hint %q was rendered partially: %q", pair, got)
		}
	}
}

func TestHintBarUsesNoAmbiguousWidthSeparators(t *testing.T) {
	// `·` (U+00B7) and `│` (U+2502) render two cells where East-Asian-ambiguous
	// glyphs are wide, and a separator repeated once per hint would put the real
	// width far past what the fit loop measured.
	got := stripANSI(hintModel(200, hintEvent([]string{"a@x.com"}, []string{"https://z/1"})).shortcutHint())
	for _, r := range got {
		if r >= 0x80 && unstableWidth(r) {
			t.Errorf("hint bar contains ambiguous-width rune U+%04X (%c): %q", r, r, got)
		}
	}
}

func TestUnsavedBarKeepsWhatIsBeingSaved(t *testing.T) {
	// `s` writes to other people's calendars. A bar naming `s SAVE` without
	// saying what it saves is worse than no bar, so the context is pinned and
	// survives even when every ordinary hint has been dropped.
	m := hintModel(200, hintEvent(nil, nil))
	mm, _, _ := m.handleQuickAction(")")
	staged := mm.(model)
	for _, w := range []int{40, 60, 100, 200} {
		staged.width = w
		got := stripANSI(staged.shortcutHint())
		if !strings.Contains(got, "UNSAVED") || !strings.Contains(got, "+15m") {
			t.Errorf("width %d dropped what is unsaved: %q", w, got)
		}
		if !strings.Contains(got, "s SAVE") || !strings.Contains(got, "esc discard") {
			t.Errorf("width %d dropped the keys that resolve it: %q", w, got)
		}
	}
}

func TestYankBarKeepsTheCancelKey(t *testing.T) {
	// `esc` is how you get out of a prefix you did not mean to start; a plain
	// string would have truncated exactly that on a narrow pane.
	m := hintModel(60, hintEvent(nil, nil))
	m.yankPending = true
	got := stripANSI(m.shortcutHint())
	if !strings.Contains(got, "esc cancel") {
		t.Errorf("yank bar dropped its cancel key: %q", got)
	}
}

func TestHintBarRendersWithoutAWindowSize(t *testing.T) {
	// A model that has not had its first WindowSizeMsg has width 0. Blanking
	// the bar there would hide every key at the moment a user knows least.
	m := model{view: viewList, jumpUnit: "day", calendar: "me@x.com"}
	got := stripANSI(m.shortcutHint())
	if !strings.Contains(got, "? help") || !strings.Contains(got, "q quit") {
		t.Errorf("zero-width bar lost its escape keys: %q", got)
	}
}

func TestModalHintsAreFittedToo(t *testing.T) {
	// The modal bars are short, but a narrow pane must still drop whole hints
	// rather than slice one — and must keep the key that closes the modal.
	for _, mode := range []inputMode{modeSearch, modeCalendarPicker, modeCreate,
		modeConfirmDelete, modeOverlayPicker, modeFindTime} {
		m := hintModel(50, hintEvent(nil, nil))
		m.mode = mode
		got := stripANSI(m.shortcutHint())
		if n := len([]rune(got)); n > 50 {
			t.Errorf("mode %v: hint is %d columns: %q", mode, n, got)
		}
		if !strings.Contains(got, "esc") && !strings.Contains(got, "n/esc") {
			t.Errorf("mode %v: no way out is shown: %q", mode, got)
		}
	}
}
