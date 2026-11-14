package tui

import (
	"cmp"
	"slices"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

// The closed issues list is ordered by closed time, newest first (tui.md
// 7.4). GitHub's issue listing orders by created, updated or comments only,
// so this is a derived order over the loaded list; open and all keep GitHub's
// most-recently-updated order.

// closedTime is the time an issue sorts and shows by in the closed view: its
// closed time, or its updated time when it has none.
func closedTime(i domain.Issue) time.Time {
	if i.ClosedAt != nil {
		return *i.ClosedAt
	}
	return i.UpdatedAt
}

// byClosed orders newest closed first; a tie falls back to updated time,
// then to the higher number.
func byClosed(a, b domain.Issue) int {
	if c := closedTime(b).Compare(closedTime(a)); c != 0 {
		return c
	}
	if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
		return c
	}
	return cmp.Compare(b.Number, a.Number)
}

// closedOrder returns the issues' numbers, newest closed first. It does not
// change issues.
func closedOrder(issues []domain.Issue) []int {
	sorted := slices.Clone(issues)
	slices.SortStableFunc(sorted, byClosed)
	out := make([]int, len(sorted))
	for k, i := range sorted {
		out[k] = i.Number
	}
	return out
}

// listTime is the time the issue list's time column shows.
func listTime(state domain.IssueState, i domain.Issue) time.Time {
	if state == "closed" {
		return closedTime(i)
	}
	return i.UpdatedAt
}
