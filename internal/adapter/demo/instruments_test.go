package demo_test

import (
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/adapter/demo"
	"go-backend-stock/internal/core/domain"
)

func TestDemoInstrumentsKnowsTheFixedList(t *testing.T) {
	provider := demo.NewInstruments()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	for _, raw := range []string{"AAPL", "MSFT", "NVDA", "GOOGL", "AMZN", "META", "TSLA", "SPY"} {
		t.Run(raw, func(t *testing.T) {
			symbol, err := domain.ParseSymbol(raw)
			if err != nil {
				t.Fatal(err)
			}
			got, err := provider.Lookup(t.Context(), symbol)
			if err != nil {
				t.Fatalf("Lookup() error = %v", err)
			}
			if got.Symbol != symbol || got.Status != domain.InstrumentStatusActive ||
				got.AssetClass != domain.AssetClassUSEquity || got.Source != domain.InstrumentSourceDemo {
				t.Errorf("Lookup() = %+v", got)
			}
			if !got.ID.IsZero() || !got.CreatedAt.IsZero() || !got.UpdatedAt.IsZero() || !got.VerifiedAt.IsZero() {
				t.Errorf("the provider must leave the ID and the times to the service: %+v", got)
			}

			id, err := domain.NewID()
			if err != nil {
				t.Fatal(err)
			}
			got.ID, got.VerifiedAt, got.CreatedAt, got.UpdatedAt = id, now, now, now
			if _, err := domain.NewInstrument(got); err != nil {
				t.Errorf("NewInstrument() rejected the demo instrument: %v", err)
			}
		})
	}

	t.Run("an unknown symbol", func(t *testing.T) {
		symbol, err := domain.ParseSymbol("ZZZZ")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := provider.Lookup(t.Context(), symbol); !errors.Is(err, domain.ErrUnknownSymbol) {
			t.Errorf("Lookup(ZZZZ) error = %v, want unknown_symbol", err)
		}
	})
}
