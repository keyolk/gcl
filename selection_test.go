package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/selection"
	"github.com/keyolk/tuikit/tuitest"
)

// selModel is threeEvents with a located, described first event and a
// theme that emits SGR, so the reverse-video highlight shows in View().
func selModel(t *testing.T) model {
	t.Helper()
	t.Setenv("NO_COLOR", "")
	colorTheme(t, true)
	m := threeEvents(t, 120, 24)
	m.events[0].Location = "Room 7F-Orion"
	m.events[0].Description = "agenda: ship drag selection today"
	return m
}

func motion(m model, x, y int) (model, tea.Cmd) {
	return send(m, tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
}

func release(m model, x, y int) (model, tea.Cmd) {
	return send(m, tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease})
}

func drag(m model, x0, y0, x1, y1 int) (model, tea.Cmd) {
	m, _ = press(m, x0, y0)
	m, _ = motion(m, x1, y1)
	return release(m, x1, y1)
}

func copiedText(m model) string { return m.textSel.Text(m.selPanes(), m.frame()) }

// span is one run of reverse-video cells in a rendered frame, located by
// finding the run's text on its stripped line.
type span struct {
	y, x0, x1 int
	text      string
}

func highlighted(view string) []span {
	var out []span
	for y, line := range strings.Split(view, "\n") {
		plain := ansi.Strip(line)
		from := 0
		for _, run := range tuitest.Reversed(line) {
			i := strings.Index(plain[from:], run)
			if i < 0 {
				continue
			}
			x0 := ansi.StringWidth(plain[:from+i])
			out = append(out, span{y, x0, x0 + ansi.StringWidth(run), run})
			from += i + len(run)
		}
	}
	return out
}

func TestDragFromTheAgendaStopsAtTheDetailPane(t *testing.T) {
	m := selModel(t)
	x, y := at(t, m, "Bravo review")
	dx, dy := rightmostAt(t, m, "agenda: ship") // the detail pane, to the right
	m, cmd := drag(m, x, y, dx+6, dy)
	if cmd == nil {
		t.Fatal("a finished drag must return the copy command")
	}
	got := copiedText(m)
	if !strings.HasPrefix(got, "Bravo review") || !strings.Contains(got, "Charlie sync") {
		t.Fatalf("copy %q should run from Bravo down the agenda", got)
	}
	if strings.Contains(got, "agenda:") || strings.Contains(got, "Room") || strings.Contains(got, "Description") {
		t.Fatalf("copy %q leaked the detail pane", got)
	}
	spans := highlighted(m.View())
	if len(spans) == 0 {
		t.Fatal("the selection is not drawn")
	}
	for _, s := range spans {
		if s.x1 > dx-1 || strings.Contains(s.text, "agenda:") {
			t.Fatalf("highlight %+v reaches the detail pane at x=%d", s, dx)
		}
	}
}

func TestDetailCardPicksAWordAndALine(t *testing.T) {
	m := selModel(t)
	x, y := rightmostAt(t, m, "7F-Orion")
	m, _ = press(m, x+2, y)
	m, _ = release(m, x+2, y)
	m, cmd := press(m, x+2, y)
	if cmd == nil {
		t.Fatal("a word pick copies")
	}
	if got := copiedText(m); got != "7F-Orion" {
		t.Fatalf("double click = %q, want 7F-Orion", got)
	}
	m, _ = release(m, x+2, y)
	m, _ = press(m, x+2, y)
	// A triple click takes the line without the card's indent.
	if got := copiedText(m); got != "@ Room 7F-Orion" {
		t.Fatalf("triple click = %q, want the whole line without its indent", got)
	}
}

func TestPressWithoutMotionIsStillAClick(t *testing.T) {
	m := selModel(t)
	x, y := at(t, m, "Bravo review")
	m, _ = press(m, x, y)
	m, _ = release(m, x, y)
	if m.selected != 1 {
		t.Fatalf("selected=%d, want Bravo (1): a still press is the app's click", m.selected)
	}
	if m.textSel.Active() || len(highlighted(m.View())) != 0 {
		t.Fatal("a click without motion must not leave a selection")
	}
}

func TestSecondClickOnTheAgendaIsStillOpenNotAWordPick(t *testing.T) {
	m := selModel(t)
	x, y := at(t, m, "Bravo review")
	m, _ = press(m, x, y)
	m, _ = release(m, x, y)
	m, _ = press(m, x, y)
	if got := toastText(m); !strings.Contains(got, "no Google Calendar link") {
		t.Fatalf("second click did not run open; toasts = %q", got)
	}
	if m.textSel.Active() {
		t.Fatal("a list pane must not pick a word on a double click")
	}
}

func TestKeyClearsTheSelection(t *testing.T) {
	m := selModel(t)
	x, y := at(t, m, "Bravo review")
	m, _ = drag(m, x, y, x+6, y)
	if !m.textSel.Active() {
		t.Fatal("setup: the drag should leave a selection")
	}
	next, _ := m.Update(keyMsg("j"))
	m = next.(model)
	if m.textSel.Active() || len(highlighted(m.View())) != 0 {
		t.Fatal("a key must clear the selection")
	}
}

func TestDragPastTheHelpBottomScrollsAndKeepsScrolledOutLines(t *testing.T) {
	m := selModel(t)
	m.mode = modeHelp
	x, y := at(t, m, "General")
	_, marker := at(t, m, "more")
	m, _ = press(m, x, y)
	for range 4 {
		m, _ = motion(m, x+70, marker)
	}
	if m.helpOff != 4 {
		t.Fatalf("helpOff=%d after four drags onto the marker, want 4", m.helpOff)
	}
	m, cmd := release(m, x+70, marker)
	if cmd == nil {
		t.Fatal("release must return the copy command")
	}
	got := copiedText(m)
	if !strings.HasPrefix(got, "General") {
		t.Fatalf("copy %q must keep the scrolled-out first line", got)
	}
	_, rows := m.helpBodySize()
	if n := strings.Count(got, "\n") + 1; n <= rows {
		t.Fatalf("copy has %d lines; it must span more than the %d-row page", n, rows)
	}
	if strings.Contains(got, "more") || strings.Contains(got, "│") {
		t.Fatalf("copy %q includes the marker or the border", got)
	}
}

func TestScheduleUnderHelpIsNotSelectable(t *testing.T) {
	m := selModel(t)
	x, y := at(t, m, "Charlie sync") // left of the help modal
	m.mode = modeHelp
	m, _ = drag(m, x, y, x+6, y)
	if m.textSel.Active() || len(highlighted(m.View())) != 0 {
		t.Fatal("the schedule under an overlay must not be selectable")
	}
}

func TestDragInTheSearchQueryNeverMovesTheCursor(t *testing.T) {
	m := selModel(t)
	m.mode = modeSearch
	m.input = "standup"
	m.searchIndex = 0
	x, y := at(t, m, "standup|")
	before := m.input
	m, _ = drag(m, x, y, x+5, y)
	// Both ends of a drag are inclusive: x..x+5 is six cells.
	if got := copiedText(m); got != "standu" {
		t.Fatalf("copy %q, want the dragged query text", got)
	}
	if m.input != before || m.mode != modeSearch {
		t.Fatalf("a drag in the query edited it (%q) or closed the search", m.input)
	}
}

// A resize can leave helpOff past the end of the page (RenderHelp clamps it
// when drawing). A drag past the top must still scroll up one line from what
// is on screen: the app sets the offset the selection reports, it does not
// add the scroll to its own stale value.
func TestDragPastTheHelpTopScrollsFromAStaleOffset(t *testing.T) {
	m := selModel(t)
	m.mode = modeHelp
	m.helpOff = 1 << 10 // past the end, as a taller terminal leaves it
	_, drawn := m.helpPage()
	if drawn == 0 {
		t.Fatal("setup: help must be longer than the page")
	}
	x, y := at(t, m, "Key overrides")
	_, top := at(t, m, " Help ") // the popup title, above the page
	m, _ = press(m, x, y)
	m, _ = motion(m, x, top)
	if m.helpOff != drawn-1 {
		t.Fatalf("helpOff=%d after a drag past the top, want %d (one above the drawn page)", m.helpOff, drawn-1)
	}
	if _, now := m.helpPage(); now != drawn-1 {
		t.Fatalf("the page did not move: drawn offset %d, want %d", now, drawn-1)
	}
}

// A drag resting on the "… more" marker keeps scrolling through the
// selection's auto-scroll ticks; the terminal sends no motion meanwhile.
func TestDragRestingPastTheHelpBottomAutoScrolls(t *testing.T) {
	m := selModel(t)
	m.mode = modeHelp
	x, y := at(t, m, "General")
	_, marker := at(t, m, "more")
	m, _ = press(m, x, y)
	m, cmd := motion(m, x+70, marker)
	if m.helpOff != 1 {
		t.Fatalf("helpOff=%d after one drag onto the marker, want 1", m.helpOff)
	}
	for want := 2; want <= 3; want++ {
		var tick tea.Msg
		for _, msg := range tuitest.Drain(cmd, 2*selection.AutoScrollInterval) {
			if _, ok := msg.(selection.AutoScrollMsg); ok {
				tick = msg
			}
		}
		if tick == nil {
			t.Fatalf("no auto-scroll tick while the drag rests past the edge (want offset %d)", want)
		}
		next, c := m.Update(tick)
		m, cmd = next.(model), c
		if m.helpOff != want {
			t.Fatalf("helpOff=%d after an auto-scroll tick, want %d", m.helpOff, want)
		}
	}
}

// NO_COLOR asks for no color; reverse video is not a color, and without it a
// selection would be invisible.
func TestSelectionStaysVisibleUnderNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := threeEvents(t, 120, 24)
	x, y := at(t, m, "Bravo review")
	m, _ = drag(m, x, y, x+4, y)
	if got := tuitest.Reversed(m.View()); len(got) != 1 || got[0] != "Bravo" {
		t.Fatalf("reverse runs under NO_COLOR = %q, want [Bravo]", got)
	}
}

// The copy reaches the clipboard through selection.Clipboard and comes back
// as the toast text the kit words.
func TestReleaseCopiesThroughTheClipboardSeam(t *testing.T) {
	var got []string
	old := selection.Clipboard
	selection.Clipboard = func(s string) error { got = append(got, s); return nil }
	t.Cleanup(func() { selection.Clipboard = old })
	m := selModel(t)
	x, y := at(t, m, "Bravo review")
	m, cmd := drag(m, x, y, x+4, y)
	for _, msg := range tuitest.Drain(cmd, 0) {
		next, _ := m.Update(msg)
		m = next.(model)
	}
	if len(got) != 1 || got[0] != "Bravo" {
		t.Fatalf("clipboard got %q, want [Bravo]", got)
	}
	if !strings.Contains(toastText(m), "copied 5 chars") {
		t.Fatalf("toasts = %q, want the copy note", toastText(m))
	}
}
