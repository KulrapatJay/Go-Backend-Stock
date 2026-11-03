package service

import (
	"context"
	"log/slog"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// securityMetadata is the allowlist of metadata of security events: where the request came from and,
// for security.rate_limited, which limit it hit.
type securityMetadata struct {
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	Scope     string `json:"scope,omitempty"`
}

// rateLimitedSpec is the marker event that replaces further audit rows of a flood: one per key per
// minute, for the user the session proved.
func rateLimitedSpec(p domain.Principal, d domain.RateLimitDecision) auditSpec {
	return auditSpec{
		Type: domain.EventSecurityRateLimited, Outcome: domain.AuditOutcomeDenied, Actor: p.UserID, SessionID: p.SessionID,
		TargetType: "rate_limit", TargetID: string(d.Scope), Scope: d.Scope,
	}
}

// accessDenials audits refused access to a portfolio of another owner. It is shared by the use cases
// that take a portfolio ID from the client.
type accessDenials struct {
	store  port.Repositories
	uow    port.UnitOfWork
	clock  port.Clock
	limits *RateLimitPolicy
	log    *slog.Logger
}

// record audits a refused access to someone else's portfolio, at most accessDeniedPerHourUser
// events per user per hour. Past that limit the first refusal each minute is audited as
// security.rate_limited instead, so a flood stays visible without filling the table. An ID that
// belongs to nobody is not audited. The answer never depends on this bookkeeping: if the lookup,
// the limiter or the audit write fails, the failure is logged with the request ID and the caller
// still gets not_found, so a foreign ID cannot be told from an unknown one by a 500.
func (d accessDenials) record(ctx context.Context, p domain.Principal, portfolioID domain.ID, meta domain.RequestMeta) {
	exists, err := d.store.Portfolios().Exists(ctx, portfolioID)
	if err != nil {
		d.logFailure("could not check who owns the portfolio", err, meta)
		return
	}
	if !exists {
		return
	}
	rules, err := d.limits.AccessDenied(p.UserID)
	if err != nil {
		d.logFailure("could not build the access denial limit", err, meta)
		return
	}
	decision, err := d.store.RateLimits().Admit(ctx, rules, NowForStorage(d.clock))
	if err != nil {
		d.logFailure("could not check the access denial limit", err, meta)
		return
	}
	spec := portfolioSpec(domain.EventSecurityAccessDenied, p, portfolioID)
	spec.Outcome, spec.Reason = domain.AuditOutcomeDenied, "not_owner"
	switch {
	case decision.Allowed:
	case decision.RecordDenialAudit:
		spec = rateLimitedSpec(p, decision)
	default:
		return
	}
	// commitFailureAudit logs a lost event; the answer to the caller does not change.
	_ = commitFailureAudit(ctx, d.uow, d.log, meta, spec.Type, func() (domain.AuditEvent, error) {
		return newAuditEvent(d.clock, meta, spec, securityMetadata{IP: meta.MaskedIP, UserAgent: meta.UserAgent, Scope: string(spec.Scope)})
	})
}

func (d accessDenials) logFailure(msg string, err error, meta domain.RequestMeta) {
	d.log.Error(msg,
		slog.String("event", "portfolio.denial_bookkeeping_failed"),
		slog.String("error", err.Error()),
		slog.String("request_id", meta.RequestID.String()))
}
