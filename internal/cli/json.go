package cli

import (
	"github.com/pkieltyka/todos/internal/domain"
	"time"
)

type IssueDTO struct {
	Number        int      `json:"number"`
	Title         string   `json:"title"`
	State         string   `json:"state"`
	StateReason   *string  `json:"state_reason"`
	Author        *string  `json:"author"`
	Assignees     []string `json:"assignees"`
	Labels        []string `json:"labels"`
	Milestone     *string  `json:"milestone"`
	CommentsCount int      `json:"comments_count"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
	ClosedAt      *string  `json:"closed_at"`
	URL           string   `json:"url"`
	BoardStatus   *string  `json:"board_status"`
}
type CommentDTO struct {
	ID        string  `json:"id"`
	Author    *string `json:"author"`
	CreatedAt string  `json:"created_at"`
	Body      string  `json:"body"`
	URL       string  `json:"url"`
}
type IssueViewDTO struct {
	IssueDTO
	AsOf              string       `json:"as_of"`
	Body              string       `json:"body"`
	Comments          []CommentDTO `json:"comments"`
	CommentsTruncated bool         `json:"comments_truncated"`
}
type PRDTO struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	State     string   `json:"state"`
	Draft     bool     `json:"draft"`
	Author    *string  `json:"author"`
	Head      string   `json:"head"`
	Base      string   `json:"base"`
	Review    string   `json:"review"`
	Checks    string   `json:"checks"`
	Assignees []string `json:"assignees"`
	Labels    []string `json:"labels"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	ClosedAt  *string  `json:"closed_at"`
	MergedAt  *string  `json:"merged_at"`
	URL       string   `json:"url"`
}
type PRViewDTO struct {
	PRDTO
	Body              string       `json:"body"`
	Mergeable         string       `json:"mergeable"`
	Closes            []int        `json:"closes"`
	Comments          []CommentDTO `json:"comments"`
	CommentsTruncated bool         `json:"comments_truncated"`
}
type ProjectDTO struct {
	Number int    `json:"number"`
	Owner  string `json:"owner"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Usable bool   `json:"usable"`
}
type CardDTO struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	State     string   `json:"state"`
	Assignees []string `json:"assignees"`
	Labels    []string `json:"labels"`
	URL       string   `json:"url"`
}
type ColumnDTO struct {
	Status *string   `json:"status"`
	Cards  []CardDTO `json:"cards"`
}
type BoardDTO struct {
	AsOf        string      `json:"as_of"`
	Project     ProjectDTO  `json:"project"`
	Columns     []ColumnDTO `json:"columns"`
	HiddenItems int         `json:"hidden_items"`
	Truncated   bool        `json:"truncated"`
}
type IssuesDTO struct {
	AsOf      string     `json:"as_of"`
	Issues    []IssueDTO `json:"issues"`
	Total     int        `json:"total"`
	Truncated bool       `json:"truncated"`
}
type PRsDTO struct {
	PullRequests []PRDTO `json:"pull_requests"`
	Total        int     `json:"total"`
	Truncated    bool    `json:"truncated"`
}
type ChangeDTO struct {
	Issue   IssueDTO `json:"issue"`
	Changed bool     `json:"changed"`
}
type CommentResultDTO struct {
	Issue   IssueDTO   `json:"issue"`
	Comment CommentDTO `json:"comment"`
}
type DeletedDTO struct {
	Deleted int `json:"deleted"`
}
type ProjectsDTO struct {
	Projects []ProjectDTO `json:"projects"`
	Selected *ProjectDTO  `json:"selected"`
}
type ProjectResultDTO struct {
	Project ProjectDTO `json:"project"`
}
type LabelDTO struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
}
type UserDTO struct {
	Login string `json:"login"`
	Name  string `json:"name"`
}
type LabelsDTO struct {
	Labels []LabelDTO `json:"labels"`
}
type AssigneesDTO struct {
	Assignees []UserDTO `json:"assignees"`
}
type CheckDTO struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Hint   string `json:"hint"`
}
type DoctorDTO struct {
	OK     bool       `json:"ok"`
	Checks []CheckDTO `json:"checks"`
}
type VersionDTO struct {
	Version string `json:"version"`
}
type ErrorDTO struct {
	Kind              string    `json:"kind"`
	Message           string    `json:"message"`
	Hint              string    `json:"hint"`
	RetryAfterSeconds *int      `json:"retry_after_seconds,omitempty"`
	Uncertain         bool      `json:"uncertain"`
	Completed         *[]string `json:"completed,omitempty"`
	Cause             *string   `json:"cause,omitempty"`
	Issue             *IssueDTO `json:"issue,omitempty"`
}
type ErrorEnvelope struct {
	Error ErrorDTO `json:"error"`
}
type QuickItemDTO struct {
	Line    int       `json:"line"`
	Title   string    `json:"title"`
	Body    string    `json:"body"`
	Checked bool      `json:"checked"`
	Result  string    `json:"result"`
	Issue   *IssueDTO `json:"issue"`
	OnBoard bool      `json:"on_board"`
	Closed  bool      `json:"closed"`
	Error   *ErrorDTO `json:"error"`
}
type QuickCountsDTO struct {
	Created     int `json:"created"`
	Failed      int `json:"failed"`
	Uncertain   int `json:"uncertain"`
	Unattempted int `json:"unattempted"`
	Skipped     int `json:"skipped"`
	Planned     int `json:"planned"`
}
type QuickDTO struct {
	Items  []QuickItemDTO `json:"items"`
	Counts QuickCountsDTO `json:"counts"`
}

func asOfTimestamp(v time.Time) string { return v.UTC().Format("2006-01-02T15:04:05.000Z") }

func timestamp(v time.Time) string { return v.UTC().Format(time.RFC3339) }
func timePointer(v *time.Time) *string {
	if v == nil {
		return nil
	}
	s := timestamp(*v)
	return &s
}
func stringsCopy(v []string) []string { return append([]string{}, v...) }
func labelNames(v []domain.Label) []string {
	out := make([]string, 0, len(v))
	for _, l := range v {
		out = append(out, l.Name)
	}
	return out
}
func issueDTO(v domain.Issue) IssueDTO {
	out := IssueDTO{Number: v.Number, Title: v.Title, State: string(v.State), Author: v.Author, Assignees: stringsCopy(v.Assignees), Labels: labelNames(v.Labels), Milestone: v.Milestone, CommentsCount: v.CommentCount, CreatedAt: timestamp(v.CreatedAt), UpdatedAt: timestamp(v.UpdatedAt), ClosedAt: timePointer(v.ClosedAt), URL: v.URL}
	if v.StateReason != nil {
		s := string(*v.StateReason)
		out.StateReason = &s
	}
	if v.Board != nil {
		out.BoardStatus = v.Board.Status
	}
	return out
}
func commentDTO(v domain.Comment) CommentDTO {
	return CommentDTO{ID: v.ID, Author: v.Author, CreatedAt: timestamp(v.CreatedAt), Body: v.Body, URL: v.URL}
}
func issueViewDTO(v domain.IssueDetail, comments []CommentDTO, truncated bool) IssueViewDTO {
	return IssueViewDTO{IssueDTO: issueDTO(v.Issue), Body: v.Body, Comments: append([]CommentDTO{}, comments...), CommentsTruncated: truncated}
}
func prDTO(v domain.PullRequest) PRDTO {
	return PRDTO{Number: v.Number, Title: v.Title, State: string(v.State), Draft: v.Draft, Author: v.Author, Head: v.Head, Base: v.Base, Review: string(v.Review), Checks: string(v.Checks), Assignees: stringsCopy(v.Assignees), Labels: labelNames(v.Labels), CreatedAt: timestamp(v.CreatedAt), UpdatedAt: timestamp(v.UpdatedAt), ClosedAt: timePointer(v.ClosedAt), MergedAt: timePointer(v.MergedAt), URL: v.URL}
}
func prViewDTO(v domain.PRDetail, comments []CommentDTO, truncated bool) PRViewDTO {
	return PRViewDTO{PRDTO: prDTO(v.PullRequest), Body: v.Body, Mergeable: string(v.Mergeable), Closes: append([]int{}, v.Closes...), Comments: append([]CommentDTO{}, comments...), CommentsTruncated: truncated}
}
func projectDTO(v domain.Project) ProjectDTO {
	return ProjectDTO{Number: v.Ref.Number, Owner: v.Ref.Owner, Title: v.Title, URL: v.URL, Usable: v.Usable}
}
func boardDTO(v domain.Board) BoardDTO {
	out := BoardDTO{Project: projectDTO(v.Project), Columns: []ColumnDTO{}, HiddenItems: v.HiddenCount, Truncated: v.Truncated}
	for _, c := range v.Columns {
		col := ColumnDTO{Status: c.Status, Cards: []CardDTO{}}
		for _, card := range c.Cards {
			col.Cards = append(col.Cards, CardDTO{Number: card.Number, Title: card.Title, State: string(card.State), Assignees: stringsCopy(card.Assignees), Labels: labelNames(card.Labels), URL: card.URL})
		}
		out.Columns = append(out.Columns, col)
	}
	return out
}
func changeDTO(v domain.Change) ChangeDTO {
	return ChangeDTO{Issue: issueDTO(v.Issue), Changed: v.Changed}
}
func errorDTO(v *domain.Error) ErrorDTO {
	out := ErrorDTO{Kind: string(v.Kind), Message: v.Message, Hint: v.Hint, Uncertain: v.Uncertain}
	if v.Kind == domain.ErrRateLimited {
		seconds := int(v.RetryAfter / time.Second)
		if v.RetryAfter%time.Second > 0 {
			seconds++
		}
		out.RetryAfterSeconds = &seconds
	}
	if v.Kind == domain.ErrPartial {
		completed := []string{}
		for _, step := range v.Completed {
			switch step {
			case "created", "commented", "added_to_board":
				completed = append(completed, step)
			}
		}
		out.Completed = &completed
		cause := string(v.Cause)
		out.Cause = &cause
		if v.Issue != nil {
			issue := issueDTO(*v.Issue)
			out.Issue = &issue
		}
	}
	return out
}
func doctorDTO(v domain.DoctorReport) DoctorDTO {
	out := DoctorDTO{OK: v.OK, Checks: []CheckDTO{}}
	for _, c := range v.Checks {
		out.Checks = append(out.Checks, CheckDTO{Name: c.Name, OK: c.OK, Detail: c.Detail, Hint: c.Hint})
	}
	return out
}
