package service

import (
	"errors"

	"go-backend-stock/internal/core/domain"
)

// PasswordPolicy applies every password rule: the ones in domain.ParsePassword plus the list of
// common passwords, which needs a data file and so cannot live in the domain package.
type PasswordPolicy struct{ common *Blocklist }

// NewPasswordPolicy returns a policy that rejects the passwords on common. A nil list is an error
// so the check cannot be turned off by mistake.
func NewPasswordPolicy(common *Blocklist) (*PasswordPolicy, error) {
	if common == nil {
		return nil, errors.New("password policy needs a blocklist")
	}
	return &PasswordPolicy{common: common}, nil
}

// Parse returns the password if it passes every rule, or an invalid_input error that lists every
// rule it broke. Register and password reset use it; login does not.
func (p *PasswordPolicy) Parse(raw string, email domain.Email) (domain.Password, error) {
	return domain.ParsePasswordChecked(raw, email, p.common.Contains)
}
