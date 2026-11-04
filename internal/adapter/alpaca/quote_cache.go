package alpaca

import (
	"sync"
	"time"

	"go-backend-stock/internal/core/domain"
)

const (
	// freshTTL is how long a quote answers without asking Alpaca again (spec section 12). It is also
	// what keeps a busy portfolio page inside the free plan's 200 requests per minute.
	freshTTL = 60 * time.Second
	// outageMaxAge is how old a stored quote may be when Alpaca cannot be reached.
	outageMaxAge = 7 * 24 * time.Hour
	// cacheMax is how many symbols are kept.
	cacheMax = 500
)

// quoteCache keeps the last quote of each symbol, as it came from Alpaca, in memory. When it is full
// the quote fetched longest ago makes room. It is lost on restart.
type quoteCache struct {
	max int

	mu      sync.Mutex
	entries map[string]domain.PriceQuote
}

func newQuoteCache(max int) *quoteCache {
	return &quoteCache{max: max, entries: make(map[string]domain.PriceQuote, max)}
}

// get returns the stored quote of symbol if it was fetched less than maxAge before now.
func (c *quoteCache) get(symbol string, now time.Time, maxAge time.Duration) (domain.PriceQuote, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	q, ok := c.entries[symbol]
	if !ok || now.Sub(q.FetchedAt) >= maxAge {
		return domain.PriceQuote{}, false
	}
	return q, true
}

// put stores q under its symbol.
func (c *quoteCache) put(q domain.PriceQuote) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := q.Symbol.String()
	if _, known := c.entries[key]; !known && len(c.entries) >= c.max {
		var oldest string
		var oldestAt time.Time
		for symbol, stored := range c.entries {
			if oldest == "" || stored.FetchedAt.Before(oldestAt) {
				oldest, oldestAt = symbol, stored.FetchedAt
			}
		}
		delete(c.entries, oldest)
	}
	c.entries[key] = q
}

func (c *quoteCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
