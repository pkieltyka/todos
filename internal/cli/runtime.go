package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/auth"
	ghrepo "github.com/cli/go-gh/v2/pkg/repository"
	"github.com/google/shlex"
	"github.com/pkieltyka/todos/internal/config"
	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/github"
	"github.com/pkieltyka/todos/internal/markdown"
	"github.com/pkieltyka/todos/internal/service"
	"github.com/pkieltyka/todos/internal/tui"
	"golang.org/x/term"
)

type GlobalOptions struct {
	Repo, Project          string
	JSON, NoColor, Version bool
}
type OutputMode int

const (
	Human OutputMode = iota
	Piped
	JSON
)

type Wiring struct {
	Service       service.Service
	Drafts        tui.DraftStore
	ConfigWarning func() *domain.Error
}
type Dependencies struct {
	In          io.Reader
	Out, Err    io.Writer
	Skill       []byte
	Version     string
	Getenv      func(string) string
	LookupEnv   func(string) (string, bool)
	Now         func() time.Time
	IsTTY       func(io.Writer) bool
	InputIsTTY  func(io.Reader) bool
	Width       func(io.Writer) int
	ResolveRepo func(context.Context, string) (domain.RepoRef, error)
	Wire        func(context.Context, func(context.Context) (domain.RepoRef, error), string) (Wiring, error)
	RunTUI      func(context.Context, tui.Deps, tui.Options) error
	Open        func(string) error
	Interrupted func() bool
}
type runtime struct {
	deps         Dependencies
	global       GlobalOptions
	mode         OutputMode
	repoMu       sync.Mutex
	repo         domain.RepoRef
	wiring       Wiring
	wired        bool
	renderer     *markdown.Renderer
	issueOnBoard bool
	listState    string
	action       string
	quickOptions domain.QuickAddOptions
}
type ListOptions struct {
	Limit int
	All   bool
}
type reportedFailure struct{ Err error }

func (e *reportedFailure) Error() string { return e.Err.Error() }
func (e *reportedFailure) Unwrap() error { return e.Err }
func ProductionDependencies(skill []byte, version string) Dependencies {
	return normaliseDeps(Dependencies{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Skill: skill, Version: version})
}
func completeDeps(d Dependencies) Dependencies {
	if d.RunTUI == nil {
		d.RunTUI = tui.Run
	}
	if d.Open == nil {
		d.Open = quietBrowser
	}
	if d.Getenv == nil {
		d.Getenv = os.Getenv
	}
	if d.LookupEnv == nil {
		d.LookupEnv = os.LookupEnv
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.IsTTY == nil {
		d.IsTTY = func(w io.Writer) bool { f, ok := w.(*os.File); return ok && term.IsTerminal(int(f.Fd())) }
	}
	if d.InputIsTTY == nil {
		d.InputIsTTY = func(r io.Reader) bool { f, ok := r.(*os.File); return ok && term.IsTerminal(int(f.Fd())) }
	}
	if d.Width == nil {
		d.Width = func(w io.Writer) int {
			if f, ok := w.(*os.File); ok {
				width, _, err := term.GetSize(int(f.Fd()))
				if err == nil && width > 0 {
					return width
				}
			}
			return 80
		}
	}
	if d.ResolveRepo == nil {
		d.ResolveRepo = func(ctx context.Context, flag string) (domain.RepoRef, error) {
			return resolveRepository(ctx, flag, d.Getenv, ghrepo.Current)
		}
	}
	if d.Wire == nil {
		d.Wire = func(ctx context.Context, resolve func(context.Context) (domain.RepoRef, error), project string) (Wiring, error) {
			paths, err := config.DefaultPaths(d.Getenv, os.UserHomeDir)
			if err != nil {
				return Wiring{}, err
			}
			settings := config.New(paths)
			client := github.New(github.Options{Version: d.Version, Host: func(ctx context.Context) (string, error) {
				ref, err := resolve(ctx)
				if err != nil {
					host, _ := auth.DefaultHost()
					return host, nil
				}
				return ref.Host, nil
			}})
			return Wiring{Service: service.New(client, settings, service.Options{ResolveRepository: resolve, Project: project, Now: d.Now}), Drafts: config.NewDraftStore(paths), ConfigWarning: settings.Warning}, nil
		}
	}
	return d
}

var _ service.Backend = (*github.Client)(nil)

func ResolveRepository(ctx context.Context, flag string) (domain.RepoRef, error) {
	return resolveRepository(ctx, flag, os.Getenv, ghrepo.Current)
}
func resolveRepository(ctx context.Context, flag string, getenv func(string) string, current func() (ghrepo.Repository, error)) (domain.RepoRef, error) {
	if ctx.Err() != nil {
		return domain.RepoRef{}, &domain.Error{Kind: domain.ErrNetwork, Message: "operation interrupted", Err: ctx.Err()}
	}
	var r ghrepo.Repository
	var err error
	explicit := flag != ""
	text := flag
	if text == "" {
		text = getenv("GH_REPO")
		explicit = text != ""
	}
	if explicit {
		r, err = ghrepo.Parse(text)
	} else {
		r, err = current()
	}
	if err != nil {
		kind := domain.ErrNoRepo
		message := "could not determine repository"
		if explicit {
			kind = domain.ErrInvalidInput
			message = "invalid repository reference"
		}
		return domain.RepoRef{}, &domain.Error{Kind: kind, Message: message, Hint: "use --repo OWNER/REPO", Err: err}
	}
	return domain.RepoRef{Host: r.Host, Owner: r.Owner, Name: r.Name}, nil
}
func (r *runtime) ensureService(ctx context.Context) error {
	if r.wired {
		return nil
	}
	resolve := r.resolvedRepo
	wiring, err := r.deps.Wire(ctx, resolve, r.global.Project)
	if err != nil {
		return domainError(err)
	}
	if wiring.Service == nil {
		return &domain.Error{Kind: domain.ErrInternal, Message: "service wiring returned no service"}
	}
	r.wiring = wiring
	r.wired = true
	return nil
}
func domainError(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if errors.As(err, &de) {
		return de
	}
	if errors.Is(err, context.Canceled) {
		return &domain.Error{Kind: domain.ErrNetwork, Message: "operation interrupted", Err: err}
	}
	return &domain.Error{Kind: domain.ErrInternal, Message: "internal operation failed", Err: err}
}
func (r *runtime) resolvedRepo(ctx context.Context) (domain.RepoRef, error) {
	r.repoMu.Lock()
	defer r.repoMu.Unlock()
	if r.repo.Host != "" {
		return r.repo, nil
	}
	ref, err := r.deps.ResolveRepo(ctx, r.global.Repo)
	if err != nil {
		return ref, domainError(err)
	}
	r.repo = ref
	return ref, nil
}

func (r *runtime) startTUI(ctx context.Context, view tui.ViewID) error {
	if r.global.JSON {
		return usage("--json cannot be used with the TUI")
	}
	if !r.deps.InputIsTTY(r.deps.In) || !r.deps.IsTTY(r.deps.Out) {
		return usage("the TUI requires a terminal for input and output")
	}
	repo, err := r.resolvedRepo(ctx)
	if err != nil {
		return err
	}
	_, noColorEnv := r.deps.LookupEnv("NO_COLOR")
	env := tui.Env{Theme: r.deps.Getenv("TODOS_THEME"), NoColor: r.global.NoColor || noColorEnv}
	editorName := "VISUAL"
	editor := r.deps.Getenv(editorName)
	if editor == "" {
		editorName = "EDITOR"
		editor = r.deps.Getenv(editorName)
	}
	if editor != "" {
		env.Editor, err = shlex.Split(editor)
		if err != nil || len(env.Editor) == 0 {
			return usage(editorName + " must contain a valid editor command")
		}
	}
	if err = r.ensureService(ctx); err != nil {
		return err
	}
	r.renderer = markdown.NewRenderer(markdown.Options{BaseURL: "https://" + repo.Host + "/" + repo.Owner + "/" + repo.Name})
	if strings.TrimSpace(env.Theme) != "" {
		if _, ok := tui.ParseOverride(env.Theme); !ok {
			_, _ = fmt.Fprintln(r.deps.Err, "warning: invalid TODOS_THEME; expected light or dark; using terminal background detection")
		}
	}
	if r.wiring.ConfigWarning != nil {
		if warning := r.wiring.ConfigWarning(); warning != nil {
			_, _ = fmt.Fprintln(r.deps.Err, "warning:", markdown.SanitizeLine(warning.Message))
		}
	}
	err = r.deps.RunTUI(ctx, tui.Deps{Service: r.wiring.Service, Drafts: r.wiring.Drafts, Renderer: r.renderer, Clock: r.deps.Now, Open: r.deps.Open, Env: env}, tui.Options{StartView: view, Repo: repo})
	if err != nil && ctx.Err() != nil {
		return domainError(ctx.Err())
	}
	return domainError(err)
}
