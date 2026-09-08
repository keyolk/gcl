package main

import tea "github.com/charmbracelet/bubbletea"

// keymap.go normalizes keystrokes that arrive under a CJK input source.
//
// With the OS input source set to Korean, pressing the `q` key emits `ㅂ`, so
// every single-letter shortcut silently stops working until the user switches
// back to English. normalizeCJKKey maps each jamo back to the Latin key at the
// same physical position on a US QWERTY keyboard — the same idea as vim's
// `langmap`.

// hangulToLatin maps jamo produced by the 2-set (두벌식) Korean layout to the
// Latin key at the same physical position on a US QWERTY keyboard.
//
// Shifted jamo (double consonants, ㅒ/ㅖ) map to the uppercase Latin letter,
// which is what the same physical chord would have produced in English.
var hangulToLatin = map[rune]rune{
	// unshifted row
	'ㅂ': 'q', 'ㅈ': 'w', 'ㄷ': 'e', 'ㄱ': 'r', 'ㅅ': 't',
	'ㅛ': 'y', 'ㅕ': 'u', 'ㅑ': 'i', 'ㅐ': 'o', 'ㅔ': 'p',
	'ㅁ': 'a', 'ㄴ': 's', 'ㅇ': 'd', 'ㄹ': 'f', 'ㅎ': 'g',
	'ㅗ': 'h', 'ㅓ': 'j', 'ㅏ': 'k', 'ㅣ': 'l',
	'ㅋ': 'z', 'ㅌ': 'x', 'ㅊ': 'c', 'ㅍ': 'v', 'ㅠ': 'b',
	'ㅜ': 'n', 'ㅡ': 'm',
	// shifted row
	'ㅃ': 'Q', 'ㅉ': 'W', 'ㄸ': 'E', 'ㄲ': 'R', 'ㅆ': 'T',
	'ㅒ': 'O', 'ㅖ': 'P',
}

// normalizeCJKKey rewrites a single-jamo key message to the Latin key at the
// same physical position, so shortcuts fire under a CJK input source.
//
// The message is returned unchanged when it is not a single rune, is a paste,
// or carries alt — those already arrive as Latin, and rewriting a chord would
// break bindings like alt+f. Ctrl chords never reach here as KeyRunes.
//
// Callers must only apply this outside text entry, so Korean can still be typed
// into the search box, the pickers, and the create form verbatim.
func normalizeCJKKey(msg tea.KeyMsg) tea.KeyMsg {
	if msg.Type != tea.KeyRunes || msg.Paste || msg.Alt || len(msg.Runes) != 1 {
		return msg
	}
	latin, ok := hangulToLatin[msg.Runes[0]]
	if !ok {
		return msg
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{latin}}
}

// isTextInputMode reports whether the current mode reads keys as text being
// typed rather than as shortcuts. Used to gate CJK normalization: in these
// modes the jamo IS the intended input.
//
// modeFindTime is split: its people step is a search box, but its slot step is
// all shortcuts (j/k/d/w/H/R), so the mode alone cannot decide — see
// isInTextInput.
func (m model) isInTextInput() bool {
	switch m.mode {
	case modeCreate, modeSearch,
		modeCalendarPicker, modeLinkPicker, modeAttendeePicker,
		modeOverlayPicker:
		return true
	case modeFindTime:
		return m.find.step == findPeople
	}
	return false
}

// isTypedRune reports whether a key message is a single printable character the
// user typed, and should therefore be appended to a text field.
//
// The obvious `len(key) == 1` is a BYTE length: it silently dropped every
// non-ASCII character, so Korean could not be typed into any field even though
// backspace already decoded runes correctly on the way out. Named chords
// ("enter", "ctrl+n") arrive as multi-rune strings and are excluded by the
// KeyRunes check rather than by length.
func isTypedRune(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyRunes && !msg.Alt && len(msg.Runes) == 1
}
