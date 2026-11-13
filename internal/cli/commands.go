package cli

import (
	"context"
	"io"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service"
	"github.com/spf13/cobra"
)

type CommandSpec struct {
	Use, Short, Group, Example string
	MinArgs, MaxArgs           int
	Flags                      func(*cobra.Command)
	Run                        func(context.Context, *runtime, *cobra.Command, []string) (any, error)
}

func usage(message string) error { return &domain.Error{Kind: domain.ErrUsage, Message: message} }
func listFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.String("state", "open", "State to include")
	f.Int("limit", 50, "Maximum results")
	f.Bool("all", false, "Read every available result")
}
func commandSpecs() []CommandSpec {
	return []CommandSpec{
		{Use: "list", Short: "List issues", Group: "issues", Example: "  todos list --mine\n  todos list --state all --json", Flags: func(c *cobra.Command) {
			listFlags(c)
			f := c.Flags()
			f.String("assignee", "", "Filter by assignee login")
			f.Bool("mine", false, "Filter by your login")
			f.StringArray("label", nil, "Require a label (repeat for AND)")
			f.String("search", "", "GitHub issue search text")
		}, Run: runList},
		{Use: "view ISSUE", Short: "View an issue and its conversation", Group: "issues", MinArgs: 1, MaxArgs: 1, Example: "  todos view 12\n  todos view '#12' --json", Run: runView},
		{Use: "board", Short: "Show the selected project board", Group: "board", Example: "  todos board\n  todos board --json", Run: runBoard},
		{Use: "board list", Short: "List candidate project boards", Example: "  todos board list\n  todos board list --json", Run: runBoardList},
		{Use: "board use PROJECT", Short: "Remember the board for this repository", MinArgs: 1, MaxArgs: 1, Example: "  todos board use 1\n  todos board use owner/2 --json", Run: runBoardUse},
		{Use: "pr list", Short: "List pull requests", Flags: listFlags, Example: "  todos pr list\n  todos pr list --state merged --json", Run: runPRList},
		{Use: "pr view PR", Short: "View a pull request and its conversation", MinArgs: 1, MaxArgs: 1, Example: "  todos pr view 12\n  todos pr view '#12' --json", Run: runPRView},
		{Use: "labels", Short: "List repository labels", Group: "other", Example: "  todos labels\n  todos labels --json", Run: runLabels},
		{Use: "assignees", Short: "List assignable users", Group: "other", Example: "  todos assignees\n  todos assignees --json", Run: runAssignees},
		{Use: "doctor", Short: "Check credentials and repository setup without writing", Group: "other", Example: "  todos doctor\n  todos doctor --json", Run: runDoctor},
	}
}
func addReadCommands(root *cobra.Command, deps Dependencies, version func() error) {
	root.AddGroup(&cobra.Group{ID: "issues", Title: "Issues:"}, &cobra.Group{ID: "board", Title: "Board:"}, &cobra.Group{ID: "prs", Title: "Pull requests:"})
	parents := map[string]*cobra.Command{"": root}
	pr := &cobra.Command{Use: "pr", Short: "Read pull requests", GroupID: "prs", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		show, _ := c.Flags().GetBool("version")
		if show {
			return version()
		}
		return usage("choose pr list or pr view")
	}}
	root.AddCommand(pr)
	parents["pr"] = pr
	specs := append(commandSpecs(), writeCommandSpecs()...)
	specs = append(specs, quickAddCommand())
	for _, spec := range specs {
		parent := root
		use := spec.Use
		if len(use) > 6 && use[:6] == "board " {
			parent = parents["board"]
			spec.Use = use[6:]
		} else if len(use) > 3 && use[:3] == "pr " {
			parent = pr
			spec.Use = use[3:]
		}
		r := &runtime{deps: deps}
		c := bindCommand(parent, spec, r)
		run := c.RunE
		c.RunE = func(cmd *cobra.Command, args []string) error {
			show, _ := cmd.Flags().GetBool("version")
			if show {
				return version()
			}
			return run(cmd, args)
		}
		if use == "board" {
			parents["board"] = c
		}
	}
}
func bindCommand(parent *cobra.Command, spec CommandSpec, r *runtime) *cobra.Command {
	argsCheck := cobra.RangeArgs(spec.MinArgs, spec.MaxArgs)
	if spec.MaxArgs < 0 {
		argsCheck = cobra.MinimumNArgs(spec.MinArgs)
	}
	c := &cobra.Command{Use: spec.Use, Short: spec.Short, Example: spec.Example, GroupID: spec.Group, Args: argsCheck}
	if spec.Flags != nil {
		spec.Flags(c)
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if err := validateFlags(cmd, args); err != nil {
			return err
		}
		r.global.Repo, _ = cmd.Flags().GetString("repo")
		r.global.Project, _ = cmd.Flags().GetString("project")
		r.global.JSON, _ = cmd.Flags().GetBool("json")
		r.global.NoColor, _ = cmd.Flags().GetBool("no-color")
		r.mode = Piped
		if r.deps.IsTTY(r.deps.Out) {
			r.mode = Human
		}
		if r.global.JSON {
			r.mode = JSON
		}
		value, err := spec.Run(cmd.Context(), r, cmd, args)
		if err != nil {
			_, report := value.(DoctorDTO)
			_, quick := value.(QuickDTO)
			if !report && !quick {
				return domainError(err)
			}
		}
		if renderErr := r.printResult(cmd.Context(), value); renderErr != nil {
			return renderErr
		}
		if err != nil {
			if _, doctor := value.(DoctorDTO); doctor {
				return &reportedFailure{Err: err}
			}
			return domainError(err)
		}
		return nil
	}
	parent.AddCommand(c)
	return c
}
func validateFlags(cmd *cobra.Command, args []string) error {
	if err := validateWriteFlags(cmd, args); err != nil {
		return err
	}
	if cmd.Flags().Lookup("limit") != nil {
		limit, _ := cmd.Flags().GetInt("limit")
		all, _ := cmd.Flags().GetBool("all")
		if limit <= 0 {
			return usage("--limit must be positive")
		}
		if all && cmd.Flags().Changed("limit") {
			return usage("--limit and --all are mutually exclusive")
		}
	}
	if cmd.Flags().Lookup("mine") != nil {
		mine, _ := cmd.Flags().GetBool("mine")
		if mine && cmd.Flags().Changed("assignee") {
			return usage("--mine and --assignee are mutually exclusive")
		}
	}
	if cmd.Flags().Lookup("state") != nil {
		state, _ := cmd.Flags().GetString("state")
		pr := cmd.Parent() != nil && cmd.Parent().Name() == "pr"
		if state != "open" && state != "closed" && state != "all" && !(pr && state == "merged") {
			return &domain.Error{Kind: domain.ErrInvalidInput, Message: "invalid state filter"}
		}
	}
	return nil
}
func listOptions(c *cobra.Command) ListOptions {
	limit, _ := c.Flags().GetInt("limit")
	all, _ := c.Flags().GetBool("all")
	return ListOptions{Limit: limit, All: all}
}
func runList(ctx context.Context, r *runtime, c *cobra.Command, _ []string) (any, error) {
	state, _ := c.Flags().GetString("state")
	r.listState = state
	assignee, _ := c.Flags().GetString("assignee")
	mine, _ := c.Flags().GetBool("mine")
	if mine {
		assignee = "@me"
	}
	labels, _ := c.Flags().GetStringArray("label")
	search, _ := c.Flags().GetString("search")
	if err := r.ensureService(ctx); err != nil {
		return nil, err
	}
	return collectIssues(ctx, r.wiring.Service, domain.IssueFilter{State: state, Assignee: assignee, Labels: labels, Search: search}, listOptions(c))
}
func runView(ctx context.Context, r *runtime, _ *cobra.Command, args []string) (any, error) {
	repo, err := r.resolvedRepo(ctx)
	if err != nil {
		return nil, err
	}
	n, err := service.ParseIssueRef(args[0], repo, false)
	if err != nil {
		return nil, err
	}
	if err = r.ensureService(ctx); err != nil {
		return nil, err
	}
	v, err := r.wiring.Service.GetIssue(ctx, n)
	if err != nil {
		return nil, err
	}
	r.issueOnBoard = v.Issue.Board != nil
	comments, truncated, err := collectComments(ctx, func(ctx context.Context, p domain.PageRequest) (domain.Page[domain.Comment], error) {
		return r.wiring.Service.ListIssueComments(ctx, n, p)
	})
	if err != nil {
		return nil, err
	}
	return issueViewDTO(v, comments, truncated), nil
}
func runPRList(ctx context.Context, r *runtime, c *cobra.Command, _ []string) (any, error) {
	if err := r.ensureService(ctx); err != nil {
		return nil, err
	}
	state, _ := c.Flags().GetString("state")
	r.listState = state
	return collectPRs(ctx, r.wiring.Service, domain.PRFilter{State: state}, listOptions(c))
}
func runPRView(ctx context.Context, r *runtime, _ *cobra.Command, args []string) (any, error) {
	repo, err := r.resolvedRepo(ctx)
	if err != nil {
		return nil, err
	}
	n, err := service.ParseIssueRef(args[0], repo, true)
	if err != nil {
		return nil, err
	}
	if err = r.ensureService(ctx); err != nil {
		return nil, err
	}
	v, err := r.wiring.Service.GetPR(ctx, n)
	if err != nil {
		return nil, err
	}
	comments, truncated, err := collectComments(ctx, func(ctx context.Context, p domain.PageRequest) (domain.Page[domain.Comment], error) {
		return r.wiring.Service.ListPRComments(ctx, n, p)
	})
	if err != nil {
		return nil, err
	}
	return prViewDTO(v, comments, truncated), nil
}
func runBoard(ctx context.Context, r *runtime, _ *cobra.Command, _ []string) (any, error) {
	if err := r.ensureService(ctx); err != nil {
		return nil, err
	}
	b, err := r.wiring.Service.Board(ctx)
	if err != nil {
		return nil, err
	}
	return boardDTO(b), nil
}
func runBoardList(ctx context.Context, r *runtime, _ *cobra.Command, _ []string) (any, error) {
	if err := r.ensureService(ctx); err != nil {
		return nil, err
	}
	v, err := r.wiring.Service.BoardCandidates(ctx)
	if err != nil {
		return nil, err
	}
	out := ProjectsDTO{Projects: []ProjectDTO{}}
	for _, p := range v.Projects {
		out.Projects = append(out.Projects, projectDTO(p))
	}
	if v.Selected != nil {
		p := projectDTO(*v.Selected)
		out.Selected = &p
	}
	return out, nil
}
func runBoardUse(ctx context.Context, r *runtime, _ *cobra.Command, args []string) (any, error) {
	repo, err := r.resolvedRepo(ctx)
	if err != nil {
		return nil, err
	}
	ref, err := service.ParseProjectRef(args[0], repo)
	if err != nil {
		return nil, err
	}
	if err = r.ensureService(ctx); err != nil {
		return nil, err
	}
	p, err := r.wiring.Service.UseBoard(ctx, ref)
	if err != nil {
		return nil, err
	}
	return ProjectResultDTO{Project: projectDTO(p)}, nil
}
func runLabels(ctx context.Context, r *runtime, _ *cobra.Command, _ []string) (any, error) {
	if err := r.ensureService(ctx); err != nil {
		return nil, err
	}
	labels, err := r.wiring.Service.Labels(ctx)
	if err != nil {
		return nil, err
	}
	out := LabelsDTO{Labels: []LabelDTO{}}
	for _, l := range labels {
		out.Labels = append(out.Labels, LabelDTO{Name: l.Name, Description: l.Description, Color: l.Color})
	}
	return out, nil
}
func runAssignees(ctx context.Context, r *runtime, _ *cobra.Command, _ []string) (any, error) {
	if err := r.ensureService(ctx); err != nil {
		return nil, err
	}
	users, err := r.wiring.Service.AssignableUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := AssigneesDTO{Assignees: []UserDTO{}}
	for _, u := range users {
		out.Assignees = append(out.Assignees, UserDTO{Login: u.Login, Name: u.Name})
	}
	return out, nil
}
func runDoctor(ctx context.Context, r *runtime, _ *cobra.Command, _ []string) (any, error) {
	if err := r.ensureService(ctx); err != nil {
		v, e := doctorWiringFailure(err)
		return doctorDTO(v), e
	}
	v, err := r.wiring.Service.Doctor(ctx)
	if err != nil && len(v.Checks) != 6 {
		v, err = doctorWiringFailure(err)
	}
	return doctorDTO(v), err
}
func doctorWiringFailure(err error) (domain.DoctorReport, error) {
	err = domainError(err)
	de := err.(*domain.Error)
	v := domain.DoctorReport{FirstFailure: de, Checks: []domain.DoctorCheck{}}
	for _, name := range []string{"Credentials", "Viewer", "Repository", "Board", "Board access", "Config"} {
		v.Checks = append(v.Checks, domain.DoctorCheck{Name: name, Detail: "blocked: " + de.Message, Hint: de.Hint})
	}
	return v, err
}
func renderJSON(w io.Writer, value any) error { return encodeJSON(w, value) }
