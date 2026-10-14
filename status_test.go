package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/keyolk/tuikit/toast"
)

// clockModel is a loaded model whose toast queue reads a test clock.
func clockModel(now *time.Time) model {
	m := loadedModel()
	m.toasts = toast.WithClock(m.toasts, func() time.Time { return *now })
	return m
}

func TestNotesExpireOnTheirOwnTimer(t *testing.T) {
	// The old status string never cleared: "copied description" stayed until
	// some other message replaced it.
	m := loadedModel()
	got, cmd := m.Update(clipboardCopiedMsg{label: "description"})
	gm := got.(model)
	if cmd == nil {
		t.Fatal("no expiry tick was scheduled for the note")
	}
	exp, ok := gm.toasts.ExpiredMsg(gm.toasts.Items()[0].ID())
	if !ok {
		t.Fatal("note has no expiry")
	}
	after, _ := gm.Update(exp)
	if s := after.(model).statusText(); s != "" {
		t.Errorf("note still shown after its tick: %q", s)
	}
}

func TestErrorsStayUntilAKeyAfterTheyWereSeen(t *testing.T) {
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.Local)
	m := clockModel(&now)
	got, cmd := m.Update(eventsMsg{reqID: m.inflightReq, err: errors.New("503 backend error")})
	gm := got.(model)
	if cmd != nil {
		t.Error("an error toast scheduled an expiry; errors must be sticky")
	}
	// A key already in flight when the error landed must not dismiss it unread.
	k, _ := gm.Update(keyMsg("j"))
	if s := k.(model).statusText(); !strings.Contains(s, "503") {
		t.Fatalf("error cleared by a key pressed before it could be read: %q", s)
	}
	now = now.Add(time.Second)
	k, _ = k.(model).Update(keyMsg("j"))
	if s := k.(model).statusText(); strings.Contains(s, "503") {
		t.Errorf("error still shown after a later key: %q", s)
	}
}

func TestGoodLoadReplacesTheLoadError(t *testing.T) {
	m := loadedModel()
	got, _ := m.Update(eventsMsg{reqID: m.inflightReq, err: errors.New("503 backend error")})
	gm := got.(model)
	got, _ = gm.Update(eventsMsg{reqID: gm.inflightReq, events: gm.events, start: gm.loadedStart, end: gm.loadedEnd})
	s := got.(model).statusText()
	if strings.Contains(s, "503") || !strings.Contains(s, "loaded 1 events") {
		t.Errorf("status = %q, want the load result to replace the stale error", s)
	}
}

func TestAReloadDoesNotEraseANote(t *testing.T) {
	// pendingNote existed because scheduleFetch overwrote the status with
	// "loading~". A reload now says nothing in the status line.
	m := loadedModel()
	m.say("timezone: KST")
	_ = m.reload() // never run: it would hit the Google API
	if s := m.statusText(); s != "timezone: KST" {
		t.Errorf("status = %q after a reload, want the note kept", s)
	}
}

func TestFrameFitsWithSeveralToasts(t *testing.T) {
	for _, h := range []int{10, 14, 30} {
		m := loadedModel()
		m.height = h
		m.say("one")
		m.fail(srcLoad, "two")
		m.fail(srcAction, "three")
		out := m.View()
		if n := strings.Count(out, "\n") + 1; n != h {
			t.Errorf("height %d: frame is %d rows", h, n)
		}
		if !strings.Contains(stripANSI(out), "three") {
			t.Errorf("height %d: newest toast not shown:\n%s", h, stripANSI(out))
		}
	}
}

func TestStatusUsesNoAmbiguousEllipsis(t *testing.T) {
	m := loadedModel()
	m.width = 30
	m.say(strings.Repeat("long note ", 10))
	if got := m.statusView(); strings.Contains(got, "…") {
		t.Errorf("status cut with U+2026, which KR/JP terminals draw two cells wide: %q", got)
	}
}
