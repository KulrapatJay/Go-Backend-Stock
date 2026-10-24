package domain

import "time"

const (
	maxInstrumentNameRunes     = 200
	maxInstrumentExchangeRunes = 20
	maxProviderAssetIDRunes    = 64
)

// AssetClass is the instrument asset class (instruments.asset_class CHECK). Phase 1: US equities.
type AssetClass string

const AssetClassUSEquity AssetClass = "us_equity"

// ParseAssetClass accepts exactly "us_equity".
func ParseAssetClass(s string) (AssetClass, error) {
	return parseEnum("asset_class", s, AssetClassUSEquity)
}

// InstrumentStatus says whether the provider still lists the instrument as tradable.
type InstrumentStatus string

const (
	InstrumentStatusActive   InstrumentStatus = "active"
	InstrumentStatusInactive InstrumentStatus = "inactive"
)

// ParseInstrumentStatus accepts exactly "active" or "inactive".
func ParseInstrumentStatus(s string) (InstrumentStatus, error) {
	return parseEnum("status", s, InstrumentStatusActive, InstrumentStatusInactive)
}

// InstrumentSource is where the instrument was verified. demo rows exist only in dev and test.
type InstrumentSource string

const (
	InstrumentSourceAlpaca InstrumentSource = "alpaca"
	InstrumentSourceDemo   InstrumentSource = "demo"
)

// ParseInstrumentSource accepts exactly "alpaca" or "demo".
func ParseInstrumentSource(s string) (InstrumentSource, error) {
	return parseEnum("source", s, InstrumentSourceAlpaca, InstrumentSourceDemo)
}

// Instrument is a verified stock shared by all users. Transactions reference its ID, not its
// symbol, because symbols change (FB -> META) and can be reused.
type Instrument struct {
	ID              ID
	Symbol          Symbol
	ProviderAssetID string // required when Source is alpaca; "" otherwise allowed
	Name            string
	Exchange        string
	AssetClass      AssetClass
	Status          InstrumentStatus
	Source          InstrumentSource
	VerifiedAt      time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// NewInstrument checks an instrument built from a provider lookup before it is stored.
func NewInstrument(i Instrument) (Instrument, error) {
	err := requireID("instrument.id", i.ID)
	switch {
	case err != nil:
	case i.Symbol.IsZero():
		err = invariant("instrument.symbol", "is required")
	case !isText(i.Name, 1, maxInstrumentNameRunes):
		err = invariant("instrument.name", "must be 1-200 printable characters")
	case !isText(i.Exchange, 1, maxInstrumentExchangeRunes):
		err = invariant("instrument.exchange", "must be 1-20 printable characters")
	case !isOneOf(i.AssetClass, AssetClassUSEquity):
		err = invariant("instrument.asset_class", "must be us_equity")
	case !isOneOf(i.Status, InstrumentStatusActive, InstrumentStatusInactive):
		err = invariant("instrument.status", "must be active or inactive")
	case !isOneOf(i.Source, InstrumentSourceAlpaca, InstrumentSourceDemo):
		err = invariant("instrument.source", "must be alpaca or demo")
	case i.Source == InstrumentSourceAlpaca && i.ProviderAssetID == "":
		err = invariant("instrument.provider_asset_id", "is required for alpaca instruments")
	case i.ProviderAssetID != "" && !isText(i.ProviderAssetID, 1, maxProviderAssetIDRunes):
		err = invariant("instrument.provider_asset_id", "must be 1-64 printable characters")
	case i.VerifiedAt.IsZero():
		err = invariant("instrument.verified_at", "is required")
	}
	if err != nil {
		return Instrument{}, err
	}
	return i, nil
}
