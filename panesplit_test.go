package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/prefs"
	"github.com/keyolk/tuikit/split"
	"github.com/keyolk/tuikit/toast"
)

// Split tests read every position back out of View(): the divider glyph, the
// detail pane's title. Nothing here recomputes the layout arithmetic.

const detailTitle = "Alpha standup" // the selected event, drawn in the detail pane

// dividerAt is the column of the split's divider: the right split draws `│`
// (`┃` while dragging) on the first body row, and nothing else on that row
// uses either glyph.
func dividerAt(t *testing.T, m model) (x, y int) {
	t.Helper()
	for _, g := range []string{"│", "┃"} {
		for _, c := range occurrences(m, g) {
			if c[1] == 1 {
				return c[0], c[1]
			}
		}
	}
	t.Fatalf("no divider on the first body row:\n%s", stripANSI(m.View()))
	return 0, 0
}

func splitKey(t *testing.T, m model, k string) model {
	t.Helper()
	next, _ := m.Update(keyMsg(k))
	return next.(model)
}

func TestDividerDragMovesTheBoundary(t *testing.T) {
	m := threeEvents(t, 120, 24)
	x, y := dividerAt(t, m)
	tx, _ := rightmostAt(t, m, detailTitle)

	m, _ = press(m, x, y)
	if !strings.Contains(stripANSI(m.View()), "┃") {
		t.Error("a press on the divider did not start a drag (no heavy divider)")
	}
	m, _ = motion(m, x-12, y+3)
	if got := copiedText(m); got != "" {
		t.Errorf("a divider drag selected text: %q", got)
	}
	m, _ = release(m, x-12, y+3)

	nx, _ := dividerAt(t, m)
	if nx != x-12 {
		t.Fatalf("divider at %d after dragging from %d by -12", nx, x)
	}
	if ntx, _ := rightmostAt(t, m, detailTitle); ntx != tx-12 {
		t.Errorf("detail title at %d, want %d: the pane did not move with the divider", ntx, tx-12)
	}
	if strings.Contains(stripANSI(m.View()), "┃") {
		t.Error("the drag highlight stayed after release")
	}

	// A cell that was the schedule's is the detail pane's now: the wheel there
	// is the read-only detail card's (nothing), not the agenda's (moves).
	before := m.selected
	m, _ = wheel(m, nx+3, y+3, true)
	if m.selected != before {
		t.Errorf("wheel right of the new divider moved the agenda selection %d -> %d", before, m.selected)
	}
	m, _ = wheel(m, nx-3, y+3, true)
	if m.selected == before {
		t.Error("wheel left of the new divider did not reach the agenda")
	}
	for _, p := range m.selPanes() {
		if p.ID == "detail" && p.Rect.X != nx+1 {
			t.Errorf("selection's detail pane starts at %d, divider at %d", p.Rect.X, nx)
		}
	}
}

func TestPanePressStillSelectsTextAndDividerPressNever(t *testing.T) {
	m := selModel(t)
	x, y := dividerAt(t, m)
	tx, ty := rightmostAt(t, m, detailTitle)
	sel, _ := drag(m, tx, ty, tx+4, ty)
	if got := copiedText(sel); !strings.Contains(got, "Alpha") {
		t.Errorf("a drag in the detail pane copied %q, want the title", got)
	}
	if len(highlighted(sel.View())) == 0 {
		t.Fatal("setup: the copied text is not highlighted")
	}
	// A divider press clears that highlight: the selection never sees the
	// press, and the resize moves the cells it covers.
	cleared, _ := press(sel, x, y)
	if len(highlighted(cleared.View())) != 0 || cleared.textSel.Active() {
		t.Error("a divider press left the old selection on screen")
	}
	div, _ := press(m, x, y)
	div, _ = motion(div, x, y+4)
	if got := copiedText(div); got != "" {
		t.Errorf("a press on the divider started a selection: %q", got)
	}
	if len(highlighted(div.View())) != 0 {
		t.Error("a divider drag highlighted text")
	}
}

// A divider press is the split's alone: the click handler, which treats any
// press as acknowledging a sticky error, never sees it.
func TestDividerPressNeverReachesTheClickHandler(t *testing.T) {
	m := threeEvents(t, 120, 24)
	t0 := time.Now()
	m.toasts = toast.WithClock(m.toasts, func() time.Time { return t0 })
	m.fail(srcStatus, "boom")
	// Past the minimum dwell, so the next input would acknowledge it.
	m.toasts = toast.WithClock(m.toasts, func() time.Time { return t0.Add(time.Hour) })
	x, y := dividerAt(t, m)
	m, _ = press(m, x, y)
	m, _ = release(m, x, y)
	if !strings.Contains(toastText(m), "boom") {
		t.Error("a divider press reached the click handler (it acknowledged the error)")
	}
	m, _ = press(m, x-5, y+3)
	if strings.Contains(toastText(m), "boom") {
		t.Error("control: a pane press did not acknowledge the error")
	}
}

func TestDoubleClickOnTheDividerResets(t *testing.T) {
	m := threeEvents(t, 120, 24)
	x0, y := dividerAt(t, m)
	m = splitKey(t, m, splitShrinkKey)
	x, _ := dividerAt(t, m)
	if x == x0 {
		t.Fatal("shrink did not move the divider")
	}
	m, _ = press(m, x, y)
	m, _ = release(m, x, y)
	m, _ = press(m, x, y)
	m, _ = release(m, x, y)
	if nx, _ := dividerAt(t, m); nx != x0 {
		t.Errorf("double click left the divider at %d, want the default %d", nx, x0)
	}
}

func TestSplitKeysMoveTheBoundary(t *testing.T) {
	m := threeEvents(t, 120, 24)
	x0, _ := dividerAt(t, m)
	m = splitKey(t, m, splitShrinkKey)
	x1, _ := dividerAt(t, m)
	if x1 >= x0 {
		t.Errorf("%s: divider %d -> %d, want left", splitShrinkKey, x0, x1)
	}
	m = splitKey(t, m, splitGrowKey)
	m = splitKey(t, m, splitGrowKey)
	x2, _ := dividerAt(t, m)
	if x2 <= x0 {
		t.Errorf("%s twice: divider %d, want right of %d", splitGrowKey, x2, x0)
	}
	m = splitKey(t, m, splitResetKey)
	if x3, _ := dividerAt(t, m); x3 != x0 {
		t.Errorf("%s: divider %d, want the default %d", splitResetKey, x3, x0)
	}
	// < and > still stage day moves; the split did not take them.
	if b, ok := baseKeymap.Resolve(m.activeStack(), keyMsg(">"), keys.Opts{}); !ok || b.Action != "nudge.nextday" {
		t.Errorf("> resolves to %q, want nudge.nextday", b.Action)
	}
}

func TestSplitKeysInFooterAndHelp(t *testing.T) {
	m := threeEvents(t, 400, 24)
	if hint := m.shortcutHint(); !strings.Contains(hint, splitShrinkKey+splitGrowKey+" split") {
		t.Errorf("wide footer has no resize hint: %q", hint)
	}
	secs, _ := m.helpSections()
	var help strings.Builder
	for _, s := range secs {
		help.WriteString(s.Title + "\n")
		for _, e := range s.Entries {
			help.WriteString(strings.Join(e.Keys, " ") + " " + e.Help + "\n")
		}
	}
	for _, want := range []string{"shrink the schedule", "grow the schedule", "reset the schedule / detail split"} {
		if !strings.Contains(help.String(), want) {
			t.Errorf("help lacks %q:\n%s", want, help.String())
		}
	}
	// No split at this size: the bar does not offer to resize one.
	// (Laid out at 60x14, measured on a bar wide enough for every hint.)
	small := threeEvents(t, 60, 14)
	if hint := small.bar(400, "", small.activeStack()...); strings.Contains(hint, splitShrinkKey+splitGrowKey+" split") {
		t.Errorf("footer offers resize with no split on screen: %q", hint)
	}
}

func TestGrowPastTheLimitThenShrinkMovesAtOnce(t *testing.T) {
	m := threeEvents(t, 120, 24)
	for range 40 {
		m = splitKey(t, m, splitGrowKey)
	}
	far, _ := dividerAt(t, m)
	if !strings.Contains(toastText(m), "limit") {
		t.Errorf("a grow at the limit said nothing: %q", toastText(m))
	}
	m = splitKey(t, m, splitShrinkKey)
	if x, _ := dividerAt(t, m); x >= far {
		t.Errorf("one shrink after overshooting left the divider at %d (was %d): dead zone", x, far)
	}
	// The detail pane kept its minimum at the far end.
	for range 40 {
		m = splitKey(t, m, splitGrowKey)
	}
	if l := m.paneLayout(); l.detail.W < 34 {
		t.Errorf("detail pane %d cols wide, want at least 34", l.detail.W)
	}
}

func TestBottomSplitKeysMoveTheBoundary(t *testing.T) {
	m := threeEvents(t, 80, 30)
	if m.splitMode() != splitBottom {
		t.Fatalf("want the bottom split at 80x30, got %v", m.splitMode())
	}
	_, y0 := lastAt(t, m, detailTitle)
	m = splitKey(t, m, splitShrinkKey)
	_, y1 := lastAt(t, m, detailTitle)
	if y1 >= y0 {
		t.Errorf("%s in the bottom split: detail title row %d -> %d, want up", splitShrinkKey, y0, y1)
	}
	m = splitKey(t, m, splitResetKey)
	if _, y := lastAt(t, m, detailTitle); y != y0 {
		t.Errorf("reset: detail title row %d, want %d", y, y0)
	}
	// The right split's ratio is its own.
	if m.splitRight != (split.Model{}) {
		t.Error("a bottom-split key changed the right split")
	}
}

func TestSplitRatioIsSavedAndRestored(t *testing.T) {
	m := persistModel(t)
	m.width, m.height = 120, 24
	x0, _ := dividerAt(t, m)
	for range 3 {
		next, cmd := m.Update(keyMsg(splitShrinkKey))
		m = next.(model)
		if cmd == nil {
			t.Fatal("a split change scheduled no save")
		}
	}
	x, _ := dividerAt(t, m)
	next, cmd := m.Update(keyMsg("q"))
	if _, quit := drain(t, next.(model), cmd); !quit {
		t.Fatal("q did not quit after flushing the UI state")
	}
	st, err := loadUIState()
	if err != nil {
		t.Fatal(err)
	}
	fresh := threeEvents(t, 120, 24)
	st.apply(&fresh)
	if fx, _ := dividerAt(t, fresh); fx != x || fx == x0 {
		t.Errorf("restored divider at %d, saved at %d (default %d)", fx, x, x0)
	}
}

func TestGarbageSplitRatioKeepsTheDefault(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	def := threeEvents(t, 120, 24)
	x0, _ := dividerAt(t, def)
	for _, raw := range []string{
		`{"split_right":{"ratio":40}}`, // a percentage
		`{"split_right":{"ratio":-1}}`,
		`{"split_right":{"ratio":"wide"}}`, // wrong type: the whole file is rejected
		`{"split_right":{}}`,
	} {
		path := prefs.Path(appName)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		st, _ := loadUIState()
		m := threeEvents(t, 120, 24)
		st.apply(&m)
		if x, _ := dividerAt(t, m); x != x0 {
			t.Errorf("%s: divider at %d, want the default %d", raw, x, x0)
		}
	}
}

func TestSplitFitsNarrowTerminals(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {60, 24}, {60, 14}, {90, 24}, {200, 50}} {
		m := threeEvents(t, sz[0], sz[1])
		for _, k := range []string{"", splitGrowKey, splitShrinkKey} {
			if k != "" {
				for range 30 {
					m = splitKey(t, m, k)
				}
			}
			lines := strings.Split(m.View(), "\n")
			if len(lines) != sz[1] {
				t.Errorf("%dx%d after %q: %d rows", sz[0], sz[1], k, len(lines))
			}
			for i, l := range lines {
				if w := lipgloss.Width(l); w > sz[0] {
					t.Errorf("%dx%d after %q: row %d is %d wide", sz[0], sz[1], k, i, w)
				}
			}
		}
	}
	// 60 columns is below the right split: the detail pane goes to the bottom
	// when there is height for it, and away when there is not.
	if m := threeEvents(t, 60, 24); m.splitMode() != splitBottom {
		t.Errorf("60x24 split = %v, want bottom", m.splitMode())
	}
	if m := threeEvents(t, 60, 14); m.splitMode() != splitNone || len(occurrences(m, detailTitle)) != 1 {
		t.Errorf("60x14 split = %v, want none (title shown once)", m.splitMode())
	}
}
