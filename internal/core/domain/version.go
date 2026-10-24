package domain

import "math"

// Version is the optimistic-locking version of a portfolio or transaction. It starts at 1.
type Version struct{ value int32 }

// ParseVersion accepts 1..math.MaxInt32.
func ParseVersion(n int64) (Version, error) {
	if n < 1 || n > math.MaxInt32 {
		return Version{}, invalidInput("version", "must be a positive integer")
	}
	return Version{value: int32(n)}, nil
}

// Int32 returns the version number; 0 only for the zero value.
func (v Version) Int32() int32 { return v.value }

// IsZero reports whether v is the zero value, which is never a valid version.
func (v Version) IsZero() bool { return v.value == 0 }
