package main

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// colorTheme applies the theme against a 256-color renderer with the given
// background, restoring the default theme when the test ends.
func colorTheme(t *testing.T, dark bool) {
	t.Helper()
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.ANSI256)
	r.SetHasDarkBackground(dark)
	applyTheme(newTheme(r))
	t.Cleanup(func() { applyTheme(newTheme(nil)) })
}

func TestDarkTerminalKeepsTheOriginalColors(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	colorTheme(t, true)
	for name, c := range map[string]struct {
		st   lipgloss.Style
		want string
	}{
		"error":   {errorStyle, "91"},
		"day":     {dayStyle, "93"},
		"pending": {pendingStyle, "38;5;214"},
		"cursor":  {timeSlotSelStyle, "48;5;81"},
	} {
		if got := c.st.Render("x"); !strings.Contains(got, c.want) {
			t.Errorf("%s = %q, want %s", name, got, c.want)
		}
	}
}

func TestLightTerminalGetsReadableForegrounds(t *testing.T) {
	// Bright yellow (11) day headers are unreadable on white; the light value
	// must be used there.
	t.Setenv("NO_COLOR", "")
	colorTheme(t, false)
	if got := dayStyle.Render("x"); strings.Contains(got, ";93m") {
		t.Errorf("light terminal still draws day headers in bright yellow: %q", got)
	}
}

func TestNoColorKeepsTheCursorVisible(t *testing.T) {
	// A focused grid day was marked only by a background color; with colors
	// removed it must fall back to reverse video rather than disappear.
	t.Setenv("NO_COLOR", "1")
	colorTheme(t, true)
	got := timeSlotSelStyle.Render("x")
	if strings.Contains(got, "38;5") || strings.Contains(got, "48;5") {
		t.Errorf("NO_COLOR cursor still carries color: %q", got)
	}
	if !strings.Contains(got, "7") || !strings.Contains(got, "\x1b[") {
		t.Errorf("NO_COLOR cursor lost reverse video: %q", got)
	}
}
