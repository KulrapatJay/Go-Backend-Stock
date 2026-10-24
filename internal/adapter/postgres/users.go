package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// UserRepository stores accounts. Only CredentialsByEmail reads password_hash.
type UserRepository struct {
	q    *sqlc.Queries
	inTx bool
}

var _ port.UserRepository = (*UserRepository)(nil)

// ByID returns the user, or domain.ErrNotFound.
func (r *UserRepository) ByID(ctx context.Context, id domain.ID) (domain.User, error) {
	row, err := r.q.GetUserByID(ctx, pgUUID(id))
	if err != nil {
		return domain.User{}, mapError(err)
	}
	return userFromColumns(row.ID, row.Email, row.EmailVerifiedAt, row.CreatedAt, row.UpdatedAt)
}

// ByEmail returns the user with exactly this (already normalized) email, or domain.ErrNotFound.
func (r *UserRepository) ByEmail(ctx context.Context, email domain.Email) (domain.User, error) {
	row, err := r.q.GetUserByEmail(ctx, email.String())
	if err != nil {
		return domain.User{}, mapError(err)
	}
	return userFromColumns(row.ID, row.Email, row.EmailVerifiedAt, row.CreatedAt, row.UpdatedAt)
}

// CredentialsByEmail returns the user and the password hash; the hash is "" for Google-only accounts.
func (r *UserRepository) CredentialsByEmail(ctx context.Context, email domain.Email) (domain.UserCredentials, error) {
	row, err := r.q.GetUserCredentialsByEmail(ctx, email.String())
	if err != nil {
		return domain.UserCredentials{}, mapError(err)
	}
	user, err := userFromColumns(row.ID, row.Email, row.EmailVerifiedAt, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return domain.UserCredentials{}, err
	}
	return domain.UserCredentials{User: user, PasswordHash: domain.PasswordHash(row.PasswordHash.String)}, nil
}

// Lock reads the user with FOR UPDATE; it only works inside a transaction.
func (r *UserRepository) Lock(ctx context.Context, id domain.ID) (domain.User, error) {
	if !r.inTx {
		return domain.User{}, errLockOutsideTx
	}
	row, err := r.q.LockUser(ctx, pgUUID(id))
	if err != nil {
		return domain.User{}, mapError(err)
	}
	return userFromColumns(row.ID, row.Email, row.EmailVerifiedAt, row.CreatedAt, row.UpdatedAt)
}

// Insert stores a new account without a password (SetPassword adds it in the same transaction).
// A taken email returns domain.ErrIdentityConflict and leaves the transaction usable.
func (r *UserRepository) Insert(ctx context.Context, u domain.User) error {
	n, err := r.q.InsertUser(ctx, sqlc.InsertUserParams{
		ID:              pgUUID(u.ID),
		Email:           u.Email.String(),
		EmailVerifiedAt: pgOptionalTime(u.EmailVerifiedAt),
		CreatedAt:       pgTime(u.CreatedAt),
		UpdatedAt:       pgTime(u.UpdatedAt),
	})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrIdentityConflict
	}
	return nil
}

// SetPassword stores a new password hash. An empty hash is a bug: Google-only accounts never get
// a password through this method.
func (r *UserRepository) SetPassword(ctx context.Context, id domain.ID, hash domain.PasswordHash, at time.Time) error {
	if hash == "" {
		return domain.NewError(domain.CodeInternal, "password hash is empty")
	}
	n, err := r.q.SetUserPassword(ctx, sqlc.SetUserPasswordParams{
		ID:           pgUUID(id),
		PasswordHash: pgtype.Text{String: string(hash), Valid: true},
		UpdatedAt:    pgTime(at),
	})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// MarkEmailVerified sets email_verified_at the first time; later calls keep the original time.
func (r *UserRepository) MarkEmailVerified(ctx context.Context, id domain.ID, at time.Time) error {
	n, err := r.q.MarkUserEmailVerified(ctx, sqlc.MarkUserEmailVerifiedParams{ID: pgUUID(id), VerifiedAt: pgTime(at)})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func userFromColumns(id pgtype.UUID, email string, verifiedAt, createdAt, updatedAt pgtype.Timestamptz) (domain.User, error) {
	userID, err := idFromPG("user.id", id)
	if err != nil {
		return domain.User{}, err
	}
	parsedEmail, err := domain.ParseEmail(email)
	if err != nil {
		return domain.User{}, fromStored(err)
	}
	return domain.User{
		ID:              userID,
		Email:           parsedEmail,
		EmailVerifiedAt: optionalTimeFromPG(verifiedAt),
		CreatedAt:       timeFromPG(createdAt),
		UpdatedAt:       timeFromPG(updatedAt),
	}, nil
}
