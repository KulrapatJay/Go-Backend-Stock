-- +goose Up
-- Daily bars of a stock, unadjusted (ADR-0024). A copy of market data, not business data: the runtime role
-- may add rows but never change or delete them. source is part of the key so sample bars made in demo mode
-- are never served as real prices after a switch to Alpaca. bar_date is the market date in New York.
CREATE TABLE app.price_bars (
    instrument_id uuid           NOT NULL,
    source        text           NOT NULL,
    bar_date      date           NOT NULL,
    open          numeric(28,10) NOT NULL,
    high          numeric(28,10) NOT NULL,
    low           numeric(28,10) NOT NULL,
    close         numeric(28,10) NOT NULL,
    volume        numeric(28,0)  NOT NULL,
    fetched_at    timestamptz    NOT NULL,
    CONSTRAINT price_bars_pkey PRIMARY KEY (instrument_id, source, bar_date),
    CONSTRAINT price_bars_instrument_id_fkey FOREIGN KEY (instrument_id) REFERENCES app.instruments (id) ON DELETE RESTRICT,
    CONSTRAINT price_bars_source_allowed CHECK (source IN ('alpaca', 'demo')),
    -- '< Infinity' also rejects NaN, which PostgreSQL sorts above every number.
    CONSTRAINT price_bars_open_valid CHECK (open > 0 AND open < 'Infinity'::numeric),
    CONSTRAINT price_bars_high_valid CHECK (high > 0 AND high < 'Infinity'::numeric),
    CONSTRAINT price_bars_low_valid CHECK (low > 0 AND low < 'Infinity'::numeric),
    CONSTRAINT price_bars_close_valid CHECK (close > 0 AND close < 'Infinity'::numeric),
    CONSTRAINT price_bars_high_not_below_low CHECK (high >= low),
    CONSTRAINT price_bars_volume_valid CHECK (volume >= 0 AND volume < 'Infinity'::numeric),
    CONSTRAINT price_bars_bar_date_min CHECK (bar_date >= DATE '1970-01-01')
);

-- The one unbroken range of dates the provider has been asked about for a stock and source. A date inside it
-- with no bar was a day without trading, not a day never fetched; without this row the two look the same.
CREATE TABLE app.price_bar_coverage (
    instrument_id   uuid        NOT NULL,
    source          text        NOT NULL,
    covered_from    date        NOT NULL,
    covered_through date        NOT NULL,
    updated_at      timestamptz NOT NULL,
    CONSTRAINT price_bar_coverage_pkey PRIMARY KEY (instrument_id, source),
    CONSTRAINT price_bar_coverage_instrument_id_fkey FOREIGN KEY (instrument_id) REFERENCES app.instruments (id) ON DELETE RESTRICT,
    CONSTRAINT price_bar_coverage_source_allowed CHECK (source IN ('alpaca', 'demo')),
    CONSTRAINT price_bar_coverage_range_valid CHECK (covered_from <= covered_through),
    CONSTRAINT price_bar_coverage_from_min CHECK (covered_from >= DATE '1970-01-01')
);

GRANT SELECT, INSERT ON app.price_bars TO app_runtime;
GRANT SELECT, INSERT, UPDATE ON app.price_bar_coverage TO app_runtime;

-- +goose Down
DROP TABLE app.price_bar_coverage;
DROP TABLE app.price_bars;
