package postgres

import (
	"context"
	"errors"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// errSymbolChanged answers a provider that reports a known asset under a new symbol (FB -> META).
// Phase 1 does not support symbol changes (AGENTS.md); the user chose unknown_symbol (2026-10-21).
var errSymbolChanged = domain.NewError(domain.CodeUnknownSymbol,
	"symbol was renamed by the data provider; renamed symbols are not supported yet")

// InstrumentRepository stores verified instruments shared by every user.
type InstrumentRepository struct {
	q *sqlc.Queries
}

var _ port.InstrumentRepository = (*InstrumentRepository)(nil)

// BySymbol returns the instrument with this symbol, or domain.ErrNotFound.
func (r *InstrumentRepository) BySymbol(ctx context.Context, symbol domain.Symbol) (domain.Instrument, error) {
	row, err := r.q.GetInstrumentBySymbol(ctx, symbol.String())
	if err != nil {
		return domain.Instrument{}, mapError(err)
	}
	return instrumentFromRow(row)
}

// Upsert stores an instrument built with domain.NewInstrument and returns the stored row. When
// the symbol already exists (a concurrent request inserted it first) it returns that row as
// stored, even if its source or provider asset id differs: phase 1 does not handle ticker reuse
// or mixed demo/alpaca rows. When the provider asset is already stored under another symbol it
// returns unknown_symbol, because
// phase 1 cannot follow a symbol change. Either way the transaction stays usable.
func (r *InstrumentRepository) Upsert(ctx context.Context, i domain.Instrument) (domain.Instrument, error) {
	if _, err := r.q.InsertInstrument(ctx, sqlc.InsertInstrumentParams{
		ID:              pgUUID(i.ID),
		Symbol:          i.Symbol.String(),
		ProviderAssetID: pgText(i.ProviderAssetID),
		Name:            i.Name,
		Exchange:        i.Exchange,
		AssetClass:      string(i.AssetClass),
		Status:          string(i.Status),
		Source:          string(i.Source),
		VerifiedAt:      pgTime(i.VerifiedAt),
		CreatedAt:       pgTime(i.CreatedAt),
	}); err != nil {
		return domain.Instrument{}, mapError(err)
	}
	stored, err := r.BySymbol(ctx, i.Symbol)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Instrument{}, errSymbolChanged
	}
	return stored, err
}

func instrumentFromRow(row sqlc.AppInstrument) (domain.Instrument, error) {
	id, err := idFromPG("instrument.id", row.ID)
	if err != nil {
		return domain.Instrument{}, err
	}
	symbol, err := domain.ParseSymbol(row.Symbol)
	if err != nil {
		return domain.Instrument{}, fromStored(err)
	}
	assetClass, err := domain.ParseAssetClass(row.AssetClass)
	if err != nil {
		return domain.Instrument{}, fromStored(err)
	}
	status, err := domain.ParseInstrumentStatus(row.Status)
	if err != nil {
		return domain.Instrument{}, fromStored(err)
	}
	source, err := domain.ParseInstrumentSource(row.Source)
	if err != nil {
		return domain.Instrument{}, fromStored(err)
	}
	return domain.Instrument{
		ID: id, Symbol: symbol, ProviderAssetID: row.ProviderAssetID.String, Name: row.Name, Exchange: row.Exchange,
		AssetClass: assetClass, Status: status, Source: source,
		VerifiedAt: timeFromPG(row.VerifiedAt), CreatedAt: timeFromPG(row.CreatedAt), UpdatedAt: timeFromPG(row.UpdatedAt),
	}, nil
}
