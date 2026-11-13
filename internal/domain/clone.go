package domain

func clonePtr[T any](v *T) *T {
	if v == nil {
		return nil
	}
	x := *v
	return &x
}

func cloneSlice[T any](v []T) []T { return append(make([]T, 0, len(v)), v...) }

// CloneIssue returns an independently owned issue, including its placement.
func CloneIssue(v Issue) Issue {
	v.StateReason = clonePtr(v.StateReason)
	v.Author = clonePtr(v.Author)
	v.Assignees = cloneSlice(v.Assignees)
	v.Labels = cloneSlice(v.Labels)
	v.Milestone = clonePtr(v.Milestone)
	v.ClosedAt = clonePtr(v.ClosedAt)
	if v.Board != nil {
		p := *v.Board
		p.Status = clonePtr(p.Status)
		v.Board = &p
	}
	return v
}

// ClonePR returns an independently owned pull request.
func ClonePR(v PullRequest) PullRequest {
	v.Author = clonePtr(v.Author)
	v.Assignees = cloneSlice(v.Assignees)
	v.Labels = cloneSlice(v.Labels)
	v.ClosedAt = clonePtr(v.ClosedAt)
	v.MergedAt = clonePtr(v.MergedAt)
	return v
}

// CloneBoard preserves order while copying all cards and placements.
func CloneBoard(v Board) Board {
	v.Columns = cloneSlice(v.Columns)
	for i := range v.Columns {
		v.Columns[i].Status = clonePtr(v.Columns[i].Status)
		v.Columns[i].Cards = cloneSlice(v.Columns[i].Cards)
		for j := range v.Columns[i].Cards {
			v.Columns[i].Cards[j].Issue = CloneIssue(v.Columns[i].Cards[j].Issue)
		}
	}
	return v
}

// ClonePlan retains verbatim source while detaching mutable plan values.
func ClonePlan(v QuickAddPlan) QuickAddPlan {
	v.Items = cloneSlice(v.Items)
	v.Problems = cloneSlice(v.Problems)
	v.Options.Assignees = cloneSlice(v.Options.Assignees)
	v.Options.Labels = cloneSlice(v.Options.Labels)
	v.Options.Status = clonePtr(v.Options.Status)
	return v
}

// CloneError copies domain diagnostic data; the wrapped error is immutable.
func CloneError(v *Error) *Error {
	if v == nil {
		return nil
	}
	x := *v
	x.Completed = cloneSlice(v.Completed)
	x.Candidates = cloneSlice(v.Candidates)
	if v.Issue != nil {
		issue := CloneIssue(*v.Issue)
		x.Issue = &issue
	}
	if e, ok := v.Err.(*Error); ok {
		x.Err = CloneError(e)
	}
	return &x
}

// CloneQuickAddResult copies rows, known issues and diagnostics.
func CloneQuickAddResult(v QuickAddResult) QuickAddResult {
	v.Items = cloneSlice(v.Items)
	for i := range v.Items {
		if v.Items[i].Issue != nil {
			issue := CloneIssue(*v.Items[i].Issue)
			v.Items[i].Issue = &issue
		}
		v.Items[i].Error = CloneError(v.Items[i].Error)
	}
	return v
}

// CloneDraft copies local markers without changing the user's text.
func CloneDraft(v Draft) Draft {
	v.Uncertain = cloneSlice(v.Uncertain)
	v.InFlight = clonePtr(v.InFlight)
	return v
}
