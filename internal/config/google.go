package config

import (
	"errors"
	"fmt"
	"strings"
)

// GoogleCallbackPath is where Google sends the browser back. GOOGLE_REDIRECT_URI must be
// PUBLIC_ORIGIN followed by this path: the browser-binding cookie belongs to this host only, so
// any other redirect target would fail every sign-in.
const GoogleCallbackPath = "/api/v1/auth/google/callback"

// GoogleConfig is the OAuth client of Google sign-in. Either every value is set or none is, and an
// empty GoogleConfig turns Google sign-in off.
type GoogleConfig struct {
	ClientID     string
	ClientSecret Secret
	RedirectURI  string
}

// Enabled reports whether Google sign-in is configured.
func (g GoogleConfig) Enabled() bool { return g.ClientID != "" }

// loadGoogle reads GOOGLE_*. Its errors name the variables but never their values.
func loadGoogle(getenv func(string) string, publicOrigin string) (GoogleConfig, []error) {
	values := []struct {
		name  string
		value string
	}{
		{"GOOGLE_CLIENT_ID", strings.TrimSpace(getenv("GOOGLE_CLIENT_ID"))},
		{"GOOGLE_CLIENT_SECRET", strings.TrimSpace(getenv("GOOGLE_CLIENT_SECRET"))},
		{"GOOGLE_REDIRECT_URI", strings.TrimSpace(getenv("GOOGLE_REDIRECT_URI"))},
	}
	var missing []string
	for _, v := range values {
		if v.value == "" {
			missing = append(missing, v.name)
		}
	}
	switch len(missing) {
	case len(values):
		return GoogleConfig{}, nil
	case 0:
	default:
		return GoogleConfig{}, []error{fmt.Errorf(
			"GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET and GOOGLE_REDIRECT_URI must be set together; missing %s",
			strings.Join(missing, ", "))}
	}
	cfg := GoogleConfig{ClientID: values[0].value, ClientSecret: Secret(values[1].value), RedirectURI: values[2].value}
	// publicOrigin is empty when PUBLIC_ORIGIN itself was invalid; that error is reported already.
	if publicOrigin != "" && cfg.RedirectURI != publicOrigin+GoogleCallbackPath {
		return GoogleConfig{}, []error{errors.New("GOOGLE_REDIRECT_URI must be PUBLIC_ORIGIN followed by " + GoogleCallbackPath)}
	}
	return cfg, nil
}
