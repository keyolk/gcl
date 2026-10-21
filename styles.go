package main

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/theme"
)

// Colors are named by role, not by hue (engine principle E10). Render code asks
// for errorStyle or pendingStyle; which color that is, how it adapts to a light
// terminal and what NO_COLOR leaves of it are decided here once.
//
// The Dark values are the 256-color codes gcl always used, so a dark terminal
// looks exactly as before. Light values exist for the roles drawn as plain
// foreground on the terminal's own background (a bright yellow day header is
// unreadable on white). Filled pills carry their own background, so they keep
// one value for both.

// palette is gcl's set of roles: the kit's shared ones plus the pills and bars
// only gcl draws.
type palette struct {
	theme.Roles
	BarFg, BarBg, BarMeta lipgloss.AdaptiveColor // top bar and its meta segment
	PillBg                lipgloss.AdaptiveColor // date/time pills
	HintFg, HintBg        lipgloss.AdaptiveColor // the key hint bar
	Day                   lipgloss.AdaptiveColor // day headers, weekday row
	Now                   lipgloss.AdaptiveColor // the "now" divider
	Time                  lipgloss.AdaptiveColor // time column of an agenda row
	Cursor                lipgloss.AdaptiveColor // selected row, focused grid day
	OnFill                lipgloss.AdaptiveColor // dark text on a bright fill
	CalendarBg            lipgloss.AdaptiveColor // calendar pill
	TimezoneBg            lipgloss.AdaptiveColor // timezone pill
	ActiveFg, ActiveBg    lipgloss.AdaptiveColor // "in effect now" counter
	WarnFill              lipgloss.AdaptiveColor // amber fills: staged change, reload, partial slot
	SuccessFill           lipgloss.AdaptiveColor // green fill: a slot everyone can make
}

func gclPalette() palette {
	c := func(light, dark string) lipgloss.AdaptiveColor {
		return lipgloss.AdaptiveColor{Light: light, Dark: dark}
	}
	same := func(v string) lipgloss.AdaptiveColor { return c(v, v) }
	roles := theme.DefaultRoles()
	roles.Muted = c("244", "8")
	roles.Info = c("26", "39") // links
	roles.Success = c("28", "10")
	roles.Warn = c("130", "214")
	roles.Error = c("160", "9")
	roles.Accent = same("63") // modal frames, section titles
	roles.Selected = c("254", "236")
	roles.Border = c("250", "240")
	roles.BorderFocus = c("32", "75")
	return palette{
		Roles:       roles,
		BarFg:       same("230"),
		BarBg:       same("62"),
		BarMeta:     same("189"),
		PillBg:      same("60"),
		HintFg:      c("236", "252"),
		HintBg:      c("254", "237"),
		Day:         c("136", "11"),
		Now:         c("160", "203"),
		Time:        c("31", "117"),
		Cursor:      c("25", "81"),
		OnFill:      same("232"),
		CalendarBg:  same("212"),
		TimezoneBg:  same("150"),
		ActiveFg:    same("231"),
		ActiveBg:    same("124"),
		WarnFill:    same("214"),
		SuccessFill: same("77"),
	}
}

// ui is the kit theme the toast queue, footer and help render with. It is set
// with the styles below by applyTheme.
var ui theme.Theme

var (
	topBarStyle       lipgloss.Style
	dayStyle          lipgloss.Style
	dayHeaderStyle    lipgloss.Style
	nowLineStyle      lipgloss.Style
	sectionTitleStyle lipgloss.Style
	selectedStyle     lipgloss.Style
	cardStyle         lipgloss.Style
	dividerStyle      lipgloss.Style // the schedule | detail split's divider
	// detailStyle intentionally uses NO border of any kind: many terminals treat
	// box-drawing and other ambiguous-width glyphs as double-width, which desyncs
	// the frame and clips the bottom edge. Plain-space padding (always width 1)
	// gives separation with zero ambiguous-width characters.
	detailStyle lipgloss.Style
	// modalStyle is for floating overlays (create/edit, pickers, confirm) where a
	// visible frame helps them stand out over the body. It keeps a rounded border;
	// overlays are short so the occasional double-width terminal quirk is minor.
	modalStyle lipgloss.Style
	pillStyle  lipgloss.Style
	// Compact agenda row styles.
	selectedRowStyle lipgloss.Style
	timePillMini     lipgloss.Style
	timeSlotSelStyle lipgloss.Style
	mutedStyle       lipgloss.Style
	linkStyle        lipgloss.Style
	statusStyle      lipgloss.Style
	errorStyle       lipgloss.Style
	hintBarStyle     lipgloss.Style
	calPillStyle     lipgloss.Style
	metaStyle        lipgloss.Style
	tzPillStyle      lipgloss.Style
	// loadingPillStyle flags a background reload while the old agenda stays up.
	loadingPillStyle lipgloss.Style
	// activePillStyle marks the header's "in effect right now" counter. Red
	// background because it is a state, not a navigation aid.
	activePillStyle lipgloss.Style
	// Staged (unsaved) time changes: amber, so "not yet real" never reads the
	// same as a committed time.
	pendingStyle    lipgloss.Style
	pendingBarStyle lipgloss.Style
	// warnStyle marks "legal but probably a mistake" at the confirm step —
	// amber, not red: errorStyle is for things that will fail or reach other
	// people, and a warning that shouts as loudly as those stops being read.
	warnStyle lipgloss.Style
	// A fully-available slot is the answer; a partial one is a compromise. The
	// two must not be told apart by reading digits.
	slotAllFreeStyle lipgloss.Style
	slotPartialStyle lipgloss.Style
)

func init() { applyTheme(newTheme(nil)) }

// newTheme builds the kit theme with gcl's palette against r (the default
// renderer when nil). NO_COLOR and GCL_ASCII are honoured there.
func newTheme(r *lipgloss.Renderer) theme.Theme {
	return theme.NewWith(r, gclPalette().Roles, theme.DetectGlyphs(appName))
}

// applyTheme rebuilds every style from th. Under NO_COLOR each style keeps its
// bold, padding and borders but no color; the two cursor styles fall back to
// reverse video, since a cursor drawn only by a background color would vanish.
func applyTheme(th theme.Theme) {
	ui = th
	p := gclPalette()
	r := th.Renderer()
	noColor := th.NoColor()
	fg := func(s lipgloss.Style, c lipgloss.AdaptiveColor) lipgloss.Style {
		if noColor {
			return s
		}
		return s.Foreground(c)
	}
	fill := func(s lipgloss.Style, f, b lipgloss.AdaptiveColor) lipgloss.Style {
		if noColor {
			return s
		}
		return s.Foreground(f).Background(b)
	}
	border := func(s lipgloss.Style, c lipgloss.AdaptiveColor) lipgloss.Style {
		s = s.Border(lipgloss.RoundedBorder())
		if noColor {
			return s
		}
		return s.BorderForeground(c)
	}
	bold := r.NewStyle().Bold(true)
	pill := bold.Padding(0, 1)

	topBarStyle = fill(pill, p.BarFg, p.BarBg)
	dayStyle = fg(bold, p.Day)
	dayHeaderStyle = fg(bold, p.Day)
	nowLineStyle = fg(bold, p.Now)
	sectionTitleStyle = fill(pill, p.BarFg, p.Accent)
	selectedStyle = border(r.NewStyle(), p.BorderFocus).Padding(0, 1)
	if !noColor {
		selectedStyle = selectedStyle.Background(p.Selected)
	}
	cardStyle = border(r.NewStyle(), p.Border).Padding(0, 1)
	dividerStyle = fg(r.NewStyle(), p.Border)
	detailStyle = r.NewStyle().PaddingLeft(2).PaddingRight(1)
	modalStyle = border(r.NewStyle(), p.Accent).Padding(0, 1)
	pillStyle = fill(pill, p.BarFg, p.PillBg)
	selectedRowStyle = fg(bold, p.Cursor)
	timePillMini = fg(r.NewStyle(), p.Time)
	timeSlotSelStyle = fill(bold, p.OnFill, p.Cursor)
	mutedStyle = fg(r.NewStyle(), p.Muted)
	linkStyle = fg(r.NewStyle(), p.Info)
	statusStyle = fg(r.NewStyle(), p.Success)
	errorStyle = fg(bold, p.Error)
	hintBarStyle = fill(r.NewStyle(), p.HintFg, p.HintBg)
	calPillStyle = fill(pill, p.OnFill, p.CalendarBg)
	metaStyle = fill(r.NewStyle().Padding(0, 1), p.BarMeta, p.BarBg)
	tzPillStyle = fill(pill, p.OnFill, p.TimezoneBg)
	loadingPillStyle = fill(pill, p.OnFill, p.WarnFill)
	activePillStyle = fill(pill, p.ActiveFg, p.ActiveBg)
	pendingStyle = fg(bold, p.Warn)
	pendingBarStyle = fill(bold, p.OnFill, p.WarnFill)
	warnStyle = fg(bold, p.Warn)
	slotAllFreeStyle = fill(bold, p.OnFill, p.SuccessFill)
	slotPartialStyle = fill(bold, p.OnFill, p.WarnFill)
	if noColor {
		timeSlotSelStyle = timeSlotSelStyle.Reverse(true)
		pendingBarStyle = pendingBarStyle.Reverse(true)
	}
}
