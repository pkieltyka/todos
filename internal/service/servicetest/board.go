package servicetest

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkieltyka/todos/internal/domain"
)

func resolveStatus(x *Fixture, status *string) (*string, string, error) {
	if status == nil {
		return nil, "", nil
	}
	for _, o := range x.BoardInfo.Options {
		if o.Name == *status {
			return ptr(o.Name), o.ID, nil
		}
	}
	var match *domain.StatusOption
	for _, o := range x.BoardInfo.Options {
		if strings.EqualFold(o.Name, *status) {
			if match != nil {
				return nil, "", invalid("ambiguous board status " + *status)
			}
			copy := o
			match = &copy
		}
	}
	if match != nil {
		return ptr(match.Name), match.ID, nil
	}
	return nil, "", invalid("unknown board status " + *status)
}
func place(x *Fixture, i *domain.Issue, status *string, optionID string) {
	item := domain.NodeID(fmt.Sprintf("ITEM_%d", i.Number))
	if i.Board != nil {
		item = i.Board.ItemID
	}
	if archived, ok := x.Archived[i.Number]; ok {
		item = archived.ItemID
		delete(x.Archived, i.Number)
	}
	var name *string
	if status != nil {
		name = ptr(*status)
	}
	i.Board = &domain.Placement{ItemID: item, Status: name, OptionID: optionID}
	found := false
	for _, c := range x.Board.Columns {
		if equalStatus(c.Status, status) {
			found = true
		}
	}
	if !found {
		column := domain.Column{Status: name, OptionID: optionID, Cards: []domain.Card{}}
		if status == nil {
			x.Board.Columns = append([]domain.Column{column}, x.Board.Columns...)
		} else {
			x.Board.Columns = append(x.Board.Columns, column)
		}
	}
}

func (f *Fake) SetStatus(ctx context.Context, n int, status *string) (domain.Change, error) {
	if status != nil {
		status = ptr(*status)
	}
	return invoke(ctx, f, "SetStatus", Arguments{Number: n, Status: status}, func(x *Fixture) (domain.Change, error) {
		index := issueIndex(x, n)
		if index < 0 {
			return domain.Change{}, missing("issue")
		}
		d := &x.Issues[index]
		canonical, id, err := resolveStatus(x, status)
		if err != nil {
			return domain.Change{}, err
		}
		_, archived := x.Archived[n]
		if !archived && d.Issue.Board != nil && equalStatus(d.Issue.Board.Status, canonical) {
			return domain.Change{Issue: domain.CloneIssue(d.Issue)}, nil
		}
		place(x, &d.Issue, canonical, id)
		return domain.Change{Issue: domain.CloneIssue(d.Issue), Changed: true}, nil
	})
}
func (f *Fake) BoardAdd(ctx context.Context, n int, status *string) (domain.Change, error) {
	if status != nil {
		status = ptr(*status)
	}
	return invoke(ctx, f, "BoardAdd", Arguments{Number: n, Status: status}, func(x *Fixture) (domain.Change, error) {
		index := issueIndex(x, n)
		if index < 0 {
			return domain.Change{}, missing("issue")
		}
		d := &x.Issues[index]
		if archived, ok := x.Archived[n]; ok && status == nil {
			canonical, id, err := resolveStatus(x, archived.Status)
			if err != nil {
				return domain.Change{}, err
			}
			place(x, &d.Issue, canonical, id)
			return domain.Change{Issue: domain.CloneIssue(d.Issue), Changed: true}, nil
		}
		canonical, id, err := resolveStatus(x, status)
		if err != nil {
			return domain.Change{}, err
		}
		_, archived := x.Archived[n]
		if !archived && d.Issue.Board != nil && (status == nil || equalStatus(d.Issue.Board.Status, canonical)) {
			return domain.Change{Issue: domain.CloneIssue(d.Issue)}, nil
		}
		place(x, &d.Issue, canonical, id)
		return domain.Change{Issue: domain.CloneIssue(d.Issue), Changed: true}, nil
	})
}
func (f *Fake) RemoveFromBoard(ctx context.Context, n int) (domain.Change, error) {
	return f.change(ctx, "RemoveFromBoard", Arguments{Number: n}, func(x *Fixture, d *domain.IssueDetail) bool {
		_, archived := x.Archived[n]
		if d.Issue.Board == nil && !archived {
			return false
		}
		d.Issue.Board = nil
		delete(x.Archived, n)
		return true
	})
}
