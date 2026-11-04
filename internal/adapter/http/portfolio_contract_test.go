package http

import (
	"slices"
	"strconv"
	"testing"
)

// documentedPortfolioOperations lists every portfolio operation with the statuses its handler can
// return, like documentedAuthOperations for the auth endpoints.
var documentedPortfolioOperations = []struct {
	method, path string
	statuses     []int
}{
	{"post", "/api/v1/portfolios", []int{201, 400, 401, 403, 409, 500, 503}},
	{"get", "/api/v1/portfolios", []int{200, 400, 401, 500, 503}},
	{"get", "/api/v1/portfolios/{portfolio_id}", []int{200, 400, 401, 404, 500, 503}},
	{"put", "/api/v1/portfolios/{portfolio_id}", []int{200, 400, 401, 403, 404, 409, 412, 428, 500, 503}},
	{"delete", "/api/v1/portfolios/{portfolio_id}", []int{204, 400, 401, 403, 404, 409, 412, 428, 500, 503}},
}

// documentedTransactionOperations lists the transaction operations with the statuses their handlers
// can return.
var documentedTransactionOperations = []struct {
	method, path string
	statuses     []int
}{
	{"post", "/api/v1/portfolios/{portfolio_id}/transactions", []int{201, 400, 401, 403, 404, 409, 422, 429, 500, 503}},
	{"get", "/api/v1/portfolios/{portfolio_id}/transactions", []int{200, 400, 401, 404, 500, 503}},
	{"get", "/api/v1/portfolios/{portfolio_id}/transactions/{transaction_id}", []int{200, 400, 401, 404, 500, 503}},
	{"put", "/api/v1/portfolios/{portfolio_id}/transactions/{transaction_id}", []int{200, 400, 401, 403, 404, 412, 422, 428, 429, 500, 503}},
	{"delete", "/api/v1/portfolios/{portfolio_id}/transactions/{transaction_id}", []int{204, 400, 401, 403, 404, 412, 422, 428, 500, 503}},
}

// documentedValuationOperations lists the holdings and valuation operations with the statuses their
// handlers can return.
var documentedValuationOperations = []struct {
	method, path string
	statuses     []int
}{
	{"get", "/api/v1/portfolios/{portfolio_id}/holdings", []int{200, 400, 401, 404, 500, 503}},
	{"get", "/api/v1/portfolios/{portfolio_id}/valuation", []int{200, 400, 401, 404, 500, 503}},
}

func TestPortfolioOperationsAreDocumented(t *testing.T) {
	spec := loadSpec(t)
	for _, op := range slices.Concat(documentedPortfolioOperations, documentedTransactionOperations, documentedValuationOperations) {
		declared, ok := spec.Paths[op.path][op.method]
		if !ok {
			t.Errorf("%s %s is not in swagger.json", op.method, op.path)
			continue
		}
		for _, status := range op.statuses {
			if _, ok := declared.Responses[strconv.Itoa(status)]; !ok {
				t.Errorf("%s %s does not declare status %d", op.method, op.path, status)
			}
		}
	}
}

var requiredPortfolioFields = map[string][]string{
	"http.PortfolioRequest":       {"name"},
	"http.UpdatePortfolioRequest": {"description", "name"},
	"http.PortfolioResponse":      {"created_at", "currency", "description", "id", "name", "updated_at", "version"},
	"http.PortfolioPageResponse":  {"data", "next_cursor"},

	"http.TransactionRequest":      {"executed_at", "fee", "price", "quantity", "side", "symbol"},
	"http.TransactionResponse":     {"created_at", "executed_at", "fee", "id", "portfolio_id", "price", "quantity", "sequence", "side", "symbol", "updated_at", "version"},
	"http.TransactionPageResponse": {"data", "next_cursor"},

	"http.HoldingResponse":   {"average_cost", "cost_basis", "instrument_name", "quantity", "realized_pnl", "symbol"},
	"http.HoldingsResponse":  {"data"},
	"http.QuoteResponse":     {"as_of", "cached", "currency", "feed", "fetched_at", "is_demo", "price", "session_status", "source", "stale"},
	"http.PositionResponse":  {"average_cost", "cost_basis", "instrument_name", "market_value", "quantity", "quote", "realized_pnl", "symbol", "unavailable_reason", "unrealized_pnl"},
	"http.ValuationResponse": {"calculated_at", "coverage", "currency", "market_value", "missing_symbols", "portfolio_id", "positions", "priced_market_value", "realized_pnl", "unrealized_pnl"},
}

func TestPortfolioSchemasDeclareRequiredFieldsAndTimestampFormats(t *testing.T) {
	spec := loadSpec(t)
	for name, want := range requiredPortfolioFields {
		def, ok := spec.Definitions[name]
		if !ok {
			t.Errorf("%s is not in swagger.json", name)
			continue
		}
		got := slices.Clone(def.Required)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s required = %v, want %v", name, got, want)
		}
	}
	for _, field := range []string{"created_at", "updated_at"} {
		if got := spec.Definitions["http.PortfolioResponse"].Properties[field].Format; got != "date-time" {
			t.Errorf("http.PortfolioResponse.%s format = %q, want date-time", field, got)
		}
	}
	for _, field := range []string{"created_at", "updated_at", "executed_at"} {
		if got := spec.Definitions["http.TransactionResponse"].Properties[field].Format; got != "date-time" {
			t.Errorf("http.TransactionResponse.%s format = %q, want date-time", field, got)
		}
	}
}

// A price that is missing is null, never "0": the schema must say so for every field that can be null.
func TestValuationDeclaresWhichFieldsCanBeNull(t *testing.T) {
	spec := loadSpec(t)
	for definition, fields := range map[string][]string{
		"http.ValuationResponse": {"market_value", "unrealized_pnl"},
		"http.PositionResponse":  {"market_value", "unrealized_pnl", "quote", "unavailable_reason"},
	} {
		for _, field := range fields {
			if !spec.Definitions[definition].Properties[field].Nullable {
				t.Errorf("%s.%s is not marked x-nullable although it is null when there is no price", definition, field)
			}
		}
	}
	for _, field := range []string{"calculated_at"} {
		if got := spec.Definitions["http.ValuationResponse"].Properties[field].Format; got != "date-time" {
			t.Errorf("http.ValuationResponse.%s format = %q, want date-time", field, got)
		}
	}
	for _, field := range []string{"as_of", "fetched_at"} {
		if got := spec.Definitions["http.QuoteResponse"].Properties[field].Format; got != "date-time" {
			t.Errorf("http.QuoteResponse.%s format = %q, want date-time", field, got)
		}
	}
}
