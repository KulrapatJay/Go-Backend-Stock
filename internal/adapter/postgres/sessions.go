package postgres

import (
	"context"
	"time"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// SessionRepository stores server-side sessions by the SHA-256 hash of their token.
type SessionRepository struct {
	q *sqlc.Queries
}

var _ port.SessionRepository = (*SessionRepository)(nil)

// ByTokenHash returns the session as stored, expired or revoked included, or domain.ErrNotFound.
func (r *SessionRepository) ByTokenHash(ctx context.Context, tokenHash []byte) (domain.Session, error) {
	row, err := r.q.GetSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		return domain.Session{}, mapError(err)
	}
	return sessionFromRow(row)
}

// Insert stores a new session; build it with domain.NewSession.
func (r *SessionRepository) Insert(ctx context.Context, s domain.Session) error {
	return mapError(r.q.InsertSession(ctx, sqlc.InsertSessionParams{
		ID:        pgUUID(s.ID),
		UserID:    pgUUID(s.UserID),
		TokenHash: s.TokenHash,
		CreatedAt: pgTime(s.CreatedAt),
		ExpiresAt: pgTime(s.ExpiresAt),
	}))
}

// Revoke reports true only when this call revoked the session; an already revoked or unknown
// session reports false, so logout writes its audit event once.
func (r *SessionRepository) Revoke(ctx context.Context, id domain.ID, at time.Time, reason domain.RevokeReason) (bool, error) {
	n, err := r.q.RevokeSession(ctx, sqlc.RevokeSessionParams{
		ID: pgUUID(id), RevokedAt: pgTime(at), RevokeReason: pgText(string(reason)),
	})
	if err != nil {
		return false, mapError(err)
	}
	return n == 1, nil
}

// RevokeAll revokes every session of userID that is not revoked yet and returns how many.
func (r *SessionRepository) RevokeAll(ctx context.Context, userID domain.ID, at time.Time, reason domain.RevokeReason) (int64, error) {
	n, err := r.q.RevokeUserSessions(ctx, sqlc.RevokeUserSessionsParams{
		UserID: pgUUID(userID), RevokedAt: pgTime(at), RevokeReason: pgText(string(reason)),
	})
	if err != nil {
		return 0, mapError(err)
	}
	return n, nil
}

func sessionFromRow(row sqlc.AppSession) (domain.Session, error) {
	id, err := idFromPG("session.id", row.ID)
	if err != nil {
		return domain.Session{}, err
	}
	userID, err := idFromPG("session.user_id", row.UserID)
	if err != nil {
		return domain.Session{}, err
	}
	var reason domain.RevokeReason
	if row.RevokeReason.Valid {
		if reason, err = domain.ParseRevokeReason(row.RevokeReason.String); err != nil {
			return domain.Session{}, fromStored(err)
		}
	}
	return domain.Session{
		ID:           id,
		UserID:       userID,
		TokenHash:    row.TokenHash,
		CreatedAt:    timeFromPG(row.CreatedAt),
		ExpiresAt:    timeFromPG(row.ExpiresAt),
		RevokedAt:    optionalTimeFromPG(row.RevokedAt),
		RevokeReason: reason,
	}, nil
}
