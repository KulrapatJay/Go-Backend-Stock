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
	Limits     *RateLimitPolicy
	Logger     *slog.Logger
}

// Portfolios is the use cases of a user's portfolios. Every method works on the caller's own
// portfolios only: another user's portfolio is reported exactly like a missing one.
type Portfolios struct {
	store   port.Repositories
	uow     port.UnitOfWork
	clock   port.Clock
	denials accessDenials
}

var _ port.PortfolioService = (*Portfolios)(nil)

// NewPortfolios checks that every dependency is present.
func NewPortfolios(d PortfolioDeps) (*Portfolios, error) {
	if d.Store == nil || d.UnitOfWork == nil || d.Clock == nil || d.Limits == nil || d.Logger == nil {
		return nil, errors.New("portfolio service is missing a dependency")
	}
	return &Portfolios{
		store: d.Store, uow: d.UnitOfWork, clock: d.Clock,
		denials: accessDenials{store: d.Store, uow: d.UnitOfWork, clock: d.Clock, limits: d.Limits, log: d.Logger},
	}, nil
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

// denyIfForeign records the access denial when the not-found of a write was really a portfolio of
// another owner, and returns err unchanged. The answer never depends on that bookkeeping: if the
// lookup, the limiter or the audit write fails, the failure is logged with the request ID and the
// caller still gets not_found, so a foreign ID cannot be told from an unknown one by a 500. The
// response time can still differ by the extra queries; IDs are random, so this only confirms an ID
// the caller already holds. Any other error passes through.
func (s *Portfolios) denyIfForeign(ctx context.Context, err error, p domain.Principal, id domain.ID, meta domain.RequestMeta) error {
	if errors.Is(err, domain.ErrNotFound) {
		s.denials.record(ctx, p, id, meta)
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
