package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

type freshReads struct {
	service.Service
	fresh []bool
	since []time.Time
}

func (s *freshReads) ListIssues(ctx context.Context, f domain.IssueFilter, p domain.PageRequest) (domain.Page[domain.Issue], error) {
	since, ok := domain.FreshSince(ctx)
	s.fresh = append(s.fresh, ok)
	s.since = append(s.since, since)
	return s.Service.ListIssues(ctx, f, p)
}
func (s *freshReads) Doctor(ctx context.Context) (domain.DoctorReport, error) {
	since, ok := domain.FreshSince(ctx)
	s.fresh = append(s.fresh, ok)
	s.since = append(s.since, since)
	return s.Service.Doctor(ctx)
}

func TestCache_FreshFlagAndDoctorContexts(t *testing.T) {
	for _, args := range [][]string{{"list", "--fresh", "--json"}, {"--fresh", "list", "--json"}, {"doctor", "--json"}} {
		var out, stderr bytes.Buffer
		f := servicetest.New(servicetest.DefaultFixture())
		s := &freshReads{Service: f}
		deps := fakeDeps(f, &out, &stderr, false)
		deps.Wire = func(context.Context, func(context.Context) (domain.RepoRef, error), string) (Wiring, error) {
			return Wiring{Service: s}, nil
		}
		if code := Run(context.Background(), args, deps); code != 0 || stderr.Len() != 0 || len(s.fresh) == 0 || !s.fresh[0] {
			t.Fatal(args, code, stderr.String(), s.fresh)
		}
		if !s.since[0].Equal(fixtureTime) {
			t.Fatal("fresh did not use the command clock")
		}
	}
}

func TestCache_OffAndConstructionAreSideEffectFree(t *testing.T) {
	for _, off := range []bool{false, true} {
		root := t.TempDir()
		getenv := func(name string) string {
			if name == "XDG_CACHE_HOME" {
				return root
			}
			if name == "TODOS_CACHE" && off {
				return "off"
			}
			return ""
		}
		store, check := cacheWiring(getenv, func() (string, error) { t.Fatal("unexpected home lookup"); return "", nil }, time.Now)
		if (store == nil) != off || !check().OK {
			t.Fatal("incorrect cache wiring")
		}
		path := filepath.Join(root, "todos", "v1")
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("cache construction/check created files")
		}
		if store != nil {
			store.Put("credential/structure", "labels", []byte("test"), time.Now())
			if _, err := os.Stat(path); err != nil {
				t.Fatal("enabled cache not persisted")
			}
		}
	}
}

func TestCache_OffDoesNotMarkTheContextFresh(t *testing.T) {
	var out, stderr bytes.Buffer
	f := servicetest.New(servicetest.DefaultFixture())
	s := &freshReads{Service: f}
	deps := fakeDeps(f, &out, &stderr, false)
	deps.Getenv = func(name string) string {
		if name == "TODOS_CACHE" {
			return "off"
		}
		return ""
	}
	deps.Wire = func(context.Context, func(context.Context) (domain.RepoRef, error), string) (Wiring, error) {
		return Wiring{Service: s}, nil
	}
	if code := Run(context.Background(), []string{"list", "--json"}, deps); code != 0 || len(s.fresh) != 1 || s.fresh[0] {
		t.Fatal("off disabled the ordinary in-process memo", code)
	}
}

func TestCache_DoctorReportsUnusableWithoutFailingOrCreating(t *testing.T) {
	var out, stderr bytes.Buffer
	f := servicetest.New(servicetest.DefaultFixture())
	deps := fakeDeps(f, &out, &stderr, false)
	root := filepath.Join(t.TempDir(), "missing")
	_, check := cacheWiring(func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return root
		}
		return ""
	}, os.UserHomeDir, time.Now)
	deps.Wire = func(context.Context, func(context.Context) (domain.RepoRef, error), string) (Wiring, error) {
		return Wiring{Service: f, CacheCheck: check}, nil
	}
	if code := Run(context.Background(), []string{"doctor", "--json"}, deps); code != 0 || stderr.Len() != 0 {
		t.Fatal(code, stderr.String())
	}
	var dto DoctorDTO
	if err := json.Unmarshal(out.Bytes(), &dto); err != nil || len(dto.Checks) != 7 || dto.Checks[6].Name != "Cache" {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("doctor created a directory")
	}
	// A bad cache remains advisory and never turns the first three checks red.
	deps.Wire = func(context.Context, func(context.Context) (domain.RepoRef, error), string) (Wiring, error) {
		return Wiring{Service: f, CacheCheck: func() domain.DoctorCheck { return domain.DoctorCheck{Name: "Cache", Detail: "unusable"} }}, nil
	}
	out.Reset()
	if code := Run(context.Background(), []string{"doctor", "--json"}, deps); code != 0 {
		t.Fatal("cache check changed exit code", code)
	}
	json.Unmarshal(out.Bytes(), &dto)
	if dto.Checks[6].OK || !dto.OK {
		t.Fatal("bad cache was hidden or made doctor fatal")
	}
}
