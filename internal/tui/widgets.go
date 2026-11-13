package tui

import (
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/pkieltyka/todos/internal/domain"
)

// lineInput is the one-line filter field. It is deliberately tiny: bubbles'
// textinput starts its own blink timer, which the test harness cannot drive.
type lineInput struct {
	value []rune
}

func (l *lineInput) String() string { return string(l.value) }
func (l *lineInput) Clear()         { l.value = nil }

// key applies an editing key and reports enter and esc.
func (l *lineInput) key(k tea.KeyPressMsg) (enter, esc bool) {
	switch k.String() {
	case "enter":
		return true, false
	case "esc":
		return false, true
	case "backspace", "ctrl+h":
		if len(l.value) > 0 {
			l.value = l.value[:len(l.value)-1]
		}
	case "ctrl+u":
		l.value = nil
	case "ctrl+w", "alt+backspace":
		v := strings.TrimRightFunc(string(l.value), unicode.IsSpace)
		i := strings.LastIndexFunc(v, unicode.IsSpace)
		l.value = []rune(v[:i+1])
	default:
		l.insert(k.Text)
	}
	return false, false
}

func (l *lineInput) insert(s string) {
	for _, r := range s {
		if unicode.IsPrint(r) && r != '\n' && r != '\t' {
			l.value = append(l.value, r)
		}
	}
}

// ---- glyphs ----

func issueGlyph(t Theme, i domain.Issue) string {
	if i.State == "closed" {
		if i.StateReason != nil && *i.StateReason == "not_planned" {
			return t.fg(t.Inactive).Render("⊘")
		}
		return t.fg(t.Done).Render("✓")
	}
	return t.fg(t.Open).Render("○")
}

func issueStateWord(t Theme, i domain.Issue) string {
	switch {
	case i.State != "closed":
		return t.fg(t.Open).Render("○ Open")
	case i.StateReason != nil && *i.StateReason == "not_planned":
		return t.fg(t.Inactive).Render("⊘ Closed as not planned")
	}
	return t.fg(t.Done).Render("✓ Closed")
}

// issueStateShort is the state for the 32-cell side panel, where "Closed as
// not planned" does not fit (dogfood, Phase 4).
func issueStateShort(t Theme, i domain.Issue) string {
	switch {
	case i.State != "closed":
		return t.fg(t.Open).Render("○ Open")
	case i.StateReason != nil && *i.StateReason == "not_planned":
		return t.fg(t.Inactive).Render("⊘ Not planned")
	}
	return t.fg(t.Done).Render("✓ Closed")
}

func prGlyph(t Theme, p domain.PullRequest) string {
	switch {
	case p.State == "merged":
		return t.fg(t.Done).Render("●")
	case p.State == "closed":
		return t.fg(t.Inactive).Render("✕")
	case p.Draft:
		return t.fg(t.Inactive).Render("◇")
	}
	return t.fg(t.Open).Render("○")
}

func prStateWord(t Theme, p domain.PullRequest) string {
	switch {
	case p.State == "merged":
		return t.fg(t.Done).Render("● Merged")
	case p.State == "closed":
		return t.fg(t.Inactive).Render("✕ Closed")
	case p.Draft:
		return t.fg(t.Inactive).Render("◇ Draft")
	}
	return t.fg(t.Open).Render("○ Open")
}

// review is the review roll-up, short or as words (UI 3.3).
func review(t Theme, r domain.ReviewStatus, words bool) string {
	switch r {
	case "approved":
		return t.fg(t.Open).Render("✓ approved")
	case "changes_requested":
		if words {
			return t.fg(t.Error).Render("± changes requested")
		}
		return t.fg(t.Error).Render("± changes")
	case "review_required":
		if words {
			return t.fg(t.Pending).Render("? review required")
		}
		return t.fg(t.Pending).Render("? review")
	}
	return ""
}

// checks is the checks roll-up; pending is static, not animated (UI 3.3).
func checks(t Theme, c domain.ChecksStatus, words bool) string {
	var g, w string
	var col = t.Muted
	switch c {
	case "passing":
		g, w, col = "✓", "✓ passing", t.Open
	case "failing":
		g, w, col = "✗", "✗ failing", t.Error
	case "pending":
		g, w, col = "⠿", "⠿ pending", t.Pending
	default:
		return ""
	}
	if words {
		return t.fg(col).Render(w)
	}
	return t.fg(col).Render(g)
}

// labelNames is the compact label cell: "bug +1", fitting w cells.
func labelNames(t Theme, ls []domain.Label, w int) string {
	if len(ls) == 0 {
		return t.muted().Render("—")
	}
	out := ""
	for i, l := range ls {
		cand := out
		if cand != "" {
			cand += " "
		}
		cand += l.Name
		rest := len(ls) - i - 1
		suffix := ""
		if rest > 0 {
			suffix = " +" + itoa(rest)
		}
		if lipgloss.Width(cand+suffix) > w && i > 0 {
			return t.text().Render(out) + t.muted().Render(" +"+itoa(len(ls)-i))
		}
		out = cand
	}
	return t.text().Render(out)
}

// labelDots is the wide label cell: "● bug  ● sync" in each label's colour.
func labelDots(t Theme, ls []domain.Label) string {
	if len(ls) == 0 {
		return t.muted().Render("—")
	}
	parts := make([]string, len(ls))
	for i, l := range ls {
		parts[i] = t.fg(LabelColor(t, l.Color)).Render("●") + " " + t.text().Render(l.Name)
	}
	return strings.Join(parts, "  ")
}

// people is "@first +N", or "—".
func people(t Theme, logins []string) string {
	if len(logins) == 0 {
		return t.muted().Render("—")
	}
	s := "@" + logins[0]
	if len(logins) > 1 {
		return t.text().Render(s) + t.muted().Render(" +"+itoa(len(logins)-1))
	}
	return t.text().Render(s)
}

func itoa(n int) string { return thousands(n) }

// rule is a horizontal line, optionally carrying a label: "── label ────".
func rule(t Theme, label string, w int) string {
	if label == "" {
		return t.subtle().Render(strings.Repeat("─", w))
	}
	head := "── " + label + " "
	rest := w - lipgloss.Width(head)
	if rest < 0 {
		return fit(t.muted().Render(head), w)
	}
	return t.muted().Render(head) + t.subtle().Render(strings.Repeat("─", rest))
}

// matches is the local text filter: title, number, labels, assignees and
// author, case-insensitive substring (5.2).
func matches(i domain.Issue, q string) bool {
	if q == "" {
		return true
	}
	q = strings.ToLower(q)
	if strings.Contains(strings.ToLower(i.Title), q) || strings.Contains("#"+itoaPlain(i.Number), q) {
		return true
	}
	for _, l := range i.Labels {
		if strings.Contains(strings.ToLower(l.Name), q) {
			return true
		}
	}
	for _, a := range i.Assignees {
		if strings.Contains(strings.ToLower(a), q) {
			return true
		}
	}
	return i.Author != nil && strings.Contains(strings.ToLower(*i.Author), q)
}

func matchesPR(p domain.PullRequest, q string) bool {
	if q == "" {
		return true
	}
	q = strings.ToLower(q)
	if strings.Contains(strings.ToLower(p.Title), q) || strings.Contains("#"+itoaPlain(p.Number), q) ||
		strings.Contains(strings.ToLower(p.Head), q) {
		return true
	}
	for _, l := range p.Labels {
		if strings.Contains(strings.ToLower(l.Name), q) {
			return true
		}
	}
	for _, a := range p.Assignees {
		if strings.Contains(strings.ToLower(a), q) {
			return true
		}
	}
	return p.Author != nil && strings.Contains(strings.ToLower(*p.Author), q)
}

func itoaPlain(n int) string { return strconv.Itoa(n) }

// centered draws message lines in the middle of an area.
func centered(lines []string, area Rect) string {
	top := max(0, (area.H-len(lines))/2)
	out := make([]string, area.H)
	for i := range out {
		if j := i - top; j >= 0 && j < len(lines) {
			out[i] = center(lines[j], area.W)
		}
	}
	return box(out, area.W, area.H)
}

// listNav moves an index for a navigation action over n rows.
func listNav(a Action, i, n, page int) int {
	switch a {
	case ActDown:
		i++
	case ActUp:
		i--
	case ActTop:
		i = 0
	case ActBottom:
		i = n - 1
	case ActHalfDown:
		i += max(1, page/2)
	case ActHalfUp:
		i -= max(1, page/2)
	}
	return clamp(i, 0, max(0, n-1))
}
