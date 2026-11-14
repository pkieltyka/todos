package servicetest

import (
	"context"
	"errors"
	"sync"
)

type gateMode int

const (
	gateRelease gateMode = iota
	gateFail
	gateApplyThenFail
)

// Gate holds one matching call until its first release decision.
type Gate struct {
	Started  <-chan struct{}
	started  chan struct{}
	released chan struct{}
	mu       sync.Mutex
	mode     gateMode
	err      error
	once     sync.Once
}

func (f *Fake) Hold(method string, number int) *Gate {
	started := make(chan struct{})
	g := &Gate{Started: started, started: started, released: make(chan struct{})}
	f.mu.Lock()
	defer f.mu.Unlock()
	k := gateKey{method, number}
	f.gates[k] = append(f.gates[k], g)
	return g
}

func (g *Gate) Release()                { g.decide(gateRelease, nil) }
func (g *Gate) Fail(err error)          { g.decide(gateFail, err) }
func (g *Gate) ApplyThenFail(err error) { g.decide(gateApplyThenFail, err) }
func (g *Gate) decide(mode gateMode, err error) {
	g.once.Do(func() {
		g.mu.Lock()
		g.mode = mode
		if err != nil {
			g.err = normalise(err)
		}
		g.mu.Unlock()
		close(g.released)
	})
}

func (g *Gate) wait(ctx context.Context) (bool, error) {
	close(g.started)
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-g.released:
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.mode == gateFail {
			if g.err == nil {
				return false, errors.New("fake call failed")
			}
			return false, g.err
		}
		return true, g.err
	}
}
