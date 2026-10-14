package main

import (
	"os"
	"slices"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/prefs"
)

// persistModel is a loaded model wired like main wires it, with the state
// file in a temp dir.
func persistModel(t *testing.T) model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := loadedModel()
	m.saver = prefs.NewSaver[uiState](appName, 0)
	m.persist = true
	return m
}

// drain runs cmd and every Cmd it batches, feeding each message back through
// Update, until the model quits or nothing is left. Messages that would reach
// the network must not be passed in: every Cmd given is run.
func drain(t *testing.T, m model, cmd tea.Cmd) (model, bool) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case tea.QuitMsg:
			return m, true
		case prefs.SaveTick, prefs.Saved:
			next, more := m.Update(msg)
			m = next.(model)
			queue = append(queue, more)
		}
	}
	return m, false
}

func TestUIStateIsSavedAndRestored(t *testing.T) {
	m := persistModel(t)
	// Set before the keys, so the saves they trigger carry it. (The overlay
	// picker would also reload events from Google.)
	m.overlay.active = []string{"a@x.com", "b@x.com"}
	for _, k := range []string{"w", "t"} { // step by week, hide the timeline
		next, cmd := m.Update(keyMsg(k))
		m = next.(model)
		if cmd == nil {
			t.Fatalf("%s changed the UI state but scheduled no save", k)
		}
	}
	next, cmd := m.Update(keyMsg("q"))
	m, quit := drain(t, next.(model), cmd)
	if !quit {
		t.Fatal("q did not quit after flushing the UI state")
	}
	if _, err := os.Stat(prefs.Path(appName)); err != nil {
		t.Fatalf("no state file after quit: %v", err)
	}

	st, err := loadUIState()
	if err != nil {
		t.Fatal(err)
	}
	var fresh model
	st.apply(&fresh)
	if fresh.jumpUnit != "week" || !fresh.timelineHidden {
		t.Errorf("restored step=%q timelineHidden=%v, want week/true", fresh.jumpUnit, fresh.timelineHidden)
	}
	if !slices.Equal(fresh.overlay.active, []string{"a@x.com", "b@x.com"}) {
		t.Errorf("restored overlay = %v", fresh.overlay.active)
	}
}

func TestGridViewIsRestoredWithItsWindow(t *testing.T) {
	var m model
	m.anchor = today()
	uiState{View: "month", JumpUnit: "day"}.apply(&m)
	if m.view != viewMonth || m.gridTop.IsZero() {
		t.Errorf("view=%v gridTop=%v, want month with a grid window", m.view, m.gridTop)
	}
}

func TestBadStateValuesKeepTheDefaults(t *testing.T) {
	m := model{view: viewList, jumpUnit: "day"}
	too := make([]string, len(overlayColors)+1)
	for i := range too {
		too[i] = string(rune('a'+i)) + "@x.com"
	}
	uiState{View: "sideways", JumpUnit: "fortnight", Overlay: too}.apply(&m)
	if m.view != viewList || m.jumpUnit != "day" || m.overlay.on() {
		t.Errorf("bad file applied: view=%v step=%q overlay=%v", m.view, m.jumpUnit, m.overlay.active)
	}
}

func TestCorruptStateFileStillStarts(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := os.MkdirAll(prefs.Path(appName)[:len(prefs.Path(appName))-len("/ui.json")], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prefs.Path(appName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := loadUIState()
	if err == nil {
		t.Error("corrupt state file not reported")
	}
	if !st.equal(defaultUIState()) {
		t.Errorf("corrupt file gave %+v, want the defaults", st)
	}
}

func TestUnchangedStateWritesNothing(t *testing.T) {
	m := persistModel(t)
	next, _ := m.Update(keyMsg("j")) // moves the selection, not saved state
	if next.(model).saver.Pending() {
		t.Error("a selection move queued a UI-state write")
	}
}

func TestModelsWithoutPersistenceNeverWrite(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := loadedModel()
	next, _ := m.Update(keyMsg("t"))
	if next.(model).saver.Pending() {
		t.Error("a model not built by main queued a write")
	}
	if _, cmd := next.(model).Update(keyMsg("q")); !isQuit(cmd) {
		t.Error("q without persistence did not quit at once")
	}
}
