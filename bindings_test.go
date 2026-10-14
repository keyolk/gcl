package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/keys"
)

// uppercaseOnly are the actions whose only key is an uppercase letter a 2-set
// Korean input source cannot type (shift+ㅁ is still ㅁ, which maps to `a`).
// They predate the check and have no free lowercase key that reads well; every
// other action has a reachable key. A new uppercase-only binding fails the
// test below instead of a Korean-layout user.
var uppercaseOnly = []string{"attendees", "links", "new", "dup", "view.month", "slots.hours"}

func TestKeymapHasNoConflicts(t *testing.T) {
	km := baseKeymap
	byAction := map[string][]string{}
	for _, bnd := range km.Bindings() {
		byAction[bnd.Action] = append(byAction[bnd.Action], bnd.Keys...)
	}
	for _, c := range km.Conflicts(allStacks...) {
		if c.Kind == keys.Unreachable {
			// An action with another reachable key is still usable; only an
			// action left with no reachable key is a problem.
			a := c.Actions[0]
			if slices.ContainsFunc(byAction[a], keys.ReachableUnderCJK) || slices.Contains(uppercaseOnly, a) {
				continue
			}
		}
		t.Error(c)
	}
}

// keyMsgFor builds the tea.KeyMsg whose String() is key.
func keyMsgFor(t *testing.T, key string) tea.KeyMsg {
	t.Helper()
	named := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
		"backspace": tea.KeyBackspace, "up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft,
		"right": tea.KeyRight, "home": tea.KeyHome, "end": tea.KeyEnd, " ": tea.KeySpace,
		"ctrl+c": tea.KeyCtrlC, "ctrl+n": tea.KeyCtrlN, "ctrl+p": tea.KeyCtrlP, "ctrl+j": tea.KeyCtrlJ,
		"ctrl+k": tea.KeyCtrlK, "ctrl+h": tea.KeyCtrlH, "ctrl+y": tea.KeyCtrlY,
	}
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	if kt, ok := named[key]; ok {
		msg = tea.KeyMsg{Type: kt}
	}
	if msg.String() != key {
		t.Fatalf("test helper cannot build key %q (got %q)", key, msg.String())
	}
	return msg
}

// Every action the help pages or the bar can name must be reachable: some key
// of it, in some stack the app can be in, resolves to it.
func TestEveryActionInHelpIsReachable(t *testing.T) {
	km := baseKeymap
	var actions []string
	for _, secs := range [][]keys.Section{
		km.Help(keys.Opts{}, helpStacks...),
		km.Help(keys.Opts{}, formHelpStacks...),
		km.Table(keys.Opts{}),
	} {
		for _, s := range secs {
			for _, e := range s.Entries {
				if !slices.Contains(actions, e.Action) {
					actions = append(actions, e.Action)
				}
			}
		}
	}
	if len(actions) < 50 {
		t.Fatalf("only %d actions documented; the table did not load", len(actions))
	}
	for _, a := range actions {
		reached := false
		for _, bnd := range km.Bindings() {
			if bnd.Action != a {
				continue
			}
			for _, stack := range allStacks {
				for _, key := range bnd.Keys {
					got, ok := km.ResolveRaw(stack, keyMsgFor(t, key), keys.Opts{})
					if ok && got.Action == a {
						reached = true
					}
				}
			}
		}
		if !reached {
			t.Errorf("action %q is documented but no key reaches it in any stack", a)
		}
	}
}

// activeStack must only ever return stacks the conflict check and the
// override validation know about.
func TestActiveStackIsAlwaysAKnownStack(t *testing.T) {
	known := func(s []keys.Ctx) bool {
		return slices.ContainsFunc(allStacks, func(a []keys.Ctx) bool { return slices.Equal(a, s) })
	}
	ev := hintEvent(nil, nil)
	var models []model
	for _, view := range []viewMode{viewList, viewWeek} {
		for _, focus := range []paneFocus{focusMain, focusDetail, focusActive} {
			for _, pending := range []bool{false, true} {
				m := hintModel(120, ev)
				m.view, m.focusPane, m.activeOpen = view, focus, true
				if pending {
					m.pending = &pendingShift{title: "x"}
				}
				models = append(models, m)
			}
		}
	}
	for _, mode := range []inputMode{modeCalendarPicker, modeLinkPicker, modeAttendeePicker,
		modeHelp, modeFormHelp, modeConfirmSubmit, modeSearch, modeConfirmDelete, modeOverlayPicker} {
		models = append(models, model{mode: mode})
	}
	for _, step := range []createStep{stepTitle, stepLocation, stepAttendees} {
		for _, editing := range []bool{false, true} {
			models = append(models, model{mode: modeCreate, create: createState{step: step, editingField: editing}})
		}
	}
	models = append(models,
		model{mode: modeFindTime, find: findState{step: findPeople}},
		model{mode: modeFindTime, find: findState{step: findSlotList}},
		model{yankPending: true})
	for _, m := range models {
		if s := m.activeStack(); !known(s) {
			t.Errorf("activeStack %v is not in allStacks", s)
		}
	}
}

func TestUserOverridesRebindAndReachTheBar(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	path := keys.ConfigPath(appName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("quit: [x]\nschedule.delete: [ctrl+d]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	km, err := baseKeymap.LoadOverrides(path, allStacks...)
	if err != nil {
		t.Fatalf("valid override rejected: %v", err)
	}
	m := hintModel(200, hintEvent(nil, nil))
	m.km = km
	if _, cmd := m.handleKey(keyMsg("x")); !isQuit(cmd) {
		t.Error("x does not quit after quit: [x]")
	}
	if _, cmd := m.handleKey(keyMsg("q")); isQuit(cmd) {
		t.Error("q still quits after quit was moved to x")
	}
	if bar := m.shortcutHint(); !strings.Contains(bar, "x quit") {
		t.Errorf("bar does not show the rebound key: %q", bar)
	}
}

func TestOverrideThatHidesHelpIsRejected(t *testing.T) {
	// `help: [j]` would make j open help in the list, where j moves — the
	// pinned `? help` hint would then advertise a key that does something else.
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	path := keys.ConfigPath(appName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("help: [j]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	km, err := baseKeymap.LoadOverrides(path, allStacks...)
	if err == nil {
		t.Fatal("an override shadowing help was accepted")
	}
	if got, _ := km.Resolve(allStacks[0], keyMsg("?"), keys.Opts{}); got.Action != "help" {
		t.Errorf("rejected override still changed the table: ? -> %q", got.Action)
	}
}
