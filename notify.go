package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// notifyStatePath is where already-fired reminders are recorded so the same
// event isn't announced twice across --notify runs and in-app ticks.
func notifyStatePath() string {
	return cachePath("notify-state")
}

// osascriptPath is the resolved path to macOS's osascript (empty on non-macOS
// or when unavailable), used to post desktop notification banners.
var osascriptPath = func() string {
	if p, err := exec.LookPath("osascript"); err == nil {
		return p
	}
	return ""
}()

// upcomingReminder is one reminder that is due to fire now, for an event that
// hasn't started yet.
type upcomingReminder struct {
	key     string // dedupe key: id@startRFC3339#reminderMin
	minutes int    // whole minutes until the event starts (rounded)
	title   string
	loc     string
}

// scanUpcoming returns reminders that are due now: for each event it honors the
// event's own reminder settings (reminders.overrides, or the calendar defaults
// when reminders.useDefault). A reminder set for N minutes before start fires
// once the current time reaches (start - N) and the event has not started.
//
// fallbackWindow (minutes) only applies to events that carry NO reminder at all:
// they fire once within that many minutes of starting. Pass 0 to disable the
// fallback and rely purely on each event's configured reminders.
func scanUpcoming(calendar string, fallbackWindow int) ([]upcomingReminder, error) {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	// Look far enough ahead to catch long lead-time reminders (e.g. 1 day / 1 week).
	end := now.Add(8 * 24 * time.Hour)
	events, err := fetchEvents(calendar, start, end)
	if err != nil {
		return nil, err
	}
	seen := loadNotifiedKeys()
	var out []upcomingReminder
	for _, ev := range events {
		if ev.AllDay() || ev.StartTime == "" {
			continue
		}
		startAt, err := time.ParseInLocation("2006-01-02 15:04", ev.StartDate.Format("2006-01-02")+" "+ev.StartTime, time.Local)
		if err != nil {
			continue
		}
		untilStart := startAt.Sub(now)
		if untilStart < 0 {
			continue // already started
		}

		// Reminder offsets to consider for this event.
		offsets := ev.ReminderMins
		usingFallback := false
		if len(offsets) == 0 {
			if fallbackWindow <= 0 {
				continue // no reminder configured and fallback disabled
			}
			offsets = []int{fallbackWindow}
			usingFallback = true
		}

		for _, mins := range offsets {
			fireAt := startAt.Add(-time.Duration(mins) * time.Minute)
			// Due when we've reached the fire time but the event hasn't started.
			if now.Before(fireAt) {
				continue
			}
			key := ev.ID + "@" + startAt.Format(time.RFC3339) + "#" + strconv.Itoa(mins)
			if usingFallback {
				key = ev.ID + "@" + startAt.Format(time.RFC3339) + "#fallback"
			}
			if seen[key] {
				continue
			}
			out = append(out, upcomingReminder{
				key:     key,
				minutes: int(untilStart.Minutes() + 0.5),
				title:   ev.Title,
				loc:     ev.Location,
			})
		}
	}
	return out, nil
}

func (r upcomingReminder) message() string {
	msg := fmt.Sprintf("gcal: in %dm  %s", r.minutes, r.title)
	if r.loc != "" {
		msg += "  @ " + r.loc
	}
	return msg
}

// sendToast delivers a non-focus-stealing notification. Inside tmux it flashes
// the status line (display-message); on macOS it also posts a desktop banner via
// osascript. Neither steals focus — deliberately NOT using tmux display-popup,
// which is modal and grabs the keyboard.
func sendToast(msg string) {
	if os.Getenv("TMUX") != "" {
		// -d 0 leaves the message up until the next status refresh / keypress.
		_ = exec.Command("tmux", "display-message", msg).Run()
	}
	if osascriptPath != "" {
		script := fmt.Sprintf("display notification %q with title %q", msg, "gcal")
		_ = exec.Command(osascriptPath, "-e", script).Run()
	}
	if os.Getenv("TMUX") == "" && osascriptPath == "" {
		fmt.Println(msg)
	}
}

// loadNotifiedKeys reads the fired-reminder state file.
func loadNotifiedKeys() map[string]bool {
	seen := map[string]bool{}
	if data, err := os.ReadFile(notifyStatePath()); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if s := strings.TrimSpace(line); s != "" {
				seen[s] = true
			}
		}
	}
	return seen
}

// markNotified appends freshly-fired keys to the state file (merged + sorted).
func markNotified(keys []string) {
	if len(keys) == 0 {
		return
	}
	seen := loadNotifiedKeys()
	for _, k := range keys {
		seen[k] = true
	}
	merged := make([]string, 0, len(seen))
	for k := range seen {
		merged = append(merged, k)
	}
	sort.Strings(merged)
	_ = os.MkdirAll(cacheDir(), 0o755)
	_ = os.WriteFile(notifyStatePath(), []byte(strings.Join(merged, "\n")+"\n"), 0o644)
}

// notifyUpcoming performs one scan-and-toast pass, honoring each event's own
// reminder settings. fallbackWindow (minutes) applies only to events with no
// reminder configured. Used by the --notify flag (cron/launchd friendly) and
// reused by the in-app watcher.
func notifyUpcoming(calendar string, fallbackWindow int) error {
	reminders, err := scanUpcoming(calendar, fallbackWindow)
	if err != nil {
		return err
	}
	var fired []string
	for _, r := range reminders {
		sendToast(r.message())
		fired = append(fired, r.key)
	}
	markNotified(fired)
	return nil
}
