package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

func TestHuman_IssueColumnsAndStateReasons(t *testing.T) {
	out, stderr, code := runFake(servicetest.New(servicetest.DefaultFixture()), true, "list", "--state", "all", "--no-color")
	if code != 0 || stderr != "" {
		t.Fatal(code, stderr)
	}
	for _, part := range []string{"LABELS", "ASSIGNEES", "bug, sync", "reviewer", "○  6", "✓  3", "⊘  2"} {
		if !strings.Contains(out, part) {
			t.Fatalf("missing %q:\n%s", part, out)
		}
	}
	if strings.Contains(out, "ago") {
		t.Fatal("list times must use short form")
	}
}
func TestHuman_PRFieldsAndDraftGlyph(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.PRs[0].PullRequest.Draft = true
	var out, stderr bytes.Buffer
	deps := fakeDeps(servicetest.New(fixture), &out, &stderr, true)
	deps.Width = func(io.Writer) int { return 140 }
	code := Run(context.Background(), []string{"pr", "list", "--state", "all", "--no-color"}, deps)
	if code != 0 || stderr.Len() != 0 {
		t.Fatal(code, stderr.String())
	}
	for _, part := range []string{"AUTHOR", "BRANCH", "REVIEW", "CHECKS", "pkieltyka", "feature/core", "approved", "◇  101", "✕  102", "●  103"} {
		if !strings.Contains(out.String(), part) {
			t.Fatalf("missing %q:\n%s", part, out.String())
		}
	}
}
func TestHuman_ColoursUseThemeRoles(t *testing.T) {
	for _, theme := range []string{"dark", "light"} {
		var out, stderr bytes.Buffer
		deps := fakeDeps(servicetest.New(servicetest.DefaultFixture()), &out, &stderr, true)
		deps.Getenv = func(string) string { return theme }
		code := Run(context.Background(), []string{"list", "--state", "all"}, deps)
		if code != 0 {
			t.Fatal(code)
		}
		r := runtime{deps: normaliseDeps(deps), mode: Human}
		for _, pair := range [][2]string{{"open", "○"}, {"done", "✓"}, {"inactive", "⊘"}} {
			if !strings.Contains(out.String(), r.paint(pair[0], pair[1])) {
				t.Fatalf("missing %s colour in %s", pair[0], out.String())
			}
		}
	}
}
func TestHuman_ViewBoardStatusAndSpacing(t *testing.T) {
	for _, status := range []string{"none", "no-status", "Todo"} {
		fixture := servicetest.DefaultFixture()
		switch status {
		case "none":
			fixture.Issues[0].Issue.Board = nil
		case "no-status":
			fixture.Issues[0].Issue.Board = &domain.Placement{}
		default:
			fixture.Issues[0].Issue.Board = &domain.Placement{Status: &status}
		}
		out, stderr, code := runFake(servicetest.New(fixture), true, "view", "6", "--no-color")
		if code != 0 || stderr != "" {
			t.Fatal(code, stderr)
		}
		expected := status
		if status == "none" {
			expected = "—"
		}
		if status == "no-status" {
			expected = "No status"
		}
		plain := ansi.Strip(out)
		if !strings.Contains(plain, "Board: "+expected) || !strings.HasSuffix(out, "\n") || !strings.Contains(plain, "\n\n── reviewer · 1 hour ago\n  ") || strings.Contains(plain, "2026-") {
			t.Fatal(plain)
		}
		for _, line := range strings.Split(out, "\n") {
			if ansi.StringWidth(line) > 80 {
				t.Fatalf("line exceeds width: %q", line)
			}
		}
	}
}
func TestHuman_EmptyAndTruncatedUseState(t *testing.T) {
	for _, pr := range []bool{false, true} {
		for _, state := range []string{"open", "closed", "all"} {
			fixture := servicetest.DefaultFixture()
			fixture.Issues = nil
			fixture.PRs = nil
			args := []string{"list", "--state", state, "--no-color"}
			noun := "issues"
			if pr {
				args = append([]string{"pr"}, args...)
				noun = "pull requests"
			}
			if state != "all" {
				noun = state + " " + noun
			}
			out, stderr, code := runFake(servicetest.New(fixture), true, args...)
			if code != 0 || stderr != "" || out != "No "+noun+".\n" {
				t.Fatal(args, code, out, stderr)
			}
		}
	}
	out, _, _ := runFake(servicetest.New(servicetest.DefaultFixture()), true, "list", "--limit", "2", "--no-color")
	if !strings.Contains(out, "Showing 2 of 4 open issues. Use --limit N or --all for more.") {
		t.Fatal(out)
	}
}
func TestHuman_TablesFitAndAlignUnicode(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.Issues[0].Issue.Title = strings.Repeat("猫👩‍💻", 40)
	fixture.Labels[0].Name = strings.Repeat("label", 20)
	fixture.Labels[0].Description = strings.Repeat("description ", 20)
	fixture.Users[0].Login = strings.Repeat("login", 20)
	fixture.Users[0].Name = strings.Repeat("名前", 30)
	fixture.Projects[0].Title = strings.Repeat("project", 30)
	for _, width := range []int{40, 80, 140} {
		for _, args := range [][]string{{"list", "--state", "all"}, {"pr", "list", "--state", "all"}, {"labels"}, {"assignees"}, {"board", "list"}, {"board"}} {
			var out, stderr bytes.Buffer
			deps := fakeDeps(servicetest.New(fixture), &out, &stderr, true)
			deps.Width = func(io.Writer) int { return width }
			if code := Run(context.Background(), append(args, "--no-color"), deps); code != 0 {
				t.Fatal(code, stderr.String())
			}
			for _, line := range strings.Split(out.String(), "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("%v width=%d line=%q", args, width, line)
				}
			}
		}
	}
	out, _, _ := runFake(servicetest.New(servicetest.DefaultFixture()), true, "labels", "--no-color")
	lines := strings.Split(out, "\n")
	if strings.Index(lines[0], "DESCRIPTION") != strings.Index(lines[1], "Something") {
		t.Fatal("columns not aligned", out)
	}
}
func TestHuman_DoctorGlyphsAndFixColumn(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.ConfigCheck = domain.DoctorCheck{Name: "Config", Detail: "bad config", Hint: "repair config", OK: false}
	out, stderr, code := runFake(servicetest.New(fixture), true, "doctor", "--no-color")
	if code != 0 || stderr != "" || !strings.Contains(out, "✓ Credentials") || !strings.Contains(out, "✗ Config") || !strings.Contains(out, "fix: repair config") || strings.Contains(out, "Board access:") {
		t.Fatal(code, out, stderr)
	}
}
func TestHuman_RelativeWeekMonthYearThresholds(t *testing.T) {
	for _, test := range []struct {
		age  time.Duration
		want string
	}{{time.Second, "1s"}, {time.Hour, "1h"}, {24 * time.Hour, "1d"}, {7 * 24 * time.Hour, "1w"}, {30 * 24 * time.Hour, "1mo"}, {365 * 24 * time.Hour, "1y"}} {
		if got := relative(timestamp(fixtureTime.Add(-test.age)), fixtureTime); got != test.want {
			t.Fatal(got, test.want)
		}
	}
}
func TestHuman_ThemeCaseAndInvalidWarning(t *testing.T) {
	outputs := map[string]string{}
	for _, theme := range []string{"light", " Light ", "LIGHT", "invalid"} {
		var out, stderr bytes.Buffer
		deps := fakeDeps(servicetest.New(servicetest.DefaultFixture()), &out, &stderr, true)
		deps.Getenv = func(string) string { return theme }
		code := Run(context.Background(), []string{"view", "6"}, deps)
		if code != 0 {
			t.Fatal(code, stderr.String())
		}
		outputs[theme] = out.String()
		if strings.Contains(stderr.String(), "invalid TODOS_THEME") != (theme == "invalid") {
			t.Fatal(theme, stderr.String())
		}
	}
	if outputs["light"] != outputs[" Light "] || outputs["light"] != outputs["LIGHT"] {
		t.Fatal("theme matching differs by case")
	}
	for _, mode := range []OutputMode{Piped, JSON} {
		var out, stderr bytes.Buffer
		deps := fakeDeps(servicetest.New(servicetest.DefaultFixture()), &out, &stderr, false)
		deps.Getenv = func(string) string { return "invalid" }
		args := []string{"view", "6"}
		if mode == JSON {
			args = append(args, "--json")
		}
		if code := Run(context.Background(), args, deps); code != 0 || stderr.Len() != 0 {
			t.Fatal(mode, code, stderr.String())
		}
	}
}

func TestHuman_PlainBodyHasNoTrailingPadding(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.Issues[0].Body = "## Scope\n\nBody with blank lines.\n\n- item\n"
	out, stderr, code := runFake(servicetest.New(fixture), true, "view", "6", "--no-color")
	if code != 0 || stderr != "" {
		t.Fatal(code, stderr)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasSuffix(line, " ") {
			t.Fatalf("trailing padding: %q", line)
		}
	}
	if !strings.Contains(out, "\n  Body with blank lines.\n\n") {
		t.Fatal(out)
	}
}
func TestHuman_BoardColumnsShareWidths(t *testing.T) {
	var out bytes.Buffer
	r := &runtime{deps: normaliseDeps(Dependencies{Width: func(io.Writer) int { return 100 }}), mode: Human, global: GlobalOptions{NoColor: true}}
	a, b := "One", "Two"
	board := BoardDTO{Columns: []ColumnDTO{{Status: &a, Cards: []CardDTO{{Number: 1, Title: "First", Labels: []string{"tiny"}, Assignees: []string{"bob"}}}}, {Status: &b, Cards: []CardDTO{{Number: 2, Title: "Second", Labels: []string{"much-longer-label"}, Assignees: []string{"alexander_12"}}}}}}
	if err := renderHuman(context.Background(), r, &out, board); err != nil {
		t.Fatal(err)
	}
	var first, second string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "#1") {
			first = line
		}
		if strings.Contains(line, "#2") {
			second = line
		}
	}
	if strings.Index(first, "tiny") != strings.Index(second, "much-longer-label") || strings.Index(first, "bob") != strings.Index(second, "alexander_12") {
		t.Fatal("columns differ between sections", out.String())
	}
}
