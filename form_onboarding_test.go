package main

import (
	"strings"
	"testing"
)

// formIsFresh reports whether the form has no user input yet — the onboarding
// hint should only show before the user touches a field.
func formIsFresh(c createState) bool {
	return strings.TrimSpace(c.title) == "" && !c.editingField
}

func TestFormOnboardingHintShowsOnFreshForm(t *testing.T) {
	c := &createState{editingField: false}
	if !formIsFresh(*c) {
		t.Fatal("expected fresh form with empty title and no field focus")
	}
}

func TestFormOnboardingHintHidesAfterTitleTyped(t *testing.T) {
	c := &createState{title: "Standup", editingField: false}
	if formIsFresh(*c) {
		t.Fatal("expected onboarding hint to hide once a title is typed")
	}
}

func TestFormOnboardingHintHidesWhileEditingField(t *testing.T) {
	c := &createState{editingField: true}
	if formIsFresh(*c) {
		t.Fatal("expected onboarding hint to hide while a field is being edited")
	}
}
