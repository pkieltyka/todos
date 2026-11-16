package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// CredentialKey exposes only an opaque cache partition, never the credential.
func (c *Client) CredentialKey(ctx context.Context) (string, error) {
	if err := c.requestReady(ctx); err != nil {
		return "", c.safeError(err)
	}
	c.authMu.RLock()
	digest := sha256.Sum256([]byte("todos-cache-v1\x00" + c.host + "\x00" + c.token))
	c.authMu.RUnlock()
	return hex.EncodeToString(digest[:]), nil
}
