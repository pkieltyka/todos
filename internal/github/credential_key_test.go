package github

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"testing"

	"github.com/pkieltyka/todos/internal/domain"
)

func TestCredentialKey_NormalizedAndNoHTTP(t *testing.T) {
	c := testClient(t, func(request) (*http.Response, error) { t.Fatal("key lookup sent HTTP"); return nil, nil })
	c.opts.Host = func(context.Context) (string, error) { return "GitHub.COM", nil }
	c.opts.TokenForHost = func(string) (string, string) { return "private-token", "GH_TOKEN" }
	key, err := c.CredentialKey(context.Background())
	expected := fmt.Sprintf("%x", sha256.Sum256([]byte("todos-cache-v1\x00github.com\x00private-token")))
	if err != nil || len(key) != 64 || key != expected {
		t.Fatal("incorrect opaque credential key")
	}
}

func TestCredentialKey_TracksReresolvedTokenAndHost(t *testing.T) {
	token := "old"
	calls := 0
	c := testClient(t, func(request) (*http.Response, error) {
		calls++
		return response(401, `{"message":"bad credentials"}`, nil), nil
	})
	c.opts.TokenForHost = func(string) (string, string) { return token, "test" }
	first, err := c.CredentialKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Viewer(context.Background())
	kind(t, err, domain.ErrAuth)
	token = "new"
	second, err := c.CredentialKey(context.Background())
	if err != nil || first == second || calls != 1 {
		t.Fatal("credential key did not follow auth refresh without replay")
	}
	other := testClient(t, func(request) (*http.Response, error) { t.Fatal("HTTP"); return nil, nil })
	other.opts.Host = func(context.Context) (string, error) { return "enterprise.example", nil }
	other.opts.TokenForHost = func(string) (string, string) { return token, "test" }
	third, err := other.CredentialKey(context.Background())
	if err != nil || third == second {
		t.Fatal("host not in partition")
	}
}

func TestCredentialKey_CancelledAndMissingCredential(t *testing.T) {
	c := testClient(t, func(request) (*http.Response, error) { t.Fatal("HTTP"); return nil, nil })
	c.opts.TokenForHost = func(string) (string, string) { return "", "default" }
	if key, err := c.CredentialKey(context.Background()); key != "" {
		t.Fatal("missing key returned")
	} else {
		kind(t, err, domain.ErrAuth)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if key, err := c.CredentialKey(ctx); key != "" || err == nil {
		t.Fatal("cancelled key lookup succeeded")
	}
}
