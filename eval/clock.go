package eval

import (
	"sync"
	"time"
)

// Day0 is virtual day zero of every eval run.
var Day0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// dayTime returns the virtual instant d days after Day0.
func dayTime(d int) time.Time { return Day0.Add(time.Duration(d) * 24 * time.Hour) }

// Clock is a settable virtual clock satisfying engram.Clock. It is safe for
// concurrent use.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

// Set moves the clock to t.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// Now returns the current virtual time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
