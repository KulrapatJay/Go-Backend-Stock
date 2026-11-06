package domain

import "time"

const maxQuoteLabelRunes = 40

// SessionStatus says whether the market was open when the quoted bar closed.
type SessionStatus string

const (
	SessionOpen         SessionStatus = "open"
	SessionMarketClosed SessionStatus = "market_closed"
	// SessionUnknown means the adapter has no market calendar to say (demo data, or a calendar that
	// could not be read): it never guesses that a day was a trading day.
	SessionUnknown SessionStatus = "unknown"
)

// ParseSessionStatus accepts exactly "open", "market_closed" or "unknown".
func ParseSessionStatus(s string) (SessionStatus, error) {
	return parseEnum("session_status", s, SessionOpen, SessionMarketClosed, SessionUnknown)
}

// PriceQuote is one delayed reference price: the close of the last bar before the cutoff. AsOf is the
// time of the source data (the end of the bar), never the moment the server fetched it; FetchedAt is
// that moment and is what a cache ages by. IsDemo marks sample data.
type PriceQuote struct {
	Symbol        Symbol
	Price         Price
	Currency      string
	AsOf          time.Time
	FetchedAt     time.Time
	Source        string
	Feed          string
	IsDemo        bool
	Stale         bool
	Cached        bool
	SessionStatus SessionStatus
}

// NewPriceQuote checks a quote an adapter built. A violation is a bug in the adapter, so it returns an
// internal error; the times come back in UTC.
func NewPriceQuote(q PriceQuote) (PriceQuote, error) {
	var err error
	switch {
	case q.Symbol.IsZero():
		err = invariant("quote.symbol", "is required")
	case q.Price.IsZero():
		err = invariant("quote.price", "must be greater than zero")
	case q.Currency != "USD":
		err = invariant("quote.currency", "must be USD")
	case q.AsOf.IsZero():
		err = invariant("quote.as_of", "is required")
	case q.FetchedAt.IsZero():
		err = invariant("quote.fetched_at", "is required")
	case q.AsOf.After(q.FetchedAt):
		err = invariant("quote.as_of", "must not be after fetched_at")
	case !isText(q.Source, 1, maxQuoteLabelRunes):
		err = invariant("quote.source", "must be 1-40 printable characters")
	case !isText(q.Feed, 1, maxQuoteLabelRunes):
		err = invariant("quote.feed", "must be 1-40 printable characters")
	case !isOneOf(q.SessionStatus, SessionOpen, SessionMarketClosed, SessionUnknown):
		err = invariant("quote.session_status", "must be open, market_closed or unknown")
	}
	if err != nil {
		return PriceQuote{}, err
	}
	q.AsOf, q.FetchedAt = q.AsOf.UTC(), q.FetchedAt.UTC()
	return q, nil
}

// QuoteBatch is a provider's answer for several symbols at once. Quotes is keyed by Symbol.String().
// A symbol the provider has no price for is in Unavailable; that is not an error.
type QuoteBatch struct {
	Quotes      map[string]PriceQuote
	Unavailable []Symbol
}
