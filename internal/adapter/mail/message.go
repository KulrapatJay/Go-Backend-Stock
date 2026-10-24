// Package mail implements port.Mailer: SMTPMailer sends over SMTP and Dispatcher hands emails to
// a background worker so a response never waits for the mail server.
package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"mime"
	"mime/quotedprintable"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go-backend-stock/internal/core/domain"
)

const (
	verificationSubject        = "รหัสยืนยันอีเมลของคุณ"
	registrationAttemptSubject = "มีผู้พยายามสมัครสมาชิกด้วยอีเมลของคุณ"
	otpExpiryLayout            = "2006-01-02 15:04"
)

// thaiZone is UTC+7, built by hand so the emails do not depend on tzdata being installed. Thailand
// has no daylight saving time.
var thaiZone = time.FixedZone("ICT", 7*3600)

func unsafeMail(rule string) error {
	return domain.NewError(domain.CodeInternal, "mail: "+rule)
}

// checkAddress rejects anything that could change the structure of an SMTP command or a header:
// line breaks (a second header or command), separators and brackets (a second recipient),
// quotes, whitespace and control characters. domain.ParseEmail already refuses these, so a hit
// here means a bug or a value that did not come from the domain.
func checkAddress(addr string) error {
	local, host, ok := strings.Cut(addr, "@")
	switch {
	case !ok || local == "" || host == "" || strings.Contains(host, "@"):
		return unsafeMail("address must have the form name@host")
	case strings.ContainsAny(addr, "\r\n,;<>\""),
		strings.ContainsFunc(addr, unicode.IsSpace),
		strings.ContainsFunc(addr, unicode.IsControl):
		return unsafeMail("address contains a character that is not allowed")
	}
	return nil
}

// checkHeaderValue accepts only printable ASCII without line breaks. It guards the Message-ID,
// which is written as is; the Subject goes through checkSubject and RFC 2047 encoding instead.
func checkHeaderValue(v string) error {
	if strings.ContainsFunc(v, func(r rune) bool { return r > unicode.MaxASCII || unicode.IsControl(r) }) {
		return unsafeMail("header value must be printable ASCII on one line")
	}
	return nil
}

// checkSubject validates the raw subject before it is encoded: valid UTF-8 without control
// characters (line breaks and tabs included) or Unicode line/paragraph separators. Letters from
// any script are fine because encodeSubject turns them into ASCII encoded-words.
func checkSubject(v string) error {
	if !utf8.ValidString(v) {
		return unsafeMail("subject must be valid UTF-8")
	}
	if strings.ContainsFunc(v, func(r rune) bool {
		return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
	}) {
		return unsafeMail("subject must be one line without control characters")
	}
	return nil
}

// encodeSubject returns the RFC 2047 Q-encoded form of a validated subject. An ASCII-only subject
// is returned unchanged; longer encoded subjects are folded with CRLF + space, which is the only
// line break the encoder can emit.
func encodeSubject(v string) string {
	return mime.QEncoding.Encode("UTF-8", v)
}

// encodeBody returns body as quoted-printable (RFC 2045) with CRLF line breaks and lines of at
// most 76 characters. Text mode turns each "\n" into CRLF, so callers keep plain "\n" text.
func encodeBody(body string) ([]byte, error) {
	var buf bytes.Buffer
	w := quotedprintable.NewWriter(&buf)
	if _, err := w.Write([]byte(strings.ReplaceAll(body, "\r\n", "\n"))); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// message is one email, built as plain text UTF-8 (quoted-printable body, RFC 2047 subject) by
// hand so no header comes from a template.
type message struct {
	from, to, subject, body string
	date                    time.Time
	id                      string // Message-ID without the angle brackets
}

func (m message) bytes() ([]byte, error) {
	for _, addr := range []string{m.from, m.to} {
		if err := checkAddress(addr); err != nil {
			return nil, err
		}
	}
	if err := checkSubject(m.subject); err != nil {
		return nil, err
	}
	if err := checkHeaderValue(m.id); err != nil {
		return nil, err
	}
	body, err := encodeBody(m.body)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	header := func(name, value string) { b.WriteString(name + ": " + value + "\r\n") }
	header("From", "<"+m.from+">")
	header("To", "<"+m.to+">")
	header("Subject", encodeSubject(m.subject))
	header("Date", m.date.UTC().Format(time.RFC1123Z))
	header("Message-ID", "<"+m.id+">")
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")
	b.Write(body)
	return []byte(b.String()), nil
}

// newMessageID returns 16 random bytes in hex followed by "@" and the host of the sender address.
func newMessageID(fromAddr string) (string, error) {
	_, host, _ := strings.Cut(fromAddr, "@")
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw) + "@" + host, nil
}

func verificationBody(m domain.OTPMail) string {
	return "รหัสยืนยันของคุณคือ " + m.OTP.Reveal() + "\n\n" +
		"รหัสนี้ใช้ได้ถึง " + m.ExpiresAt.In(thaiZone).Format(otpExpiryLayout) + " น. (เวลาประเทศไทย) ห้ามบอกรหัสนี้กับผู้อื่น\n\n" +
		"รหัสอ้างอิง: " + m.ChallengeID.String() + "\n\n" +
		"หากคุณไม่ได้สมัครบัญชี ไม่ต้องทำอะไร\n"
}

func registrationAttemptBody() string {
	return "มีผู้พยายามสร้างบัญชีด้วยอีเมลนี้ แต่อีเมลนี้มีบัญชีอยู่แล้ว\n\n" +
		"หากเป็นคุณ ให้เข้าสู่ระบบแทน หรือใช้ตัวเลือก \"ลืมรหัสผ่าน\" หากจำรหัสผ่านไม่ได้\n" +
		"หากไม่ใช่คุณ ไม่ต้องทำอะไร\n"
}
