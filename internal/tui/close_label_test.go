package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

// The x hint names what x would do to the issue it acts on: "reopen" for a
// closed issue, "close" for an open one, judged on the drawn (effective)
// issue, so a pending close already says "reopen" (Peter's bug report).

// wide makes room for the x hint: at 80 columns the detail and board
// footers run out before it.
func wide(h *harness) *harness { h.size(140, 40); return h }

func footerLine(h *harness) string {
	lines := strings.Split(ansi.Strip(h.app.View().Content), "\n")
	return lines[len(lines)-1]
}

func wantX(t *testing.T, h *harness, want string) {
	t.Helper()
	f := footerLine(h)
	other := map[string]string{"close": "reopen", "reopen": "close"}[want]
	if !strings.Contains(f, "x "+want) || strings.Contains(f, "x "+other) {
		t.Fatalf("footer should say %q, not %q:\n%s", "x "+want, "x "+other, f)
	}
}

func withClosed(n int) servicetest.Fixture {
	fx := richFixture()
	for i := range fx.Issues {
		if fx.Issues[i].Issue.Number == n {
			fx.Issues[i].Issue.State = "closed"
			r := domain.StateReason("completed")
			fx.Issues[i].Issue.StateReason = &r
			at := fx.Issues[i].Issue.UpdatedAt
			fx.Issues[i].Issue.ClosedAt = &at
		}
	}
	return fx
}

func TestCloseLabel_issueList(t *testing.T) {
	t.Run("open row says close, pending close says reopen", func(t *testing.T) {
		h := newHarness(t, richFixture())
		wantX(t, h, "close")
		h.hold("Close", 6)
		h.press("x")
		wantX(t, h, "reopen")
	})
	t.Run("closed row says reopen", func(t *testing.T) {
		h := newHarness(t, richFixture())
		h.press("s") // closed issues
		wantX(t, h, "reopen")
	})
	t.Run("follows the selected row", func(t *testing.T) {
		h := newHarness(t, withClosed(5)) // #6 open, #5 closed
		h.press("s", "s")                 // all
		wantX(t, h, "close")
		h.press("j")
		wantX(t, h, "reopen")
	})
}

func TestCloseLabel_detail(t *testing.T) {
	t.Run("open issue, then pending close", func(t *testing.T) {
		h := wide(newHarness(t, richFixture()))
		h.press("enter")
		wantX(t, h, "close")
		h.hold("Close", 6)
		h.press("x")
		wantX(t, h, "reopen")
	})
	t.Run("closed issue says reopen", func(t *testing.T) {
		h := wide(newHarness(t, richFixture()))
		h.press("s", "enter")
		wantX(t, h, "reopen")
	})
}

func TestCloseLabel_board(t *testing.T) {
	t.Run("open card, then pending close", func(t *testing.T) {
		h := wide(newHarness(t, richFixture(), withStart(ViewBoard)))
		wantX(t, h, "close")
		h.hold("Close", 6)
		h.press("x")
		wantX(t, h, "reopen")
	})
	t.Run("closed card says reopen", func(t *testing.T) {
		h := wide(newHarness(t, withClosed(6), withStart(ViewBoard)))
		wantX(t, h, "reopen")
	})
}
