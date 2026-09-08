package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// isQuit reports whether a returned command is tea.Quit, by running it and
// checking for the QuitMsg. tea.Quit is a plain func, so it cannot be compared
// directly.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestNormalizeCJKKeyMapsJamoByPhysicalPosition(t *testing.T) {
	cases := map[string]string{
		"ㅂ": "q", "ㅁ": "a", "ㅋ": "z", "ㅓ": "j", "ㅏ": "k",
		"ㅃ": "Q", "ㄲ": "R",
	}
	for jamo, want := range cases {
		got := normalizeCJKKey(keyMsg(jamo)).String()
		if got != want {
			t.Errorf("normalizeCJKKey(%q) = %q, want %q", jamo, got, want)
		}
	}
}

func TestNormalizeCJKKeyLeavesEverythingElseAlone(t *testing.T) {
	// Latin keys, digits, and composed syllables pass through: a composed
	// syllable only reaches the TUI as committed text, never as a shortcut.
	for _, k := range []string{"q", "R", "0", "가"} {
		if got := normalizeCJKKey(keyMsg(k)).String(); got != k {
			t.Errorf("normalizeCJKKey(%q) = %q, want it unchanged", k, got)
		}
	}
	// Non-rune keys carry no jamo to map.
	for _, in := range []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyCtrlC}, {Type: tea.KeyEscape}} {
		if got := normalizeCJKKey(in); got.String() != in.String() {
			t.Errorf("normalizeCJKKey(%q) = %q, want it unchanged", in.String(), got.String())
		}
	}
}

func TestNormalizeCJKKeyLeavesPasteAndAltAlone(t *testing.T) {
	paste := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'ㅂ'}, Paste: true}
	if got := normalizeCJKKey(paste); got.String() != paste.String() {
		t.Errorf("pasted jamo was rewritten to %q", got.String())
	}
	alt := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'ㅂ'}, Alt: true}
	if got := normalizeCJKKey(alt); got.String() != alt.String() {
		t.Errorf("alt chord was rewritten to %q", got.String())
	}
}

// --- through the real dispatcher --------------------------------------------

// Under a Korean input source the navigation keys arrive as jamo. They have to
// keep working, or the agenda is unusable until the input source is switched.
func TestHangulNavigatesTheAgendaLikeLatin(t *testing.T) {
	m := model{view: viewList, events: []Event{
		{Title: "first"}, {Title: "second"},
	}}
	// `ㅓ` is the physical `j` key.
	mm, _ := m.handleKey(keyMsg("ㅓ"))
	if got := mm.(model).selected; got != 1 {
		t.Fatalf("selected = %d after ㅓ; want 1", got)
	}
	// `ㅏ` is the physical `k` key.
	back, _ := mm.(model).handleKey(keyMsg("ㅏ"))
	if got := back.(model).selected; got != 0 {
		t.Fatalf("selected = %d after ㅏ; want 0", got)
	}
}

func TestHangulQuitsLikeLatinQ(t *testing.T) {
	// `ㅂ` sits on the physical `q` key.
	_, cmd := model{view: viewList}.handleKey(keyMsg("ㅂ"))
	if !isQuit(cmd) {
		t.Fatal("ㅂ (physical q) did not quit")
	}
}

// A jamo typed into a text field is the intended input, so normalization must
// not reach it — Korean event titles and searches would be impossible.
func TestTextInputModesKeepHangulVerbatim(t *testing.T) {
	t.Run("search box", func(t *testing.T) {
		mm, _ := model{mode: modeSearch}.handleKey(keyMsg("ㅂ"))
		if got := mm.(model).input; got != "ㅂ" {
			t.Fatalf("search input = %q, want the jamo verbatim", got)
		}
	})
	t.Run("picker", func(t *testing.T) {
		mm, _ := model{mode: modeLinkPicker}.handleKey(keyMsg("ㅁ"))
		if got := mm.(model).input; got != "ㅁ" {
			t.Fatalf("picker input = %q, want the jamo verbatim", got)
		}
	})
	t.Run("find-time people step", func(t *testing.T) {
		m := model{mode: modeFindTime, find: findState{step: findPeople}}
		mm, _ := m.handleKey(keyMsg("ㅂ"))
		if got := mm.(model).find.input; got != "ㅂ" {
			t.Fatalf("find input = %q, want the jamo verbatim", got)
		}
	})
}

// …but the find-time SLOT step is all shortcuts, so it must normalize even
// though the surrounding mode is the same one whose first step is a text box.
func TestFindTimeSlotStepNormalizesHangul(t *testing.T) {
	m := model{mode: modeFindTime, find: findState{
		step:  findSlotList,
		slots: []candidateSlot{{}, {}},
	}}
	// `ㅓ` is the physical `j` key, which moves the slot cursor.
	mm, _ := m.handleKey(keyMsg("ㅓ"))
	if got := mm.(model).find.slotIdx; got != 1 {
		t.Fatalf("slotIdx = %d after ㅓ; want 1", got)
	}
}

// Text fields used to test `len(key) == 1`, which is a BYTE length, so every
// multi-byte character was silently dropped -- a Korean event title or search
// term could not be typed at all. Composed syllables are the real-world case:
// a Korean IME commits `가`, not the jamo a shortcut sees.
func TestTextFieldsAcceptMultiByteCharacters(t *testing.T) {
	t.Run("search box", func(t *testing.T) {
		mm, _ := model{mode: modeSearch}.handleKey(keyMsg("가"))
		if got := mm.(model).input; got != "가" {
			t.Fatalf("search input = %q, want 가", got)
		}
	})
	t.Run("create form", func(t *testing.T) {
		m := model{mode: modeCreate, create: createState{step: stepTitle, editingField: true}}
		mm, _ := m.handleKey(keyMsg("가"))
		if got := mm.(model).create.title; got != "가" {
			t.Fatalf("title = %q, want 가", got)
		}
	})
	t.Run("overlay picker", func(t *testing.T) {
		mm, _ := model{mode: modeOverlayPicker}.handleKey(keyMsg("가"))
		if got := mm.(model).overlay.input; got != "가" {
			t.Fatalf("overlay input = %q, want 가", got)
		}
	})
}

// …and the same fields must still reject named chords, which arrive as
// multi-rune strings and are not typed characters.
func TestTextFieldsRejectNamedChords(t *testing.T) {
	m := model{mode: modeSearch, input: "x"}
	for _, in := range []tea.KeyMsg{{Type: tea.KeyCtrlN}, {Type: tea.KeyTab}, {Type: tea.KeyUp}} {
		mm, _ := m.handleKey(in)
		if got := mm.(model).input; got != "x" {
			t.Fatalf("%q was appended to the search box: input = %q", in.String(), got)
		}
	}
}

// --- ctrl+c ------------------------------------------------------------------

func TestCtrlCQuitsFromNormalMode(t *testing.T) {
	_, cmd := model{view: viewList}.handleKey(keyMsg("ctrl+c"))
	if !isQuit(cmd) {
		t.Fatal("ctrl+c did not quit")
	}
}

// The form and the overlays swallow ordinary keys, so ctrl+c must be handled
// above them or there is no exit that does not depend on their own bindings.
func TestCtrlCQuitsFromEveryMode(t *testing.T) {
	for _, mode := range []inputMode{
		modeCreate, modeSearch, modeFindTime, modeOverlayPicker,
		modeCalendarPicker, modeLinkPicker, modeAttendeePicker,
		modeHelp, modeFormHelp, modeConfirmSubmit, modeConfirmDelete,
	} {
		_, cmd := model{mode: mode}.handleKey(keyMsg("ctrl+c"))
		if !isQuit(cmd) {
			t.Errorf("ctrl+c did not quit from mode %v", mode)
		}
	}
}

// ctrl+c is the abort, so unlike `q` it does not stop at the unsaved-nudge
// warning. The nudge is staged in memory, so nothing on the calendar changes.
func TestCtrlCQuitsPastAnUnsavedNudge(t *testing.T) {
	m := model{view: viewList, pending: &pendingShift{title: "standup"}}

	// `q` refuses and explains.
	same, cmd := m.handleKey(keyMsg("q"))
	if isQuit(cmd) {
		t.Fatal("q quit with an unsaved nudge staged; it must warn first")
	}
	if same.(model).status == "" {
		t.Fatal("q left no explanation for refusing to quit")
	}

	// ctrl+c does not.
	_, cmd = m.handleKey(keyMsg("ctrl+c"))
	if !isQuit(cmd) {
		t.Fatal("ctrl+c did not quit past the unsaved-nudge warning")
	}
}
