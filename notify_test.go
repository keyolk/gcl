package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/keyolk/tuikit/tmux"
)

// captureStdout runs fn and returns whatever it wrote to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// Outside tmux with no osascript (e.g. Linux), sendToast used to fmt.Println the
// reminder. Called from a Cmd goroutine, that print lands in the alt screen and
// corrupts the TUI frame. It must report "no sink" and print nothing.
func TestSendToastWithoutSinkNeverPrints(t *testing.T) {
	t.Setenv("TMUX", "")
	orig := osascriptPath
	osascriptPath = ""
	defer func() { osascriptPath = orig }()

	var err error
	out := captureStdout(t, func() { err = sendToast("gcal: in 5m  standup") })
	if out != "" {
		t.Errorf("sendToast wrote to stdout: %q", out)
	}
	if !errors.Is(err, errNoToastSink) {
		t.Errorf("err = %v, want errNoToastSink", err)
	}
}

// A reminder that could not be toasted is surfaced in the status line rather
// than dropped; a scan error is shown rather than swallowed.
func TestNotifyResultReachesStatus(t *testing.T) {
	m := model{}
	m.say("loaded 3 events")
	got, _ := m.Update(notifyResultMsg{fired: 1, shown: []string{"gcal: in 5m  standup"}})
	if s := got.(model).statusText(); !strings.Contains(s, "standup") {
		t.Errorf("status = %q, want the undelivered reminder", s)
	}

	got, _ = m.Update(notifyResultMsg{err: errors.New("token expired")})
	if s := got.(model).statusText(); !strings.Contains(s, "reminder failed") || !strings.Contains(s, "token expired") {
		t.Errorf("status = %q, want the scan error", s)
	}

	// A quiet pass (nothing due) must not clobber whatever status is up.
	got, _ = m.Update(notifyResultMsg{})
	if s := got.(model).statusText(); s != "loaded 3 events" {
		t.Errorf("empty scan changed status to %q", s)
	}
}

func TestMarkNotifiedReportsWriteFailure(t *testing.T) {
	// Point the cache dir at a regular file so MkdirAll fails.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", filepath.Join(blocker, "sub"))
	if err := markNotified([]string{"k"}); err == nil {
		t.Fatal("markNotified swallowed the write failure")
	}
}

// Picking a calendar still switches when the recents file can't be written, but
// the failure has to be visible instead of vanishing. The write runs in a Cmd,
// never inside Update.
func TestChooseCalendarSurfacesRecentsWriteFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", filepath.Join(blocker, "sub"))

	m := model{mode: modeCalendarPicker, picker: pickerState{kind: pickerCalendar},
		recents: []string{"old@example.com"}}
	got, _ := m.choosePicker(pickerItem{label: "team", value: "team@example.com"})
	gm := got.(model)
	if gm.calendar != "team@example.com" {
		t.Fatalf("calendar = %q, want switch to proceed", gm.calendar)
	}
	if want := []string{"team@example.com", "old@example.com"}; !slices.Equal(gm.recents, want) {
		t.Errorf("recents = %v, want %v", gm.recents, want)
	}
	// The returned batch also reloads (Google API), so the save Cmd is run on
	// its own.
	msg := saveRecentsCmd(gm.recents)()
	saved, ok := msg.(recentsSavedMsg)
	if !ok || saved.err == nil || !strings.Contains(saved.err.Error(), "save recent calendars") {
		t.Fatalf("save Cmd = %#v, want the write error", msg)
	}
	after, _ := gm.Update(saved)
	if s := after.(model).statusText(); !strings.Contains(s, "save recent calendars") {
		t.Errorf("status = %q, want the recents write error", s)
	}
}

// choosePicker must not write the recents file itself: Update stays free of
// disk IO, and the write happens when the returned Cmd runs.
func TestChooseCalendarDoesNotWriteInsideUpdate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	m := model{mode: modeCalendarPicker, picker: pickerState{kind: pickerCalendar}}
	m.choosePicker(pickerItem{label: "team", value: "team@example.com"})
	if _, err := os.Stat(recentCalendarsPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("recents file touched inside Update: %v", err)
	}
}

// Recents read at startup go behind a pick that landed first.
func TestRecentsLoadKeepsEarlierPicksInFront(t *testing.T) {
	m := model{recents: []string{"new@x.com"}}
	got, _ := m.Update(recentsMsg{list: []string{"a@x.com", "new@x.com", "b@x.com"}})
	want := []string{"new@x.com", "a@x.com", "b@x.com"}
	if r := got.(model).recents; !slices.Equal(r, want) {
		t.Errorf("recents = %v, want %v", r, want)
	}
}

// Inside tmux the reminder flashes the status line through the kit's client:
// never a popup (modal, steals the keyboard), never `-p` (prints instead of
// showing), and with `#` escaped so tmux does not expand the title as a format.
func TestSendToastFlashesTmuxStatusLine(t *testing.T) {
	orig := osascriptPath
	osascriptPath = ""
	defer func() { osascriptPath = orig }()

	f := tmux.NewFake().On("display-message", "", nil)
	if err := sendToastVia(tmux.Client{Runner: f}, "gcal: in 5m  C# sync #{pane_id}"); err != nil {
		t.Fatalf("sendToastVia: %v", err)
	}
	want := []string{"display-message", "--", "gcal: in 5m  C## sync ##{pane_id}"}
	if calls := f.Calls(); len(calls) != 1 || !slices.Equal(calls[0], want) {
		t.Errorf("tmux calls = %q, want one %q", calls, want)
	}
}

// A tmux failure with no other sink is reported, not mistaken for "no sink"
// (which would silently fall back to the status line / stdout).
func TestSendToastReportsTmuxFailure(t *testing.T) {
	orig := osascriptPath
	osascriptPath = ""
	defer func() { osascriptPath = orig }()

	f := tmux.NewFake().On("display-message", "", errors.New("no current client"))
	err := sendToastVia(tmux.Client{Runner: f}, "gcal: in 5m  standup")
	if err == nil || errors.Is(err, errNoToastSink) || !strings.Contains(err.Error(), "no current client") {
		t.Errorf("err = %v, want the tmux failure", err)
	}
}
