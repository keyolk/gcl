package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/link"
)

// Links (E11). An event's title hides its Google Calendar page, so every
// place that draws a title makes it an OSC 8 hyperlink to HTMLLink: the
// terminal opens it on cmd/ctrl+click even under mouse capture. `o`
// (link.Binding) opens the same page from the keyboard; the meeting and
// document links stay one key away in the L picker, which lists every URL
// the event carries. `o` takes the event page, not the meeting link, so that
// a key and a click on the title open the same thing, and because every
// event read from the API has one while many have no meeting at all.

// linkSpan wraps the part of s between prefix and suffix (a row's marker and
// badges) as a link to u, so only the title text is the link, never the
// markers or padding (rule: wrap the text, not the row). s is plain text: a
// truncated row may have lost the suffix, and then the link runs to the end.
func linkSpan(u, s, prefix, suffix string) string {
	if !link.Valid(u) || !strings.HasPrefix(s, prefix) {
		return s
	}
	body, tail := s[len(prefix):], ""
	if suffix != "" && strings.HasSuffix(body, suffix) {
		body, tail = body[:len(body)-len(suffix)], suffix
	}
	if body == "" {
		return s
	}
	return prefix + link.Wrap(u, body) + tail
}

// eventLink is the URL an event's title links to and `o` opens: its Google
// Calendar page, or the calendar page found among its parsed links when the
// API left htmlLink out. Empty when neither is an http(s) URL.
func (e Event) eventLink() string {
	if link.Valid(e.HTMLLink) {
		return e.HTMLLink
	}
	for _, l := range e.Links {
		if strings.Contains(l, "google.com/calendar/event") && link.Valid(l) {
			return l
		}
	}
	return ""
}

// opened reports link.Open's result: a toast either way, so a missing opener
// or an SSH session is not mistaken for a browser that never came up.
func (m model) opened(msg link.OpenedMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.fail(srcStatus, msg.Note())
	} else {
		m.say(msg.Note())
	}
	return m, nil
}
