// Package domain holds transport-independent todos data.
package domain

import "time"

type PageRequest struct {
	Cursor string
	Size   int
} // 1..100; empty cursor=first

type Page[T any] struct {
	// AsOf is the original read's send time on a cache hit; zero means live.
	AsOf       time.Time
	Items      []T
	Total      int
	NextCursor string
	HasMore    bool
	// Search has more than 1000 matches; the provider cannot return every match.
	ProviderTruncated bool
}

type IssueFilter struct {
	State    string
	Assignee string
	Labels   []string
	Search   string
}

type PRFilter struct{ State string }
