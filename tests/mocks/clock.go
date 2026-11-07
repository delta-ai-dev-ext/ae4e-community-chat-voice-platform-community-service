// Package mocks holds the test doubles used by the unit and integration suites.
// They implement the same interfaces as the production dependencies, so a test
// never needs PostgreSQL, Kafka or wall-clock time.
package mocks

import (
	"sync"
	"time"

	"community_service/internal/models"
)

// Clock is a deterministic, mutable clock implementing models.Clock.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock frozen at the given instant.
func NewClock(now time.Time) *Clock {
	return &Clock{now: now.UTC()}
}

// Now returns the frozen instant.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward and returns the new instant.
func (c *Clock) Advance(duration time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
	return c.now
}

// Set jumps the clock to the given instant.
func (c *Clock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now.UTC()
}

// compile-time assertion that the mock satisfies the port.
var _ models.Clock = (*Clock)(nil)
