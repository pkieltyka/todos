package service

import "time"

// CacheStore is disposable, best-effort storage. Errors are always cache misses.
type CacheStore interface {
	// readAt is the send time of the GitHub read, never file-write time.
	Get(space, key string) (data []byte, readAt time.Time, ok bool)
	Put(space, key string, data []byte, readAt time.Time)
	Epoch(space string) string
	Bump(space string) bool
	Drop(space, prefix string)
}
