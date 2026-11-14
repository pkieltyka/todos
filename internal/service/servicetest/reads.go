package servicetest

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/pkieltyka/todos/internal/domain"
)

func missing(what string) error {
	return &domain.Error{Kind: domain.ErrNotFound, Message: what + " not found or no access"}
}
func invalid(what string) error { return &domain.Error{Kind: domain.ErrInvalidInput, Message: what} }

func page[T any](items []T, request domain.PageRequest) (domain.Page[T], error) {
	out := domain.Page[T]{Items: []T{}, Total: len(items)}
	size := request.Size
	if size == 0 {
		size = 100
	}
	if size < 1 || size > 100 {
		return out, invalid("page size must be between 1 and 100")
	}
	start := 0
	if request.Cursor != "" {
		n, err := strconv.Atoi(request.Cursor)
		if err != nil || n < 0 || n > len(items) {
			return out, invalid("invalid fixture cursor")
		}
		start = n
	}
	end := min(start+size, len(items))
	out.Items = append(out.Items, items[start:end]...)
	out.HasMore = end < len(items)
	if out.HasMore {
		out.NextCursor = strconv.Itoa(end)
	}
	return out, nil
}

func (f *Fake) Session(ctx context.Context) (domain.Session, error) {
	return invoke(ctx, f, "Session", Arguments{}, func(x *Fixture) (domain.Session, error) { return x.Session, nil })
}
func (f *Fake) RefreshMetadata(ctx context.Context) error {
	_, err := invoke(ctx, f, "RefreshMetadata", Arguments{}, func(*Fixture) (struct{}, error) { return struct{}{}, nil })
	return err
}
func (f *Fake) Labels(ctx context.Context) ([]domain.Label, error) {
	return invoke(ctx, f, "Labels", Arguments{}, func(x *Fixture) ([]domain.Label, error) { return copySlice(x.Labels), nil })
}
func (f *Fake) AssignableUsers(ctx context.Context) ([]domain.User, error) {
	return invoke(ctx, f, "AssignableUsers", Arguments{}, func(x *Fixture) ([]domain.User, error) { return copySlice(x.Users), nil })
}
func (f *Fake) ListIssues(ctx context.Context, filter domain.IssueFilter, request domain.PageRequest) (domain.Page[domain.Issue], error) {
	filter.Labels = copySlice(filter.Labels)
	return invoke(ctx, f, "ListIssues", Arguments{Filter: filter, Page: request}, func(x *Fixture) (domain.Page[domain.Issue], error) {
		items := []domain.Issue{}
		assignee := filter.Assignee
		if assignee == "@me" {
			assignee = x.Session.Viewer.Login
		}
		for _, detail := range x.Issues {
			i := detail.Issue
			if state := filter.State; state != "all" && state != "" && string(i.State) != state {
				continue
			}
			if filter.State == "" && i.State != "open" {
				continue
			}
			if assignee != "" && !contains(i.Assignees, assignee) {
				continue
			}
			matches := true
			for _, label := range filter.Labels {
				found := false
				for _, v := range i.Labels {
					if v.Name == label {
						found = true
					}
				}
				if !found {
					matches = false
				}
			}
			if !matches {
				continue
			}
			if filter.Search != "" && !strings.Contains(strings.ToLower(i.Title+"\n"+detail.Body), strings.ToLower(filter.Search)) {
				continue
			}
			if _, archived := x.Archived[i.Number]; archived {
				i.Board = nil
			}
			items = append(items, domain.CloneIssue(i))
		}
		sort.SliceStable(items, func(a, b int) bool { return items[a].UpdatedAt.After(items[b].UpdatedAt) })
		total := len(items)
		limited := filter.Search != "" && total > 1000
		if limited {
			items = items[:1000]
		}
		result, err := page(items, request)
		result.Total = total
		result.ProviderTruncated = limited
		return result, err
	})
}
func contains(v []string, want string) bool {
	for _, s := range v {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}
func issueIndex(x *Fixture, n int) int {
	for i := range x.Issues {
		if x.Issues[i].Issue.Number == n {
			return i
		}
	}
	return -1
}
func (f *Fake) GetIssue(ctx context.Context, n int) (domain.IssueDetail, error) {
	return invoke(ctx, f, "GetIssue", Arguments{Number: n}, func(x *Fixture) (domain.IssueDetail, error) {
		i := issueIndex(x, n)
		if i < 0 {
			return domain.IssueDetail{}, missing(fmt.Sprintf("issue #%d", n))
		}
		d := x.Issues[i]
		d.Issue = domain.CloneIssue(d.Issue)
		if _, archived := x.Archived[n]; archived {
			d.Issue.Board = nil
		}
		return d, nil
	})
}
func (f *Fake) ListIssueComments(ctx context.Context, n int, request domain.PageRequest) (domain.Page[domain.Comment], error) {
	return invoke(ctx, f, "ListIssueComments", Arguments{Number: n, Page: request}, func(x *Fixture) (domain.Page[domain.Comment], error) {
		if issueIndex(x, n) < 0 {
			return domain.Page[domain.Comment]{Items: []domain.Comment{}}, missing("issue")
		}
		c := cloneComments(x.IssueComments[n])
		sort.SliceStable(c, func(i, j int) bool { return c[i].CreatedAt.Before(c[j].CreatedAt) })
		return page(c, request)
	})
}
func (f *Fake) ListPRs(ctx context.Context, filter domain.PRFilter, request domain.PageRequest) (domain.Page[domain.PullRequest], error) {
	return invoke(ctx, f, "ListPRs", Arguments{PRFilter: filter, Page: request}, func(x *Fixture) (domain.Page[domain.PullRequest], error) {
		items := []domain.PullRequest{}
		state := filter.State
		if state == "" {
			state = "open"
		}
		for _, d := range x.PRs {
			if state == "all" || string(d.PullRequest.State) == state {
				items = append(items, domain.ClonePR(d.PullRequest))
			}
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
		return page(items, request)
	})
}
func prIndex(x *Fixture, n int) int {
	for i := range x.PRs {
		if x.PRs[i].PullRequest.Number == n {
			return i
		}
	}
	return -1
}
func (f *Fake) GetPR(ctx context.Context, n int) (domain.PRDetail, error) {
	return invoke(ctx, f, "GetPR", Arguments{Number: n}, func(x *Fixture) (domain.PRDetail, error) {
		i := prIndex(x, n)
		if i < 0 {
			return domain.PRDetail{}, missing("pull request")
		}
		d := x.PRs[i]
		d.PullRequest = domain.ClonePR(d.PullRequest)
		d.Closes = copySlice(d.Closes)
		return d, nil
	})
}
func (f *Fake) ListPRComments(ctx context.Context, n int, request domain.PageRequest) (domain.Page[domain.Comment], error) {
	return invoke(ctx, f, "ListPRComments", Arguments{Number: n, Page: request}, func(x *Fixture) (domain.Page[domain.Comment], error) {
		if prIndex(x, n) < 0 {
			return domain.Page[domain.Comment]{Items: []domain.Comment{}}, missing("pull request")
		}
		c := cloneComments(x.PRComments[n])
		sort.SliceStable(c, func(i, j int) bool { return c[i].CreatedAt.Before(c[j].CreatedAt) })
		return page(c, request)
	})
}
func boardInfo(x *Fixture) (domain.BoardInfo, error) {
	i := cloneInfo(x.BoardInfo)
	if i.Project.NodeID == "" {
		linked := []domain.Project{}
		for _, p := range x.Projects {
			if p.Linked {
				linked = append(linked, p)
			}
		}
		return i, &domain.Error{Kind: domain.ErrNoBoard, Message: "no board selected", Candidates: linked}
	}
	if !i.Project.Usable || i.StatusFieldID == "" {
		i.Project.Usable = false
		return i, &domain.Error{Kind: domain.ErrNoBoard, Message: "project has no single-select Status field", Candidates: []domain.Project{i.Project}}
	}
	return i, nil
}
func (f *Fake) BoardInfo(ctx context.Context) (domain.BoardInfo, error) {
	return invoke(ctx, f, "BoardInfo", Arguments{}, boardInfo)
}
func (f *Fake) BoardCandidates(ctx context.Context) (domain.ProjectsResult, error) {
	return invoke(ctx, f, "BoardCandidates", Arguments{}, func(x *Fixture) (domain.ProjectsResult, error) {
		out := domain.ProjectsResult{Projects: []domain.Project{}}
		seen := map[domain.NodeID]bool{}
		for _, linked := range []bool{true, false} {
			for _, p := range x.Projects {
				if p.Linked == linked && !seen[p.NodeID] {
					out.Projects = append(out.Projects, p)
					seen[p.NodeID] = true
				}
			}
		}
		if x.BoardInfo.Project.NodeID != "" {
			p := x.BoardInfo.Project
			out.Selected = &p
		}
		return out, nil
	})
}
func (f *Fake) UseBoard(ctx context.Context, ref domain.ProjectRef) (domain.Project, error) {
	return invoke(ctx, f, "UseBoard", Arguments{Project: ref}, func(x *Fixture) (domain.Project, error) {
		for _, p := range x.Projects {
			if p.Ref.Number == ref.Number && strings.EqualFold(p.Ref.Owner, ref.Owner) && (ref.Host == "" || strings.EqualFold(ref.Host, p.Ref.Host)) {
				if !p.Usable {
					return p, &domain.Error{Kind: domain.ErrNoBoard, Message: "project has no single-select Status field", Candidates: []domain.Project{p}}
				}
				x.BoardInfo.Project = p
				x.Board.Project = p
				return p, nil
			}
		}
		return domain.Project{}, missing("project")
	})
}
func snapshotBoard(x *Fixture) domain.Board {
	b := domain.CloneBoard(x.Board)
	b.Project = x.BoardInfo.Project
	seen := map[int]bool{}
	for c := range b.Columns {
		cards := []domain.Card{}
		for _, card := range b.Columns[c].Cards {
			if _, archived := x.Archived[card.Number]; archived {
				continue
			}
			idx := issueIndex(x, card.Number)
			if idx >= 0 {
				card.Issue = domain.CloneIssue(x.Issues[idx].Issue)
			}
			if card.Board != nil && equalStatus(card.Board.Status, b.Columns[c].Status) {
				cards = append(cards, card)
				seen[card.Number] = true
			}
		}
		b.Columns[c].Cards = cards
	}
	for _, d := range x.Issues {
		i := d.Issue
		if _, archived := x.Archived[i.Number]; archived {
			continue
		}
		if i.Board == nil || seen[i.Number] {
			continue
		}
		for c := range b.Columns {
			if equalStatus(i.Board.Status, b.Columns[c].Status) {
				b.Columns[c].Cards = append(b.Columns[c].Cards, domain.Card{Issue: domain.CloneIssue(i)})
				seen[i.Number] = true
				break
			}
		}
	}
	b.VisibleCount = len(seen)
	b.ScannedCount = b.VisibleCount + b.HiddenCount
	b.TotalItems = max(b.TotalItems, b.ScannedCount)
	b.CardsTruncated = b.VisibleCount > 500
	out := []domain.Column{}
	for _, c := range b.Columns {
		if c.Status != nil || len(c.Cards) > 0 {
			out = append(out, c)
		}
	}
	b.Columns = out
	return b
}
func equalStatus(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func (f *Fake) Board(ctx context.Context) (domain.Board, error) {
	return invoke(ctx, f, "Board", Arguments{}, func(x *Fixture) (domain.Board, error) {
		if _, err := boardInfo(x); err != nil {
			return domain.Board{}, err
		}
		return snapshotBoard(x), nil
	})
}
func (f *Fake) Doctor(ctx context.Context) (domain.DoctorReport, error) {
	return invoke(ctx, f, "Doctor", Arguments{}, func(x *Fixture) (domain.DoctorReport, error) {
		_, boardErr := boardInfo(x)
		checks := []domain.DoctorCheck{{Name: "Credentials", OK: x.Credentials.Source != "", Detail: x.Credentials.Source}, {Name: "Viewer", OK: x.Session.Viewer.Login != "", Detail: x.Session.Viewer.Login}, {Name: "Repository", OK: x.Session.Repo.NodeID != "" && x.Session.Repo.IssuesEnabled && x.Session.Repo.ViewerCanCreateIssues, Detail: x.Session.Repo.Ref.Owner + "/" + x.Session.Repo.Ref.Name}, {Name: "Board", OK: boardErr == nil, Detail: x.BoardInfo.Project.Title}, {Name: "Board access", OK: boardErr == nil && x.BoardInfo.Project.ViewerCanUpdate}, x.ConfigCheck}
		report := domain.DoctorReport{OK: true, Checks: checks}
		for i := 0; i < 3; i++ {
			if !checks[i].OK {
				kind := domain.ErrAuth
				if i == 2 {
					kind = domain.ErrForbidden
					if x.Session.Repo.NodeID == "" {
						kind = domain.ErrNoRepo
					}
					if !x.Session.Repo.IssuesEnabled {
						kind = domain.ErrIssuesDisabled
					}
				}
				report.OK = false
				report.FirstFailure = &domain.Error{Kind: kind, Message: checks[i].Name + " check failed"}
				return report, report.FirstFailure
			}
		}
		return report, nil
	})
}
