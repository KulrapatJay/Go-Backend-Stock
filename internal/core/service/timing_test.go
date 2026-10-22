package service

import (
	"context"
	"testing"
	"time"
)

// sleepRecorder replaces sleeping: it records the wait and moves the fake clock instead.
type sleepRecorder struct {
	clock *fakeClock
	calls []time.Duration
}

func (s *sleepRecorder) sleep(_ context.Context, d time.Duration) {
	s.calls = append(s.calls, d)
	s.clock.Advance(d)
}

func newTestPacer(clock *fakeClock, rec *sleepRecorder, floor, jitter, sample time.Duration) *Pacer {
	return &Pacer{
		clock: clock, floor: floor, jitter: jitter, sleep: rec.sleep,
		randomJitter: func(max time.Duration) time.Duration { return min(sample, max) },
	}
}

func TestPacerWaitsOutTheRestOfTheFloor(t *testing.T) {
	clock := newFakeClock()
	rec := &sleepRecorder{clock: clock}
	pacer := newTestPacer(clock, rec, 500*time.Millisecond, 100*time.Millisecond, 40*time.Millisecond)

	start := clock.Now()
	clock.Advance(120 * time.Millisecond) // the work took 120ms
	pacer.Wait(t.Context(), start)

	if len(rec.calls) != 1 || rec.calls[0] != 420*time.Millisecond { // 500 + 40 - 120
		t.Errorf("sleeps = %v, want [420ms]", rec.calls)
	}
	if elapsed := clock.Now().Sub(start); elapsed != 540*time.Millisecond {
		t.Errorf("the response took %v, want floor + jitter = 540ms", elapsed)
	}
}

func TestPacerDoesNotSleepWhenTheWorkWasSlower(t *testing.T) {
	clock := newFakeClock()
	rec := &sleepRecorder{clock: clock}
	pacer := newTestPacer(clock, rec, 500*time.Millisecond, 100*time.Millisecond, 100*time.Millisecond)

	start := clock.Now()
	clock.Advance(700 * time.Millisecond)
	pacer.Wait(t.Context(), start)
	if len(rec.calls) != 0 {
		t.Errorf("slept %v although the work already took longer than the target", rec.calls)
	}

	// Exactly on the target: nothing left to wait for either.
	start = clock.Now()
	clock.Advance(600 * time.Millisecond)
	pacer.Wait(t.Context(), start)
	if len(rec.calls) != 0 {
		t.Errorf("slept %v when the target had just been reached", rec.calls)
	}
}

func TestPacerWithoutJitterWaitsExactlyTheFloor(t *testing.T) {
	clock := newFakeClock()
	rec := &sleepRecorder{clock: clock}
	pacer := newTestPacer(clock, rec, 500*time.Millisecond, 0, 50*time.Millisecond)

	pacer.Wait(t.Context(), clock.Now())
	if len(rec.calls) != 1 || rec.calls[0] != 500*time.Millisecond {
		t.Errorf("sleeps = %v, want [500ms]", rec.calls)
	}
}

func TestSleepContextStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	start := time.Now()
	sleepContext(ctx, time.Hour)
	if time.Since(start) > time.Second {
		t.Errorf("sleepContext kept waiting %v after the context ended", time.Since(start))
	}

	start = time.Now()
	sleepContext(t.Context(), 30*time.Millisecond)
	if elapsed := time.Since(start); elapsed < 25*time.Millisecond {
		t.Errorf("sleepContext returned after %v, want about 30ms", elapsed)
	}
}

func TestCryptoJitterStaysInRange(t *testing.T) {
	if got := cryptoJitter(0); got != 0 {
		t.Errorf("cryptoJitter(0) = %v, want 0", got)
	}
	positive := false
	for range 200 {
		got := cryptoJitter(100 * time.Millisecond)
		if got < 0 || got > 100*time.Millisecond {
			t.Fatalf("cryptoJitter(100ms) = %v, outside 0..100ms", got)
		}
		positive = positive || got > 0
	}
	if !positive {
		t.Error("cryptoJitter(100ms) returned 0 in 200 draws")
	}
}

func TestNewPacerIsUsable(t *testing.T) {
	clock := newFakeClock()
	pacer := NewPacer(clock, 0, 0)
	pacer.Wait(t.Context(), clock.Now()) // must neither panic nor block
}

func TestNowForStorageTruncatesToMicroseconds(t *testing.T) {
	clock := newFakeClock()
	clock.now = time.Date(2026, 10, 22, 9, 0, 0, 123_456_789, time.FixedZone("ICT", 7*3600))

	got := NowForStorage(clock)
	if want := time.Date(2026, 10, 22, 2, 0, 0, 123_456_000, time.UTC); !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("NowForStorage() = %v, want %v in UTC", got, want)
	}
}
