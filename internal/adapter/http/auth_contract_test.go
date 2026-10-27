package http

import (
	"slices"
	"strconv"
	"testing"
)

// documentedAuthOperations lists every auth operation with the statuses its handler can return. The
// generated Swagger spec must declare each of them, so the docs cannot fall behind the code.
var documentedAuthOperations = []struct {
	method, path string
	statuses     []int
}{
	{"get", "/api/v1/auth/csrf", []int{200, 503}},
	{"post", "/api/v1/auth/register", []int{202, 400, 403, 429, 500, 503}},
	{"post", "/api/v1/auth/email-verification/request", []int{202, 400, 403, 429, 500, 503}},
	{"post", "/api/v1/auth/email-verification/confirm", []int{204, 400, 403, 429, 500, 503}},
	{"post", "/api/v1/auth/login", []int{200, 400, 401, 403, 429, 500, 503}},
	{"get", "/api/v1/auth/me", []int{200, 401, 503}},
	{"post", "/api/v1/auth/logout", []int{204, 403, 500, 503}},
	{"get", "/api/v1/auth/config", []int{200}},
	{"post", "/api/v1/auth/google/start", []int{200, 403, 429, 500, 503}},
	{"post", "/api/v1/auth/google/link", []int{200, 401, 403, 429, 500, 503}},
}

func TestAuthOperationsAreDocumented(t *testing.T) {
	spec := loadSpec(t)
	for _, op := range documentedAuthOperations {
		declared, ok := spec.Paths[op.path][op.method]
		if !ok {
			t.Errorf("%s %s is not in swagger.json", op.method, op.path)
			continue
		}
		for _, status := range op.statuses {
			if _, ok := declared.Responses[strconv.Itoa(status)]; !ok {
				t.Errorf("%s %s does not declare status %d", op.method, op.path, status)
			}
		}
	}
}

// requiredAuthFields lists the fields each auth schema must mark required (details stays optional),
// and dateTimeFields the timestamps that must carry format date-time.
var requiredAuthFields = map[string][]string{
	"http.RegisterRequest":     {"email", "password"},
	"http.LoginRequest":        {"email", "password"},
	"http.EmailRequest":        {"email"},
	"http.ConfirmEmailRequest": {"challenge_id", "otp"},
	"http.CSRFResponse":        {"csrf_token", "expires_at"},
	"http.OTPAcceptedResponse": {"challenge_id", "expires_in", "message"},
	"http.SessionResponse":     {"expires_at", "user"},
	"http.UserResponse":        {"created_at", "email", "email_verified", "id"},
	"http.ErrorResponse":       {"error"},
	"http.ErrorBody":           {"code", "message", "request_id"},
	"http.AuthConfigResponse":  {"google_enabled"},
	"http.GoogleStartResponse": {"authorization_url"},
}

var dateTimeFields = map[string]string{
	"http.CSRFResponse":    "expires_at",
	"http.SessionResponse": "expires_at",
	"http.UserResponse":    "created_at",
}

func TestAuthSchemasDeclareRequiredFieldsAndTimestampFormats(t *testing.T) {
	spec := loadSpec(t)
	for name, want := range requiredAuthFields {
		def, ok := spec.Definitions[name]
		if !ok {
			t.Errorf("%s is not in swagger.json", name)
			continue
		}
		got := slices.Clone(def.Required)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s required = %v, want %v", name, got, want)
		}
	}
	for name, field := range dateTimeFields {
		if got := spec.Definitions[name].Properties[field].Format; got != "date-time" {
			t.Errorf("%s.%s format = %q, want date-time", name, field, got)
		}
	}
	if slices.Contains(spec.Definitions["http.ErrorBody"].Required, "details") {
		t.Error("details must stay optional")
	}
}
