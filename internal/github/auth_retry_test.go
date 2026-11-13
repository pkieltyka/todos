package github

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pkieltyka/todos/internal/domain"
)

func TestAuthFailure_NextRequestRefreshesWithoutReplay(t *testing.T) {
	for _, graphQL := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP401", true: "GraphQLAuth"}[graphQL], func(t *testing.T) {
			var token atomic.Value
			token.Store("old")
			var lookups, calls atomic.Int32
			c := testClient(t, func(r request) (*http.Response, error) {
				calls.Add(1)
				if r.Header.Get("Authorization") == "token old" {
					if graphQL {
						return response(200, `{"errors":[{"type":"UNAUTHENTICATED","message":"bad credentials"}]}`, nil), nil
					}
					return response(401, `{"message":"bad credentials"}`, nil), nil
				}
				return response(200, `{"data":{"viewer":{"id":"U","login":"new-user"}}}`, nil), nil
			})
			c.opts.TokenForHost = func(string) (string, string) { lookups.Add(1); return token.Load().(string), "test" }
			_, err := c.Viewer(context.Background())
			kind(t, err, domain.ErrAuth)
			if calls.Load() != 1 || lookups.Load() != 1 {
				t.Fatal("failed operation was replayed", calls.Load(), lookups.Load())
			}
			token.Store("new")
			viewer, err := c.Viewer(context.Background())
			if err != nil || viewer.Login != "new-user" || calls.Load() != 2 || lookups.Load() != 2 {
				t.Fatal(viewer, err, calls.Load(), lookups.Load())
			}
			_, err = c.Viewer(context.Background())
			if err != nil || lookups.Load() != 2 {
				t.Fatal("successful credential was refreshed again", err, lookups.Load())
			}
		})
	}
}
func TestAuthFailure_DoctorScopesAlsoInvalidates(t *testing.T) {
	var token atomic.Value
	token.Store("old")
	var lookups, calls atomic.Int32
	c := testClient(t, func(request) (*http.Response, error) { panic("replaced") })
	c.opts.TokenForHost = func(string) (string, string) { lookups.Add(1); return token.Load().(string), "test" }
	c.opts.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.Header.Get("Authorization") == "token old" {
			return response(401, `{"message":"bad credentials"}`, nil), nil
		}
		return response(200, `{"login":"new-user"}`, nil), nil
	})
	_, err := c.DoctorScopes(context.Background())
	kind(t, err, domain.ErrAuth)
	token.Store("new")
	_, err = c.DoctorScopes(context.Background())
	if err != nil || calls.Load() != 2 || lookups.Load() != 2 {
		t.Fatal(err, calls.Load(), lookups.Load())
	}
}
func TestAuthFailure_InitialMissingTokenCanRecover(t *testing.T) {
	var calls, lookups atomic.Int32
	var token atomic.Value
	token.Store("")
	c := testClient(t, func(request) (*http.Response, error) {
		calls.Add(1)
		return response(200, `{"data":{"viewer":{"id":"U","login":"test"}}}`, nil), nil
	})
	c.opts.TokenForHost = func(string) (string, string) { lookups.Add(1); return token.Load().(string), "test" }
	_, err := c.Viewer(context.Background())
	kind(t, err, domain.ErrAuth)
	if calls.Load() != 0 || lookups.Load() != 1 {
		t.Fatal(calls.Load(), lookups.Load())
	}
	token.Store("new")
	_, err = c.Viewer(context.Background())
	if err != nil || lookups.Load() != 2 || calls.Load() != 1 {
		t.Fatal(err, lookups.Load(), calls.Load())
	}
}
func TestAuthFailure_LateOldRejectionCannotInvalidateNewToken(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls, lookups atomic.Int32
	var token atomic.Value
	token.Store("old")
	c := testClient(t, func(r request) (*http.Response, error) {
		n := calls.Add(1)
		if n == 1 {
			close(started)
			<-release
		}
		if r.Header.Get("Authorization") == "token old" {
			return response(401, `{"message":"bad credentials"}`, nil), nil
		}
		return response(200, `{"data":{"viewer":{"id":"U","login":"test"}}}`, nil), nil
	})
	c.opts.TokenForHost = func(string) (string, string) { lookups.Add(1); return token.Load().(string), "test" }
	oldDone := make(chan error, 1)
	go func() { _, err := c.Viewer(context.Background()); oldDone <- err }()
	<-started
	_, err := c.Viewer(context.Background())
	kind(t, err, domain.ErrAuth)
	token.Store("new")
	_, err = c.Viewer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	kind(t, <-oldDone, domain.ErrAuth)
	_, err = c.Viewer(context.Background())
	if err != nil || lookups.Load() != 2 || calls.Load() != 4 {
		t.Fatal(err, lookups.Load(), calls.Load())
	}
}
func TestAuthFailure_ConcurrentRefreshSharedAndCancelable(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var lookups atomic.Int32
	c := testClient(t, func(request) (*http.Response, error) { return response(401, `{"message":"bad credentials"}`, nil), nil })
	c.opts.TokenForHost = func(string) (string, string) {
		if lookups.Add(1) == 1 {
			return "old", "test"
		}
		close(started)
		<-release
		return "new", "test"
	}
	_, err := c.Viewer(context.Background())
	kind(t, err, domain.ErrAuth)
	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() { _, err := c.Credentials(ctx); canceled <- err }()
	<-started
	var wg sync.WaitGroup
	survived := make(chan error, 1)
	wg.Add(1)
	go func() { defer wg.Done(); _, err := c.Credentials(context.Background()); survived <- err }()
	cancel()
	if err := <-canceled; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if err := <-survived; err != nil || lookups.Load() != 2 {
		t.Fatal(err, lookups.Load())
	}
}
func TestAuthFailure_NonAuthErrorsDoNotRefresh(t *testing.T) {
	for _, code := range []int{403, 404, 429, 503} {
		var lookups atomic.Int32
		c := testClient(t, func(request) (*http.Response, error) { return response(code, `{"message":"rejected"}`, nil), nil })
		c.opts.TokenForHost = func(string) (string, string) { lookups.Add(1); return "token", "test" }
		_, _ = c.Viewer(context.Background())
		_, _ = c.Viewer(context.Background())
		if lookups.Load() != 1 {
			t.Fatal(code, lookups.Load())
		}
	}
}
func TestAuthFailure_RefreshMissingTokenStopsBeforeHTTP(t *testing.T) {
	var lookups, calls atomic.Int32
	c := testClient(t, func(request) (*http.Response, error) {
		calls.Add(1)
		return response(401, `{"message":"bad credentials"}`, nil), nil
	})
	c.opts.TokenForHost = func(string) (string, string) {
		if lookups.Add(1) == 1 {
			return "old", "test"
		}
		return "", "default"
	}
	_, err := c.Viewer(context.Background())
	kind(t, err, domain.ErrAuth)
	_, err = c.Viewer(context.Background())
	kind(t, err, domain.ErrAuth)
	if calls.Load() != 1 || lookups.Load() != 2 {
		t.Fatal("request sent after missing credential", calls.Load(), lookups.Load())
	}
}
func TestAuthFailure_MutationIsNotReplayed(t *testing.T) {
	var headers []string
	token := "old"
	c := testClient(t, func(r request) (*http.Response, error) {
		headers = append(headers, r.Header.Get("Authorization"))
		if len(headers) == 1 {
			return response(401, `{"message":"bad credentials"}`, nil), nil
		}
		return response(200, `{"data":{"deleteIssue":{"repository":{"id":"R"}}}}`, nil), nil
	})
	c.opts.TokenForHost = func(string) (string, string) { return token, "test" }
	var out any
	err := c.mutate(context.Background(), "DeleteIssue", nil, &out)
	de := kind(t, err, domain.ErrAuth)
	if de.Uncertain || len(headers) != 1 {
		t.Fatal(de, headers)
	}
	token = "new"
	if err = c.mutate(context.Background(), "DeleteIssue", nil, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(headers, []string{"token old", "token new"}) {
		t.Fatal(headers)
	}
}
