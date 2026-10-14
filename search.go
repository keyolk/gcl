package main

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/keyolk/tuikit/fuzzy"
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

// fuzzyScore ranks text against a search or picker query: 0 for no match,
// otherwise positive and higher for a better match. It wraps the kit's scorer
// (fuzzy.Score), which compares runes — the hand-written one compared a byte
// of the query with each rune of the text, so a Korean query only matched as
// an exact substring.
//
// Two things are kept from the old scorer: matching ignores case (the query is
// lowercased, so the kit's smart case never applies), and an exact substring
// ranks above any scattered match. The kit's length penalty is added back:
// here the text is a whole event (title, place, attendees, description), and
// a long description must not push a better match down the list.
func fuzzyScore(query, text string) int {
	query = strings.TrimSpace(strings.ToLower(query))
	text = strings.ToLower(text)
	if query == "" {
		return 1
	}
	s, _, ok := fuzzy.Score(text, query)
	if !ok {
		return 0
	}
	s += utf8.RuneCountInString(text) / 8
	if strings.Contains(text, query) {
		s += 1000
	}
	return max(1, s)
}
