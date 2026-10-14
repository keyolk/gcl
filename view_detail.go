package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// viewDayDetail lists every event on the focused day (grid detail pane).
func (m model) viewDayDetail(width, height int) string {
	iw := max(16, width-3)
	inner := max(3, height)
	evs := m.focusDayEvents()
	var lines []string
	lines = append(lines, sectionTitleStyle.Render(truncate("  "+m.anchor.Format("Mon Jan 02")+"  ", max(10, iw))))
	lines = append(lines, "")
	if len(evs) == 0 {
		lines = append(lines, mutedStyle.Render("no events"))
	}
	for i, ev := range evs {
		tm := "all-day"
		if !ev.AllDay() && !ev.StartAt.IsZero() {
			tm = m.timeLabel(ev)
		}
		pending := m.pendingFor(ev)
		if pending != nil {
			loc := m.tz()
			tm = pending.start().In(loc).Format("15:04") + "-" + pending.end().In(loc).Format("15:04")
		}
		badges := ""
		if pending != nil {
			badges += " " + pendingBadge(pending)
		}
		if isActiveAt(ev, time.Now()) {
			badges += " ◉now"
		}
		if n := len(ev.Attendees); n > 0 {
			badges += fmt.Sprintf(" 👥%d", n)
		}
		if ev.otherLinkCount() > 0 {
			badges += " >"
		}
		line := pillStyle.Render(tm) + " " + truncate(ev.Title, max(6, iw-lipgloss.Width(tm)-lipgloss.Width(badges)-2)) + mutedStyle.Render(badges)
		if m.focusPane == focusDetail && i == max(0, min(m.gridDetail, len(evs)-1)) {
			line = selectedRowStyle.Render(truncate("▸ "+tm+" "+ev.Title+badges, iw))
		}
		if pending != nil {
			line = pendingStyle.Render(truncate(tm+" "+ev.Title+badges, iw))
		}
		lines = append(lines, line)
		if loc := locationWithoutRooms(ev); loc != "" {
			lines = append(lines, mutedStyle.Render("     @ "+truncate(loc, iw-7)))
		}
		if len(ev.Rooms) > 0 {
			lines = append(lines, mutedStyle.Render("     room: "+truncate(strings.Join(ev.Rooms, ", "), iw-11)))
		}
	}
	lines = append(lines, "")
	lines = append(lines, mutedStyle.Render("tab detail focus | j/k event | a active now | e calendar | A/L/E/X act"))
	content := truncateLines(strings.Join(lines, "\n"), inner, iw)
	return detailStyle.Width(max(28, width)).Height(inner).Render(content)
}

func (m model) viewDetailCard(width, height int) string {
	// Grid views: show ALL of the focused day's events, not just one.
	if m.view != viewList {
		return m.viewDayDetail(width, height)
	}
	ev := m.selectedEvent()
	if ev == nil {
		return detailStyle.Width(max(28, width)).Height(max(3, height)).Render("No event selected")
	}
	var lines []string
	lines = append(lines, sectionTitleStyle.Render(truncate("  "+ev.Title+"  ", max(10, width-4))))
	lines = append(lines, "")
	lines = append(lines, pillStyle.Render(m.viewDate(ev).Format("Mon Jan 02"))+" "+pillStyle.Render(m.timeLabel(ev)))
	// A staged change spells out saved → staged in full here, since the row can
	// only fit the delta. Pre-truncated to the card's content width (padding is
	// 3 cells) so lipgloss never re-wraps and pushes the card past `height`; the
	// diff splits across two lines when the pane is too narrow for one.
	if p := m.pendingFor(ev); p != nil {
		iw := max(10, width-4)
		lines = append(lines, pendingStyle.Render(truncate("! unsaved "+p.label(), iw)))
		before, after := p.diffParts(m.tz())
		if lipgloss.Width(before+" -> "+after) <= iw-2 {
			lines = append(lines, pendingStyle.Render("  "+before+" -> "+after))
		} else {
			lines = append(lines, mutedStyle.Render(truncate("  from "+before, iw)))
			lines = append(lines, pendingStyle.Render(truncate("  to   "+after, iw)))
		}
		lines = append(lines, mutedStyle.Render(truncate("  s saves · esc discards", iw)))
	}
	if isActiveAt(ev, time.Now()) {
		// How long it has been running matters for a maintenance window in a way
		// it doesn't for a meeting — the detail pane has room to say both.
		now := time.Now()
		lines = append(lines, activePillStyle.Render("◉ active now")+" "+mutedStyle.Render(remainingLabel(ev, now)))
		lines = append(lines, mutedStyle.Render(truncate("  "+elapsedLabel(ev, now), max(10, width-4))))
	}
	if loc := locationWithoutRooms(ev); loc != "" {
		lines = append(lines, linkStyle.Render("@ ")+wrap(loc, max(10, width-4)))
	}
	if len(ev.Rooms) > 0 {
		lines = append(lines, mutedStyle.Render("room: ")+wrap(strings.Join(ev.Rooms, ", "), max(10, width-7)))
	}
	if ev.Calendar != "" {
		lines = append(lines, mutedStyle.Render("Calendar: "+displayNameForCalendar(ev.Calendar)))
	}
	attendees := ev.Attendees
	if len(attendees) == 0 && ev.AttendeeEmail != "" {
		attendees = []string{ev.AttendeeEmail}
	}
	if len(attendees) > 0 {
		lines = append(lines, "")
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("Attendees (%d)", len(attendees))))
		for i, a := range attendees {
			if i >= 12 {
				lines = append(lines, mutedStyle.Render(fmt.Sprintf("~ +%d more", len(attendees)-12)))
				break
			}
			lines = append(lines, "- "+truncate(prettyAttendee(a), max(10, width-5)))
		}
	}
	if ev.Description != "" {
		lines = append(lines, "")
		lines = append(lines, mutedStyle.Render("Description"))
		lines = append(lines, wrap(ev.Description, max(10, width-4)))
	}
	if len(ev.Links) > 0 {
		lines = append(lines, "")
		lines = append(lines, linkStyle.Render("Links"))
		for i, link := range ev.Links {
			if i >= 6 {
				lines = append(lines, mutedStyle.Render("~ more links"))
				break
			}
			lines = append(lines, linkStyle.Render(fmt.Sprintf("%d. %s", i+1, truncate(link, max(10, width-7)))))
		}
	}
	content := strings.Join(lines, "\n")
	// No border: the card occupies exactly `height` rows. Left+right padding is
	// 3 cells, so the content width is width-3. Pre-truncate to that width so
	// lipgloss never re-wraps (which would grow the block past `height`).
	inner := max(3, height)
	return detailStyle.Width(max(28, width)).Height(inner).Render(truncateLines(content, inner, max(20, width-3)))
}
