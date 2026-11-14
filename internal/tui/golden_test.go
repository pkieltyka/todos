package tui

import (
	"os"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/golden"
	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

// Goldens are stable because the clock is fixed, the spinner never ticks in
// the harness, the theme is forced and Markdown renders run to completion
// before the frame is taken (tui.md 18.2). Regenerate with:
//
//	go test ./internal/tui -run TestGolden -update
func TestMain(m *testing.M) {
	for _, v := range []string{"TERM", "COLORTERM", "NO_COLOR", "TODOS_THEME"} {
		_ = os.Unsetenv(v)
	}
	os.Exit(m.Run())
}

type goldenCase struct {
	name  string
	setup func(t *testing.T) *harness
}

var sizes = []struct {
	name string
	w, h int
}{{"80x24", 80, 24}, {"140x40", 140, 40}}

func TestGolden(t *testing.T) {
	cases := []goldenCase{
		{"issue_list_loaded", func(t *testing.T) *harness { return newHarness(t, richFixture()) }},
		{"issue_list_loading", func(t *testing.T) *harness {
			h := newHarness(t, richFixture(), withLazy())
			h.hold("ListIssues", 0)
			h.boot()
			return h
		}},
		{"issue_list_empty", func(t *testing.T) *harness {
			fx := richFixture()
			fx.Issues = nil
			return newHarness(t, fx)
		}},
		{"issue_list_no_match", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			h.press("/")
			h.typeText("zzz")
			h.press("enter")
			return h
		}},
		{"issue_list_failed", func(t *testing.T) *harness {
			h := newHarness(t, richFixture(), withLazy())
			g := h.hold("ListIssues", 0)
			h.boot()
			h.fail(g, &domain.Error{Kind: domain.ErrAuth, Message: "not authenticated"})
			return h
		}},
		{"issue_list_closed", func(t *testing.T) *harness {
			fx := richFixture()
			fx.Issues[4].Issue.ClosedAt = ptr(fixtureNow.Add(-10 * time.Minute)) // #2 closed after #3
			h := newHarness(t, fx)
			h.press("s")
			return h
		}},
		{"issue_list_truncated", func(t *testing.T) *harness { return newHarness(t, bigFixture(1204)) }},
		{"issue_list_stale", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			g := h.hold("ListIssues", 0)
			h.press("r")
			h.fail(g, &domain.Error{Kind: domain.ErrNetwork, Message: "network unreachable"})
			return h
		}},
		{"issue_detail", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			h.press("enter")
			return h
		}},
		{"pr_list", func(t *testing.T) *harness { return newHarness(t, prFixture(), withStart(ViewPRs)) }},
		{"pr_list_mine", func(t *testing.T) *harness {
			h := newHarness(t, prFixture(), withStart(ViewPRs))
			h.press("m")
			return h
		}},
		{"pr_detail", func(t *testing.T) *harness {
			h := newHarness(t, prFixture(), withStart(ViewPRs))
			h.press("enter")
			return h
		}},
		{"board", func(t *testing.T) *harness { return newHarness(t, richFixture(), withStart(ViewBoard)) }},
		{"board_setup_no_board", func(t *testing.T) *harness {
			return newHarness(t, unlinkedFixture(), withStart(ViewBoard))
		}},
		{"board_setup_several", func(t *testing.T) *harness {
			fx := richFixture()
			fx.BoardInfo.Project = domain.Project{}
			second := fx.Projects[0]
			second.Title, second.NodeID, second.Ref.Number, second.TotalItems = "Roadmap", "PROJECT_2", 2, 88
			fx.Projects = append(fx.Projects, second)
			return newHarness(t, fx, withStart(ViewBoard))
		}},
		{"board_setup_missing_scope", func(t *testing.T) *harness {
			h := newHarness(t, richFixture(), withStart(ViewBoard), withLazy())
			g := h.hold("BoardInfo", 0)
			h.boot()
			h.fail(g, &domain.Error{Kind: domain.ErrMissingScope, Message: "missing project scope"})
			return h
		}},
		{"board_setup_no_status_field", func(t *testing.T) *harness {
			fx := richFixture()
			fx.BoardInfo.StatusFieldID = ""
			return newHarness(t, fx, withStart(ViewBoard))
		}},
		{"help", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			h.press("?")
			return h
		}},
		{"toast", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			h.press("y")
			return h
		}},
		{"create_form", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			h.press("n")
			h.typeText("Fix the header at 80 columns")
			h.press("tab")
			return h
		}},
		{"create_form_preview", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			h.press("n")
			h.typeText("Title")
			h.press("tab", "tab", "tab", "tab")
			h.typeText("Some **bold** text")
			h.press("ctrl+r")
			return h
		}},
		{"edit_conflict", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			h.press("e")
			h.typeText("!")
			h.fake.Mutate(func(f *servicetest.Fixture) { f.Issues[0].Body = "changed" })
			h.press("ctrl+s")
			return h
		}},
		{"comment_form", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			h.press("enter", "c")
			h.typeText("Reproduced on master.")
			return h
		}},
		{"assignee_picker", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			h.press("a")
			return h
		}},
		{"label_picker", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			h.press("L", "j")
			return h
		}},
		{"status_picker", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			h.press("M")
			return h
		}},
		{"delete_confirm", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			h.press("D")
			h.typeText("6")
			return h
		}},
		{"quit_confirm", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			h.hold("Close", 6)
			h.press("x", "q")
			return h
		}},
		{"pending_markers_list", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			h.hold("Close", 6)
			h.press("x", "j")
			g := h.hold("Close", 5)
			h.press("x")
			h.fail(g, uncertainErr()) // ? on #5 (its re-read is not held, so it settles)
			return h
		}},
		{"pending_markers_detail", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			h.hold("UpdateLabels", 6)
			h.press("enter", "L", "space", "j", "j", "space", "enter")
			return h
		}},
		{"board_moved_card", func(t *testing.T) *harness {
			h := newHarness(t, richFixture(), withStart(ViewBoard))
			h.hold("SetStatus", 6)
			h.press(">")
			return h
		}},
		{"error_toast", func(t *testing.T) *harness {
			h := newHarness(t, richFixture())
			h.app.toastErr("Couldn't load the session: network unreachable")
			h.send(nudge{})
			return h
		}},
	}
	cases = append(cases, quickAddGoldens...)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, sz := range sizes {
				t.Run(sz.name, func(t *testing.T) {
					h := c.setup(t)
					h.size(sz.w, sz.h)
					golden.RequireEqual(t, []byte(h.view()))
				})
			}
		})
	}
}

func TestGolden_tooSmall(t *testing.T) {
	h := newHarness(t, richFixture())
	h.size(79, 24)
	golden.RequireEqual(t, []byte(h.view()))
}

// TestGolden_ansi keeps one styled golden per main screen, dark and light.
func TestGolden_ansi(t *testing.T) {
	screens := map[string]func(t *testing.T, theme string) *harness{
		"issue_list": func(t *testing.T, theme string) *harness { return newHarness(t, richFixture(), withTheme(theme)) },
		"issue_detail": func(t *testing.T, theme string) *harness {
			h := newHarness(t, richFixture(), withTheme(theme))
			h.press("enter")
			return h
		},
		"board": func(t *testing.T, theme string) *harness {
			return newHarness(t, richFixture(), withTheme(theme), withStart(ViewBoard))
		},
	}
	for name, setup := range screens {
		for _, theme := range []string{"dark", "light"} {
			t.Run(name+"_"+theme, func(t *testing.T) {
				h := setup(t, theme)
				golden.RequireEqual(t, []byte(h.app.View().Content))
			})
		}
	}
}

// prFixture has pull requests in every state, from two authors.
func prFixture() servicetest.Fixture {
	fx := richFixture()
	fx.PRs[0].PullRequest.Title = "TUI: board view skeleton"
	fx.PRs[0].PullRequest.Head = "fe/board"
	fx.PRs[0].Body = "Adds the **board** view.\n\n- columns\n- cards\n"
	fx.PRComments[101] = []domain.Comment{{ID: "P1", Author: ptr("reviewer"), CreatedAt: fixtureNow.Add(-time30m()), Body: "Looks good."}}
	second := fx.PRs[0]
	second.PullRequest.Number, second.PullRequest.NodeID, second.PullRequest.Title = 104, "PR_104", "Core: go-gh issue client"
	second.PullRequest.Draft, second.PullRequest.Review, second.PullRequest.Checks = true, "review_required", "pending"
	second.PullRequest.Author, second.PullRequest.Head = ptr("lead-dev"), "core/issues"
	second.PullRequest.UpdatedAt = fixtureNow.Add(-3 * time30m())
	fx.PRs = append(fx.PRs, second)
	return fx
}

// quickAddGoldens are the five quick-add screens of tui.md 18.2.
var quickAddGoldens = []goldenCase{
	{"quickadd_write_marks", func(t *testing.T) *harness {
		store := newMemDrafts()
		text := "- Alpha\n- Bravo\n  with a body\n- Charlie\n"
		store.m["github.com/pkieltyka/todos"] = domain.Draft{RepoKey: "github.com/pkieltyka/todos", Text: text,
			Uncertain: []domain.DraftSpan{{StartByte: 8, EndByte: 30, SourceSHA256: sourceHash("- Bravo\n  with a body\n")}}}
		h := newHarness(t, richFixture(), withDrafts(store))
		h.press("A")
		return h
	}},
	{"quickadd_preview", func(t *testing.T) *harness {
		h := newHarness(t, richFixture(), withStart(ViewBoard), withDrafts(newMemDrafts()))
		h.press("l", "A") // the Todo column
		h.paste(threeItems + "- [x] Already done\n")
		h.press("ctrl+s")
		return h
	}},
	{"quickadd_preview_problems", func(t *testing.T) *harness {
		h := newHarness(t, richFixture(), withDrafts(newMemDrafts()))
		h.press("A")
		h.paste("  indented before any item\n- Alpha\n")
		h.press("ctrl+s")
		return h
	}},
	{"quickadd_progress", func(t *testing.T) *harness {
		h := newHarness(t, richFixture(), withDrafts(newMemDrafts()))
		h.press("A")
		h.paste(threeItems)
		h.hold("RunQuickAdd.item", 1) // Alpha done, Bravo next
		h.press("ctrl+s", "enter")
		return h
	}},
	{"quickadd_stopped", func(t *testing.T) *harness {
		h := newHarness(t, richFixture(), withStart(ViewBoard), withDrafts(newMemDrafts()))
		h.press("l", "A") // the Todo column: a named status, placed with BoardAdd
		h.paste("- Alpha\n- Bravo\n")
		g := h.hold("BoardAdd", 104)
		h.press("ctrl+s", "enter")
		h.fail(g, &domain.Error{Kind: domain.ErrGitHub, Message: "board unavailable"})
		return h
	}},
}
