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
	Provider   string // "google" for identity events
	Scope      domain.RateLimitScope
}

// auditMetadata is the allowlist of metadata (AGENTS.md): no raw email, no full IP.
type auditMetadata struct {
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	Method    string `json:"method,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Scope     string `json:"scope,omitempty"`
}

func (a *Auth) newAuditEvent(meta domain.RequestMeta, s auditSpec) (domain.AuditEvent, error) {
	return newAuditEvent(a.clock, meta, s, auditMetadata{
		IP: meta.MaskedIP, UserAgent: meta.UserAgent, Method: s.Method, Provider: s.Provider, Scope: string(s.Scope),
	})
}

// newAuditEvent builds an event from the spec and the metadata its use case allows; metadata must
// be a struct of allowlisted fields so nothing outside the allowlist can reach the table.
func newAuditEvent(clock port.Clock, meta domain.RequestMeta, s auditSpec, metadata any) (domain.AuditEvent, error) {
	id, err := domain.NewID()
	if err != nil {
		return domain.AuditEvent{}, err
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("encode audit metadata: %w", err)
	}
	return domain.NewAuditEvent(domain.AuditEvent{
		ID: id, OccurredAt: NowForStorage(clock), EventType: s.Type, ActorUserID: s.Actor,
		RequestID: meta.RequestID, SessionID: s.SessionID, TargetType: s.TargetType, TargetID: s.TargetID,
		Outcome: s.Outcome, ReasonCode: s.Reason, Metadata: encoded,
	})
}

// appendAuditEvent writes e in tx, the transaction of the change it describes.
func appendAuditEvent(ctx context.Context, tx port.TxRepositories, e domain.AuditEvent) error {
	if err := tx.Audit().Append(ctx, e); err != nil {
		return fmt.Errorf("%w: %w", errAuditWriteFailed, err)
	}
	return nil
}

// appendAudit writes the event in tx, the transaction of the change it describes.
func (a *Auth) appendAudit(ctx context.Context, tx port.TxRepositories, meta domain.RequestMeta, s auditSpec) error {
	e, err := a.newAuditEvent(meta, s)
	if err != nil {
		return err
	}
	return appendAuditEvent(ctx, tx, e)
}

// recordFailure writes the event of a failed or refused attempt in a transaction of its own: there
// is no business change to commit it with, and it must survive the error the caller returns.
func (a *Auth) recordFailure(ctx context.Context, meta domain.RequestMeta, s auditSpec) error {
	return commitFailureAudit(ctx, a.uow, a.log, meta, s.Type, func() (domain.AuditEvent, error) {
		return a.newAuditEvent(meta, s)
	})
}

// commitFailureAudit stores the event build returns in a transaction of its own and logs, without
// request data, an event that could not be stored. The error is returned for callers that must act
// on it; callers that must not change their answer may ignore it, the log line is already written.
func commitFailureAudit(ctx context.Context, uow port.UnitOfWork, log *slog.Logger, meta domain.RequestMeta, eventType domain.EventType, build func() (domain.AuditEvent, error)) error {
	err := uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		e, err := build()
		if err != nil {
			return err
		}
		return appendAuditEvent(ctx, tx, e)
	})
	if err != nil {
		log.Error("could not record an audit event",
			slog.String("event", "audit.write_failed"),
			slog.String("error_code", string(domain.CodeAuditWriteFailed)),
			slog.String("error", err.Error()),
			slog.String("request_id", meta.RequestID.String()),
			slog.String("event_type", string(eventType)))
	}
	return err
}
