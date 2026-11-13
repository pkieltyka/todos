package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/markdown"
	"github.com/spf13/cobra"
)

func quickAddCommand() CommandSpec {
	return CommandSpec{Use: "quick-add", Short: "Create issues from a punch list", Group: "issues", Example: "  todos quick-add --file tasks.md --dry-run --json\n  todos quick-add --file - --checked close --status Sprint --json", Flags: func(c *cobra.Command) {
		f := c.Flags()
		f.String("file", "", "Punch-list file, or - for stdin (required)")
		f.StringArray("assign", nil, "Assignee login (repeatable)")
		f.StringArray("label", nil, "Existing label (repeatable)")
		f.String("status", "", "Board status for open items")
		f.String("checked", "skip", "skip or close checked items")
		f.Bool("dry-run", false, "Validate and preview without creating issues")
	}, Run: runQuickAdd}
}
func runQuickAdd(ctx context.Context, r *runtime, c *cobra.Command, _ []string) (any, error) {
	path, _ := c.Flags().GetString("file")
	checked, _ := c.Flags().GetString("checked")
	if !c.Flags().Changed("file") || path == "" {
		return nil, usage("quick-add requires --file PATH (or --file - for stdin)")
	}
	if checked != "skip" && checked != "close" {
		return nil, usage("--checked must be skip or close")
	}
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(r.deps.In)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, &domain.Error{Kind: domain.ErrInvalidInput, Message: "could not read --file", Err: err}
	}
	opts := domain.QuickAddOptions{Checked: domain.CheckedMode(checked)}
	opts.Assignees, _ = c.Flags().GetStringArray("assign")
	opts.Labels, _ = c.Flags().GetStringArray("label")
	if c.Flags().Changed("status") {
		status, _ := c.Flags().GetString("status")
		opts.Status = &status
		opts.AddToBoard = true
	}
	if err := r.ensureService(ctx); err != nil {
		return nil, err
	}
	plan, err := r.wiring.Service.PlanQuickAdd(ctx, string(data), opts)
	if err != nil {
		return nil, err
	}
	if len(plan.Problems) > 0 {
		return nil, planProblems(plan.Problems)
	}
	dry, _ := c.Flags().GetBool("dry-run")
	r.quickOptions = plan.Options
	result, err := r.wiring.Service.RunQuickAdd(ctx, plan, domain.QuickAddRunOptions{DryRun: dry})
	if err != nil && result.Items == nil {
		return nil, err
	}
	return quickDTO(result), err
}
func planProblems(problems []domain.Problem) error {
	lines := []string{}
	for _, p := range problems {
		lines = append(lines, fmt.Sprintf("line %d: %s", p.Line, p.Message))
	}
	return &domain.Error{Kind: domain.ErrInvalidInput, Message: strings.Join(lines, "; "), Line: problems[0].Line}
}
func quickDTO(result domain.QuickAddResult) QuickDTO {
	out := QuickDTO{Items: []QuickItemDTO{}, Counts: QuickCountsDTO{Created: result.Counts.Created, Failed: result.Counts.Failed, Uncertain: result.Counts.Uncertain, Unattempted: result.Counts.Unattempted, Skipped: result.Counts.Skipped, Planned: result.Counts.Planned}}
	for _, row := range result.Items {
		item := QuickItemDTO{Line: row.Item.Line, Title: row.Item.Title, Body: row.Item.Body, Checked: row.Item.Checked, Result: string(row.Result), OnBoard: row.OnBoard, Closed: row.Closed}
		if row.Issue != nil {
			i := issueDTO(*row.Issue)
			item.Issue = &i
		}
		if row.Error != nil {
			e := errorDTO(row.Error)
			item.Error = &e
		}
		out.Items = append(out.Items, item)
	}
	return out
}
func renderQuick(r *runtime, w io.Writer, result QuickDTO) error {
	for _, row := range result.Items {
		label := row.Result
		role := "muted"
		switch row.Result {
		case "created":
			role = "open"
		case "failed":
			role = "error"
		case "uncertain":
			role = "warning"
		}
		number := ""
		if row.Issue != nil {
			number = fmt.Sprintf(" #%d", row.Issue.Number)
		}
		details := []string{}
		if row.Result == "created" {
			if row.Closed {
				details = append(details, "closed")
			} else if row.Checked && r.quickOptions.Checked == "close" {
				details = append(details, "not closed")
			}
			if row.OnBoard {
				details = append(details, "on board")
			} else if !row.Checked && r.quickOptions.AddToBoard {
				details = append(details, "not placed on board")
			}
		}
		if row.Result == "planned" && row.Checked {
			details = append(details, "will close")
		}
		suffix := ""
		if len(details) > 0 {
			suffix = " (" + strings.Join(details, ", ") + ")"
		}
		if row.Error != nil {
			suffix += " — " + markdown.SanitizeLine(row.Error.Message)
		}
		fmt.Fprintf(w, "%s%s %s%s\n", r.paint(role, label), number, markdown.SanitizeLine(row.Title), suffix)
	}
	c := result.Counts
	fmt.Fprintf(w, "Created %d; failed %d; uncertain %d; unattempted %d; skipped %d; planned %d.\n", c.Created, c.Failed, c.Uncertain, c.Unattempted, c.Skipped, c.Planned)
	return nil
}
