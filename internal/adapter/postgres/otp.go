package postgres

import (
	"context"
	"time"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// OTPChallengeRepository stores email-verification and password-reset challenges. Every query
// filters by purpose.
type OTPChallengeRepository struct {
	q    *sqlc.Queries
	inTx bool
}

var _ port.OTPChallengeRepository = (*OTPChallengeRepository)(nil)

// ByID returns the challenge of this purpose, or domain.ErrNotFound.
func (r *OTPChallengeRepository) ByID(ctx context.Context, purpose domain.OTPPurpose, id domain.ID) (domain.OTPChallenge, error) {
	row, err := r.q.GetOTPChallenge(ctx, sqlc.GetOTPChallengeParams{ID: pgUUID(id), Purpose: string(purpose)})
	if err != nil {
		return domain.OTPChallenge{}, mapError(err)
	}
	return otpChallengeFromRow(row)
}

// Lock reads the challenge with FOR UPDATE; it only works inside a transaction.
func (r *OTPChallengeRepository) Lock(ctx context.Context, purpose domain.OTPPurpose, id domain.ID) (domain.OTPChallenge, error) {
	if !r.inTx {
		return domain.OTPChallenge{}, errLockOutsideTx
	}
	row, err := r.q.LockOTPChallenge(ctx, sqlc.LockOTPChallengeParams{ID: pgUUID(id), Purpose: string(purpose)})
	if err != nil {
		return domain.OTPChallenge{}, mapError(err)
	}
	return otpChallengeFromRow(row)
}

// InvalidateOpen closes userID's open challenges of this purpose, expired ones included. The
// caller holds the user's row lock, so two concurrent requests cannot both pass this step and
// then collide on the one-open-challenge index.
func (r *OTPChallengeRepository) InvalidateOpen(ctx context.Context, userID domain.ID, purpose domain.OTPPurpose, at time.Time) error {
	return mapError(r.q.InvalidateOpenOTPChallenges(ctx, sqlc.InvalidateOpenOTPChallengesParams{
		UserID: pgUUID(userID), Purpose: string(purpose), InvalidatedAt: pgTime(at),
	}))
}

// Insert stores a new challenge; build it with domain.NewOTPChallenge after InvalidateOpen.
func (r *OTPChallengeRepository) Insert(ctx context.Context, c domain.OTPChallenge) error {
	return mapError(r.q.InsertOTPChallenge(ctx, sqlc.InsertOTPChallengeParams{
		ID:        pgUUID(c.ID),
		UserID:    pgUUID(c.UserID),
		Purpose:   string(c.Purpose),
		OtpHash:   c.OTPHash,
		CreatedAt: pgTime(c.CreatedAt),
		ExpiresAt: pgTime(c.ExpiresAt),
	}))
}

// RecordFailure adds one failed attempt atomically, or returns domain.ErrNotFound.
func (r *OTPChallengeRepository) RecordFailure(ctx context.Context, purpose domain.OTPPurpose, id domain.ID) error {
	n, err := r.q.RecordOTPFailure(ctx, sqlc.RecordOTPFailureParams{ID: pgUUID(id), Purpose: string(purpose)})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Consume marks the challenge used. It succeeds once, and only while the challenge is open and
// unexpired at `at`; otherwise it returns domain.ErrNotFound.
func (r *OTPChallengeRepository) Consume(ctx context.Context, purpose domain.OTPPurpose, id domain.ID, at time.Time) error {
	n, err := r.q.ConsumeOTPChallenge(ctx, sqlc.ConsumeOTPChallengeParams{ID: pgUUID(id), Purpose: string(purpose), At: pgTime(at)})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func otpChallengeFromRow(row sqlc.AppOtpChallenge) (domain.OTPChallenge, error) {
	id, err := idFromPG("otp_challenge.id", row.ID)
	if err != nil {
		return domain.OTPChallenge{}, err
	}
	userID, err := idFromPG("otp_challenge.user_id", row.UserID)
	if err != nil {
		return domain.OTPChallenge{}, err
	}
	purpose, err := domain.ParseOTPPurpose(row.Purpose)
	if err != nil {
		return domain.OTPChallenge{}, fromStored(err)
	}
	return domain.OTPChallenge{
		ID:             id,
		UserID:         userID,
		Purpose:        purpose,
		OTPHash:        row.OtpHash,
		CreatedAt:      timeFromPG(row.CreatedAt),
		ExpiresAt:      timeFromPG(row.ExpiresAt),
		FailedAttempts: int(row.FailedAttempts),
		ConsumedAt:     optionalTimeFromPG(row.ConsumedAt),
		InvalidatedAt:  optionalTimeFromPG(row.InvalidatedAt),
	}, nil
}
