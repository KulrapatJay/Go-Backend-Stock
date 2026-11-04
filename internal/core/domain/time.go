package domain

import "time"

var minExecutedAt = time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)

// ParseExecutedAt parses the time a trade was executed. It requires RFC 3339 with an explicit
// offset, rejects sub-microsecond precision instead of rounding (PostgreSQL keeps microseconds),
// and accepts 1970-01-01T00:00:00Z up to and including now. The result is in UTC.
func ParseExecutedAt(s string, now time.Time) (time.Time, error) {
	t, err := parseTimestamp("executed_at", s)
	if err != nil {
		return time.Time{}, err
	}
	if t.Before(minExecutedAt) {
		return time.Time{}, invalidInput("executed_at", "must not be before 1970-01-01T00:00:00Z")
	}
	if t.After(now) {
		return time.Time{}, invalidInput("executed_at", "must not be in the future")
	}
	return t, nil
}

// ParseTimeFilter parses a from/to filter on executed_at. It follows the same format rules as
// ParseExecutedAt (RFC 3339 with an offset, at most microsecond precision, result in UTC) but
// allows any time, since a filter may look at the future or before 1970. field names the query
// parameter in the error message.
func ParseTimeFilter(field, s string) (time.Time, error) {
	return parseTimestamp(field, s)
}

// parseTimestamp is the format check shared by ParseExecutedAt and ParseTimeFilter.
func parseTimestamp(field, s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		// Never wrap err: it repeats the input.
		return time.Time{}, invalidInput(field, "must be an RFC 3339 timestamp with a timezone, such as 2026-10-01T14:30:00Z")
	}
	if t.Nanosecond()%int(time.Microsecond) != 0 {
		return time.Time{}, invalidInput(field, "must have at most microsecond precision")
	}
	return t.UTC(), nil
}
