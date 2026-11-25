package domain

import "math"

// Time is a virtual instant measured in abstract ticks. It never comes from a wall clock.
type Time int64

// Duration is a span of virtual ticks.
type Duration int64

// Never is the return time of an operation that did not complete.
const Never Time = math.MaxInt64

// Clock is the controlled virtual clock. Only the scheduler advances it.
type Clock struct{ now Time }

// Now returns the current virtual time.
func (c *Clock) Now() Time { return c.now }

// AdvanceTo moves the clock forward. Moving backwards returns ErrTimeTravel.
func (c *Clock) AdvanceTo(t Time) error {
	if t < c.now {
		return ErrTimeTravel
	}
	c.now = t
	return nil
}
