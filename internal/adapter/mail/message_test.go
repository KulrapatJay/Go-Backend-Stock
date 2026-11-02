package mail

import (
	"errors"
	"io"
	"mime"
	"mime/quotedprintable"
	netmail "net/mail"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go-backend-stock/internal/core/domain"
)

const (
	wantVerificationSubject        = "รหัสยืนยันอีเมลของคุณ"
	wantRegistrationAttemptSubject = "มีผู้พยายามสมัครสมาชิกด้วยอีเมลของคุณ"
	wantPasswordResetSubject       = "รหัสสำหรับตั้งรหัสผ่านใหม่"
	wantPasswordChangedSubject     = "รหัสผ่านของคุณถูกเปลี่ยนแล้ว"
)

// parsedMail is a received message after the transfer and header encodings are undone.
type parsedMail struct {
	header  netmail.Header
	subject string
	body    string // decoded, with "\n" line breaks
}

// parseReceivedMail parses a raw message the way a mail client does: it decodes the RFC 2047
// Subject and the quoted-printable body, and fails the test if the transfer encoding is not the
// one the package promises.
func parseReceivedMail(t *testing.T, raw string) parsedMail {
	t.Helper()
	msg, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("message does not parse: %v\n%q", err, raw)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatalf("Subject does not decode: %v", err)
	}
	if got := msg.Header.Get("Content-Transfer-Encoding"); got != "quoted-printable" {
		t.Fatalf("Content-Transfer-Encoding = %q, want quoted-printable", got)
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatalf("body does not decode: %v", err)
	}
	return parsedMail{header: msg.Header, subject: subject, body: strings.ReplaceAll(string(decoded), "\r\n", "\n")}
}

var sixDigitCode = regexp.MustCompile(`\b\d{6}\b`)

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
	const body = "บรรทัดที่หนึ่ง\n\nline two = ok\n"
	m := message{
		from: "no-reply@localhost", to: "user@example.com", subject: wantVerificationSubject,
		body: body, date: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC), id: "abc123@localhost",
	}
	raw, err := m.bytes()
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)

	for _, want := range []string{
		"From: <no-reply@localhost>\r\n",
		"To: <user@example.com>\r\n",
		"Date: Thu, 22 Oct 2026 09:00:00 +0000\r\n",
		"Message-ID: <abc123@localhost>\r\n",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"Content-Transfer-Encoding: quoted-printable\r\n",
		"Subject: =?UTF-8?",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("message lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "8bit") || strings.Contains(s, "รหัส") {
		t.Errorf("message must carry no raw Thai (subject and body are encoded) and no 8bit:\n%s", s)
	}
	if strings.Count(s, "\n") != strings.Count(s, "\r\n") {
		t.Errorf("message has a bare line feed:\n%q", s)
	}

	got := parseReceivedMail(t, s)
	if got.subject != wantVerificationSubject {
		t.Errorf("decoded subject = %q, want %q", got.subject, wantVerificationSubject)
	}
	if got.body != body {
		t.Errorf("decoded body = %q, want %q", got.body, body)
	}
}

func TestMessageBodyIsQuotedPrintableWithShortLines(t *testing.T) {
	long := strings.Repeat("ทดสอบข้อความยาว ", 40) + "end"
	body := long + "\n\n" + "trailing space \nคำสุดท้าย\n"
	m := message{
		from: "no-reply@localhost", to: "user@example.com", subject: "Hello",
		body: body, date: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC), id: "abc123@localhost",
	}
	raw, err := m.bytes()
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Count(s, "\n") != strings.Count(s, "\r\n") {
		t.Errorf("message has a bare line feed:\n%q", s)
	}
	for _, line := range strings.Split(s, "\r\n") {
		if utf8.RuneCountInString(line) > 76 {
			t.Errorf("line of %d characters, limit is 76: %q", utf8.RuneCountInString(line), line)
		}
	}
	if got := parseReceivedMail(t, s); got.body != body {
		t.Errorf("decoded body = %q, want %q", got.body, body)
	}
}

func TestMessageEncodesTheSubjectSafely(t *testing.T) {
	for name, subject := range map[string]string{
		"thai":       wantVerificationSubject,
		"long thai":  strings.Repeat("ก", 60),
		"ascii":      "Hello",
		"with quote": `หัวเรื่อง "quoted" = ?`,
	} {
		m := message{
			from: "no-reply@localhost", to: "user@example.com", subject: subject,
			body: "body\n", date: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC), id: "abc@localhost",
		}
		raw, err := m.bytes()
		if err != nil {
			t.Errorf("%s: bytes() error = %v", name, err)
			continue
		}
		s := string(raw)
		headers, _, _ := strings.Cut(s, "\r\n\r\n")
		// The only line breaks inside the header block are CRLF followed by a header name or by
		// folding whitespace; a subject must not be able to add either of the former.
		if strings.Count(headers, "\r") != strings.Count(headers, "\r\n") || strings.Count(headers, "\n") != strings.Count(headers, "\r\n") {
			t.Errorf("%s: header block has a bare CR or LF: %q", name, headers)
		}
		lines := strings.Split(headers, "\r\n")
		subjectLines := 0
		for i, line := range lines {
			if strings.HasPrefix(line, "Subject: ") {
				subjectLines++
				for _, cont := range lines[i+1:] {
					if !strings.HasPrefix(cont, " ") && !strings.HasPrefix(cont, "\t") {
						break
					}
					if strings.HasPrefix(cont, " ") && strings.Contains(cont, ":") && !strings.HasPrefix(cont, " =?") {
						t.Errorf("%s: folded Subject line looks like a header: %q", name, cont)
					}
				}
			}
			if len(line) > 998 {
				t.Errorf("%s: line too long (%d): %q", name, len(line), line)
			}
		}
		if subjectLines != 1 {
			t.Errorf("%s: %d Subject headers, want 1", name, subjectLines)
		}
		if strings.Contains(headers, "\r\nBcc:") {
			t.Errorf("%s: a Bcc header appeared: %q", name, headers)
		}
		if got := parseReceivedMail(t, s); got.subject != subject {
			t.Errorf("%s: decoded subject = %q, want %q", name, got.subject, subject)
		}
		if subject != "Hello" && strings.ContainsFunc(headers, func(r rune) bool { return r > 127 }) {
			t.Errorf("%s: headers carry raw non-ASCII: %q", name, headers)
		}
	}
}

func TestMessageRejectsUnsafeValues(t *testing.T) {
	base := message{
		from: "no-reply@localhost", to: "user@example.com", subject: "Hello",
		body: "body", date: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC), id: "abc@localhost",
	}
	for name, mutate := range map[string]func(*message){
		"subject with a line break":           func(m *message) { m.subject = "Hello\r\nBcc: x@y.com" },
		"subject with a bare line feed":       func(m *message) { m.subject = "Hello\nBcc: x@y.com" },
		"subject with a bare carriage return": func(m *message) { m.subject = "Hello\rBcc: x@y.com" },
		"thai subject with a line break":      func(m *message) { m.subject = "สวัสดี\r\nBcc: x@y.com" },
		"thai subject with a line feed":       func(m *message) { m.subject = "สวัสดี\nBcc: x@y.com" },
		"subject with a NUL":                  func(m *message) { m.subject = "สวัสดี\x00" },
		"subject with a tab":                  func(m *message) { m.subject = "สวัสดี\tBcc" },
		"subject with a C1 control":           func(m *message) { m.subject = "สวัสดี\u0085Bcc" },
		"subject with a line separator":       func(m *message) { m.subject = "สวัสดี\u2028Bcc" },
		"subject that is not valid UTF-8":     func(m *message) { m.subject = "สวัสดี\xff" },
		"id with a line break":                func(m *message) { m.id = "abc\r\n@localhost" },
		"id with non-ASCII":                   func(m *message) { m.id = "สวัสดี@localhost" },
		"recipient with a header":             func(m *message) { m.to = "user@example.com\r\nBcc: x@y.com" },
		"sender with a comma":                 func(m *message) { m.from = "a,b@localhost" },
	} {
		m := base
		mutate(&m)
		if _, err := m.bytes(); err == nil {
			t.Errorf("%s: bytes() accepted it", name)
		}
	}

	thai := base
	thai.subject = wantVerificationSubject
	if _, err := thai.bytes(); err != nil {
		t.Errorf("a Thai subject must be accepted: %v", err)
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
	wantVerification := "รหัสยืนยันของคุณคือ 042731\n\n" +
		"รหัสนี้ใช้ได้ถึง 2026-10-22 16:10 น. (เวลาประเทศไทย) ห้ามบอกรหัสนี้กับผู้อื่น\n\n" +
		"รหัสอ้างอิง: " + id.String() + "\n\n" +
		"หากคุณไม่ได้สมัครบัญชี ไม่ต้องทำอะไร\n"
	if body != wantVerification {
		t.Errorf("verification body:\n got %q\nwant %q", body, wantVerification)
	}

	attempt := registrationAttemptBody()
	wantAttempt := "มีผู้พยายามสร้างบัญชีด้วยอีเมลนี้ แต่อีเมลนี้มีบัญชีอยู่แล้ว\n\n" +
		"หากเป็นคุณ ให้เข้าสู่ระบบแทน หรือใช้ตัวเลือก \"ลืมรหัสผ่าน\" หากจำรหัสผ่านไม่ได้\n" +
		"หากไม่ใช่คุณ ไม่ต้องทำอะไร\n"
	if attempt != wantAttempt {
		t.Errorf("registration attempt body:\n got %q\nwant %q", attempt, wantAttempt)
	}
	if sixDigitCode.MatchString(attempt) {
		t.Errorf("registration attempt body must carry no code:\n%s", attempt)
	}

	if verificationSubject != wantVerificationSubject || registrationAttemptSubject != wantRegistrationAttemptSubject {
		t.Errorf("subjects = %q, %q", verificationSubject, registrationAttemptSubject)
	}
}

func TestPasswordResetBodyCarriesTheCodeAndReference(t *testing.T) {
	mail := testOTPMail(t)
	body := passwordResetBody(mail)
	want := "รหัสสำหรับตั้งรหัสผ่านใหม่ของคุณคือ 042731\n\n" +
		"รหัสนี้ใช้ได้ครั้งเดียวถึง 2026-10-22 16:10 น. (เวลาประเทศไทย) ห้ามบอกรหัสนี้กับผู้อื่น\n\n" +
		"รหัสอ้างอิง: " + mail.ChallengeID.String() + "\n\n" +
		"หากคุณไม่ได้ขอตั้งรหัสผ่านใหม่ ไม่ต้องทำอะไร รหัสผ่านเดิมยังใช้ได้\n"
	if body != want {
		t.Errorf("password reset body:\n got %q\nwant %q", body, want)
	}
	if passwordResetSubject != wantPasswordResetSubject {
		t.Errorf("password reset subject = %q", passwordResetSubject)
	}
}

func TestPasswordChangedBodyHasNoCode(t *testing.T) {
	body := passwordChangedBody()
	want := "รหัสผ่านของบัญชีนี้เพิ่งถูกเปลี่ยน และทุกอุปกรณ์ที่เข้าสู่ระบบอยู่ถูกออกจากระบบแล้ว\n\n" +
		"หากเป็นคุณ ให้เข้าสู่ระบบด้วยรหัสผ่านใหม่\n" +
		"หากไม่ใช่คุณ ให้ใช้ \"ลืมรหัสผ่าน\" ตั้งรหัสใหม่ทันที\n"
	if body != want {
		t.Errorf("password changed body:\n got %q\nwant %q", body, want)
	}
	if sixDigitCode.MatchString(body) {
		t.Errorf("password changed body must carry no code:\n%s", body)
	}
	if passwordChangedSubject != wantPasswordChangedSubject {
		t.Errorf("password changed subject = %q", passwordChangedSubject)
	}
}

func TestExpiryIsShownInThaiTimeRegardlessOfTheInputZone(t *testing.T) {
	to, _ := domain.ParseEmail("user@example.com")
	otp, _ := domain.ParseOTP("042731")
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	// 20:30 UTC is 03:30 the next day in Thailand.
	body := verificationBody(domain.OTPMail{To: to, OTP: otp, ChallengeID: id, ExpiresAt: time.Date(2026, 10, 22, 20, 30, 0, 0, time.UTC)})
	if !strings.Contains(body, "ถึง 2026-10-23 03:30 น.") {
		t.Errorf("expiry not shown in UTC+7:\n%s", body)
	}
	local := time.FixedZone("X", -5*3600)
	body = verificationBody(domain.OTPMail{To: to, OTP: otp, ChallengeID: id, ExpiresAt: time.Date(2026, 10, 22, 15, 30, 0, 0, local)})
	if !strings.Contains(body, "ถึง 2026-10-23 03:30 น.") {
		t.Errorf("expiry not shown in UTC+7 for a non-UTC input:\n%s", body)
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
