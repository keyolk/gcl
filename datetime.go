package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Flexible date/time entry for the create/edit form.
//
// The form previously accepted only strict `YYYY-MM-DD` and `HH:MM`, which
// meant typing a full date for "tomorrow at 3pm". These parsers accept the
// shorthand people actually type — keywords (`today`, `tmr`), relative offsets
// (`+3d`, `2w`), weekday names (`mon`, `next fri`), short numeric dates
// (`7/20`, `0720`), 12-hour times (`3pm`, `3:30pm`), compact digits (`1530`),
// and Korean suffixes (`15시`, `30분`) — while still accepting the canonical
// forms. The form normalizes what the user typed back into canonical text so
// the rendered field and the submitted event always agree.

var (
	reISODate      = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})$`)
	reMonthDay     = regexp.MustCompile(`^(\d{1,2})[-/.](\d{1,2})$`)
	reCompact4     = regexp.MustCompile(`^(\d{2})(\d{2})$`)
	reDayOnly      = regexp.MustCompile(`^(\d{1,2})$`)
	reOffset       = regexp.MustCompile(`^([+-]?)(\d+)\s*([dwmy])$`)
	reClock        = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
	reClockCompact = regexp.MustCompile(`^(\d{3,4})$`)
	reHourOnly     = regexp.MustCompile(`^(\d{1,2})$`)
	reAmPm         = regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?\s*(am|pm)$`)
	reKoreanTime   = regexp.MustCompile(`^(\d{1,2})시(?:\s*(\d{1,2})분?)?$`)
	reHourMin      = regexp.MustCompile(`^(\d+)h\s*(\d+)m?$`)
	reKoreanDur    = regexp.MustCompile(`^(?:(\d+)시간)?(?:(\d+)분)?$`)
)

var weekdayNames = map[string]time.Weekday{
	"sun": time.Sunday, "sunday": time.Sunday,
	"mon": time.Monday, "monday": time.Monday,
	"tue": time.Tuesday, "tues": time.Tuesday, "tuesday": time.Tuesday,
	"wed": time.Wednesday, "weds": time.Wednesday, "wednesday": time.Wednesday,
	"thu": time.Thursday, "thur": time.Thursday, "thurs": time.Thursday, "thursday": time.Thursday,
	"fri": time.Friday, "friday": time.Friday,
	"sat": time.Saturday, "saturday": time.Saturday,
}

// parseFlexibleDate resolves a user-typed date against `now`, returning a
// midnight-local time on the resolved day.
func parseFlexibleDate(s string, now time.Time) (time.Time, error) {
	raw := strings.ToLower(strings.TrimSpace(s))
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	loc := now.Location()
	day := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	}
	today := day(now)

	// Keywords.
	switch raw {
	case "today", "tod", "now", "오늘":
		return today, nil
	case "tomorrow", "tmr", "tom", "tmrw", "내일":
		return today.AddDate(0, 0, 1), nil
	case "yesterday", "yst", "yes", "어제":
		return today.AddDate(0, 0, -1), nil
	}

	// "next <weekday>" behaves like the bare weekday form (next occurrence).
	if rest, ok := strings.CutPrefix(raw, "next "); ok {
		raw = strings.TrimSpace(rest)
	}

	// Weekday name → strictly-future occurrence.
	if wd, ok := weekdayNames[raw]; ok {
		delta := (int(wd) - int(today.Weekday()) + 7) % 7
		if delta == 0 {
			delta = 7 // "wed" on a Wednesday means next Wednesday
		}
		return today.AddDate(0, 0, delta), nil
	}

	// Relative offset: +3d, -1d, 2w, +1m, +1y.
	if mm := reOffset.FindStringSubmatch(raw); mm != nil {
		n, err := strconv.Atoi(mm[2])
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid offset")
		}
		if mm[1] == "-" {
			n = -n
		}
		switch mm[3] {
		case "d":
			return today.AddDate(0, 0, n), nil
		case "w":
			return today.AddDate(0, 0, n*7), nil
		case "m":
			return today.AddDate(0, n, 0), nil
		case "y":
			return today.AddDate(n, 0, 0), nil
		}
	}

	// Canonical ISO date.
	if mm := reISODate.FindStringSubmatch(raw); mm != nil {
		return buildDate(mm[1], mm[2], mm[3], loc)
	}

	// Month/day within the current year (7/20, 07-20).
	if mm := reMonthDay.FindStringSubmatch(raw); mm != nil {
		return buildDate(strconv.Itoa(now.Year()), mm[1], mm[2], loc)
	}

	// Compact MMDD (0720).
	if mm := reCompact4.FindStringSubmatch(raw); mm != nil {
		return buildDate(strconv.Itoa(now.Year()), mm[1], mm[2], loc)
	}

	// Bare day-of-month in the current month (20). Checked after the offset
	// form so "3d" is an offset, not a day.
	if mm := reDayOnly.FindStringSubmatch(raw); mm != nil {
		return buildDate(strconv.Itoa(now.Year()), strconv.Itoa(int(now.Month())), mm[1], loc)
	}

	// Bare offset without a sign or unit suffix already handled above; a bare
	// number with a unit but no sign (e.g. "3d") matches reOffset.
	return time.Time{}, fmt.Errorf("unrecognized date: %s", s)
}

// buildDate validates and constructs a local midnight date. Go's time.Date
// normalizes out-of-range values (month 13 → next January), so re-check the
// components round-trip to reject genuinely invalid input like 2026-13-40.
func buildDate(yStr, mStr, dStr string, loc *time.Location) (time.Time, error) {
	y, err1 := strconv.Atoi(yStr)
	mo, err2 := strconv.Atoi(mStr)
	d, err3 := strconv.Atoi(dStr)
	if err1 != nil || err2 != nil || err3 != nil {
		return time.Time{}, fmt.Errorf("invalid date")
	}
	if mo < 1 || mo > 12 || d < 1 || d > 31 {
		return time.Time{}, fmt.Errorf("date out of range")
	}
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, loc)
	if t.Year() != y || int(t.Month()) != mo || t.Day() != d {
		return time.Time{}, fmt.Errorf("date out of range")
	}
	return t, nil
}

// parseFlexibleTime resolves a user-typed time to canonical "HH:MM" (24h).
func parseFlexibleTime(s string) (string, error) {
	raw := strings.ToLower(strings.TrimSpace(s))
	if raw == "" {
		return "", fmt.Errorf("empty time")
	}
	raw = strings.TrimSuffix(raw, "분") // "9시30분" → handled by reKoreanTime

	// 12-hour with am/pm.
	if mm := reAmPm.FindStringSubmatch(raw); mm != nil {
		h, _ := strconv.Atoi(mm[1])
		min := 0
		if mm[2] != "" {
			min, _ = strconv.Atoi(mm[2])
		}
		if h < 1 || h > 12 || min > 59 {
			return "", fmt.Errorf("time out of range")
		}
		if mm[3] == "pm" && h != 12 {
			h += 12
		}
		if mm[3] == "am" && h == 12 {
			h = 0
		}
		return fmt.Sprintf("%02d:%02d", h, min), nil
	}

	// Korean: 15시, 9시30(분).
	if mm := reKoreanTime.FindStringSubmatch(raw); mm != nil {
		h, _ := strconv.Atoi(mm[1])
		min := 0
		if mm[2] != "" {
			min, _ = strconv.Atoi(mm[2])
		}
		return clampClock(h, min)
	}

	// Canonical / short 24h clock: 15:04, 9:30.
	if mm := reClock.FindStringSubmatch(raw); mm != nil {
		h, _ := strconv.Atoi(mm[1])
		min, _ := strconv.Atoi(mm[2])
		return clampClock(h, min)
	}

	// Compact digits: 1530, 930.
	if mm := reClockCompact.FindStringSubmatch(raw); mm != nil {
		v := mm[1]
		if len(v) == 3 {
			v = "0" + v
		}
		h, _ := strconv.Atoi(v[:2])
		min, _ := strconv.Atoi(v[2:])
		return clampClock(h, min)
	}

	// Hour only: 15, 9.
	if mm := reHourOnly.FindStringSubmatch(raw); mm != nil {
		h, _ := strconv.Atoi(mm[1])
		return clampClock(h, 0)
	}

	return "", fmt.Errorf("unrecognized time: %s", s)
}

func clampClock(h, min int) (string, error) {
	if h < 0 || h > 23 || min < 0 || min > 59 {
		return "", fmt.Errorf("time out of range")
	}
	return fmt.Sprintf("%02d:%02d", h, min), nil
}

// parseDurationMinutes parses a duration into whole minutes. Accepts plain
// minutes ("30"), hours ("1h", "1.5h"), combined ("1h30m", "1h30"), explicit
// minutes ("45m"), days ("1일"), and Korean units ("30분", "1시간30분").
func parseDurationMinutes(s string) (int, error) {
	raw := strings.ToLower(strings.TrimSpace(s))
	if raw == "" {
		return 0, fmt.Errorf("empty")
	}
	raw = strings.ReplaceAll(raw, " ", "")

	// Days: 1일 / 2d (duration context, distinct from a date offset).
	if v, ok := strings.CutSuffix(raw, "일"); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n * 1440, nil
		}
		return 0, fmt.Errorf("invalid duration")
	}

	// Korean: 30분, 1시간, 1시간30분.
	if strings.Contains(raw, "시간") || strings.HasSuffix(raw, "분") {
		if mm := reKoreanDur.FindStringSubmatch(raw); mm != nil && (mm[1] != "" || mm[2] != "") {
			total := 0
			if mm[1] != "" {
				h, _ := strconv.Atoi(mm[1])
				total += h * 60
			}
			if mm[2] != "" {
				min, _ := strconv.Atoi(mm[2])
				total += min
			}
			if total > 0 {
				return total, nil
			}
		}
		return 0, fmt.Errorf("invalid duration")
	}

	// Combined hours+minutes: 1h30m, 2h15m, 1h30.
	if mm := reHourMin.FindStringSubmatch(raw); mm != nil {
		h, _ := strconv.Atoi(mm[1])
		min, _ := strconv.Atoi(mm[2])
		if total := h*60 + min; total > 0 {
			return total, nil
		}
		return 0, fmt.Errorf("invalid duration")
	}

	// Hours, possibly fractional: 1h, 1.5h.
	if v, ok := strings.CutSuffix(raw, "h"); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return int(f * 60), nil
		}
		return 0, fmt.Errorf("invalid duration")
	}

	// Plain minutes, with optional 'm'.
	v := strings.TrimSuffix(raw, "m")
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n, nil
	}
	return 0, fmt.Errorf("invalid duration")
}

// fieldHint returns a one-line hint describing what shorthand the focused form
// field accepts, so the flexible parsers are discoverable in the UI rather than
// only in the README.
func fieldHint(step createStep) string {
	switch step {
	case stepDate:
		return "today · tmr · mon · +3d · 7/20 · 2026-07-20"
	case stepStart:
		return "15:00 · 3pm · 3:30pm · 1530 · 15시"
	case stepDuration:
		return "30 · 45m · 1h · 1h30m · 90m"
	case stepRepeat:
		return "daily · weekly · biweekly · monthly · weekdays (+ x4)"
	}
	return ""
}

// repeatSpec maps a repeat shorthand to the RRULE body it produces (without
// the "RRULE:" prefix or any COUNT clause).
var repeatSpecs = map[string]string{
	"daily": "FREQ=DAILY", "d": "FREQ=DAILY", "매일": "FREQ=DAILY",
	"weekly": "FREQ=WEEKLY", "w": "FREQ=WEEKLY", "매주": "FREQ=WEEKLY",
	"biweekly": "FREQ=WEEKLY;INTERVAL=2", "2w": "FREQ=WEEKLY;INTERVAL=2", "격주": "FREQ=WEEKLY;INTERVAL=2",
	"monthly": "FREQ=MONTHLY", "m": "FREQ=MONTHLY", "매월": "FREQ=MONTHLY", "매달": "FREQ=MONTHLY",
	"yearly": "FREQ=YEARLY", "y": "FREQ=YEARLY", "매년": "FREQ=YEARLY",
	"weekdays": "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", "평일": "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR",
	"weekday": "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR",
}

var reRepeatCount = regexp.MustCompile(`^(.*?)\s*[x*]\s*(\d+)$`)

// splitRepeat separates the repeat shorthand from an optional occurrence count
// ("weekly x4" → "weekly", 4). count is 0 when absent.
func splitRepeat(s string) (spec string, count int) {
	raw := strings.ToLower(strings.TrimSpace(s))
	if mm := reRepeatCount.FindStringSubmatch(raw); mm != nil {
		n, err := strconv.Atoi(mm[2])
		if err == nil && n > 0 {
			return strings.TrimSpace(mm[1]), n
		}
	}
	return raw, 0
}

// recurrenceRules converts the repeat shorthand into Google Calendar RRULE
// lines. Unrecognized text yields no rules — validateRepeat surfaces the error
// to the user rather than silently creating a wrong recurrence.
func (c *createState) recurrenceRules() []string {
	spec, count := splitRepeat(c.repeat)
	if spec == "" || spec == "none" || spec == "no" || spec == "없음" {
		return nil
	}
	body, ok := repeatSpecs[spec]
	if !ok {
		return nil
	}
	if count > 0 {
		body += fmt.Sprintf(";COUNT=%d", count)
	}
	return []string{"RRULE:" + body}
}

// validateRepeat reports a human-readable error when the repeat field has text
// that isn't a recognized shorthand.
func (c *createState) validateRepeat() string {
	spec, _ := splitRepeat(c.repeat)
	if spec == "" || spec == "none" || spec == "no" || spec == "없음" {
		return ""
	}
	if _, ok := repeatSpecs[spec]; !ok {
		return "repeat: try daily, weekly, biweekly, monthly, weekdays (add x4 for 4 times)"
	}
	return ""
}

// describeRepeat renders the repeat shorthand as human-readable text for the
// form preview ("weekly x4" → "weekly, 4 times").
func describeRepeat(s string) string {
	spec, count := splitRepeat(s)
	if spec == "" {
		return ""
	}
	label := spec
	switch spec {
	case "d", "매일":
		label = "daily"
	case "w", "매주":
		label = "weekly"
	case "2w", "격주":
		label = "biweekly"
	case "m", "매월", "매달":
		label = "monthly"
	case "y", "매년":
		label = "yearly"
	case "평일", "weekday":
		label = "weekdays"
	}
	if count > 0 {
		return fmt.Sprintf("%s, %d times", label, count)
	}
	return label
}

// previewLine renders the resolved date/time/end for the form preview. Unlike
// the stored field text (which may still be shorthand mid-typing), this shows
// what would actually be created — or a hint when the input isn't parseable yet.
func (c *createState) previewLine(loc *time.Location, tzLabel string, now time.Time) string {
	d, err := parseFlexibleDate(c.date, now)
	if err != nil {
		return "date not recognized yet"
	}
	hm, err := parseFlexibleTime(c.start)
	if err != nil {
		return d.Format("Mon Jan 02") + "  time not recognized yet"
	}
	mins, err := parseDurationMinutes(c.durationStr)
	if err != nil {
		return fmt.Sprintf("%s  %s  duration not recognized yet", d.Format("Mon Jan 02"), hm)
	}
	t, err := time.ParseInLocation("15:04", hm, loc)
	if err != nil {
		return d.Format("Mon Jan 02") + "  " + hm
	}
	start := time.Date(d.Year(), d.Month(), d.Day(), t.Hour(), t.Minute(), 0, 0, loc)
	end := start.Add(time.Duration(mins) * time.Minute)
	// Note when the event runs past midnight so a long duration isn't a
	// surprise.
	if end.Day() != start.Day() {
		return fmt.Sprintf("%s  %s → %s (+1d, %s)  %s",
			start.Format("Mon Jan 02"), start.Format("15:04"), end.Format("15:04"),
			humanMinutes(mins), tzLabel)
	}
	return fmt.Sprintf("%s  %s → %s (%s)  %s",
		start.Format("Mon Jan 02"), start.Format("15:04"), end.Format("15:04"),
		humanMinutes(mins), tzLabel)
}

// humanMinutes renders a minute count compactly: 30 → "30m", 90 → "1h30m".
func humanMinutes(mins int) string {
	if mins < 60 {
		return fmt.Sprintf("%dm", mins)
	}
	h, m := mins/60, mins%60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

// normalize resolves the form's raw date/start/duration text into canonical
// values in place, so the rendered field text matches what will be submitted.
// Returns a human-readable error string (empty when the form is valid).
func (c *createState) normalize(now time.Time) string {
	d, err := parseFlexibleDate(c.date, now)
	if err != nil {
		return "date: try 2026-07-20, tmr, mon, or +3d"
	}
	c.date = d.Format("2006-01-02")

	hm, err := parseFlexibleTime(c.start)
	if err != nil {
		return "start: try 15:00, 3pm, 1530, or 15시"
	}
	c.start = hm

	mins, err := parseDurationMinutes(c.durationStr)
	if err != nil {
		return "duration: try 30, 1h, 1h30m, or 90m"
	}
	c.duration = mins
	c.durationStr = strconv.Itoa(mins)

	if err := c.validateRepeat(); err != "" {
		return err
	}
	return ""
}
