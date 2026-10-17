// Package port declares the boundaries the core owns. Adapters implement these interfaces;
// the core never imports an adapter. Service and provider ports are added in the milestone
// that first implements them (spec 2026-10-16-milestone-2-domain-ports-design.md section 5).
package port

import "time"

// Clock is the core's only source of the current time, so tests can fix it.
type Clock interface {
	Now() time.Time
}
