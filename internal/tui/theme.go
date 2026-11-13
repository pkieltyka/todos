package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/pkieltyka/todos/internal/markdown"
)

// Theme holds the colour roles of UI 6.1 for one background.
type Theme struct {
	Dark bool

	Text, Muted, Subtle, Accent, SelectionBg color.Color
	Open, Done, Inactive, Pending, Error     color.Color

	dimmed bool
}

// NewTheme builds the roles for a dark or light background (UI 6.1).
func NewTheme(dark bool) Theme {
	ld := lipgloss.LightDark(dark)
	c := lipgloss.Color
	return Theme{
		Dark:        dark,
		Text:        ld(c("#1F2328"), c("#E6EDF3")),
		Muted:       ld(c("#656D76"), c("#8B949E")),
		Subtle:      ld(c("#D0D7DE"), c("#3D444D")),
		Accent:      ld(c("#0969DA"), c("#58A6FF")),
		SelectionBg: ld(c("#DDF4FF"), c("#1C2B3F")),
		Open:        ld(c("#1A7F37"), c("#3FB950")),
		Done:        ld(c("#8250DF"), c("#A371F7")),
		Inactive:    ld(c("#656D76"), c("#8B949E")),
		Pending:     ld(c("#9A6700"), c("#D29922")),
		Error:       ld(c("#CF222E"), c("#F85149")),
	}
}

// Dimmed maps every foreground to Muted and drops backgrounds; it draws the
// backdrop behind an overlay (UI 5.1).
func (t Theme) Dimmed() Theme {
	d := t
	for _, p := range []*color.Color{&d.Text, &d.Subtle, &d.Accent, &d.Open, &d.Done, &d.Inactive, &d.Pending, &d.Error} {
		*p = t.Muted
	}
	d.SelectionBg = nil
	d.dimmed = true
	return d
}

// ParseOverride reads TODOS_THEME: "light" or "dark".
func ParseOverride(v string) (dark, ok bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "light":
		return false, true
	case "dark":
		return true, true
	}
	return false, false
}

// LabelColor is a label's own colour, or Muted when GitHub's value is malformed.
func LabelColor(t Theme, hex string) color.Color {
	if t.dimmed || len(hex) != 6 {
		return t.Muted
	}
	for _, r := range hex {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return t.Muted
		}
	}
	return lipgloss.Color("#" + hex)
}

// MarkdownStyle is the glamour style matching the theme.
func (t Theme) MarkdownStyle() markdown.Style {
	if t.Dark {
		return markdown.Dark
	}
	return markdown.Light
}

// fg returns a style with the given foreground.
func (t Theme) fg(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

func (t Theme) text() lipgloss.Style    { return t.fg(t.Text) }
func (t Theme) muted() lipgloss.Style   { return t.fg(t.Muted) }
func (t Theme) subtle() lipgloss.Style  { return t.fg(t.Subtle) }
func (t Theme) accent() lipgloss.Style  { return t.fg(t.Accent) }
func (t Theme) pending() lipgloss.Style { return t.fg(t.Pending) }
func (t Theme) errorS() lipgloss.Style  { return t.fg(t.Error) }
func (t Theme) bold() lipgloss.Style    { return t.fg(t.Text).Bold(true) }

// selected paints a full-width selected row.
func (t Theme) selected(s string) string {
	if t.SelectionBg == nil {
		return s
	}
	return lipgloss.NewStyle().Background(t.SelectionBg).Render(s)
}
