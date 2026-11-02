// Package clock is the production domain.Clock adapter.
package clock

import "time"

// Clock reads the system wall clock in UTC.
type Clock struct{}

// New returns the system clock.
func New() Clock { return Clock{} }

// Now returns the current time in UTC.
func (Clock) Now() time.Time { return time.Now().UTC() }
