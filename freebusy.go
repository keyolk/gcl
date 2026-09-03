package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Free/busy lookup and mutual-slot search.
//
// The question this answers is the one a calendar is worst at: "when can these
// five people actually meet?". Reading five agendas side by side and diffing
// them by eye is exactly the work a computer should do, and Google exposes the
// raw material for it — freeBusy.query returns each calendar's busy intervals
// without needing read access to the event bodies, so it works for colleagues
// whose calendars only expose free/busy.
//
// The search is a straight sweep: collect every busy interval per person,
// enumerate candidate slots on a fixed grid, and count how many people are free
// in each. Slots where everyone is free rank first; partial ones are kept
// (a 4/5 slot at a good hour beats no slot at all) and carry the names of who
// is blocked, so the organizer can decide whether that person matters.

// busyInterval is one blocked span on someone's calendar. Half-open: an
// interval ending exactly at 10:00 does not block a 10:00 start.
type busyInterval struct {
	start time.Time
	end   time.Time
}

// freeBusyResult is one calendar's answer: its busy intervals, or the reason
// the lookup failed for it. A per-calendar error must not fail the whole query —
// one unreadable calendar out of five should degrade to "unknown", not to
// nothing.
type freeBusyResult struct {
	calendar string
	busy     []busyInterval
	err      string
}

// freeBusyBatchLimit is how many calendars go in one freeBusy.query. Google
// caps the request at 50 items; batching keeps a 20-person search to one call.
const freeBusyBatchLimit = 50

// queryFreeBusy fetches busy intervals for every calendar id in the window.
// Calendars that error out come back with err set rather than being dropped, so
// the UI can say "unknown" instead of silently treating them as free.
func queryFreeBusy(calendars []string, start, end time.Time) ([]freeBusyResult, error) {
	if len(calendars) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	at, err := googleToken.accessToken(ctx)
	if err != nil {
		return nil, err
	}

	// resolveCalendarID maps aliases/display names to real ids, but the API
	// echoes results back keyed by the id it was given — keep both directions
	// so results can be matched back to what the caller asked for.
	type idPair struct{ requested, resolved string }
	pairs := make([]idPair, 0, len(calendars))
	seen := map[string]bool{}
	for _, c := range calendars {
		c = strings.TrimSpace(c)
		if c == "" || seen[strings.ToLower(c)] {
			continue
		}
		seen[strings.ToLower(c)] = true
		pairs = append(pairs, idPair{requested: c, resolved: resolveCalendarID(c)})
	}

	byRequested := make(map[string]*freeBusyResult, len(pairs))
	out := make([]freeBusyResult, 0, len(pairs))
	for i := range pairs {
		out = append(out, freeBusyResult{calendar: pairs[i].requested})
	}
	for i := range out {
		byRequested[strings.ToLower(out[i].calendar)] = &out[i]
	}

	for begin := 0; begin < len(pairs); begin += freeBusyBatchLimit {
		stop := min(begin+freeBusyBatchLimit, len(pairs))
		batch := pairs[begin:stop]

		type itemReq struct {
			ID string `json:"id"`
		}
		payload := struct {
			TimeMin string    `json:"timeMin"`
			TimeMax string    `json:"timeMax"`
			Items   []itemReq `json:"items"`
		}{
			TimeMin: start.Format(time.RFC3339),
			TimeMax: end.Format(time.RFC3339),
		}
		for _, p := range batch {
			payload.Items = append(payload.Items, itemReq{ID: p.resolved})
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			"https://www.googleapis.com/calendar/v3/freeBusy", strings.NewReader(string(body)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+at)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			err := apiError(resp, "freebusy")
			resp.Body.Close()
			return nil, err
		}
		var decoded struct {
			Calendars map[string]struct {
				Busy []struct {
					Start string `json:"start"`
					End   string `json:"end"`
				} `json:"busy"`
				Errors []struct {
					Reason string `json:"reason"`
				} `json:"errors"`
			} `json:"calendars"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		for _, p := range batch {
			res := byRequested[strings.ToLower(p.requested)]
			if res == nil {
				continue
			}
			cal, ok := decoded.Calendars[p.resolved]
			if !ok {
				// Google keys the response by the id it accepted, which is not
				// always byte-identical to what we sent (case, alias forms).
				// Fall back to a case-insensitive scan before giving up.
				for k, v := range decoded.Calendars {
					if strings.EqualFold(k, p.resolved) {
						cal, ok = v, true
						break
					}
				}
			}
			if !ok {
				res.err = "no free/busy data"
				continue
			}
			if len(cal.Errors) > 0 {
				res.err = cal.Errors[0].Reason
				continue
			}
			for _, b := range cal.Busy {
				bs, err1 := time.Parse(time.RFC3339, b.Start)
				be, err2 := time.Parse(time.RFC3339, b.End)
				if err1 != nil || err2 != nil || !be.After(bs) {
					continue
				}
				res.busy = append(res.busy, busyInterval{start: bs, end: be})
			}
			sort.Slice(res.busy, func(i, j int) bool { return res.busy[i].start.Before(res.busy[j].start) })
		}
	}
	return out, nil
}

// slotSearch describes what to look for. Zero values are not meaningful; build
// one with defaultSlotSearch and override from there.
type slotSearch struct {
	// duration of the meeting to place.
	duration time.Duration
	// step is the grid candidate starts land on. 30m means slots begin at :00
	// and :30 — the granularity people actually book on.
	step time.Duration
	// from/to bound the search window (absolute instants).
	from time.Time
	to   time.Time
	// dayStartHour/dayEndHour clip each local day to workable hours, in the
	// display timezone. The default excludes only the small hours (00:00-07:00),
	// so evenings stay bookable for cross-timezone teams.
	dayStartHour int
	dayEndHour   int
	// skipWeekends drops Saturday/Sunday candidates entirely.
	skipWeekends bool
	// loc is the timezone the day-hour clipping is evaluated in.
	loc *time.Location
	// maxResults caps how many slots are returned after ranking.
	maxResults int
}

// candidateSlot is one proposed meeting time with who can make it.
type candidateSlot struct {
	start time.Time
	end   time.Time
	// free/busy hold the participant calendar ids, so the UI can name exactly
	// who is blocked rather than only counting.
	free []string
	busy []string
	// unknown are participants whose free/busy could not be read. They are
	// neither counted as free nor as blocked: claiming a slot works for someone
	// whose calendar we could not see would be worse than admitting the gap.
	unknown []string
}

// allFree reports whether every participant with readable free/busy is free.
func (s candidateSlot) allFree() bool { return len(s.busy) == 0 }

// findSlots sweeps the window and returns ranked candidate slots.
//
// Ranking: fully-free slots first, then by how many people are free, then
// earliest. "Soonest" is the tiebreak rather than the primary key because a
// 5/5 slot next Tuesday is almost always a better answer than a 3/5 slot
// tomorrow — but among equally-good slots, sooner wins.
func findSlots(results []freeBusyResult, search slotSearch) []candidateSlot {
	if search.duration <= 0 || search.step <= 0 || !search.to.After(search.from) {
		return nil
	}
	loc := search.loc
	if loc == nil {
		loc = time.Local
	}

	// Split participants into readable and unreadable up front: the unknown set
	// is identical for every slot, so it does not belong in the inner loop.
	var unknown []string
	readable := make([]freeBusyResult, 0, len(results))
	for _, r := range results {
		if r.err != "" {
			unknown = append(unknown, r.calendar)
			continue
		}
		readable = append(readable, r)
	}

	var out []candidateSlot
	// Align the first candidate to the step grid so slots read as 10:00/10:30
	// rather than 10:07 — an off-grid proposal looks like a bug even when the
	// arithmetic is right.
	cursor := alignUp(search.from, search.step, loc)
	for !cursor.After(search.to.Add(-search.duration)) {
		slotEnd := cursor.Add(search.duration)
		local := cursor.In(loc)
		endLocal := slotEnd.In(loc)
		if !withinWorkHours(local, endLocal, search) {
			cursor = cursor.Add(search.step)
			continue
		}
		var free, busy []string
		for _, r := range readable {
			if intervalsOverlap(r.busy, cursor, slotEnd) {
				busy = append(busy, r.calendar)
			} else {
				free = append(free, r.calendar)
			}
		}
		out = append(out, candidateSlot{
			start:   cursor,
			end:     slotEnd,
			free:    free,
			busy:    busy,
			unknown: unknown,
		})
		cursor = cursor.Add(search.step)
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if len(a.busy) != len(b.busy) {
			return len(a.busy) < len(b.busy)
		}
		return a.start.Before(b.start)
	})
	// Adjacent grid steps inside one long gap produce near-identical slots
	// (10:00, 10:30, 11:00 …), which would fill the whole list with one
	// afternoon. Keep the earliest start of each contiguous run instead.
	out = collapseAdjacent(out, search.step)
	if search.maxResults > 0 && len(out) > search.maxResults {
		out = out[:search.maxResults]
	}
	return out
}

// collapseAdjacent removes slots that merely slide one step forward inside the
// same free window for the same participant set. The earliest start of each run
// survives, so a three-hour opening offers one entry, not six.
func collapseAdjacent(slots []candidateSlot, step time.Duration) []candidateSlot {
	if len(slots) <= 1 {
		return slots
	}
	// Group by participant set so a run is only collapsed when the *same* people
	// are free throughout — a slot where one more person becomes free is a
	// genuinely different option and must survive.
	type runKey struct {
		busyKey string
		day     string
	}
	kept := make([]candidateSlot, 0, len(slots))
	lastEnd := map[runKey]time.Time{}
	for _, s := range slots {
		k := runKey{busyKey: strings.Join(s.busy, "|"), day: s.start.Format("2006-01-02")}
		if prevEnd, ok := lastEnd[k]; ok && !s.start.After(prevEnd) {
			// Still inside the run that started earlier: extend it, drop this one.
			if s.end.After(prevEnd) {
				lastEnd[k] = s.end
			}
			continue
		}
		lastEnd[k] = s.end
		kept = append(kept, s)
	}
	return kept
}

// alignUp rounds t up to the next multiple of step within its local day, so
// candidate starts land on clean boundaries (:00, :15, :30, :45).
func alignUp(t time.Time, step time.Duration, loc *time.Location) time.Time {
	local := t.In(loc)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	since := local.Sub(midnight)
	if since%step == 0 {
		return t
	}
	bump := step - since%step
	return t.Add(bump).Truncate(time.Minute)
}

// withinWorkHours reports whether a slot fits inside the searchable hours of
// the day it starts on. A slot must both start and end inside the window: a
// 1-hour meeting starting at 17:30 with a 18:00 cutoff is not bookable.
func withinWorkHours(start, end time.Time, search slotSearch) bool {
	if search.skipWeekends {
		switch start.Weekday() {
		case time.Saturday, time.Sunday:
			return false
		}
	}
	if search.dayStartHour == 0 && search.dayEndHour == 24 {
		return true
	}
	dayStart := time.Date(start.Year(), start.Month(), start.Day(), search.dayStartHour, 0, 0, 0, start.Location())
	// dayEndHour == 24 means midnight of the following day.
	dayEnd := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location()).
		Add(time.Duration(search.dayEndHour) * time.Hour)
	return !start.Before(dayStart) && !end.After(dayEnd)
}

// intervalsOverlap reports whether [start,end) collides with any busy interval.
// Both are half-open, so back-to-back meetings do not count as a conflict.
func intervalsOverlap(busy []busyInterval, start, end time.Time) bool {
	for _, b := range busy {
		if b.start.Before(end) && start.Before(b.end) {
			return true
		}
	}
	return false
}

// defaultSlotSearch builds a search from the configured defaults, anchored at
// `now` (rounded up to the next grid step — a slot that already started is not
// a proposal).
func defaultSlotSearch(now time.Time, loc *time.Location) slotSearch {
	step := time.Duration(settings.slotStep) * time.Minute
	if step <= 0 {
		step = 30 * time.Minute
	}
	return slotSearch{
		duration:     time.Duration(settings.slotDuration) * time.Minute,
		step:         step,
		from:         now,
		to:           now.AddDate(0, 0, settings.slotSearchDays),
		dayStartHour: settings.slotDayStart,
		dayEndHour:   settings.slotDayEnd,
		skipWeekends: settings.slotSkipWeekends,
		loc:          loc,
		maxResults:   slotMaxResults,
	}
}

// slotMaxResults caps the candidate list. Past a couple of screens the list
// stops being a decision aid and becomes a scroll.
const slotMaxResults = 40

// slotDurationOptions are the meeting lengths cycled with `d` in the picker.
// 30m and 1h are what almost every meeting actually is; the rest are there so
// the cycle does not dead-end.
var slotDurationOptions = []int{15, 30, 45, 60, 90, 120}

// describeSlotWindow renders the search window for the panel header, e.g.
// "next 14d · 07:00-24:00 · weekends included".
func describeSlotWindow(s slotSearch) string {
	days := int(s.to.Sub(s.from).Hours() / 24)
	if days < 1 {
		days = 1
	}
	hours := fmt.Sprintf("%02d:00-%02d:00", s.dayStartHour, s.dayEndHour)
	if s.dayStartHour == 0 && s.dayEndHour == 24 {
		hours = "any hour"
	}
	weekends := "weekends included"
	if s.skipWeekends {
		weekends = "weekdays only"
	}
	return fmt.Sprintf("next %dd · %s · %s", days, hours, weekends)
}
