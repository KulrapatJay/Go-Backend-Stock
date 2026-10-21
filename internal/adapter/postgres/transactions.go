package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// TransactionRepository stores trades. Every query checks the owner through portfolios.user_id
// and returns the instrument's current symbol.
type TransactionRepository struct {
	q *sqlc.Queries
}

var _ port.TransactionRepository = (*TransactionRepository)(nil)

// transactionRow has the columns every transaction query returns, in the same order, so each
// sqlc row type converts to it.
type transactionRow struct {
	ID           pgtype.UUID
	PortfolioID  pgtype.UUID
	Sequence     pgtype.Int8
	InstrumentID pgtype.UUID
	Symbol       string
	Side         string
	Quantity     pgtype.Numeric
	Price        pgtype.Numeric
	Fee          pgtype.Numeric
	ExecutedAt   pgtype.Timestamptz
	Version      int32
	CreatedAt    pgtype.Timestamptz
	UpdatedAt    pgtype.Timestamptz
}

// History returns every trade of the owner's portfolio ordered by (executed_at, sequence) for
// replay. A missing or foreign portfolio returns domain.ErrNotFound.
func (r *TransactionRepository) History(ctx context.Context, ownerID, portfolioID domain.ID) ([]domain.Transaction, error) {
	rows, err := r.q.OwnedTransactionHistory(ctx, sqlc.OwnedTransactionHistoryParams{OwnerID: pgUUID(ownerID), PortfolioID: pgUUID(portfolioID)})
	if err != nil {
		return nil, mapError(err)
	}
	if len(rows) == 0 {
		if err := r.requireOwnedPortfolio(ctx, ownerID, portfolioID); err != nil {
			return nil, err
		}
	}
	out := make([]domain.Transaction, 0, len(rows))
	for _, row := range rows {
		t, err := transactionFromRow(transactionRow(row))
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// List returns one page of the owner's portfolio transactions, newest first, narrowed by the
// filter. A cursor from another list or filter is invalid_input; a missing or foreign portfolio
// domain.ErrNotFound.
func (r *TransactionRepository) List(ctx context.Context, ownerID, portfolioID domain.ID, f domain.TransactionFilter) (domain.TransactionPage, error) {
	if f.Page.IsZero() {
		return domain.TransactionPage{}, domain.NewError(domain.CodeInternal, "transaction list needs a page")
	}
	fingerprint := transactionFilterFingerprint(f)
	params := sqlc.ListOwnedTransactionsParams{
		OwnerID:     pgUUID(ownerID),
		PortfolioID: pgUUID(portfolioID),
		FromTime:    pgOptionalTime(f.From),
		ToTime:      pgOptionalTime(f.To),
		RowLimit:    int32(f.Page.Limit() + 1), //nolint:gosec // Limit is at most domain.MaxPageLimit.
	}
	if f.Symbol != nil {
		params.Symbol = pgtype.Text{String: f.Symbol.String(), Valid: true}
	}
	if f.Side != nil {
		params.Side = pgtype.Text{String: string(*f.Side), Valid: true}
	}
	if f.Page.Cursor() != "" {
		pos, err := decodeCursor(f.Page.Cursor(), cursorKindTransactions, fingerprint)
		if err != nil {
			return domain.TransactionPage{}, err
		}
		if pos.Sequence < 1 {
			return domain.TransactionPage{}, errInvalidCursor
		}
		params.AfterExecutedAt = pgTime(pos.At)
		params.AfterSequence = pgtype.Int8{Int64: pos.Sequence, Valid: true}
	}
	rows, err := r.q.ListOwnedTransactions(ctx, params)
	if err != nil {
		return domain.TransactionPage{}, mapError(err)
	}
	if len(rows) == 0 {
		if err := r.requireOwnedPortfolio(ctx, ownerID, portfolioID); err != nil {
			return domain.TransactionPage{}, err
		}
	}
	var out domain.TransactionPage
	for i, row := range rows {
		if i == f.Page.Limit() {
			last := out.Items[len(out.Items)-1]
			out.NextCursor = encodeCursor(cursorPosition{
				Kind: cursorKindTransactions, Filter: fingerprint, At: last.ExecutedAt, Sequence: last.Sequence,
			})
			break
		}
		t, err := transactionFromRow(transactionRow(row))
		if err != nil {
			return domain.TransactionPage{}, err
		}
		out.Items = append(out.Items, t)
	}
	return out, nil
}

// Get returns one trade of the owner's portfolio, or domain.ErrNotFound.
func (r *TransactionRepository) Get(ctx context.Context, ownerID, portfolioID, transactionID domain.ID) (domain.Transaction, error) {
	row, err := r.q.GetOwnedTransaction(ctx, sqlc.GetOwnedTransactionParams{
		OwnerID: pgUUID(ownerID), PortfolioID: pgUUID(portfolioID), ID: pgUUID(transactionID),
	})
	if err != nil {
		return domain.Transaction{}, mapError(err)
	}
	return transactionFromRow(transactionRow(row))
}

// Insert stores a trade in the owner's portfolio at version 1 and returns it with the sequence
// the database assigned. A missing or foreign portfolio returns domain.ErrNotFound.
func (r *TransactionRepository) Insert(ctx context.Context, ownerID domain.ID, t domain.Transaction) (domain.Transaction, error) {
	quantity, price, fee, err := tradeNumerics(t)
	if err != nil {
		return domain.Transaction{}, err
	}
	row, err := r.q.InsertOwnedTransaction(ctx, sqlc.InsertOwnedTransactionParams{
		ID:           pgUUID(t.ID),
		InstrumentID: pgUUID(t.InstrumentID),
		Side:         string(t.Side),
		Quantity:     quantity,
		Price:        price,
		Fee:          fee,
		ExecutedAt:   pgTime(t.ExecutedAt),
		CreatedAt:    pgTime(t.CreatedAt),
		PortfolioID:  pgUUID(t.PortfolioID),
		OwnerID:      pgUUID(ownerID),
	})
	if err != nil {
		return domain.Transaction{}, mapError(err)
	}
	return transactionFromRow(transactionRow(row))
}

// Update writes the trade fields when the version matches and returns the row at version + 1.
// It matches on t.PortfolioID and never changes it. A stale version returns
// domain.ErrVersionConflict; a missing, foreign or other-portfolio trade domain.ErrNotFound.
func (r *TransactionRepository) Update(ctx context.Context, ownerID domain.ID, t domain.Transaction) (domain.Transaction, error) {
	quantity, price, fee, err := tradeNumerics(t)
	if err != nil {
		return domain.Transaction{}, err
	}
	row, err := r.q.UpdateOwnedTransaction(ctx, sqlc.UpdateOwnedTransactionParams{
		InstrumentID: pgUUID(t.InstrumentID),
		Side:         string(t.Side),
		Quantity:     quantity,
		Price:        price,
		Fee:          fee,
		ExecutedAt:   pgTime(t.ExecutedAt),
		UpdatedAt:    pgTime(t.UpdatedAt),
		ID:           pgUUID(t.ID),
		PortfolioID:  pgUUID(t.PortfolioID),
		Version:      t.Version.Int32(),
		OwnerID:      pgUUID(ownerID),
	})
	if err = mapError(err); errors.Is(err, domain.ErrNotFound) {
		exists, existsErr := r.q.OwnedTransactionExists(ctx, sqlc.OwnedTransactionExistsParams{
			OwnerID: pgUUID(ownerID), ID: pgUUID(t.ID), PortfolioID: pgUUID(t.PortfolioID),
		})
		switch {
		case existsErr != nil:
			return domain.Transaction{}, mapError(existsErr)
		case exists:
			return domain.Transaction{}, domain.ErrVersionConflict
		default:
			return domain.Transaction{}, domain.ErrNotFound
		}
	} else if err != nil {
		return domain.Transaction{}, err
	}
	return transactionFromRow(transactionRow(row))
}

// Delete removes one trade of the owner's portfolio, or returns domain.ErrNotFound. The service
// checks the version under the portfolio lock first.
func (r *TransactionRepository) Delete(ctx context.Context, ownerID, portfolioID, transactionID domain.ID) error {
	n, err := r.q.DeleteOwnedTransaction(ctx, sqlc.DeleteOwnedTransactionParams{
		ID: pgUUID(transactionID), PortfolioID: pgUUID(portfolioID), OwnerID: pgUUID(ownerID),
	})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// requireOwnedPortfolio tells an empty result from a portfolio that is missing or not the owner's.
func (r *TransactionRepository) requireOwnedPortfolio(ctx context.Context, ownerID, portfolioID domain.ID) error {
	exists, err := r.q.OwnedPortfolioExists(ctx, sqlc.OwnedPortfolioExistsParams{ID: pgUUID(portfolioID), UserID: pgUUID(ownerID)})
	if err != nil {
		return mapError(err)
	}
	if !exists {
		return domain.ErrNotFound
	}
	return nil
}

// tradeNumerics encodes the decimal fields. They come from Parse*, so numeric(28,10) stores them
// exactly; a value that bypassed Parse* could be rounded by PostgreSQL.
func tradeNumerics(t domain.Transaction) (quantity, price, fee pgtype.Numeric, err error) {
	if quantity, err = pgNumeric(t.Quantity.String()); err != nil {
		return
	}
	if price, err = pgNumeric(t.Price.String()); err != nil {
		return
	}
	fee, err = pgNumeric(t.Fee.String())
	return
}

func transactionFromRow(row transactionRow) (domain.Transaction, error) {
	id, err := idFromPG("transaction.id", row.ID)
	if err != nil {
		return domain.Transaction{}, err
	}
	portfolioID, err := idFromPG("transaction.portfolio_id", row.PortfolioID)
	if err != nil {
		return domain.Transaction{}, err
	}
	instrumentID, err := idFromPG("transaction.instrument_id", row.InstrumentID)
	if err != nil {
		return domain.Transaction{}, err
	}
	if !row.Sequence.Valid || row.Sequence.Int64 < 1 {
		return domain.Transaction{}, domain.NewError(domain.CodeInternal, "stored transaction.sequence is not positive")
	}
	symbol, err := domain.ParseSymbol(row.Symbol)
	if err != nil {
		return domain.Transaction{}, fromStored(err)
	}
	side, err := domain.ParseSide(row.Side)
	if err != nil {
		return domain.Transaction{}, fromStored(err)
	}
	quantity, err := storedDecimal("transaction.quantity", row.Quantity, domain.ParseQuantity)
	if err != nil {
		return domain.Transaction{}, err
	}
	price, err := storedDecimal("transaction.price", row.Price, domain.ParsePrice)
	if err != nil {
		return domain.Transaction{}, err
	}
	fee, err := storedDecimal("transaction.fee", row.Fee, domain.ParseFee)
	if err != nil {
		return domain.Transaction{}, err
	}
	version, err := domain.ParseVersion(int64(row.Version))
	if err != nil {
		return domain.Transaction{}, fromStored(err)
	}
	return domain.Transaction{
		ID: id, PortfolioID: portfolioID, InstrumentID: instrumentID, Symbol: symbol, Sequence: row.Sequence.Int64,
		Side: side, Quantity: quantity, Price: price, Fee: fee, ExecutedAt: timeFromPG(row.ExecutedAt), Version: version,
		CreatedAt: timeFromPG(row.CreatedAt), UpdatedAt: timeFromPG(row.UpdatedAt),
	}, nil
}

// storedDecimal reads a numeric column through the domain parser without float.
func storedDecimal[T any](field string, n pgtype.Numeric, parse func(string) (T, error)) (T, error) {
	var zero T
	s, err := numericString(field, n)
	if err != nil {
		return zero, err
	}
	v, err := parse(s)
	if err != nil {
		return zero, fromStored(err)
	}
	return v, nil
}

// transactionFilterFingerprint identifies the filters of a transaction list (not the page).
func transactionFilterFingerprint(f domain.TransactionFilter) string {
	h := sha256.New()
	write := func(s string) { h.Write([]byte(s)); h.Write([]byte{0}) }
	if f.Symbol != nil {
		write("symbol=" + f.Symbol.String())
	}
	if f.Side != nil {
		write("side=" + string(*f.Side))
	}
	if f.From != nil {
		write("from=" + f.From.UTC().Format(time.RFC3339Nano))
	}
	if f.To != nil {
		write("to=" + f.To.UTC().Format(time.RFC3339Nano))
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}
