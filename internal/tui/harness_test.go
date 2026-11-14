package tui

import (
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/markdown"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

var fixtureNow = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

// harness drives App.Update directly (tui.md 18.1): every returned Cmd runs
// in its own goroutine and its message is fed back in arrival order. Timers
// are recorded, never slept on; tests fire them explicitly.
type harness struct {
	t    *testing.T
	app  *App
	fake *servicetest.Fake

	msgs    chan tea.Msg
	wake    chan struct{}
	running int // dispatched Cmds, run goroutines and sent messages not yet handled; under mu

	mu     sync.Mutex
	timers []tea.Msg
	delays []time.Duration // the duration of every Tick, in order
	now    time.Time
	gates  []*hgate
	opened []string
	quit   bool
	lazy   bool
}

type hgate struct {
	g    *servicetest.Gate
	done bool
}

type harnessOpt func(*harness, *Deps, *Options)

func withStart(v ViewID) harnessOpt { return func(_ *harness, _ *Deps, o *Options) { o.StartView = v } }
func withTheme(s string) harnessOpt { return func(_ *harness, d *Deps, _ *Options) { d.Env.Theme = s } }
func withEditor(argv ...string) harnessOpt {
	return func(_ *harness, d *Deps, _ *Options) { d.Env.Editor = argv }
}

// withLazy builds the App without starting it, so gates can be registered
// before Init; call boot.
func withLazy() harnessOpt { return func(h *harness, _ *Deps, _ *Options) { h.lazy = true } }

func newHarness(t *testing.T, fx servicetest.Fixture, opts ...harnessOpt) *harness {
	t.Helper()
	h := &harness{t: t, fake: servicetest.New(fx), msgs: make(chan tea.Msg, 256), wake: make(chan struct{}, 64), now: fixtureNow}
	deps := Deps{
		Service:  h.fake,
		Renderer: markdown.NewRenderer(markdown.Options{BaseURL: "https://github.com/pkieltyka/todos"}),
		Clock:    func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now },
		Open:     func(u string) error { h.mu.Lock(); defer h.mu.Unlock(); h.opened = append(h.opened, u); return nil },
		Env:      Env{Theme: "dark"},
		// The external editor runs directly; tea.ExecProcess only works
		// inside a real program.
		Exec: func(cmd *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
			return func() tea.Msg { return fn(cmd.Run()) }
		},
		// A quick-add run goroutine counts as running until it returns; its
		// messages count until handled, so a run parked at a gate counts as
		// blocked like any gated call.
		Go: func(f func()) {
			h.mu.Lock()
			h.running++
			h.mu.Unlock()
			go func() { f(); h.msgs <- goroutineDone{} }()
		},
		Send: func(m tea.Msg) {
			h.mu.Lock()
			h.running++
			h.mu.Unlock()
			h.msgs <- m
		},
		Tick: func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
			return func() tea.Msg {
				h.mu.Lock()
				h.timers = append(h.timers, fn(h.now.Add(d)))
				h.delays = append(h.delays, d)
				h.mu.Unlock()
				return nil
			}
		},
	}
	o := Options{StartView: ViewIssues, Repo: domain.RepoRef{Host: "github.com", Owner: "pkieltyka", Name: "todos"}}
	for _, opt := range opts {
		opt(h, &deps, &o)
	}
	h.app = New(deps, o)
	if !h.lazy {
		h.boot()
	}
	return h
}

// boot runs Init and sizes the window to 80×24.
func (h *harness) boot() {
	h.t.Helper()
	h.dispatch(h.app.Init())
	h.size(80, 24)
}

// goroutineDone marks the end of a goroutine started through Deps.Go.
type goroutineDone struct{}

func (h *harness) dispatch(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	h.mu.Lock()
	h.running++
	h.mu.Unlock()
	go func() { h.msgs <- cmd() }()
}

func (h *harness) runningNow() int { h.mu.Lock(); defer h.mu.Unlock(); return h.running }

func (h *harness) handle(msg tea.Msg) {
	switch m := msg.(type) {
	case nil:
		return
	case tea.BatchMsg:
		for _, c := range m {
			h.dispatch(c)
		}
		return
	case tea.QuitMsg:
		h.quit = true
		return
	}
	_, cmd := h.app.Update(msg)
	h.dispatch(cmd)
}

// blocked counts goroutines parked at a gate the test has not released.
func (h *harness) blocked() int {
	n := 0
	for _, g := range h.gates {
		if g.done {
			continue
		}
		select {
		case <-g.g.Started:
			n++
		default:
		}
	}
	return n
}

// settleDeadline only detects a hang: settle returns as soon as everything
// not held at a gate is done, so a passing test never waits for it. It is
// generous because one settle can cover a whole load (500 comments, their
// pages and renders) on a slow, shared CI runner under -race, which once
// took over 5 s.
const settleDeadline = 30 * time.Second

// settle runs until nothing is running except calls held at gates.
func (h *harness) settle() {
	h.t.Helper()
	deadline := time.After(settleDeadline)
	for h.runningNow() > h.blocked() {
		select {
		case m := <-h.msgs:
			h.mu.Lock()
			h.running--
			h.mu.Unlock()
			if _, done := m.(goroutineDone); !done {
				h.handle(m)
			}
		case <-h.wake:
		case <-deadline:
			h.t.Fatalf("harness did not settle: %d running, %d blocked", h.runningNow(), h.blocked())
		}
	}
}

func (h *harness) send(msg tea.Msg) { h.t.Helper(); h.handle(msg); h.settle() }

func (h *harness) size(w, hgt int) { h.t.Helper(); h.send(tea.WindowSizeMsg{Width: w, Height: hgt}) }

// press sends keys: "j", "enter", "ctrl+d", or text for text mode.
func (h *harness) press(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		h.send(keyMsg(k))
	}
}

func keyMsg(k string) tea.KeyPressMsg {
	named := map[string]tea.Key{
		"enter": {Code: tea.KeyEnter}, "esc": {Code: tea.KeyEscape}, "tab": {Code: tea.KeyTab},
		"shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift}, "backspace": {Code: tea.KeyBackspace},
		"up": {Code: tea.KeyUp}, "down": {Code: tea.KeyDown}, "left": {Code: tea.KeyLeft}, "right": {Code: tea.KeyRight},
		"ctrl+c": {Code: 'c', Mod: tea.ModCtrl}, "ctrl+d": {Code: 'd', Mod: tea.ModCtrl}, "ctrl+u": {Code: 'u', Mod: tea.ModCtrl},
		"space": {Code: tea.KeySpace, Text: " "},
	}
	if k2, ok := named[k]; ok {
		return tea.KeyPressMsg(k2)
	}
	if strings.HasPrefix(k, "ctrl+") && len(k) == 6 {
		return tea.KeyPressMsg(tea.Key{Code: rune(k[5]), Mod: tea.ModCtrl})
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg(tea.Key{Code: r, Text: k})
}

// typeText types runes one key at a time.
func (h *harness) typeText(s string) {
	h.t.Helper()
	for _, r := range s {
		h.send(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
	}
}

// hold registers a gate the harness knows about.
func (h *harness) hold(method string, n int) *hgate {
	g := &hgate{g: h.fake.Hold(method, n)}
	h.gates = append(h.gates, g)
	go func() { <-g.g.Started; h.wake <- struct{}{} }()
	return g
}

// pass registers a gate that lets its call through at once (so later gates
// on the same method hold later calls).
func (h *harness) pass(method string, n int) {
	g := h.hold(method, n)
	g.g.Release()
	g.done = true
}

func (h *harness) release(g *hgate) { g.g.Release(); g.done = true; h.settle() }

func (h *harness) fail(g *hgate, err error) { g.g.Fail(err); g.done = true; h.settle() }

// fire delivers every recorded timer whose message has the type of sample.
func (h *harness) fire(sample tea.Msg) {
	h.t.Helper()
	h.mu.Lock()
	var due, keep []tea.Msg
	for _, m := range h.timers {
		if reflect.TypeOf(m) == reflect.TypeOf(sample) {
			due = append(due, m)
		} else {
			keep = append(keep, m)
		}
	}
	h.timers = keep
	h.mu.Unlock()
	for _, m := range due {
		h.send(m)
	}
}

func (h *harness) advance(d time.Duration) { h.mu.Lock(); h.now = h.now.Add(d); h.mu.Unlock() }

// view is the frame with styling stripped.
func (h *harness) view() string { return ansi.Strip(h.app.View().Content) }

func (h *harness) contains(s string) {
	h.t.Helper()
	if v := h.view(); !strings.Contains(v, s) {
		h.t.Fatalf("view does not contain %q:\n%s", s, v)
	}
}

func (h *harness) lacks(s string) {
	h.t.Helper()
	if v := h.view(); strings.Contains(v, s) {
		h.t.Fatalf("view contains %q:\n%s", s, v)
	}
}

func (h *harness) calls(method string) []servicetest.Call {
	var out []servicetest.Call
	for _, c := range h.fake.Calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// richFixture adds what goldens need on top of the default fixture: bodies
// with Markdown, comments and pull requests in every state.
func richFixture() servicetest.Fixture {
	fx := servicetest.DefaultFixture()
	fx.Issues[0].Body = "When `gh` can't reach the network the refresh spinner never stops.\n\n## Steps\n\n1. Disconnect wifi\n2. Press r\n\n```go\nif err != nil { return err }\n```\n"
	fx.IssueComments[6] = append(fx.IssueComments[6], domain.Comment{ID: "C2", Author: ptr("pkieltyka"), CreatedAt: fixtureNow.Add(-30 * time.Minute), Body: "Thanks, assigning to me."})
	fx.Issues[0].Issue.CommentCount = 2
	return fx
}

func ptr[T any](v T) *T { return &v }

// nudge is a message nothing handles; sending it runs Update and Sync.
type nudge struct{}

func time30m() time.Duration { return 30 * time.Minute }
