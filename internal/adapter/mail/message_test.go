package mail

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestCheckAddress(t *testing.T) {
	for _, ok := range []string{"a.b+c@example.com", "no-reply@localhost", "o'brien@example.co.th"} {
		if err := checkAddress(ok); err != nil {
			t.Errorf("checkAddress(%q) error = %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "no-at-sign", "@example.com", "user@", "a@@b.com",
		"a@b.com\r\nBcc: x@y.com", "a@b.com\nBcc: x@y.com", "a\r@b.com",
		"a,b@example.com", "a;b@example.com", "<a@example.com>", `"a"@example.com`,
		"a b@example.com", "a\tb@example.com", "a\x00b@example.com", "a\u0085b@example.com",
	} {
		err := checkAddress(bad)
		if err == nil {
			t.Errorf("checkAddress(%q) accepted it", bad)
			continue
		}
		var de *domain.Error
		if !errors.As(err, &de) || de.Code != domain.CodeInternal {
			t.Errorf("checkAddress(%q) error = %v, want an internal domain error", bad, err)
		}
	}
}

func TestMessageBytes(t *testing.T) {
	m := message{
		from: "no-reply@localhost", to: "user@example.com", subject: "Your verification code",
		body: "line one\nline two\n", date: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC), id: "abc123@localhost",
	}
	raw, err := m.bytes()
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)

	for _, want := range []string{
		"From: <no-reply@localhost>\r\n",
		"To: <user@example.com>\r\n",
		"Subject: Your verification code\r\n",
		"Date: Thu, 22 Oct 2026 09:00:00 +0000\r\n",
		"Message-ID: <abc123@localhost>\r\n",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"Content-Transfer-Encoding: 8bit\r\n",
		"\r\n\r\nline one\r\nline two\r\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("message lacks %q:\n%s", want, s)
		}
	}
	if strings.Count(s, "\n") != strings.Count(s, "\r\n") {
		t.Errorf("message has a bare line feed:\n%q", s)
	}
}

func TestMessageRejectsUnsafeValues(t *testing.T) {
	base := message{
		from: "no-reply@localhost", to: "user@example.com", subject: "Hello",
		body: "body", date: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC), id: "abc@localhost",
	}
	for name, mutate := range map[string]func(*message){
		"subject with a line break": func(m *message) { m.subject = "Hello\r\nBcc: x@y.com" },
		"subject with non-ASCII":    func(m *message) { m.subject = "สวัสดี" },
		"id with a line break":      func(m *message) { m.id = "abc\r\n@localhost" },
		"recipient with a header":   func(m *message) { m.to = "user@example.com\r\nBcc: x@y.com" },
		"sender with a comma":       func(m *message) { m.from = "a,b@localhost" },
	} {
		m := base
		mutate(&m)
		if _, err := m.bytes(); err == nil {
			t.Errorf("%s: bytes() accepted it", name)
		}
	}
}

func TestBodies(t *testing.T) {
	to, err := domain.ParseEmail("user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	otp, err := domain.ParseOTP("042731")
	if err != nil {
		t.Fatal(err)
	}
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	body := verificationBody(domain.OTPMail{To: to, OTP: otp, ChallengeID: id, ExpiresAt: time.Date(2026, 10, 22, 9, 10, 0, 0, time.UTC)})
	for _, want := range []string{"042731", "2026-10-22 09:10 UTC", id.String(), "ignore this email"} {
		if !strings.Contains(body, want) {
			t.Errorf("verification body lacks %q:\n%s", want, body)
		}
	}

	attempt := registrationAttemptBody()
	if strings.Contains(attempt, "code") || !strings.Contains(attempt, "already exists") {
		t.Errorf("registration attempt body must say an account exists and carry no code:\n%s", attempt)
	}
}

func TestNewMessageIDIsUniqueAndUsesTheSenderHost(t *testing.T) {
	a, err := newMessageID("no-reply@example.org")
	if err != nil {
		t.Fatal(err)
	}
	b, err := newMessageID("no-reply@example.org")
	if err != nil {
		t.Fatal(err)
	}
	if a == b || !strings.HasSuffix(a, "@example.org") || len(a) != 32+len("@example.org") {
		t.Errorf("ids %q and %q must differ, carry 16 random bytes in hex and end with @example.org", a, b)
	}
}
