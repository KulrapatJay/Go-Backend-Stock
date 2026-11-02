package service

import (
	"context"
	"errors"
	"log/slog"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// PortfolioDeps are what Portfolios needs; every field is required.
type PortfolioDeps struct {
	Store      port.Repositories
	UnitOfWork port.UnitOfWork
	Clock      port.Clock
	Logger     *slog.Logger
}

// Portfolios is the use cases of a user's portfolios. Every method works on the caller's own
// portfolios only: another user's portfolio is reported exactly like a missing one.
type Portfolios struct {
	store port.Repositories
	uow   port.UnitOfWork
	clock port.Clock
	log   *slog.Logger
}

var _ port.PortfolioService = (*Portfolios)(nil)

// NewPortfolios checks that every dependency is present.
func NewPortfolios(d PortfolioDeps) (*Portfolios, error) {
	if d.Store == nil || d.UnitOfWork == nil || d.Clock == nil || d.Logger == nil {
		return nil, errors.New("portfolio service is missing a dependency")
	}
	return &Portfolios{store: d.Store, uow: d.UnitOfWork, clock: d.Clock, log: d.Logger}, nil
}

// portfolioMetadata is the allowlist of portfolio audit metadata. Changed names the fields whose
// value changed, never the values: names and descriptions are free text from the user.
type portfolioMetadata struct {
	IP        string   `json:"ip,omitempty"`
	UserAgent string   `json:"user_agent,omitempty"`
	Changed   []string `json:"changed,omitempty"`
}

const portfolioTarget = "portfolio"

// Create adds a portfolio and its audit event in one transaction.
func (s *Portfolios) Create(ctx context.Context, p domain.Principal, in domain.PortfolioInput, meta domain.RequestMeta) (domain.Portfolio, error) {
	if p.IsZero() {
		return domain.Portfolio{}, domain.ErrUnauthenticated
	}
	id, err := domain.NewID()
	if err != nil {
		return domain.Portfolio{}, err
	}
	version, err := domain.ParseVersion(1)
	if err != nil {
		return domain.Portfolio{}, err
	}
	now := NowForStorage(s.clock)
	created := domain.Portfolio{
		ID: id, UserID: p.UserID, Name: in.Name, Description: in.Description, Currency: domain.CurrencyUSD,
		Version: version, CreatedAt: now, UpdatedAt: now,
	}
	err = s.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		if err := tx.Portfolios().Insert(ctx, created); err != nil {
			return err
		}
		return s.appendAudit(ctx, tx, meta, portfolioSpec(domain.EventPortfolioCreated, p, id), nil)
	})
	if err != nil {
		return domain.Portfolio{}, err
	}
	return created, nil
}

// List returns one page of the caller's portfolios, newest first. Reading is not audited.
func (s *Portfolios) List(ctx context.Context, p domain.Principal, page domain.Page) (domain.PortfolioPage, error) {
	if p.IsZero() {
		return domain.PortfolioPage{}, domain.ErrUnauthenticated
	}
	return s.store.Portfolios().ListOwned(ctx, p.UserID, page)
}

// Get returns one of the caller's portfolios. Reading is not audited.
func (s *Portfolios) Get(ctx context.Context, p domain.Principal, id domain.ID) (domain.Portfolio, error) {
	if p.IsZero() {
		return domain.Portfolio{}, domain.ErrUnauthenticated
	}
	return s.store.Portfolios().GetOwned(ctx, p.UserID, id)
}

// Update takes the portfolio row lock, then checks the version the client read, so two updates
// from the same version cannot both succeed.
func (s *Portfolios) Update(ctx context.Context, p domain.Principal, id domain.ID, expected domain.Version, in domain.PortfolioInput, meta domain.RequestMeta) (domain.Portfolio, error) {
	if p.IsZero() {
		return domain.Portfolio{}, domain.ErrUnauthenticated
	}
	var updated domain.Portfolio
	err := s.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		locked, err := tx.Portfolios().LockOwned(ctx, p.UserID, id)
		if err != nil {
			return err
		}
		if locked.Version != expected {
			return domain.ErrVersionConflict
		}
		next := locked
		next.Name, next.Description, next.UpdatedAt = in.Name, in.Description, NowForStorage(s.clock)
		if updated, err = tx.Portfolios().UpdateOwned(ctx, next, p.UserID); err != nil {
			return err
		}
		return s.appendAudit(ctx, tx, meta, portfolioSpec(domain.EventPortfolioUpdated, p, id), changedFields(locked, in))
	})
	if err != nil {
		return domain.Portfolio{}, s.denyIfForeign(ctx, err, p, id, meta)
	}
	return updated, nil
}

// Delete removes an empty portfolio. Idempotency keys of the portfolio go first, in the same
// transaction: they reference it, and if the portfolio still has transactions the whole delete,
// keys included, rolls back.
func (s *Portfolios) Delete(ctx context.Context, p domain.Principal, id domain.ID, expected domain.Version, meta domain.RequestMeta) error {
	if p.IsZero() {
		return domain.ErrUnauthenticated
	}
	err := s.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		locked, err := tx.Portfolios().LockOwned(ctx, p.UserID, id)
		if err != nil {
			return err
		}
		if locked.Version != expected {
			return domain.ErrVersionConflict
		}
		if err := tx.Idempotency().DeleteForPortfolio(ctx, p.UserID, id); err != nil {
			return err
		}
		if err := tx.Portfolios().DeleteOwnedEmpty(ctx, p.UserID, id); err != nil {
			return err
		}
		return s.appendAudit(ctx, tx, meta, portfolioSpec(domain.EventPortfolioDeleted, p, id), nil)
	})
	if err != nil {
		return s.denyIfForeign(ctx, err, p, id, meta)
	}
	return nil
}

// denyIfForeign turns the not-found of a write into an audited access denial when the portfolio
// exists under another owner. The caller always gets not_found, so nothing tells a portfolio that
// belongs to someone else from one that was never there. Any other error passes through.
func (s *Portfolios) denyIfForeign(ctx context.Context, err error, p domain.Principal, id domain.ID, meta domain.RequestMeta) error {
	if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	exists, lookupErr := s.store.Portfolios().Exists(ctx, id)
	if lookupErr != nil {
		return lookupErr
	}
	if !exists {
		return err
	}
	spec := portfolioSpec(domain.EventSecurityAccessDenied, p, id)
	spec.Outcome, spec.Reason = domain.AuditOutcomeDenied, "not_owner"
	auditErr := s.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		return s.appendAudit(ctx, tx, meta, spec, nil)
	})
	if auditErr != nil {
		s.log.Error("could not record an audit event",
			slog.String("event", "audit.write_failed"),
			slog.String("error_code", string(domain.CodeAuditWriteFailed)),
			slog.String("error", auditErr.Error()),
			slog.String("request_id", meta.RequestID.String()),
			slog.String("event_type", string(spec.Type)))
		return auditErr
	}
	return err
}

func (s *Portfolios) appendAudit(ctx context.Context, tx port.TxRepositories, meta domain.RequestMeta, spec auditSpec, changed []string) error {
	e, err := newAuditEvent(s.clock, meta, spec, portfolioMetadata{IP: meta.MaskedIP, UserAgent: meta.UserAgent, Changed: changed})
	if err != nil {
		return err
	}
	return appendAuditEvent(ctx, tx, e)
}

func portfolioSpec(t domain.EventType, p domain.Principal, id domain.ID) auditSpec {
	return auditSpec{
		Type: t, Outcome: domain.AuditOutcomeSuccess, Actor: p.UserID, SessionID: p.SessionID,
		TargetType: portfolioTarget, TargetID: id.String(),
	}
}

// changedFields lists, in a fixed order, the fields whose value differs from the stored portfolio.
func changedFields(before domain.Portfolio, in domain.PortfolioInput) []string {
	var changed []string
	if before.Name != in.Name {
		changed = append(changed, "name")
	}
	if before.Description != in.Description {
		changed = append(changed, "description")
	}
	return changed
}
