package service

import (
	"time"

	"go-backend-stock/internal/core/domain"
)

// issueSession builds a session of userID that starts at now and lasts the session lifetime. Only
// its SHA-256 is stored; the raw token goes into the cookie and nowhere else.
func (a *Auth) issueSession(userID domain.ID, now time.Time) (domain.Session, domain.SecretText, error) {
	token, hash, err := NewSessionToken()
	if err != nil {
		return domain.Session{}, domain.SecretText{}, err
	}
	id, err := domain.NewID()
	if err != nil {
		return domain.Session{}, domain.SecretText{}, err
	}
	s, err := domain.NewSession(domain.Session{ID: id, UserID: userID, TokenHash: hash, CreatedAt: now, ExpiresAt: now.Add(a.sessionTTL)})
	if err != nil {
		return domain.Session{}, domain.SecretText{}, err
	}
	return s, domain.NewSecretText(token), nil
}
