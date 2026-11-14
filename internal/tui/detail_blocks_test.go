package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

// Comment blocks are drawn once and reused across rebuilds (screen_detail.go,
// commentBlocks): before, every comment page and render batch restyled every
// comment, so loading 500 comments did about 45 x 500 block draws.

func manyComments(n int) []domain.Comment {
	var cs []domain.Comment
	for i := 0; i < n; i++ {
		cs = append(cs, domain.Comment{ID: fmt.Sprint(i), Author: ptr("reviewer"), CreatedAt: fixtureNow.Add(time.Duration(i-900) * time.Minute), Body: fmt.Sprintf("comment %d", i)})
	}
	return cs
}

func TestDetail_commentBlocksDrawnOncePerVersion(t *testing.T) {
	fx := richFixture()
	fx.IssueComments[6] = manyComments(200)
	h := newHarness(t, fx)
	h.press("enter")
	s := h.app.top(ViewIssues).(*IssueDetail)
	if d, _ := h.app.store.PeekDetail(6); len(d.Comments) != 200 {
		t.Fatalf("%d comments loaded", len(d.Comments))
	}
	// Each comment is drawn as plain text, then once more when its Markdown
	// lands: never per rebuild.
	if b := s.blocks.built; b > 2*200 {
		t.Fatalf("%d block draws for 200 comments", b)
	}
	before := s.blocks.built
	h.app.renderVer++ // a rebuild with nothing new to draw
	h.send(nudge{})
	if s.blocks.built != before {
		t.Fatalf("a rebuild drew %d blocks again", s.blocks.built-before)
	}
	if n := len(s.blocks.cur); n != 200 {
		t.Fatalf("%d cached blocks, want only the 200 in use", n)
	}
}

func TestDetail_commentBlocksRedrawWhenTheirHeaderChanges(t *testing.T) {
	fx := richFixture()
	fx.IssueComments[6] = []domain.Comment{{ID: "C1", Author: ptr("reviewer"), CreatedAt: fixtureNow, Body: "hello"}}
	fx.Issues[0].Issue.CommentCount = 1
	h := newHarness(t, fx)
	h.press("enter")
	h.contains("reviewer · just now")
	h.advance(5 * time.Minute)
	h.app.renderVer++
	h.send(nudge{})
	h.contains("reviewer · 5m ago")
	h.lacks("reviewer · just now")
}
