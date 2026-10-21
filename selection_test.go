package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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

func copiedText(m model) string { return m.textSel.Text(m.selPanes(), m.View()) }

// span is one run of reverse-video cells in a rendered frame.
type span struct {
	y, x0, x1 int
	text      string
}

func highlighted(view string) []span {
	const on, off = "\x1b[7m", "\x1b[0m"
	var out []span
	for y, line := range strings.Split(view, "\n") {
		rest, base := line, 0
		for {
			i := strings.Index(rest, on)
			if i < 0 {
				break
			}
			x0 := base + ansi.StringWidth(ansi.Strip(rest[:i]))
			body := rest[i+len(on):]
			j := strings.Index(body, off)
			if j < 0 {
				j = len(body)
			}
			text := ansi.Strip(body[:j])
			out = append(out, span{y, x0, x0 + ansi.StringWidth(text), text})
			base = x0 + ansi.StringWidth(text)
			rest = body[j:]
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
	// The line is the pane's whole row, so the card's indent comes with it.
	if got := copiedText(m); strings.TrimSpace(got) != "@ Room 7F-Orion" {
		t.Fatalf("triple click = %q, want the whole line", got)
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
