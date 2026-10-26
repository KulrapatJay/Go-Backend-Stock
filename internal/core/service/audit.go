package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// errAuditWriteFailed reports an audit event that could not be stored. A business change never
// commits without its event (AGENTS.md), so the caller's transaction rolls back.
var errAuditWriteFailed = domain.NewError(domain.CodeAuditWriteFailed, "could not record the audit event")

// auditSpec is what a use case says about one event; newAuditEvent adds the time, IDs and metadata.
type auditSpec struct {
	Type       domain.EventType
	Outcome    domain.AuditOutcome
	Actor      domain.ID // only an identity the request proved; zero for failed and anonymous attempts
	SessionID  domain.ID
	TargetType string
	TargetID   string
	Reason     string
	Method     string // "password" or "google" for login events
	Scope      domain.RateLimitScope
}

// auditMetadata is the allowlist of metadata (AGENTS.md): no raw email, no full IP.
type auditMetadata struct {
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	Method    string `json:"method,omitempty"`
	Scope     string `json:"scope,omitempty"`
}

func (a *Auth) newAuditEvent(meta domain.RequestMeta, s auditSpec) (domain.AuditEvent, error) {
	id, err := domain.NewID()
	if err != nil {
		return domain.AuditEvent{}, err
	}
	metadata, err := json.Marshal(auditMetadata{IP: meta.MaskedIP, UserAgent: meta.UserAgent, Method: s.Method, Scope: string(s.Scope)})
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("encode audit metadata: %w", err)
	}
	return domain.NewAuditEvent(domain.AuditEvent{
		ID: id, OccurredAt: NowForStorage(a.clock), EventType: s.Type, ActorUserID: s.Actor,
		RequestID: meta.RequestID, SessionID: s.SessionID, TargetType: s.TargetType, TargetID: s.TargetID,
		Outcome: s.Outcome, ReasonCode: s.Reason, Metadata: metadata,
	})
}

// appendAudit writes the event in tx, the transaction of the change it describes.
func (a *Auth) appendAudit(ctx context.Context, tx port.TxRepositories, meta domain.RequestMeta, s auditSpec) error {
	e, err := a.newAuditEvent(meta, s)
	if err != nil {
		return err
	}
	if err := tx.Audit().Append(ctx, e); err != nil {
		return fmt.Errorf("%w: %w", errAuditWriteFailed, err)
	}
	return nil
}

// recordFailure writes the event of a failed or refused attempt in a transaction of its own: there
// is no business change to commit it with, and it must survive the error the caller returns.
func (a *Auth) recordFailure(ctx context.Context, meta domain.RequestMeta, s auditSpec) error {
	err := a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		return a.appendAudit(ctx, tx, meta, s)
	})
	if err != nil {
		a.log.Error("could not record an audit event",
			slog.String("event", "audit.write_failed"),
			slog.String("error_code", string(domain.CodeAuditWriteFailed)),
			slog.String("error", err.Error()),
			slog.String("request_id", meta.RequestID.String()),
			slog.String("event_type", string(s.Type)))
	}
	return err
}
