package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Rect is a region of the screen in cells.
type Rect struct{ X, Y, W, H int }

// Frame is the four bands of every screen (UI 1).
type Frame struct{ Header, SubBar, Body, Footer Rect }

const (
	minTerminalWidth  = 65
	minTerminalHeight = 24
	compactWidth      = 80
)

// ComputeFrame lays out the bands, or reports that the window is too small.
func ComputeFrame(w, h int) (Frame, bool) {
	if w < minTerminalWidth || h < minTerminalHeight {
		return Frame{}, false
	}
	return Frame{
		Header: Rect{0, 0, w, 1},
		SubBar: Rect{0, 1, w, 1},
		Body:   Rect{0, 2, w, h - 3},
		Footer: Rect{0, h - 1, w, 1},
	}, true
}

// ColID names a list column.
type ColID int

const (
	ColGutter ColID = iota
	ColState
	ColNumber
	ColTitle
	ColUpdated
	ColAssignees
	ColLabels
	ColStatus
	ColChecks
	ColReview
	ColBranch
	ColAuthor
)

// Col describes a column for Fit.
type Col struct {
	ID       ColID
	Min, Max int // cells; Max 0 = flexible
	Drop     int // higher drops first; 0 = never dropped
	MinWidth int // only shown when the width is at least this (0 = always)
	Need     int // what the flexible column would like; 0 = everything left
}

// Placed is a column with its position.
type Placed struct {
	ID   ColID
	X, W int
}

// Fit chooses the visible columns and their widths for width cells, with a
// one-cell gap between columns (tui.md 10.2).
func Fit(cols []Col, width int) []Placed {
	var cs []Col
	for _, c := range cols {
		if c.MinWidth == 0 || width >= c.MinWidth {
			cs = append(cs, c)
		}
	}
	total := func(ws []int) int {
		n := 0
		for _, w := range ws {
			n += w
		}
		return n + max(0, len(ws)-1)
	}
	widths := func() []int {
		ws := make([]int, len(cs))
		for i, c := range cs {
			ws[i] = c.Min
		}
		return ws
	}
	ws := widths()
	for total(ws) > width {
		drop, best := -1, 0
		for i, c := range cs {
			if c.Drop > best {
				drop, best = i, c.Drop
			}
		}
		if drop < 0 {
			break // nothing left to drop; the caller's minimums do not fit
		}
		cs = append(cs[:drop:drop], cs[drop+1:]...)
		ws = widths()
	}
	free := width - total(ws)
	// The flexible column first, up to its need.
	for i, c := range cs {
		if c.Max == 0 {
			grow := free
			if c.Need > 0 {
				grow = min(free, max(0, c.Need-ws[i]))
			}
			ws[i] += grow
			free -= grow
		}
	}
	// Then the others towards Max, in ascending Drop order.
	order := make([]int, len(cs))
	for i := range order {
		order[i] = i
	}
	for a := 1; a < len(order); a++ {
		for b := a; b > 0 && cs[order[b]].Drop < cs[order[b-1]].Drop; b-- {
			order[b], order[b-1] = order[b-1], order[b]
		}
	}
	for _, i := range order {
		if cs[i].Max > 0 && free > 0 {
			grow := min(free, cs[i].Max-ws[i])
			ws[i] += grow
			free -= grow
		}
	}
	// Whatever is left goes to the flexible column, so rows fill the width.
	for i, c := range cs {
		if c.Max == 0 {
			ws[i] += free
			free = 0
		}
	}
	out := make([]Placed, len(cs))
	x := 0
	for i, c := range cs {
		out[i] = Placed{ID: c.ID, X: x, W: ws[i]}
		x += ws[i] + 1
	}
	return out
}

// BoardWindow chooses the column width and which columns are visible
// (tui.md 10.4).
func BoardWindow(bodyW, n, focus, offset int) (colW, first, count int) {
	// 3 cells for "‹N" on the left and 3 for "N›" on the right, a one-cell
	// gap between columns: count*colW + (count-1) + 6 <= bodyW.
	colW = clamp((bodyW-5)/max(n, 1)-1, 24, 40)
	fit := max(1, (bodyW-5)/(colW+1))
	count = min(fit, n)
	first = clampWindow(offset, focus, count, n)
	return colW, first, count
}

func clampWindow(offset, focus, count, n int) int {
	if count >= n {
		return 0
	}
	if focus < offset {
		offset = focus
	}
	if focus >= offset+count {
		offset = focus - count + 1
	}
	return clamp(offset, 0, n-count)
}

// clampOffset keeps offset unless the selected row would leave the viewport,
// then scrolls the minimum distance.
func clampOffset(offset, sel, height, n int) int {
	if height <= 0 || n <= 0 {
		return 0
	}
	if sel < offset {
		offset = sel
	}
	if sel >= offset+height {
		offset = sel - height + 1
	}
	return clamp(offset, 0, max(0, n-height))
}

// reselect keeps the selected issue by number after the rows change
// (tui.md 7.8).
func reselect(rows []int, sel, prevIndex, offset, height int) (newSel, newOffset int) {
	if i, ok := indexOf(rows, sel); ok {
		return sel, clampOffset(offset, i, height, len(rows))
	}
	if len(rows) == 0 {
		return 0, 0
	}
	i := clamp(prevIndex, 0, len(rows)-1)
	return rows[i], clampOffset(offset, i, height, len(rows))
}

func indexOf(rows []int, n int) (int, bool) {
	for i, v := range rows {
		if v == n {
			return i, true
		}
	}
	return 0, false
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return max(lo, min(v, hi))
}

// fit truncates s with "…" or pads it with spaces to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	if pad := w - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// spread places left and right in exactly w cells, truncating left first.
func spread(left, right string, w int) string {
	rw := lipgloss.Width(right)
	if rw >= w {
		return fit(right, w)
	}
	return fit(left, w-rw-1) + " " + right
}

// center places s in the middle of w cells.
func center(s string, w int) string {
	sw := lipgloss.Width(s)
	if sw >= w {
		return fit(s, w)
	}
	left := (w - sw) / 2
	return fit(strings.Repeat(" ", left)+s, w)
}

// box pads or cuts lines to exactly h lines of w cells.
func box(lines []string, w, h int) string {
	out := make([]string, h)
	for i := range out {
		if i < len(lines) {
			out[i] = fit(lines[i], w)
		} else {
			out[i] = strings.Repeat(" ", w)
		}
	}
	return strings.Join(out, "\n")
}
