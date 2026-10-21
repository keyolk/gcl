package main

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/link"
	"github.com/keyolk/tuikit/tuitest"
)

const bravoURL = "https://calendar.google.com/calendar/event?eid=YnJhdm8"

// osc8 is text as an OSC 8 hyperlink to u, the exact bytes link.Wrap emits.
func osc8(u, text string) string { return "\x1b]8;;" + u + "\x1b\\" + text + "\x1b]8;;\x1b\\" }

// stubLauncher records the URLs link.Open would hand to the browser.
func stubLauncher(t *testing.T) *[]string {
	t.Helper()
	var got []string
	old := link.Launcher
	link.Launcher = func(u string) error { got = append(got, u); return nil }
	t.Cleanup(func() { link.Launcher = old })
	return &got
}

func linkedModel(t *testing.T) model {
	t.Helper()
	m := threeEvents(t, 120, 40)
	m.events[1].HTMLLink = bravoURL
	m.events[1].Links = []string{"https://meet.google.com/abc-defg-hij", bravoURL}
	return m
}

// pressKey sends a key through Update and runs what it returns, feeding the
// messages back, as the program would.
func pressKey(t *testing.T, m model, key string) model {
	t.Helper()
	next, cmd := m.Update(keyMsgFor(t, key))
	m = next.(model)
	for _, msg := range tuitest.Drain(cmd, 0) {
		next, _ = m.Update(msg)
		m = next.(model)
	}
	return m
}

func TestEventTitleIsALinkToItsPage(t *testing.T) {
	m := linkedModel(t)
	m.selected = 1
	out := m.View()
	// Agenda row and the detail card title: the link is exactly the title,
	// not the time pill, the marker or the card's padding.
	if !strings.Contains(out, osc8(bravoURL, "Bravo review")) {
		t.Fatalf("the agenda title is not linked to its page:\n%q", out)
	}
	if n := strings.Count(out, "\x1b]8;;"+bravoURL+"\x1b\\"); n < 2 {
		t.Fatalf("%d links to the event page, want the row and the card title", n)
	}
	// Per surface, so one linked title cannot stand in for another.
	ev := &m.events[1]
	for _, sel := range []bool{false, true} {
		row := m.eventRow(ev, sel, 80, dayStartIn(ev.StartAt, m.tz()), 0)
		if !strings.Contains(row, osc8(bravoURL, "Bravo review")) {
			t.Fatalf("agenda row (selected=%v) does not link exactly the title: %q", sel, row)
		}
	}
	card := m.viewDetailCard(60, 30)
	if !strings.Contains(card, osc8(bravoURL, "Bravo review")) || strings.Contains(card, "\x1b]8;;"+bravoURL+"\x1b\\ ") {
		t.Fatalf("the card title's padding is inside the link: %q", card)
	}
	// The card's link list cuts the meeting link to the card; the link
	// carries the whole URL, around the cut text only (not the "1." marker).
	meet := "https://meet.google.com/abc-defg-hij"
	if !regexp.MustCompile(`1\. ` + regexp.QuoteMeta("\x1b]8;;"+meet+"\x1b\\") + `https://meet\.google\.com/abc[^\x1b]*~` + regexp.QuoteMeta("\x1b]8;;\x1b\\")).MatchString(out) {
		t.Fatalf("the card's cut link row does not link the full URL:\n%q", out)
	}
	// An event with no page has no link.
	if strings.Count(ansi.Strip(out), "Alpha standup") == 0 || strings.Contains(out, "\x1b]8;;\x1b\\Alpha") {
		t.Fatal("Alpha must be drawn plain")
	}
}

func TestOOpensTheSelectedEventsPage(t *testing.T) {
	got := stubLauncher(t)
	m := linkedModel(t)
	m.selected = 1
	next, cmd := m.Update(keyMsgFor(t, "o"))
	m = next.(model)
	msgs := tuitest.Drain(cmd, 0)
	var opened *link.OpenedMsg
	for _, msg := range msgs {
		if o, ok := msg.(link.OpenedMsg); ok {
			opened = &o
		}
	}
	if opened == nil || opened.URL != bravoURL || opened.Err != nil {
		t.Fatalf("o returned %#v, want OpenedMsg for %s", msgs, bravoURL)
	}
	if len(*got) != 1 || (*got)[0] != bravoURL {
		t.Fatalf("launcher got %q, want exactly [%s]", *got, bravoURL)
	}
	next, _ = m.Update(*opened)
	if s := toastText(next.(model)); !strings.Contains(s, "opened "+bravoURL) {
		t.Fatalf("toasts = %q, want the opened note", s)
	}
}

func TestOOnAnEventWithoutAPageLaunchesNothing(t *testing.T) {
	got := stubLauncher(t)
	m := linkedModel(t)
	m.selected = 0 // Alpha: no htmlLink
	m = pressKey(t, m, "o")
	if len(*got) != 0 {
		t.Fatalf("launcher got %q for an event with no link", *got)
	}
	if s := toastText(m); !strings.Contains(s, "no Google Calendar link") {
		t.Fatalf("toasts = %q, want a note that there is no link", s)
	}
}

// htmlLink comes from the API. A scheme other than http(s) is neither linked
// nor opened: a click or a key in a TUI must not run javascript: or file:.
func TestNonHTTPLinksAreNeitherLinkedNorOpened(t *testing.T) {
	for _, bad := range []string{"javascript:alert(1)", "file:///etc/passwd"} {
		got := stubLauncher(t)
		m := linkedModel(t)
		m.events[1].HTMLLink = bad
		m.events[1].Links = nil
		m.selected = 1
		if out := m.View(); strings.Contains(out, "\x1b]8;;") {
			t.Fatalf("%s: View carries a link: %q", bad, out)
		}
		m = pressKey(t, m, "o")
		if len(*got) != 0 {
			t.Fatalf("%s: launcher got %q", bad, *got)
		}
		if s := toastText(m); !strings.Contains(s, "no Google Calendar link") {
			t.Fatalf("%s: toasts = %q, want the no-link note", bad, s)
		}
	}
}

func TestOpenLinkIsInHelpAndTheBar(t *testing.T) {
	m := linkedModel(t)
	m.selected = 1
	m.width = 240
	if bar := m.shortcutHint(); !strings.Contains(bar, "o link") {
		t.Fatalf("bar %q does not offer o", bar)
	}
	m.mode = modeHelp
	secs, st := m.helpSections()
	found := false
	for _, l := range keys.HelpLines(secs, 200, st) {
		l = ansi.Strip(l)
		if strings.Contains(l, "open the event page (the title's link)") && strings.Contains(l, "o") {
			found = true
		}
	}
	if !found {
		t.Fatal("help does not list o as the open-link key")
	}
}

// The link picker (L) lists every URL; a truncated row still links the
// whole URL, and enter opens it through link.Open.
func TestLinkPickerRowsLinkAndOpen(t *testing.T) {
	got := stubLauncher(t)
	m := linkedModel(t)
	m.selected = 1
	m = pressKey(t, m, "L")
	if m.mode != modeLinkPicker {
		t.Fatalf("mode = %v, want the link picker", m.mode)
	}
	meet := "https://meet.google.com/abc-defg-hij"
	// The modal alone: the detail card behind it links the same URL.
	// The selected entry's box wraps the label onto a second line; lipgloss
	// closes the link at the line end and reopens it (BEL-terminated) below.
	if modal := m.modalView(); !regexp.MustCompile(regexp.QuoteMeta("\x1b]8;;"+meet) + "(\x1b\\\\|\a)https://meet").MatchString(modal) {
		t.Fatalf("the picker row is not linked: %q", modal)
	}
	m = pressKey(t, m, "enter")
	if len(*got) != 1 || (*got)[0] != meet {
		t.Fatalf("launcher got %q, want [%s]", *got, meet)
	}
}

// In the week grid a cell is "time title": only the title is the link.
func TestGridCellLinksOnlyTheTitle(t *testing.T) {
	m := linkedModel(t)
	m.view = viewWeek
	m.gridTop = weekStart(today())
	out := m.View()
	if !strings.Contains(out, "10:00 "+"\x1b]8;;"+bravoURL+"\x1b\\Bra") {
		t.Fatalf("grid cell does not link the title after its time:\n%q", out)
	}
}

// The active-now panel leads with the countdown; only the title is linked.
func TestActivePanelLinksOnlyTheTitle(t *testing.T) {
	m := linkedModel(t)
	ev := &m.events[1]
	now := time.Now() // the panel reads the wall clock, not m.now
	ev.StartAt, ev.EndAt = now.Add(-10*time.Minute), now.Add(50*time.Minute)
	ev.StartDate = today()
	out := m.viewActivePanel(100, 6)
	if !strings.Contains(out, "  ·  \x1b]8;;"+bravoURL+"\x1b\\Bravo review") {
		t.Fatalf("panel row does not link exactly the title: %q", out)
	}
}
