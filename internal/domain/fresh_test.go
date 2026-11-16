package domain

import (
	"context"
	"testing"
	"time"
)

func TestFresh_ContextPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, ok := FreshSince(ctx); ok {
		t.Fatal("ordinary context is fresh")
	}
	since := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	fresh := WithFreshSince(ctx, since)
	child, cancelChild := context.WithCancel(fresh)
	defer cancelChild()
	got, ok := FreshSince(child)
	_, parentMarked := FreshSince(ctx)
	if !ok || !got.Equal(since) || parentMarked {
		t.Fatal("fresh mark did not propagate or leaked to parent")
	}
	cancel()
	if child.Err() != context.Canceled {
		t.Fatal("cancellation lost")
	}
}

func TestFresh_LatestMarkWins(t *testing.T) {
	start := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	ctx := WithFreshSince(context.Background(), start)
	ctx = WithFreshSince(ctx, start.Add(time.Minute))
	ctx = WithFreshSince(ctx, start)
	got, ok := FreshSince(ctx)
	if !ok || !got.Equal(start.Add(time.Minute)) {
		t.Fatal("earlier child mark weakened freshness")
	}
}
