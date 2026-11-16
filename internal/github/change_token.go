package github

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/pkieltyka/todos/internal/domain"
)

func projectProbeRefused(err error) bool {
	var de *domain.Error
	var ge *api.GraphQLError
	if !errors.As(err, &de) || (de.Kind != domain.ErrMissingScope && de.Kind != domain.ErrForbidden && de.Kind != domain.ErrNotFound) || !errors.As(err, &ge) || len(ge.Errors) == 0 {
		return false
	}
	for _, e := range ge.Errors {
		if len(e.Path) == 0 || e.Path[0] != "node" {
			return false
		}
	}
	return true
}

// On a refusal confined to the optional project half, the returned token has
// the successfully decoded issue/viewer half and the classified error. Every
// other failure returns a zero token. Neither is suitable for caching.
func (c *Client) ChangeToken(ctx context.Context, ref domain.RepoRef, project domain.NodeID) (domain.ChangeToken, error) {
	var out struct {
		Viewer     *struct{ Login string }
		Repository *struct {
			Issues *struct {
				TotalCount *int
				Nodes      []struct {
					Number    int
					UpdatedAt time.Time
				}
			}
		}
		Node *struct {
			UpdatedAt time.Time
			Items     *struct{ TotalCount *int }
		}
	}
	vars := map[string]any{"owner": ref.Owner, "name": ref.Name}
	vars["project"], vars["withProject"] = project, project != ""
	meta := responseMeta{}
	err := c.executeWithMeta(ctx, "ChangeToken", vars, &out, false, &meta)
	refused := project != "" && projectProbeRefused(err)
	if err != nil && !refused {
		return domain.ChangeToken{}, err
	}
	if out.Viewer == nil || out.Viewer.Login == "" || out.Repository == nil || out.Repository.Issues == nil || out.Repository.Issues.TotalCount == nil || out.Repository.Issues.Nodes == nil {
		return domain.ChangeToken{}, malformed("missing change-token issue collection")
	}
	issues := out.Repository.Issues
	token := domain.ChangeToken{Viewer: out.Viewer.Login, IssuesTotal: *issues.TotalCount}
	if token.IssuesTotal < 0 || len(issues.Nodes) > 1 || (token.IssuesTotal > 0 && len(issues.Nodes) == 0) {
		return domain.ChangeToken{}, malformed("invalid change-token issue collection")
	}
	if len(issues.Nodes) > 0 {
		token.NewestIssue, token.NewestUpdated = issues.Nodes[0].Number, issues.Nodes[0].UpdatedAt
		if token.NewestIssue < 1 || token.NewestUpdated.IsZero() {
			return domain.ChangeToken{}, malformed("missing newest issue identity")
		}
	}
	token.ServerTime, _ = http.ParseTime(meta.Headers.Get("Date"))
	if refused {
		return token, err
	}
	if project != "" {
		if out.Node == nil || out.Node.UpdatedAt.IsZero() || out.Node.Items == nil || out.Node.Items.TotalCount == nil || *out.Node.Items.TotalCount < 0 {
			return domain.ChangeToken{}, malformed("missing change-token project")
		}
		token.ProjectUpdated, token.ProjectItems = out.Node.UpdatedAt, *out.Node.Items.TotalCount
	}
	return token, nil
}
