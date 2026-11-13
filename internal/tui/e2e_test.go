package tui

import (
	"bytes"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/markdown"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

// End-to-end tests run a real tea.Program through teatest (tui.md 18.3).
// They are few on purpose: teatest v2 is an untagged pseudo-version.

func e2eModel() *App {
	fake := servicetest.New(richFixture())
	return New(Deps{
		Service:  fake,
		Renderer: markdown.NewRenderer(markdown.Options{}),
		Clock:    func() time.Time { return fixtureNow },
		Env:      Env{Theme: "dark"},
	}, Options{StartView: ViewIssues, Repo: domain.RepoRef{Host: "github.com", Owner: "pkieltyka", Name: "todos"}})
}

func TestE2E_startupToFirstFrame(t *testing.T) {
	tm := teatest.NewTestModel(t, e2eModel(), teatest.WithInitialTermSize(80, 24))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("Fix flaky sync when gh is offline")) && bytes.Contains(b, []byte("4 open issues"))
	}, teatest.WithDuration(5*time.Second))
	if err := tm.Quit(); err != nil {
		t.Fatal(err)
	}
}

func TestE2E_qQuits(t *testing.T) {
	tm := teatest.NewTestModel(t, e2eModel(), teatest.WithInitialTermSize(80, 24))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("4 open issues"))
	}, teatest.WithDuration(5*time.Second))
	tm.Send(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}))
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}
