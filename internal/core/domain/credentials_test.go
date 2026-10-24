package domain_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"go-backend-stock/internal/core/domain"
)

const hashMarker = "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHQ$SECRETHASHVALUE"

func TestUserCredentialsHasPassword(t *testing.T) {
	googleOnly := domain.UserCredentials{User: domain.User{ID: mustID(t)}}
	if googleOnly.HasPassword() {
		t.Error("credentials without a hash report HasPassword() = true")
	}
	local := domain.UserCredentials{User: domain.User{ID: mustID(t)}, PasswordHash: domain.PasswordHash(hashMarker)}
	if !local.HasPassword() {
		t.Error("credentials with a hash report HasPassword() = false")
	}
	if string(local.PasswordHash) != hashMarker {
		t.Error("string(PasswordHash) must return the stored hash for verification")
	}
}

// TestPasswordHashIsNeverPrinted guards AGENTS.md: password hashes must never reach logs, even
// when someone logs the whole credentials value by mistake.
func TestPasswordHashIsNeverPrinted(t *testing.T) {
	creds := domain.UserCredentials{User: domain.User{ID: mustID(t)}, PasswordHash: domain.PasswordHash(hashMarker)}

	var outputs []string
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		outputs = append(outputs, fmt.Sprintf(format, creds), fmt.Sprintf(format, creds.PasswordHash))
	}
	js, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	outputs = append(outputs, string(js))
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("login", "creds", creds, "hash", creds.PasswordHash)
	slog.New(slog.NewTextHandler(&buf, nil)).Info("login", "creds", creds, "hash", creds.PasswordHash)
	outputs = append(outputs, buf.String())

	for _, out := range outputs {
		if strings.Contains(out, "SECRETHASHVALUE") {
			t.Errorf("password hash printed: %s", out)
		}
	}
	if !strings.Contains(fmt.Sprint(creds.PasswordHash), "REDACTED") {
		t.Errorf("PasswordHash prints %q, want a redaction marker", fmt.Sprint(creds.PasswordHash))
	}
}
