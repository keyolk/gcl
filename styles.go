package main

import "github.com/charmbracelet/lipgloss"

var (
	topBarStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("62")).
			Padding(0, 1)
	helpStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	dayStyle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	dayHeaderStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	nowLineStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
	sectionTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("230")).
				Background(lipgloss.Color("63")).
				Padding(0, 1)
	selectedStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("75")).
			Background(lipgloss.Color("236")).
			Padding(0, 1)
	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)
	// detailStyle intentionally uses NO border of any kind: many terminals treat
	// box-drawing and other ambiguous-width glyphs as double-width, which desyncs
	// the frame and clips the bottom edge. Plain-space padding (always width 1)
	// gives separation with zero ambiguous-width characters.
	detailStyle = lipgloss.NewStyle().
			PaddingLeft(2).
			PaddingRight(1)
	// modalStyle is for floating overlays (create/edit, pickers, confirm) where a
	// visible frame helps them stand out over the body. It keeps a rounded border;
	// overlays are short so the occasional double-width terminal quirk is minor.
	modalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 1)
	pillStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("60")).Padding(0, 1)
	// Compact agenda row styles.
	selectedRowStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	timePillMini     = lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	timeSlotSelStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("232")).Background(lipgloss.Color("81"))
	mutedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	linkStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	statusStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	errorStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	hintBarStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("237"))
	calPillStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("232")).Background(lipgloss.Color("212")).Padding(0, 1)
	metaStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("189")).Background(lipgloss.Color("62")).Padding(0, 1)
	tzPillStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("232")).Background(lipgloss.Color("150")).Padding(0, 1)
	// loadingPillStyle flags a background reload while the old agenda stays up.
	loadingPillStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("214")).Padding(0, 1)
	// activePillStyle marks the header's "in effect right now" counter. Red
	// background because it is a state, not a navigation aid.
	activePillStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("124")).Padding(0, 1)
	// Staged (unsaved) time changes: amber, so "not yet real" never reads the
	// same as a committed time.
	pendingStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	pendingBarStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("232")).Background(lipgloss.Color("214"))
	// warnStyle marks "legal but probably a mistake" at the confirm step —
	// amber, not red: errorStyle is for things that will fail or reach other
	// people, and a warning that shouts as loudly as those stops being read.
	warnStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
)
