package postgres

import (
	"context"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// IdentityRepository stores links between users and external identities (Google).
type IdentityRepository struct {
	q *sqlc.Queries
}

var _ port.IdentityRepository = (*IdentityRepository)(nil)

// BySubject returns the link for the provider's stable subject, or domain.ErrNotFound.
func (r *IdentityRepository) BySubject(ctx context.Context, provider domain.IdentityProviderName, subject string) (domain.AuthProvider, error) {
	row, err := r.q.GetIdentityBySubject(ctx, sqlc.GetIdentityBySubjectParams{
		Provider: string(provider), ProviderSubject: subject,
	})
	if err != nil {
		return domain.AuthProvider{}, mapError(err)
	}
	return identityFromRow(row)
}

// Insert stores a link; build it with domain.NewAuthProvider. An existing subject, or an existing
// link of this user to the provider, returns domain.ErrIdentityConflict and leaves the transaction
// usable, so the caller can still write an auth.identity_link_failed audit event.
func (r *IdentityRepository) Insert(ctx context.Context, link domain.AuthProvider) error {
	n, err := r.q.InsertIdentity(ctx, sqlc.InsertIdentityParams{
		ID:              pgUUID(link.ID),
		UserID:          pgUUID(link.UserID),
		Provider:        string(link.Provider),
		ProviderSubject: link.ProviderSubject,
		CreatedAt:       pgTime(link.CreatedAt),
	})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrIdentityConflict
	}
	return nil
}

func identityFromRow(row sqlc.AppAuthProvider) (domain.AuthProvider, error) {
	id, err := idFromPG("auth_provider.id", row.ID)
	if err != nil {
		return domain.AuthProvider{}, err
	}
	userID, err := idFromPG("auth_provider.user_id", row.UserID)
	if err != nil {
		return domain.AuthProvider{}, err
	}
	provider, err := domain.ParseIdentityProviderName(row.Provider)
	if err != nil {
		return domain.AuthProvider{}, fromStored(err)
	}
	return domain.AuthProvider{
		ID: id, UserID: userID, Provider: provider, ProviderSubject: row.ProviderSubject, CreatedAt: timeFromPG(row.CreatedAt),
	}, nil
}
