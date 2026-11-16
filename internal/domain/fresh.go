package domain

import (
	"context"
	"time"
)

type freshKey struct{}

// WithFreshSince rejects data whose GitHub read began before since. A later
// mark inherited from the parent context cannot be weakened by a child.
func WithFreshSince(ctx context.Context, since time.Time) context.Context {
	if previous, ok := FreshSince(ctx); ok && previous.After(since) {
		return ctx
	}
	return context.WithValue(ctx, freshKey{}, since)
}

func FreshSince(ctx context.Context) (time.Time, bool) {
	since, ok := ctx.Value(freshKey{}).(time.Time)
	return since, ok
}
