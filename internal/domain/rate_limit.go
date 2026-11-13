// Package domain holds transport-independent todos data.
package domain

import "time"

type RateLimit struct {
	Known            bool
	Remaining, Limit int
	ResetAt          time.Time
	Resource         string
	Buckets          []RateBucket
	BlockedUntil     time.Time // secondary-rate-limit cooldown
}

type RateBucket struct {
	Resource         string
	Remaining, Limit int
	ResetAt          time.Time
}

type DoctorCheck struct {
	Name         string
	OK           bool
	Detail, Hint string
}

type DoctorReport struct {
	OK           bool
	Checks       []DoctorCheck
	FirstFailure *Error
}
