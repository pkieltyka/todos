package github

import (
	"context"

	"github.com/pkieltyka/todos/internal/domain"
)

func (c *Client) logicalHost() string { c.authMu.RLock(); defer c.authMu.RUnlock(); return c.host }
func (c *Client) LinkedProjects(ctx context.Context, r domain.RepoRef, p domain.PageRequest) (domain.Page[domain.Project], error) {
	v, err := pageVariables(p)
	if err != nil {
		return domain.Page[domain.Project]{}, err
	}
	addRepo(v, r)
	var out struct{ Repository *rawRepository }
	if err = c.query(ctx, "LinkedProjects", v, &out); err != nil {
		return domain.Page[domain.Project]{}, err
	}
	if out.Repository == nil {
		return domain.Page[domain.Project]{}, malformed("missing repository")
	}
	if out.Repository.ProjectsV2.Nodes == nil {
		return domain.Page[domain.Project]{}, malformed("missing linked-project connection")
	}
	return pageResult(out.Repository.ProjectsV2, func(v rawProject) domain.Project { p := project(v, r.Host); p.Linked = true; return p }), nil
}
func (c *Client) OwnerProjects(ctx context.Context, owner string, p domain.PageRequest) (domain.Page[domain.Project], error) {
	v, err := pageVariables(p)
	if err != nil {
		return domain.Page[domain.Project]{}, err
	}
	v["owner"] = owner
	var out struct {
		RepositoryOwner *struct{ ProjectsV2 connection[rawProject] }
	}
	if err = c.query(ctx, "OwnerProjects", v, &out); err != nil {
		return domain.Page[domain.Project]{}, err
	}
	if out.RepositoryOwner == nil {
		return domain.Page[domain.Project]{}, malformed("missing project owner")
	}
	if out.RepositoryOwner.ProjectsV2.Nodes == nil {
		return domain.Page[domain.Project]{}, malformed("missing owned-project connection")
	}
	return pageResult(out.RepositoryOwner.ProjectsV2, func(v rawProject) domain.Project { return project(v, c.logicalHost()) }), nil
}
func (c *Client) Project(ctx context.Context, r domain.ProjectRef) (domain.Project, error) {
	if r.Number < 1 {
		return domain.Project{}, invalidInput("project number must be positive")
	}
	var out struct {
		RepositoryOwner *struct{ ProjectV2 *rawProject }
	}
	if err := c.query(ctx, "ProjectByNumber", map[string]any{"owner": r.Owner, "number": r.Number}, &out); err != nil {
		return domain.Project{}, err
	}
	if out.RepositoryOwner == nil || out.RepositoryOwner.ProjectV2 == nil {
		return domain.Project{}, malformed("missing project")
	}
	p := project(*out.RepositoryOwner.ProjectV2, c.logicalHost())
	if p.NodeID == "" {
		return domain.Project{}, malformed("missing project identity")
	}
	return p, nil
}
func (c *Client) ProjectFields(ctx context.Context, id domain.NodeID) (domain.BoardInfo, error) {
	var result domain.BoardInfo
	result.Options = []domain.StatusOption{}
	v := map[string]any{"id": id, "after": nil}
	seen := map[string]bool{}
	matches := 0
	wrongType := false
	for {
		var out struct{ Node *rawProject }
		if err := c.query(ctx, "ProjectFields", v, &out); err != nil {
			return domain.BoardInfo{}, err
		}
		if out.Node == nil {
			return domain.BoardInfo{}, malformed("missing project")
		}
		if out.Node.ID == "" {
			return domain.BoardInfo{}, malformed("missing project identity")
		}
		if out.Node.Fields.Nodes == nil {
			return domain.BoardInfo{}, malformed("missing project-fields connection")
		}
		result.Project = project(*out.Node, c.logicalHost())
		for _, f := range out.Node.Fields.Nodes {
			if f.Name == "Status" {
				matches++
				if f.Typename != "ProjectV2SingleSelectField" {
					wrongType = true
					continue
				}
				result.StatusFieldID = f.ID
				result.Options = append([]domain.StatusOption{}, f.Options...)
			}
		}
		p := out.Node.Fields.PageInfo
		if !p.HasNextPage {
			break
		}
		if p.EndCursor == "" || seen[p.EndCursor] {
			return domain.BoardInfo{}, malformed("repeated or empty project-field cursor")
		}
		seen[p.EndCursor] = true
		v["after"] = p.EndCursor
	}
	result.Project.Usable = matches == 1 && !wrongType && result.StatusFieldID != ""
	if !result.Project.Usable {
		result.StatusFieldID = ""
		result.Options = []domain.StatusOption{}
	}
	return result, nil
}
func (c *Client) mapItem(v rawPlacement) domain.ProjectItem {
	out := domain.ProjectItem{ID: v.ID, ProjectID: v.Project.ID, Archived: v.IsArchived, Kind: "Redacted", Placement: placement(v)}
	if v.Content != nil {
		out.Kind = v.Content.Typename
		if out.Kind == "Issue" {
			if v.Content.Repository != nil {
				out.Repo = repoRef(c.logicalHost(), v.Content.Repository.NameWithOwner)
				out.RepositoryID = v.Content.Repository.ID
			}
			i := issue(*v.Content, "")
			p := out.Placement
			i.Board = &p
			out.Issue = &i
		}
	}
	return out
}
func (c *Client) ProjectItems(ctx context.Context, id domain.NodeID, p domain.PageRequest) (domain.Page[domain.ProjectItem], error) {
	v, err := pageVariables(p)
	if err != nil {
		return domain.Page[domain.ProjectItem]{}, err
	}
	v["id"] = id
	var out struct{ Node *rawProject }
	if err = c.query(ctx, "BoardItems", v, &out); err != nil {
		return domain.Page[domain.ProjectItem]{}, err
	}
	if out.Node == nil {
		return domain.Page[domain.ProjectItem]{}, malformed("missing project")
	}
	if out.Node.Items.Nodes == nil {
		return domain.Page[domain.ProjectItem]{}, malformed("missing project-items connection")
	}
	return pageResult(out.Node.Items, c.mapItem), nil
}
func (c *Client) IssuePlacements(ctx context.Context, id domain.NodeID) ([]domain.ProjectItem, error) {
	var out struct {
		Node *struct{ ProjectItems connection[rawPlacement] }
	}
	if err := c.query(ctx, "IssuePlacements", map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	if out.Node == nil {
		return nil, malformed("missing issue")
	}
	if out.Node.ProjectItems.Nodes == nil {
		return nil, malformed("missing issue placements connection")
	}
	result := make([]domain.ProjectItem, 0, len(out.Node.ProjectItems.Nodes))
	for _, v := range out.Node.ProjectItems.Nodes {
		result = append(result, c.mapItem(v))
	}
	return result, nil
}
