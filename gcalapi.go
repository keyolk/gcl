package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Google Calendar access reusing gcalcli's stored OAuth credentials.
//
// gcalcli saves a pickled google.oauth2.credentials.Credentials at
// "$HOME/Library/Application Support/gcalcli/oauth". We do not need a real
// pickle parser: the fields we want are stored as SHORT_BINUNICODE strings, so
// we extract client_id / client_secret / refresh_token by scanning for the
// known keys. Then we mint access tokens via the OAuth token endpoint and call
// the Calendar v3 REST API directly — this reaches calendars that are readable
// but NOT subscribed in gcalcli (the whole reason gcalcli returned nothing).

type oauthCreds struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	TokenURI     string
}

type tokenCache struct {
	mu     sync.Mutex
	token  string
	expiry time.Time
	creds  *oauthCreds
}

var googleToken = &tokenCache{}

func gcalcliOAuthPath() string {
	if v := os.Getenv("GCALCLI_OAUTH"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "gcalcli", "oauth")
}

// nextSBU returns the SHORT_BINUNICODE string that immediately follows the given
// key marker in the pickle byte stream. Layout per value:
//
//	0x8c <len byte> "<key>"  0x8c <len byte> "<value>"
func extractPickleString(data []byte, key string) string {
	marker := append([]byte{0x8c, byte(len(key))}, []byte(key)...)
	i := indexBytes(data, marker)
	if i < 0 {
		return ""
	}
	j := i + len(marker)
	// The key may be followed by a MEMOIZE opcode (0x94) before the value's
	// SHORT_BINUNICODE (0x8c) marker.
	if j < len(data) && data[j] == 0x94 {
		j++
	}
	if j >= len(data) || data[j] != 0x8c {
		return ""
	}
	if j+1 >= len(data) {
		return ""
	}
	n := int(data[j+1])
	start := j + 2
	if start+n > len(data) {
		return ""
	}
	return string(data[start : start+n])
}

func indexBytes(haystack, needle []byte) int {
	return strings.Index(string(haystack), string(needle))
}

func loadOAuthCreds() (*oauthCreds, error) {
	data, err := os.ReadFile(gcalcliOAuthPath())
	if err != nil {
		return nil, fmt.Errorf("read gcalcli oauth: %w", err)
	}
	c := &oauthCreds{
		ClientID:     extractPickleString(data, "_client_id"),
		ClientSecret: extractPickleString(data, "_client_secret"),
		RefreshToken: extractPickleString(data, "_refresh_token"),
		TokenURI:     extractPickleString(data, "_token_uri"),
	}
	if c.TokenURI == "" {
		c.TokenURI = "https://oauth2.googleapis.com/token"
	}
	if c.ClientID == "" || c.ClientSecret == "" || c.RefreshToken == "" {
		return nil, errors.New("could not extract client_id/secret/refresh_token from gcalcli oauth")
	}
	return c, nil
}

func (t *tokenCache) accessToken(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && time.Now().Before(t.expiry.Add(-60*time.Second)) {
		return t.token, nil
	}
	if t.creds == nil {
		c, err := loadOAuthCreds()
		if err != nil {
			return "", err
		}
		t.creds = c
	}
	form := url.Values{
		"client_id":     {t.creds.ClientID},
		"client_secret": {t.creds.ClientSecret},
		"refresh_token": {t.creds.RefreshToken},
		"grant_type":    {"refresh_token"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.creds.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("token refresh failed: %s %s", tok.Error, tok.ErrorDesc)
	}
	t.token = tok.AccessToken
	t.expiry = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return t.token, nil
}

type apiEvent struct {
	ID             string      `json:"id"`
	Summary        string      `json:"summary"`
	Location       string      `json:"location"`
	HTMLLink       string      `json:"htmlLink"`
	Status         string      `json:"status"`
	Start          apiTime     `json:"start"`
	End            apiTime     `json:"end"`
	Description    string      `json:"description"`
	HangoutLink    string      `json:"hangoutLink"`
	Organizer      apiPerson   `json:"organizer"`
	Attendees      []apiPerson `json:"attendees"`
	ConferenceData struct {
		EntryPoints []struct {
			URI string `json:"uri"`
		} `json:"entryPoints"`
	} `json:"conferenceData"`
	Reminders struct {
		UseDefault bool `json:"useDefault"`
		Overrides  []struct {
			Method  string `json:"method"`
			Minutes int    `json:"minutes"`
		} `json:"overrides"`
	} `json:"reminders"`
}

type apiTime struct {
	Date     string `json:"date"`
	DateTime string `json:"dateTime"`
}

type apiPerson struct {
	Email          string `json:"email"`
	DisplayName    string `json:"displayName"`
	ResponseStatus string `json:"responseStatus"`
	Self           bool   `json:"self"`
	Resource       bool   `json:"resource"`
	Organizer      bool   `json:"organizer"`
}

// CalendarEntry is one subscribed/accessible calendar from calendarList.
type CalendarEntry struct {
	ID         string
	Name       string
	AccessRole string
	Primary    bool
}

var (
	calListMu    sync.Mutex
	calListCache []CalendarEntry
	// nameToID maps a lowercased calendar display name to its id, so alias
	// display names (e.g. "My Team Calendar") resolve to the
	// group.calendar.google.com id the API actually needs.
	nameToID map[string]string
	// defaultReminderMins maps a calendar id to its default reminder "minutes
	// before start" list, used for events with reminders.useDefault=true.
	defaultReminderMins map[string][]int
)

func listCalendars() ([]CalendarEntry, error) {
	calListMu.Lock()
	defer calListMu.Unlock()
	if calListCache != nil {
		return calListCache, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	at, err := googleToken.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := "https://www.googleapis.com/calendar/v3/users/me/calendarList?maxResults=250&fields=items(id,summary,summaryOverride,accessRole,primary,defaultReminders)"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+at)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("calendarList API %d", resp.StatusCode)
	}
	var out struct {
		Items []struct {
			ID               string `json:"id"`
			Summary          string `json:"summary"`
			SummaryOverride  string `json:"summaryOverride"`
			AccessRole       string `json:"accessRole"`
			Primary          bool   `json:"primary"`
			DefaultReminders []struct {
				Method  string `json:"method"`
				Minutes int    `json:"minutes"`
			} `json:"defaultReminders"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	entries := make([]CalendarEntry, 0, len(out.Items))
	m := map[string]string{}
	defReminders := map[string][]int{}
	for _, it := range out.Items {
		name := it.SummaryOverride
		if name == "" {
			name = it.Summary
		}
		name = strings.TrimSpace(name)
		entries = append(entries, CalendarEntry{ID: it.ID, Name: name, AccessRole: it.AccessRole, Primary: it.Primary})
		if name != "" {
			m[strings.ToLower(name)] = it.ID
		}
		var mins []int
		for _, r := range it.DefaultReminders {
			mins = append(mins, r.Minutes)
		}
		if len(mins) > 0 {
			defReminders[it.ID] = mins
		}
	}
	calListCache = entries
	nameToID = m
	defaultReminderMins = defReminders
	return entries, nil
}

// calendarDefaultReminders returns the calendar's default reminder minutes,
// loading the calendar list if needed. Empty when none are configured.
func calendarDefaultReminders(calendarID string) []int {
	if defaultReminderMins == nil {
		_, _ = listCalendars()
	}
	return defaultReminderMins[calendarID]
}

// primaryCalendarEmail returns the id of the primary calendar, which for a
// personal Google account is the account's own email. Empty if unavailable.
func primaryCalendarEmail() string {
	cals, err := listCalendars()
	if err != nil {
		return ""
	}
	for _, c := range cals {
		if c.Primary {
			return c.ID
		}
	}
	return ""
}

// resolveCalendarID turns an alias display name into the calendar id the API
// needs. Emails and raw ids pass through unchanged.
func resolveCalendarID(calendar string) string {
	if strings.Contains(calendar, "@") {
		return calendar
	}
	if nameToID == nil {
		_, _ = listCalendars()
	}
	if id, ok := nameToID[strings.ToLower(strings.TrimSpace(calendar))]; ok {
		return id
	}
	return calendar
}

// fetchEventsAPI queries Calendar v3 events.list for a calendar id (email or
// calendar name). start/end are inclusive-exclusive local dates.
func fetchEventsAPI(calendar string, start, end time.Time) ([]Event, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	at, err := googleToken.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	calendar = resolveCalendarID(calendar)

	var events []Event
	pageToken := ""
	for page := 0; page < 20; page++ { // safety cap
		q := url.Values{
			"timeMin":      {start.Format(time.RFC3339)},
			"timeMax":      {end.Format(time.RFC3339)},
			"singleEvents": {"true"},
			"orderBy":      {"startTime"},
			"maxResults":   {"2500"},
		}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		endpoint := fmt.Sprintf("https://www.googleapis.com/calendar/v3/calendars/%s/events?%s",
			url.PathEscape(calendar), q.Encode())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+at)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return nil, fmt.Errorf("calendar not found or no access: %s", calendar)
		}
		if resp.StatusCode == http.StatusForbidden {
			resp.Body.Close()
			return nil, fmt.Errorf("no permission to read calendar: %s", calendar)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("calendar API %d for %s", resp.StatusCode, calendar)
		}
		var out struct {
			Items         []apiEvent `json:"items"`
			NextPageToken string     `json:"nextPageToken"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()
		for _, it := range out.Items {
			if it.Status == "cancelled" {
				continue
			}
			if ev := apiEventToEvent(calendar, it); ev != nil {
				events = append(events, *ev)
			}
		}
		if out.NextPageToken == "" {
			break
		}
		pageToken = out.NextPageToken
	}
	return events, nil
}

func apiEventToEvent(calendar string, it apiEvent) *Event {
	var startDate time.Time
	var startTime, endTime string
	var startAt, endAt time.Time
	if it.Start.DateTime != "" {
		t, err := time.Parse(time.RFC3339, it.Start.DateTime)
		if err != nil {
			return nil
		}
		startAt = t
		lt := t.Local()
		startDate = time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, time.Local)
		startTime = lt.Format("15:04")
		if it.End.DateTime != "" {
			if te, err := time.Parse(time.RFC3339, it.End.DateTime); err == nil {
				endAt = te
				endTime = te.Local().Format("15:04")
			}
		}
	} else if it.Start.Date != "" {
		t, err := time.ParseInLocation("2006-01-02", it.Start.Date, time.Local)
		if err != nil {
			return nil
		}
		startDate = t
	} else {
		return nil
	}

	title := cleanText(it.Summary)
	if title == "" {
		title = "(no title)"
	}
	loc := cleanText(it.Location)
	desc := cleanText(it.Description)

	var attendees []string
	var rooms []string
	for _, a := range it.Attendees {
		if a.Resource {
			// Meeting rooms / equipment come through as resource attendees.
			name := a.DisplayName
			if name == "" {
				name = a.Email
			}
			if name != "" {
				rooms = appendUnique(rooms, name)
			}
			continue
		}
		name := a.Email
		if name == "" {
			name = a.DisplayName
		}
		if name == "" {
			continue
		}
		label := name
		switch a.ResponseStatus {
		case "accepted":
			label = "✓ " + name
		case "declined":
			label = "✗ " + name
		case "tentative":
			label = "? " + name
		}
		attendees = appendUnique(attendees, label)
	}

	var confURI string
	if len(it.ConferenceData.EntryPoints) > 0 {
		confURI = it.ConferenceData.EntryPoints[0].URI
	}

	// Reminder minutes: explicit overrides win; otherwise fall back to the
	// calendar's default reminders when the event opts into them.
	var reminderMins []int
	if len(it.Reminders.Overrides) > 0 {
		for _, r := range it.Reminders.Overrides {
			reminderMins = append(reminderMins, r.Minutes)
		}
	} else if it.Reminders.UseDefault {
		reminderMins = calendarDefaultReminders(calendar)
	}

	ev := &Event{
		ID:            it.ID,
		StartDate:     startDate,
		StartTime:     startTime,
		EndTime:       endTime,
		StartAt:       startAt,
		EndAt:         endAt,
		Title:         title,
		Location:      loc,
		Rooms:         rooms,
		Description:   desc,
		Calendar:      calendar,
		AttendeeEmail: firstAttendeeEmail(it),
		Attendees:     attendees,
		HTMLLink:      it.HTMLLink,
		ConferenceURI: confURI,
		ReminderMins:  reminderMins,
	}
	ev.Links = uniqueLinks([]string{
		confURI,
		it.HangoutLink,
		it.HTMLLink,
		loc,
		desc,
		title,
	})
	return ev
}

func recentCalendarsPath() string {
	return cachePath("recent-calendars")
}

// loadRecentCalendars returns previously-selected calendar ids/emails, most
// recent first.
func loadRecentCalendars() []string {
	data, err := os.ReadFile(recentCalendarsPath())
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// rememberCalendar prepends value to the recent list (dedup, cap 30).
func rememberCalendar(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	recent := loadRecentCalendars()
	next := []string{value}
	for _, r := range recent {
		if r != value {
			next = append(next, r)
		}
	}
	if len(next) > 30 {
		next = next[:30]
	}
	_ = os.MkdirAll(cacheDir(), 0o755)
	_ = os.WriteFile(recentCalendarsPath(), []byte(strings.Join(next, "\n")+"\n"), 0o644)
}

// createEventInput describes a new event to insert.
type createEventInput struct {
	Calendar  string    // calendar id/email/alias to create on
	Title     string    // summary
	Start     time.Time // absolute start (already in the intended tz)
	End       time.Time // absolute end
	Attendees []string  // attendee emails
	Notify    bool      // sendUpdates=all when true
}

// createEvent inserts a new event via Calendar v3 events.insert. When Notify is
// true, Google emails invitations to attendees. Returns the created event's
// htmlLink for confirmation.
func createEvent(in createEventInput) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	at, err := googleToken.accessToken(ctx)
	if err != nil {
		return "", err
	}
	calID := resolveCalendarID(in.Calendar)

	type apiDateTime struct {
		DateTime string `json:"dateTime"`
		TimeZone string `json:"timeZone,omitempty"`
	}
	type apiAttendeeReq struct {
		Email string `json:"email"`
	}
	payload := struct {
		Summary   string           `json:"summary"`
		Start     apiDateTime      `json:"start"`
		End       apiDateTime      `json:"end"`
		Attendees []apiAttendeeReq `json:"attendees,omitempty"`
	}{
		Summary: in.Title,
		Start:   apiDateTime{DateTime: in.Start.Format(time.RFC3339)},
		End:     apiDateTime{DateTime: in.End.Format(time.RFC3339)},
	}
	for _, a := range in.Attendees {
		a = strings.TrimSpace(a)
		if a != "" {
			payload.Attendees = append(payload.Attendees, apiAttendeeReq{Email: a})
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	sendUpdates := "none"
	if in.Notify {
		sendUpdates = "all"
	}
	endpoint := fmt.Sprintf("https://www.googleapis.com/calendar/v3/calendars/%s/events?sendUpdates=%s",
		url.PathEscape(calID), sendUpdates)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+at)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error.Message != "" {
			return "", fmt.Errorf("create failed (%d): %s", resp.StatusCode, e.Error.Message)
		}
		return "", fmt.Errorf("create failed (%d)", resp.StatusCode)
	}
	var created struct {
		HTMLLink string `json:"htmlLink"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)
	return created.HTMLLink, nil
}

// patchEventInput describes fields to update on an existing event.
type patchEventInput struct {
	Calendar  string
	EventID   string
	Title     string
	Start     time.Time
	End       time.Time
	Attendees []string // full desired attendee set (replaces existing)
	Notify    bool
}

// patchEvent updates an existing event via events.patch. Sends update emails
// to attendees when Notify is true.
func patchEvent(in patchEventInput) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	at, err := googleToken.accessToken(ctx)
	if err != nil {
		return "", err
	}
	calID := resolveCalendarID(in.Calendar)

	type apiDateTime struct {
		DateTime string `json:"dateTime"`
	}
	type apiAttendeeReq struct {
		Email string `json:"email"`
	}
	payload := map[string]any{
		"summary": in.Title,
		"start":   apiDateTime{DateTime: in.Start.Format(time.RFC3339)},
		"end":     apiDateTime{DateTime: in.End.Format(time.RFC3339)},
	}
	// Always send attendees (even empty) so removals take effect.
	atts := make([]apiAttendeeReq, 0, len(in.Attendees))
	for _, a := range in.Attendees {
		if a = strings.TrimSpace(a); a != "" {
			atts = append(atts, apiAttendeeReq{Email: a})
		}
	}
	payload["attendees"] = atts

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sendUpdates := "none"
	if in.Notify {
		sendUpdates = "all"
	}
	endpoint := fmt.Sprintf("https://www.googleapis.com/calendar/v3/calendars/%s/events/%s?sendUpdates=%s",
		url.PathEscape(calID), url.PathEscape(in.EventID), sendUpdates)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+at)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", apiError(resp, "update")
	}
	var updated struct {
		HTMLLink string `json:"htmlLink"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&updated)
	return updated.HTMLLink, nil
}

// deleteEvent removes an event via events.delete. Notifies attendees when true.
func deleteEvent(calendar, eventID string, notify bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	at, err := googleToken.accessToken(ctx)
	if err != nil {
		return err
	}
	calID := resolveCalendarID(calendar)
	sendUpdates := "none"
	if notify {
		sendUpdates = "all"
	}
	endpoint := fmt.Sprintf("https://www.googleapis.com/calendar/v3/calendars/%s/events/%s?sendUpdates=%s",
		url.PathEscape(calID), url.PathEscape(eventID), sendUpdates)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+at)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// 200 or 204 = success; 410 = already deleted (treat as success).
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusGone {
		return nil
	}
	return apiError(resp, "delete")
}

func apiError(resp *http.Response, action string) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if e.Error.Message != "" {
		return fmt.Errorf("%s failed (%d): %s", action, resp.StatusCode, e.Error.Message)
	}
	return fmt.Errorf("%s failed (%d)", action, resp.StatusCode)
}

func firstAttendeeEmail(it apiEvent) string {
	if it.Organizer.Email != "" {
		return it.Organizer.Email
	}
	for _, a := range it.Attendees {
		if a.Email != "" {
			return a.Email
		}
	}
	return ""
}
