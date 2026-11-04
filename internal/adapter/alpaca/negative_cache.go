package alpaca

import (
	"sync"
	"time"
)

// negativeCache remembers symbols Alpaca does not know, so a user who keeps asking for a typo does
// not send every attempt to Alpaca. It holds at most max entries; when full it drops the expired
// ones first and then the oldest.
type negativeCache struct {
	ttl time.Duration
	max int

	mu      sync.Mutex
	entries map[string]time.Time // symbol -> when it was remembered
}

func newNegativeCache(ttl time.Duration, max int) *negativeCache {
	return &negativeCache{ttl: ttl, max: max, entries: make(map[string]time.Time, max)}
}

// seen reports whether symbol was remembered less than ttl ago.
func (c *negativeCache) seen(symbol string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	at, ok := c.entries[symbol]
	if !ok {
		return false
	}
	if now.Sub(at) >= c.ttl {
		delete(c.entries, symbol)
		return false
	}
	return true
}

// remember records symbol at now.
func (c *negativeCache) remember(symbol string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[symbol]; !ok && len(c.entries) >= c.max {
		c.makeRoom(now)
	}
	c.entries[symbol] = now
}

// makeRoom frees one slot. The caller holds mu.
func (c *negativeCache) makeRoom(now time.Time) {
	var oldest string
	var oldestAt time.Time
	for symbol, at := range c.entries {
		if now.Sub(at) >= c.ttl {
			delete(c.entries, symbol)
			continue
		}
		if oldest == "" || at.Before(oldestAt) {
			oldest, oldestAt = symbol, at
		}
	}
	if len(c.entries) >= c.max && oldest != "" {
		delete(c.entries, oldest)
	}
}
