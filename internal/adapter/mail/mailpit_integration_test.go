package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

type mailpitMessage struct {
	ID      string `json:"ID"`
	Subject string `json:"Subject"`
}

// mailpitMailer returns a mailer that sends to the Mailpit named by the environment, and that
// Mailpit's API URL. Without the variables the test is skipped, except when
// REQUIRE_MAIL_INTEGRATION=1 (make test-mail), where it fails so a missing stack is not a pass.
func mailpitMailer(t *testing.T) (*SMTPMailer, string) {
	t.Helper()
	smtpAddr, apiURL := os.Getenv("TEST_SMTP_ADDR"), os.Getenv("TEST_MAILPIT_API_URL")
	if smtpAddr == "" || apiURL == "" {
		if os.Getenv("REQUIRE_MAIL_INTEGRATION") == "1" {
			t.Fatal("REQUIRE_MAIL_INTEGRATION=1 but TEST_SMTP_ADDR or TEST_MAILPIT_API_URL is not set")
		}
		t.Skip("set TEST_SMTP_ADDR and TEST_MAILPIT_API_URL (make test-mail) to run against Mailpit")
	}
	host, portText, err := net.SplitHostPort(smtpAddr)
	if err != nil {
		t.Fatalf("TEST_SMTP_ADDR: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("TEST_SMTP_ADDR port: %v", err)
	}
	mailer, err := NewSMTPMailer(SMTPConfig{
		Host: host, Port: port, From: "no-reply@localhost", TLS: TLSNone, Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mailer, strings.TrimRight(apiURL, "/")
}

func mailpitDo(ctx context.Context, method, apiURL, path string, body any, out any) error {
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: status %d", method, path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// waitForMessage polls Mailpit until a message to recipient arrives, returns it with its text
// body and raw source, and deletes it afterwards.
func waitForMessage(t *testing.T, apiURL, recipient string) (mailpitMessage, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	var found mailpitMessage
	for {
		var result struct {
			Messages []mailpitMessage `json:"messages"`
		}
		err := mailpitDo(ctx, http.MethodGet, apiURL, "/api/v1/search?query="+url.QueryEscape("to:"+recipient), nil, &result)
		if err == nil && len(result.Messages) > 0 {
			found = result.Messages[0]
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("no message to %s reached Mailpit (last error: %v)", recipient, err)
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Cleanup(func() {
		cleanup, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelCleanup()
		_ = mailpitDo(cleanup, http.MethodDelete, apiURL, "/api/v1/messages", map[string][]string{"IDs": {found.ID}}, nil)
	})

	var detail struct {
		Text string `json:"Text"`
	}
	if err := mailpitDo(ctx, http.MethodGet, apiURL, "/api/v1/message/"+url.PathEscape(found.ID), nil, &detail); err != nil {
		t.Fatalf("read message %s: %v", found.ID, err)
	}

	rawReq, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/api/v1/message/"+url.PathEscape(found.ID)+"/raw", nil)
	if err != nil {
		t.Fatal(err)
	}
	rawResp, err := http.DefaultClient.Do(rawReq)
	if err != nil {
		t.Fatalf("read raw message %s: %v", found.ID, err)
	}
	defer rawResp.Body.Close()
	rawBytes, err := io.ReadAll(rawResp.Body)
	if err != nil || rawResp.StatusCode != http.StatusOK {
		t.Fatalf("read raw message %s: status %d, error %v", found.ID, rawResp.StatusCode, err)
	}
	return found, detail.Text, string(rawBytes)
}

// assertThaiTransport checks the wire format Mailpit stored: an RFC 2047 Subject, a UTF-8
// quoted-printable body, and no raw Thai octets anywhere in the message. The headers are logged
// so `go test -v` shows what a mail server really received.
func assertThaiTransport(t *testing.T, raw string) {
	t.Helper()
	headers, _, _ := strings.Cut(raw, "\r\n\r\n")
	t.Logf("headers as stored by Mailpit:\n%s", headers)
	if !strings.Contains(headers, "Subject: =?UTF-8?") {
		t.Errorf("Subject is not RFC 2047 encoded:\n%s", headers)
	}
	if !strings.Contains(headers, "Content-Transfer-Encoding: quoted-printable") {
		t.Errorf("body is not quoted-printable:\n%s", headers)
	}
	if strings.ContainsFunc(raw, func(r rune) bool { return r > 127 }) {
		t.Errorf("raw message carries non-ASCII octets")
	}
}

func uniqueRecipient(t *testing.T) domain.Email {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	to, err := domain.ParseEmail("it-" + id.String() + "@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return to
}

func TestMailpitReceivesTheVerificationEmail(t *testing.T) {
	mailer, apiURL := mailpitMailer(t)
	mail := testOTPMail(t)
	mail.To = uniqueRecipient(t)

	if err := mailer.SendEmailVerification(t.Context(), mail); err != nil {
		t.Fatalf("SendEmailVerification() error = %v", err)
	}
	msg, text, raw := waitForMessage(t, apiURL, mail.To.String())
	if msg.Subject != wantVerificationSubject {
		t.Errorf("Subject = %q, want %q", msg.Subject, wantVerificationSubject)
	}
	if !strings.Contains(text, "042731") || !strings.Contains(text, mail.ChallengeID.String()) || !strings.Contains(text, "รหัสยืนยันของคุณคือ 042731") {
		t.Errorf("body lacks the code or the reference:\n%s", text)
	}
	assertThaiTransport(t, raw)
}

func TestMailpitReceivesTheRegistrationAttemptEmail(t *testing.T) {
	mailer, apiURL := mailpitMailer(t)
	to := uniqueRecipient(t)

	if err := mailer.SendRegistrationAttempt(t.Context(), to); err != nil {
		t.Fatalf("SendRegistrationAttempt() error = %v", err)
	}
	msg, text, raw := waitForMessage(t, apiURL, to.String())
	if msg.Subject != wantRegistrationAttemptSubject || !strings.Contains(text, "มีบัญชีอยู่แล้ว") {
		t.Errorf("Subject = %q, body:\n%s", msg.Subject, text)
	}
	if sixDigitCode.MatchString(text) {
		t.Errorf("the registration attempt email must carry no 6-digit code:\n%s", text)
	}
	assertThaiTransport(t, raw)
}

func TestMailpitReceivesThePasswordResetEmail(t *testing.T) {
	mailer, apiURL := mailpitMailer(t)
	mail := testOTPMail(t)
	mail.To = uniqueRecipient(t)

	if err := mailer.SendPasswordReset(t.Context(), mail); err != nil {
		t.Fatalf("SendPasswordReset() error = %v", err)
	}
	msg, text, raw := waitForMessage(t, apiURL, mail.To.String())
	if msg.Subject != passwordResetSubject {
		t.Errorf("Subject = %q, want %q", msg.Subject, passwordResetSubject)
	}
	if !strings.Contains(text, "042731") || !strings.Contains(text, mail.ChallengeID.String()) || !strings.Contains(text, "รหัสสำหรับตั้งรหัสผ่านใหม่ของคุณคือ 042731") {
		t.Errorf("body lacks the code or the reference:\n%s", text)
	}
	assertThaiTransport(t, raw)
}

func TestMailpitReceivesThePasswordChangedEmail(t *testing.T) {
	mailer, apiURL := mailpitMailer(t)
	to := uniqueRecipient(t)

	if err := mailer.SendPasswordChanged(t.Context(), to); err != nil {
		t.Fatalf("SendPasswordChanged() error = %v", err)
	}
	msg, text, raw := waitForMessage(t, apiURL, to.String())
	if msg.Subject != passwordChangedSubject {
		t.Errorf("Subject = %q, want %q", msg.Subject, passwordChangedSubject)
	}
	if sixDigitCode.MatchString(text) {
		t.Errorf("the password changed email must carry no 6-digit code:\n%s", text)
	}
	assertThaiTransport(t, raw)
}
