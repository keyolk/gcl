package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "~"
}

func wrap(s string, width int) string {
	return strings.Join(textwrap(s, width), "\n")
}

// textwrap hard-wraps s to `width` display cells per line. It must cut with the
// ANSI-aware slicer: a byte-wise cut lands inside an escape sequence and spills
// the raw `\x1b[...m` bytes into the frame — a styled 40-cell line then measures
// 120 columns and shoves the layout sideways.
func textwrap(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		for lipgloss.Width(para) > width {
			lines = append(lines, ansi.Truncate(para, width, ""))
			para = ansi.TruncateLeft(para, width, "")
		}
		lines = append(lines, para)
	}
	return lines
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func truncateLines(s string, maxLines int, width int) string {
	if maxLines <= 0 {
		return ""
	}
	var out []string
	for _, raw := range strings.Split(s, "\n") {
		wrapped := textwrap(raw, width)
		for _, line := range wrapped {
			if len(out) >= maxLines {
				if len(out) > 0 {
					out[len(out)-1] = truncate(out[len(out)-1], max(1, width-1)) + "~"
				}
				return strings.Join(out, "\n")
			}
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
