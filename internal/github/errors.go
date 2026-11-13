package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/pkieltyka/todos/internal/domain"
)

type operation struct {
	Name              string
	Mutation, Started bool
}
type responseMeta struct {
	Headers           http.Header
	CredentialVersion uint64
}

func classify(err error, op operation, meta responseMeta, now time.Time) *domain.Error {
	if err == nil {
		return nil
	}
	var existing *domain.Error
	if errors.As(err, &existing) {
		return existing
	}
	out := &domain.Error{Kind: domain.ErrGitHub, Message: "GitHub returned an invalid response", Err: err}
	var he *api.HTTPError
	var ge *api.GraphQLError
	isHTTP, isGraphQL := errors.As(err, &he), errors.As(err, &ge)
	if !isHTTP && !isGraphQL {
		var syntax *json.SyntaxError
		var types *json.UnmarshalTypeError
		var ne net.Error
		var unsupported *json.UnsupportedTypeError
		var unsupportedValue *json.UnsupportedValueError
		switch {
		case errors.As(err, &unsupported) || errors.As(err, &unsupportedValue):
			out.Kind = domain.ErrInternal
			out.Message = "could not encode GitHub request"
			return out
		case errors.Is(err, context.Canceled):
			out.Kind = domain.ErrNetwork
			out.Message = "operation interrupted"
		case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout():
			out.Kind = domain.ErrTimeout
			out.Message = "GitHub request timed out"
		case errors.As(err, &syntax) || errors.As(err, &types):
		case errors.As(err, &ne) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
			out.Kind = domain.ErrNetwork
			out.Message = "GitHub request failed"
		default:
			out.Kind = domain.ErrNetwork
			out.Message = "GitHub request failed"
		}
		out.Uncertain = op.Mutation && op.Started
		return out
	}
	status := 0
	messages := []string{}
	codes := []string{}
	if isHTTP {
		status = he.StatusCode
		if he.Message != "" {
			messages = append(messages, he.Message)
		}
		if len(he.Headers) > 0 {
			meta.Headers = he.Headers
		}
	}
	if isGraphQL {
		for _, item := range ge.Errors {
			messages = append(messages, item.Message)
			codes = append(codes, strings.ToUpper(item.Type))
			for _, key := range []string{"classification", "code"} {
				if code, ok := item.Extensions[key].(string); ok {
					codes = append(codes, strings.ToUpper(code))
				}
			}
		}
	}
	text := strings.Join(messages, "; ")
	lower := strings.ToLower(text)
	hasCode := func(want ...string) bool {
		for _, c := range codes {
			for _, w := range want {
				if c == w {
					return true
				}
			}
		}
		return false
	}
	scopeMissing := hasCode("INSUFFICIENT_SCOPES") || strings.Contains(lower, "scope") && (strings.Contains(lower, "missing") || strings.Contains(lower, "requires") || strings.Contains(lower, "required") || strings.Contains(lower, "insufficient") || strings.Contains(lower, "lacks"))
	rate := status == http.StatusTooManyRequests || status == http.StatusForbidden && meta.Headers.Get("X-RateLimit-Remaining") == "0" || hasCode("RATE_LIMITED", "RATE_LIMIT") || strings.Contains(lower, "rate limit") || strings.Contains(lower, "abuse detection") || status == http.StatusUnprocessableEntity && (strings.Contains(lower, "spam") || strings.Contains(lower, "abuse"))
	switch {
	case status == http.StatusUnauthorized || hasCode("UNAUTHENTICATED", "UNAUTHORIZED", "BAD_CREDENTIALS") || strings.Contains(lower, "bad credentials"):
		out.Kind = domain.ErrAuth
		out.Message = "GitHub rejected the authentication token"
		out.Hint = "gh auth login"
	case scopeMissing:
		out.Kind = domain.ErrMissingScope
		out.Message = text
		scope := "repo"
		if strings.Contains(lower, "read:project") {
			scope = "read:project"
		} else if strings.Contains(lower, "project") {
			scope = "project"
		}
		out.Hint = "gh auth refresh -s " + scope
	case rate:
		out.Kind = domain.ErrRateLimited
		out.Message = "GitHub rate limit reached"
		out.RetryAfter = retryAfter(meta.Headers, now)
	case status == http.StatusForbidden || hasCode("FORBIDDEN"):
		out.Kind = domain.ErrForbidden
		out.Message = "GitHub refused this operation"
		if text != "" {
			out.Message += " : " + text
		}
	case status == http.StatusNotFound || hasCode("NOT_FOUND"):
		out.Kind = domain.ErrNotFound
		out.Message = "GitHub resource not found or no access"
	case status == http.StatusGone:
		out.Kind = domain.ErrNotFound
		out.Message = "GitHub resource not found or no access"
		if strings.Contains(lower, "issues") && strings.Contains(lower, "disabled") {
			out.Kind = domain.ErrIssuesDisabled
			out.Message = "issues are disabled for this repository"
		}
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity || hasCode("UNPROCESSABLE"):
		out.Kind = domain.ErrInvalidInput
		out.Message = "GitHub rejected the supplied values"
		if text != "" {
			out.Message += " : " + text
		}
	default:
		out.Kind = domain.ErrGitHub
		out.Message = "GitHub returned an invalid response"
		if status >= 500 {
			out.Message = "GitHub server error"
		}
		out.Uncertain = op.Mutation && op.Started
	}
	return out
}

func invalidInput(message string) error {
	return &domain.Error{Kind: domain.ErrInvalidInput, Message: message}
}
func malformed(message string) error {
	return &domain.Error{Kind: domain.ErrGitHub, Message: "invalid GitHub response: " + message}
}
