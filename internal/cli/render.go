package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/markdown"
)

func encodeJSON(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return &domain.Error{Kind: domain.ErrInternal, Message: "could not encode output", Err: err}
	}
	return writeOutput(w, append(data, '\n'))
}
func (r *runtime) printResult(ctx context.Context, value any) error {
	if value == nil {
		return &domain.Error{Kind: domain.ErrInternal, Message: "command returned no result"}
	}
	var buf bytes.Buffer
	var err error
	switch r.mode {
	case JSON:
		err = renderJSON(&buf, value)
	case Human:
		err = renderHuman(ctx, r, &buf, value)
	default:
		err = renderPiped(ctx, r, &buf, value)
	}
	if err != nil {
		return domainError(err)
	}
	if _, doctor := value.(DoctorDTO); !doctor && ctx.Err() != nil {
		return domainError(ctx.Err())
	}
	if err = writeOutput(r.deps.Out, buf.Bytes()); err != nil {
		return err
	}
	if r.mode == Human {
		raw := strings.TrimSpace(r.deps.Getenv("TODOS_THEME"))
		if raw != "" && !strings.EqualFold(raw, "light") && !strings.EqualFold(raw, "dark") {
			_, _ = fmt.Fprintln(r.deps.Err, "warning: invalid TODOS_THEME; expected light or dark; using dark")
		}
	}
	if _, doctor := value.(DoctorDTO); !doctor && r.mode != JSON && r.wiring.ConfigWarning != nil {
		if warning := r.wiring.ConfigWarning(); warning != nil {
			_, _ = fmt.Fprintln(r.deps.Err, "warning:", markdown.SanitizeLine(warning.Message))
		}
	}
	return nil
}
func tsvField(s string) string {
	return markdown.SanitizeLine(strings.NewReplacer("\\", "\\\\", "\t", "\\t", "\n", "\\n", "\r", "\\r").Replace(s))
}
func tsv(w io.Writer, fields ...string) {
	for i := range fields {
		fields[i] = tsvField(fields[i])
	}
	fmt.Fprintln(w, strings.Join(fields, "\t"))
}
func summary(w io.Writer, shown, total int, truncated bool) {
	if truncated {
		fmt.Fprintf(w, "Showing %d of %d items.\n", shown, total)
	}
}
func renderPiped(ctx context.Context, r *runtime, w io.Writer, value any) error {
	switch v := value.(type) {
	case IssuesDTO:
		if len(v.Issues) == 0 {
			fmt.Fprintln(w, "No issues found.")
		}
		for _, i := range v.Issues {
			tsv(w, strconv.Itoa(i.Number), i.Title, i.State, strings.Join(i.Labels, ","), strings.Join(i.Assignees, ","), i.UpdatedAt, i.URL)
		}
		summary(w, len(v.Issues), v.Total, v.Truncated)
	case PRsDTO:
		if len(v.PullRequests) == 0 {
			fmt.Fprintln(w, "No pull requests found.")
		}
		for _, p := range v.PullRequests {
			tsv(w, strconv.Itoa(p.Number), p.Title, p.State, p.Head, p.Review, p.Checks, p.UpdatedAt, p.URL)
		}
		summary(w, len(v.PullRequests), v.Total, v.Truncated)
	case BoardDTO:
		cards := 0
		for _, column := range v.Columns {
			status := ""
			if column.Status != nil {
				status = *column.Status
			}
			for _, i := range column.Cards {
				cards++
				tsv(w, status, strconv.Itoa(i.Number), i.Title, i.State, strings.Join(i.Labels, ","), strings.Join(i.Assignees, ","), i.URL)
			}
		}
		if cards == 0 {
			fmt.Fprintln(w, "No issues on this board.")
		}
		if v.HiddenItems > 0 {
			fmt.Fprintf(w, "Hidden items: %d.\n", v.HiddenItems)
		}
		if v.Truncated {
			fmt.Fprintln(w, "Board truncated at 1000 items.")
		}
	case ProjectsDTO:
		if len(v.Projects) == 0 {
			fmt.Fprintln(w, "No projects found.")
		}
		for _, p := range v.Projects {
			tsv(w, p.Owner, strconv.Itoa(p.Number), p.Title, p.URL, strconv.FormatBool(p.Usable))
		}
	case ProjectResultDTO:
		p := v.Project
		tsv(w, p.Owner, strconv.Itoa(p.Number), p.Title, p.URL, strconv.FormatBool(p.Usable))
	case LabelsDTO:
		if len(v.Labels) == 0 {
			fmt.Fprintln(w, "No labels found.")
		}
		for _, l := range v.Labels {
			tsv(w, l.Name, l.Description, l.Color)
		}
	case AssigneesDTO:
		if len(v.Assignees) == 0 {
			fmt.Fprintln(w, "No assignable users found.")
		}
		for _, u := range v.Assignees {
			tsv(w, u.Login, u.Name)
		}
	case IssueViewDTO:
		return renderDetail(ctx, r, w, v.Title, v.Number, v.State, v.URL, v.Body, v.Comments, v.CommentsTruncated, issueMetadata(v))
	case PRViewDTO:
		return renderDetail(ctx, r, w, v.Title, v.Number, v.State, v.URL, v.Body, v.Comments, v.CommentsTruncated, prMetadata(v))
	case DoctorDTO:
		for _, c := range v.Checks {
			tsv(w, c.Name, strconv.FormatBool(c.OK), c.Detail, c.Hint)
		}
	default:
		return &domain.Error{Kind: domain.ErrInternal, Message: "unsupported output type"}
	}
	return nil
}
func (r *runtime) colours() bool {
	_, noColor := r.deps.LookupEnv("NO_COLOR")
	return r.mode == Human && !r.global.NoColor && !noColor
}
func (r *runtime) width() int {
	width := r.deps.Width(r.deps.Out)
	if width < 1 {
		return 80
	}
	return width
}
func renderDetail(ctx context.Context, r *runtime, w io.Writer, title string, number int, state, url, body string, comments []CommentDTO, truncated bool, metadata []string) error {
	title = markdown.SanitizeLine(title)
	fmt.Fprintf(w, "#%d %s\n%s  %s\n", number, title, markdown.SanitizeLine(state), markdown.SanitizeLine(url))
	for _, line := range metadata {
		fmt.Fprintln(w, markdown.SanitizeLine(line))
	}
	fmt.Fprintln(w)
	renderBody := func(source string) error {
		if ctx.Err() != nil {
			return domainError(ctx.Err())
		}
		fmt.Fprintln(w, markdown.SanitizeBody(source))
		return nil
	}
	if err := renderBody(body); err != nil {
		return err
	}
	for _, c := range comments {
		author := "deleted user"
		if c.Author != nil {
			author = markdown.SanitizeLine(*c.Author)
		}
		fmt.Fprintf(w, "\nComment by %s · %s\n", author, markdown.SanitizeLine(c.CreatedAt))
		if err := renderBody(c.Body); err != nil {
			return err
		}
	}
	if truncated {
		fmt.Fprintln(w, "Comments truncated at 500.")
	}
	return nil
}

func optionalText(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func issueMetadata(v IssueViewDTO) []string {
	lines := []string{"Author: " + optionalText(v.Author), "Assignees: " + strings.Join(v.Assignees, ", "), "Labels: " + strings.Join(v.Labels, ", "), "Created: " + v.CreatedAt, "Updated: " + v.UpdatedAt}
	if v.StateReason != nil {
		lines = append(lines, "Reason: "+*v.StateReason)
	}
	if v.Milestone != nil {
		lines = append(lines, "Milestone: "+*v.Milestone)
	}
	if v.ClosedAt != nil {
		lines = append(lines, "Closed: "+*v.ClosedAt)
	}
	return lines
}
func prMetadata(v PRViewDTO) []string {
	lines := []string{"Author: " + optionalText(v.Author), "Assignees: " + strings.Join(v.Assignees, ", "), "Labels: " + strings.Join(v.Labels, ", "), "Branches: " + v.Head + " → " + v.Base, "Review: " + v.Review + "; checks: " + v.Checks, "Mergeable: " + v.Mergeable, "Created: " + v.CreatedAt, "Updated: " + v.UpdatedAt}
	if v.Draft {
		lines = append(lines, "Draft: yes")
	}
	if v.ClosedAt != nil {
		lines = append(lines, "Closed: "+*v.ClosedAt)
	}
	if v.MergedAt != nil {
		lines = append(lines, "Merged: "+*v.MergedAt)
	}
	if len(v.Closes) > 0 {
		numbers := []string{}
		for _, n := range v.Closes {
			numbers = append(numbers, fmt.Sprintf("#%d", n))
		}
		lines = append(lines, "Closes: "+strings.Join(numbers, ", "))
	}
	return lines
}
