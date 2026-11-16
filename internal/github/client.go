package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/auth"
	ghconfig "github.com/cli/go-gh/v2/pkg/config"
	"github.com/pkieltyka/todos/internal/domain"
)

// Options supplies host/credential discovery and the HTTP boundary. Tests set
// APIHost, Transport and TokenForHost explicitly, avoiding all ambient auth.
type Options struct {
	Version      string
	Host         func(context.Context) (string, error)
	APIHost      string
	Transport    http.RoundTripper
	TokenForHost func(string) (token, source string)
	Now          func() time.Time
}

type Client struct {
	opts                              Options
	initMu                            sync.Mutex
	initial                           *initialization
	rest                              *api.RESTClient
	graphql                           *api.GraphQLClient
	authMu                            sync.RWMutex
	host, apiHost, token, tokenSource string
	secrets                           []string
	tokenVersion                      uint64
	needsRefresh                      bool
	refreshing                        *credentialRefresh
	tracker                           rateTracker
}

func New(opts Options) *Client {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Host == nil {
		opts.Host = func(context.Context) (string, error) { h, _ := auth.DefaultHost(); return h, nil }
	}
	if opts.TokenForHost == nil {
		opts.TokenForHost = auth.TokenForHost
	}
	if opts.Transport == nil {
		opts.Transport = http.DefaultTransport
	}
	return &Client{opts: opts}
}

type initialization struct {
	done chan struct{}
	err  error
}
type credentialRefresh struct {
	done chan struct{}
	err  error
}

func isAuth(err error) bool {
	var de *domain.Error
	return errors.As(err, &de) && de.Kind == domain.ErrAuth
}
func (c *Client) initialize(ctx context.Context) error {
	if ctx.Err() != nil {
		return classify(ctx.Err(), operation{}, responseMeta{}, c.opts.Now())
	}
	c.initMu.Lock()
	retry := false
	if c.initial != nil {
		select {
		case <-c.initial.done:
			retry = isAuth(c.initial.err)
		default:
		}
	}
	if c.initial == nil || retry {
		task := &initialization{done: make(chan struct{})}
		c.initial = task
		go func() { task.err = c.build(); close(task.done) }()
	}
	task := c.initial
	c.initMu.Unlock()
	select {
	case <-ctx.Done():
		return classify(ctx.Err(), operation{}, responseMeta{}, c.opts.Now())
	case <-task.done:
		return task.err
	}
}
func (c *Client) requestReady(ctx context.Context) error {
	if err := c.initialize(ctx); err != nil {
		return c.safeError(err)
	}
	return c.refreshToken(ctx, false)
}

// Refresh is shared by concurrent callers, but never includes an API request.
// A waiter can cancel while the read-only credential lookup finishes separately.
func (c *Client) refreshToken(ctx context.Context, force bool) error {
	if ctx.Err() != nil {
		return classify(ctx.Err(), operation{}, responseMeta{}, c.opts.Now())
	}
	c.authMu.Lock()
	if !force && !c.needsRefresh {
		c.authMu.Unlock()
		return nil
	}
	if c.refreshing == nil {
		task := &credentialRefresh{done: make(chan struct{})}
		c.refreshing = task
		host := c.host
		go func() {
			token, source := c.opts.TokenForHost(host)
			c.authMu.Lock()
			if token == "" {
				task.err = &domain.Error{Kind: domain.ErrAuth, Message: "no authentication token found for " + host, Hint: "gh auth login"}
				c.needsRefresh = true
			} else {
				c.token = token
				c.tokenSource = source
				c.secrets = append(c.secrets, token)
				c.tokenVersion++
				c.needsRefresh = false
			}
			c.refreshing = nil
			close(task.done)
			c.authMu.Unlock()
		}()
	}
	task := c.refreshing
	c.authMu.Unlock()
	select {
	case <-ctx.Done():
		return classify(ctx.Err(), operation{}, responseMeta{}, c.opts.Now())
	case <-task.done:
		return c.safeError(task.err)
	}
}
func (c *Client) authFailed(version uint64) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if version == c.tokenVersion {
		c.needsRefresh = true
	}
}

func (c *Client) build() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host, err := c.opts.Host(ctx)
	if err != nil {
		var de *domain.Error
		if errors.As(err, &de) {
			return de
		}
		return &domain.Error{Kind: domain.ErrConfig, Message: "could not resolve GitHub host", Err: err}
	}
	host = auth.NormalizeHostname(strings.ToLower(strings.TrimSpace(host)))
	if host == "" || strings.ContainsAny(host, "/@:#?\\%") {
		return &domain.Error{Kind: domain.ErrConfig, Message: "invalid GitHub host"}
	}
	token, source := c.opts.TokenForHost(host)
	if token == "" {
		return &domain.Error{Kind: domain.ErrAuth, Message: "no authentication token found for " + host, Hint: "gh auth login"}
	}
	apiHost := c.opts.APIHost
	if apiHost == "" {
		if cfg, err := ghconfig.Read(nil); err == nil && cfg != nil {
			apiHost, _ = cfg.Get([]string{"hosts", host, "api_host"})
		}
	}
	c.authMu.Lock()
	c.host = host
	c.apiHost = apiHost
	c.token = token
	c.tokenSource = source
	c.tokenVersion++
	c.secrets = append(c.secrets, token)
	c.authMu.Unlock()
	opts := api.ClientOptions{Host: host, APIHost: apiHost, AuthToken: token, Timeout: 30 * time.Second, EnableCache: false, LogIgnoreEnv: true,
		Transport: &observingTransport{base: c.opts.Transport, client: c}, Headers: map[string]string{"User-Agent": "todos/" + c.opts.Version, "Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"}}
	rest, err := api.NewRESTClient(opts)
	if err != nil {
		return &domain.Error{Kind: domain.ErrConfig, Message: "could not configure GitHub API client", Err: err}
	}
	graphql, err := api.NewGraphQLClient(opts)
	if err != nil {
		return &domain.Error{Kind: domain.ErrConfig, Message: "could not configure GitHub API client", Err: err}
	}
	c.rest = rest
	c.graphql = graphql
	return nil
}

type metaKey struct{}
type observingTransport struct {
	base   http.RoundTripper
	client *Client
}

func (t *observingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header = req.Header.Clone()
	t.client.authMu.RLock()
	host := strings.ToLower(r.URL.Hostname())
	allowed := host == t.client.host || strings.HasSuffix(host, "."+t.client.host) || t.client.apiHost != "" && (strings.EqualFold(host, t.client.apiHost) || strings.EqualFold(r.URL.Host, t.client.apiHost))
	if allowed {
		r.Header.Set("Authorization", "token "+t.client.token)
	} else {
		r.Header.Del("Authorization")
	}
	if meta, ok := req.Context().Value(metaKey{}).(*responseMeta); ok {
		meta.CredentialVersion = t.client.tokenVersion
	}
	t.client.authMu.RUnlock()
	response, err := t.base.RoundTrip(r)
	if response != nil {
		t.client.tracker.observe(response.Header, t.client.opts.Now())
		if meta, ok := req.Context().Value(metaKey{}).(*responseMeta); ok {
			meta.Headers = response.Header.Clone()
		}
	}
	return response, err
}

func (c *Client) safeError(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		de = classify(err, operation{}, responseMeta{}, c.opts.Now())
	}
	out := *de
	c.authMu.RLock()
	defer c.authMu.RUnlock()
	if out.Kind == domain.ErrAuth && strings.HasSuffix(c.tokenSource, "_TOKEN") {
		out.Hint = c.tokenSource + " overrides stored gh credentials; update or unset it, then gh auth login"
	}
	for _, secret := range c.secrets {
		if secret != "" {
			out.Message = strings.ReplaceAll(out.Message, secret, "[REDACTED]")
			out.Hint = strings.ReplaceAll(out.Hint, secret, "[REDACTED]")
		}
	}
	return &out
}

func (c *Client) query(ctx context.Context, name string, variables map[string]any, out any) error {
	return c.execute(ctx, name, variables, out, false)
}
func (c *Client) mutate(ctx context.Context, name string, variables map[string]any, out any) error {
	return c.execute(ctx, name, variables, out, true)
}
func (c *Client) execute(ctx context.Context, name string, variables map[string]any, out any, mutation bool) error {
	return c.executeWithMeta(ctx, name, variables, out, mutation, &responseMeta{})
}

func (c *Client) executeWithMeta(ctx context.Context, name string, variables map[string]any, out any, mutation bool, meta *responseMeta) error {
	doc, err := document(name)
	if err != nil {
		return &domain.Error{Kind: domain.ErrInternal, Message: "unknown GitHub operation", Err: err}
	}
	if mutation != strings.HasPrefix(doc, "mutation ") {
		return &domain.Error{Kind: domain.ErrInternal, Message: "incorrect GitHub operation mode"}
	}
	if err = c.requestReady(ctx); err != nil {
		return c.safeError(err)
	}
	if ctx.Err() != nil {
		return c.safeError(classify(ctx.Err(), operation{Name: name}, responseMeta{}, c.opts.Now()))
	}
	ctx = context.WithValue(ctx, metaKey{}, meta)
	op := operation{Name: name, Mutation: mutation, Started: true}
	var raw json.RawMessage
	err = c.graphql.DoWithContext(ctx, doc, variables, &raw)
	if len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if decodeErr := json.Unmarshal(raw, out); decodeErr != nil {
			if err == nil {
				err = decodeErr
			}
		}
	}
	if err == nil {
		var object map[string]json.RawMessage
		if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &object) != nil || len(object) == 0 {
			err = malformed("missing GraphQL data")
		}
	}
	if err == nil {
		return nil
	}
	de := classify(err, op, *meta, c.opts.Now())
	if mutation && de.Kind == domain.ErrGitHub {
		copy := *de
		copy.Uncertain = true
		de = &copy
	}
	if de.Kind == domain.ErrAuth {
		c.authFailed(meta.CredentialVersion)
	}
	if de.Kind == domain.ErrRateLimited {
		c.tracker.block(c.opts.Now().Add(de.RetryAfter))
	}
	return c.safeError(de)
}

func (c *Client) Credentials(ctx context.Context) (domain.CredentialInfo, error) {
	if err := c.requestReady(ctx); err != nil {
		return domain.CredentialInfo{}, c.safeError(err)
	}
	c.authMu.RLock()
	defer c.authMu.RUnlock()
	return domain.CredentialInfo{Source: c.tokenSource, Scopes: []string{}}, nil
}

func (c *Client) DoctorScopes(ctx context.Context) (domain.CredentialInfo, error) {
	info, err := c.Credentials(ctx)
	if err != nil {
		return info, err
	}
	meta := responseMeta{}
	ctx = context.WithValue(ctx, metaKey{}, &meta)
	response, err := c.rest.RequestWithContext(ctx, http.MethodGet, "user", nil)
	if err != nil {
		de := classify(err, operation{Name: "DoctorScopes", Started: true}, meta, c.opts.Now())
		if de.Kind == domain.ErrAuth {
			c.authFailed(meta.CredentialVersion)
		}
		if de.Kind == domain.ErrRateLimited {
			c.tracker.block(c.opts.Now().Add(de.RetryAfter))
		}
		return info, c.safeError(de)
	}
	defer response.Body.Close()
	var user struct{ Login string }
	decoder := json.NewDecoder(response.Body)
	if err = decoder.Decode(&user); err != nil {
		if errors.Is(err, io.EOF) {
			return info, malformed("missing authenticated-user response")
		}
		return info, c.safeError(classify(err, operation{Name: "DoctorScopes"}, meta, c.opts.Now()))
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return info, c.safeError(classify(err, operation{Name: "DoctorScopes"}, meta, c.opts.Now()))
		}
		return info, malformed("trailing authenticated-user data")
	}
	if user.Login == "" {
		return info, malformed("missing authenticated user")
	}
	for _, scope := range strings.Split(response.Header.Get("X-OAuth-Scopes"), ",") {
		if scope = strings.TrimSpace(scope); scope != "" {
			info.Scopes = append(info.Scopes, scope)
		}
	}
	return info, nil
}

func (c *Client) RefreshCredentials(ctx context.Context) error {
	c.initMu.Lock()
	task := c.initial
	if task != nil {
		select {
		case <-task.done:
			if task.err != nil {
				c.initial = nil
				task = nil
			}
		default:
		}
	}
	c.initMu.Unlock()
	if task == nil {
		return c.safeError(c.initialize(ctx))
	}
	if err := c.initialize(ctx); err != nil {
		return c.safeError(err)
	}
	return c.refreshToken(ctx, true)
}

func (c *Client) RateLimit() domain.RateLimit { return c.tracker.snapshot(c.opts.Now()) }

// Keep the standard-library timeout/interface assumptions explicit.
var _ http.RoundTripper = (*observingTransport)(nil)
