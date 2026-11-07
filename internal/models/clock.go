package models

import "time"

// Clock supplies the current time so domain logic is deterministic in tests and
// every timestamp the service writes comes from one injectable source.
type Clock interface {
	Now() time.Time
}

// SystemClock is the production Clock; it always reports UTC so stored
// timestamps and event envelopes are UTC by construction.
type SystemClock struct{}

// Now returns the current UTC time.
func (SystemClock) Now() time.Time { return time.Now().UTC() }
