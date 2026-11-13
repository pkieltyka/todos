package github

import (
	"math"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

type rateTracker struct {
	mu           sync.Mutex
	buckets      map[string]domain.RateBucket
	blockedUntil time.Time
}

func (r *rateTracker) observe(h http.Header, _ time.Time) {
	resource := h.Get("X-RateLimit-Resource")
	if resource != "core" && resource != "graphql" && resource != "search" {
		return
	}
	limit, e1 := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	remaining, e2 := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	reset, e3 := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || limit <= 0 || limit > math.MaxInt/10 || remaining < 0 || remaining > limit || reset <= 0 {
		return
	}
	next := domain.RateBucket{Resource: resource, Limit: limit, Remaining: remaining, ResetAt: time.Unix(reset, 0).UTC()}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.buckets == nil {
		r.buckets = make(map[string]domain.RateBucket)
	}
	if old, ok := r.buckets[resource]; ok {
		if next.ResetAt.Before(old.ResetAt) {
			return
		}
		if next.ResetAt.Equal(old.ResetAt) {
			next.Remaining = min(next.Remaining, old.Remaining)
		}
	}
	r.buckets[resource] = next
}

func (r *rateTracker) block(until time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if until.After(r.blockedUntil) {
		r.blockedUntil = until
	}
}

func (r *rateTracker) snapshot(now time.Time) domain.RateLimit {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := domain.RateLimit{Buckets: []domain.RateBucket{}}
	if r.blockedUntil.After(now) {
		out.BlockedUntil = r.blockedUntil
	}
	for _, b := range r.buckets {
		if !b.ResetAt.After(now) {
			continue
		}
		out.Buckets = append(out.Buckets, b)
		if !out.Known || float64(b.Remaining)/float64(b.Limit) < float64(out.Remaining)/float64(out.Limit) {
			out.Known = true
			out.Resource = b.Resource
			out.Limit = b.Limit
			out.Remaining = b.Remaining
			out.ResetAt = b.ResetAt
		}
	}
	sort.Slice(out.Buckets, func(i, j int) bool { return out.Buckets[i].Resource < out.Buckets[j].Resource })
	return out
}

func retryAfter(h http.Header, now time.Time) time.Duration {
	var until time.Time
	if raw := h.Get("Retry-After"); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 32); err == nil && seconds >= 0 {
			until = now.Add(time.Duration(seconds) * time.Second)
		} else if parsed, err := http.ParseTime(raw); err == nil {
			until = parsed
		}
	}
	if h.Get("X-RateLimit-Remaining") == "0" {
		if seconds, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil && seconds > 0 {
			reset := time.Unix(seconds, 0)
			if reset.After(until) {
				until = reset
			}
		}
	}
	if until.IsZero() {
		return time.Minute
	}
	return max(time.Second, until.Sub(now))
}
