package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// Subcommands for other tools (the cc-mods office): `gcl rsvp` answers an
// invitation, `gcl find` proposes meeting times. Both print one JSON object
// on stdout and exit non-zero with the reason on stderr when they fail.

// rsvpReplies maps what a caller types to the API's responseStatus.
var rsvpReplies = map[string]string{
	"yes": "accepted", "accept": "accepted", "accepted": "accepted",
	"no": "declined", "decline": "declined", "declined": "declined",
	"maybe": "tentative", "tentative": "tentative",
}

// runSubcommand runs `gcl <name> …` when args[0] names one, and reports
// whether it did. It runs before flag.Parse: the flag package stops at the
// first non-flag argument, so `gcl rsvp -c team …` would never reach it.
func runSubcommand(args []string, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}
	switch args[0] {
	case "rsvp":
		return true, cliRSVP(args[1:], stdout, stderr)
	case "find":
		return true, cliFind(args[1:], stdout, stderr, time.Now())
	}
	return false, 0
}

func cliCalendar(flagValue string) string {
	if flagValue != "" {
		return resolveCalendar(flagValue)
	}
	if settings.defaultCalendar != "" {
		return resolveCalendar(settings.defaultCalendar)
	}
	return resolveCalendar("me")
}

func cliRSVP(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rsvp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cal := fs.String("c", "", "calendar alias/email/name (default: config default_calendar)")
	notify := fs.Bool("notify", true, "email the organizer the reply (sendUpdates=all)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: gcl rsvp [-c calendar] [-notify=false] <event id> yes|no|maybe")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return 2
	}
	id, reply := fs.Arg(0), rsvpReplies[strings.ToLower(fs.Arg(1))]
	if reply == "" {
		fmt.Fprintf(stderr, "gcl rsvp: %q is not yes, no or maybe\n", fs.Arg(1))
		return 2
	}
	calendar := cliCalendar(*cal)
	ev, err := rsvpEvent(calendar, id, reply, *notify)
	if err != nil {
		fmt.Fprintln(stderr, "gcl rsvp:", err)
		return 1
	}
	return writeJSON(stdout, stderr, ev)
}

type rsvpResult struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Response string `json:"response"`
	Before   string `json:"before"`
	Link     string `json:"link,omitempty"`
}

var errNotInvited = errors.New("the calendar's owner is not a guest of this event")

// rsvpEvent sets the self attendee's responseStatus. It reads the event's
// attendees first and sends them all back with only that one changed: a
// patch's attendees replace the whole list, so sending just the self entry
// would drop every other guest. Each attendee goes back as it came (every
// field), so optional guests, comments and +N guests survive.
func rsvpEvent(calendar, eventID, reply string, notify bool) (rsvpResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	at, err := googleToken.accessToken(ctx)
	if err != nil {
		return rsvpResult{}, err
	}
	base := fmt.Sprintf("https://www.googleapis.com/calendar/v3/calendars/%s/events/%s",
		url.PathEscape(resolveCalendarID(calendar)), url.PathEscape(eventID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, nil)
	if err != nil {
		return rsvpResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+at)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return rsvpResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return rsvpResult{}, apiError(resp, "read event")
	}
	var cur struct {
		Summary   string           `json:"summary"`
		HTMLLink  string           `json:"htmlLink"`
		Attendees []map[string]any `json:"attendees"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cur); err != nil {
		return rsvpResult{}, fmt.Errorf("read event: %w", err)
	}
	atts, before, err := withReply(cur.Attendees, reply)
	if err != nil {
		return rsvpResult{}, err
	}
	body, err := json.Marshal(map[string]any{"attendees": atts})
	if err != nil {
		return rsvpResult{}, err
	}
	sendUpdates := "none"
	if notify {
		sendUpdates = "all"
	}
	preq, err := http.NewRequestWithContext(ctx, http.MethodPatch, base+"?sendUpdates="+sendUpdates, strings.NewReader(string(body)))
	if err != nil {
		return rsvpResult{}, err
	}
	preq.Header.Set("Authorization", "Bearer "+at)
	preq.Header.Set("Content-Type", "application/json")
	presp, err := http.DefaultClient.Do(preq)
	if err != nil {
		return rsvpResult{}, err
	}
	defer presp.Body.Close()
	if presp.StatusCode != http.StatusOK {
		return rsvpResult{}, apiError(presp, "rsvp")
	}
	return rsvpResult{ID: eventID, Title: cleanText(cur.Summary), Response: reply, Before: before, Link: cur.HTMLLink}, nil
}

// withReply returns the attendees with the self one's responseStatus set to
// reply, and its reply before. The others are the same maps, untouched.
func withReply(atts []map[string]any, reply string) ([]map[string]any, string, error) {
	out := make([]map[string]any, len(atts))
	before, found := "", false
	for i, a := range atts {
		if self, _ := a["self"].(bool); self && !found {
			c := make(map[string]any, len(a))
			for k, v := range a {
				c[k] = v
			}
			before, _ = a["responseStatus"].(string)
			c["responseStatus"] = reply
			out[i], found = c, true
			continue
		}
		out[i] = a
	}
	if !found {
		return nil, "", errNotInvited
	}
	return out, before, nil
}

type findResult struct {
	Window   string        `json:"window"`
	Duration int           `json:"duration"`
	People   []string      `json:"people"`
	Errors   []findError   `json:"errors,omitempty"`
	Slots    []findSlotOut `json:"slots"`
}

type findError struct {
	Calendar string `json:"calendar"`
	Error    string `json:"error"`
}

type findSlotOut struct {
	Start   string   `json:"start"`
	End     string   `json:"end"`
	AllFree bool     `json:"allFree"`
	Busy    []string `json:"busy,omitempty"`
	Unknown []string `json:"unknown,omitempty"`
}

func cliFind(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := flag.NewFlagSet("find", flag.ContinueOnError)
	fs.SetOutput(stderr)
	with := fs.String("with", "", "comma-separated emails or calendar aliases to meet (you are added)")
	dur := fs.Int("d", settings.slotDuration, "meeting length in minutes")
	days := fs.Int("days", settings.slotSearchDays, "days ahead to search")
	max := fs.Int("n", 10, "slots to print (most people free first, then soonest)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: gcl find -with a@x.com,b@x.com [-d 30] [-days 14] [-n 10]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	people := cliFindPeople(*with)
	if len(people) == 0 || *dur < 5 || *dur > 8*60 || *days < 1 || *days > 62 || *max < 1 {
		fs.Usage()
		return 2
	}
	search := defaultSlotSearch(now, time.Local)
	search.duration = time.Duration(*dur) * time.Minute
	search.to = now.AddDate(0, 0, *days)
	search.maxResults = *max
	results, err := queryFreeBusy(people, search.from, search.to)
	if err != nil {
		fmt.Fprintln(stderr, "gcl find:", err)
		return 1
	}
	return writeJSON(stdout, stderr, findOutput(people, results, search))
}

// cliFindPeople resolves -with to calendar ids and adds the person's own.
func cliFindPeople(with string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if v = strings.TrimSpace(v); v == "" {
			return
		}
		id := resolveCalendarID(resolveCalendar(v))
		if id == "" || seen[strings.ToLower(id)] {
			return
		}
		seen[strings.ToLower(id)] = true
		out = append(out, id)
	}
	for _, v := range strings.Split(with, ",") {
		add(v)
	}
	if len(out) == 0 {
		return nil
	}
	add(cliCalendar(""))
	return out
}

func findOutput(people []string, results []freeBusyResult, search slotSearch) findResult {
	out := findResult{Window: describeSlotWindow(search), Duration: int(search.duration / time.Minute), People: people, Slots: []findSlotOut{}}
	for _, r := range results {
		if r.err != "" {
			out.Errors = append(out.Errors, findError{Calendar: r.calendar, Error: r.err})
		}
	}
	sort.Slice(out.Errors, func(i, j int) bool { return out.Errors[i].Calendar < out.Errors[j].Calendar })
	for _, s := range findSlots(results, search) {
		out.Slots = append(out.Slots, findSlotOut{Start: s.start.Format(time.RFC3339), End: s.end.Format(time.RFC3339), AllFree: s.allFree(), Busy: s.busy, Unknown: s.unknown})
	}
	return out
}

func writeJSON(stdout, stderr io.Writer, v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(stderr, "gcl:", err)
		return 1
	}
	return 0
}

// exitSubcommand runs a subcommand and exits when args name one.
func exitSubcommand() {
	if ok, code := runSubcommand(os.Args[1:], os.Stdout, os.Stderr); ok {
		os.Exit(code)
	}
}
