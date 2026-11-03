package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// idempotencyTTL is how long an Idempotency-Key keeps its first result, counted from the first
// request and never extended by a retry (phase 1 spec section 9).
const idempotencyTTL = 24 * time.Hour

// TransactionDeps are what Transactions needs; every field is required.
type TransactionDeps struct {
	Store       port.Repositories
	UnitOfWork  port.UnitOfWork
	Clock       port.Clock
	Limits      *RateLimitPolicy
	Instruments port.InstrumentProvider
	Logger      *slog.Logger
}

// Transactions is the use cases of the trades recorded in a user's portfolios. Every change takes
// the portfolio row lock first, so changes inside one portfolio are serialized; the instrument
// provider is only ever called before that lock is taken. Another user's portfolio is reported
// exactly like a missing one.
type Transactions struct {
	store       port.Repositories
	uow         port.UnitOfWork
	clock       port.Clock
	limits      *RateLimitPolicy
	instruments port.InstrumentProvider
	log         *slog.Logger
	denials     accessDenials
}

// NewTransactions checks that every dependency is present.
func NewTransactions(d TransactionDeps) (*Transactions, error) {
	if d.Store == nil || d.UnitOfWork == nil || d.Clock == nil || d.Limits == nil || d.Instruments == nil || d.Logger == nil {
		return nil, errors.New("transaction service is missing a dependency")
	}
	return &Transactions{
		store: d.Store, uow: d.UnitOfWork, clock: d.Clock, limits: d.Limits, instruments: d.Instruments, log: d.Logger,
		denials: accessDenials{store: d.Store, uow: d.UnitOfWork, clock: d.Clock, limits: d.Limits, log: d.Logger},
	}, nil
}

// Create records a trade (phase 1 spec section 9). The symbol is resolved first, outside any
// transaction. Then, in one transaction: lock the portfolio, look up the key, store a newly
// verified instrument, insert the trade, replay the whole portfolio history, write the audit
// event and save the key with the stored row. A sale past the shares held at its point in the
// history rolls everything back with insufficient_shares.
func (s *Transactions) Create(ctx context.Context, p domain.Principal, portfolioID domain.ID, key string, in domain.TransactionInput, meta domain.RequestMeta) (domain.Transaction, error) {
	if p.IsZero() {
		return domain.Transaction{}, domain.ErrUnauthenticated
	}
	instrument, fresh, err := s.instrumentFor(ctx, p, portfolioID, in.Symbol, meta)
	if err != nil {
		return domain.Transaction{}, err
	}
	id, err := domain.NewID()
	if err != nil {
		return domain.Transaction{}, err
	}
	version, err := domain.ParseVersion(1)
	if err != nil {
		return domain.Transaction{}, err
	}
	scope := domain.IdempotencyScope{UserID: p.UserID, PortfolioID: portfolioID, Operation: domain.IdempotencyOperationCreateTransaction}
	hash := in.RequestHash()

	var result domain.Transaction
	err = s.inPortfolio(ctx, p, portfolioID, meta, func(tx port.TxRepositories) error {
		now := NowForStorage(s.clock)
		earlier, replay, err := s.earlierResult(ctx, tx, scope, key, hash, now)
		if err != nil {
			return err
		}
		if replay {
			result = earlier
			return nil
		}
		stored := instrument
		if fresh {
			// A concurrent request may have stored the symbol first: use the row that is stored.
			if stored, err = tx.Instruments().Upsert(ctx, instrument); err != nil {
				return err
			}
		}
		created, err := tx.Transactions().Insert(ctx, p.UserID, domain.Transaction{
			ID: id, PortfolioID: portfolioID, InstrumentID: stored.ID, Symbol: stored.Symbol,
			Side: in.Side, Quantity: in.Quantity, Price: in.Price, Fee: in.Fee, ExecutedAt: in.ExecutedAt,
			Version: version, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		record, err := createRecord(scope, key, hash, created, now)
		if err != nil {
			return err
		}
		history, err := tx.Transactions().History(ctx, p.UserID, portfolioID)
		if err != nil {
			return err
		}
		if _, err := domain.ReplayHistory(history); err != nil {
			return err
		}
		if err := s.appendAudit(ctx, tx, meta, domain.EventTransactionCreated, p, portfolioID, created.ID, nil, tradeFieldsOf(created)); err != nil {
			return err
		}
		// Get ran under the same lock, so Save cannot meet the key; if it does anyway, the
		// transaction is aborted and the error is returned as is.
		if err := tx.Idempotency().Save(ctx, record); err != nil {
			return err
		}
		result = created
		return nil
	})
	if err != nil {
		return domain.Transaction{}, err
	}
	return result, nil
}

// earlierResult looks up key under the portfolio lock. An unexpired record of the same request
// returns its stored transaction with replay true; of another request, idempotency_conflict. An
// expired record is deleted so the key can be used again.
func (s *Transactions) earlierResult(ctx context.Context, tx port.TxRepositories, scope domain.IdempotencyScope, key string, hash []byte, now time.Time) (domain.Transaction, bool, error) {
	rec, err := tx.Idempotency().Get(ctx, scope, key)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return domain.Transaction{}, false, nil
	case err != nil:
		return domain.Transaction{}, false, err
	case !now.Before(rec.ExpiresAt): // expires_at <= now, as DeleteExpired matches
		return domain.Transaction{}, false, tx.Idempotency().DeleteExpired(ctx, scope, key, now)
	case !bytes.Equal(rec.RequestHash, hash):
		return domain.Transaction{}, false, domain.ErrIdempotencyConflict
	}
	earlier, err := domain.ParseTransactionSnapshot(rec.Result)
	if err != nil {
		return domain.Transaction{}, false, err
	}
	return earlier, true, nil
}

// createRecord is the idempotency record of created, the row the database returned. The snapshot
// must parse back before anything commits: a stored result that cannot be read would turn every
// retry of a request that already succeeded into a 500.
func createRecord(scope domain.IdempotencyScope, key string, hash []byte, created domain.Transaction, now time.Time) (domain.IdempotencyRecord, error) {
	raw, err := domain.MarshalTransactionSnapshot(created)
	if err != nil {
		return domain.IdempotencyRecord{}, err
	}
	if _, err := domain.ParseTransactionSnapshot(raw); err != nil {
		return domain.IdempotencyRecord{}, fmt.Errorf("stored transaction cannot be replayed: %w", err)
	}
	return domain.NewIdempotencyRecord(domain.IdempotencyRecord{
		Scope: scope, Key: key, RequestHash: hash, Result: raw, CreatedAt: now, ExpiresAt: now.Add(idempotencyTTL),
	})
}

// inPortfolio runs fn in one transaction after taking the row lock of the caller's portfolio.
// Only a not-found from that lock is an access denial (the portfolio is missing or someone
// else's); it is recorded after the transaction has rolled back. A not-found from fn, such as a
// missing transaction of the caller's own portfolio, is returned without a denial.
func (s *Transactions) inPortfolio(ctx context.Context, p domain.Principal, portfolioID domain.ID, meta domain.RequestMeta, fn func(port.TxRepositories) error) error {
	var notOwned bool
	err := s.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		notOwned = false
		if _, err := tx.Portfolios().LockOwned(ctx, p.UserID, portfolioID); err != nil {
			notOwned = errors.Is(err, domain.ErrNotFound)
			return err
		}
		return fn(tx)
	})
	if err != nil && notOwned {
		s.denials.record(ctx, p, portfolioID, meta)
	}
	return err
}

// instrumentFor resolves symbol before any transaction, so the provider is never called under the
// portfolio lock. A stored instrument is returned at once. An unknown one is verified with the
// provider, but only after the caller is shown to own the portfolio and within the per-user lookup
// limit, so another user's portfolio ID cannot spend the provider quota. fresh is true when the
// instrument still has to be stored, in the transaction that records the trade.
func (s *Transactions) instrumentFor(ctx context.Context, p domain.Principal, portfolioID domain.ID, symbol domain.Symbol, meta domain.RequestMeta) (instrument domain.Instrument, fresh bool, err error) {
	stored, err := s.store.Instruments().BySymbol(ctx, symbol)
	if err == nil {
		return stored, false, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.Instrument{}, false, err
	}
	if _, err := s.store.Portfolios().GetOwned(ctx, p.UserID, portfolioID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			s.denials.record(ctx, p, portfolioID, meta)
		}
		return domain.Instrument{}, false, err
	}
	rules, err := s.limits.InstrumentLookup(p.UserID)
	if err != nil {
		return domain.Instrument{}, false, err
	}
	if err := admitRules(ctx, s.store.RateLimits(), s.clock, rules, func(d domain.RateLimitDecision) {
		// A lost marker is logged by commitFailureAudit; the client still gets its 429.
		_ = commitFailureAudit(ctx, s.uow, s.log, meta, domain.EventSecurityRateLimited, func() (domain.AuditEvent, error) {
			return newAuditEvent(s.clock, meta, rateLimitedSpec(p, d), securityMetadata{IP: meta.MaskedIP, UserAgent: meta.UserAgent, Scope: string(d.Scope)})
		})
	}); err != nil {
		return domain.Instrument{}, false, err
	}

	found, err := s.instruments.Lookup(ctx, symbol)
	switch {
	case errors.Is(err, domain.ErrUnknownSymbol):
		return domain.Instrument{}, false, err
	case err != nil:
		return domain.Instrument{}, false, fmt.Errorf("%w: %w", domain.ErrDependencyUnavailable, err)
	case found.AssetClass != domain.AssetClassUSEquity:
		return domain.Instrument{}, false, domain.ErrUnknownSymbol
	}
	if found.ID, err = domain.NewID(); err != nil {
		return domain.Instrument{}, false, err
	}
	now := NowForStorage(s.clock)
	found.VerifiedAt, found.CreatedAt, found.UpdatedAt = now, now, now
	if instrument, err = domain.NewInstrument(found); err != nil {
		return domain.Instrument{}, false, err
	}
	return instrument, true, nil
}
