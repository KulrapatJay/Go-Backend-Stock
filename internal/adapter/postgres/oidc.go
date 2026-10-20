package postgres

import (
	"context"
	"time"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// OIDCFlowRepository stores pending Google sign-in and account-link flows.
type OIDCFlowRepository struct {
	q *sqlc.Queries
}

var _ port.OIDCFlowRepository = (*OIDCFlowRepository)(nil)

// Insert stores a new flow; build it with domain.NewOIDCFlow.
func (r *OIDCFlowRepository) Insert(ctx context.Context, f domain.OIDCFlow) error {
	return mapError(r.q.InsertOIDCFlow(ctx, sqlc.InsertOIDCFlowParams{
		ID:                 pgUUID(f.ID),
		Purpose:            string(f.Purpose),
		UserID:             pgUUID(f.UserID),
		StateHash:          f.StateHash,
		NonceHash:          f.NonceHash,
		BrowserBindingHash: f.BrowserBindingHash,
		CreatedAt:          pgTime(f.CreatedAt),
		ExpiresAt:          pgTime(f.ExpiresAt),
	}))
}

// Consume marks the flow matching both hashes as consumed in one statement, so only one of two
// concurrent callbacks succeeds. An unknown, expired or already consumed flow, or a different
// browser binding, returns domain.ErrNotFound.
func (r *OIDCFlowRepository) Consume(ctx context.Context, stateHash, browserBindingHash []byte, at time.Time) (domain.OIDCFlow, error) {
	row, err := r.q.ConsumeOIDCFlow(ctx, sqlc.ConsumeOIDCFlowParams{
		At: pgTime(at), StateHash: stateHash, BrowserBindingHash: browserBindingHash,
	})
	if err != nil {
		return domain.OIDCFlow{}, mapError(err)
	}
	return oidcFlowFromRow(row)
}

func oidcFlowFromRow(row sqlc.AppOauthFlow) (domain.OIDCFlow, error) {
	id, err := idFromPG("oidc_flow.id", row.ID)
	if err != nil {
		return domain.OIDCFlow{}, err
	}
	userID, err := optionalIDFromPG("oidc_flow.user_id", row.UserID)
	if err != nil {
		return domain.OIDCFlow{}, err
	}
	purpose, err := domain.ParseOIDCPurpose(row.Purpose)
	if err != nil {
		return domain.OIDCFlow{}, fromStored(err)
	}
	return domain.OIDCFlow{
		ID:                 id,
		Purpose:            purpose,
		UserID:             userID,
		StateHash:          row.StateHash,
		NonceHash:          row.NonceHash,
		BrowserBindingHash: row.BrowserBindingHash,
		CreatedAt:          timeFromPG(row.CreatedAt),
		ExpiresAt:          timeFromPG(row.ExpiresAt),
		ConsumedAt:         optionalTimeFromPG(row.ConsumedAt),
	}, nil
}
