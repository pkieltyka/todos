package servicetest

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service"
)

var _ service.Service = (*Fake)(nil)

type gateKey struct {
	Method string
	Number int
}

// Fake implements Service directly. All fixture effects are in memory.
type Fake struct {
	mu          sync.Mutex
	fixture     Fixture
	calls       []Call
	gates       map[gateKey][]*Gate
	sequence    uint64
	now         time.Time
	nextNumber  int
	nextComment int
}

func New(f Fixture) *Fake {
	nextComment := 0
	for _, comments := range f.IssueComments {
		nextComment += len(comments)
	}
	nextNumber := 0
	for _, d := range f.Issues {
		nextNumber = max(nextNumber, d.Issue.Number)
	}
	for _, d := range f.PRs {
		nextNumber = max(nextNumber, d.PullRequest.Number)
	}
	return &Fake{fixture: cloneFixture(f), gates: make(map[gateKey][]*Gate), calls: []Call{}, now: fixtureTime(), nextComment: nextComment, nextNumber: nextNumber}
}

// Mutate simulates an external edit. The callback must not call Fake methods.
func (f *Fake) Mutate(fn func(*Fixture)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(&f.fixture)
	for _, d := range f.fixture.Issues {
		f.nextNumber = max(f.nextNumber, d.Issue.Number)
	}
	for _, d := range f.fixture.PRs {
		f.nextNumber = max(f.nextNumber, d.PullRequest.Number)
	}
	f.fixture = cloneFixture(f.fixture)
}

func (f *Fake) SetRateLimit(v domain.RateLimit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fixture.RateLimit = cloneRate(v)
}

func (f *Fake) RateLimit() domain.RateLimit {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneRate(f.fixture.RateLimit)
}

// Calls returns an owned call log, including calls still blocked at a gate.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]Call{}, f.calls...)
	for i := range out {
		out[i].Args = cloneArguments(out[i].Args)
		out[i].Err = domain.CloneError(out[i].Err)
	}
	return out
}

func invoke[T any](ctx context.Context, f *Fake, method string, args Arguments, effect func(*Fixture) (T, error)) (value T, err error) {
	idx, gate := f.beginCall(method, args)
	defer func() { f.finishCall(idx, err) }()
	apply, injected := true, error(nil)
	if gate != nil {
		apply, injected = gate.wait(ctx)
	}
	if ctx.Err() != nil {
		return value, normalise(ctx.Err())
	}
	if !apply {
		return value, normalise(injected)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if ctx.Err() != nil {
		return value, normalise(ctx.Err())
	}
	value, err = effect(&f.fixture)
	if err != nil {
		return value, normalise(err)
	}
	if injected != nil {
		return value, normalise(injected)
	}
	return value, nil
}

func (f *Fake) beginCall(method string, args Arguments) (int, *Gate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sequence++
	idx := len(f.calls)
	f.calls = append(f.calls, Call{Method: method, Args: cloneArguments(args), StartOrder: f.sequence})
	key := gateKey{method, args.Number}
	var gate *Gate
	if q := f.gates[key]; len(q) > 0 {
		gate = q[0]
		f.gates[key] = q[1:]
	}
	return idx, gate
}
func (f *Fake) finishCall(idx int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sequence++
	f.calls[idx].EndOrder = f.sequence
	f.calls[idx].Err = normalise(err)
}

func normalise(err error) *domain.Error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if errors.As(err, &de) {
		return domain.CloneError(de)
	}
	if errors.Is(err, context.Canceled) {
		return &domain.Error{Kind: domain.ErrNetwork, Message: "operation interrupted", Err: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &domain.Error{Kind: domain.ErrTimeout, Message: "operation timed out", Err: err}
	}
	return &domain.Error{Kind: domain.ErrInternal, Message: err.Error(), Err: err}
}

func errNotBuilt() error {
	return &domain.Error{Kind: domain.ErrInternal, Message: "quick add is not built yet"}
}

func (f *Fake) tick() time.Time { f.now = f.now.Add(time.Second); return f.now }
