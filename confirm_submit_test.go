package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A valid form at the last field.
func validCreateForm() createState {
	return createState{
		step:         stepAttendees, // last field before submit
		title:        "Standup",
		date:         "2026-07-16",
		start:        "15:00",
		durationStr:  "30",
		duration:     30,
		selected:     map[string]bool{},
		editingField: false,
	}
}

// validCreateFormEditing is the same form with an active edit cursor, so
// Enter submits (routes through the confirm overlay rather than
// hitting the API directly).
func validCreateFormEditing() createState {
	c := validCreateForm()
	c.editingField = true
	return c
}

// dispatchKey drives a key through the full Update → handleKey path so the
// routing reflects what the user actually experiences.
func dispatchKey(t *testing.T, m model, key string) model {
	t.Helper()
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "y":
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}
	case "n":
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(key[0])}}
	}
	mm, _ := m.Update(msg)
	return mm.(model)
}

func TestFormEnterRoutesToConfirmSubmit(t *testing.T) {
	m := model{calendar: "me", mode: modeCreate, create: validCreateFormEditing()}
	m = dispatchKey(t, m, "enter")
	if m.mode != modeConfirmSubmit {
		t.Fatalf("Enter while editing should open the confirm-submit overlay, got mode %v", m.mode)
	}
}

func TestConfirmSubmitYesStartsSubmit(t *testing.T) {
	// y/Enter on the confirm overlay kicks off the submit command.
	// The actual API call is asynchronous, so at the synchronous
	// test point the submit is in-flight (submitting=true, mode stays
	// at modeConfirmSubmit); the createdMsg lands later.
	m := model{calendar: "me", mode: modeCreate, create: validCreateFormEditing()}
	m = dispatchKey(t, m, "enter")
	m = dispatchKey(t, m, "y")
	if m.mode != modeConfirmSubmit {
		t.Fatalf("y on confirm-submit should keep the overlay while the submit is in-flight, got mode %v", m.mode)
	}
	if !m.create.submitting {
		t.Fatal("expected submitting=true after y on confirm-submit")
	}
}

func TestConfirmSubmitNoReturnsToForm(t *testing.T) {
	m := model{calendar: "me", mode: modeCreate, create: validCreateFormEditing()}
	m = dispatchKey(t, m, "enter")
	// n/ESC returns to the form, not to normal mode, so an in-progress
	// edit isn't lost.
	m = dispatchKey(t, m, "n")
	if m.mode != modeCreate {
		t.Fatalf("n on confirm-submit should return to the form, got mode %v", m.mode)
	}
}

func TestFormEnterOnInvalidFormStaysInForm(t *testing.T) {
	// An invalid form (empty title) with an active edit cursor should not
	// route to the confirm overlay — Enter stays in the form and surfaces the
	// validation error.
	m := model{calendar: "me", mode: modeCreate, create: createState{step: stepTitle, editingField: true}}
	m = dispatchKey(t, m, "enter")
	if m.mode != modeCreate {
		t.Fatalf("Enter on an invalid form should stay in the form, got mode %v", m.mode)
	}
	if m.create.err == "" {
		t.Fatal("expected a validation error on the invalid form")
	}
}

func TestFormEnterOnFreshFormEntersEditMode(t *testing.T) {
	// A fresh form (editingField=false) at any text field: Enter enters
	// edit mode rather than submitting — the confirm overlay only
	// appears once the user is already editing a field. This guards the
	// non-editing Enter from being treated as a submit.
	m := model{calendar: "me", mode: modeCreate, create: validCreateForm()}
	m = dispatchKey(t, m, "enter")
	if m.mode != modeCreate {
		t.Fatalf("Enter on a fresh form should enter edit mode, not submit, got mode %v", m.mode)
	}
	if !m.create.editingField {
		t.Fatal("expected editingField to be true after Enter on a fresh form")
	}
}
