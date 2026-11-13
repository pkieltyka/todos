package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/markdown"
	"github.com/pkieltyka/todos/internal/service"
	"github.com/spf13/cobra"
)

func bodyFlags(c *cobra.Command) {
	c.Flags().String("body", "", "Markdown body")
	c.Flags().String("body-file", "", "Read body from a file, or - for stdin")
}
func writeCommandSpecs() []CommandSpec {
	specs := []CommandSpec{
		{Use: "add TITLE", Short: "Create an issue", Group: "issues", MinArgs: 1, MaxArgs: 1, Flags: func(c *cobra.Command) {
			bodyFlags(c)
			c.Flags().StringArray("assign", nil, "Assignee login (repeatable)")
			c.Flags().StringArray("label", nil, "Existing label (repeatable)")
			c.Flags().String("status", "", "Board status")
		}},
		{Use: "edit ISSUE", Short: "Edit an issue's title or body", Group: "issues", MinArgs: 1, MaxArgs: 1, Flags: func(c *cobra.Command) { bodyFlags(c); c.Flags().String("title", "", "New title") }},
		{Use: "assign ISSUE LOGIN...", Short: "Add issue assignees", Group: "issues", MinArgs: 2, MaxArgs: -1},
		{Use: "unassign ISSUE [LOGIN...]", Short: "Remove assignees (all when omitted)", Group: "issues", MinArgs: 1, MaxArgs: -1},
		{Use: "reassign ISSUE LOGIN...", Short: "Replace all assignees", Group: "issues", MinArgs: 2, MaxArgs: -1},
		{Use: "label ISSUE NAME...", Short: "Add existing labels", Group: "issues", MinArgs: 2, MaxArgs: -1},
		{Use: "unlabel ISSUE NAME...", Short: "Remove labels", Group: "issues", MinArgs: 2, MaxArgs: -1},
		{Use: "comment ISSUE [TEXT]", Short: "Add a Markdown comment", Group: "issues", MinArgs: 1, MaxArgs: 2, Flags: func(c *cobra.Command) { c.Flags().String("body-file", "", "Read comment from a file, or - for stdin") }},
		{Use: "close ISSUE", Short: "Close an issue", Group: "issues", MinArgs: 1, MaxArgs: 1, Flags: func(c *cobra.Command) {
			c.Flags().String("reason", "completed", "completed or not-planned")
			c.Flags().String("comment", "", "Closing comment")
		}},
		{Use: "reopen ISSUE", Short: "Reopen a closed issue", Group: "issues", MinArgs: 1, MaxArgs: 1},
		{Use: "rm ISSUE --yes", Short: "Permanently delete an issue", Group: "issues", MinArgs: 1, MaxArgs: 1, Flags: func(c *cobra.Command) { c.Flags().Bool("yes", false, "Confirm permanent deletion") }},
		{Use: "move ISSUE [STATUS]", Short: "Move an issue to a board status", Group: "board", MinArgs: 1, MaxArgs: 2, Flags: func(c *cobra.Command) { c.Flags().Bool("no-status", false, "Clear the board status") }},
		{Use: "board add ISSUE", Short: "Add an issue to the board", MinArgs: 1, MaxArgs: 1, Flags: func(c *cobra.Command) { c.Flags().String("status", "", "Board status") }},
		{Use: "board remove ISSUE", Short: "Remove an issue from the board", MinArgs: 1, MaxArgs: 1},
	}
	for i := range specs {
		specs[i].Run = runChange
		name := strings.Fields(specs[i].Use)[0]
		examples := map[string]string{"add": "add 'Fix login' --body 'Details'", "edit": "edit 12 --title 'New title'", "assign": "assign 12 @me", "unassign": "unassign 12", "reassign": "reassign 12 @me", "label": "label 12 bug", "unlabel": "unlabel 12 bug", "comment": "comment 12 'Verified'", "close": "close 12 --reason completed", "reopen": "reopen 12", "rm": "rm 12 --yes", "move": "move 12 'In progress'"}
		example := examples[name]
		if name == "board" {
			example = specs[i].Use[:strings.LastIndex(specs[i].Use, " ")] + " 12"
		}
		specs[i].Example = "  todos " + example + "\n  todos " + example + " --json"
	}
	return specs
}
func validateWriteFlags(c *cobra.Command, args []string) error {
	f := c.Flags()
	if f.Lookup("body") != nil && f.Changed("body") && f.Changed("body-file") {
		return usage("--body and --body-file are mutually exclusive")
	}
	if f.Lookup("body-file") != nil && f.Changed("body-file") {
		path, _ := f.GetString("body-file")
		if path == "" {
			return usage("--body-file requires a path or -")
		}
	}
	switch c.Name() {
	case "edit":
		if !f.Changed("title") && !f.Changed("body") && !f.Changed("body-file") {
			return usage("edit requires --title, --body or --body-file")
		}
	case "comment":
		if (len(args) == 2) == f.Changed("body-file") {
			return usage("comment requires either TEXT or --body-file")
		}
		if len(args) == 2 && strings.TrimSpace(args[1]) == "" {
			return usage("comment must not be blank")
		}
	case "close":
		reason, _ := f.GetString("reason")
		if reason != "completed" && reason != "not-planned" {
			return usage("--reason must be completed or not-planned")
		}
		if f.Changed("comment") {
			text, _ := f.GetString("comment")
			if strings.TrimSpace(text) == "" {
				return usage("--comment must not be blank")
			}
		}
	case "rm":
		yes, _ := f.GetBool("yes")
		if !yes {
			return usage("permanent deletion requires --yes")
		}
	case "move":
		none, _ := f.GetBool("no-status")
		if (len(args) == 2) == none {
			return usage("move requires either STATUS or --no-status")
		}
		if len(args) == 2 && strings.TrimSpace(args[1]) == "" {
			return usage("status must not be blank")
		}
	}
	return nil
}
func readBody(r *runtime, c *cobra.Command) (string, error) {
	if !c.Flags().Changed("body-file") {
		body, _ := c.Flags().GetString("body")
		return body, nil
	}
	path, _ := c.Flags().GetString("body-file")
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(r.deps.In)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", &domain.Error{Kind: domain.ErrInvalidInput, Message: "could not read --body-file", Err: err}
	}
	return string(b), nil
}
func runChange(ctx context.Context, r *runtime, c *cobra.Command, args []string) (any, error) {
	var body string
	var err error
	if c.Name() == "add" || c.Name() == "edit" || c.Name() == "comment" {
		if c.Name() == "comment" && len(args) == 2 {
			body = args[1]
		} else {
			body, err = readBody(r, c)
			if err != nil {
				return nil, err
			}
		}
		if c.Name() == "comment" && strings.TrimSpace(body) == "" {
			return nil, usage("comment must not be blank")
		}
	}
	var n int
	if c.Name() != "add" || c.Parent().Name() == "board" {
		repo, err := r.resolvedRepo(ctx)
		if err != nil {
			return nil, err
		}
		n, err = service.ParseIssueRef(args[0], repo, false)
		if err != nil {
			return nil, err
		}
	}
	if err := r.ensureService(ctx); err != nil {
		return nil, err
	}
	s := r.wiring.Service
	var change domain.Change
	r.action = map[string]string{"add": "Created", "edit": "Edited", "assign": "Assigned", "unassign": "Unassigned", "reassign": "Reassigned", "label": "Labeled", "unlabel": "Unlabeled", "close": "Closed", "reopen": "Reopened", "move": "Moved"}[c.Name()]
	switch c.Name() {
	case "add":
		if c.Parent().Name() == "board" {
			r.action = "Added to board"
			var status *string
			if c.Flags().Changed("status") {
				v, _ := c.Flags().GetString("status")
				status = &v
			}
			change, err = s.BoardAdd(ctx, n, status)
		} else {
			assign, _ := c.Flags().GetStringArray("assign")
			labels, _ := c.Flags().GetStringArray("label")
			var status *string
			if c.Flags().Changed("status") {
				v, _ := c.Flags().GetString("status")
				status = &v
			}
			change, err = s.CreateIssue(ctx, domain.NewIssue{Title: args[0], Body: body, Assignees: assign, Labels: labels, AddToBoard: status != nil, Status: status})
		}
	case "edit":
		in := domain.EditIssue{}
		if c.Flags().Changed("title") {
			v, _ := c.Flags().GetString("title")
			in.Title = &v
		}
		if c.Flags().Changed("body") || c.Flags().Changed("body-file") {
			in.Body = &body
		}
		change, err = s.EditIssue(ctx, n, in)
	case "assign":
		change, err = s.UpdateAssignees(ctx, n, args[1:], nil)
	case "unassign":
		names := args[1:]
		if len(names) == 0 {
			d, e := s.GetIssue(ctx, n)
			if e != nil {
				return nil, e
			}
			names = d.Issue.Assignees
		}
		change, err = s.UpdateAssignees(ctx, n, nil, names)
	case "reassign":
		change, err = s.Reassign(ctx, n, args[1:])
	case "label":
		change, err = s.UpdateLabels(ctx, n, args[1:], nil)
	case "unlabel":
		change, err = s.UpdateLabels(ctx, n, nil, args[1:])
	case "close":
		reason, _ := c.Flags().GetString("reason")
		in := domain.CloseOptions{Reason: strings.ReplaceAll(reason, "-", "_")}
		if c.Flags().Changed("comment") {
			v, _ := c.Flags().GetString("comment")
			in.Comment = &v
		}
		change, err = s.Close(ctx, n, in)
	case "reopen":
		change, err = s.Reopen(ctx, n)
	case "rm":
		if err := s.Delete(ctx, n); err != nil {
			return nil, err
		}
		return DeletedDTO{Deleted: n}, nil
	case "comment":
		result, e := s.Comment(ctx, n, body)
		if e != nil {
			return nil, e
		}
		return CommentResultDTO{Issue: issueDTO(result.Issue), Comment: commentDTO(result.Comment)}, nil
	case "move":
		var status *string
		if len(args) == 2 {
			status = &args[1]
		}
		change, err = s.SetStatus(ctx, n, status)
	case "remove":
		r.action = "Removed from board"
		change, err = s.RemoveFromBoard(ctx, n)
	default:
		return nil, &domain.Error{Kind: domain.ErrInternal, Message: "unknown change command"}
	}
	if err != nil {
		return nil, err
	}
	return changeDTO(change), nil
}
func renderChange(r *runtime, w io.Writer, value any) error {
	switch v := value.(type) {
	case ChangeDTO:
		action := r.action
		if !v.Changed {
			action = "No change to"
		}
		fmt.Fprintf(w, "%s #%d %s  %s\n", action, v.Issue.Number, markdown.SanitizeLine(v.Issue.Title), markdown.SanitizeLine(v.Issue.URL))
	case CommentResultDTO:
		fmt.Fprintf(w, "Commented on #%d %s  %s\n", v.Issue.Number, markdown.SanitizeLine(v.Issue.Title), markdown.SanitizeLine(v.Comment.URL))
	case DeletedDTO:
		fmt.Fprintf(w, "Deleted #%d\n", v.Deleted)
	default:
		return &domain.Error{Kind: domain.ErrInternal, Message: "unsupported change output"}
	}
	return nil
}
