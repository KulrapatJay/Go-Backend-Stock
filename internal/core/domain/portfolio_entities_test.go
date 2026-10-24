package domain_test

import (
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestPortfolioEnums(t *testing.T) {
	cases := map[string]func(string) error{
		"USD":       func(s string) error { _, err := domain.ParseCurrency(s); return err },
		"us_equity": func(s string) error { _, err := domain.ParseAssetClass(s); return err },
		"active":    func(s string) error { _, err := domain.ParseInstrumentStatus(s); return err },
		"inactive":  func(s string) error { _, err := domain.ParseInstrumentStatus(s); return err },
		"alpaca":    func(s string) error { _, err := domain.ParseInstrumentSource(s); return err },
		"demo":      func(s string) error { _, err := domain.ParseInstrumentSource(s); return err },
		"buy":       func(s string) error { _, err := domain.ParseSide(s); return err },
		"sell":      func(s string) error { _, err := domain.ParseSide(s); return err },
	}
	for value, parse := range cases {
		if err := parse(value); err != nil {
			t.Errorf("parse(%q) error = %v", value, err)
		}
		if err := parse(strings.ToUpper(value) + "x"); err == nil {
			t.Errorf("parse accepted %q", strings.ToUpper(value)+"x")
		}
	}
	_, err := domain.ParseSide("BUY")
	requireInvalidInput(t, err, "side")
	_, err = domain.ParseCurrency("usd")
	requireInvalidInput(t, err, "currency")
}

func validInstrument(t *testing.T) domain.Instrument {
	sym, err := domain.ParseSymbol("AAPL")
	if err != nil {
		t.Fatal(err)
	}
	return domain.Instrument{
		ID: mustID(t), Symbol: sym, ProviderAssetID: "b0b6dd9d-8b9b-48a9-ba46-b9d54906e415", Name: "Apple Inc. Common Stock",
		Exchange: "NASDAQ", AssetClass: domain.AssetClassUSEquity, Status: domain.InstrumentStatusActive,
		Source: domain.InstrumentSourceAlpaca, VerifiedAt: t0, CreatedAt: t0, UpdatedAt: t0,
	}
}

func TestNewInstrument(t *testing.T) {
	if _, err := domain.NewInstrument(validInstrument(t)); err != nil {
		t.Fatalf("NewInstrument(valid) error = %v", err)
	}
	demo := validInstrument(t)
	demo.Source, demo.ProviderAssetID = domain.InstrumentSourceDemo, ""
	if _, err := domain.NewInstrument(demo); err != nil {
		t.Errorf("demo instrument without provider asset id rejected: %v", err)
	}
	for name, mutate := range map[string]func(*domain.Instrument){
		"no id":                  func(i *domain.Instrument) { i.ID = domain.ID{} },
		"no symbol":              func(i *domain.Instrument) { i.Symbol = domain.Symbol{} },
		"empty name":             func(i *domain.Instrument) { i.Name = "" },
		"long name":              func(i *domain.Instrument) { i.Name = strings.Repeat("n", 201) },
		"control in name":        func(i *domain.Instrument) { i.Name = "Apple\nInc" },
		"long exchange":          func(i *domain.Instrument) { i.Exchange = strings.Repeat("x", 21) },
		"crypto":                 func(i *domain.Instrument) { i.AssetClass = "crypto" },
		"unknown status":         func(i *domain.Instrument) { i.Status = "halted" },
		"unknown source":         func(i *domain.Instrument) { i.Source = "yahoo" },
		"alpaca without asset":   func(i *domain.Instrument) { i.ProviderAssetID = "" },
		"long provider asset id": func(i *domain.Instrument) { i.ProviderAssetID = strings.Repeat("a", 65) },
		"never verified":         func(i *domain.Instrument) { i.VerifiedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			i := validInstrument(t)
			mutate(&i)
			_, err := domain.NewInstrument(i)
			requireCode(t, err, domain.CodeInternal)
		})
	}
}

func TestNewTransactionFilter(t *testing.T) {
	page, err := domain.ParsePage("", "")
	if err != nil {
		t.Fatal(err)
	}
	from, to := t0.Add(-time.Hour), t0
	f, err := domain.NewTransactionFilter(page, nil, nil, &from, &to)
	if err != nil || f.Page.Limit() != domain.DefaultPageLimit || !f.From.Equal(from) {
		t.Fatalf("NewTransactionFilter(valid) = %+v, %v", f, err)
	}
	if _, err := domain.NewTransactionFilter(page, nil, nil, &to, &to); err != nil {
		t.Errorf("from == to rejected: %v", err)
	}
	if _, err := domain.NewTransactionFilter(page, nil, nil, &from, nil); err != nil {
		t.Errorf("open-ended range rejected: %v", err)
	}
	_, err = domain.NewTransactionFilter(page, nil, nil, &to, &from)
	requireInvalidInput(t, err, "from")
	_, err = domain.NewTransactionFilter(domain.Page{}, nil, nil, nil, nil)
	requireCode(t, err, domain.CodeInternal)
}

// TestNewTransactionFilterRejectsEmptyFilterValues: an absent filter is nil. A pointer to the zero
// Symbol or an empty Side is a bug in the caller, not a filter that matches everything or nothing.
func TestNewTransactionFilterRejectsEmptyFilterValues(t *testing.T) {
	page, err := domain.ParsePage("", "")
	if err != nil {
		t.Fatal(err)
	}
	var noSymbol domain.Symbol
	_, err = domain.NewTransactionFilter(page, &noSymbol, nil, nil, nil)
	requireCode(t, err, domain.CodeInternal)
	noSide := domain.Side("")
	_, err = domain.NewTransactionFilter(page, nil, &noSide, nil, nil)
	requireCode(t, err, domain.CodeInternal)
	short := domain.Side("short")
	_, err = domain.NewTransactionFilter(page, nil, &short, nil, nil)
	requireCode(t, err, domain.CodeInternal)
}
