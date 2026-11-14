package tui

import (
	"context"
	"image/color"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/pkieltyka/todos/internal/markdown"
)

func TestKeymapSameKeySameAction(t *testing.T) {
	// Context-dependent keys, documented in tui.md 9.4.
	allowed := map[string]bool{
		"enter":     true, // Open, Apply: never active in the same context
		"tab":       true, // NextView, NextField: fields only in text mode
		"shift+tab": true,
		"y":         true, // CopyURL, Yes: Yes only inside a Confirm
		"o":         true, // Browser, Overwrite: Overwrite only inside Conflict
	}
	k := DefaultKeymap()
	byKey := map[string][]Action{}
	for a := Action(0); a < actCount; a++ {
		for _, s := range k.Binding(a).Keys() {
			byKey[s] = append(byKey[s], a)
		}
	}
	var bad []string
	for s, acts := range byKey {
		if len(acts) > 1 && !allowed[s] {
			bad = append(bad, s)
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("keys bound to several actions: %v", bad)
	}
	for a := Action(0); a < actCount; a++ {
		if len(k.Binding(a).Keys()) == 0 {
			t.Errorf("action %d has no key", a)
		}
	}
}

func TestKeymap_textModeKeysFreeInTextarea(t *testing.T) {
	km := textarea.DefaultKeyMap()
	v := reflect.ValueOf(km)
	bound := map[string]bool{}
	for i := 0; i < v.NumField(); i++ {
		if b, ok := v.Field(i).Interface().(key.Binding); ok {
			for _, s := range b.Keys() {
				bound[s] = true
			}
		}
	}
	for _, s := range []string{"ctrl+s", "ctrl+r", "ctrl+o", "ctrl+x"} {
		if bound[s] {
			t.Errorf("%s is bound by the textarea", s)
		}
	}
}

func TestFooter_fromActions_fitsWidth(t *testing.T) {
	for _, w := range []int{80, 100, 140} {
		h := newHarness(t, richFixture())
		h.size(w, 24)
		lines := strings.Split(h.view(), "\n")
		footer := lines[len(lines)-1]
		if lipgloss.Width(footer) != w {
			t.Fatalf("width %d: footer is %d wide", w, lipgloss.Width(footer))
		}
		for _, want := range []string{"j/k move", "enter open", "? help", "1/4"} {
			if !strings.Contains(footer, want) {
				t.Fatalf("width %d: footer %q lacks %q", w, footer, want)
			}
		}
	}
}

func TestHelp_listsEveryEnabledAction(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("?")
	for _, want := range []string{"Help · Issues", "Navigate", "Views & app", "j/k ↓/↑", "/", "filter",
		"s", "state", "only mine", "open in browser", "copy URL", "refresh", "quit"} {
		h.contains(want)
	}
	for _, want := range []string{"Issue actions", "new issue", "edit title/body", "assignees", "labels", "comment",
		"close / reopen", "close as not planned", "board status", "move on board", "delete"} {
		h.contains(want) // Phase 6: the issue actions are listed
	}
	h.contains("quick add") // Phase 8
	h.press("esc")
	h.lacks("Help ·")
	// On the board, its own keys are listed.
	h.press("3", "?")
	h.contains("choose project")
	h.contains("hidden items")
}

func TestTextMode_lettersGoToInput(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("/")
	h.typeText("sq")
	if h.quit {
		t.Fatal("q quit while typing in the filter")
	}
	if got := h.app.store.Issues.Query.State; got != "open" {
		t.Fatalf("s changed the state filter to %q", got)
	}
	h.contains("/sq")
	h.press("backspace", "esc")
	h.lacks("/s")
	h.contains("filter: —")
}

func TestFilter_narrowsLoadedRows(t *testing.T) {
	h := newHarness(t, richFixture())
	before := len(h.calls("ListIssues"))
	h.press("/")
	h.typeText("flaky")
	h.press("enter")
	h.contains("1 of 4")
	h.contains("#6")
	h.lacks("#5 ")
	if len(h.calls("ListIssues")) != before {
		t.Fatal("filtering asked GitHub")
	}
	h.press("esc") // clears in navigation mode
	h.contains("4 open issues")
}

func TestOverlay_blocksGlobalActions(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("?", "2")
	if h.app.active != ViewIssues {
		t.Fatal("tab switched under the help overlay")
	}
	h.press("q")
	if h.quit {
		t.Fatal("q quit under the help overlay")
	}
}

func TestQuit_q(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("q")
	if !h.quit {
		t.Fatal("q did not quit")
	}
	h = newHarness(t, richFixture())
	h.press("ctrl+c")
	if !h.quit {
		t.Fatal("ctrl+c did not quit")
	}
}

func TestTheme_overrideSkipsQuery(t *testing.T) {
	h := newHarness(t, richFixture(), withTheme("light"))
	if h.app.theme.Dark || !h.app.themeFixed {
		t.Fatal("override not applied")
	}
	h.send(tea.BackgroundColorMsg{Color: color.Black})
	if h.app.theme.Dark {
		t.Fatal("background answer overrode TODOS_THEME")
	}
}

func TestTheme_backgroundMsgSwitchesAndMarkdownStyle(t *testing.T) {
	h := newHarness(t, richFixture(), withTheme(""))
	if !h.app.theme.Dark {
		t.Fatal("not dark before the answer")
	}
	h.send(tea.BackgroundColorMsg{Color: color.White})
	if h.app.theme.Dark || h.app.theme.MarkdownStyle() != 1 {
		t.Fatal("light background not applied")
	}
}

func TestTheme_badLabelColourFallsBack(t *testing.T) {
	th := NewTheme(true)
	for _, bad := range []string{"", "12345", "zzzzzz", "#d73a4a"} {
		if LabelColor(th, bad) != th.Muted {
			t.Errorf("%q did not fall back", bad)
		}
	}
	if LabelColor(th, "d73a4a") == th.Muted {
		t.Fatal("valid colour fell back")
	}
}

func TestTheme_dimmedMapsForegroundsToMuted(t *testing.T) {
	th := NewTheme(false)
	d := th.Dimmed()
	for _, c := range []color.Color{d.Text, d.Accent, d.Open, d.Error, d.Pending} {
		if c != th.Muted {
			t.Fatal("foreground not muted")
		}
	}
	if d.SelectionBg != nil || LabelColor(d, "d73a4a") != th.Muted {
		t.Fatal("dimmed theme keeps colours")
	}
}

func TestParseOverride(t *testing.T) {
	for in, want := range map[string][2]bool{"light": {false, true}, "DARK": {true, true}, "": {false, false}, "blue": {false, false}} {
		d, ok := ParseOverride(in)
		if d != want[0] || ok != want[1] {
			t.Errorf("%q → %v %v", in, d, ok)
		}
	}
}

func TestNoColor_plainMarkdownAndASCIIProfile(t *testing.T) {
	h := newHarness(t, richFixture(), func(_ *harness, d *Deps, _ *Options) { d.Env.NoColor = true })
	if h.app.theme.MarkdownStyle() != markdown.Plain {
		t.Fatal("Markdown not Plain with --no-color")
	}
	h.send(tea.BackgroundColorMsg{Color: color.White}) // a later theme change keeps it
	if h.app.theme.MarkdownStyle() != markdown.Plain {
		t.Fatal("background answer dropped --no-color")
	}
	if n := len(programOptions(context.Background(), Env{NoColor: true})); n != 2 {
		t.Fatalf("%d program options, want the colour profile too", n)
	}
	if n := len(programOptions(context.Background(), Env{})); n != 1 {
		t.Fatalf("%d program options without --no-color", n)
	}
}

// Dogfood (Phase 4): while typing in the filter the footer shows the
// filter's keys, not navigation keys that would be typed instead.
func TestFooter_textModeShowsFilterKeys(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("/")
	lines := strings.Split(h.view(), "\n")
	footer := lines[len(lines)-1]
	if !strings.Contains(footer, "enter keep filter") || !strings.Contains(footer, "esc clear filter") || strings.Contains(footer, "j/k") {
		t.Fatalf("footer while filtering: %q", footer)
	}
}

// Dogfood (Phase 4): at wide widths the spelled-out time is not truncated.
func TestIssueList_wideUpdatedFits(t *testing.T) {
	h := newHarness(t, richFixture())
	h.advance(10 * 24 * time.Hour * 30) // "10 months ago"
	h.size(140, 40)
	h.contains("10 months ago")
	h.lacks("months a…")
}

// Phase 9 section 4: the help screens of the issue views list the writing
// keys too (forms, comments, quick add), and shift+tab.
func TestHelp_listsWritingKeysAndPrevView(t *testing.T) {
	for _, v := range []ViewID{ViewIssues, ViewBoard} {
		h := newHarness(t, richFixture(), withStart(v))
		h.press("?")
		for _, s := range []string{"quick add", "Writing · forms, comments, quick add", "ctrl+o", "$EDITOR",
			"ctrl+r", "preview", "done: skip/close", "discard draft", "next/prev view", "next/prev field"} {
			h.contains(s)
		}
	}
	h := newHarness(t, richFixture(), withStart(ViewPRs))
	h.press("?")
	h.lacks("Writing")
}
