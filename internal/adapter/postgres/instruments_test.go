package postgres

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"go-backend-stock/internal/core/domain"
)

func TestInstrumentUpsertAndBySymbol(t *testing.T) {
	store := newRuntimeStore(t)
	i := newInstrument(t, randomSymbol(t), "asset-"+newID(t).String())

	stored, err := store.Instruments().Upsert(t.Context(), i)
	if err != nil || stored.ID != i.ID || stored.Symbol != i.Symbol || stored.ProviderAssetID != i.ProviderAssetID ||
		stored.Name != i.Name || stored.Exchange != i.Exchange || stored.AssetClass != i.AssetClass ||
		stored.Status != i.Status || stored.Source != i.Source || !stored.VerifiedAt.Equal(i.VerifiedAt) {
		t.Fatalf("Upsert = %+v, %v; want %+v", stored, err, i)
	}
	got, err := store.Instruments().BySymbol(t.Context(), i.Symbol)
	if err != nil || got.ID != i.ID {
		t.Fatalf("BySymbol = %+v, %v", got, err)
	}
	if _, err := store.Instruments().BySymbol(t.Context(), randomSymbol(t)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("BySymbol(unknown) error = %v, want not_found", err)
	}
}

func TestInstrumentDemoSourceNeedsNoProviderAssetID(t *testing.T) {
	store := newRuntimeStore(t)
	at := now()
	i, err := domain.NewInstrument(domain.Instrument{
		ID: newID(t), Symbol: randomSymbol(t), Name: "Demo Corp", Exchange: "DEMO", AssetClass: domain.AssetClassUSEquity,
		Status: domain.InstrumentStatusActive, Source: domain.InstrumentSourceDemo, VerifiedAt: at, CreatedAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.Instruments().Upsert(t.Context(), i)
	if err != nil || got.ProviderAssetID != "" || got.Source != domain.InstrumentSourceDemo {
		t.Fatalf("Upsert(demo) = %+v, %v", got, err)
	}
}

// TestInstrumentConcurrentUpsertReturnsOneRow: requests that verified the same symbol at the same
// time all get the row that won, so their transactions reference one instrument.
func TestInstrumentConcurrentUpsertReturnsOneRow(t *testing.T) {
	store := newRuntimeStore(t)
	symbol := randomSymbol(t)
	assetID := "asset-" + newID(t).String()
	ids := make(chan domain.ID, 10)

	succeeded, errs := runConcurrently(10, func() error {
		got, err := store.Instruments().Upsert(t.Context(), newInstrument(t, symbol, assetID))
		ids <- got.ID
		return err
	})

	if succeeded != 10 {
		t.Fatalf("%d of 10 concurrent upserts succeeded: %v", succeeded, errs)
	}
	close(ids)
	first := <-ids
	for id := range ids {
		if id != first {
			t.Fatalf("concurrent upserts returned different rows: %s and %s", first, id)
		}
	}
}

// TestInstrumentSymbolChangeIsUnknownSymbol: the provider reports a stored asset under a new
// symbol (FB -> META). Phase 1 does not follow symbol changes, so the request gets a readable
// unknown_symbol instead of a 500, and the transaction stays usable.
func TestInstrumentSymbolChangeIsUnknownSymbol(t *testing.T) {
	store := newRuntimeStore(t)
	assetID := "asset-" + newID(t).String()
	if _, err := store.Instruments().Upsert(t.Context(), newInstrument(t, randomSymbol(t), assetID)); err != nil {
		t.Fatal(err)
	}
	renamed := newInstrument(t, randomSymbol(t), assetID)
	e := auditEvent(t, `{}`)

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		_, err := r.Instruments().Upsert(t.Context(), renamed)
		if de := (*domain.Error)(nil); !errors.As(err, &de) || de.Code != domain.CodeUnknownSymbol {
			t.Errorf("Upsert(renamed asset) error = %v, want unknown_symbol", err) // not Fatal inside the callback
		}
		return r.Audit().Append(t.Context(), e)
	})

	if err != nil || !auditExists(t, e.ID) {
		t.Fatalf("transaction after the symbol change: %v", err)
	}
	if _, err := store.Instruments().BySymbol(t.Context(), renamed.Symbol); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("renamed symbol was stored: %v", err)
	}
}

// randomSymbol returns a valid 15-character symbol no other test uses.
func randomSymbol(t *testing.T) domain.Symbol {
	t.Helper()
	b := make([]byte, 7)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	s, err := domain.ParseSymbol("T" + strings.ToUpper(hex.EncodeToString(b)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newInstrument(t *testing.T, symbol domain.Symbol, assetID string) domain.Instrument {
	t.Helper()
	at := now()
	i, err := domain.NewInstrument(domain.Instrument{
		ID: newID(t), Symbol: symbol, ProviderAssetID: assetID, Name: "Test Corp", Exchange: "NASDAQ",
		AssetClass: domain.AssetClassUSEquity, Status: domain.InstrumentStatusActive, Source: domain.InstrumentSourceAlpaca,
		VerifiedAt: at, CreatedAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	return i
}
