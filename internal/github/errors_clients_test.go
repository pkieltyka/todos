package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/pkieltyka/todos/internal/domain"
)

func httpErr(code int, message string, h http.Header) error {
	return &api.HTTPError{StatusCode: code, Message: message, Headers: h}
}
func TestErrorPriority_Table(t *testing.T) {
	for _, test := range []struct {
		err  error
		want domain.ErrorKind
	}{
		{httpErr(401, "missing scope and rate limit", nil), domain.ErrAuth},
		{httpErr(403, "requires project scope; rate limit", nil), domain.ErrMissingScope},
		{httpErr(403, "policy", http.Header{"X-Ratelimit-Remaining": {"0"}}), domain.ErrRateLimited},
		{httpErr(422, "abuse detection", nil), domain.ErrRateLimited},
		{httpErr(403, "policy", nil), domain.ErrForbidden},
		{httpErr(404, "secret", nil), domain.ErrNotFound},
		{httpErr(410, "issues are disabled", nil), domain.ErrIssuesDisabled},
		{httpErr(422, "bad title", nil), domain.ErrInvalidInput},
		{httpErr(500, "server", nil), domain.ErrGitHub},
		{context.Canceled, domain.ErrNetwork}, {context.DeadlineExceeded, domain.ErrTimeout}, {io.EOF, domain.ErrNetwork},
	} {
		t.Run(fmt.Sprint(test.want), func(t *testing.T) { kind(t, classify(test.err, operation{}, responseMeta{}, epoch), test.want) })
	}
	existing := &domain.Error{Kind: domain.ErrConfig, Message: "local"}
	if classify(existing, operation{}, responseMeta{}, epoch) != existing {
		t.Fatal("existing error replaced")
	}
}
func TestGraphQL_HTTP200Errors(t *testing.T) {
	b, err := os.ReadFile("testdata/errors/missing_scope.json")
	if err != nil {
		t.Fatal(err)
	}
	c := testClient(t, func(request) (*http.Response, error) { return response(200, string(b), nil), nil })
	_, err = c.Repository(context.Background(), ref)
	de := kind(t, err, domain.ErrMissingScope)
	if de.Hint != "gh auth refresh -s read:project" {
		t.Fatal(de)
	}
}
func TestScopeLessRefusal_Forbidden(t *testing.T) {
	kind(t, classify(httpErr(403, "Resource not accessible by personal access token", nil), operation{}, responseMeta{}, epoch), domain.ErrForbidden)
}
func TestNotFound_PrivateMessage(t *testing.T) {
	de := classify(httpErr(404, "private secret repo", nil), operation{}, responseMeta{}, epoch)
	if de.Message != "GitHub resource not found or no access" {
		t.Fatal(de)
	}
}
func TestMalformedAnswer_GitHub(t *testing.T) {
	for _, body := range []string{"{", "{}", "null", "{\"data\":null}", "{\"data\":[]}", "{\"data\":{\"viewer\":{}}}"} {
		c := testClient(t, func(request) (*http.Response, error) { return response(200, body, nil), nil })
		_, err := c.Viewer(context.Background())
		kind(t, err, domain.ErrGitHub)
	}
}
func TestMutation_StartedTimeoutNetwork5xxMalformedUncertain(t *testing.T) {
	for _, test := range []struct {
		code int
		body string
		err  error
		want domain.ErrorKind
	}{{0, "", context.DeadlineExceeded, domain.ErrTimeout}, {0, "", io.EOF, domain.ErrNetwork}, {503, `{"message":"server"}`, nil, domain.ErrGitHub}, {200, "{", nil, domain.ErrGitHub}, {200, `{"data":null}`, nil, domain.ErrGitHub}} {
		c := testClient(t, func(request) (*http.Response, error) {
			if test.err != nil {
				return nil, test.err
			}
			return response(test.code, test.body, nil), nil
		})
		var out any
		de := kind(t, c.mutate(context.Background(), "DeleteIssue", map[string]any{"input": map[string]any{"issueId": "I"}}, &out), test.want)
		if !de.Uncertain {
			t.Fatal("mutation certainty lost", de)
		}
	}
}
func TestMutation_PreStartCancellationCertain(t *testing.T) {
	c := testClient(t, func(request) (*http.Response, error) { t.Fatal("request after cancellation"); return nil, nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out any
	de := kind(t, c.mutate(ctx, "DeleteIssue", nil, &out), domain.ErrNetwork)
	if de.Uncertain || !errors.Is(de, context.Canceled) {
		t.Fatal(de)
	}
}
func TestMutation_ExplicitRejectionCertain(t *testing.T) {
	for _, code := range []int{401, 403, 404, 422, 429} {
		c := testClient(t, func(request) (*http.Response, error) { return response(code, `{"message":"rejected"}`, nil), nil })
		var out any
		var de *domain.Error
		if err := c.mutate(context.Background(), "DeleteIssue", nil, &out); !errors.As(err, &de) || de.Uncertain {
			t.Fatal(code, err)
		}
	}
}
func TestNoAutomaticRetry(t *testing.T) {
	calls := 0
	c := testClient(t, func(request) (*http.Response, error) { calls++; return response(503, `{"message":"server"}`, nil), nil })
	_, _ = c.Viewer(context.Background())
	if calls != 1 {
		t.Fatal(calls)
	}
}
func TestRetryAfter_SecondsDateResetFallback(t *testing.T) {
	for _, test := range []struct {
		h    http.Header
		want time.Duration
	}{{http.Header{"Retry-After": {"12"}}, 12 * time.Second}, {http.Header{"Retry-After": {epoch.Add(25 * time.Second).Format(http.TimeFormat)}}, 25 * time.Second}, {http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {fmt.Sprint(epoch.Add(45 * time.Second).Unix())}, "Retry-After": {"20"}}, 45 * time.Second}, {nil, time.Minute}, {http.Header{"Retry-After": {"0"}}, time.Second}} {
		if got := retryAfter(test.h, epoch); got != test.want {
			t.Fatal(got, test.want)
		}
	}
}
func quota(resource string, remaining int, reset time.Time) http.Header {
	return http.Header{"X-Ratelimit-Resource": {resource}, "X-Ratelimit-Limit": {"5000"}, "X-Ratelimit-Remaining": {fmt.Sprint(remaining)}, "X-Ratelimit-Reset": {fmt.Sprint(reset.Unix())}}
}
func TestQuota_ResourceSeparationAndOutOfOrder(t *testing.T) {
	var r rateTracker
	reset := epoch.Add(time.Hour)
	r.observe(quota("core", 100, reset), epoch)
	r.observe(quota("graphql", 5, reset), epoch)
	r.observe(quota("graphql", 10, reset), epoch)
	r.observe(quota("core", 1, reset.Add(-time.Minute)), epoch)
	r.observe(http.Header{"X-Ratelimit-Resource": {"core"}}, epoch)
	v := r.snapshot(epoch)
	if !v.Known || v.Resource != "graphql" || v.Remaining != 5 || len(v.Buckets) != 2 {
		t.Fatal(v)
	}
	v.Buckets[0].Remaining = 0
	if r.snapshot(epoch).Buckets[0].Remaining != 100 {
		t.Fatal("alias")
	}
}
func TestQuota_ResetExpires(t *testing.T) {
	var r rateTracker
	r.observe(quota("core", 0, epoch), epoch)
	if r.snapshot(epoch).Known {
		t.Fatal("expired")
	}
	r.block(epoch.Add(time.Minute))
	if r.snapshot(epoch).BlockedUntil.IsZero() {
		t.Fatal("missing cooldown")
	}
	if !r.snapshot(epoch.Add(time.Minute)).BlockedUntil.IsZero() {
		t.Fatal("expired cooldown")
	}
}
func TestQuota_ConcurrentSnapshot(t *testing.T) {
	var r rateTracker
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.observe(quota("core", i, epoch.Add(time.Hour)), epoch)
			_ = r.snapshot(epoch)
		}(i)
	}
	wg.Wait()
	if r.snapshot(epoch).Remaining != 0 {
		t.Fatal("not minimum")
	}
}
func TestClients_ReusableAndContextBound(t *testing.T) {
	var hostCalls, tokenCalls atomic.Int32
	blocked := make(chan struct{})
	started := make(chan struct{})
	c := New(Options{APIHost: "api.github.com", Host: func(context.Context) (string, error) { hostCalls.Add(1); return "github.com", nil }, TokenForHost: func(string) (string, string) { tokenCalls.Add(1); close(started); <-blocked; return "secret", "test" }, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(200, `{"data":{"viewer":{"login":"test","id":"U"}}}`, nil), nil
	})})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Viewer(ctx); done <- err }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(blocked)
	for i := 0; i < 2; i++ {
		if _, err := c.Viewer(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if hostCalls.Load() != 1 || tokenCalls.Load() != 1 {
		t.Fatal(hostCalls.Load(), tokenCalls.Load())
	}
}
func TestTimeout_30Seconds(t *testing.T) {
	c := testClient(t, func(request) (*http.Response, error) {
		return response(200, `{"data":{"viewer":{"id":"U","login":"test"}}}`, nil), nil
	})
	c.opts.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 30*time.Second || time.Until(deadline) < 29*time.Second {
			t.Error("request timeout is not 30s")
		}
		return response(200, `{"data":{"viewer":{"id":"U","login":"test"}}}`, nil), nil
	})
	if _, err := c.Viewer(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestDefaultRedirectPolicy_NoOverride(t *testing.T) {
	var destinations []string
	c := testClient(t, func(request) (*http.Response, error) { panic("replaced") })
	c.opts.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		destinations = append(destinations, r.URL.Host)
		if len(destinations) == 1 {
			return response(302, "", http.Header{"Location": {"https://unrelated.example/viewer"}}), nil
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("token forwarded to unrelated host")
		}
		return response(200, `{"data":{"viewer":{"id":"U","login":"test"}}}`, nil), nil
	})
	if _, err := c.Viewer(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(destinations, []string{"api.github.com", "unrelated.example"}) {
		t.Fatal(destinations)
	}
}
func TestLogging_EnvCannotLeak(t *testing.T) {
	t.Setenv("GH_DEBUG", "api")
	c := testClient(t, func(r request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "token test-secret" || r.Header.Get("User-Agent") != "todos/test" {
			t.Fatal("headers missing")
		}
		return response(403, `{"message":"test-secret requires project scope"}`, nil), nil
	})
	_, err := c.Viewer(context.Background())
	if strings.Contains(err.Error(), "test-secret") {
		t.Fatal("token leaked")
	}
}
func TestCache_NoDiskWrites(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GH_CACHE_DIR", dir)
	c := dataClient(t, func(request) any { return map[string]any{"viewer": map[string]any{"id": "U", "login": "test"}} })
	if _, err := c.Viewer(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) > 0 {
		t.Fatal(entries, err)
	}
}
func TestCredentials_LocalScopesAndRefresh(t *testing.T) {
	var token atomic.Value
	token.Store("old")
	var authorizations []string
	c := New(Options{APIHost: "api.github.com", Host: func(context.Context) (string, error) { return "github.com", nil }, TokenForHost: func(string) (string, string) { return token.Load().(string), "GH_TOKEN" }, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		if r.Method == http.MethodGet {
			return response(200, `{"login":"test"}`, http.Header{"X-Oauth-Scopes": {"repo, project"}}), nil
		}
		return response(200, `{"data":{"viewer":{"id":"U","login":"test"}}}`, nil), nil
	})})
	ctx := context.Background()
	info, err := c.Credentials(ctx)
	if err != nil || info.Source != "GH_TOKEN" || len(authorizations) != 0 || len(info.Scopes) != 0 {
		t.Fatal(info, err)
	}
	scopes, err := c.DoctorScopes(ctx)
	if err != nil || len(scopes.Scopes) != 2 {
		t.Fatal(scopes, err)
	}
	token.Store("new")
	if err = c.RefreshCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Viewer(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(authorizations, []string{"token old", "token new"}) {
		t.Fatal(authorizations)
	}
}
func TestGraphQLError_ConcurrentResponseHeaders(t *testing.T) {
	c := testClient(t, func(r request) (*http.Response, error) {
		h := http.Header{"Retry-After": {"7"}}
		return response(200, `{"errors":[{"type":"RATE_LIMITED","message":"rate limit"}]}`, h), nil
	})
	_, err := c.Viewer(context.Background())
	de := kind(t, err, domain.ErrRateLimited)
	if de.RetryAfter != 7*time.Second || c.RateLimit().BlockedUntil != epoch.Add(7*time.Second) {
		t.Fatal(de, c.RateLimit())
	}
}

func TestMutation_LocalEncodingFailureInternalCertain(t *testing.T) {
	c := testClient(t, func(request) (*http.Response, error) { t.Fatal("encoded unsupported input"); return nil, nil })
	var out any
	de := kind(t, c.mutate(context.Background(), "DeleteIssue", map[string]any{"bad": make(chan int)}, &out), domain.ErrInternal)
	if de.Uncertain {
		t.Fatal("local encoding failure is certain")
	}
}
func TestCredentials_MissingThenRefresh(t *testing.T) {
	c := testClient(t, func(request) (*http.Response, error) {
		return response(200, `{"data":{"viewer":{"id":"U","login":"test"}}}`, nil), nil
	})
	c.opts.TokenForHost = func(string) (string, string) { return "", "default" }
	_, err := c.Credentials(context.Background())
	kind(t, err, domain.ErrAuth)
	c.opts.TokenForHost = func(string) (string, string) { return "valid", "test" }
	if err = c.RefreshCredentials(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Viewer(context.Background()); err != nil {
		t.Fatal(err)
	}
}
