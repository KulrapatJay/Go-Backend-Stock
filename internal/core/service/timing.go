package service

import (
	"context"
	"crypto/rand"
	"math/big"
	"time"

	"go-backend-stock/internal/core/port"
)

// Pacer makes a response take at least a floor plus a random jitter, so response time does not
// reveal whether an email has an account (register, resend, password reset). Use it as
// `defer pacer.Wait(ctx, clock.Now())` at the top of the use case so success and error paths both
// wait. It is a property of the use case, not of HTTP, which is why it lives in the core.
type Pacer struct {
	clock        port.Clock
	floor        time.Duration
	jitter       time.Duration
	sleep        func(ctx context.Context, d time.Duration)
	randomJitter func(max time.Duration) time.Duration
}

// NewPacer returns a pacer that really sleeps. The defaults are a 500 ms floor and 0-100 ms of jitter.
func NewPacer(clock port.Clock, floor, jitter time.Duration) *Pacer {
	return &Pacer{clock: clock, floor: floor, jitter: jitter, sleep: sleepContext, randomJitter: cryptoJitter}
}

// Wait blocks until floor plus a random jitter in 0..jitter has passed since start. It returns at
// once if that time has already passed, and as soon as ctx ends.
func (p *Pacer) Wait(ctx context.Context, start time.Time) {
	target := p.floor + p.randomJitter(p.jitter)
	if remaining := target - p.clock.Now().Sub(start); remaining > 0 {
		p.sleep(ctx, remaining)
	}
}

func sleepContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// cryptoJitter returns a uniform duration in 0..max. If the random source fails it returns 0:
// less jitter weakens the timing defence slightly but must not fail the request.
func cryptoJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)+1))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64())
}

// NowForStorage returns the current time in UTC cut to microseconds. PostgreSQL keeps
// microseconds, so a time written with nanoseconds would compare differently in Go and in SQL
// by up to a microsecond when expiry is checked. Services pass this, not Clock.Now(), to repositories.
func NowForStorage(clock port.Clock) time.Time {
	return clock.Now().UTC().Truncate(time.Microsecond)
}
