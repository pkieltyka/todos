// Package cli implements the todos command-line entry point.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/markdown"
	"github.com/pkieltyka/todos/internal/tui"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func Main(skill []byte, version string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	deps := ProductionDependencies(skill, version)
	deps.Interrupted = func() bool { return ctx.Err() != nil }
	os.Exit(Run(ctx, os.Args[1:], deps))
}

func normaliseDeps(deps Dependencies) Dependencies {
	if deps.In == nil {
		deps.In = strings.NewReader("")
	}
	if deps.Out == nil {
		deps.Out = io.Discard
	}
	if deps.Err == nil {
		deps.Err = io.Discard
	}
	if deps.Version == "" {
		deps.Version = "dev"
	}
	return completeDeps(deps)
}

func NewRoot(deps Dependencies) *cobra.Command {
	deps = normaliseDeps(deps)
	var asJSON, showVersion bool
	root := &cobra.Command{Use: "todos", Short: "GitHub issues and project boards in your terminal", Long: "todos with no arguments opens the TUI (Issues view).\nWork with one GitHub repository and one project board.", SilenceUsage: true, SilenceErrors: true, Args: cobra.NoArgs}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetIn(deps.In)
	root.SetOut(deps.Out)
	root.SetErr(deps.Err)
	flags := root.PersistentFlags()
	flags.String("repo", "", "Repository [HOST/]OWNER/REPO")
	flags.String("project", "", "Project NUMBER, OWNER/NUMBER or URL")
	flags.BoolVar(&asJSON, "json", false, "Print a JSON document")
	flags.Bool("no-color", false, "Disable colour")
	flags.BoolVar(&showVersion, "version", false, "Print the version")
	writeVersion := func() error {
		if asJSON {
			data, err := json.Marshal(struct {
				Version string `json:"version"`
			}{deps.Version})
			if err != nil {
				return outputError(err)
			}
			return writeOutput(deps.Out, append(data, '\n'))
		}
		return writeOutput(deps.Out, []byte(deps.Version+"\n"))
	}
	root.RunE = func(cmd *cobra.Command, _ []string) error {
		if showVersion {
			return writeVersion()
		}
		if asJSON {
			return &domain.Error{Kind: domain.ErrUsage, Message: "--json cannot be used with the TUI"}
		}
		r := tuiRuntime(cmd, deps)
		return r.startTUI(cmd.Context(), tui.ViewIssues)
	}
	root.AddCommand(&cobra.Command{Use: "version", Short: "Print the todos version", GroupID: "other", Args: cobra.NoArgs, Example: "  todos version\n  todos version --json", RunE: func(*cobra.Command, []string) error { return writeVersion() }})
	root.AddCommand(&cobra.Command{Use: "skill", Short: "Print the agent skill", GroupID: "other", Args: cobra.NoArgs, Example: "  todos skill\n  todos skill > SKILL.md", RunE: func(*cobra.Command, []string) error {
		if showVersion {
			return writeVersion()
		}
		if asJSON {
			return &domain.Error{Kind: domain.ErrUsage, Message: "--json cannot be used with skill"}
		}
		return writeOutput(deps.Out, deps.Skill)
	}})
	addReadCommands(root, deps, writeVersion)
	addTUICommand(root, deps, writeVersion)
	root.AddGroup(&cobra.Group{ID: "other", Title: "Other commands:"})
	return root
}

func Run(ctx context.Context, args []string, deps Dependencies) int {
	deps = normaliseDeps(deps)
	root := NewRoot(deps)
	root.SetArgs(append([]string{}, args...))
	parseFailed := false
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { parseFailed = true; return err })
	var cmd *cobra.Command
	var err error
	if ctx.Err() != nil {
		err = &domain.Error{Kind: domain.ErrNetwork, Message: "operation interrupted", Err: ctx.Err()}
	} else {
		cmd, err = root.ExecuteContextC(ctx)
	}
	if err == nil {
		return 0
	}
	var reported *reportedFailure
	if errors.As(err, &reported) {
		return exitCode(reported.Err, deps.Interrupted != nil && deps.Interrupted())
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		kind := domain.ErrUsage
		// Cobra returns plain errors for command/flag/argument parsing.
		// Writer failures are identifiable by their common path/sentinel types.
		var pathErr *os.PathError
		if errors.As(err, &pathErr) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, io.ErrShortWrite) {
			kind = domain.ErrInternal
		}
		de = &domain.Error{Kind: kind, Message: err.Error(), Err: err}
	}
	if errorJSONMode(root, cmd, args, parseFailed) {
		_ = json.NewEncoder(deps.Err).Encode(ErrorEnvelope{Error: errorDTO(de)})
	} else {
		_, _ = fmt.Fprintln(deps.Err, "error:", markdown.SanitizeLine(de.Message))
		if de.Hint != "" {
			_, _ = fmt.Fprintln(deps.Err, "hint:", markdown.SanitizeLine(de.Hint))
		}
		if de.Kind == domain.ErrUsage {
			if cmd == nil {
				cmd = root
			}
			_, _ = fmt.Fprintln(deps.Err, "Usage:", cmd.UseLine())
		}
	}
	interrupted := deps.Interrupted != nil && deps.Interrupted()
	return exitCode(de, interrupted)
}

func outputError(err error) error {
	if err == nil {
		return nil
	}
	return &domain.Error{Kind: domain.ErrInternal, Message: "could not write output", Err: err}
}
func writeOutput(w io.Writer, data []byte) error {
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return outputError(err)
}

// Prefer Cobra's actual value whenever parsing succeeded, even for argument or
// command errors. Raw scanning is only a recovery path for unparsed arguments.
func errorJSONMode(root, cmd *cobra.Command, args []string, parseFailed bool) bool {
	if cmd != nil && cmd.Flags().Parsed() && !parseFailed {
		value, _ := cmd.Flags().GetBool("json")
		return value
	}
	return detectJSON(root, args)
}

func detectJSON(root *cobra.Command, args []string) bool {
	// Collect metadata from the command tree so newly added value flags cannot
	// accidentally turn their values into output-mode switches on parse failure.
	long := map[string]*pflag.Flag{}
	short := map[string]*pflag.Flag{}
	var collect func(*cobra.Command)
	collect = func(cmd *cobra.Command) {
		visit := func(flag *pflag.Flag) {
			if old := long[flag.Name]; old == nil || flag.NoOptDefVal == "" {
				long[flag.Name] = flag
			}
			if flag.Shorthand != "" {
				if old := short[flag.Shorthand]; old == nil || flag.NoOptDefVal == "" {
					short[flag.Shorthand] = flag
				}
			}
		}
		cmd.Flags().VisitAll(visit)
		cmd.PersistentFlags().VisitAll(visit)
		for _, child := range cmd.Commands() {
			collect(child)
		}
	}
	collect(root)
	value := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if name, ok := strings.CutPrefix(arg, "--"); ok {
			name, raw, attached := strings.Cut(name, "=")
			flag := long[name]
			if flag == nil {
				continue
			}
			if flag.NoOptDefVal == "" && !attached {
				i++
				continue
			}
			if name == "json" {
				if !attached {
					raw = flag.NoOptDefVal
				}
				if parsed, err := strconv.ParseBool(raw); err == nil {
					value = parsed
				}
			}
			continue
		}
		if strings.HasPrefix(arg, "-") {
			for j := 1; j < len(arg); j++ {
				flag := short[arg[j:j+1]]
				if flag == nil {
					continue
				}
				if flag.NoOptDefVal == "" {
					if j+1 == len(arg) {
						i++
					}
					break
				}
			}
		}
	}
	return value
}

func exitCode(err error, interrupted bool) int {
	if err == nil {
		return 0
	}
	if interrupted && errors.Is(err, context.Canceled) {
		return 130
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		return 1
	}
	switch de.Kind {
	case domain.ErrUsage, domain.ErrInvalidInput:
		return 2
	case domain.ErrNotFound:
		return 3
	case domain.ErrAuth, domain.ErrMissingScope, domain.ErrForbidden:
		return 4
	case domain.ErrRateLimited:
		return 5
	case domain.ErrNoRepo, domain.ErrNoBoard, domain.ErrIssuesDisabled, domain.ErrConfig:
		return 6
	case domain.ErrPartial:
		return 7
	default:
		return 1
	}
}

func tuiRuntime(cmd *cobra.Command, deps Dependencies) *runtime {
	r := &runtime{deps: deps, mode: Human}
	r.global.Repo, _ = cmd.Flags().GetString("repo")
	r.global.Project, _ = cmd.Flags().GetString("project")
	r.global.JSON, _ = cmd.Flags().GetBool("json")
	r.global.NoColor, _ = cmd.Flags().GetBool("no-color")
	return r
}
func addTUICommand(root *cobra.Command, deps Dependencies, version func() error) {
	cmd := &cobra.Command{Use: "tui [issues|prs|board]", Short: "Open the terminal UI", GroupID: "other", Args: cobra.MaximumNArgs(1), Example: "  todos tui\n  todos tui board --repo pkieltyka/playg", RunE: func(cmd *cobra.Command, args []string) error {
		show, _ := cmd.Flags().GetBool("version")
		if show {
			return version()
		}
		view := tui.ViewIssues
		if len(args) > 0 {
			switch args[0] {
			case "issues":
			case "prs":
				view = tui.ViewPRs
			case "board":
				view = tui.ViewBoard
			default:
				return usage("TUI view must be issues, prs or board")
			}
		}
		return tuiRuntime(cmd, deps).startTUI(cmd.Context(), view)
	}}
	root.AddCommand(cmd)
}
