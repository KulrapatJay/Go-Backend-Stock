package http

import (
	"strconv"
	"time"

	"go-backend-stock/internal/core/domain"
)

// TransactionRequest is the body of POST /api/v1/portfolios/{portfolio_id}/transactions: one trade
// the user made elsewhere and records here. Every field is required. Quantity, price and fee are
// decimal strings, never JSON numbers: a number would pass through a float. Quantity and price must
// be greater than zero, fee may be "0"; each has at most 10 decimals and 18 digits before the point.
type TransactionRequest struct {
	Symbol     string `json:"symbol" example:"AAPL" minLength:"1" maxLength:"15" validate:"required"`
	Side       string `json:"side" enums:"buy,sell" example:"buy" validate:"required"`
	Quantity   string `json:"quantity" example:"0.5" validate:"required"`
	Price      string `json:"price" example:"200.25" validate:"required"`
	Fee        string `json:"fee" example:"1" validate:"required"`
	ExecutedAt string `json:"executed_at" format:"date-time" example:"2026-10-01T14:30:00Z" validate:"required"`
}

// TransactionResponse is one recorded trade. Version is the number to send back in If-Match.
// Sequence is a decimal integer string the database assigns: it never changes and orders trades
// that were executed at the same time. Numbers are canonical decimal strings without trailing zeros.
type TransactionResponse struct {
	ID          string    `json:"id" example:"0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d" validate:"required"`
	PortfolioID string    `json:"portfolio_id" example:"0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5e" validate:"required"`
	Sequence    string    `json:"sequence" example:"42" validate:"required"`
	Symbol      string    `json:"symbol" example:"AAPL" validate:"required"`
	Side        string    `json:"side" enums:"buy,sell" example:"buy" validate:"required"`
	Quantity    string    `json:"quantity" example:"0.5" validate:"required"`
	Price       string    `json:"price" example:"200.25" validate:"required"`
	Fee         string    `json:"fee" example:"1" validate:"required"`
	ExecutedAt  time.Time `json:"executed_at" format:"date-time" example:"2026-10-01T14:30:00Z" validate:"required"`
	Version     int32     `json:"version" example:"1" validate:"required"`
	CreatedAt   time.Time `json:"created_at" format:"date-time" example:"2026-10-29T09:30:00Z" validate:"required"`
	UpdatedAt   time.Time `json:"updated_at" format:"date-time" example:"2026-10-29T09:30:00Z" validate:"required"`
}

// TransactionPageResponse is one page of trades, newest first. NextCursor is null on the last page.
type TransactionPageResponse struct {
	Data       []TransactionResponse `json:"data" validate:"required"`
	NextCursor *string               `json:"next_cursor" extensions:"x-nullable" example:"eyJrIjoidHJhbnNhY3Rpb25zIn0" validate:"required"`
}

func newTransactionResponse(t domain.Transaction) TransactionResponse {
	return TransactionResponse{
		ID: t.ID.String(), PortfolioID: t.PortfolioID.String(), Sequence: strconv.FormatInt(t.Sequence, 10),
		Symbol: t.Symbol.String(), Side: string(t.Side), Quantity: t.Quantity.String(), Price: t.Price.String(),
		Fee: t.Fee.String(), ExecutedAt: t.ExecutedAt.UTC(), Version: t.Version.Int32(),
		CreatedAt: t.CreatedAt.UTC(), UpdatedAt: t.UpdatedAt.UTC(),
	}
}

func newTransactionPageResponse(page domain.TransactionPage) TransactionPageResponse {
	out := TransactionPageResponse{Data: make([]TransactionResponse, 0, len(page.Items))}
	for _, t := range page.Items {
		out.Data = append(out.Data, newTransactionResponse(t))
	}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return out
}
