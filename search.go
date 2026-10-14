package main

import (
	"fmt"
	"sort"
	"strings"
)

func (m model) searchMatches() []searchMatch {
	query := strings.ToLower(strings.TrimSpace(m.input))
	onlyMine := false
	for _, prefix := range []string{"mine ", "mine", "@me ", "@me", "owner:me ", "owner:me"} {
		if strings.HasPrefix(query, prefix) {
			onlyMine = true
			query = strings.TrimSpace(strings.TrimPrefix(query, prefix))
			break
		}
	}
	matches := make([]searchMatch, 0, len(m.events))
	for i := range m.events {
		ev := &m.events[i]
		vd := m.viewDate(ev)
		if onlyMine {
			me := myIdentity()
			mine := me != "" && (strings.Contains(strings.ToLower(ev.Calendar), me) ||
				strings.Contains(strings.ToLower(strings.Join(ev.Attendees, " ")), me) ||
				strings.Contains(strings.ToLower(ev.AttendeeEmail), me))
			if !mine {
				continue
			}
		}
		corpus := strings.ToLower(strings.Join([]string{
			vd.Format("2006-01-02"),
			vd.Format("Jan 02"),
			vd.Format("Mon"),
			m.timeLabel(ev),
			ev.Title,
			ev.Location,
			strings.Join(ev.Rooms, " "),
			ev.Description,
			ev.Calendar,
			ev.AttendeeEmail,
			strings.Join(ev.Attendees, " "),
			strings.Join(ev.Links, " "),
		}, " "))
		score := fuzzyScore(query, corpus)
		if query == "" || score > 0 {
			label := fmt.Sprintf("%s %-13s %s", vd.Format("01/02 Mon"), m.timeLabel(ev), ev.Title)
			if ev.Location != "" {
				label += " @ " + ev.Location
			}
			matches = append(matches, searchMatch{eventIndex: i, score: score, label: label})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if query == "" {
			return matches[i].eventIndex < matches[j].eventIndex
		}
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].eventIndex < matches[j].eventIndex
	})
	return matches
}

func fuzzyScore(query, text string) int {
	query = strings.TrimSpace(strings.ToLower(query))
	text = strings.ToLower(text)
	if query == "" {
		return 1
	}
	if strings.Contains(text, query) {
		return 1000 + len(query)*10
	}
	qi := 0
	score := 0
	streak := 0
	for _, r := range text {
		if qi >= len(query) {
			break
		}
		if byte(r) == query[qi] {
			score += 10 + streak*5
			streak++
			qi++
		} else {
			streak = 0
		}
	}
	if qi == len(query) {
		return score
	}
	return 0
}
