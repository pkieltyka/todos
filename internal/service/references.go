package service

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/pkieltyka/todos/internal/domain"
)

func positive(s string) (int, error) {
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, invalid("reference must contain a positive number")
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, invalid("reference must contain a positive number")
	}
	return n, nil
}
func ParseIssueRef(text string, repo domain.RepoRef, pr bool) (int, error) {
	text = strings.TrimSpace(text)
	if !strings.Contains(text, "://") {
		return positive(strings.TrimPrefix(text, "#"))
	}
	u, err := url.Parse(text)
	if err != nil || u.Scheme != "https" || u.User != nil || !strings.EqualFold(u.Host, repo.Host) || u.RawQuery != "" {
		return 0, invalid("issue URL must use the selected repository host")
	}
	path := strings.Split(strings.Trim(u.Path, "/"), "/")
	kind := "issues"
	if pr {
		kind = "pull"
	}
	if len(path) != 4 || !strings.EqualFold(path[0], repo.Owner) || !strings.EqualFold(path[1], repo.Name) || path[2] != kind {
		return 0, invalid("URL must refer to the selected repository and issue kind")
	}
	return positive(path[3])
}
func ParseProjectRef(text string, repo domain.RepoRef) (domain.ProjectRef, error) {
	result := domain.ProjectRef{Host: repo.Host, Owner: repo.Owner}
	text = strings.TrimSpace(text)
	if strings.Contains(text, "://") {
		u, err := url.Parse(text)
		if err != nil || u.Scheme != "https" || u.User != nil || !strings.EqualFold(u.Host, repo.Host) || u.RawQuery != "" {
			return result, invalid("project URL must use the selected GitHub host")
		}
		path := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(path) != 4 || (path[0] != "users" && path[0] != "orgs") || path[2] != "projects" {
			return result, invalid("invalid project URL")
		}
		result.Owner = path[1]
		text = path[3]
	} else if owner, n, ok := strings.Cut(text, "/"); ok {
		if owner == "" || strings.Contains(n, "/") {
			return result, invalid("invalid project reference")
		}
		result.Owner = owner
		text = n
	}
	if result.Owner == "" || strings.ContainsAny(result.Owner, " \\:@?#") {
		return result, invalid("invalid project owner")
	}
	number, err := positive(text)
	result.Number = number
	return result, err
}
