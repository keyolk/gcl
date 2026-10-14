package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	m := model{status: "loaded 3 events"}
	got, _ := m.Update(notifyResultMsg{fired: 1, shown: []string{"gcal: in 5m  standup"}})
	if s := got.(model).status; !strings.Contains(s, "standup") {
		t.Errorf("status = %q, want the undelivered reminder", s)
	}

	got, _ = m.Update(notifyResultMsg{err: errors.New("token expired")})
	if s := got.(model).status; !strings.Contains(s, "reminder failed") || !strings.Contains(s, "token expired") {
		t.Errorf("status = %q, want the scan error", s)
	}

	// A quiet pass (nothing due) must not clobber whatever status is up.
	got, _ = m.Update(notifyResultMsg{})
	if s := got.(model).status; s != "loaded 3 events" {
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
// the failure has to be visible after the load lands instead of vanishing.
func TestChooseCalendarSurfacesRecentsWriteFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", filepath.Join(blocker, "sub"))

	m := model{mode: modeCalendarPicker, picker: pickerState{kind: pickerCalendar}}
	got, _ := m.choosePicker(pickerItem{label: "team", value: "team@example.com"})
	gm := got.(model)
	if gm.calendar != "team@example.com" {
		t.Fatalf("calendar = %q, want switch to proceed", gm.calendar)
	}
	if !strings.Contains(gm.pendingNote, "save recent calendars") {
		t.Errorf("pendingNote = %q, want the recents write error", gm.pendingNote)
	}
}
