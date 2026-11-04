package service

import (
	"context"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

const transactionTarget = "transaction"

// tradeFields is the allowlist of trade fields an audit event may carry (AGENTS.md: before/after
// of the business fields only). Decimals and the time are strings, as in the API.
type tradeFields struct {
	Symbol     string `json:"symbol"`
	Side       string `json:"side"`
	Quantity   string `json:"quantity"`
	Price      string `json:"price"`
	Fee        string `json:"fee"`
	ExecutedAt string `json:"executed_at"`
}

// transactionMetadata is the allowlist of transaction audit metadata: after for created, before
// and after for updated, before for deleted.
type transactionMetadata struct {
	PortfolioID string       `json:"portfolio_id"`
	IP          string       `json:"ip,omitempty"`
	UserAgent   string       `json:"user_agent,omitempty"`
	Before      *tradeFields `json:"before,omitempty"`
	After       *tradeFields `json:"after,omitempty"`
}

// tradeFieldsOf takes the audited fields of a stored transaction; Symbol is the instrument's
// current symbol.
func tradeFieldsOf(t domain.Transaction) *tradeFields {
	return &tradeFields{
		Symbol: t.Symbol.String(), Side: string(t.Side), Quantity: t.Quantity.String(), Price: t.Price.String(),
		Fee: t.Fee.String(), ExecutedAt: t.ExecutedAt.UTC().Format(time.RFC3339Nano),
	}
}

// appendAudit writes the event of a change to transactionID in tx, the transaction of the change.
func (s *Transactions) appendAudit(ctx context.Context, tx port.TxRepositories, meta domain.RequestMeta, eventType domain.EventType, p domain.Principal, portfolioID, transactionID domain.ID, before, after *tradeFields) error {
	spec := auditSpec{
		Type: eventType, Outcome: domain.AuditOutcomeSuccess, Actor: p.UserID, SessionID: p.SessionID,
		TargetType: transactionTarget, TargetID: transactionID.String(),
	}
	e, err := newAuditEvent(s.clock, meta, spec, transactionMetadata{
		PortfolioID: portfolioID.String(), IP: meta.MaskedIP, UserAgent: meta.UserAgent, Before: before, After: after,
	})
	if err != nil {
		return err
	}
	return appendAuditEvent(ctx, tx, e)
}
