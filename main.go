package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/keyolk/tuikit/prefs"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/keys"
)

func main() {
	calendarFlag := flag.String("calendar", "", "calendar alias/email/name (default: config default_calendar)")
	calendarShort := flag.String("c", "", "calendar alias/email/name")
	dateFlag := flag.String("date", "", "anchor date YYYY-MM-DD")
	dumpFlag := flag.Bool("dump", false, "print events and exit")
	notifyFlag := flag.Bool("notify", false, "send tmux notifications for upcoming events in the selected calendar")
	notifyWindowFlag := flag.Int("notify-window", 15, "fallback minutes-before-start for events with no reminder set (events with reminders use their own)")
	flag.Parse()

	// Calendar aliases are user-managed in a dotfile (~/.config/<app>/config).
	// A missing file is seeded with sensible defaults on first run.
	if _, err := loadConfig(); err != nil {
		fmt.Fprintf(os.Stderr, appName+": config warning: %v\n", err)
	}

	// Flags win over the config default; -c is shorthand for --calendar.
	calendar := settings.defaultCalendar
	if *calendarFlag != "" {
		calendar = *calendarFlag
	}
	if *calendarShort != "" {
		calendar = *calendarShort
	}
	if calendar == "" {
		calendar = "me"
	}

	anchor := today()
	if *dateFlag != "" {
		parsed, err := time.ParseInLocation("2006-01-02", *dateFlag, time.Local)
		if err != nil {
			fatalf("invalid --date: %v", err)
		}
		anchor = parsed
	}

	// We reach the Calendar API directly using gcalcli's stored OAuth. gcalcli
	// itself is only needed as a fallback, so just ensure the oauth exists.
	if _, err := os.Stat(gcalcliOAuthPath()); err != nil {
		fatalf("gcalcli oauth not found at %s. Run gcal.sh init (gcalcli init) first", gcalcliOAuthPath())
	}

	if *dumpFlag {
		events, err := fetchEvents(resolveCalendar(calendar), anchor, anchor.AddDate(0, 0, 7))
		if err != nil {
			fatalf("%v", err)
		}
		for _, ev := range events {
			fmt.Printf("%s %-13s %s\n", ev.StartDate.Format("2006-01-02"), ev.TimeLabel(), ev.Title)
			if ev.Location != "" {
				fmt.Printf("  location: %s\n", ev.Location)
			}
			if len(ev.Rooms) > 0 {
				fmt.Printf("  rooms: %s\n", strings.Join(ev.Rooms, ", "))
			}
			if s := ev.attendeeSummary(); s != "" {
				fmt.Printf("  attendees: %s\n", s)
			}
			if len(ev.ReminderMins) > 0 {
				parts := make([]string, len(ev.ReminderMins))
				for i, mmin := range ev.ReminderMins {
					parts[i] = fmt.Sprintf("%dm", mmin)
				}
				fmt.Printf("  reminders: %s\n", strings.Join(parts, ", "))
			}
			for i, link := range ev.Links {
				if i >= 3 {
					break
				}
				fmt.Printf("  link: %s\n", link)
			}
		}
		return
	}

	if *notifyFlag {
		// Outside the TUI stdout is a fine last resort (cron mails it, a shell
		// shows it), so the CLI keeps the print fallback the TUI must not use.
		deliver := func(msg string) error {
			err := sendToast(msg)
			if errors.Is(err, errNoToastSink) {
				fmt.Println(msg)
				return nil
			}
			return err
		}
		if _, err := notifyUpcoming(resolveCalendar(calendar), *notifyWindowFlag, deliver); err != nil {
			fatalf("notify: %v", err)
		}
		return
	}

	// User key overrides (~/.config/gcl/keys.yaml). A bad file is reported
	// and ignored rather than fatal: the defaults still work.
	km, keysErr := baseKeymap.LoadOverrides(keys.ConfigPath(appName), allStacks...)

	m := model{
		km:             km,
		calendarKey:    calendar,
		calendar:       resolveCalendar(calendar),
		view:           viewList,
		jumpUnit:       settings.defaultStep,
		timelineHidden: !settings.timeline,
		anchor:         anchor,
		loading:        true,
		nextReqID:      1,
		inflightReq:    1,
	}
	m.saver = prefs.NewSaver[uiState](appName, 0)
	st, stErr := loadUIState()
	st.apply(&m)
	if stErr != nil {
		m.fail(srcFiles, "UI state not restored: "+stErr.Error())
	}
	if keysErr != nil {
		// Shown in the TUI, not on stderr: the alt screen would hide a
		// printed warning until exit.
		m.fail(srcStatus, "keys.yaml ignored: "+keysErr.Error())
	}
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
