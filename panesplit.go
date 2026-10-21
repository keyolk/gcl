package main

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/split"
)

// The schedule | detail boundary is resizable (tuikit split). gcl keeps its
// automatic orientation rule (splitMode): the detail pane sits to the RIGHT on
// wide terminals and at the BOTTOM on narrow ones. The two orientations mean
// different things (columns vs rows), so each has its own split.Model and its
// own saved ratio, and the keys act on whichever is on screen.
//
// The models live on the model as plain values; their configuration is applied
// here on every use instead of in a constructor, so the zero model of a test
// literal lays out exactly like the real program.

// Split keys. gcl binds `< >` to staged day moves and `+` to duplicate, so the
// kit's `< >` cannot be used without shadowing; `[ ]` are free in every
// schedule stack. `=` (reset) is free and stays the kit's key.
const (
	splitShrinkKey = "["
	splitGrowKey   = "]"
	splitResetKey  = "="
)

// rightSplit configures the side-by-side split. The look and the default are
// what the fixed layout drew: a one-cell divider, the schedule at least 30
// columns, the detail pane at least 34 and about a third of the width.
func rightSplit(s split.Model) split.Model {
	s.Orientation = split.Horizontal
	s.Divider = 1
	s.MinA, s.MinB = 30, 34
	s.Default = 2.0 / 3
	s.Primary = split.A
	return s
}

// bottomSplit configures the stacked split. The fixed layout had no divider
// row between the panes, so this keeps none: the boundary resizes with the
// keys only (a zero-height divider has no cell to press). The detail pane
// keeps its 6-row minimum and the schedule its 4.
func bottomSplit(s split.Model) split.Model {
	s.Orientation = split.Vertical
	s.Divider = 0
	s.MinA, s.MinB = 4, 6
	s.Default = 2.0 / 3
	s.Primary = split.A
	return s
}

// paneSplit is the configured split for kind.
func (m model) paneSplit(kind splitKind) split.Model {
	if kind == splitBottom {
		return bottomSplit(m.splitBottom)
	}
	return rightSplit(m.splitRight)
}

func (m model) withPaneSplit(kind splitKind, s split.Model) model {
	if kind == splitBottom {
		m.splitBottom = s
	} else {
		m.splitRight = s
	}
	return m
}

// splitShown reports that a two-pane split is on screen, so the resize keys
// have a boundary to move.
func (l paneLayout) splitShown() bool {
	return l.split != splitNone && l.detail.W > 0 && l.detail.H > 0
}

// splitBindings are the kit's resize actions under gcl's keys, in the
// schedule context every agenda/grid stack shares.
func splitBindings() []keys.Binding {
	remap := map[string]string{
		split.ActionShrink: splitShrinkKey,
		split.ActionGrow:   splitGrowKey,
		split.ActionReset:  splitResetKey,
	}
	// Shrink first, so the grouped hint reads "[] split", left key first.
	order := map[string]int{split.ActionShrink: 0, split.ActionGrow: 1, split.ActionReset: 2}
	bs := split.Bindings(ctxSchedule)
	slices.SortFunc(bs, func(a, b keys.Binding) int { return order[a.Action] - order[b.Action] })
	for i := range bs {
		bs[i].Keys = []string{remap[bs[i].Action]}
		bs[i].Help = map[string]string{
			split.ActionShrink: "shrink the schedule (detail pane grows)",
			split.ActionGrow:   "grow the schedule (detail pane shrinks)",
			split.ActionReset:  "reset the schedule / detail split",
		}[bs[i].Action]
		if bs[i].Action != split.ActionReset {
			// Lowest tier: the bar names resizing only when nothing more
			// useful fits, as one "split" hint for both keys.
			bs[i].Footer, bs[i].FooterGroup = prioExtra, "split"
		}
	}
	return bs
}

func isSplitAction(action string) bool {
	switch action {
	case split.ActionGrow, split.ActionShrink, split.ActionReset:
		return true
	}
	return false
}

// runSplit moves the boundary on screen by one key step. A press that cannot
// move it (at a limit, or no split at this size) says so instead of doing
// nothing silently.
func (m model) runSplit(action string) (tea.Model, tea.Cmd) {
	l := m.paneLayout()
	if !l.splitShown() {
		m.say("no detail pane at this size to resize")
		return m, nil
	}
	s, r := m.paneSplit(l.split).Handle(action, l.splitArea)
	m = m.withPaneSplit(l.split, s)
	if !r.Changed {
		m.say("the split is at its limit")
	}
	// The ratio is part of uiState: Update's saveUIState queues the write.
	return m, nil
}

// splitMouse gives a mouse event to the split before the selection and the
// click handlers (the kit's order). It reports whether the split took it: a
// press on the divider, the drag that follows, its release.
func (m model) splitMouse(msg tea.MouseMsg) (model, bool) {
	if m.width <= 0 || m.height <= 0 || m.mode != modeNormal || m.yankPending || m.bodyIsPlaceholder() {
		return m, false
	}
	l := m.paneLayout()
	if !l.splitShown() {
		return m, false
	}
	// A tiny terminal can lay the split out past the body's last row; a press
	// there lands on the status bar, which is not the divider.
	if mouse.IsLeftPress(msg) && !l.bodyRect().Contains(msg.X, msg.Y) {
		return m, false
	}
	s, r := m.paneSplit(l.split).Update(msg, l.splitArea)
	return m.withPaneSplit(l.split, s), r.Consumed
}
