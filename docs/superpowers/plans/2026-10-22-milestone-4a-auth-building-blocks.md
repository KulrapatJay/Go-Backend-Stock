# Milestone 4a — ชิ้นส่วนพื้นฐานของ auth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** สร้างชิ้นส่วนที่ `AuthService` และ `EmailVerificationService` (แผน 4b) ต้องใช้ ได้แก่ password policy + blocklist, Argon2id hasher, OTP, session/CSRF token, rate-limit keys และ rules, client IP, pacer, config ใหม่ และ Mailer (SMTP + dispatcher เบื้องหลัง) โดยยังไม่เปิด endpoint ใหม่

**Architecture:** ชนิดข้อมูลที่ตรวจแล้ว (`Password`, `OTPCode`, `OTPMail`, `MaskIP`) อยู่ใน `internal/core/domain`; helper ด้านความปลอดภัยเป็น concrete struct ใน `internal/core/service` (ไม่มี interface เพราะไม่ใช่ขอบจริง) ที่รับ `port.Clock` และ secret ผ่าน constructor; `Mailer` เป็น port ใหม่ใน `internal/core/port` ที่ `internal/adapter/mail` implement ด้วย `SMTPMailer` (net/smtp) และ `Dispatcher` (คิวเบื้องหลัง); การอ่าน IP ของ client อยู่ใน HTTP adapter; config ใหม่โหลดและ validate ใน `internal/config` โดย secret ใช้ชนิด `config.Secret` ที่พิมพ์ไม่ออก

**Tech Stack:** Go 1.27 (go.mod `go 1.27.1`), `golang.org/x/crypto/argon2` (v0.54.0 มีใน go.mod แล้วเป็น indirect — แผนนี้ทำให้เป็น direct), `net/smtp` ของ stdlib, Fiber v3.5.0 (ไม่แตะในแผนนี้), golangci-lint v2.14.0, Mailpit v1.31.6 (compose)

**Spec:** [docs/superpowers/specs/2026-10-22-milestone-4-auth-design.md](../specs/2026-10-22-milestone-4-auth-design.md) หัวข้อ 2, 3, 4 (แผน 4a) และ 9 ซึ่งอ้าง [phase 1 spec](../specs/2026-10-05-phase-1-design.md) หัวข้อ 8, 10 และ 13 กับ [AGENTS.md](../../../AGENTS.md)

## Global Constraints

- core (`internal/core/**` รวม `_test.go`) import ได้เฉพาะ standard library, `github.com/shopspring/decimal`, `github.com/google/uuid`, `golang.org/x/crypto/argon2` และ `go-backend-stock/internal/core/...`; `go-oidc`/`oauth2` อยู่ใน adapter เท่านั้น (4c)
- Argon2id: memory 19456 KiB (19 MiB), iterations 2, parallelism 1, salt 16 bytes, key 32 bytes; เก็บเป็น PHC string `$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>` ด้วย base64 แบบไม่มี padding
- รหัสผ่าน: printable ASCII 0x21–0x7E ยาว 6–32 ตัว ต้องมีพิมพ์เล็ก พิมพ์ใหญ่ และอักขระพิเศษ ไม่ trim ไม่ normalize; ปฏิเสธเมื่อส่วนหน้า `@` ของอีเมลที่ยาวอย่างน้อย 4 ตัวปรากฏในรหัส (ไม่สนตัวพิมพ์) และเมื่อพิมพ์เล็กแล้วอยู่ใน blocklist; reasons: `password_too_short`, `password_too_long`, `password_non_ascii`, `password_contains_space`, `password_missing_lowercase`, `password_missing_uppercase`, `password_missing_special`, `password_too_common`, `password_contains_email`
- semaphore ของ hasher: ขนาด `PASSWORD_HASH_CONCURRENCY`, รอคิวไม่เกิน 2 วินาทีหรือจน context ยกเลิก แล้วคืน `server_busy` (HTTP 503 + `Retry-After: 1` ทำใน 4b) พร้อม warn log และไม่สร้าง audit; ครอบทุก hash/verify รวม dummy hash
- OTP: 6 หลักจาก `crypto/rand` ช่วง 0–999999 รวมศูนย์นำหน้า; digest = HMAC-SHA-256(`OTP_SECRET`, `"otp" ‖ 0x00 ‖ purpose ‖ 0x00 ‖ challengeID ‖ 0x00 ‖ userID ‖ 0x00 ‖ otp`); เทียบด้วย `hmac.Equal`; input ต้องเป็นเลข ASCII 6 ตัวพอดี
- session token: 32 bytes จาก `crypto/rand` ส่งเป็น base64url ไม่มี padding; เก็บ SHA-256 ของ bytes (ไม่ใช่ของ string)
- CSRF token: base64url(`version (1 byte = 1)` ‖ `expiry unix seconds (8 bytes big-endian)` ‖ `salt (16 bytes)` ‖ `mac (32 bytes)`) โดย mac = HMAC-SHA-256(`CSRF_SECRET`, `"csrf" ‖ 0x00 ‖ binding ‖ 0x00 ‖ expiry ‖ salt`); binding = `session:<session ID>` หรือ `pre:<nonce>`; อายุ 1 ชั่วโมง; token ที่ผิดทุกแบบคืน `csrf_failed` ข้อความเดียวกัน
- rate-limit key = HMAC-SHA-256(`RATE_LIMIT_SECRET`, `scope ‖ 0x00 ‖ value`) โดย value เป็น normalized email หรือ IP เต็ม; rules สร้างผ่าน `domain.NewRateLimitRule` เท่านั้น
- client IP: ใช้ remote address; ถ้าอยู่ใน `TRUSTED_PROXY_CIDRS` จึงอ่าน `X-Forwarded-For` จากขวาไปซ้ายและใช้ address แรกที่ไม่อยู่ใน trusted CIDRs; `MaskIP` = IPv4 /24, IPv6 /48; User-Agent ตัดที่ 256 bytes และแทนอักขระควบคุม
- เวลาที่ service ส่งเข้า repository ตัดที่ microsecond (`Truncate(time.Microsecond)`)
- `Pacer`: floor 500 ms + jitter สุ่ม 0–100 ms เป็นค่าเริ่มต้น; ไม่รอเกิน context deadline
- Mailer: คิวขนาด 100; log ต้องไม่มี OTP, ที่อยู่อีเมลเต็ม หรือ secret; ปฏิเสธที่อยู่/header ที่มี CR, LF หรือ `,;<>"`
- `ParseEmail` ปฏิเสธ `"(),:;<>[\]` เป็น `invalid_input`
- config: `SESSION_TTL` 168h (1h–720h); `PASSWORD_HASH_CONCURRENCY` 4 (1–64); `AUTH_RESPONSE_FLOOR` 500ms (0–5s); `AUTH_RESPONSE_JITTER` 100ms (0–1s); `OTP_MAX_ATTEMPTS` 5 (1–20); `OTP_RESEND_COOLDOWN` 60s; `OTP_REQUESTS_PER_HOUR_EMAIL` 5; `OTP_REQUESTS_PER_HOUR_IP` 20; `LOGIN_ATTEMPTS_PER_EMAIL` 10/15 นาที; `LOGIN_ATTEMPTS_PER_IP` 30/15 นาที; `MAIL_SEND_TIMEOUT` 10s; secret ต้องยาวอย่างน้อย 32 bytes, production ห้ามขึ้นต้นด้วย `dev-only-` และสี่ค่าต้องไม่ซ้ำกัน; production ห้าม `SMTP_TLS=none` และต้องมี `SMTP_FROM`
- ห้ามแก้ migration, `docs/api/` และ `internal/adapter/postgres/sqlc/`; แผนนี้ไม่มี migration ใหม่ ไม่มี endpoint ใหม่ และไม่เปลี่ยน `main.go`
- ต้องเห็น test ล้มก่อนแล้วจึงเขียนโค้ดทุก task; แต่ละ task จบด้วย `gofmt -l` ที่ไม่พิมพ์อะไร (ถ้าพิมพ์ชื่อไฟล์ ให้ `gofmt -w` ไฟล์นั้นแล้วดู diff ก่อน) และ `go vet` ผ่าน
- commit message ไม่มี `Co-Authored-By` หรือชื่อ Claude; commit ในเครื่องเท่านั้น ห้าม push
- รันทุกคำสั่งที่ root ของ repo บน branch `feature/creat_login_and_register`; `go` ที่ใช้คือ `/usr/local/go/bin/go` (1.27.2) และ `make` target ใช้เครื่องมือใน `./bin`

## Review Focus

1. **ตัวเลขและอักขระที่ไม่ใช่ ASCII** — ผู้ใช้ไทยพิมพ์ OTP เป็นเลขไทย (`๑๒๓๔๕๖`) หรือเลขอารบิกอินเดีย, รหัสผ่านเป็นภาษาไทย/มีช่องว่างพิเศษ (NBSP) ต้องถูกปฏิเสธด้วย reason ที่ชี้ตรงจุด ไม่ผ่านไปเทียบ HMAC/hash แล้วล้มแบบลึกลับ — Task 1 (`TestParsePassword` กรณี Thai/NBSP), Task 3 (`TestParseOTPRejectsNonASCIIDigits`)
2. **`X-Forwarded-For` ปลอมและ hop ที่อ่านไม่ได้** — client ส่ง header เองเข้ามาตรง ๆ, ใส่ IP ปลอมไว้ซ้ายสุดผ่าน proxy ที่เชื่อถือ หรือใส่ขยะ ต้องไม่ทำให้หลบ rate limit ได้ — Task 11 (`TestClientIPResolver`)
3. **ความลับถูกพิมพ์ออกทาง `%v`, `%+v`, `%#v`, slog หรือ JSON** — `Password`, `OTPCode`, `OTPMail`, `Config` และ `Secret` — Task 1, 3, 10
4. **CR/LF และอักขระซ่อนใน header อีเมล, User-Agent และ log ของ dispatcher** — ที่อยู่ที่แทรก `Bcc:` ได้, UA ที่มี `\n` หรือ bidi override, error ของ SMTP ที่มีที่อยู่อีเมลหลุดลง log — Task 2, 3 (`TestSanitizeUserAgent`), 12 (`TestCheckAddress`), 13 (`TestDispatcherLogsNeverContainAddressOrOTP`)
5. **token ที่ encode ต่างกันแต่ได้ bytes เดียวกัน และ hash ที่เก็บไว้มี parameters ผิดปกติ** — session token แบบ base64 ไม่ canonical ทำให้สร้าง "session เดียวกัน" ได้หลายสตริง; hash เสียที่มี `m=` ใหญ่ทำให้ Verify กิน RAM — Task 5 (`TestVerifyRejectsOversizedParameters`), Task 7 (`TestHashSessionTokenRejectsNonCanonicalEncoding`)

---

## File Structure

| ไฟล์ | หน้าที่ | Task |
| --- | --- | --- |
| `internal/core/domain/errors.go` (+test) | `Error.Reasons`, `NewReasonsError` | 1 |
| `internal/core/domain/password.go` (+test), `echo_test.go` | `Password`, `PasswordReason`, `ParsePassword`, `ParsePasswordChecked` | 1 |
| `internal/core/domain/email.go`, `text_test.go` | `ParseEmail` ปฏิเสธอักขระ mail syntax และอักขระซ่อน | 2 |
| `internal/core/domain/otp.go`, `mail.go`, `client.go` (+tests) | `OTPCode`, `ParseOTP`, `OTPMail`, `MaskIP`, `SanitizeUserAgent` | 3 |
| `internal/core/service/doc.go`, `blocklist.go`, `password_policy.go` (+tests), `scripts/build_blocklist.go` | กรอง/โหลด blocklist และ `PasswordPolicy` | 4 |
| `internal/core/service/password.go` (+test), `internal/core/boundary_test.go`, `go.mod` | `PasswordHasher` (Argon2id + semaphore + dummy hash) | 5 |
| `internal/core/service/otp.go` (+test) | `GenerateOTP`, `OTPCodec` | 6 |
| `internal/core/service/session.go`, `csrf.go`, `clock_test.go` (+tests) | session token, `CSRFCodec` | 7 |
| `internal/core/service/ratelimit.go` (+test) | `RateLimitKeys`, `RateLimitPolicy` | 8 |
| `internal/core/service/timing.go` (+test) | `Pacer`, `NowForStorage` | 9 |
| `internal/config/auth.go` (+test), `config.go`, `config_test.go`, `.env.example`, `docker-compose.yml` | config ใหม่, `Secret`, redaction | 10 |
| `internal/adapter/http/clientip.go` (+test) | `ClientIPResolver` | 11 |
| `internal/core/port/mailer.go`, `internal/adapter/mail/message.go`, `smtp.go` (+tests) | `Mailer` port, `SMTPMailer` | 12 |
| `internal/adapter/mail/dispatcher.go` (+test) | `Dispatcher` | 13 |
| `internal/core/service/blocklist_embed.go`, `blocklist.txt`, `NOTICE` (+test) | blocklist จริง (ต้องได้รับอนุญาตดาวน์โหลดก่อน) | 14 |
| `internal/adapter/mail/mailpit_integration_test.go`, `Makefile` | ส่งถึง Mailpit จริง | 15 |
| `docs/adr/0014…`, `0015…`, `README.md`, spec, roadmap | เอกสารและตรวจรับ | 16 |

---

### Task 1: `Error.Reasons` และ `ParsePassword`

**Files:**
- Modify: `internal/core/domain/errors.go`
- Create: `internal/core/domain/password.go`, `internal/core/domain/password_test.go`, `internal/core/domain/errors_reasons_test.go`
- Modify: `internal/core/domain/echo_test.go`

**Interfaces:**
- Consumes: `domain.Email` (`String()`), `invalidInput`, `redacted` (ค่าคงที่ `"[REDACTED]"` ใน `user.go`)
- Produces:
  - `Error.Reasons []string` และ `func NewReasonsError(code ErrorCode, message string, reasons ...string) *Error`
  - `type PasswordReason string` กับค่าคงที่ `PasswordReasonTooShort`, `PasswordReasonTooLong`, `PasswordReasonNonASCII`, `PasswordReasonContainsSpace`, `PasswordReasonMissingLowercase`, `PasswordReasonMissingUppercase`, `PasswordReasonMissingSpecial`, `PasswordReasonTooCommon`, `PasswordReasonContainsEmail`
  - `type Password struct{...}` (`String()`/`GoString()`/`MarshalJSON()` ได้ `[REDACTED]`, `Reveal() string`, `IsZero() bool`)
  - `func ParsePassword(raw string, email Email) (Password, error)` — ไม่ตรวจ blocklist
  - `func ParsePasswordChecked(raw string, email Email, isCommon func(lowercase string) bool) (Password, error)` — `isCommon` รับรหัสที่แปลงเป็นพิมพ์เล็กแล้ว; `nil` = ไม่ตรวจ

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/core/domain/errors_reasons_test.go`:

```go
package domain_test

import (
	"errors"
	"testing"

	"go-backend-stock/internal/core/domain"
)

func TestNewReasonsErrorCopiesReasons(t *testing.T) {
	in := []string{"a", "b"}
	err := domain.NewReasonsError(domain.CodeInvalidInput, "x is wrong", in...)
	in[0] = "changed"

	if err.Reasons[0] != "a" || len(err.Reasons) != 2 {
		t.Errorf("Reasons = %v, want a copy of [a b]", err.Reasons)
	}
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Error("an error with reasons must still match its sentinel by code")
	}
	if plain := domain.NewError(domain.CodeInvalidInput, "x"); plain.Reasons != nil {
		t.Errorf("NewError Reasons = %v, want nil", plain.Reasons)
	}
}
```

สร้าง `internal/core/domain/password_test.go`:

```go
package domain_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"go-backend-stock/internal/core/domain"
)

func passwordTestEmail(t *testing.T, s string) domain.Email {
	t.Helper()
	e, err := domain.ParseEmail(s)
	if err != nil {
		t.Fatalf("ParseEmail(%q) error = %v", s, err)
	}
	return e
}

// reasonsOf returns the reason codes of an invalid_input password error, or nil when err is nil.
func reasonsOf(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		return nil
	}
	requireInvalidInput(t, err, "password")
	var de *domain.Error
	if !errors.As(err, &de) {
		t.Fatalf("error = %v (%T), want *domain.Error", err, err)
	}
	return de.Reasons
}

func TestParsePassword(t *testing.T) {
	const (
		short     = "password_too_short"
		long      = "password_too_long"
		nonASCII  = "password_non_ascii"
		space     = "password_contains_space"
		noLower   = "password_missing_lowercase"
		noUpper   = "password_missing_uppercase"
		noSpecial = "password_missing_special"
		withEmail = "password_contains_email"
	)
	tests := []struct {
		name  string
		email string // default somebody@example.com
		in    string
		want  []string
	}{
		{name: "valid minimum length", in: "Ab!def"},
		{name: "valid maximum length", in: "Aa!" + strings.Repeat("x", 29)},
		{name: "too long", in: "Aa!" + strings.Repeat("x", 30), want: []string{long}},
		{name: "too short", in: "Ab!", want: []string{short}},
		{name: "empty", in: "", want: []string{short, noLower, noUpper, noSpecial}},
		{name: "only lowercase", in: "abcdefgh", want: []string{noUpper, noSpecial}},
		{name: "no lowercase", in: "ABCDE!12", want: []string{noLower}},
		{name: "digits are not special characters", in: "Abcdef12", want: []string{noSpecial}},
		{name: "space", in: "Abc d!ef", want: []string{space}},
		{name: "tab", in: "Abc\td!ef", want: []string{space}},
		{name: "no-break space", in: "Abc d!ef", want: []string{nonASCII, space}},
		{name: "accented letter", in: "Pässword!1", want: []string{nonASCII}},
		{name: "thai only", in: "รหัสผ่าน", want: []string{nonASCII, noLower, noUpper, noSpecial}},
		{name: "control character", in: "Abc!12\x00x", want: []string{nonASCII}},
		{name: "invalid utf-8", in: "Abc!12\xffx", want: []string{nonASCII}},
		{name: "contains the email local part", in: "XSomeBody!1", want: []string{withEmail}},
		{name: "email local part in other case", in: "SOMEBODY-a1", want: []string{withEmail}},
		{name: "email local part shorter than four", email: "abc@example.com", in: "Abc!1234"},
		{name: "several reasons in a stable order", in: "ab", want: []string{short, noUpper, noSpecial}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email := tt.email
			if email == "" {
				email = "somebody@example.com"
			}
			p, err := domain.ParsePassword(tt.in, passwordTestEmail(t, email))
			got := reasonsOf(t, err)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("reasons = %v, want %v", got, tt.want)
			}
			if err == nil && p.Reveal() != tt.in {
				t.Errorf("Reveal() did not return the accepted password")
			}
		})
	}
}

func TestParsePasswordWithoutEmail(t *testing.T) {
	if _, err := domain.ParsePassword("Ab!def", domain.Email{}); err != nil {
		t.Fatalf("ParsePassword() with a zero email error = %v", err)
	}
}

func TestParsePasswordCheckedAsksTheCommonListWithLowercase(t *testing.T) {
	email := passwordTestEmail(t, "somebody@example.com")
	var seen []string
	common := func(lowercase string) bool {
		seen = append(seen, lowercase)
		return lowercase == "p@ssw0rd"
	}

	_, err := domain.ParsePasswordChecked("P@ssw0rd", email, common)
	if got := reasonsOf(t, err); !slices.Equal(got, []string{"password_too_common"}) {
		t.Fatalf("reasons = %v, want [password_too_common]", got)
	}
	if !slices.Equal(seen, []string{"p@ssw0rd"}) {
		t.Errorf("common list was asked %v, want [p@ssw0rd]", seen)
	}

	if _, err := domain.ParsePasswordChecked("Zq9!kVx1", email, common); err != nil {
		t.Errorf("a password that is not on the list error = %v", err)
	}
	if _, err := domain.ParsePasswordChecked("P@ssw0rd", email, nil); err != nil {
		t.Errorf("a nil common list must skip the check, error = %v", err)
	}
}

func TestPasswordNeverPrintsItself(t *testing.T) {
	const secret = "Zq9!kVx1"
	p, err := domain.ParsePassword(secret, domain.Email{})
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{
		fmt.Sprint(p), fmt.Sprintf("%v", p), fmt.Sprintf("%+v", p), fmt.Sprintf("%#v", p), fmt.Sprintf("%q", p),
	} {
		if strings.Contains(out, secret) {
			t.Errorf("formatted password %q leaks the secret", out)
		}
	}
	data, err := json.Marshal(struct{ Password domain.Password }{p})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || !strings.Contains(string(data), "[REDACTED]") {
		t.Errorf("JSON = %s, want the secret redacted", data)
	}
	if p.Reveal() != secret || p.IsZero() || !(domain.Password{}).IsZero() {
		t.Error("Reveal/IsZero misbehave")
	}
}
```

แก้ `internal/core/domain/echo_test.go` — แทน

```go
		"ParseEmail":    func(s string) error { _, err := domain.ParseEmail(s + " x"); return err },
```

ด้วย

```go
		"ParseEmail":    func(s string) error { _, err := domain.ParseEmail(s + " x"); return err },
		"ParsePassword": func(s string) error { _, err := domain.ParsePassword(s, domain.Email{}); return err },
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/domain/ -run 'TestNewReasonsError|TestParsePassword|TestPasswordNever|TestErrorsNeverEcho' -count=1`
Expected: build fail — `undefined: domain.NewReasonsError`, `undefined: domain.ParsePassword`

- [ ] **Step 3: เขียนโค้ด**

แก้ `internal/core/domain/errors.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
type Error struct {
	Code    ErrorCode
	Message string
}
```

   ด้วย

```go
type Error struct {
	Code    ErrorCode
	Message string
	// Reasons are stable codes that say which rules a value broke, such as "password_too_short",
	// so a client can point at the exact problem. Optional; the HTTP adapter returns them as
	// error.details.
	Reasons []string
}
```

2. แทน

```go
func NewError(code ErrorCode, message string) *Error {
	return &Error{Code: code, Message: message}
}
```

   ด้วย

```go
func NewError(code ErrorCode, message string) *Error {
	return &Error{Code: code, Message: message}
}

// NewReasonsError returns an *Error that also lists the rule codes the value broke. It copies
// reasons so the caller may reuse the slice.
func NewReasonsError(code ErrorCode, message string, reasons ...string) *Error {
	return &Error{Code: code, Message: message, Reasons: append([]string(nil), reasons...)}
}
```

สร้าง `internal/core/domain/password.go`:

```go
package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	minPasswordRunes = 6
	maxPasswordRunes = 32
	// minEmailLocalPartRunes is how long the part of the email before "@" must be before a
	// password containing it is rejected; shorter parts would match by chance too often.
	minEmailLocalPartRunes = 4
)

// PasswordReason is a stable code for one rule a password broke (phase 1 spec section 10).
type PasswordReason string

const (
	PasswordReasonTooShort         PasswordReason = "password_too_short"
	PasswordReasonTooLong          PasswordReason = "password_too_long"
	PasswordReasonNonASCII         PasswordReason = "password_non_ascii"
	PasswordReasonContainsSpace    PasswordReason = "password_contains_space"
	PasswordReasonMissingLowercase PasswordReason = "password_missing_lowercase"
	PasswordReasonMissingUppercase PasswordReason = "password_missing_uppercase"
	PasswordReasonMissingSpecial   PasswordReason = "password_missing_special"
	PasswordReasonTooCommon        PasswordReason = "password_too_common"
	PasswordReasonContainsEmail    PasswordReason = "password_contains_email"
)

// Password is a password that passed the policy. It prints, formats and marshals as [REDACTED];
// Reveal returns the real value for hashing only.
type Password struct{ value string }

func (Password) String() string   { return redacted }
func (Password) GoString() string { return redacted }

// MarshalJSON keeps the password out of JSON logs and responses.
func (Password) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// Reveal returns the password. Call it only to hash the value.
func (p Password) Reveal() string { return p.value }

// IsZero reports whether p is the zero value (no password).
func (p Password) IsZero() bool { return p.value == "" }

// ParsePassword checks raw against every password rule except the common-password list, which
// needs a data file and is checked by service.PasswordPolicy through ParsePasswordChecked. The
// error is invalid_input with one reason code per broken rule, all at once and in a stable order.
// Used for register and password reset; login never calls it (phase 1 spec section 10).
func ParsePassword(raw string, email Email) (Password, error) {
	return ParsePasswordChecked(raw, email, nil)
}

// ParsePasswordChecked is ParsePassword plus the common-password check. isCommon receives the
// password converted to lower case; nil skips the check.
func ParsePasswordChecked(raw string, email Email, isCommon func(lowercase string) bool) (Password, error) {
	reasons := passwordReasons(raw, email, isCommon)
	if len(reasons) > 0 {
		return Password{}, NewReasonsError(CodeInvalidInput, "password does not meet the requirements", reasons...)
	}
	return Password{value: raw}, nil
}

func passwordReasons(raw string, email Email, isCommon func(lowercase string) bool) []string {
	var hasLower, hasUpper, hasSpecial, hasSpace, hasNonASCII bool
	for _, r := range raw {
		space := unicode.IsSpace(r)
		hasSpace = hasSpace || space
		switch {
		case r > 0x7e, r < 0x21 && !space: // outside printable ASCII; plain whitespace is reported as a space
			hasNonASCII = true
		case space:
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r < '0' || r > '9': // the remaining printable ASCII is punctuation
			hasSpecial = true
		}
	}

	var reasons []string
	add := func(broken bool, reason PasswordReason) {
		if broken {
			reasons = append(reasons, string(reason))
		}
	}
	n := utf8.RuneCountInString(raw)
	lower := strings.ToLower(raw)
	add(n < minPasswordRunes, PasswordReasonTooShort)
	add(n > maxPasswordRunes, PasswordReasonTooLong)
	add(hasNonASCII, PasswordReasonNonASCII)
	add(hasSpace, PasswordReasonContainsSpace)
	add(!hasLower, PasswordReasonMissingLowercase)
	add(!hasUpper, PasswordReasonMissingUppercase)
	add(!hasSpecial, PasswordReasonMissingSpecial)
	add(isCommon != nil && isCommon(lower), PasswordReasonTooCommon)
	add(containsEmailLocalPart(lower, email), PasswordReasonContainsEmail)
	return reasons
}

// containsEmailLocalPart reports whether lower contains the part of the email before "@", when
// that part is long enough to be meaningful. Email is already lower case.
func containsEmailLocalPart(lower string, email Email) bool {
	local, _, _ := strings.Cut(email.String(), "@")
	return utf8.RuneCountInString(local) >= minEmailLocalPartRunes && strings.Contains(lower, local)
}
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/core/domain; go vet ./internal/core/domain/ && go test ./internal/core/domain/ -count=1`
Expected: `gofmt -l` ไม่พิมพ์อะไร, test ทั้งแพ็กเกจผ่าน

- [ ] **Step 5: Commit**

```bash
git add internal/core/domain
git commit -m "feat(domain): add password policy with per-rule reasons"
```

---

### Task 2: `ParseEmail` ปฏิเสธอักขระ mail syntax

**Files:**
- Modify: `internal/core/domain/email.go`, `internal/core/domain/text_test.go`

**Interfaces:**
- Consumes: `hasHidden` (ใน `text.go`), `invalidInput`
- Produces: `ParseEmail` ปฏิเสธ `"(),:;<>[\]` และอักขระซ่อน (format characters, bidi controls) เป็น `invalid_input` ที่ขึ้นต้นด้วย `email`; ปิดข้อส่งต่อจาก milestone 2

- [ ] **Step 1: เขียน failing test**

แก้ `internal/core/domain/text_test.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
		"ÄLICE@EXAMPLE.COM":                       "älice@example.com",
```

   ด้วย

```go
		"ÄLICE@EXAMPLE.COM":                       "älice@example.com",
		"O'Brien@Example.com":                     "o'brien@example.com", // an apostrophe is a legal local-part character
```

2. แทน

```go
		"user\x00@example.com", "\xff@example.com",
```

   ด้วย

```go
		"user\x00@example.com", "\xff@example.com",
		"a,b@example.com", "a;b@example.com", "<a@example.com>", `"a"@example.com`, "a(b)@example.com",
		"a:b@example.com", "a[b]@example.com", `a\b@example.com`, "a@exa]mple.com",
		"a​b@example.com", // a zero-width space hides a character
		"a‮b@example.com", // a bidi override reorders the text
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/domain/ -run TestParseEmail -count=1`
Expected: FAIL — `ParseEmail("a,b@example.com")` ไม่คืน error

- [ ] **Step 3: เขียนโค้ด**

แก้ `internal/core/domain/email.go` ตามคู่ต่อไปนี้:

1. แทน

```go
// ParseEmail checks only the shape (one "@" with text on both sides): ownership is proven by
// the verification OTP, not by syntax.
```

   ด้วย

```go
// ParseEmail checks the shape (one "@" with text on both sides) and rejects the characters that
// could change an SMTP command or a mail header, which would let one address address several
// people, and invisible characters: ownership is proven by the verification OTP, not by syntax.
```

2. แทน

```go
		return Email{}, invalidInput("email", "must not contain spaces or control characters")
	}
```

   ด้วย

```go
		return Email{}, invalidInput("email", "must not contain spaces or control characters")
	}
	if strings.ContainsAny(v, `"(),:;<>[\]`) || hasHidden(v) {
		return Email{}, invalidInput("email", `must not contain " ( ) , : ; < > [ \ ] or invisible characters`)
	}
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/core/domain; go test ./internal/core/domain/ ./internal/adapter/postgres/ -count=1`
Expected: ผ่านทั้งคู่ (test ของ postgres ที่ใช้ `ParseEmail` ยัง skip integration ตามปกติ)

- [ ] **Step 5: Commit**

```bash
git add internal/core/domain
git commit -m "feat(domain): reject mail syntax characters and invisible characters in emails"
```

---

### Task 3: `OTPCode`, `OTPMail`, `MaskIP` และ `SanitizeUserAgent`

**Files:**
- Modify: `internal/core/domain/otp.go`, `internal/core/domain/echo_test.go`
- Create: `internal/core/domain/mail.go`, `internal/core/domain/client.go`, `internal/core/domain/otp_code_test.go`, `internal/core/domain/client_test.go`

**Interfaces:**
- Consumes: `redacted`, `invalidInput`, `invariant`, `isHidden`, `Email`, `ID`
- Produces:
  - `const OTPLength = 6`; `type OTPCode struct{...}` (`String()`/`GoString()`/`MarshalJSON()` ได้ `[REDACTED]`, `Reveal() string`)
  - `func ParseOTP(s string) (OTPCode, error)` — เลข ASCII 6 ตัวพอดี ไม่เช่นนั้น `invalid_input` ที่ขึ้นต้นด้วย `otp`
  - `func OTPCodeFromNumber(n int64) (OTPCode, error)` — 0..999999 → 6 ตัวรวมศูนย์นำหน้า; นอกช่วงเป็น `internal`
  - `type OTPMail struct{ To Email; OTP OTPCode; ChallengeID ID; ExpiresAt time.Time }` ที่ `String()`/`GoString()` ไม่แสดง OTP และไม่แสดงที่อยู่
  - `func MaskIP(addr netip.Addr) string` — IPv4 `a.b.c.0/24`, IPv6 /48, zero → `""`
  - `const MaxUserAgentBytes = 256`; `func SanitizeUserAgent(s string) string`

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/core/domain/otp_code_test.go`:

```go
package domain_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestParseOTP(t *testing.T) {
	for _, in := range []string{"000000", "012345", "999999"} {
		c, err := domain.ParseOTP(in)
		if err != nil || c.Reveal() != in {
			t.Errorf("ParseOTP(%q) = %q, %v; want it accepted", in, c.Reveal(), err)
		}
	}
	for _, in := range []string{"", "12345", "1234567", "12345a", " 12345", "12345 ", "12 345", "-12345", "+12345", "1.2345"} {
		_, err := domain.ParseOTP(in)
		requireInvalidInput(t, err, "otp")
	}
}

// Thai and Arabic-Indic digits are Unicode digits, so a check built on unicode.IsDigit would
// accept them and the HMAC comparison would then fail with no useful message.
func TestParseOTPRejectsNonASCIIDigits(t *testing.T) {
	for _, in := range []string{"๑๒๓๔๕๖", "١٢٣٤٥٦", "１２３４５６", "12345٦"} {
		_, err := domain.ParseOTP(in)
		requireInvalidInput(t, err, "otp")
	}
}

func TestOTPCodeFromNumber(t *testing.T) {
	for n, want := range map[int64]string{0: "000000", 7: "000007", 42: "000042", 123456: "123456", 999999: "999999"} {
		c, err := domain.OTPCodeFromNumber(n)
		if err != nil || c.Reveal() != want {
			t.Errorf("OTPCodeFromNumber(%d) = %q, %v; want %q", n, c.Reveal(), err, want)
		}
	}
	for _, n := range []int64{-1, 1_000_000} {
		_, err := domain.OTPCodeFromNumber(n)
		requireCode(t, err, domain.CodeInternal)
	}
}

func TestOTPCodeAndMailNeverPrintTheCode(t *testing.T) {
	const code = "042731"
	otp, err := domain.ParseOTP(code)
	if err != nil {
		t.Fatal(err)
	}
	to, err := domain.ParseEmail("user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	mail := domain.OTPMail{To: to, OTP: otp, ChallengeID: mustID(t), ExpiresAt: time.Date(2026, 10, 22, 9, 10, 0, 0, time.UTC)}

	var outputs []string
	for _, v := range []any{otp, mail} {
		outputs = append(outputs, fmt.Sprint(v), fmt.Sprintf("%v", v), fmt.Sprintf("%+v", v), fmt.Sprintf("%#v", v))
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, string(data))
	}
	for _, out := range outputs {
		if strings.Contains(out, code) {
			t.Errorf("output %q leaks the OTP", out)
		}
		if strings.Contains(out, "user@example.com") {
			t.Errorf("output %q leaks the address", out)
		}
	}
	if !strings.Contains(fmt.Sprint(mail), mail.ChallengeID.String()) {
		t.Error("OTPMail.String() should name the challenge so log lines can be correlated")
	}
}
```

สร้าง `internal/core/domain/client_test.go`:

```go
package domain_test

import (
	"net/netip"
	"strings"
	"testing"
	"unicode/utf8"

	"go-backend-stock/internal/core/domain"
)

func TestMaskIP(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.77":           "203.0.113.0/24",
		"::ffff:203.0.113.77":    "203.0.113.0/24", // an IPv4 address in IPv6 notation is still IPv4
		"2001:db8:1234:5678::1":  "2001:db8:1234::/48",
		"fe80::1%eth0":           "fe80::/48", // the zone is dropped
		"2001:db8:ffff:ffff::99": "2001:db8:ffff::/48",
	} {
		if got := domain.MaskIP(netip.MustParseAddr(in)); got != want {
			t.Errorf("MaskIP(%s) = %q, want %q", in, got, want)
		}
	}
	if got := domain.MaskIP(netip.Addr{}); got != "" {
		t.Errorf("MaskIP(zero) = %q, want empty", got)
	}
}

func TestSanitizeUserAgent(t *testing.T) {
	if got := domain.SanitizeUserAgent("Mozilla/5.0 (X11; Linux x86_64)"); got != "Mozilla/5.0 (X11; Linux x86_64)" {
		t.Errorf("a normal user agent changed to %q", got)
	}
	if got := domain.SanitizeUserAgent(""); got != "" {
		t.Errorf("empty user agent = %q", got)
	}

	for name, in := range map[string]string{
		"line break":    "Mozilla/5.0\r\nX-Injected: yes",
		"nul":           "a\x00b",
		"escape":        "a\x1b[31mb",
		"c1 control":    "a\u0085b",
		"bidi override": "a‮b",
		"zero width":    "a​b",
	} {
		got := domain.SanitizeUserAgent(in)
		if strings.ContainsAny(got, "\r\n\x00\x1b\u0085‮​") {
			t.Errorf("%s: SanitizeUserAgent(%q) = %q still has the character", name, in, got)
		}
		if got == "" {
			t.Errorf("%s: the user agent should keep its other characters", name)
		}
	}

	if got := domain.SanitizeUserAgent("a\xffb"); !utf8.ValidString(got) {
		t.Errorf("invalid UTF-8 survived: %q", got)
	}
}

func TestSanitizeUserAgentCapsLengthOnARuneBoundary(t *testing.T) {
	if got := domain.SanitizeUserAgent(strings.Repeat("a", 1000)); len(got) != domain.MaxUserAgentBytes {
		t.Errorf("len = %d, want %d", len(got), domain.MaxUserAgentBytes)
	}
	// 200 two-byte runes = 400 bytes: the cut at byte 256 falls between runes.
	if got := domain.SanitizeUserAgent(strings.Repeat("é", 200)); len(got) != 256 || !utf8.ValidString(got) {
		t.Errorf("len = %d valid = %v, want 256 bytes of valid UTF-8", len(got), utf8.ValidString(got))
	}
	// One leading byte moves every rune start to an odd offset, so byte 256 is mid-rune.
	if got := domain.SanitizeUserAgent("a" + strings.Repeat("é", 200)); len(got) != 255 || !utf8.ValidString(got) {
		t.Errorf("len = %d valid = %v, want 255 bytes of valid UTF-8", len(got), utf8.ValidString(got))
	}
}
```

แก้ `internal/core/domain/echo_test.go` — แทน

```go
		"ParsePassword": func(s string) error { _, err := domain.ParsePassword(s, domain.Email{}); return err },
```

ด้วย

```go
		"ParsePassword": func(s string) error { _, err := domain.ParsePassword(s, domain.Email{}); return err },
		"ParseOTP":      func(s string) error { _, err := domain.ParseOTP(s); return err },
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/domain/ -run 'TestParseOTP|TestOTPCode|TestMaskIP|TestSanitizeUserAgent|TestErrorsNeverEcho' -count=1`
Expected: build fail — `undefined: domain.ParseOTP`, `domain.OTPMail`, `domain.MaskIP`

- [ ] **Step 3: เขียนโค้ด**

แก้ `internal/core/domain/otp.go` — แทน

```go
import "time"
```

ด้วย

```go
import (
	"strconv"
	"strings"
	"time"
)
```

แล้วต่อท้ายไฟล์ด้วย

```go

// OTPLength is the number of digits in an OTP.
const OTPLength = 6

// OTPCode is a six-digit one-time code. It prints, formats and marshals as [REDACTED]; Reveal
// returns the digits for hashing and for the email body only.
type OTPCode struct{ value string }

func (OTPCode) String() string   { return redacted }
func (OTPCode) GoString() string { return redacted }

// MarshalJSON keeps the code out of JSON logs and responses.
func (OTPCode) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// Reveal returns the digits. Call it only to compute the digest or to write the email.
func (c OTPCode) Reveal() string { return c.value }

// ParseOTP accepts exactly six ASCII digits ("0".."9"). Other Unicode digits such as Thai or
// Arabic-Indic numerals are rejected: they would never match the digest of the issued code.
func ParseOTP(s string) (OTPCode, error) {
	if len(s) != OTPLength {
		return OTPCode{}, invalidInput("otp", "must be 6 digits")
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return OTPCode{}, invalidInput("otp", "must be 6 digits")
		}
	}
	return OTPCode{value: s}, nil
}

// OTPCodeFromNumber formats a number from 0 to 999999 as six digits with leading zeros. The
// server calls it with a value it drew itself, so a number out of range is an internal error.
func OTPCodeFromNumber(n int64) (OTPCode, error) {
	if n < 0 || n > 999999 {
		return OTPCode{}, invariant("otp", "number must be between 0 and 999999")
	}
	s := strconv.FormatInt(n, 10)
	return OTPCode{value: strings.Repeat("0", OTPLength-len(s)) + s}, nil
}
```

สร้าง `internal/core/domain/mail.go`:

```go
package domain

import "time"

// OTPMail is what the Mailer needs to send a one-time code. It never prints the code or the
// address: String and GoString name only the challenge, so a stray log line cannot leak either.
type OTPMail struct {
	To          Email
	OTP         OTPCode
	ChallengeID ID
	ExpiresAt   time.Time
}

func (m OTPMail) String() string   { return "OTPMail{challenge_id=" + m.ChallengeID.String() + "}" }
func (m OTPMail) GoString() string { return m.String() }
```

สร้าง `internal/core/domain/client.go`:

```go
package domain

import (
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxUserAgentBytes is the longest user agent kept in audit metadata.
const MaxUserAgentBytes = 256

// MaskIP returns the network of addr for audit metadata: /24 for IPv4 and /48 for IPv6, such as
// "203.0.113.0/24". It returns "" for the zero Addr. Rate limiting uses the full address instead.
func MaskIP(addr netip.Addr) string {
	if !addr.IsValid() {
		return ""
	}
	addr = addr.Unmap().WithZone("")
	bits := 48
	if addr.Is4() {
		bits = 24
	}
	prefix, err := addr.Prefix(bits)
	if err != nil {
		return ""
	}
	return prefix.String()
}

// SanitizeUserAgent makes a User-Agent header safe to store and log: invalid UTF-8, control
// characters and invisible or bidi characters become a space, and the result is cut to
// MaxUserAgentBytes on a rune boundary.
func SanitizeUserAgent(s string) string {
	s = strings.ToValidUTF8(s, " ")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || isHidden(r) {
			return ' '
		}
		return r
	}, s)
	if len(s) <= MaxUserAgentBytes {
		return s
	}
	cut := MaxUserAgentBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/core/domain; go vet ./internal/core/domain/ && go test ./internal/core/... -count=1`
Expected: ผ่านทั้งหมด (รวม `boundary_test`)

- [ ] **Step 5: Commit**

```bash
git add internal/core/domain
git commit -m "feat(domain): add OTP code, OTP mail and client metadata helpers"
```

---
### Task 4: Blocklist (ตัวกรอง/ตัวโหลด) และ `PasswordPolicy`

แผนนี้แยกโค้ดของ blocklist ออกจากข้อมูล: Task นี้ทำตัวกรอง ตัวโหลด สคริปต์สร้างไฟล์ และ `PasswordPolicy` ที่ทดสอบด้วยรายการเล็ก ๆ ในหน่วยความจำ ส่วนไฟล์ข้อมูลจริง (ต้องดาวน์โหลดจากภายนอก) อยู่ที่ Task 14 หลังผู้ใช้อนุญาต

**Files:**
- Create: `internal/core/service/doc.go`, `internal/core/service/blocklist.go`, `internal/core/service/password_policy.go`, `internal/core/service/blocklist_test.go`, `internal/core/service/password_policy_test.go`, `scripts/build_blocklist.go`

**Interfaces:**
- Consumes: `domain.ParsePasswordChecked`, `domain.Password`, `domain.Email`
- Produces:
  - `func FilterBlocklistEntry(line string) (entry string, ok bool)` — แปลงเป็นพิมพ์เล็กและคืน `ok=true` เฉพาะรายการยาว 6–32 ตัว printable ASCII ไม่มีช่องว่าง มีตัวอักษรและอักขระพิเศษอย่างน้อยอย่างละตัว
  - `func BuildBlocklist(src io.Reader, dst io.Writer) (kept int, err error)` — กรอง ตัดซ้ำ เรียง แล้วเขียนบรรทัดละรายการ
  - `type Blocklist`; `func NewBlocklist(r io.Reader) (*Blocklist, error)` (ว่างเปล่า = error); `(*Blocklist).Contains(lowercase string) bool`; `(*Blocklist).Len() int`
  - `type PasswordPolicy`; `func NewPasswordPolicy(common *Blocklist) (*PasswordPolicy, error)` (nil = error); `(*PasswordPolicy).Parse(raw string, email domain.Email) (domain.Password, error)`

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/core/service/blocklist_test.go`:

```go
package service

import (
	"bytes"
	"strings"
	"testing"
)

func TestFilterBlocklistEntry(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"P@ssw0rd", "p@ssw0rd", true},
		{"ABCDEF!", "abcdef!", true},
		{"abc!12\r\n", "abc!12", true},
		{strings.Repeat("a", 31) + "!", strings.Repeat("a", 31) + "!", true}, // 32 bytes
		{"password", "", false}, // no special character, so no password that passes our rules can equal it
		{"123456!", "", false},  // no letter
		{"a!", "", false},       // shorter than 6
		{strings.Repeat("a", 32) + "!", "", false}, // 33 bytes
		{"pass word!", "", false},                  // space
		{"pässwörd!", "", false},                   // non-ASCII
		{"รหัสผ่าน!", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := FilterBlocklistEntry(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("FilterBlocklistEntry(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestBuildBlocklist(t *testing.T) {
	src := "P@ssw0rd\npassword\nP@SSW0RD\n123456\nqwerty!\nabc!12\r\nabc!12\nzzz!zzz\n"
	var out bytes.Buffer

	n, err := BuildBlocklist(strings.NewReader(src), &out)
	if err != nil {
		t.Fatal(err)
	}
	if want := "abc!12\np@ssw0rd\nqwerty!\nzzz!zzz\n"; out.String() != want || n != 4 {
		t.Errorf("BuildBlocklist() = %d entries %q, want 4 entries %q", n, out.String(), want)
	}
}

func TestNewBlocklist(t *testing.T) {
	list, err := NewBlocklist(strings.NewReader("p@ssw0rd\n\nqwerty!\n"))
	if err != nil {
		t.Fatal(err)
	}
	if list.Len() != 2 || !list.Contains("p@ssw0rd") || !list.Contains("qwerty!") {
		t.Errorf("Len = %d, want both entries and nothing else", list.Len())
	}
	if list.Contains("P@ssw0rd") || list.Contains("other!1") {
		t.Error("Contains takes a lower-case password and matches exactly")
	}

	if _, err := NewBlocklist(strings.NewReader("\n\n")); err == nil {
		t.Error("an empty blocklist must be an error: it means the embedded file is broken")
	}
}

func TestBuiltBlocklistLoads(t *testing.T) {
	var built bytes.Buffer
	if _, err := BuildBlocklist(strings.NewReader("Hello!World\nnope\n"), &built); err != nil {
		t.Fatal(err)
	}
	list, err := NewBlocklist(&built)
	if err != nil || !list.Contains("hello!world") || list.Len() != 1 {
		t.Errorf("round trip: Len = %d, error = %v", list.Len(), err)
	}
}
```

สร้าง `internal/core/service/password_policy_test.go`:

```go
package service

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"go-backend-stock/internal/core/domain"
)

func mustEmail(t *testing.T, s string) domain.Email {
	t.Helper()
	e, err := domain.ParseEmail(s)
	if err != nil {
		t.Fatalf("ParseEmail(%q) error = %v", s, err)
	}
	return e
}

func reasonsOf(t *testing.T, err error) []string {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != domain.CodeInvalidInput {
		t.Fatalf("error = %v, want invalid_input", err)
	}
	return de.Reasons
}

func TestPasswordPolicyParse(t *testing.T) {
	list, err := NewBlocklist(strings.NewReader("p@ssw0rd\nqwerty!1a\n"))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewPasswordPolicy(list)
	if err != nil {
		t.Fatal(err)
	}
	email := mustEmail(t, "somebody@example.com")

	if _, err := policy.Parse("Zq9!kVx1", email); err != nil {
		t.Errorf("a strong password error = %v", err)
	}

	_, err = policy.Parse("p@ssW0rd", email) // matched after lower-casing
	if got := reasonsOf(t, err); !slices.Equal(got, []string{"password_too_common"}) {
		t.Errorf("reasons = %v, want [password_too_common]", got)
	}

	_, err = policy.Parse("p@ssw0rd", email) // the other rules are still reported with it
	if got := reasonsOf(t, err); !slices.Equal(got, []string{"password_missing_uppercase", "password_too_common"}) {
		t.Errorf("reasons = %v, want missing_uppercase and too_common", got)
	}
}

func TestNewPasswordPolicyNeedsABlocklist(t *testing.T) {
	if _, err := NewPasswordPolicy(nil); err == nil {
		t.Error("NewPasswordPolicy(nil) must fail: a nil list would silently turn the check off")
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/service/ -count=1`
Expected: FAIL — `no non-test Go files` / `undefined: FilterBlocklistEntry`

- [ ] **Step 3: เขียนโค้ด**

สร้าง `internal/core/service/doc.go`:

```go
// Package service holds the core's use cases (added from milestone 4b) and the security helpers
// they share: password hashing and policy, OTP, CSRF and session tokens, rate-limit keys and
// response pacing. It depends only on the standard library, golang.org/x/crypto/argon2 and the
// core's own packages.
package service
```

สร้าง `internal/core/service/blocklist.go`:

```go
package service

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

const (
	blocklistMinLength = 6
	blocklistMaxLength = 32
)

// FilterBlocklistEntry normalises one line of a list of common passwords. It returns the entry
// in lower case and true only if some password that passes our other rules (6-32 printable
// ASCII characters without spaces, with a lower-case letter, an upper-case letter and a special
// character) could equal it once lower-cased, which needs a letter and a special character. Every
// other line can never match, so leaving it out gives the same result with a smaller file.
func FilterBlocklistEntry(line string) (string, bool) {
	line = strings.TrimRight(line, "\r\n")
	if len(line) < blocklistMinLength || len(line) > blocklistMaxLength {
		return "", false
	}
	var hasLetter, hasSpecial bool
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c < 0x21 || c > 0x7e:
			return "", false
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			hasLetter = true
		case c >= '0' && c <= '9':
		default:
			hasSpecial = true
		}
	}
	if !hasLetter || !hasSpecial {
		return "", false
	}
	return strings.ToLower(line), true
}

// BuildBlocklist reads a source list, keeps the entries FilterBlocklistEntry accepts, removes
// duplicates and writes them sorted, one per line. It returns how many it wrote.
func BuildBlocklist(src io.Reader, dst io.Writer) (int, error) {
	kept := make(map[string]struct{})
	sc := newLineScanner(src)
	for sc.Scan() {
		if entry, ok := FilterBlocklistEntry(sc.Text()); ok {
			kept[entry] = struct{}{}
		}
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("read source list: %w", err)
	}

	w := bufio.NewWriter(dst)
	entries := slices.Sorted(maps.Keys(kept))
	for _, entry := range entries {
		if _, err := w.WriteString(entry + "\n"); err != nil {
			return 0, fmt.Errorf("write blocklist: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		return 0, fmt.Errorf("write blocklist: %w", err)
	}
	return len(entries), nil
}

// Blocklist is a set of lower-case common passwords, loaded once at startup.
type Blocklist struct{ entries map[string]struct{} }

// NewBlocklist reads one entry per line. An empty list is an error because it means the bundled
// file is missing or broken, and a password check that matches nothing looks like a working one.
func NewBlocklist(r io.Reader) (*Blocklist, error) {
	list := &Blocklist{entries: make(map[string]struct{})}
	sc := newLineScanner(r)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			list.entries[strings.ToLower(line)] = struct{}{}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read blocklist: %w", err)
	}
	if len(list.entries) == 0 {
		return nil, errors.New("blocklist is empty")
	}
	return list, nil
}

// Contains reports whether lowercase is on the list. Callers pass the password already lower-cased.
func (b *Blocklist) Contains(lowercase string) bool {
	_, ok := b.entries[lowercase]
	return ok
}

// Len returns the number of entries.
func (b *Blocklist) Len() int { return len(b.entries) }

func newLineScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return sc
}
```

สร้าง `internal/core/service/password_policy.go`:

```go
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
```

สร้าง `scripts/build_blocklist.go`:

```go
//go:build ignore

// Command build_blocklist filters a list of common passwords down to the entries that can match
// a password our rules accept (service.FilterBlocklistEntry), removes duplicates and writes the
// sorted result. See internal/core/service/NOTICE for the source list and how it was fetched.
//
//	go run scripts/build_blocklist.go -in /path/to/source.txt -out internal/core/service/blocklist.txt
package main

import (
	"flag"
	"fmt"
	"os"

	"go-backend-stock/internal/core/service"
)

func main() {
	in := flag.String("in", "", "source list of common passwords, one per line")
	out := flag.String("out", "internal/core/service/blocklist.txt", "filtered blocklist to write")
	flag.Parse()
	if *in == "" {
		fmt.Fprintln(os.Stderr, "-in is required")
		os.Exit(2)
	}
	if err := run(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(inPath, outPath string) error {
	src, err := os.Open(inPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(outPath)
	if err != nil {
		return err
	}
	kept, buildErr := service.BuildBlocklist(src, dst)
	closeErr := dst.Close()
	if buildErr != nil {
		return buildErr
	}
	if closeErr != nil {
		return closeErr
	}
	fmt.Printf("wrote %d entries to %s\n", kept, outPath)
	return nil
}
```

- [ ] **Step 4: รัน test ให้ผ่าน และลองสคริปต์กับไฟล์เล็ก**

Run:
```bash
gofmt -l internal/core/service scripts; go vet ./internal/core/... && go test ./internal/core/... -count=1
SCRATCH=/private/tmp/claude-501/-Users-markedtr-Docs-MyCode-work-Go-Backend-Stock/302f47cb-762d-48f0-81b1-2d0b3cc098ab/scratchpad
printf 'P@ssw0rd\npassword\nqwerty!\n' > "$SCRATCH/blocklist-in.txt"
go run scripts/build_blocklist.go -in "$SCRATCH/blocklist-in.txt" -out "$SCRATCH/blocklist-out.txt" && cat "$SCRATCH/blocklist-out.txt"
```
Expected: test ผ่าน; สคริปต์พิมพ์ `wrote 2 entries to …` และไฟล์มี `p@ssw0rd` กับ `qwerty!` บรรทัดละรายการ

- [ ] **Step 5: Commit**

```bash
git add internal/core/service scripts/build_blocklist.go
git commit -m "feat(service): add blocklist filter and password policy"
```

---

### Task 5: `PasswordHasher` (Argon2id + semaphore + dummy hash)

**Files:**
- Create: `internal/core/service/password.go`, `internal/core/service/password_test.go`
- Modify: `internal/core/boundary_test.go`, `go.mod`

**Interfaces:**
- Consumes: `domain.Password` (`Reveal`, `IsZero`), `domain.PasswordHash`, `domain.NewError`, `domain.CodeServerBusy`, `domain.CodeInternal`
- Produces:
  - `type Argon2Params struct{ MemoryKiB, Iterations uint32; Parallelism uint8 }`; `var DefaultArgon2Params`
  - `type PasswordHasherConfig struct{ Params Argon2Params; Concurrency int; QueueTimeout time.Duration; Logger *slog.Logger }` (`QueueTimeout` 0 = 2 วินาที)
  - `func NewPasswordHasher(cfg PasswordHasherConfig) (*PasswordHasher, error)` — สร้าง dummy hash ตอนนี้ครั้งเดียว
  - `(*PasswordHasher).Hash(ctx context.Context, password domain.Password) (domain.PasswordHash, error)`
  - `(*PasswordHasher).Verify(ctx context.Context, hash domain.PasswordHash, password string) (bool, error)` — hash ที่ parse ไม่ได้ = `internal` ไม่ใช่ `false`
  - `(*PasswordHasher).VerifyDummy(ctx context.Context, password string) error` — เสียต้นทุนเท่ากับ Verify ไม่ว่าผลเป็นอะไร
  - `(*PasswordHasher).NeedsRehash(hash domain.PasswordHash) (bool, error)`
  - ทุกเมธอดที่รอคิวไม่ได้คืน `domain.CodeServerBusy` และเขียน warn log `event=auth.hash_busy`

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/core/service/password_test.go`:

```go
package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// testArgon2Params keeps unit tests fast; DefaultArgon2Params is exercised once below.
var testArgon2Params = Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1}

func requireCode(t *testing.T, err error, want domain.ErrorCode) *domain.Error {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != want {
		t.Fatalf("error = %v, want *domain.Error with code %s", err, want)
	}
	return de
}

func mustPassword(t *testing.T, s string) domain.Password {
	t.Helper()
	p, err := domain.ParsePassword(s, domain.Email{})
	if err != nil {
		t.Fatalf("ParsePassword(%q) error = %v", s, err)
	}
	return p
}

func newHasher(t *testing.T, params Argon2Params, concurrency int, queueTimeout time.Duration) (*PasswordHasher, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	h, err := NewPasswordHasher(PasswordHasherConfig{
		Params: params, Concurrency: concurrency, QueueTimeout: queueTimeout,
		Logger: slog.New(slog.NewJSONHandler(logs, nil)),
	})
	if err != nil {
		t.Fatalf("NewPasswordHasher() error = %v", err)
	}
	return h, logs
}

// phc builds a well-formed hash string with all-zero salt and key.
func phc(params string, saltLen, keyLen int) domain.PasswordHash {
	enc := base64.RawStdEncoding
	return domain.PasswordHash("$argon2id$v=19$" + params + "$" +
		enc.EncodeToString(make([]byte, saltLen)) + "$" + enc.EncodeToString(make([]byte, keyLen)))
}

func TestHashAndVerify(t *testing.T) {
	h, _ := newHasher(t, testArgon2Params, 2, 0)
	ctx := t.Context()

	hash, err := h.Hash(ctx, mustPassword(t, "Zq9!kVx1"))
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := h.Verify(ctx, hash, "Zq9!kVx1"); err != nil || !ok {
		t.Errorf("Verify(right password) = %v, %v; want true, nil", ok, err)
	}
	if ok, err := h.Verify(ctx, hash, "Zq9!kVx2"); err != nil || ok {
		t.Errorf("Verify(wrong password) = %v, %v; want false, nil", ok, err)
	}
	if ok, err := h.Verify(ctx, hash, ""); err != nil || ok {
		t.Errorf("Verify(empty password) = %v, %v; want false, nil", ok, err)
	}

	again, err := h.Hash(ctx, mustPassword(t, "Zq9!kVx1"))
	if err != nil {
		t.Fatal(err)
	}
	if again == hash {
		t.Error("two hashes of one password must differ (random salt)")
	}
}

func TestHashRejectsZeroPassword(t *testing.T) {
	h, _ := newHasher(t, testArgon2Params, 1, 0)
	_, err := h.Hash(t.Context(), domain.Password{})
	requireCode(t, err, domain.CodeInternal)
}

func TestHashUsesTheConfirmedFormat(t *testing.T) {
	h, _ := newHasher(t, DefaultArgon2Params, 1, 0)
	hash, err := h.Hash(t.Context(), mustPassword(t, "Zq9!kVx1"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(hash)
	if !strings.HasPrefix(s, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash %q does not start with the confirmed parameters", s)
	}
	parts := strings.Split(s, "$") // "", argon2id, v=19, params, salt, key
	if len(parts) != 6 || strings.Contains(parts[4]+parts[5], "=") {
		t.Fatalf("hash %q must have six parts and no base64 padding", s)
	}
	salt, err1 := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	key, err2 := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(salt) != 16 || len(key) != 32 {
		t.Errorf("salt %d bytes (%v), key %d bytes (%v); want 16 and 32", len(salt), err1, len(key), err2)
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	h, _ := newHasher(t, testArgon2Params, 1, 0)
	for name, hash := range map[string]domain.PasswordHash{
		"empty":             "",
		"plain text":        "not a hash",
		"wrong algorithm":   "$argon2i$v=19$m=64,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"wrong version":     domain.PasswordHash(strings.Replace(string(phc("m=64,t=1,p=1", 16, 32)), "v=19", "v=18", 1)),
		"two parameters":    phc("m=64,t=1", 16, 32),
		"parameters order":  phc("t=1,m=64,p=1", 16, 32),
		"text parameter":    phc("m=abc,t=1,p=1", 16, 32),
		"zero iterations":   phc("m=64,t=0,p=1", 16, 32),
		"zero parallelism":  phc("m=64,t=1,p=0", 16, 32),
		"memory below 8p":   phc("m=15,t=1,p=2", 16, 32),
		"bad salt encoding": domain.PasswordHash("$argon2id$v=19$m=64,t=1,p=1$%%%$" + strings.Repeat("A", 43)),
		"padded key":        domain.PasswordHash(string(phc("m=64,t=1,p=1", 16, 32)) + "="),
	} {
		ok, err := h.Verify(t.Context(), hash, "Zq9!kVx1")
		if ok {
			t.Errorf("%s: Verify() = true for a malformed hash", name)
		}
		requireCode(t, err, domain.CodeInternal)
		if _, err := h.NeedsRehash(hash); err == nil {
			t.Errorf("%s: NeedsRehash() accepted a malformed hash", name)
		}
	}
}

// A corrupt or hostile stored hash must not make Verify allocate gigabytes: the parameters are
// bounded before any hashing starts.
func TestVerifyRejectsOversizedParameters(t *testing.T) {
	h, _ := newHasher(t, testArgon2Params, 1, 0)
	for name, hash := range map[string]domain.PasswordHash{
		"memory":      phc("m=262145,t=1,p=1", 16, 32),
		"huge memory": phc("m=4194304,t=2,p=1", 16, 32),
		"iterations":  phc("m=64,t=11,p=1", 16, 32),
		"parallelism": phc("m=64,t=1,p=9", 16, 32),
		"short salt":  phc("m=64,t=1,p=1", 7, 32),
		"long salt":   phc("m=64,t=1,p=1", 65, 32),
		"short key":   phc("m=64,t=1,p=1", 16, 15),
		"long key":    phc("m=64,t=1,p=1", 16, 65),
	} {
		start := time.Now()
		_, err := h.Verify(t.Context(), hash, "Zq9!kVx1")
		requireCode(t, err, domain.CodeInternal)
		if time.Since(start) > time.Second {
			t.Errorf("%s: rejecting took %v, it must not start hashing", name, time.Since(start))
		}
	}

	// The documented upper bounds themselves are accepted by the parser (not hashed here).
	if _, _, _, err := decodePHC(string(phc("m=262144,t=10,p=8", 64, 64))); err != nil {
		t.Errorf("decodePHC() rejected parameters at the bound: %v", err)
	}
}

func TestVerifyWellFormedButWrongHashIsFalseNotError(t *testing.T) {
	h, _ := newHasher(t, testArgon2Params, 1, 0)
	ok, err := h.Verify(t.Context(), phc("m=64,t=1,p=1", 16, 32), "Zq9!kVx1")
	if err != nil || ok {
		t.Errorf("Verify() = %v, %v; want false, nil", ok, err)
	}
}

func TestNeedsRehash(t *testing.T) {
	current, _ := newHasher(t, testArgon2Params, 1, 0)
	older, _ := newHasher(t, Argon2Params{MemoryKiB: 32, Iterations: 1, Parallelism: 1}, 1, 0)

	hash, err := current.Hash(t.Context(), mustPassword(t, "Zq9!kVx1"))
	if err != nil {
		t.Fatal(err)
	}
	if need, err := current.NeedsRehash(hash); err != nil || need {
		t.Errorf("NeedsRehash(current) = %v, %v; want false, nil", need, err)
	}
	if need, err := older.NeedsRehash(hash); err != nil || !need {
		t.Errorf("NeedsRehash(other parameters) = %v, %v; want true, nil", need, err)
	}
}

// hold takes a slot the way an in-flight hash would, so the next caller has to queue.
func hold(t *testing.T, h *PasswordHasher) (release func()) {
	t.Helper()
	release, err := h.acquire(t.Context())
	if err != nil {
		t.Fatalf("acquire() error = %v", err)
	}
	return release
}

func TestQueueTimeoutReturnsServerBusy(t *testing.T) {
	h, logs := newHasher(t, testArgon2Params, 1, 30*time.Millisecond)
	release := hold(t, h)

	start := time.Now()
	_, err := h.Hash(t.Context(), mustPassword(t, "Zq9!kVx1"))
	requireCode(t, err, domain.CodeServerBusy)
	if elapsed := time.Since(start); elapsed < 25*time.Millisecond || elapsed > time.Second {
		t.Errorf("waited %v, want about the 30ms queue timeout", elapsed)
	}
	if !strings.Contains(logs.String(), `"event":"auth.hash_busy"`) || !strings.Contains(logs.String(), `"level":"WARN"`) {
		t.Errorf("log %q should hold a warn auth.hash_busy record", logs.String())
	}

	release()
	if _, err := h.Hash(t.Context(), mustPassword(t, "Zq9!kVx1")); err != nil {
		t.Errorf("Hash() after the slot was released error = %v", err)
	}
}

func TestCancelledContextWhileWaitingReturnsServerBusy(t *testing.T) {
	h, _ := newHasher(t, testArgon2Params, 1, time.Minute)
	release := hold(t, h)
	defer release()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := h.Hash(ctx, mustPassword(t, "Zq9!kVx1"))
	requireCode(t, err, domain.CodeServerBusy)
	if time.Since(start) > time.Second {
		t.Errorf("a cancelled request kept waiting for %v", time.Since(start))
	}
}

func TestVerifyDummyCostsAsMuchAsVerifyAndUsesTheQueue(t *testing.T) {
	h, _ := newHasher(t, testArgon2Params, 1, 20*time.Millisecond)
	if err := h.VerifyDummy(t.Context(), "anything at all"); err != nil {
		t.Fatalf("VerifyDummy() error = %v", err)
	}

	release := hold(t, h)
	defer release()
	requireCode(t, h.VerifyDummy(t.Context(), "anything at all"), domain.CodeServerBusy)
}

func TestNewPasswordHasherValidatesConfig(t *testing.T) {
	discard := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	for name, cfg := range map[string]PasswordHasherConfig{
		"no iterations":      {Params: Argon2Params{MemoryKiB: 64, Iterations: 0, Parallelism: 1}, Concurrency: 1, Logger: discard},
		"no parallelism":     {Params: Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 0}, Concurrency: 1, Logger: discard},
		"memory below 8p":    {Params: Argon2Params{MemoryKiB: 15, Iterations: 1, Parallelism: 2}, Concurrency: 1, Logger: discard},
		"no concurrency":     {Params: testArgon2Params, Concurrency: 0, Logger: discard},
		"no logger":          {Params: testArgon2Params, Concurrency: 1},
	} {
		if _, err := NewPasswordHasher(cfg); err == nil {
			t.Errorf("%s: NewPasswordHasher() accepted the config", name)
		}
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/service/ -run 'TestHash|TestVerify|TestNeedsRehash|TestQueue|TestCancelled|TestNewPasswordHasher' -count=1`
Expected: build fail — `undefined: PasswordHasher`, `Argon2Params`

- [ ] **Step 3: อนุญาต argon2 ใน boundary test และเขียนโค้ด**

แก้ `internal/core/boundary_test.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
var allowedThirdParty = map[string]bool{
	"github.com/shopspring/decimal": true,
	"github.com/google/uuid":        true,
}
```

   ด้วย

```go
var allowedThirdParty = map[string]bool{
	"github.com/shopspring/decimal": true,
	"github.com/google/uuid":        true,
	"golang.org/x/crypto/argon2":    true,
}
```

2. แทน

```go
		"github.com/google/uuid":                      true,
```

   ด้วย

```go
		"github.com/google/uuid":                      true,
		"golang.org/x/crypto/argon2":                  true,
		"golang.org/x/crypto/bcrypt":                  false,
```

   (ถ้า Edit หาข้อความไม่เจอเพราะช่องว่างต่างกัน ให้เพิ่มสองบรรทัดนี้ในแมป `cases` ของ `TestAllowedImportPaths` ด้วยมือ แล้วรัน `gofmt -w internal/core/boundary_test.go`)

สร้าง `internal/core/service/password.go`:

```go
package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"go-backend-stock/internal/core/domain"
)

// Argon2Params are the Argon2id cost parameters. They are stored inside every hash, so changing
// them later leaves old hashes verifiable; NeedsRehash reports which ones to upgrade at login.
type Argon2Params struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
}

// DefaultArgon2Params are the confirmed phase 1 parameters (ADR-0005): 19 MiB, 2 passes, 1 lane.
var DefaultArgon2Params = Argon2Params{MemoryKiB: 19456, Iterations: 2, Parallelism: 1}

const (
	argon2SaltBytes = 16
	argon2KeyBytes  = 32

	defaultHashQueueTimeout = 2 * time.Second

	// Limits for a hash read back from the database. The values we write are far below them; a
	// corrupt or hostile row must not be able to make Verify allocate gigabytes.
	maxStoredMemoryKiB   = 256 * 1024
	maxStoredIterations  = 10
	maxStoredParallelism = 8
	minStoredSaltBytes   = 8
	maxStoredSaltBytes   = 64
	minStoredKeyBytes    = 16
	maxStoredKeyBytes    = 64
)

// PasswordHasherConfig configures NewPasswordHasher.
type PasswordHasherConfig struct {
	Params       Argon2Params
	Concurrency  int           // how many hashes may run at once (PASSWORD_HASH_CONCURRENCY)
	QueueTimeout time.Duration // how long a caller may wait for a slot; 0 means 2 seconds
	Logger       *slog.Logger
}

// PasswordHasher hashes and verifies passwords with Argon2id. At most Concurrency hashes run at
// once because each needs MemoryKiB of RAM; a caller that cannot get a slot in time receives
// server_busy. Every Hash, Verify and VerifyDummy call goes through the same slots. Callers must
// not hold a database transaction while they wait for one. Safe for concurrent use.
type PasswordHasher struct {
	params       Argon2Params
	slots        chan struct{}
	queueTimeout time.Duration
	log          *slog.Logger
	dummy        domain.PasswordHash
}

// NewPasswordHasher validates cfg and creates the dummy hash that login verifies against when the
// account does not exist or has no password, so response time does not reveal which it is.
func NewPasswordHasher(cfg PasswordHasherConfig) (*PasswordHasher, error) {
	p := cfg.Params
	switch {
	case p.Iterations < 1 || p.Parallelism < 1 || p.MemoryKiB < 8*uint32(p.Parallelism):
		return nil, errors.New("invalid argon2 parameters")
	case cfg.Concurrency < 1:
		return nil, errors.New("password hash concurrency must be at least 1")
	case cfg.Logger == nil:
		return nil, errors.New("password hasher needs a logger")
	}
	h := &PasswordHasher{
		params:       p,
		slots:        make(chan struct{}, cfg.Concurrency),
		queueTimeout: cfg.QueueTimeout,
		log:          cfg.Logger,
	}
	if h.queueTimeout <= 0 {
		h.queueTimeout = defaultHashQueueTimeout
	}

	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate dummy password: %w", err)
	}
	dummy, err := h.hashString(base64.RawStdEncoding.EncodeToString(secret))
	if err != nil {
		return nil, fmt.Errorf("create dummy hash: %w", err)
	}
	h.dummy = dummy
	return h, nil
}

// Hash returns the PHC string for password, or server_busy when no slot frees up in time.
func (h *PasswordHasher) Hash(ctx context.Context, password domain.Password) (domain.PasswordHash, error) {
	if password.IsZero() {
		return "", domain.NewError(domain.CodeInternal, "cannot hash an empty password")
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	return h.hashString(password.Reveal())
}

// Verify reports whether password matches hash, comparing in constant time. A hash that cannot be
// parsed is an internal error, not a wrong password. It returns server_busy when no slot frees up.
func (h *PasswordHasher) Verify(ctx context.Context, hash domain.PasswordHash, password string) (bool, error) {
	params, salt, key, err := decodePHC(string(hash))
	if err != nil {
		return false, err
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	got := argon2.IDKey([]byte(password), salt, params.Iterations, params.MemoryKiB, params.Parallelism,
		uint32(len(key))) //nolint:gosec // decodePHC limits the key to 16..64 bytes.
	return subtle.ConstantTimeCompare(got, key) == 1, nil
}

// VerifyDummy spends the cost of one Verify against a hash nobody knows the password of and
// discards the result. Login calls it when there is no password to check.
func (h *PasswordHasher) VerifyDummy(ctx context.Context, password string) error {
	_, err := h.Verify(ctx, h.dummy, password)
	return err
}

// NeedsRehash reports whether hash was made with parameters other than the current ones, so login
// can upgrade it after a successful check.
func (h *PasswordHasher) NeedsRehash(hash domain.PasswordHash) (bool, error) {
	params, _, _, err := decodePHC(string(hash))
	if err != nil {
		return false, err
	}
	return params != h.params, nil
}

// acquire takes a slot, waiting up to the queue timeout or until ctx ends.
func (h *PasswordHasher) acquire(ctx context.Context) (release func(), err error) {
	select {
	case h.slots <- struct{}{}:
		return h.release, nil
	default:
	}
	timer := time.NewTimer(h.queueTimeout)
	defer timer.Stop()
	select {
	case h.slots <- struct{}{}:
		return h.release, nil
	case <-timer.C:
	case <-ctx.Done():
	}
	h.log.Warn("password hashing queue is full", slog.String("event", "auth.hash_busy"))
	return nil, domain.NewError(domain.CodeServerBusy, "server is busy, try again shortly")
}

func (h *PasswordHasher) release() { <-h.slots }

func (h *PasswordHasher) hashString(password string) (domain.PasswordHash, error) {
	salt := make([]byte, argon2SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, h.params.Iterations, h.params.MemoryKiB, h.params.Parallelism, argon2KeyBytes)
	enc := base64.RawStdEncoding
	return domain.PasswordHash(fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.params.MemoryKiB, h.params.Iterations, h.params.Parallelism,
		enc.EncodeToString(salt), enc.EncodeToString(key))), nil
}

func malformedHash() error {
	return domain.NewError(domain.CodeInternal, "stored password hash is malformed")
}

// decodePHC parses "$argon2id$v=19$m=…,t=…,p=…$<salt>$<key>" and enforces the limits above.
func decodePHC(hash string) (params Argon2Params, salt, key []byte, err error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return Argon2Params{}, nil, nil, malformedHash()
	}
	fields := strings.Split(parts[3], ",")
	if len(fields) != 3 {
		return Argon2Params{}, nil, nil, malformedHash()
	}
	m, okM := parseUintField(fields[0], "m=", 32)
	t, okT := parseUintField(fields[1], "t=", 32)
	p, okP := parseUintField(fields[2], "p=", 8)
	if !okM || !okT || !okP ||
		t < 1 || t > maxStoredIterations || p < 1 || p > maxStoredParallelism ||
		m < 8*p || m > maxStoredMemoryKiB {
		return Argon2Params{}, nil, nil, malformedHash()
	}
	enc := base64.RawStdEncoding.Strict()
	salt, errSalt := enc.DecodeString(parts[4])
	key, errKey := enc.DecodeString(parts[5])
	if errSalt != nil || errKey != nil ||
		len(salt) < minStoredSaltBytes || len(salt) > maxStoredSaltBytes ||
		len(key) < minStoredKeyBytes || len(key) > maxStoredKeyBytes {
		return Argon2Params{}, nil, nil, malformedHash()
	}
	//nolint:gosec // the values are bounded above (m <= 262144, t <= 10, p <= 8).
	return Argon2Params{MemoryKiB: uint32(m), Iterations: uint32(t), Parallelism: uint8(p)}, salt, key, nil
}

func parseUintField(field, prefix string, bits int) (uint64, bool) {
	v, ok := strings.CutPrefix(field, prefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(v, 10, bits)
	return n, err == nil
}
```

- [ ] **Step 4: ทำให้ argon2 เป็น direct dependency และตรวจ**

Run:
```bash
go mod tidy
git diff go.mod
go list -m -json golang.org/x/crypto | grep -E '"(Version|Time)"'
gofmt -l internal/core; go vet ./internal/core/... && go test ./internal/core/... -count=1 -race
```
Expected:
- `git diff go.mod` ย้าย `golang.org/x/crypto v0.54.0` จากบล็อก indirect ขึ้นบล็อก direct (ไม่เปลี่ยนเวอร์ชัน และ `go.sum` ไม่เปลี่ยน)
- `Time` ของ v0.54.0 ต้องเก่ากว่า 2026-10-08 (อย่างน้อย 2 สัปดาห์ก่อนวันนี้ 2026-10-22 ตามข้อกำหนดของ spec) — ถ้าใหม่กว่า หยุดและถามผู้ใช้ก่อนใช้
- test ทั้งหมดผ่านด้วย `-race`

จากนั้นตรวจช่องโหว่ (ต้องใช้ network; ถ้าไม่มี ให้ข้ามและบันทึกว่าข้ามในสรุปท้าย task):

Run: `go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...`
Expected: `No vulnerabilities found.` ถ้าพบช่องโหว่ในโค้ดที่เราเรียกใช้ ให้หยุดและรายงานผู้ใช้

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/core
git commit -m "feat(service): add Argon2id password hasher with bounded concurrency"
```

---

### Task 6: OTP — `GenerateOTP` และ `OTPCodec`

**Files:**
- Create: `internal/core/service/secret.go`, `internal/core/service/otp.go`, `internal/core/service/otp_test.go`

**Interfaces:**
- Consumes: `domain.OTPCode`, `domain.OTPCodeFromNumber`, `domain.OTPPurpose`, `domain.ID` (`String()`)
- Produces:
  - `const minSecretBytes = 32` และ `func copySecret(name string, secret []byte) ([]byte, error)` (ใช้ซ้ำใน Task 7 และ 8)
  - `func GenerateOTP() (domain.OTPCode, error)`
  - `func NewOTPCodec(secret []byte) (*OTPCodec, error)` (secret สั้นกว่า 32 bytes = error ที่ไม่มีค่า secret)
  - `(*OTPCodec).Digest(purpose domain.OTPPurpose, challengeID, userID domain.ID, otp domain.OTPCode) []byte` — 32 bytes
  - `(*OTPCodec).Verify(stored []byte, purpose domain.OTPPurpose, challengeID, userID domain.ID, otp domain.OTPCode) bool` — constant time

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/core/service/otp_test.go`:

```go
package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"go-backend-stock/internal/core/domain"
)

func mustID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	return id
}

func mustOTP(t *testing.T, s string) domain.OTPCode {
	t.Helper()
	c, err := domain.ParseOTP(s)
	if err != nil {
		t.Fatalf("ParseOTP(%q) error = %v", s, err)
	}
	return c
}

func TestGenerateOTP(t *testing.T) {
	const samples = 3000
	seen := make(map[string]struct{}, samples)
	leadingZero := false
	for range samples {
		code, err := GenerateOTP()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := domain.ParseOTP(code.Reveal()); err != nil {
			t.Fatalf("GenerateOTP() = %q, which is not six digits", code.Reveal())
		}
		leadingZero = leadingZero || strings.HasPrefix(code.Reveal(), "0")
		seen[code.Reveal()] = struct{}{}
	}
	if !leadingZero {
		t.Error("no code started with 0 in 3000 draws: the format must keep leading zeros")
	}
	if len(seen) < 2900 {
		t.Errorf("only %d distinct codes in %d draws, the draw is not spread over 000000-999999", len(seen), samples)
	}
}

func TestGenerateOTPReportsRandomnessFailure(t *testing.T) {
	_, err := generateOTP(iotest.ErrReader(errors.New("entropy exhausted")))
	if err == nil {
		t.Fatal("generateOTP() ignored a failing random source")
	}
}

func TestOTPDigestFormat(t *testing.T) {
	secret := bytes.Repeat([]byte("s"), 32)
	codec, err := NewOTPCodec(secret)
	if err != nil {
		t.Fatal(err)
	}
	challengeID, userID := mustID(t), mustID(t)

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("otp\x00email_verification\x00" + challengeID.String() + "\x00" + userID.String() + "\x00042731"))
	want := mac.Sum(nil)

	got := codec.Digest(domain.OTPPurposeEmailVerification, challengeID, userID, mustOTP(t, "042731"))
	if !bytes.Equal(got, want) || len(got) != domain.HashSize {
		t.Errorf("Digest() = %x, want %x", got, want)
	}
}

func TestOTPDigestIsBoundToEveryInput(t *testing.T) {
	codec, err := NewOTPCodec(bytes.Repeat([]byte("s"), 32))
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewOTPCodec(bytes.Repeat([]byte("t"), 32))
	if err != nil {
		t.Fatal(err)
	}
	challengeID, userID := mustID(t), mustID(t)
	otp := mustOTP(t, "123456")
	base := codec.Digest(domain.OTPPurposeEmailVerification, challengeID, userID, otp)

	variants := map[string][]byte{
		"purpose":   codec.Digest(domain.OTPPurposePasswordReset, challengeID, userID, otp),
		"challenge": codec.Digest(domain.OTPPurposeEmailVerification, mustID(t), userID, otp),
		"user":      codec.Digest(domain.OTPPurposeEmailVerification, challengeID, mustID(t), otp),
		"code":      codec.Digest(domain.OTPPurposeEmailVerification, challengeID, userID, mustOTP(t, "123457")),
		"secret":    other.Digest(domain.OTPPurposeEmailVerification, challengeID, userID, otp),
	}
	for name, d := range variants {
		if bytes.Equal(d, base) {
			t.Errorf("changing the %s did not change the digest", name)
		}
	}
	if !bytes.Equal(base, codec.Digest(domain.OTPPurposeEmailVerification, challengeID, userID, otp)) {
		t.Error("Digest() is not deterministic")
	}
}

func TestOTPVerify(t *testing.T) {
	codec, err := NewOTPCodec(bytes.Repeat([]byte("s"), 32))
	if err != nil {
		t.Fatal(err)
	}
	challengeID, userID := mustID(t), mustID(t)
	stored := codec.Digest(domain.OTPPurposePasswordReset, challengeID, userID, mustOTP(t, "000123"))

	if !codec.Verify(stored, domain.OTPPurposePasswordReset, challengeID, userID, mustOTP(t, "000123")) {
		t.Error("Verify(right code) = false")
	}
	if codec.Verify(stored, domain.OTPPurposePasswordReset, challengeID, userID, mustOTP(t, "000124")) {
		t.Error("Verify(wrong code) = true")
	}
	if codec.Verify(stored, domain.OTPPurposeEmailVerification, challengeID, userID, mustOTP(t, "000123")) {
		t.Error("a code issued for password reset verified as an email verification code")
	}
	for _, bad := range [][]byte{nil, {}, stored[:31], append(bytes.Clone(stored), 0)} {
		if codec.Verify(bad, domain.OTPPurposePasswordReset, challengeID, userID, mustOTP(t, "000123")) {
			t.Errorf("Verify() accepted a stored digest of %d bytes", len(bad))
		}
	}
}

func TestNewOTPCodecRejectsShortSecretsWithoutEchoingThem(t *testing.T) {
	_, err := NewOTPCodec([]byte("short-secret-ZQXJ"))
	if err == nil {
		t.Fatal("NewOTPCodec() accepted a secret under 32 bytes")
	}
	if strings.Contains(err.Error(), "ZQXJ") {
		t.Errorf("error %q echoes the secret", err)
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/service/ -run 'TestGenerateOTP|TestOTP|TestNewOTPCodec' -count=1`
Expected: build fail — `undefined: GenerateOTP`, `NewOTPCodec`

- [ ] **Step 3: เขียนโค้ด**

สร้าง `internal/core/service/secret.go`:

```go
package service

import (
	"bytes"
	"fmt"
)

// minSecretBytes is the shortest HMAC secret the helpers accept (config enforces the same).
const minSecretBytes = 32

// copySecret checks the length of an HMAC secret and returns a private copy. The error names the
// secret but never contains its value.
func copySecret(name string, secret []byte) ([]byte, error) {
	if len(secret) < minSecretBytes {
		return nil, fmt.Errorf("%s must be at least %d bytes", name, minSecretBytes)
	}
	return bytes.Clone(secret), nil
}
```

สร้าง `internal/core/service/otp.go`:

```go
package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"math/big"
	"strings"

	"go-backend-stock/internal/core/domain"
)

var otpRange = big.NewInt(1_000_000)

// GenerateOTP draws a uniformly distributed six-digit code from crypto/rand, leading zeros included.
func GenerateOTP() (domain.OTPCode, error) { return generateOTP(rand.Reader) }

func generateOTP(r io.Reader) (domain.OTPCode, error) {
	n, err := rand.Int(r, otpRange)
	if err != nil {
		return domain.OTPCode{}, fmt.Errorf("generate otp: %w", err)
	}
	return domain.OTPCodeFromNumber(n.Int64())
}

// OTPCodec computes and checks the HMAC digests stored in otp_challenges.otp_hash. Six digits
// have only a million values, so a plain hash would be reversible from a leaked row; the digest
// needs OTP_SECRET, which never reaches the database, and is bound to the flow, the challenge and
// the user so a code cannot be moved to another.
type OTPCodec struct{ secret []byte }

// NewOTPCodec returns a codec keyed with secret (at least 32 bytes).
func NewOTPCodec(secret []byte) (*OTPCodec, error) {
	s, err := copySecret("OTP secret", secret)
	if err != nil {
		return nil, err
	}
	return &OTPCodec{secret: s}, nil
}

// Digest returns HMAC-SHA-256(secret, "otp" ‖ 0x00 ‖ purpose ‖ 0x00 ‖ challengeID ‖ 0x00 ‖ userID ‖ 0x00 ‖ otp).
func (c *OTPCodec) Digest(purpose domain.OTPPurpose, challengeID, userID domain.ID, otp domain.OTPCode) []byte {
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(strings.Join([]string{"otp", string(purpose), challengeID.String(), userID.String(), otp.Reveal()}, "\x00")))
	return mac.Sum(nil)
}

// Verify compares stored with the digest of otp in constant time.
func (c *OTPCodec) Verify(stored []byte, purpose domain.OTPPurpose, challengeID, userID domain.ID, otp domain.OTPCode) bool {
	return hmac.Equal(stored, c.Digest(purpose, challengeID, userID, otp))
}
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/core; go vet ./internal/core/... && go test ./internal/core/... -count=1 -race`
Expected: ผ่านทั้งหมด

- [ ] **Step 5: Commit**

```bash
git add internal/core/service
git commit -m "feat(service): add OTP generation and HMAC digest"
```

---

### Task 7: Session token และ `CSRFCodec`

**Files:**
- Create: `internal/core/service/session.go`, `internal/core/service/csrf.go`, `internal/core/service/clock_test.go`, `internal/core/service/session_test.go`, `internal/core/service/csrf_test.go`

**Interfaces:**
- Consumes: `copySecret` (Task 6), `port.Clock`, `domain.ID`, `domain.NewError`, `domain.CodeCSRFFailed`
- Produces:
  - `func NewSessionToken() (token string, hash []byte, err error)` — token = base64url ไม่มี padding ยาว 43 ตัว; hash = SHA-256 ของ 32 bytes
  - `func HashSessionToken(token string) (hash []byte, ok bool)` — `ok=false` สำหรับทุกอย่างที่ `NewSessionToken` ไม่เคยออก รวมถึงการ encode ที่ไม่ canonical
  - `func SessionCSRFBinding(sessionID domain.ID) string` (= `"session:"+id`), `func PreLoginCSRFBinding(nonce string) string` (= `"pre:"+nonce`), `func NewCSRFNonce() (string, error)` (32 bytes base64url)
  - `func NewCSRFCodec(secret []byte, clock port.Clock) (*CSRFCodec, error)`
  - `(*CSRFCodec).Issue(binding string) (token string, expiresAt time.Time, err error)` — อายุ 1 ชั่วโมง ปัดลงเป็นวินาที
  - `(*CSRFCodec).Verify(token, binding string) error` — ทุกความผิดคืน `domain.CodeCSRFFailed` ข้อความเดียวกัน; หมดอายุเมื่อ `now >= expiresAt`
  - ใน test: `fakeClock` (`newFakeClock()`, `Now()`, `Advance(d)`) ใช้ซ้ำใน Task 9

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/core/service/clock_test.go`:

```go
package service

import "time"

// fakeClock is a port.Clock that only moves when a test says so.
type fakeClock struct{ now time.Time }

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, time.October, 22, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }
```

สร้าง `internal/core/service/session_test.go`:

```go
package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

const base64URLAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

func TestNewSessionToken(t *testing.T) {
	token, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 43 || strings.ContainsAny(token, "=+/") {
		t.Errorf("token %q must be 43 base64url characters without padding", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		t.Fatalf("token decodes to %d bytes (%v), want 32", len(raw), err)
	}
	if sum := sha256.Sum256(raw); !bytes.Equal(hash, sum[:]) {
		t.Error("the hash must be SHA-256 of the 32 raw bytes, not of the string")
	}

	other, _, err := NewSessionToken()
	if err != nil || other == token {
		t.Errorf("two tokens are equal (%v)", err)
	}
}

func TestHashSessionToken(t *testing.T) {
	token, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := HashSessionToken(token)
	if !ok || !bytes.Equal(got, hash) {
		t.Errorf("HashSessionToken() = %x, %v; want the hash NewSessionToken returned", got, ok)
	}

	for name, bad := range map[string]string{
		"empty":     "",
		"short":     token[:42],
		"long":      token + "A",
		"padded":    token + "=",
		"standard":  strings.NewReplacer("-", "+", "_", "/").Replace(token) + "=", // wrong alphabet and padding
		"not base64": strings.Repeat("!", 43),
		"whitespace": " " + token[1:],
	} {
		if _, ok := HashSessionToken(bad); ok {
			t.Errorf("%s: HashSessionToken(%q) accepted it", name, bad)
		}
	}
}

// 32 bytes need 43 characters, but the last one carries only 4 data bits; a lenient decoder
// ignores the other 2, so several strings would name the same session.
func TestHashSessionTokenRejectsNonCanonicalEncoding(t *testing.T) {
	token, _, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	last := strings.IndexByte(base64URLAlphabet, token[42])
	mutated := token[:42] + string(base64URLAlphabet[last|1]) // sets a padding bit
	if mutated == token {
		t.Fatal("test bug: the mutation changed nothing")
	}
	if raw, err := base64.RawURLEncoding.DecodeString(mutated); err != nil || len(raw) != 32 {
		t.Fatalf("test bug: a lenient decoder should accept %q (%v)", mutated, err)
	}
	if _, ok := HashSessionToken(mutated); ok {
		t.Errorf("HashSessionToken(%q) accepted a second spelling of the same token", mutated)
	}
}
```

สร้าง `internal/core/service/csrf_test.go`:

```go
package service

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func newTestCSRF(t *testing.T) (*CSRFCodec, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	codec, err := NewCSRFCodec([]byte(strings.Repeat("s", 32)), clock)
	if err != nil {
		t.Fatal(err)
	}
	return codec, clock
}

func TestCSRFIssueAndVerify(t *testing.T) {
	codec, clock := newTestCSRF(t)
	binding := SessionCSRFBinding(mustID(t))

	token, expiresAt, err := codec.Issue(binding)
	if err != nil {
		t.Fatal(err)
	}
	if want := clock.Now().Add(time.Hour); !expiresAt.Equal(want) {
		t.Errorf("expiresAt = %v, want %v", expiresAt, want)
	}
	if err := codec.Verify(token, binding); err != nil {
		t.Errorf("Verify() error = %v", err)
	}

	clock.Advance(time.Hour - time.Second)
	if err := codec.Verify(token, binding); err != nil {
		t.Errorf("Verify() one second before expiry error = %v", err)
	}
	clock.Advance(time.Second)
	requireCode(t, codec.Verify(token, binding), domain.CodeCSRFFailed) // expired at the expiry second
}

func TestCSRFTokenLayout(t *testing.T) {
	codec, clock := newTestCSRF(t)
	token, expiresAt, err := codec.Issue("pre:abc")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 57 || raw[0] != 1 {
		t.Fatalf("token is %d bytes, want 57 bytes starting with version byte 1", len(raw))
	}
	var expiry uint64
	for _, b := range raw[1:9] {
		expiry = expiry<<8 | uint64(b)
	}
	if int64(expiry) != expiresAt.Unix() || expiresAt.Unix() != clock.Now().Add(time.Hour).Unix() {
		t.Errorf("embedded expiry = %d, want %d (big-endian unix seconds)", expiry, expiresAt.Unix())
	}

	again, _, err := codec.Issue("pre:abc")
	if err != nil || again == token {
		t.Errorf("two tokens for one binding are equal (%v): the salt must differ", err)
	}
}

func TestCSRFVerifyRejectsEverythingElseWithOneMessage(t *testing.T) {
	codec, clock := newTestCSRF(t)
	otherCodec, err := NewCSRFCodec([]byte(strings.Repeat("t", 32)), clock)
	if err != nil {
		t.Fatal(err)
	}
	sessionBinding := SessionCSRFBinding(mustID(t))
	token, _, err := codec.Issue(sessionBinding)
	if err != nil {
		t.Fatal(err)
	}
	preToken, _, err := codec.Issue(PreLoginCSRFBinding("nonce"))
	if err != nil {
		t.Fatal(err)
	}
	foreign, _, err := otherCodec.Issue(sessionBinding)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct{ token, binding string }{
		"empty":                      {"", sessionBinding},
		"not base64":                 {"!!!", sessionBinding},
		"other session":              {token, SessionCSRFBinding(mustID(t))},
		"pre-login token on session": {preToken, sessionBinding},
		"session token on pre-login": {token, PreLoginCSRFBinding("nonce")},
		"other secret":               {foreign, sessionBinding},
		"one byte short":             {base64.RawURLEncoding.EncodeToString(raw[:56]), sessionBinding},
		"one byte long":              {base64.RawURLEncoding.EncodeToString(append(slices.Clone(raw), 0)), sessionBinding},
	}
	// Flipping any single bit anywhere in the token must break it: version, expiry, salt or MAC.
	for i := range raw {
		mutated := slices.Clone(raw)
		mutated[i] ^= 0x01
		cases[fmt.Sprintf("bit flipped in byte %d", i)] = struct{ token, binding string }{
			base64.RawURLEncoding.EncodeToString(mutated), sessionBinding,
		}
	}

	messages := map[string]bool{}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			de := requireCode(t, codec.Verify(c.token, c.binding), domain.CodeCSRFFailed)
			messages[de.Message] = true
		})
	}
	if len(messages) != 1 {
		t.Errorf("Verify() produced %d different messages, want one: %v", len(messages), messages)
	}
}

func TestCSRFNonce(t *testing.T) {
	a, err := NewCSRFNonce()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewCSRFNonce()
	raw, err := base64.RawURLEncoding.DecodeString(a)
	if err != nil || len(raw) != 32 || a == b {
		t.Errorf("nonce %q decodes to %d bytes (%v), equal to the next one: %v", a, len(raw), err, a == b)
	}
	if got := SessionCSRFBinding(mustID(t)); !strings.HasPrefix(got, "session:") {
		t.Errorf("session binding = %q", got)
	}
	if got := PreLoginCSRFBinding(a); got != "pre:"+a {
		t.Errorf("pre-login binding = %q", got)
	}
}

func TestNewCSRFCodecRejectsShortSecret(t *testing.T) {
	if _, err := NewCSRFCodec([]byte("short"), newFakeClock()); err == nil {
		t.Error("NewCSRFCodec() accepted a secret under 32 bytes")
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/service/ -run 'Session|CSRF' -count=1`
Expected: build fail — `undefined: NewSessionToken`, `NewCSRFCodec`

- [ ] **Step 3: เขียนโค้ด**

สร้าง `internal/core/service/session.go`:

```go
package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const (
	sessionTokenBytes = 32
	sessionTokenChars = 43 // 32 bytes in unpadded base64url
)

// NewSessionToken returns a random token for the session cookie and the hash to store. Only the
// hash reaches the database, so a leaked row cannot be turned into a login. The hash is SHA-256 of
// the 32 raw bytes, not of the text, so a token has exactly one spelling.
func NewSessionToken() (token string, hash []byte, err error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate session token: %w", err)
	}
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(raw), sum[:], nil
}

// HashSessionToken returns the stored hash of a token from a cookie. ok is false for anything
// NewSessionToken could not have produced, including a second spelling of a real token (the
// unused bits of the last character set), so a bad cookie means "no session", never an error.
func HashSessionToken(token string) (hash []byte, ok bool) {
	if len(token) != sessionTokenChars {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != sessionTokenBytes {
		return nil, false
	}
	sum := sha256.Sum256(raw)
	return sum[:], true
}
```

สร้าง `internal/core/service/csrf.go`:

```go
package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

const (
	csrfVersion    byte = 1
	csrfLifetime        = time.Hour
	csrfSaltBytes       = 16
	csrfNonceBytes      = 32
	csrfTokenBytes      = 1 + 8 + csrfSaltBytes + sha256.Size
)

// SessionCSRFBinding is the binding of a token issued to a signed-in session.
func SessionCSRFBinding(sessionID domain.ID) string { return "session:" + sessionID.String() }

// PreLoginCSRFBinding is the binding of a token issued before login, tied to the random nonce in
// the browser's csrf_nonce cookie.
func PreLoginCSRFBinding(nonce string) string { return "pre:" + nonce }

// NewCSRFNonce returns the random value for the csrf_nonce cookie.
func NewCSRFNonce() (string, error) {
	raw := make([]byte, csrfNonceBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate csrf nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// CSRFCodec issues and checks the signed CSRF tokens of phase 1 spec section 10. A token proves
// the request came from a page that asked this server for it for this session (or, before login,
// this browser): it is bound to a session ID or pre-login nonce, signed with CSRF_SECRET and
// expires after one hour. It contains no session token or hash.
type CSRFCodec struct {
	secret []byte
	clock  port.Clock
}

// NewCSRFCodec returns a codec keyed with secret (at least 32 bytes).
func NewCSRFCodec(secret []byte, clock port.Clock) (*CSRFCodec, error) {
	s, err := copySecret("CSRF secret", secret)
	if err != nil {
		return nil, err
	}
	return &CSRFCodec{secret: s, clock: clock}, nil
}

// Issue returns a token bound to binding and the moment it expires (whole seconds).
// Layout: base64url(version ‖ expiry (8 bytes, big-endian unix seconds) ‖ salt (16) ‖ mac (32)).
func (c *CSRFCodec) Issue(binding string) (token string, expiresAt time.Time, err error) {
	expiresAt = time.Unix(c.clock.Now().Add(csrfLifetime).Unix(), 0).UTC()
	salt := make([]byte, csrfSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", time.Time{}, fmt.Errorf("generate csrf salt: %w", err)
	}
	raw := make([]byte, 0, csrfTokenBytes)
	raw = append(raw, csrfVersion)
	raw = binary.BigEndian.AppendUint64(raw, uint64(expiresAt.Unix())) //nolint:gosec // a unix time after 1970 is never negative.
	expiry := raw[1:9]
	raw = append(raw, salt...)
	raw = append(raw, c.mac(binding, expiry, salt)...)
	return base64.RawURLEncoding.EncodeToString(raw), expiresAt, nil
}

// Verify checks token against binding. Every failure returns the same csrf_failed error, so the
// response does not say which check failed.
func (c *CSRFCodec) Verify(token, binding string) error {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != csrfTokenBytes || raw[0] != csrfVersion {
		return csrfFailed()
	}
	expiry, salt, got := raw[1:9], raw[9:9+csrfSaltBytes], raw[9+csrfSaltBytes:]
	if !hmac.Equal(got, c.mac(binding, expiry, salt)) {
		return csrfFailed()
	}
	expiresAt := time.Unix(int64(binary.BigEndian.Uint64(expiry)), 0) //nolint:gosec // the MAC proves we wrote this value.
	if !c.clock.Now().Before(expiresAt) {
		return csrfFailed()
	}
	return nil
}

func (c *CSRFCodec) mac(binding string, expiry, salt []byte) []byte {
	m := hmac.New(sha256.New, c.secret)
	m.Write([]byte("csrf\x00" + binding + "\x00"))
	m.Write(expiry)
	m.Write(salt)
	return m.Sum(nil)
}

func csrfFailed() error {
	return domain.NewError(domain.CodeCSRFFailed, "CSRF token is missing, invalid or expired")
}
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/core; go vet ./internal/core/... && go test ./internal/core/... -count=1 -race`
Expected: ผ่านทั้งหมด

- [ ] **Step 5: Commit**

```bash
git add internal/core/service
git commit -m "feat(service): add session token hashing and CSRF tokens"
```

---

### Task 8: `RateLimitKeys` และ `RateLimitPolicy`

**Files:**
- Create: `internal/core/service/ratelimit.go`, `internal/core/service/ratelimit_test.go`

**Interfaces:**
- Consumes: `copySecret`, `domain.NewRateLimitRule`, `domain.RateLimitScope*`, `domain.Email`
- Produces:
  - `func NewRateLimitKeys(secret []byte) (*RateLimitKeys, error)`; `(*RateLimitKeys).Key(scope domain.RateLimitScope, value string) []byte` = HMAC-SHA-256(secret, `scope ‖ 0x00 ‖ value`)
  - `type RateLimitConfig struct{ OTPResendCooldown time.Duration; OTPRequestsPerHourEmail, OTPRequestsPerHourIP, LoginAttemptsPerEmail, LoginAttemptsPerIP int }`
  - `func NewRateLimitPolicy(keys *RateLimitKeys, cfg RateLimitConfig) (*RateLimitPolicy, error)` (ค่าที่ไม่เป็นบวก = error)
  - `(*RateLimitPolicy).Login(email domain.Email, ip netip.Addr) ([]domain.RateLimitRule, error)` — `login_ip` และ `login_email`
  - `(*RateLimitPolicy).Signup(email, ip)` — `signup_ip` 20/ชม. + `email_verification_request_email` (cooldown และ N/ชม. บน key เดียวกัน)
  - `(*RateLimitPolicy).EmailVerificationRequest(email, ip)` — `email_verification_request_ip` N/ชม. + กฎ email ชุดเดียวกับ Signup
  - `(*RateLimitPolicy).EmailVerificationConfirm(ip)` — `email_verification_confirm_ip` 20/15 นาที
  - `(*RateLimitPolicy).GoogleStart(ip)` — `google_start_ip` 20/ชม.
  - IP ที่ว่าง (`netip.Addr{}`) = `internal` error; IPv4 ในรูป IPv6 (`::ffff:a.b.c.d`) ใช้ key เดียวกับ IPv4

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/core/service/ratelimit_test.go`:

```go
package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"net/netip"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func newTestPolicy(t *testing.T) *RateLimitPolicy {
	t.Helper()
	keys, err := NewRateLimitKeys([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewRateLimitPolicy(keys, RateLimitConfig{
		OTPResendCooldown: time.Minute, OTPRequestsPerHourEmail: 5, OTPRequestsPerHourIP: 20,
		LoginAttemptsPerEmail: 10, LoginAttemptsPerIP: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

type ruleShape struct {
	limit  int
	window time.Duration
}

// shapes groups rules by scope; a scope with two rules (cooldown and hourly) lists both.
func shapes(rules []domain.RateLimitRule) map[domain.RateLimitScope][]ruleShape {
	out := map[domain.RateLimitScope][]ruleShape{}
	for _, r := range rules {
		out[r.Scope] = append(out[r.Scope], ruleShape{r.Limit, r.Window})
	}
	return out
}

func TestRateLimitKeyFormat(t *testing.T) {
	secret := []byte(strings.Repeat("k", 32))
	keys, err := NewRateLimitKeys(secret)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("login_email\x00user@example.com"))
	if got := keys.Key(domain.RateLimitLoginEmail, "user@example.com"); !bytes.Equal(got, mac.Sum(nil)) || len(got) != domain.HashSize {
		t.Errorf("Key() = %x, want HMAC-SHA-256(secret, scope ‖ 0x00 ‖ value)", got)
	}

	a := keys.Key(domain.RateLimitLoginEmail, "user@example.com")
	if b := keys.Key(domain.RateLimitSignupIP, "user@example.com"); bytes.Equal(a, b) {
		t.Error("the same value under two scopes must give two keys")
	}
	other, err := NewRateLimitKeys([]byte(strings.Repeat("j", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, other.Key(domain.RateLimitLoginEmail, "user@example.com")) {
		t.Error("a different secret must give a different key")
	}
}

func TestRateLimitPolicyRules(t *testing.T) {
	policy := newTestPolicy(t)
	email := mustEmail(t, "user@example.com")
	ip := netip.MustParseAddr("203.0.113.9")
	hour, quarter := time.Hour, 15*time.Minute

	cooldownAndHourly := []ruleShape{{1, time.Minute}, {5, hour}}

	tests := []struct {
		name  string
		build func() ([]domain.RateLimitRule, error)
		want  map[domain.RateLimitScope][]ruleShape
	}{
		{"login", func() ([]domain.RateLimitRule, error) { return policy.Login(email, ip) },
			map[domain.RateLimitScope][]ruleShape{
				domain.RateLimitLoginIP:    {{30, quarter}},
				domain.RateLimitLoginEmail: {{10, quarter}},
			}},
		{"signup", func() ([]domain.RateLimitRule, error) { return policy.Signup(email, ip) },
			map[domain.RateLimitScope][]ruleShape{
				domain.RateLimitSignupIP:                      {{20, hour}},
				domain.RateLimitEmailVerificationRequestEmail: cooldownAndHourly,
			}},
		{"email verification request", func() ([]domain.RateLimitRule, error) { return policy.EmailVerificationRequest(email, ip) },
			map[domain.RateLimitScope][]ruleShape{
				domain.RateLimitEmailVerificationRequestIP:    {{20, hour}},
				domain.RateLimitEmailVerificationRequestEmail: cooldownAndHourly,
			}},
		{"email verification confirm", func() ([]domain.RateLimitRule, error) { return policy.EmailVerificationConfirm(ip) },
			map[domain.RateLimitScope][]ruleShape{domain.RateLimitEmailVerificationConfirmIP: {{20, quarter}}}},
		{"google start", func() ([]domain.RateLimitRule, error) { return policy.GoogleStart(ip) },
			map[domain.RateLimitScope][]ruleShape{domain.RateLimitGoogleStartIP: {{20, hour}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules, err := tt.build()
			if err != nil {
				t.Fatal(err)
			}
			got := shapes(rules)
			if len(got) != len(tt.want) {
				t.Fatalf("scopes = %v, want %v", got, tt.want)
			}
			for scope, want := range tt.want {
				if len(got[scope]) != len(want) {
					t.Fatalf("%s rules = %v, want %v", scope, got[scope], want)
				}
				for i := range want {
					if got[scope][i] != want[i] {
						t.Errorf("%s rule %d = %v, want %v", scope, i, got[scope][i], want[i])
					}
				}
			}
			for _, r := range rules {
				if _, err := domain.NewRateLimitRule(r); err != nil {
					t.Errorf("rule for %s is not valid: %v", r.Scope, err)
				}
			}
		})
	}
}

func TestRateLimitPolicyEmailRulesShareOneKey(t *testing.T) {
	policy := newTestPolicy(t)
	rules, err := policy.Signup(mustEmail(t, "user@example.com"), netip.MustParseAddr("203.0.113.9"))
	if err != nil {
		t.Fatal(err)
	}
	var emailKeys [][]byte
	for _, r := range rules {
		if r.Scope == domain.RateLimitEmailVerificationRequestEmail {
			emailKeys = append(emailKeys, r.KeyHash)
		}
	}
	if len(emailKeys) != 2 || !bytes.Equal(emailKeys[0], emailKeys[1]) {
		t.Errorf("the cooldown and the hourly rule must count the same events: keys = %x", emailKeys)
	}
}

func TestRateLimitPolicyKeysHideEmailAndNormaliseIP(t *testing.T) {
	policy := newTestPolicy(t)
	email := mustEmail(t, "user@example.com")

	plain, err := policy.EmailVerificationConfirm(netip.MustParseAddr("203.0.113.9"))
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := policy.EmailVerificationConfirm(netip.MustParseAddr("::ffff:203.0.113.9"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain[0].KeyHash, mapped[0].KeyHash) {
		t.Error("an IPv4 address in IPv6 notation must share the IPv4 address's quota")
	}
	neighbour, err := policy.EmailVerificationConfirm(netip.MustParseAddr("203.0.113.10"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(plain[0].KeyHash, neighbour[0].KeyHash) {
		t.Error("the full address is the key: a neighbour must not share the quota")
	}

	rules, err := policy.Login(email, netip.MustParseAddr("203.0.113.9"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if bytes.Contains(r.KeyHash, []byte("user@example.com")) || bytes.Contains(r.KeyHash, []byte("203.0.113.9")) {
			t.Errorf("key for %s contains the raw value", r.Scope)
		}
	}
}

func TestRateLimitPolicyRejectsUnknownClientIP(t *testing.T) {
	policy := newTestPolicy(t)
	_, err := policy.Login(mustEmail(t, "user@example.com"), netip.Addr{})
	requireCode(t, err, domain.CodeInternal)
}

func TestNewRateLimitPolicyValidatesConfig(t *testing.T) {
	keys, err := NewRateLimitKeys([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	good := RateLimitConfig{
		OTPResendCooldown: time.Minute, OTPRequestsPerHourEmail: 5, OTPRequestsPerHourIP: 20,
		LoginAttemptsPerEmail: 10, LoginAttemptsPerIP: 30,
	}
	for name, mutate := range map[string]func(*RateLimitConfig){
		"cooldown":     func(c *RateLimitConfig) { c.OTPResendCooldown = 0 },
		"otp email":    func(c *RateLimitConfig) { c.OTPRequestsPerHourEmail = 0 },
		"otp ip":       func(c *RateLimitConfig) { c.OTPRequestsPerHourIP = -1 },
		"login email":  func(c *RateLimitConfig) { c.LoginAttemptsPerEmail = 0 },
		"login ip":     func(c *RateLimitConfig) { c.LoginAttemptsPerIP = 0 },
	} {
		cfg := good
		mutate(&cfg)
		if _, err := NewRateLimitPolicy(keys, cfg); err == nil {
			t.Errorf("%s: NewRateLimitPolicy() accepted a non-positive value", name)
		}
	}
	if _, err := NewRateLimitPolicy(nil, good); err == nil {
		t.Error("NewRateLimitPolicy(nil keys) accepted")
	}
	if _, err := NewRateLimitKeys([]byte("short")); err == nil {
		t.Error("NewRateLimitKeys() accepted a secret under 32 bytes")
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/service/ -run 'RateLimit' -count=1`
Expected: build fail — `undefined: NewRateLimitKeys`

- [ ] **Step 3: เขียนโค้ด**

สร้าง `internal/core/service/ratelimit.go`:

```go
package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net/netip"
	"time"

	"go-backend-stock/internal/core/domain"
)

// Limits that are fixed in phase 1 rather than configurable (phase 1 spec section 10).
const (
	loginWindow             = 15 * time.Minute
	hourWindow              = time.Hour
	signupPerHourIP         = 20
	googleStartPerHourIP    = 20
	verificationConfirmIP   = 20 // per loginWindow
	verificationConfirmSpan = loginWindow
)

// RateLimitKeys turns an email or an IP address into the opaque key stored in rate_limit_keys, so
// the table never holds a raw email or IP.
type RateLimitKeys struct{ secret []byte }

// NewRateLimitKeys returns keys derived with secret (at least 32 bytes).
func NewRateLimitKeys(secret []byte) (*RateLimitKeys, error) {
	s, err := copySecret("rate limit secret", secret)
	if err != nil {
		return nil, err
	}
	return &RateLimitKeys{secret: s}, nil
}

// Key returns HMAC-SHA-256(secret, scope ‖ 0x00 ‖ value). value is a normalised email or a full
// client IP.
func (k *RateLimitKeys) Key(scope domain.RateLimitScope, value string) []byte {
	mac := hmac.New(sha256.New, k.secret)
	mac.Write([]byte(string(scope) + "\x00" + value))
	return mac.Sum(nil)
}

// RateLimitConfig holds the configurable limits (OTP_RESEND_COOLDOWN, OTP_REQUESTS_PER_HOUR_*,
// LOGIN_ATTEMPTS_PER_*). All values must be positive.
type RateLimitConfig struct {
	OTPResendCooldown       time.Duration
	OTPRequestsPerHourEmail int
	OTPRequestsPerHourIP    int
	LoginAttemptsPerEmail   int
	LoginAttemptsPerIP      int
}

// RateLimitPolicy builds the rules each endpoint passes to RateLimitRepository.Admit. Every rule
// goes through domain.NewRateLimitRule, so none can have a zero limit.
type RateLimitPolicy struct {
	keys *RateLimitKeys
	cfg  RateLimitConfig
}

// NewRateLimitPolicy checks cfg and returns the policy.
func NewRateLimitPolicy(keys *RateLimitKeys, cfg RateLimitConfig) (*RateLimitPolicy, error) {
	switch {
	case keys == nil:
		return nil, errors.New("rate limit policy needs keys")
	case cfg.OTPResendCooldown <= 0, cfg.OTPRequestsPerHourEmail < 1, cfg.OTPRequestsPerHourIP < 1,
		cfg.LoginAttemptsPerEmail < 1, cfg.LoginAttemptsPerIP < 1:
		return nil, errors.New("rate limits must be positive")
	}
	return &RateLimitPolicy{keys: keys, cfg: cfg}, nil
}

// Login limits password login by client IP and by account email.
func (p *RateLimitPolicy) Login(email domain.Email, ip netip.Addr) ([]domain.RateLimitRule, error) {
	return p.build(
		p.spec(domain.RateLimitLoginIP, ipValue(ip), p.cfg.LoginAttemptsPerIP, loginWindow),
		p.spec(domain.RateLimitLoginEmail, email.String(), p.cfg.LoginAttemptsPerEmail, loginWindow),
	)
}

// Signup limits registration by client IP, and counts it against the email's verification-code
// quota so register followed by "send again" cannot beat the cooldown.
func (p *RateLimitPolicy) Signup(email domain.Email, ip netip.Addr) ([]domain.RateLimitRule, error) {
	return p.build(append(p.verificationEmailSpecs(email),
		p.spec(domain.RateLimitSignupIP, ipValue(ip), signupPerHourIP, hourWindow))...)
}

// EmailVerificationRequest limits asking for a new verification code.
func (p *RateLimitPolicy) EmailVerificationRequest(email domain.Email, ip netip.Addr) ([]domain.RateLimitRule, error) {
	return p.build(append(p.verificationEmailSpecs(email),
		p.spec(domain.RateLimitEmailVerificationRequestIP, ipValue(ip), p.cfg.OTPRequestsPerHourIP, hourWindow))...)
}

// EmailVerificationConfirm limits guessing codes from one client IP.
func (p *RateLimitPolicy) EmailVerificationConfirm(ip netip.Addr) ([]domain.RateLimitRule, error) {
	return p.build(p.spec(domain.RateLimitEmailVerificationConfirmIP, ipValue(ip), verificationConfirmIP, verificationConfirmSpan))
}

// GoogleStart limits starting a Google sign-in from one client IP.
func (p *RateLimitPolicy) GoogleStart(ip netip.Addr) ([]domain.RateLimitRule, error) {
	return p.build(p.spec(domain.RateLimitGoogleStartIP, ipValue(ip), googleStartPerHourIP, hourWindow))
}

// verificationEmailSpecs are the two rules on one email key: at most one code per cooldown and at
// most OTPRequestsPerHourEmail per hour.
func (p *RateLimitPolicy) verificationEmailSpecs(email domain.Email) []ruleSpec {
	return []ruleSpec{
		p.spec(domain.RateLimitEmailVerificationRequestEmail, email.String(), 1, p.cfg.OTPResendCooldown),
		p.spec(domain.RateLimitEmailVerificationRequestEmail, email.String(), p.cfg.OTPRequestsPerHourEmail, hourWindow),
	}
}

type ruleSpec struct {
	scope  domain.RateLimitScope
	value  string
	limit  int
	window time.Duration
}

func (p *RateLimitPolicy) spec(scope domain.RateLimitScope, value string, limit int, window time.Duration) ruleSpec {
	return ruleSpec{scope: scope, value: value, limit: limit, window: window}
}

func (p *RateLimitPolicy) build(specs ...ruleSpec) ([]domain.RateLimitRule, error) {
	rules := make([]domain.RateLimitRule, 0, len(specs))
	for _, s := range specs {
		if s.value == "" {
			return nil, domain.NewError(domain.CodeInternal, "rate limit needs a client IP")
		}
		rule, err := domain.NewRateLimitRule(domain.RateLimitRule{
			Scope: s.scope, KeyHash: p.keys.Key(s.scope, s.value), Limit: s.limit, Window: s.window,
		})
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// ipValue is the full address, with an IPv4 address in IPv6 notation turned back into IPv4 so
// both spellings share one quota. It is "" for the zero Addr.
func ipValue(ip netip.Addr) string {
	if !ip.IsValid() {
		return ""
	}
	return ip.Unmap().WithZone("").String()
}
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/core; go vet ./internal/core/... && go test ./internal/core/... -count=1 -race`
Expected: ผ่านทั้งหมด

- [ ] **Step 5: Commit**

```bash
git add internal/core/service
git commit -m "feat(service): add rate-limit keys and per-endpoint rules"
```

---

### Task 9: `Pacer` และ `NowForStorage`

**Files:**
- Create: `internal/core/service/timing.go`, `internal/core/service/timing_test.go`

**Interfaces:**
- Consumes: `port.Clock`, `fakeClock` (Task 7)
- Produces:
  - `func NewPacer(clock port.Clock, floor, jitter time.Duration) *Pacer`
  - `(*Pacer).Wait(ctx context.Context, start time.Time)` — รอจนครบ `floor` + jitter สุ่ม 0..`jitter` นับจาก `start`; ถ้าครบแล้วไม่รอ; หยุดรอทันทีเมื่อ `ctx` สิ้นสุด; ใช้เป็น `defer pacer.Wait(ctx, clock.Now())` ใน service
  - `func NowForStorage(clock port.Clock) time.Time` — เวลาปัจจุบัน UTC ตัดที่ microsecond

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/core/service/timing_test.go`:

```go
package service

import (
	"context"
	"testing"
	"time"
)

// sleepRecorder replaces sleeping: it records the wait and moves the fake clock instead.
type sleepRecorder struct {
	clock *fakeClock
	calls []time.Duration
}

func (s *sleepRecorder) sleep(_ context.Context, d time.Duration) {
	s.calls = append(s.calls, d)
	s.clock.Advance(d)
}

func newTestPacer(clock *fakeClock, rec *sleepRecorder, floor, jitter, sample time.Duration) *Pacer {
	return &Pacer{
		clock: clock, floor: floor, jitter: jitter, sleep: rec.sleep,
		randomJitter: func(max time.Duration) time.Duration { return min(sample, max) },
	}
}

func TestPacerWaitsOutTheRestOfTheFloor(t *testing.T) {
	clock := newFakeClock()
	rec := &sleepRecorder{clock: clock}
	pacer := newTestPacer(clock, rec, 500*time.Millisecond, 100*time.Millisecond, 40*time.Millisecond)

	start := clock.Now()
	clock.Advance(120 * time.Millisecond) // the work took 120ms
	pacer.Wait(t.Context(), start)

	if len(rec.calls) != 1 || rec.calls[0] != 420*time.Millisecond { // 500 + 40 - 120
		t.Errorf("sleeps = %v, want [420ms]", rec.calls)
	}
	if elapsed := clock.Now().Sub(start); elapsed != 540*time.Millisecond {
		t.Errorf("the response took %v, want floor + jitter = 540ms", elapsed)
	}
}

func TestPacerDoesNotSleepWhenTheWorkWasSlower(t *testing.T) {
	clock := newFakeClock()
	rec := &sleepRecorder{clock: clock}
	pacer := newTestPacer(clock, rec, 500*time.Millisecond, 100*time.Millisecond, 100*time.Millisecond)

	start := clock.Now()
	clock.Advance(700 * time.Millisecond)
	pacer.Wait(t.Context(), start)
	if len(rec.calls) != 0 {
		t.Errorf("slept %v although the work already took longer than the target", rec.calls)
	}

	// Exactly on the target: nothing left to wait for either.
	start = clock.Now()
	clock.Advance(600 * time.Millisecond)
	pacer.Wait(t.Context(), start)
	if len(rec.calls) != 0 {
		t.Errorf("slept %v when the target had just been reached", rec.calls)
	}
}

func TestPacerWithoutJitterWaitsExactlyTheFloor(t *testing.T) {
	clock := newFakeClock()
	rec := &sleepRecorder{clock: clock}
	pacer := newTestPacer(clock, rec, 500*time.Millisecond, 0, 50*time.Millisecond)

	pacer.Wait(t.Context(), clock.Now())
	if len(rec.calls) != 1 || rec.calls[0] != 500*time.Millisecond {
		t.Errorf("sleeps = %v, want [500ms]", rec.calls)
	}
}

func TestSleepContextStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	start := time.Now()
	sleepContext(ctx, time.Hour)
	if time.Since(start) > time.Second {
		t.Errorf("sleepContext kept waiting %v after the context ended", time.Since(start))
	}

	start = time.Now()
	sleepContext(t.Context(), 30*time.Millisecond)
	if elapsed := time.Since(start); elapsed < 25*time.Millisecond {
		t.Errorf("sleepContext returned after %v, want about 30ms", elapsed)
	}
}

func TestCryptoJitterStaysInRange(t *testing.T) {
	if got := cryptoJitter(0); got != 0 {
		t.Errorf("cryptoJitter(0) = %v, want 0", got)
	}
	positive := false
	for range 200 {
		got := cryptoJitter(100 * time.Millisecond)
		if got < 0 || got > 100*time.Millisecond {
			t.Fatalf("cryptoJitter(100ms) = %v, outside 0..100ms", got)
		}
		positive = positive || got > 0
	}
	if !positive {
		t.Error("cryptoJitter(100ms) returned 0 in 200 draws")
	}
}

func TestNewPacerIsUsable(t *testing.T) {
	clock := newFakeClock()
	pacer := NewPacer(clock, 0, 0)
	pacer.Wait(t.Context(), clock.Now()) // must neither panic nor block
}

func TestNowForStorageTruncatesToMicroseconds(t *testing.T) {
	clock := newFakeClock()
	clock.now = time.Date(2026, 10, 22, 9, 0, 0, 123_456_789, time.FixedZone("ICT", 7*3600))

	got := NowForStorage(clock)
	if want := time.Date(2026, 10, 22, 2, 0, 0, 123_456_000, time.UTC); !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("NowForStorage() = %v, want %v in UTC", got, want)
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/service/ -run 'Pacer|SleepContext|CryptoJitter|NowForStorage' -count=1`
Expected: build fail — `undefined: Pacer`

- [ ] **Step 3: เขียนโค้ด**

สร้าง `internal/core/service/timing.go`:

```go
package service

import (
	"context"
	"crypto/rand"
	"math/big"
	"time"

	"go-backend-stock/internal/core/port"
)

// Pacer makes a response take at least a floor plus a random jitter, so response time does not
// reveal whether an email has an account (register, resend, password reset). Use it as
// `defer pacer.Wait(ctx, clock.Now())` at the top of the use case so success and error paths both
// wait. It is a property of the use case, not of HTTP, which is why it lives in the core.
type Pacer struct {
	clock        port.Clock
	floor        time.Duration
	jitter       time.Duration
	sleep        func(ctx context.Context, d time.Duration)
	randomJitter func(max time.Duration) time.Duration
}

// NewPacer returns a pacer that really sleeps. The defaults are a 500 ms floor and 0-100 ms of jitter.
func NewPacer(clock port.Clock, floor, jitter time.Duration) *Pacer {
	return &Pacer{clock: clock, floor: floor, jitter: jitter, sleep: sleepContext, randomJitter: cryptoJitter}
}

// Wait blocks until floor plus a random jitter in 0..jitter has passed since start. It returns at
// once if that time has already passed, and as soon as ctx ends.
func (p *Pacer) Wait(ctx context.Context, start time.Time) {
	target := p.floor + p.randomJitter(p.jitter)
	if remaining := target - p.clock.Now().Sub(start); remaining > 0 {
		p.sleep(ctx, remaining)
	}
}

func sleepContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// cryptoJitter returns a uniform duration in 0..max. If the random source fails it returns 0:
// less jitter weakens the timing defence slightly but must not fail the request.
func cryptoJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)+1))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64())
}

// NowForStorage returns the current time in UTC cut to microseconds. PostgreSQL keeps
// microseconds, so a time written with nanoseconds would compare differently in Go and in SQL
// by up to a microsecond when expiry is checked. Services pass this, not Clock.Now(), to repositories.
func NowForStorage(clock port.Clock) time.Time {
	return clock.Now().UTC().Truncate(time.Microsecond)
}
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/core; go vet ./internal/core/... && go test ./internal/core/... -count=1 -race`
Expected: ผ่านทั้งหมด

- [ ] **Step 5: Commit**

```bash
git add internal/core/service
git commit -m "feat(service): add response pacer and storage time helper"
```

---
### Task 10: Config ใหม่, `Secret` และ redaction

**Files:**
- Create: `internal/config/auth.go`, `internal/config/redact.go`, `internal/config/auth_test.go`
- Modify: `internal/config/config.go`, `internal/config/config_test.go`, `.env.example`, `docker-compose.yml`

**Interfaces:**
- Consumes: `parseIntInRange`, `parseDurationInRange`, `valueOr` (ใน `config.go`)
- Produces:
  - `type Secret string` — `String()`/`GoString()`/`MarshalJSON()`/`LogValue()` ได้ `[REDACTED]`; `Reveal() string`; `Bytes() []byte`
  - `Config.Auth AuthConfig` และ `Config.SMTP SMTPConfig` (ฟิลด์ตามตารางด้านล่าง)
  - `Config.String()`, `GoString()`, `MarshalJSON()`, `LogValue()` ที่สรุปเฉพาะค่าที่ปลอดภัย (ไม่มี `DatabaseURL`, secret หรือรหัส SMTP)
  - ค่าคงที่ `SMTPTLSNone = "none"`, `SMTPTLSStartTLS = "starttls"`, `SMTPTLSImplicit = "tls"`

| env | ฟิลด์ | ค่าเริ่มต้น / กติกา |
| --- | --- | --- |
| `CSRF_SECRET`, `OTP_SECRET`, `RATE_LIMIT_SECRET`, `OIDC_SECRET` | `Auth.CSRFSecret`, `OTPSecret`, `RateLimitSecret`, `OIDCSecret` (`Secret`) | ต้องมี ยาวอย่างน้อย 32 bytes; production ห้ามขึ้นต้น `dev-only-` และสี่ค่าต้องไม่ซ้ำกัน |
| `SESSION_TTL` | `Auth.SessionTTL` | 168h (1h–720h) |
| `PASSWORD_HASH_CONCURRENCY` | `Auth.PasswordHashConcurrency` | 4 (1–64) |
| `AUTH_RESPONSE_FLOOR`, `AUTH_RESPONSE_JITTER` | `Auth.ResponseFloor`, `ResponseJitter` | 500ms (0–5s), 100ms (0–1s) |
| `TRUSTED_PROXY_CIDRS` | `Auth.TrustedProxies []netip.Prefix` | ว่าง = ไม่เชื่อ header ใด; รายการ CIDR คั่นด้วย comma; ปฏิเสธ `/0`; ค่าที่ได้ผ่าน `Masked()` |
| `OTP_MAX_ATTEMPTS` | `Auth.OTPMaxAttempts` | 5 (1–20 ตาม DB CHECK) |
| `OTP_RESEND_COOLDOWN` | `Auth.OTPResendCooldown` | 60s (1s–1h) |
| `OTP_REQUESTS_PER_HOUR_EMAIL`, `OTP_REQUESTS_PER_HOUR_IP` | `Auth.OTPRequestsPerHourEmail`, `OTPRequestsPerHourIP` | 5, 20 (1–1000) |
| `LOGIN_ATTEMPTS_PER_EMAIL`, `LOGIN_ATTEMPTS_PER_IP` | `Auth.LoginAttemptsPerEmail`, `LoginAttemptsPerIP` | 10, 30 (1–1000) ต่อ 15 นาที |
| `SMTP_HOST`, `SMTP_PORT` | `SMTP.Host`, `SMTP.Port` | dev: `mailpit`, 1025; production ต้องตั้งเอง |
| `SMTP_USERNAME`, `SMTP_PASSWORD` | `SMTP.Username`, `SMTP.Password` (`Secret`) | ว่างทั้งคู่ หรือมีทั้งคู่ |
| `SMTP_FROM` | `SMTP.From` | dev: `no-reply@localhost`; ต้องเป็นที่อยู่ล้วน ๆ ไม่มีชื่อ; production ต้องตั้งเอง |
| `SMTP_TLS` | `SMTP.TLS` | dev: `none`; `none`/`starttls`/`tls` (ไม่สนตัวพิมพ์); production ต้องตั้งเองและห้าม `none` |
| `MAIL_SEND_TIMEOUT` | `SMTP.Timeout` | 10s (1s–60s) |

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/config/auth_test.go`:

```go
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func devSecrets() map[string]string {
	return map[string]string{
		"CSRF_SECRET":       "dev-only-csrf-secret-0123456789abcdef",
		"OTP_SECRET":        "dev-only-otp-secret-0123456789abcdef",
		"RATE_LIMIT_SECRET": "dev-only-rate-limit-secret-0123456789",
		"OIDC_SECRET":       "dev-only-oidc-secret-0123456789abcdef",
	}
}

func validProdEnv() map[string]string {
	return map[string]string{
		"APP_ENV":           "production",
		"PUBLIC_ORIGIN":     "https://stock.example.com",
		"DATABASE_URL":      "postgres://app_runtime:prodpass@db.example.com:5432/postgres?sslmode=verify-full",
		"CSRF_SECRET":       strings.Repeat("c", 32),
		"OTP_SECRET":        strings.Repeat("o", 32),
		"RATE_LIMIT_SECRET": strings.Repeat("r", 32),
		"OIDC_SECRET":       strings.Repeat("i", 32),
		"SMTP_HOST":         "smtp.example.com",
		"SMTP_PORT":         "587",
		"SMTP_FROM":         "no-reply@example.com",
		"SMTP_TLS":          "starttls",
	}
}

func TestLoadAuthDefaults(t *testing.T) {
	cfg, err := Load(envFrom(validDevEnv()))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	a := cfg.Auth
	if a.CSRFSecret.Reveal() != devSecrets()["CSRF_SECRET"] || a.OTPSecret.Reveal() != devSecrets()["OTP_SECRET"] ||
		a.RateLimitSecret.Reveal() != devSecrets()["RATE_LIMIT_SECRET"] || a.OIDCSecret.Reveal() != devSecrets()["OIDC_SECRET"] {
		t.Error("secrets were not loaded")
	}
	if a.SessionTTL != 168*time.Hour || a.PasswordHashConcurrency != 4 ||
		a.ResponseFloor != 500*time.Millisecond || a.ResponseJitter != 100*time.Millisecond ||
		a.OTPMaxAttempts != 5 || a.OTPResendCooldown != time.Minute ||
		a.OTPRequestsPerHourEmail != 5 || a.OTPRequestsPerHourIP != 20 ||
		a.LoginAttemptsPerEmail != 10 || a.LoginAttemptsPerIP != 30 || len(a.TrustedProxies) != 0 {
		t.Errorf("auth defaults = %+v", a)
	}
	s := cfg.SMTP
	if s.Host != "mailpit" || s.Port != 1025 || s.Username != "" || s.Password != "" ||
		s.From != "no-reply@localhost" || s.TLS != SMTPTLSNone || s.Timeout != 10*time.Second {
		t.Errorf("smtp defaults = %+v", s)
	}
}

func TestLoadAuthOverrides(t *testing.T) {
	env := validDevEnv()
	for k, v := range map[string]string{
		"SESSION_TTL": "24h", "PASSWORD_HASH_CONCURRENCY": "8", "AUTH_RESPONSE_FLOOR": "0s", "AUTH_RESPONSE_JITTER": "0s",
		"OTP_MAX_ATTEMPTS": "3", "OTP_RESEND_COOLDOWN": "2m", "OTP_REQUESTS_PER_HOUR_EMAIL": "3", "OTP_REQUESTS_PER_HOUR_IP": "10",
		"LOGIN_ATTEMPTS_PER_EMAIL": "4", "LOGIN_ATTEMPTS_PER_IP": "12", "TRUSTED_PROXY_CIDRS": "172.16.0.0/12",
		"SMTP_HOST": "smtp.example.com", "SMTP_PORT": "587", "SMTP_USERNAME": "mailer", "SMTP_PASSWORD": "mail-pass",
		"SMTP_FROM": "noreply@example.com", "SMTP_TLS": "STARTTLS", "MAIL_SEND_TIMEOUT": "20s",
	} {
		env[k] = v
	}
	cfg, err := Load(envFrom(env))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	a, s := cfg.Auth, cfg.SMTP
	if a.SessionTTL != 24*time.Hour || a.PasswordHashConcurrency != 8 || a.ResponseFloor != 0 || a.ResponseJitter != 0 ||
		a.OTPMaxAttempts != 3 || a.OTPResendCooldown != 2*time.Minute || a.OTPRequestsPerHourEmail != 3 ||
		a.OTPRequestsPerHourIP != 10 || a.LoginAttemptsPerEmail != 4 || a.LoginAttemptsPerIP != 12 {
		t.Errorf("auth overrides = %+v", a)
	}
	if len(a.TrustedProxies) != 1 || a.TrustedProxies[0] != netip.MustParsePrefix("172.16.0.0/12") {
		t.Errorf("TrustedProxies = %v", a.TrustedProxies)
	}
	if s.Host != "smtp.example.com" || s.Port != 587 || s.Username != "mailer" || s.Password.Reveal() != "mail-pass" ||
		s.From != "noreply@example.com" || s.TLS != SMTPTLSStartTLS || s.Timeout != 20*time.Second {
		t.Errorf("smtp overrides = %+v", s)
	}
}

func TestLoadAuthRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(map[string]string)
		wantErr string
	}{
		{"missing csrf secret", func(e map[string]string) { delete(e, "CSRF_SECRET") }, "CSRF_SECRET is required"},
		{"short otp secret", func(e map[string]string) { e["OTP_SECRET"] = strings.Repeat("x", 31) }, "OTP_SECRET must be at least 32 bytes"},
		{"missing rate limit secret", func(e map[string]string) { delete(e, "RATE_LIMIT_SECRET") }, "RATE_LIMIT_SECRET is required"},
		{"missing oidc secret", func(e map[string]string) { delete(e, "OIDC_SECRET") }, "OIDC_SECRET is required"},
		{"session ttl too short", func(e map[string]string) { e["SESSION_TTL"] = "30m" }, "SESSION_TTL"},
		{"session ttl too long", func(e map[string]string) { e["SESSION_TTL"] = "721h" }, "SESSION_TTL"},
		{"hash concurrency zero", func(e map[string]string) { e["PASSWORD_HASH_CONCURRENCY"] = "0" }, "PASSWORD_HASH_CONCURRENCY"},
		{"hash concurrency 65", func(e map[string]string) { e["PASSWORD_HASH_CONCURRENCY"] = "65" }, "PASSWORD_HASH_CONCURRENCY"},
		{"floor too long", func(e map[string]string) { e["AUTH_RESPONSE_FLOOR"] = "6s" }, "AUTH_RESPONSE_FLOOR"},
		{"jitter too long", func(e map[string]string) { e["AUTH_RESPONSE_JITTER"] = "2s" }, "AUTH_RESPONSE_JITTER"},
		{"negative jitter", func(e map[string]string) { e["AUTH_RESPONSE_JITTER"] = "-1s" }, "AUTH_RESPONSE_JITTER"},
		{"otp attempts zero", func(e map[string]string) { e["OTP_MAX_ATTEMPTS"] = "0" }, "OTP_MAX_ATTEMPTS"},
		{"otp attempts above the database ceiling", func(e map[string]string) { e["OTP_MAX_ATTEMPTS"] = "21" }, "OTP_MAX_ATTEMPTS"},
		{"cooldown zero", func(e map[string]string) { e["OTP_RESEND_COOLDOWN"] = "0s" }, "OTP_RESEND_COOLDOWN"},
		{"cooldown two hours", func(e map[string]string) { e["OTP_RESEND_COOLDOWN"] = "2h" }, "OTP_RESEND_COOLDOWN"},
		{"otp per hour email zero", func(e map[string]string) { e["OTP_REQUESTS_PER_HOUR_EMAIL"] = "0" }, "OTP_REQUESTS_PER_HOUR_EMAIL"},
		{"otp per hour ip zero", func(e map[string]string) { e["OTP_REQUESTS_PER_HOUR_IP"] = "0" }, "OTP_REQUESTS_PER_HOUR_IP"},
		{"login per email zero", func(e map[string]string) { e["LOGIN_ATTEMPTS_PER_EMAIL"] = "0" }, "LOGIN_ATTEMPTS_PER_EMAIL"},
		{"login per ip zero", func(e map[string]string) { e["LOGIN_ATTEMPTS_PER_IP"] = "0" }, "LOGIN_ATTEMPTS_PER_IP"},
		{"cidr without mask", func(e map[string]string) { e["TRUSTED_PROXY_CIDRS"] = "10.0.0.1" }, "TRUSTED_PROXY_CIDRS"},
		{"cidr trusting everything", func(e map[string]string) { e["TRUSTED_PROXY_CIDRS"] = "0.0.0.0/0" }, "TRUSTED_PROXY_CIDRS"},
		{"ipv6 cidr trusting everything", func(e map[string]string) { e["TRUSTED_PROXY_CIDRS"] = "::/0" }, "TRUSTED_PROXY_CIDRS"},
		{"one bad cidr in a list", func(e map[string]string) { e["TRUSTED_PROXY_CIDRS"] = "10.0.0.0/8,garbage" }, "TRUSTED_PROXY_CIDRS"},
		{"smtp port zero", func(e map[string]string) { e["SMTP_PORT"] = "0" }, "SMTP_PORT"},
		{"smtp port too large", func(e map[string]string) { e["SMTP_PORT"] = "70000" }, "SMTP_PORT"},
		{"smtp port text", func(e map[string]string) { e["SMTP_PORT"] = "abc" }, "SMTP_PORT"},
		{"unknown smtp tls", func(e map[string]string) { e["SMTP_TLS"] = "ssl" }, "SMTP_TLS must be one of"},
		{"smtp from with a name", func(e map[string]string) { e["SMTP_FROM"] = "Stock <a@example.com>" }, "SMTP_FROM"},
		{"smtp from with a header", func(e map[string]string) { e["SMTP_FROM"] = "a@example.com\r\nBcc: b@example.com" }, "SMTP_FROM"},
		{"smtp from not an address", func(e map[string]string) { e["SMTP_FROM"] = "not-an-address" }, "SMTP_FROM"},
		{"smtp username without password", func(e map[string]string) { e["SMTP_USERNAME"] = "mailer" }, "SMTP_USERNAME and SMTP_PASSWORD"},
		{"smtp password without username", func(e map[string]string) { e["SMTP_PASSWORD"] = "pw" }, "SMTP_USERNAME and SMTP_PASSWORD"},
		{"mail timeout zero", func(e map[string]string) { e["MAIL_SEND_TIMEOUT"] = "0s" }, "MAIL_SEND_TIMEOUT"},
		{"mail timeout two minutes", func(e map[string]string) { e["MAIL_SEND_TIMEOUT"] = "2m" }, "MAIL_SEND_TIMEOUT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validDevEnv()
			tt.mutate(env)
			_, err := Load(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadProductionAuthRules(t *testing.T) {
	if _, err := Load(envFrom(validProdEnv())); err != nil {
		t.Fatalf("Load(valid production) error = %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(map[string]string)
		wantErr string
	}{
		{"development placeholder", func(e map[string]string) { e["CSRF_SECRET"] = "dev-only-" + strings.Repeat("x", 30) }, "CSRF_SECRET must not use a development placeholder"},
		{"duplicate secrets", func(e map[string]string) { e["OTP_SECRET"] = e["CSRF_SECRET"] }, "must all be different"},
		{"no smtp host", func(e map[string]string) { delete(e, "SMTP_HOST") }, "SMTP_HOST is required in production"},
		{"no smtp port", func(e map[string]string) { delete(e, "SMTP_PORT") }, "SMTP_PORT is required in production"},
		{"no smtp from", func(e map[string]string) { delete(e, "SMTP_FROM") }, "SMTP_FROM is required in production"},
		{"no smtp tls", func(e map[string]string) { delete(e, "SMTP_TLS") }, "SMTP_TLS is required in production"},
		{"smtp tls none", func(e map[string]string) { e["SMTP_TLS"] = "none" }, "SMTP_TLS=none is not allowed in production"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validProdEnv()
			tt.mutate(env)
			_, err := Load(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadDevelopmentAllowsSharedSecrets(t *testing.T) {
	env := validDevEnv()
	for _, k := range []string{"OTP_SECRET", "RATE_LIMIT_SECRET", "OIDC_SECRET"} {
		env[k] = env["CSRF_SECRET"]
	}
	if _, err := Load(envFrom(env)); err != nil {
		t.Errorf("development must accept repeated placeholder secrets, error = %v", err)
	}
}

func TestLoadTrustedProxyCIDRs(t *testing.T) {
	env := validDevEnv()
	env["TRUSTED_PROXY_CIDRS"] = "10.1.2.3/8, 192.168.1.0/24 ,2001:db8::/32"
	cfg, err := Load(envFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.168.1.0/24"), netip.MustParsePrefix("2001:db8::/32")}
	if fmt.Sprint(cfg.Auth.TrustedProxies) != fmt.Sprint(want) {
		t.Errorf("TrustedProxies = %v, want %v (host bits cleared)", cfg.Auth.TrustedProxies, want)
	}
}

func TestLoadErrorsNeverEchoValues(t *testing.T) {
	env := validDevEnv()
	env["CSRF_SECRET"] = "short-ZQXJ"
	env["TRUSTED_PROXY_CIDRS"] = "ZQXJ"
	env["SMTP_FROM"] = "ZQXJ-not-an-address"
	env["SMTP_TLS"] = "ZQXJ"
	env["SMTP_PORT"] = "ZQXJ"
	_, err := Load(envFrom(env))
	if err == nil {
		t.Fatal("Load() accepted invalid values")
	}
	if strings.Contains(err.Error(), "ZQXJ") {
		t.Errorf("error echoes a configured value: %v", err)
	}
}

func TestConfigNeverPrintsSecrets(t *testing.T) {
	env := validProdEnv()
	env["DATABASE_URL"] = "postgres://app_runtime:S3cretDbPass@db.example.com:5432/postgres?sslmode=verify-full"
	env["SMTP_USERNAME"] = "mailer"
	env["SMTP_PASSWORD"] = "S3cretMailPass"
	cfg, err := Load(envFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	secrets := []string{
		"S3cretDbPass", "S3cretMailPass",
		strings.Repeat("c", 32), strings.Repeat("o", 32), strings.Repeat("r", 32), strings.Repeat("i", 32),
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	log.Info("config", slog.Any("config", cfg))
	log.Info("auth", slog.Any("auth", cfg.Auth), slog.Any("smtp", cfg.SMTP))
	log.Info("secret", slog.Any("secret", cfg.Auth.OTPSecret))
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}

	outputs := []string{
		buf.String(), string(data),
		fmt.Sprint(cfg), fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg),
		fmt.Sprintf("%+v", cfg.Auth), fmt.Sprintf("%+v", cfg.SMTP), fmt.Sprintf("%#v", cfg.Auth.CSRFSecret),
	}
	for _, out := range outputs {
		for _, secret := range secrets {
			if strings.Contains(out, secret) {
				t.Errorf("output leaks %q: %s", secret, out)
			}
		}
	}
	if cfg.Auth.CSRFSecret.Reveal() != strings.Repeat("c", 32) || len(cfg.Auth.CSRFSecret.Bytes()) != 32 {
		t.Error("Reveal/Bytes must still return the value")
	}
}
```

แก้ `internal/config/config_test.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
func validDevEnv() map[string]string {
	return map[string]string{
		"APP_ENV":       "development",
		"PUBLIC_ORIGIN": "http://localhost:8080",
		"DATABASE_URL":  "postgres://app_runtime:devpass@postgres:5432/stock?sslmode=disable",
	}
}
```

   ด้วย

```go
func validDevEnv() map[string]string {
	env := map[string]string{
		"APP_ENV":       "development",
		"PUBLIC_ORIGIN": "http://localhost:8080",
		"DATABASE_URL":  "postgres://app_runtime:devpass@postgres:5432/stock?sslmode=disable",
	}
	for k, v := range devSecrets() {
		env[k] = v
	}
	return env
}
```

2. แทน

```go
	cfg, err := Load(envFrom(map[string]string{
		"APP_ENV":       "production",
		"PUBLIC_ORIGIN": "https://stock.example.com",
		"DATABASE_URL":  "postgres://app_runtime:prodpass@db.example.com:5432/postgres?sslmode=verify-full",
	}))
```

   ด้วย

```go
	cfg, err := Load(envFrom(validProdEnv()))
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/config/ -count=1`
Expected: build fail — `cfg.Auth undefined`, `undefined: SMTPTLSNone`

- [ ] **Step 3: เขียนโค้ด**

แก้ `internal/config/config.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
	DocsTryItOutEnabled bool
	ShutdownTimeout     time.Duration
}
```

   ด้วย

```go
	DocsTryItOutEnabled bool
	ShutdownTimeout     time.Duration
	Auth                AuthConfig
	SMTP                SMTPConfig
}
```

2. แทน

```go
	cfg.ShutdownTimeout, err = parseDurationInRange("SHUTDOWN_TIMEOUT", getenv("SHUTDOWN_TIMEOUT"), 10*time.Second, time.Second, time.Minute)
	add(err)
```

   ด้วย

```go
	cfg.ShutdownTimeout, err = parseDurationInRange("SHUTDOWN_TIMEOUT", getenv("SHUTDOWN_TIMEOUT"), 10*time.Second, time.Second, time.Minute)
	add(err)

	var authErrs, smtpErrs []error
	cfg.Auth, authErrs = loadAuth(getenv, prod)
	cfg.SMTP, smtpErrs = loadSMTP(getenv, prod)
	errs = append(errs, authErrs...)
	errs = append(errs, smtpErrs...)
```

สร้าง `internal/config/redact.go`:

```go
package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

const redactedValue = "[REDACTED]"

// Secret is a configuration value that must never be printed or logged. It formats, marshals and
// logs as [REDACTED]; Reveal and Bytes return the real value for the code that needs it.
type Secret string

func (Secret) String() string   { return redactedValue }
func (Secret) GoString() string { return redactedValue }

// MarshalJSON keeps the value out of JSON.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redactedValue + `"`), nil }

// LogValue keeps the value out of slog records.
func (Secret) LogValue() slog.Value { return slog.StringValue(redactedValue) }

// Reveal returns the value.
func (s Secret) Reveal() string { return string(s) }

// Bytes returns the value as bytes, for HMAC keys.
func (s Secret) Bytes() []byte { return []byte(s) }

// String summarises the configuration for humans. It leaves out everything that can hold a
// credential: the database URL, the secrets and the SMTP credentials.
func (c Config) String() string {
	return fmt.Sprintf("config{env=%s addr=%s origin=%s docs=%t try_it_out=%t smtp=%s:%d smtp_tls=%s}",
		c.Env, c.HTTPAddr, c.PublicOrigin, c.DocsEnabled, c.DocsTryItOutEnabled, c.SMTP.Host, c.SMTP.Port, c.SMTP.TLS)
}

// GoString makes %#v print the same safe summary.
func (c Config) GoString() string { return c.String() }

// MarshalJSON marshals the same safe summary, so encoding a Config cannot expose the database URL.
func (c Config) MarshalJSON() ([]byte, error) { return json.Marshal(c.String()) }

// LogValue makes slog log the same safe summary as a group.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("env", string(c.Env)),
		slog.String("http_addr", c.HTTPAddr),
		slog.String("public_origin", c.PublicOrigin),
		slog.Bool("docs_enabled", c.DocsEnabled),
		slog.Bool("docs_try_it_out_enabled", c.DocsTryItOutEnabled),
		slog.String("smtp_host", c.SMTP.Host),
		slog.Int("smtp_port", c.SMTP.Port),
		slog.String("smtp_tls", c.SMTP.TLS),
	)
}
```

สร้าง `internal/config/auth.go`:

```go
package config

import (
	"errors"
	"fmt"
	"net/mail"
	"net/netip"
	"strings"
	"time"
)

const (
	minSecretBytes  = 32
	devSecretPrefix = "dev-only-"
	maxRateLimit    = 1000
)

// SMTP_TLS values.
const (
	SMTPTLSNone     = "none"
	SMTPTLSStartTLS = "starttls"
	SMTPTLSImplicit = "tls"
)

// AuthConfig holds the settings of registration, login, sessions and OTP.
type AuthConfig struct {
	CSRFSecret              Secret
	OTPSecret               Secret
	RateLimitSecret         Secret
	OIDCSecret              Secret
	SessionTTL              time.Duration
	PasswordHashConcurrency int
	ResponseFloor           time.Duration
	ResponseJitter          time.Duration
	TrustedProxies          []netip.Prefix
	OTPMaxAttempts          int
	OTPResendCooldown       time.Duration
	OTPRequestsPerHourEmail int
	OTPRequestsPerHourIP    int
	LoginAttemptsPerEmail   int
	LoginAttemptsPerIP      int
}

// SMTPConfig holds the settings of the mail server. TLS is one of the SMTPTLS* values.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password Secret
	From     string
	TLS      string
	Timeout  time.Duration
}

func loadAuth(getenv func(string) string, prod bool) (AuthConfig, []error) {
	var cfg AuthConfig
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	var err error

	secrets := []struct {
		name string
		dst  *Secret
	}{
		{"CSRF_SECRET", &cfg.CSRFSecret},
		{"OTP_SECRET", &cfg.OTPSecret},
		{"RATE_LIMIT_SECRET", &cfg.RateLimitSecret},
		{"OIDC_SECRET", &cfg.OIDCSecret},
	}
	for _, s := range secrets {
		*s.dst, err = parseSecret(s.name, getenv(s.name), prod)
		add(err)
	}
	if prod {
		seen := map[Secret]bool{}
		for _, s := range secrets {
			if *s.dst == "" {
				continue
			}
			if seen[*s.dst] {
				add(errors.New("CSRF_SECRET, OTP_SECRET, RATE_LIMIT_SECRET and OIDC_SECRET must all be different"))
				break
			}
			seen[*s.dst] = true
		}
	}

	cfg.SessionTTL, err = parseDurationInRange("SESSION_TTL", getenv("SESSION_TTL"), 168*time.Hour, time.Hour, 720*time.Hour)
	add(err)
	cfg.PasswordHashConcurrency, err = parseIntInRange("PASSWORD_HASH_CONCURRENCY", getenv("PASSWORD_HASH_CONCURRENCY"), 4, 1, 64)
	add(err)
	cfg.ResponseFloor, err = parseDurationInRange("AUTH_RESPONSE_FLOOR", getenv("AUTH_RESPONSE_FLOOR"), 500*time.Millisecond, 0, 5*time.Second)
	add(err)
	cfg.ResponseJitter, err = parseDurationInRange("AUTH_RESPONSE_JITTER", getenv("AUTH_RESPONSE_JITTER"), 100*time.Millisecond, 0, time.Second)
	add(err)
	cfg.TrustedProxies, err = parseCIDRs(getenv("TRUSTED_PROXY_CIDRS"))
	add(err)
	cfg.OTPMaxAttempts, err = parseIntInRange("OTP_MAX_ATTEMPTS", getenv("OTP_MAX_ATTEMPTS"), 5, 1, 20)
	add(err)
	cfg.OTPResendCooldown, err = parseDurationInRange("OTP_RESEND_COOLDOWN", getenv("OTP_RESEND_COOLDOWN"), time.Minute, time.Second, time.Hour)
	add(err)
	cfg.OTPRequestsPerHourEmail, err = parseIntInRange("OTP_REQUESTS_PER_HOUR_EMAIL", getenv("OTP_REQUESTS_PER_HOUR_EMAIL"), 5, 1, maxRateLimit)
	add(err)
	cfg.OTPRequestsPerHourIP, err = parseIntInRange("OTP_REQUESTS_PER_HOUR_IP", getenv("OTP_REQUESTS_PER_HOUR_IP"), 20, 1, maxRateLimit)
	add(err)
	cfg.LoginAttemptsPerEmail, err = parseIntInRange("LOGIN_ATTEMPTS_PER_EMAIL", getenv("LOGIN_ATTEMPTS_PER_EMAIL"), 10, 1, maxRateLimit)
	add(err)
	cfg.LoginAttemptsPerIP, err = parseIntInRange("LOGIN_ATTEMPTS_PER_IP", getenv("LOGIN_ATTEMPTS_PER_IP"), 30, 1, maxRateLimit)
	add(err)
	return cfg, errs
}

func loadSMTP(getenv func(string) string, prod bool) (SMTPConfig, []error) {
	var cfg SMTPConfig
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	var err error

	cfg.Host = strings.TrimSpace(getenv("SMTP_HOST"))
	if cfg.Host == "" {
		if prod {
			add(errors.New("SMTP_HOST is required in production"))
		} else {
			cfg.Host = "mailpit"
		}
	}
	if prod && strings.TrimSpace(getenv("SMTP_PORT")) == "" {
		add(errors.New("SMTP_PORT is required in production"))
	}
	cfg.Port, err = parseIntInRange("SMTP_PORT", getenv("SMTP_PORT"), 1025, 1, 65535)
	add(err)

	cfg.Username = strings.TrimSpace(getenv("SMTP_USERNAME"))
	cfg.Password = Secret(getenv("SMTP_PASSWORD"))
	if (cfg.Username == "") != (cfg.Password == "") {
		add(errors.New("SMTP_USERNAME and SMTP_PASSWORD must be set together"))
	}

	from := strings.TrimSpace(getenv("SMTP_FROM"))
	switch {
	case from == "" && prod:
		add(errors.New("SMTP_FROM is required in production"))
	case from == "":
		cfg.From = "no-reply@localhost"
	default:
		addr, parseErr := mail.ParseAddress(from)
		if parseErr != nil || addr.Address != from || strings.ContainsAny(from, "\r\n") {
			add(errors.New("SMTP_FROM must be a plain email address such as no-reply@example.com"))
		} else {
			cfg.From = from
		}
	}

	switch mode := strings.ToLower(strings.TrimSpace(getenv("SMTP_TLS"))); mode {
	case "":
		if prod {
			add(errors.New("SMTP_TLS is required in production and must be starttls or tls"))
		} else {
			cfg.TLS = SMTPTLSNone
		}
	case SMTPTLSNone:
		if prod {
			add(errors.New("SMTP_TLS=none is not allowed in production"))
		} else {
			cfg.TLS = SMTPTLSNone
		}
	case SMTPTLSStartTLS, SMTPTLSImplicit:
		cfg.TLS = mode
	default:
		add(errors.New("SMTP_TLS must be one of none, starttls, tls"))
	}

	cfg.Timeout, err = parseDurationInRange("MAIL_SEND_TIMEOUT", getenv("MAIL_SEND_TIMEOUT"), 10*time.Second, time.Second, time.Minute)
	add(err)
	return cfg, errs
}

// parseSecret never puts the value in its errors.
func parseSecret(name, raw string, prod bool) (Secret, error) {
	v := strings.TrimSpace(raw)
	switch {
	case v == "":
		return "", fmt.Errorf("%s is required", name)
	case len(v) < minSecretBytes:
		return "", fmt.Errorf("%s must be at least %d bytes", name, minSecretBytes)
	case prod && strings.HasPrefix(v, devSecretPrefix):
		return "", fmt.Errorf("%s must not use a development placeholder in production", name)
	}
	return Secret(v), nil
}

// parseCIDRs reads a comma-separated list of CIDR ranges. A range that trusts every address
// (/0) is refused: it would let any client choose the address that rate limits are keyed on.
func parseCIDRs(raw string) ([]netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []netip.Prefix
	for _, item := range strings.Split(raw, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(item))
		if err != nil || prefix.Bits() == 0 {
			return nil, errors.New("TRUSTED_PROXY_CIDRS must be a comma-separated list of CIDR ranges such as 10.0.0.0/8, and must not trust every address")
		}
		out = append(out, prefix.Masked())
	}
	return out, nil
}
```

แก้ `.env.example` — แทน

```text
APP_MAINTENANCE_PASSWORD=
```

ด้วย

```text
APP_MAINTENANCE_PASSWORD=

# Secrets for CSRF tokens, OTP digests, rate-limit keys and Google OIDC (milestone 4c).
# Development placeholders only: production rejects values that start with "dev-only-" and
# needs four different values of at least 32 bytes each.
CSRF_SECRET=dev-only-csrf-secret-0123456789abcdef
OTP_SECRET=dev-only-otp-secret-0123456789abcdef
RATE_LIMIT_SECRET=dev-only-rate-limit-secret-0123456789
OIDC_SECRET=dev-only-oidc-secret-0123456789abcdef
```

แก้ `docker-compose.yml` — แทน

```yaml
      DOCS_TRY_IT_OUT_ENABLED: ${DOCS_TRY_IT_OUT_ENABLED}
      SHUTDOWN_TIMEOUT: 10s
```

ด้วย

```yaml
      DOCS_TRY_IT_OUT_ENABLED: ${DOCS_TRY_IT_OUT_ENABLED}
      SHUTDOWN_TIMEOUT: 10s
      CSRF_SECRET: ${CSRF_SECRET}
      OTP_SECRET: ${OTP_SECRET}
      RATE_LIMIT_SECRET: ${RATE_LIMIT_SECRET}
      OIDC_SECRET: ${OIDC_SECRET}
      SMTP_HOST: mailpit
      SMTP_PORT: "1025"
      SMTP_FROM: no-reply@localhost
      SMTP_TLS: none
```

- [ ] **Step 4: รัน test ให้ผ่านและตรวจ compose**

Run: `gofmt -l internal/config; go vet ./internal/config/ && go test ./internal/config/ -count=1 -race && docker compose config --quiet`
Expected: test ผ่าน; `docker compose config --quiet` ไม่พิมพ์ error (ถ้า `.env` ของคุณยังไม่มีสี่บรรทัด secret ใหม่ compose จะเตือนว่าตัวแปรว่าง — ให้คัดลอกสี่บรรทัดจาก `.env.example` ไปใส่ `.env` ซึ่งถูก ignore ไม่ commit)

ตรวจว่า API ยังเริ่มได้ด้วย config ใหม่ (ถ้า Docker ใช้งานได้):

Run: `docker compose up -d --build && sleep 8 && curl -fsS http://127.0.0.1:8080/readyz && docker compose down`
Expected: `readyz` ตอบ 200; ถ้า API ไม่เริ่ม `docker compose logs api` ต้องบอกว่าขาดตัวแปรใด (ไม่มีค่า secret ในข้อความ)

- [ ] **Step 5: Commit**

```bash
git add internal/config .env.example docker-compose.yml
git commit -m "feat(config): add auth, session and SMTP settings with redacted secrets"
```

---

### Task 11: `ClientIPResolver`

**Files:**
- Create: `internal/adapter/http/clientip.go`, `internal/adapter/http/clientip_test.go`

**Interfaces:**
- Consumes: `config.AuthConfig.TrustedProxies` (`[]netip.Prefix`) — ต่อสายที่ composition root ใน 4b
- Produces:
  - `func NewClientIPResolver(trusted []netip.Prefix) *ClientIPResolver`
  - `(*ClientIPResolver).Resolve(remoteAddr string, forwardedFor []string) netip.Addr` — `remoteAddr` เป็น `host:port` ของ connection; `forwardedFor` คือค่าของ header `X-Forwarded-For` ทุกบรรทัด; ผลเป็น IPv4 ที่ unmap แล้ว; อ่านไม่ได้ = `netip.Addr{}`

กติกา: remote ที่ไม่อยู่ใน trusted → ใช้ remote และเมินทุก header; remote อยู่ใน trusted → เดินจาก hop ขวาสุดไปซ้าย ข้าม hop ที่อยู่ใน trusted แล้วใช้ hop แรกที่ไม่อยู่ใน trusted; hop ที่อ่านไม่ได้หรือไม่มี hop ที่ไม่เชื่อถือเลย → ใช้ remote (ปลอดภัยไว้ก่อน: ลูกค้าทั้งหมดหลัง proxy นั้นใช้โควตาร่วมกัน ดีกว่าปล่อยให้ปลอม)

- [ ] **Step 1: เขียน failing test**

สร้าง `internal/adapter/http/clientip_test.go`:

```go
package http

import (
	"net/netip"
	"testing"
)

func TestClientIPResolver(t *testing.T) {
	resolver := NewClientIPResolver([]netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("2001:db8::/32"),
	})
	tests := []struct {
		name      string
		remote    string
		forwarded []string
		want      string // "" means the zero Addr
	}{
		{"direct client", "203.0.113.9:5000", nil, "203.0.113.9"},
		{"untrusted peer cannot choose its address", "203.0.113.9:5000", []string{"1.2.3.4"}, "203.0.113.9"},
		{"trusted proxy forwards the client", "10.0.0.5:4000", []string{"198.51.100.7"}, "198.51.100.7"},
		{"trusted proxy chain", "10.0.0.5:4000", []string{"198.51.100.7, 10.0.0.9"}, "198.51.100.7"},
		{"forged left-most entry is ignored", "10.0.0.5:4000", []string{"6.6.6.6, 198.51.100.7"}, "198.51.100.7"},
		{"several header lines", "10.0.0.5:4000", []string{"6.6.6.6", "198.51.100.7"}, "198.51.100.7"},
		{"every hop trusted", "10.0.0.5:4000", []string{"10.0.0.8, 10.0.0.9"}, "10.0.0.5"},
		{"trusted proxy without header", "10.0.0.5:4000", nil, "10.0.0.5"},
		{"unreadable hop", "10.0.0.5:4000", []string{"garbage"}, "10.0.0.5"},
		{"unreadable hop next to the proxy", "10.0.0.5:4000", []string{"198.51.100.7, garbage"}, "10.0.0.5"},
		{"hop with a port", "10.0.0.5:4000", []string{"198.51.100.7:1234"}, "10.0.0.5"},
		{"empty hop", "10.0.0.5:4000", []string{"198.51.100.7,"}, "10.0.0.5"},
		{"ipv6 client", "[2001:4860::1]:443", nil, "2001:4860::1"},
		{"ipv6 trusted proxy", "[2001:db8::1]:443", []string{"2001:4860::7"}, "2001:4860::7"},
		{"ipv4 in ipv6 notation", "[::ffff:203.0.113.9]:80", nil, "203.0.113.9"},
		{"ipv4-mapped proxy hop", "10.0.0.5:4000", []string{"::ffff:198.51.100.7"}, "198.51.100.7"},
		{"unreadable remote", "garbage", []string{"198.51.100.7"}, ""},
		{"empty remote", "", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolver.Resolve(tt.remote, tt.forwarded)
			want := netip.Addr{}
			if tt.want != "" {
				want = netip.MustParseAddr(tt.want)
			}
			if got != want {
				t.Errorf("Resolve(%q, %q) = %v, want %v", tt.remote, tt.forwarded, got, want)
			}
		})
	}
}

func TestClientIPResolverWithoutTrustedProxiesIgnoresHeaders(t *testing.T) {
	for _, resolver := range []*ClientIPResolver{NewClientIPResolver(nil), NewClientIPResolver([]netip.Prefix{})} {
		got := resolver.Resolve("10.0.0.5:4000", []string{"198.51.100.7"})
		if want := netip.MustParseAddr("10.0.0.5"); got != want {
			t.Errorf("Resolve() = %v, want the peer %v: with no trusted proxy no header is believed", got, want)
		}
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/adapter/http/ -run TestClientIP -count=1`
Expected: build fail — `undefined: NewClientIPResolver`

- [ ] **Step 3: เขียนโค้ด**

สร้าง `internal/adapter/http/clientip.go`:

```go
package http

import (
	"net/netip"
	"slices"
	"strings"
)

// ClientIPResolver finds the address of the client for rate limiting and audit. It believes
// X-Forwarded-For only when the connection comes from a configured trusted proxy, and then reads
// it from the right: the entry the trusted proxy itself appended is the last one, and anything to
// its left was written by whoever sent the request and can be forged.
type ClientIPResolver struct{ trusted []netip.Prefix }

// NewClientIPResolver returns a resolver that trusts the given proxy networks (none = trust no header).
func NewClientIPResolver(trusted []netip.Prefix) *ClientIPResolver {
	return &ClientIPResolver{trusted: slices.Clone(trusted)}
}

// Resolve returns the client address, or the zero Addr when remoteAddr cannot be read.
// remoteAddr is the connection's "host:port"; forwardedFor holds the value of every
// X-Forwarded-For header line. A hop that cannot be read, or a header made only of trusted
// proxies, makes Resolve return the proxy's own address: everyone behind it then shares one
// quota, which is safer than guessing.
func (r *ClientIPResolver) Resolve(remoteAddr string, forwardedFor []string) netip.Addr {
	remote, ok := parseRemote(remoteAddr)
	if !ok {
		return netip.Addr{}
	}
	if !r.isTrusted(remote) {
		return remote
	}
	var hops []string
	for _, line := range forwardedFor {
		hops = append(hops, strings.Split(line, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return remote
		}
		hop = hop.Unmap().WithZone("")
		if !r.isTrusted(hop) {
			return hop
		}
	}
	return remote
}

func (r *ClientIPResolver) isTrusted(addr netip.Addr) bool {
	return slices.ContainsFunc(r.trusted, func(p netip.Prefix) bool { return p.Contains(addr) })
}

func parseRemote(remoteAddr string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(remoteAddr); err == nil {
		return ap.Addr().Unmap().WithZone(""), true
	}
	if addr, err := netip.ParseAddr(remoteAddr); err == nil {
		return addr.Unmap().WithZone(""), true
	}
	return netip.Addr{}, false
}
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/adapter/http; go vet ./internal/adapter/http/ && go test ./internal/adapter/http/... -count=1 -race`
Expected: ผ่านทั้งหมด

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/http/clientip.go internal/adapter/http/clientip_test.go
git commit -m "feat(http): resolve the client IP behind trusted proxies"
```

---

### Task 12: `Mailer` port และ `SMTPMailer`

**Files:**
- Create: `internal/core/port/mailer.go`, `internal/adapter/mail/message.go`, `internal/adapter/mail/smtp.go`, `internal/adapter/mail/message_test.go`, `internal/adapter/mail/smtp_test.go`

**Interfaces:**
- Consumes: `domain.OTPMail`, `domain.Email`, `domain.NewError`
- Produces:
  - `port.Mailer` — `SendEmailVerification(ctx, domain.OTPMail) error`, `SendRegistrationAttempt(ctx, to domain.Email) error` (`SendPasswordReset`/`SendPasswordChanged` เพิ่มใน milestone 5)
  - `mail.TLSMode` (`TLSNone`, `TLSStartTLS`, `TLSImplicit`); `mail.SMTPConfig{Host string; Port int; Username, Password string; From string; TLS TLSMode; Timeout time.Duration}`
  - `mail.NewSMTPMailer(cfg SMTPConfig) (*SMTPMailer, error)`; `*SMTPMailer` implements `port.Mailer` แบบ synchronous, ส่งข้อความ plain text UTF-8 (`Content-Transfer-Encoding: 8bit`) และไม่พิมพ์รหัสผ่านของ SMTP ทาง `%v`/`%+v`
  - ที่อยู่หรือ header ที่มี CR, LF หรือ `,;<>"` เป็น `internal` error ก่อนเปิด connection

เนื้อหาอีเมลเป็นภาษาอังกฤษแบบ ASCII ในแผนนี้ (หัวข้อ `Subject` ที่ไม่ใช่ ASCII ต้อง encode ตาม RFC 2047 ซึ่ง `message.bytes()` ปฏิเสธไว้ก่อน) — การแปลเป็นภาษาไทยเป็นงานต่อยอดที่ไม่เปลี่ยน port

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/adapter/mail/message_test.go`:

```go
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
```

สร้าง `internal/adapter/mail/smtp_test.go`:

```go
package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

type fakeSMTPOptions struct {
	tlsConfig   *tls.Config // offers STARTTLS, or wraps every connection when implicitTLS
	implicitTLS bool
	offerAuth   bool
	rejectRcpt  bool
	stall       bool // accept connections and never send the greeting
}

type received struct {
	From, Auth string
	To         []string
	Data       string
	TLS        bool
}

// fakeSMTP is a minimal SMTP server: enough of RFC 5321 for net/smtp, recording what it receives.
type fakeSMTP struct {
	opts  fakeSMTPOptions
	ln    net.Listener
	done  chan struct{}
	wg    sync.WaitGroup
	mu    sync.Mutex
	conns []net.Conn
	msgs  []received
}

func startFakeSMTP(t *testing.T, opts fakeSMTPOptions) *fakeSMTP {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if opts.implicitTLS {
		ln = tls.NewListener(ln, opts.tlsConfig)
	}
	s := &fakeSMTP{opts: opts, ln: ln, done: make(chan struct{})}
	t.Cleanup(func() {
		close(s.done)
		_ = ln.Close()
		s.mu.Lock()
		for _, c := range s.conns {
			_ = c.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
	})
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns = append(s.conns, conn)
			s.mu.Unlock()
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.serve(conn)
			}()
		}
	}()
	return s
}

func (s *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	if s.opts.stall {
		<-s.done
		return
	}
	reader := bufio.NewReader(conn)
	tlsActive := s.opts.implicitTLS
	say := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	var cur received
	say("220 fake ESMTP ready")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			say("250-fake.local")
			if s.opts.tlsConfig != nil && !s.opts.implicitTLS && !tlsActive {
				say("250-STARTTLS")
			}
			if s.opts.offerAuth {
				say("250-AUTH PLAIN")
			}
			say("250 SIZE 1048576")
		case upper == "STARTTLS":
			say("220 ready to start TLS")
			tlsConn := tls.Server(conn, s.opts.tlsConfig)
			conn = tlsConn
			reader = bufio.NewReader(tlsConn)
			tlsActive = true
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			cur.Auth = line
			say("235 authenticated")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			cur = received{From: strings.TrimSpace(line[len("MAIL FROM:"):]), Auth: cur.Auth, TLS: tlsActive}
			say("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			if s.opts.rejectRcpt {
				say("550 mailbox unavailable")
				continue
			}
			cur.To = append(cur.To, strings.TrimSpace(line[len("RCPT TO:"):]))
			say("250 ok")
		case upper == "DATA":
			say("354 end data with <CR><LF>.<CR><LF>")
			var data strings.Builder
			for {
				l, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				data.WriteString(strings.TrimPrefix(l, ".")) // undo dot-stuffing
			}
			cur.Data = data.String()
			s.mu.Lock()
			s.msgs = append(s.msgs, cur)
			s.mu.Unlock()
			say("250 queued")
		case upper == "QUIT":
			say("221 bye")
			return
		default:
			say("502 command not implemented")
		}
	}
}

func (s *fakeSMTP) messages() []received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]received(nil), s.msgs...)
}

// testTLS returns a server TLS config with a self-signed certificate for 127.0.0.1 and a pool
// that trusts it.
func testTLS(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake smtp"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}, pool
}

func newTestMailer(t *testing.T, srv *fakeSMTP, mutate func(*SMTPConfig)) *SMTPMailer {
	t.Helper()
	host, portText, err := net.SplitHostPort(srv.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	cfg := SMTPConfig{Host: host, Port: port, From: "no-reply@localhost", TLS: TLSNone, Timeout: 5 * time.Second}
	if mutate != nil {
		mutate(&cfg)
	}
	m, err := NewSMTPMailer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC) }
	return m
}

func testOTPMail(t *testing.T) domain.OTPMail {
	t.Helper()
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
	return domain.OTPMail{To: to, OTP: otp, ChallengeID: id, ExpiresAt: time.Date(2026, 10, 22, 9, 10, 0, 0, time.UTC)}
}

func TestSMTPMailerSendsTheVerificationEmail(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{})
	mailer := newTestMailer(t, srv, nil)
	mail := testOTPMail(t)

	if err := mailer.SendEmailVerification(t.Context(), mail); err != nil {
		t.Fatalf("SendEmailVerification() error = %v", err)
	}
	got := srv.messages()
	if len(got) != 1 {
		t.Fatalf("server received %d messages, want 1", len(got))
	}
	m := got[0]
	if m.From != "<no-reply@localhost>" || len(m.To) != 1 || m.To[0] != "<user@example.com>" {
		t.Errorf("envelope = from %q to %q", m.From, m.To)
	}
	for _, want := range []string{
		"Subject: Your verification code\r\n", "Date: Thu, 22 Oct 2026 09:00:00 +0000\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n", "Content-Transfer-Encoding: 8bit\r\n",
		"042731", mail.ChallengeID.String(),
	} {
		if !strings.Contains(m.Data, want) {
			t.Errorf("message lacks %q:\n%s", want, m.Data)
		}
	}
}

func TestSMTPMailerSendsTheRegistrationAttemptWithoutACode(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{})
	mailer := newTestMailer(t, srv, nil)
	to, err := domain.ParseEmail("user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := mailer.SendRegistrationAttempt(t.Context(), to); err != nil {
		t.Fatalf("SendRegistrationAttempt() error = %v", err)
	}
	got := srv.messages()
	if len(got) != 1 || !strings.Contains(got[0].Data, "already exists") || strings.Contains(got[0].Data, "verification code") {
		t.Errorf("messages = %+v", got)
	}
}

func TestSMTPMailerAuthenticatesWithPlain(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{offerAuth: true})
	mailer := newTestMailer(t, srv, func(c *SMTPConfig) { c.Username, c.Password = "mailer", "mail-pass" })

	if err := mailer.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatal(err)
	}
	want := "AUTH PLAIN " + base64.StdEncoding.EncodeToString([]byte("\x00mailer\x00mail-pass"))
	if got := srv.messages(); len(got) != 1 || got[0].Auth != want {
		t.Errorf("auth line = %+v, want %q", got, want)
	}
}

func TestSMTPMailerStartTLS(t *testing.T) {
	serverTLS, pool := testTLS(t)
	srv := startFakeSMTP(t, fakeSMTPOptions{tlsConfig: serverTLS})
	mailer := newTestMailer(t, srv, func(c *SMTPConfig) { c.TLS = TLSStartTLS })
	mailer.tlsConfig.RootCAs = pool

	if err := mailer.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatalf("SendEmailVerification() error = %v", err)
	}
	if got := srv.messages(); len(got) != 1 || !got[0].TLS {
		t.Errorf("messages = %+v, want one sent over TLS", got)
	}
}

func TestSMTPMailerImplicitTLS(t *testing.T) {
	serverTLS, pool := testTLS(t)
	srv := startFakeSMTP(t, fakeSMTPOptions{tlsConfig: serverTLS, implicitTLS: true})
	mailer := newTestMailer(t, srv, func(c *SMTPConfig) { c.TLS = TLSImplicit })
	mailer.tlsConfig.RootCAs = pool

	if err := mailer.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatalf("SendEmailVerification() error = %v", err)
	}
	if got := srv.messages(); len(got) != 1 || !got[0].TLS {
		t.Errorf("messages = %+v, want one sent over TLS", got)
	}
}

func TestSMTPMailerRefusesAnUntrustedCertificate(t *testing.T) {
	serverTLS, _ := testTLS(t) // the mailer does not trust this certificate
	srv := startFakeSMTP(t, fakeSMTPOptions{tlsConfig: serverTLS, implicitTLS: true})
	mailer := newTestMailer(t, srv, func(c *SMTPConfig) { c.TLS = TLSImplicit })

	if err := mailer.SendEmailVerification(t.Context(), testOTPMail(t)); err == nil {
		t.Fatal("SendEmailVerification() accepted a certificate nobody vouches for")
	}
	if got := srv.messages(); len(got) != 0 {
		t.Errorf("a message was delivered over an unverified connection: %+v", got)
	}
}

func TestSMTPMailerReportsServerRejection(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{rejectRcpt: true})
	mailer := newTestMailer(t, srv, nil)

	err := mailer.SendEmailVerification(t.Context(), testOTPMail(t))
	var tpErr *textproto.Error
	if !errors.As(err, &tpErr) || tpErr.Code != 550 {
		t.Errorf("error = %v, want an SMTP 550 error", err)
	}
}

func TestSMTPMailerTimesOut(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{stall: true})
	mailer := newTestMailer(t, srv, func(c *SMTPConfig) { c.Timeout = 100 * time.Millisecond })

	start := time.Now()
	err := mailer.SendEmailVerification(t.Context(), testOTPMail(t))
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Errorf("error = %v, want a timeout", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v, the 100ms timeout was not applied", time.Since(start))
	}
}

func TestSMTPMailerStopsWhenTheContextIsCancelled(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{stall: true})
	mailer := newTestMailer(t, srv, nil) // 5s timeout: only the context can end this quickly

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := mailer.SendEmailVerification(ctx, testOTPMail(t)); err == nil {
		t.Fatal("SendEmailVerification() succeeded against a server that never answers")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v after the context ended", time.Since(start))
	}
}

func TestSMTPMailerFailsWhenNothingListens(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{})
	mailer := newTestMailer(t, srv, nil)
	_ = srv.ln.Close()

	if err := mailer.SendEmailVerification(t.Context(), testOTPMail(t)); err == nil {
		t.Error("SendEmailVerification() succeeded without a server")
	}
}

func TestNewSMTPMailerValidatesConfig(t *testing.T) {
	good := SMTPConfig{Host: "mailpit", Port: 1025, From: "no-reply@localhost", TLS: TLSNone, Timeout: time.Second}
	if _, err := NewSMTPMailer(good); err != nil {
		t.Fatalf("NewSMTPMailer(good) error = %v", err)
	}
	for name, mutate := range map[string]func(*SMTPConfig){
		"no host":            func(c *SMTPConfig) { c.Host = "" },
		"port zero":          func(c *SMTPConfig) { c.Port = 0 },
		"port too large":     func(c *SMTPConfig) { c.Port = 65536 },
		"unknown tls":        func(c *SMTPConfig) { c.TLS = "ssl" },
		"no timeout":         func(c *SMTPConfig) { c.Timeout = 0 },
		"unsafe sender":      func(c *SMTPConfig) { c.From = "a@b.com\r\nBcc: x@y.com" },
		"username only":      func(c *SMTPConfig) { c.Username = "mailer" },
		"password only":      func(c *SMTPConfig) { c.Password = "pw" },
	} {
		cfg := good
		mutate(&cfg)
		if _, err := NewSMTPMailer(cfg); err == nil {
			t.Errorf("%s: NewSMTPMailer() accepted the config", name)
		}
	}
}

func TestSMTPMailerNeverPrintsItsPassword(t *testing.T) {
	m, err := NewSMTPMailer(SMTPConfig{
		Host: "smtp.example.com", Port: 587, Username: "mailer", Password: "S3cretMailPass",
		From: "no-reply@example.com", TLS: TLSStartTLS, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{
		fmt.Sprint(m), fmt.Sprintf("%v", m), fmt.Sprintf("%+v", m), fmt.Sprintf("%#v", m), fmt.Sprintf("%+v", *m),
		fmt.Sprintf("%+v", m.cfg), fmt.Sprintf("%#v", m.cfg),
	} {
		if strings.Contains(out, "S3cretMailPass") {
			t.Errorf("output %q leaks the SMTP password", out)
		}
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/adapter/mail/ -count=1`
Expected: build fail — `undefined: checkAddress`, `SMTPConfig`

- [ ] **Step 3: เขียนโค้ด**

สร้าง `internal/core/port/mailer.go`:

```go
package port

import (
	"context"

	"go-backend-stock/internal/core/domain"
)

// Mailer sends the account emails. Implementations must not log the OTP or the full address.
// Sending happens outside any database transaction, and a failure never rolls back the change
// that caused the email (phase 1 spec section 10): callers log it and carry on.
type Mailer interface {
	// SendEmailVerification sends the verification code in mail to mail.To.
	SendEmailVerification(ctx context.Context, mail domain.OTPMail) error
	// SendRegistrationAttempt tells the owner of to that someone tried to register with an
	// address that already has an account. It carries no code.
	SendRegistrationAttempt(ctx context.Context, to domain.Email) error
}
```

สร้าง `internal/adapter/mail/message.go`:

```go
// Package mail implements port.Mailer: SMTPMailer sends over SMTP and Dispatcher hands emails to
// a background worker so a response never waits for the mail server.
package mail

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
	"unicode"

	"go-backend-stock/internal/core/domain"
)

const (
	verificationSubject        = "Your verification code"
	registrationAttemptSubject = "Someone tried to register with your email address"
	otpExpiryLayout            = "2006-01-02 15:04"
)

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

// checkHeaderValue accepts only printable ASCII without line breaks. Subjects are fixed ASCII
// strings; anything else would need RFC 2047 encoding, which this package does not do yet.
func checkHeaderValue(v string) error {
	if strings.ContainsFunc(v, func(r rune) bool { return r > unicode.MaxASCII || unicode.IsControl(r) }) {
		return unsafeMail("header value must be printable ASCII on one line")
	}
	return nil
}

// message is one email, built as plain text UTF-8 by hand so no header comes from a template.
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
	for _, v := range []string{m.subject, m.id} {
		if err := checkHeaderValue(v); err != nil {
			return nil, err
		}
	}
	var b strings.Builder
	header := func(name, value string) { b.WriteString(name + ": " + value + "\r\n") }
	header("From", "<"+m.from+">")
	header("To", "<"+m.to+">")
	header("Subject", m.subject)
	header("Date", m.date.UTC().Format(time.RFC1123Z))
	header("Message-ID", "<"+m.id+">")
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "8bit")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(m.body, "\r\n", "\n"), "\n", "\r\n"))
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
	return "Your verification code is " + m.OTP.Reveal() + ".\n\n" +
		"It expires at " + m.ExpiresAt.UTC().Format(otpExpiryLayout) + " UTC. Do not share it with anyone.\n\n" +
		"Reference: " + m.ChallengeID.String() + "\n\n" +
		"If you did not create an account, you can ignore this email.\n"
}

func registrationAttemptBody() string {
	return "Someone tried to create an account with this email address, but an account already exists.\n\n" +
		"If it was you, sign in instead, or use the password reset option if you forgot your password.\n" +
		"If it was not you, you can ignore this email.\n"
}
```

สร้าง `internal/adapter/mail/smtp.go`:

```go
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// TLSMode says how the connection to the mail server is protected.
type TLSMode string

const (
	TLSNone     TLSMode = "none"     // plain text; for a local Mailpit only
	TLSStartTLS TLSMode = "starttls" // upgrade after EHLO
	TLSImplicit TLSMode = "tls"      // TLS from the first byte
)

// SMTPConfig configures NewSMTPMailer.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string // empty = no authentication
	Password string
	From     string // plain address, no display name
	TLS      TLSMode
	Timeout  time.Duration // the whole send: connect, handshake, and delivery
}

// SMTPMailer sends email synchronously over SMTP with net/smtp (frozen, but enough for
// STARTTLS, implicit TLS and AUTH PLAIN). Certificates are always verified.
type SMTPMailer struct {
	cfg       SMTPConfig
	tlsConfig *tls.Config
	now       func() time.Time
}

var _ port.Mailer = (*SMTPMailer)(nil)

// NewSMTPMailer validates cfg.
func NewSMTPMailer(cfg SMTPConfig) (*SMTPMailer, error) {
	switch {
	case cfg.Host == "":
		return nil, errors.New("smtp host is required")
	case cfg.Port < 1 || cfg.Port > 65535:
		return nil, errors.New("smtp port must be between 1 and 65535")
	case cfg.TLS != TLSNone && cfg.TLS != TLSStartTLS && cfg.TLS != TLSImplicit:
		return nil, errors.New("smtp tls mode must be none, starttls or tls")
	case cfg.Timeout <= 0:
		return nil, errors.New("smtp timeout must be positive")
	case (cfg.Username == "") != (cfg.Password == ""):
		return nil, errors.New("smtp username and password must be set together")
	}
	if err := checkAddress(cfg.From); err != nil {
		return nil, fmt.Errorf("smtp sender: %w", err)
	}
	return &SMTPMailer{
		cfg:       cfg,
		tlsConfig: &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12},
		now:       time.Now,
	}, nil
}

// String names the server only, so `%v` and `%+v` of a mailer cannot print the password.
func (c SMTPConfig) String() string {
	return "SMTPConfig{" + net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) + "}"
}

// GoString makes %#v print the same safe form.
func (c SMTPConfig) GoString() string { return c.String() }

// String names the server only (value receiver, so a copy of the mailer is safe to print too).
func (m SMTPMailer) String() string { return "SMTPMailer{" + m.cfg.String() + "}" }

// GoString makes %#v print the same safe form.
func (m SMTPMailer) GoString() string { return m.String() }

// SendEmailVerification implements port.Mailer.
func (m *SMTPMailer) SendEmailVerification(ctx context.Context, mail domain.OTPMail) error {
	return m.send(ctx, mail.To.String(), verificationSubject, verificationBody(mail))
}

// SendRegistrationAttempt implements port.Mailer.
func (m *SMTPMailer) SendRegistrationAttempt(ctx context.Context, to domain.Email) error {
	return m.send(ctx, to.String(), registrationAttemptSubject, registrationAttemptBody())
}

func (m *SMTPMailer) send(ctx context.Context, to, subject, body string) error {
	id, err := newMessageID(m.cfg.From)
	if err != nil {
		return fmt.Errorf("create message id: %w", err)
	}
	raw, err := message{from: m.cfg.From, to: to, subject: subject, body: body, date: m.now(), id: id}.bytes()
	if err != nil {
		return err
	}
	return m.deliver(ctx, to, raw)
}

// deliver runs one SMTP conversation. The deadline covers all of it; cancelling ctx closes the
// connection so a stalled server cannot hold a shutdown.
func (m *SMTPMailer) deliver(ctx context.Context, to string, raw []byte) error {
	deadline := time.Now().Add(m.cfg.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	dialCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(dialCtx, "tcp", net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port)))
	if err != nil {
		return err
	}
	// The caller's context (not dialCtx) closes the connection, so a send that runs out of time
	// reports the connection's own timeout error while a cancelled one is cut off at once.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if m.cfg.TLS == TLSImplicit {
		tlsConn := tls.Client(conn, m.tlsConfig)
		if err := tlsConn.HandshakeContext(dialCtx); err != nil {
			_ = conn.Close()
			return err
		}
		conn = tlsConn
	}
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer client.Close()

	if m.cfg.TLS == TLSStartTLS {
		if err := client.StartTLS(m.tlsConfig); err != nil {
			return err
		}
	}
	if m.cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); err != nil {
			return err
		}
	}
	if err := client.Mail(m.cfg.From); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/adapter/mail internal/core/port; go vet ./internal/adapter/mail/ ./internal/core/... && go test ./internal/adapter/mail/ ./internal/core/... -count=1 -race`
Expected: ผ่านทั้งหมด; `TestSMTPMailerTimesOut` และ `...StopsWhenTheContextIsCancelled` จบภายในไม่กี่ร้อยมิลลิวินาที

- [ ] **Step 5: Commit**

```bash
git add internal/core/port/mailer.go internal/adapter/mail
git commit -m "feat(mail): add Mailer port and SMTP mailer"
```

---

### Task 13: `Dispatcher` (ส่งอีเมลเบื้องหลัง)

**Files:**
- Create: `internal/adapter/mail/dispatcher.go`, `internal/adapter/mail/dispatcher_test.go`

**Interfaces:**
- Consumes: `port.Mailer` (ตัวส่งจริง เช่น `*SMTPMailer`), `domain.OTPMail`, `domain.Email`
- Produces:
  - `var ErrQueueFull`, `var ErrDispatcherClosed`
  - `type DispatcherConfig struct{ Sender port.Mailer; Logger *slog.Logger; QueueSize int; Workers int; SendTimeout time.Duration }` (`QueueSize` 0 = 100, `Workers` 0 = 2)
  - `func NewDispatcher(cfg DispatcherConfig) (*Dispatcher, error)` — เริ่ม worker ทันที
  - `*Dispatcher` implements `port.Mailer`: คืนทันทีหลังใส่คิว (`context` ของ request ถูกละเว้นโดยตั้งใจเพราะอีเมลถูกส่งหลัง request จบ); คิวเต็ม → `ErrQueueFull`, ปิดแล้ว → `ErrDispatcherClosed` พร้อม log
  - `(*Dispatcher).Close(ctx context.Context) error` — หยุดรับงาน รอส่งที่ค้างจน `ctx` หมดเวลา แล้วยกเลิกงานที่ยังส่งอยู่; เรียกซ้ำได้
  - log ของ dispatcher มีเฉพาะ `event` (`mail.queue_full`, `mail.closed`, `mail.send_failed`), `kind` (`email_verification`, `registration_attempt`) และ `error_class` (`smtp_<code>`, `timeout`, `canceled`, `network`, `tls`, `other`) — ไม่มี OTP ที่อยู่อีเมล หรือข้อความ error ของเซิร์ฟเวอร์

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/adapter/mail/dispatcher_test.go`:

```go
package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// fakeSender records what it is asked to send. If block is set, a send waits for it (or for its
// context to end); started receives a value each time a send begins.
type fakeSender struct {
	mu        sync.Mutex
	sent      []domain.OTPMail
	attempts  []domain.Email
	started   chan struct{}
	block     chan struct{}
	err       error
	cancelled atomic.Bool
}

func (f *fakeSender) wait(ctx context.Context) error {
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.block == nil {
		return nil
	}
	select {
	case <-f.block:
		return nil
	case <-ctx.Done():
		f.cancelled.Store(true)
		return ctx.Err()
	}
}

func (f *fakeSender) SendEmailVerification(ctx context.Context, m domain.OTPMail) error {
	if err := f.wait(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return f.err
}

func (f *fakeSender) SendRegistrationAttempt(ctx context.Context, to domain.Email) error {
	if err := f.wait(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts = append(f.attempts, to)
	return f.err
}

func (f *fakeSender) counts() (verifications, attempts int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent), len(f.attempts)
}

func newTestDispatcher(t *testing.T, sender *fakeSender, mutate func(*DispatcherConfig)) (*Dispatcher, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	cfg := DispatcherConfig{
		Sender: sender, Logger: slog.New(slog.NewJSONHandler(logs, nil)),
		QueueSize: 10, Workers: 1, SendTimeout: 5 * time.Second,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	d, err := NewDispatcher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = d.Close(ctx)
	})
	return d, logs
}

func TestDispatcherReturnsAtOnceAndDeliversInTheBackground(t *testing.T) {
	sender := &fakeSender{block: make(chan struct{}), started: make(chan struct{}, 4)}
	d, _ := newTestDispatcher(t, sender, nil)

	start := time.Now()
	if err := d.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Errorf("SendEmailVerification() took %v while the server was blocked", time.Since(start))
	}
	<-sender.started
	if v, _ := sender.counts(); v != 0 {
		t.Fatal("the send finished although the sender was blocked")
	}

	close(sender.block)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if v, _ := sender.counts(); v != 1 {
		t.Errorf("delivered %d verification emails, want 1", v)
	}
}

func TestDispatcherDeliversEverythingQueuedBeforeClose(t *testing.T) {
	sender := &fakeSender{}
	d, _ := newTestDispatcher(t, sender, func(c *DispatcherConfig) { c.Workers = 2; c.QueueSize = 50 })
	to, err := domain.ParseEmail("user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if err := d.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
			t.Fatal(err)
		}
		if err := d.SendRegistrationAttempt(t.Context(), to); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if v, a := sender.counts(); v != 20 || a != 20 {
		t.Errorf("delivered %d verification and %d attempt emails, want 20 and 20", v, a)
	}
}

func TestDispatcherQueueFull(t *testing.T) {
	sender := &fakeSender{block: make(chan struct{}), started: make(chan struct{}, 4)}
	d, logs := newTestDispatcher(t, sender, func(c *DispatcherConfig) { c.QueueSize = 1 })
	mail := testOTPMail(t)

	if err := d.SendEmailVerification(t.Context(), mail); err != nil {
		t.Fatal(err)
	}
	<-sender.started // the worker took the first one, so the queue is empty again
	if err := d.SendEmailVerification(t.Context(), mail); err != nil {
		t.Fatalf("second send error = %v, want it queued", err)
	}
	err := d.SendEmailVerification(t.Context(), mail)
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("third send error = %v, want ErrQueueFull", err)
	}
	if !strings.Contains(logs.String(), `"event":"mail.queue_full"`) || !strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("log %q should hold an error mail.queue_full record", logs.String())
	}
	close(sender.block)
}

func TestClassifyError(t *testing.T) {
	for name, tt := range map[string]struct {
		err  error
		want string
	}{
		"smtp rejection":    {&textproto.Error{Code: 550, Msg: "user@example.com does not exist"}, "smtp_550"},
		"wrapped rejection": {errors.Join(errors.New("x"), &textproto.Error{Code: 421, Msg: "later"}), "smtp_421"},
		"deadline":          {context.DeadlineExceeded, "timeout"},
		"cancelled":         {context.Canceled, "canceled"},
		"network timeout":   {&net.OpError{Op: "read", Err: timeoutError{}}, "timeout"},
		"network":           {&net.OpError{Op: "dial", Err: errors.New("connection refused")}, "network"},
		"certificate":       {&tls.CertificateVerificationError{Err: errors.New("unknown authority")}, "tls"},
		"anything else":     {errors.New("550 user@example.com mailbox full"), "other"},
	} {
		if got := classifyError(tt.err); got != tt.want {
			t.Errorf("%s: classifyError() = %q, want %q", name, got, tt.want)
		}
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestDispatcherLogsNeverContainAddressOrOTP(t *testing.T) {
	sender := &fakeSender{err: &textproto.Error{Code: 550, Msg: "user@example.com 042731 mailbox unavailable"}}
	d, logs := newTestDispatcher(t, sender, nil)
	if err := d.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatal(err)
	}

	out := logs.String()
	for _, want := range []string{`"event":"mail.send_failed"`, `"kind":"email_verification"`, `"error_class":"smtp_550"`, `"level":"ERROR"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q lacks %s", out, want)
		}
	}
	for _, secret := range []string{"user@example.com", "example.com", "042731", "mailbox unavailable"} {
		if strings.Contains(out, secret) {
			t.Errorf("log %q leaks %q", out, secret)
		}
	}
}

func TestDispatcherAppliesTheSendTimeout(t *testing.T) {
	sender := &fakeSender{block: make(chan struct{})}
	d, logs := newTestDispatcher(t, sender, func(c *DispatcherConfig) { c.SendTimeout = 50 * time.Millisecond })
	if err := d.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(logs.String(), `"error_class":"timeout"`) {
		if time.Now().After(deadline) {
			t.Fatalf("no timeout was logged, log = %q", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDispatcherRefusesAfterClose(t *testing.T) {
	sender := &fakeSender{}
	d, logs := newTestDispatcher(t, sender, nil)
	if err := d.Close(t.Context()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := d.Close(t.Context()); err != nil {
		t.Errorf("second Close() error = %v, want it to be harmless", err)
	}
	if err := d.SendEmailVerification(t.Context(), testOTPMail(t)); !errors.Is(err, ErrDispatcherClosed) {
		t.Errorf("send after Close error = %v, want ErrDispatcherClosed", err)
	}
	if !strings.Contains(logs.String(), `"event":"mail.closed"`) {
		t.Errorf("log %q should record the dropped email", logs.String())
	}
}

func TestDispatcherCloseGivesUpAtItsDeadlineAndCancelsInFlightSends(t *testing.T) {
	sender := &fakeSender{block: make(chan struct{}), started: make(chan struct{}, 1)}
	d, _ := newTestDispatcher(t, sender, nil)
	if err := d.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatal(err)
	}
	<-sender.started

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := d.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close() error = %v, want context.DeadlineExceeded", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !sender.cancelled.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the in-flight send was not cancelled when Close gave up")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDispatcherSendsAndCloseMayRace(t *testing.T) {
	sender := &fakeSender{}
	d, _ := newTestDispatcher(t, sender, func(c *DispatcherConfig) { c.QueueSize = 5; c.Workers = 2 })
	mail := testOTPMail(t)

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := d.SendEmailVerification(context.Background(), mail)
			if err != nil && !errors.Is(err, ErrQueueFull) && !errors.Is(err, ErrDispatcherClosed) {
				t.Errorf("send error = %v", err)
			}
		}()
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	wg.Wait()
}

func TestNewDispatcherValidatesConfig(t *testing.T) {
	good := DispatcherConfig{Sender: &fakeSender{}, Logger: slog.New(slog.NewJSONHandler(&syncBuffer{}, nil)), SendTimeout: time.Second}
	d, err := NewDispatcher(good)
	if err != nil {
		t.Fatalf("NewDispatcher(good) error = %v", err)
	}
	_ = d.Close(context.Background())

	for name, mutate := range map[string]func(*DispatcherConfig){
		"no sender":        func(c *DispatcherConfig) { c.Sender = nil },
		"no logger":        func(c *DispatcherConfig) { c.Logger = nil },
		"no timeout":       func(c *DispatcherConfig) { c.SendTimeout = 0 },
		"negative queue":   func(c *DispatcherConfig) { c.QueueSize = -1 },
		"negative workers": func(c *DispatcherConfig) { c.Workers = -1 },
	} {
		cfg := good
		mutate(&cfg)
		if _, err := NewDispatcher(cfg); err == nil {
			t.Errorf("%s: NewDispatcher() accepted the config", name)
		}
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/adapter/mail/ -run 'Dispatcher|ClassifyError' -count=1`
Expected: build fail — `undefined: Dispatcher`, `DispatcherConfig`, `classifyError`

- [ ] **Step 3: เขียนโค้ด**

สร้าง `internal/adapter/mail/dispatcher.go`:

```go
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/textproto"
	"strconv"
	"sync"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// ErrQueueFull is returned when the queue has no room: the email was dropped and logged.
var ErrQueueFull = errors.New("mail queue is full")

// ErrDispatcherClosed is returned after Close: the email was dropped and logged.
var ErrDispatcherClosed = errors.New("mail dispatcher is closed")

const (
	defaultQueueSize = 100
	defaultWorkers   = 2
)

// DispatcherConfig configures NewDispatcher.
type DispatcherConfig struct {
	Sender      port.Mailer // the mailer that really sends, normally an *SMTPMailer
	Logger      *slog.Logger
	QueueSize   int           // emails that may wait; 0 means 100
	Workers     int           // concurrent sends; 0 means 2
	SendTimeout time.Duration // limit for one send (MAIL_SEND_TIMEOUT)
}

// Dispatcher implements port.Mailer by queueing each email for background workers, so a
// response never waits for the mail server and its timing does not depend on it. The trade-off
// (ADR-0014): an email still queued when the process dies is lost; the user asks for a new code
// after the resend cooldown. It never logs a code, an address or the server's error text.
type Dispatcher struct {
	sender  port.Mailer
	log     *slog.Logger
	timeout time.Duration

	jobs    chan job
	// baseCtx is the parent of every send; Close cancels it to abort sends still running at the
	// shutdown deadline.
	baseCtx context.Context
	cancel  context.CancelFunc

	mu     sync.Mutex // guards closed and sends on jobs against close(jobs)
	closed bool
	wg     sync.WaitGroup
}

var _ port.Mailer = (*Dispatcher)(nil)

type job struct {
	kind string
	run  func(ctx context.Context) error
}

// NewDispatcher validates cfg and starts the workers.
func NewDispatcher(cfg DispatcherConfig) (*Dispatcher, error) {
	switch {
	case cfg.Sender == nil:
		return nil, errors.New("dispatcher needs a sender")
	case cfg.Logger == nil:
		return nil, errors.New("dispatcher needs a logger")
	case cfg.SendTimeout <= 0:
		return nil, errors.New("dispatcher send timeout must be positive")
	case cfg.QueueSize < 0 || cfg.Workers < 0:
		return nil, errors.New("dispatcher queue size and workers must not be negative")
	}
	if cfg.QueueSize == 0 {
		cfg.QueueSize = defaultQueueSize
	}
	if cfg.Workers == 0 {
		cfg.Workers = defaultWorkers
	}
	d := &Dispatcher{sender: cfg.Sender, log: cfg.Logger, timeout: cfg.SendTimeout, jobs: make(chan job, cfg.QueueSize)}
	d.baseCtx, d.cancel = context.WithCancel(context.Background())
	for range cfg.Workers {
		d.wg.Add(1)
		go d.worker()
	}
	return d, nil
}

// SendEmailVerification queues the email. The request context is ignored on purpose: the email
// is sent after the request that caused it has finished.
func (d *Dispatcher) SendEmailVerification(_ context.Context, mail domain.OTPMail) error {
	return d.enqueue("email_verification", func(ctx context.Context) error {
		return d.sender.SendEmailVerification(ctx, mail)
	})
}

// SendRegistrationAttempt queues the email.
func (d *Dispatcher) SendRegistrationAttempt(_ context.Context, to domain.Email) error {
	return d.enqueue("registration_attempt", func(ctx context.Context) error {
		return d.sender.SendRegistrationAttempt(ctx, to)
	})
}

func (d *Dispatcher) enqueue(kind string, run func(ctx context.Context) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		d.log.Warn("mail dispatcher is closed, email dropped",
			slog.String("event", "mail.closed"), slog.String("kind", kind))
		return ErrDispatcherClosed
	}
	select {
	case d.jobs <- job{kind: kind, run: run}:
		return nil
	default:
		d.log.Error("mail queue is full, email dropped",
			slog.String("event", "mail.queue_full"), slog.String("kind", kind))
		return ErrQueueFull
	}
}

func (d *Dispatcher) worker() {
	defer d.wg.Done()
	for j := range d.jobs {
		ctx, cancel := context.WithTimeout(d.baseCtx, d.timeout)
		err := j.run(ctx)
		cancel()
		if err != nil {
			d.log.Error("sending email failed",
				slog.String("event", "mail.send_failed"),
				slog.String("kind", j.kind),
				slog.String("error_class", classifyError(err)))
		}
	}
}

// Close stops accepting emails and waits for the queued ones until ctx ends; then it cancels
// sends still running and returns ctx.Err(). Calling it again is harmless.
func (d *Dispatcher) Close(ctx context.Context) error {
	d.mu.Lock()
	if !d.closed {
		d.closed = true
		close(d.jobs)
	}
	d.mu.Unlock()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		d.cancel()
		return nil
	case <-ctx.Done():
		d.cancel()
		return ctx.Err()
	}
}

// classifyError names the kind of failure without any of its text: a server's error message can
// repeat the recipient's address.
func classifyError(err error) string {
	var smtpErr *textproto.Error
	var certErr *tls.CertificateVerificationError
	var netErr net.Error
	switch {
	case errors.As(err, &smtpErr):
		return "smtp_" + strconv.Itoa(smtpErr.Code)
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.As(err, &certErr):
		return "tls"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case errors.As(err, &netErr):
		return "network"
	}
	return "other"
}
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/adapter/mail; go vet ./internal/adapter/mail/ && go test ./internal/adapter/mail/ -count=1 -race`
Expected: ผ่านทั้งหมด ไม่มี data race

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/mail/dispatcher.go internal/adapter/mail/dispatcher_test.go
git commit -m "feat(mail): add background dispatcher with bounded queue"
```

---
### Task 14: Blocklist จริง (ต้องได้รับอนุญาตดาวน์โหลดก่อน)

spec หัวข้อ 4.2 กำหนดให้ **ขออนุญาตผู้ใช้ก่อนดาวน์โหลด** และตรวจ license ของไฟล์ต้นทางก่อนใช้ Task นี้จึงเริ่มด้วยการหยุดถาม ห้ามข้ามขั้นตอนนี้แม้แผนอื่นจะรอ Task นี้อยู่

**Files:**
- Create: `internal/core/service/blocklist.txt`, `internal/core/service/NOTICE`, `internal/core/service/blocklist_embed.go`, `internal/core/service/blocklist_data_test.go`

**Interfaces:**
- Consumes: `NewBlocklist`, `FilterBlocklistEntry`, `NewPasswordPolicy`, `mustEmail`, `reasonsOf` (Task 4)
- Produces: `func DefaultBlocklist() (*Blocklist, error)` — โหลดรายการที่ bundle ใน binary ด้วย `go:embed` (4b เรียกตอนเริ่มระบบแล้วส่งให้ `NewPasswordPolicy`)

- [ ] **Step 1: ขออนุญาตผู้ใช้ (หยุดตรงนี้จนกว่าจะได้คำตอบ)**

ใช้ `AskUserQuestion` ถามผู้ใช้ด้วยข้อความตามนี้ (ปรับถ้อยคำได้ แต่ต้องมีทุกประเด็น):

> ต้องดาวน์โหลดรายการรหัสผ่านยอดนิยมประมาณ 100,000 รายการเพื่อสร้าง blocklist ตาม spec 4.2
> - ต้นทาง: repository `danielmiessler/SecLists` บน GitHub (MIT ตาม spec) ไฟล์ในโฟลเดอร์ `Passwords/Common-Credentials/` ที่เป็นรายการ 100k (ผมจะ pin ด้วย commit SHA)
> - ดาวน์โหลดผ่าน HTTPS ไปไว้นอก repository (โฟลเดอร์ scratchpad) ขนาดประมาณ 1 MB และใช้เป็นข้อมูลเท่านั้น ไม่รันอะไรจากไฟล์นั้น
> - ผมจะอ่านข้อความ license ของ repository และหมายเหตุที่มาของไฟล์นั้นก่อนใช้ แล้วรายงานให้คุณก่อน commit
> อนุญาตให้ดาวน์โหลดไหม?

ตัวเลือก: (1) อนุญาต (2) ไม่อนุญาต — ผู้ใช้จะให้ไฟล์ต้นทางเอง (3) เลือกรายการอื่น (ผู้ใช้ระบุ)

ถ้าผู้ใช้ไม่อนุญาตหรือให้ไฟล์เอง: ใช้ไฟล์ที่ผู้ใช้ให้เป็น `-in` ใน Step 4 แล้วข้าม Step 2–3 (ยังต้องบันทึกที่มาและ license ใน `NOTICE` ตามที่ผู้ใช้แจ้ง) ห้ามหาแหล่งอื่นเองและห้ามเขียนรายการขึ้นเอง

- [ ] **Step 2: ตรวจที่มา เวอร์ชัน และ license (หลังได้รับอนุญาต)**

Run:
```bash
SHA=$(gh api repos/danielmiessler/SecLists/commits/master --jq .sha)
echo "$SHA"
gh api "repos/danielmiessler/SecLists/contents/Passwords/Common-Credentials?ref=$SHA" --jq '.[].name' | grep -i '100k'
gh api "repos/danielmiessler/SecLists/contents/LICENSE?ref=$SHA" --jq .content | base64 -d | head -30
```
Expected: ได้ SHA เต็ม 40 ตัว, ชื่อไฟล์ 100k (เช่น `100k-most-used-passwords-NCSC.txt`) และข้อความ MIT License พร้อมชื่อผู้ถือลิขสิทธิ์

ตรวจต่อว่าไฟล์ที่เลือกมีหมายเหตุที่มา/license ของตัวเองหรือไม่ (`gh api "repos/danielmiessler/SecLists/contents/Passwords/Common-Credentials/README.md?ref=$SHA" --jq .content | base64 -d | grep -i -B2 -A4 '<ชื่อไฟล์>'` ถ้ามี README) แล้วรายงานผู้ใช้เป็นข้อความว่า license ของไฟล์นั้นคืออะไร **หยุดถามผู้ใช้อีกครั้งถ้า license ของไฟล์ต่างจาก MIT หรือไม่ชัดเจน** — ห้ามตัดสินเอง

- [ ] **Step 3: ดาวน์โหลดไปไว้นอก repository**

Run (แทน `<FILE>` ด้วยชื่อไฟล์ที่เลือกใน Step 2):
```bash
SCRATCH=/private/tmp/claude-501/-Users-markedtr-Docs-MyCode-work-Go-Backend-Stock/302f47cb-762d-48f0-81b1-2d0b3cc098ab/scratchpad
SRC="$SCRATCH/blocklist-src"
mkdir -p "$SRC"
curl -fsSL -o "$SRC/source.txt" "https://raw.githubusercontent.com/danielmiessler/SecLists/$SHA/Passwords/Common-Credentials/<FILE>"
shasum -a 256 "$SRC/source.txt"
wc -l "$SRC/source.txt"
```
Expected: ไฟล์ประมาณ 100,000 บรรทัด; จด sha256 ไว้สำหรับ `NOTICE` (โฟลเดอร์ `blocklist-src` ต้องว่างอยู่ก่อนดาวน์โหลดและสคริปต์ที่เรารันอยู่นอกโฟลเดอร์นี้)

- [ ] **Step 4: สร้าง blocklist ที่กรองแล้ว**

Run:
```bash
go run scripts/build_blocklist.go -in "$SRC/source.txt" -out internal/core/service/blocklist.txt
wc -lc internal/core/service/blocklist.txt
head -3 internal/core/service/blocklist.txt
grep -c '' internal/core/service/blocklist.txt
grep -x 'p@ssw0rd' internal/core/service/blocklist.txt
```
Expected: `wrote N entries` โดย N เป็นหลักพัน, ไฟล์เล็กกว่า 200 KB (ถ้าใหญ่กว่า 1 MB หยุดและรายงาน), และ `grep -x` พิมพ์ `p@ssw0rd` — ถ้าไม่พบ ให้เลือกรายการอื่นจาก `head -50` ที่ถ้าเปลี่ยนตัวอักษรแรกเป็นพิมพ์ใหญ่แล้วผ่านกฎอื่นของรหัสผ่าน (มีพิมพ์เล็ก พิมพ์ใหญ่ และอักขระพิเศษ) แล้วใช้รายการนั้นแทน `p@ssw0rd`/`P@ssw0rd` ใน test ของ Step 5

- [ ] **Step 5: เขียน failing test**

สร้าง `internal/core/service/blocklist_data_test.go`:

```go
package service

import (
	"slices"
	"strings"
	"testing"
)

func TestDefaultBlocklist(t *testing.T) {
	list, err := DefaultBlocklist()
	if err != nil {
		t.Fatal(err)
	}
	if list.Len() < 500 {
		t.Errorf("the bundled blocklist has only %d entries, the file looks truncated", list.Len())
	}
	// Appears in every common-password list and passes the other rules once capitalised.
	if !list.Contains("p@ssw0rd") {
		t.Error("the bundled blocklist lacks p@ssw0rd")
	}
}

// The file is generated by scripts/build_blocklist.go; this pins that it was not edited by hand and
// is exactly what the filter rules produce, so the NOTICE's rebuild command reproduces it.
func TestBundledBlocklistFollowsTheFilterRules(t *testing.T) {
	if strings.Contains(embeddedBlocklist, "\r") || strings.HasSuffix(embeddedBlocklist, "\n\n") || !strings.HasSuffix(embeddedBlocklist, "\n") {
		t.Fatal("blocklist.txt must have LF line endings and end with exactly one newline")
	}
	lines := strings.Split(strings.TrimSuffix(embeddedBlocklist, "\n"), "\n")
	if !slices.IsSorted(lines) {
		t.Error("blocklist.txt is not sorted")
	}
	for i, line := range lines {
		entry, ok := FilterBlocklistEntry(line)
		if !ok || entry != line {
			t.Fatalf("line %d does not follow the filter rules (kept=%v, normalised=%q)", i+1, ok, entry)
		}
		if i > 0 && lines[i-1] == line {
			t.Fatalf("line %d repeats the line before it", i+1)
		}
	}
}

func TestPolicyRejectsABundledPasswordThatPassesTheOtherRules(t *testing.T) {
	list, err := DefaultBlocklist()
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewPasswordPolicy(list)
	if err != nil {
		t.Fatal(err)
	}
	_, err = policy.Parse("P@ssw0rd", mustEmail(t, "somebody@example.com"))
	if got := reasonsOf(t, err); !slices.Equal(got, []string{"password_too_common"}) {
		t.Errorf("reasons = %v, want [password_too_common]", got)
	}
}
```

- [ ] **Step 6: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/service/ -run 'TestDefaultBlocklist|TestBundled|TestPolicyRejectsABundled' -count=1`
Expected: build fail — `undefined: DefaultBlocklist`, `embeddedBlocklist`

- [ ] **Step 7: เขียนโค้ด และ NOTICE**

สร้าง `internal/core/service/blocklist_embed.go`:

```go
package service

import (
	_ "embed" // for the go:embed directive below
	"strings"
)

// embeddedBlocklist is internal/core/service/blocklist.txt, built by scripts/build_blocklist.go
// from the source named in NOTICE. It is compiled into the binary so the check needs no network
// and no file at runtime.
//
//go:embed blocklist.txt
var embeddedBlocklist string

// DefaultBlocklist loads the bundled list of common passwords. Call it once at startup.
func DefaultBlocklist() (*Blocklist, error) { return NewBlocklist(strings.NewReader(embeddedBlocklist)) }
```

สร้าง `internal/core/service/NOTICE` โดยแทนค่าในวงเล็บเหลี่ยม `[...]` ด้วยผลจาก Step 2–4 (เป็นข้อเท็จจริงที่ได้ตอนรัน ไม่ใช่ข้อความที่ต้องเดา):

```text
internal/core/service/blocklist.txt
===================================

Derived from a list of common passwords in SecLists
(https://github.com/danielmiessler/SecLists), file
Passwords/Common-Credentials/[FILE] at commit [SHA], fetched on 2026-10-22.
SHA-256 of the downloaded file: [SHA256].

Licence of the source: [what Step 2 found: the SecLists licence name, and the file's own
provenance or licence note if it has one].

Filtering (service.FilterBlocklistEntry): lower-cased; only entries of 6-32 printable ASCII
characters without spaces that contain at least one letter and one special character are kept;
duplicates are removed; the result is sorted. An entry that fails these rules can never equal a
password our policy accepts, so leaving it out does not change which passwords are rejected.

Rebuild:
  curl -fsSL -o /tmp/source.txt https://raw.githubusercontent.com/danielmiessler/SecLists/[SHA]/Passwords/Common-Credentials/[FILE]
  go run scripts/build_blocklist.go -in /tmp/source.txt -out internal/core/service/blocklist.txt

----------------------------------------------------------------------
SecLists licence text, copied from the repository at the commit above:

[paste the full text printed by the LICENSE command in Step 2]
```

ตรวจว่าไม่มี `[` ค้างใน NOTICE: `grep -n '\[[A-Za-z0-9 ]*\]' internal/core/service/NOTICE` (ต้องไม่พิมพ์อะไรนอกจากข้อความ license เดิมที่อาจมีวงเล็บ)

- [ ] **Step 8: รัน test ให้ผ่าน และตรวจ Docker context**

Run:
```bash
gofmt -l internal/core; go vet ./internal/core/... && go test ./internal/core/... -count=1 -race
grep -n 'txt' .dockerignore || echo "no txt rule in .dockerignore"
```
Expected: test ผ่าน; `.dockerignore` ต้องไม่ตัด `blocklist.txt` (ถ้ามีกฎ `*.txt` ให้เพิ่ม `!internal/core/service/blocklist.txt` ก่อน commit)

- [ ] **Step 9: Commit**

```bash
git add internal/core/service/blocklist.txt internal/core/service/NOTICE internal/core/service/blocklist_embed.go internal/core/service/blocklist_data_test.go
git commit -m "feat(service): bundle the filtered common-password blocklist"
```

---

### Task 15: ส่งถึง Mailpit จริงด้วย `make test-mail`

เกณฑ์ผ่านของ 4a ข้อ "Mailer ส่งถึง Mailpit ใน compose ได้จริง" ยังไม่มี endpoint ให้ลองด้วย curl จึงพิสูจน์ด้วย test ที่ส่งผ่าน `SMTPMailer` ไปยัง Mailpit แล้วอ่านกลับด้วย API ของ Mailpit; test นี้ skip เมื่อไม่ได้ตั้ง env (CI ไม่มี Mailpit)

**Files:**
- Create: `internal/adapter/mail/mailpit_integration_test.go`
- Modify: `Makefile`

**Interfaces:**
- Consumes: `NewSMTPMailer`, `SMTPConfig`, `testOTPMail` (Task 12 `smtp_test.go`)
- Produces: `make test-mail` (ตั้ง `REQUIRE_MAIL_INTEGRATION=1`, `TEST_SMTP_ADDR`, `TEST_MAILPIT_API_URL`)

- [ ] **Step 1: เปิด Mailpit และดูรูปแบบ API จริง**

Run:
```bash
docker compose up -d mailpit
curl -fsS http://127.0.0.1:8025/api/v1/messages | head -c 400
```
Expected: JSON ที่มีคีย์ `messages` (อาร์เรย์) — ถ้ารูปแบบต่างจากที่ test ด้านล่างอ่าน (`messages[].ID`, `messages[].Subject`, และ `Text` ของ `/api/v1/message/{ID}`) ให้ปรับ struct ใน test ให้ตรงกับของจริงก่อนไปต่อ

- [ ] **Step 2: เขียน test**

สร้าง `internal/adapter/mail/mailpit_integration_test.go`:

```go
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
// body, and deletes it afterwards.
func waitForMessage(t *testing.T, apiURL, recipient string) (mailpitMessage, string) {
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
	return found, detail.Text
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
	msg, text := waitForMessage(t, apiURL, mail.To.String())
	if msg.Subject != "Your verification code" {
		t.Errorf("Subject = %q", msg.Subject)
	}
	if !strings.Contains(text, "042731") || !strings.Contains(text, mail.ChallengeID.String()) {
		t.Errorf("body lacks the code or the reference:\n%s", text)
	}
}

func TestMailpitReceivesTheRegistrationAttemptEmail(t *testing.T) {
	mailer, apiURL := mailpitMailer(t)
	to := uniqueRecipient(t)

	if err := mailer.SendRegistrationAttempt(t.Context(), to); err != nil {
		t.Fatalf("SendRegistrationAttempt() error = %v", err)
	}
	msg, text := waitForMessage(t, apiURL, to.String())
	if msg.Subject != "Someone tried to register with your email address" || !strings.Contains(text, "already exists") {
		t.Errorf("Subject = %q, body:\n%s", msg.Subject, text)
	}
}
```

แก้ `Makefile` — แทน

```make
	go test -race -count=1 ./internal/adapter/postgres/...
```

ด้วย

```make
	go test -race -count=1 ./internal/adapter/postgres/...

MAILPIT_SMTP_ADDR ?= 127.0.0.1:1025
MAILPIT_API_URL ?= http://127.0.0.1:8025

.PHONY: test-mail

# Sends real emails to the Mailpit of docker compose (docker compose up -d mailpit) and reads them back.
test-mail:
	REQUIRE_MAIL_INTEGRATION=1 \
	TEST_SMTP_ADDR="$(MAILPIT_SMTP_ADDR)" \
	TEST_MAILPIT_API_URL="$(MAILPIT_API_URL)" \
	go test -race -count=1 -run 'TestMailpit' ./internal/adapter/mail/...
```

- [ ] **Step 3: ดูว่า test ล้มเมื่อไม่มี Mailpit และ skip เมื่อไม่ตั้ง env**

Run:
```bash
docker compose stop mailpit
make test-mail
docker compose up -d mailpit
go test ./internal/adapter/mail/ -run TestMailpit -v -count=1
```
Expected: `make test-mail` ล้ม (ส่งไม่ได้: connection refused) ขณะ Mailpit หยุด; `go test` โดยไม่ตั้ง env แสดง `--- SKIP` ทั้งสอง test

- [ ] **Step 4: รัน test กับ Mailpit จริง**

Run: `make test-mail`
Expected: `PASS` ทั้งสอง test และไม่มี `SKIP`; เปิด http://localhost:8025 ดูด้วยตา: ต้องไม่เหลืออีเมลของ test (cleanup ลบแล้ว) — ลองส่งแล้วดูครั้งเดียวโดยคอมเมนต์ cleanup ชั่วคราวได้ถ้าต้องการเห็นหน้าตาอีเมล แล้วคืนโค้ดเดิม

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/adapter/mail; go vet ./internal/adapter/mail/
git add internal/adapter/mail/mailpit_integration_test.go Makefile
git commit -m "test(mail): deliver to Mailpit and read the message back"
```

---

### Task 16: เอกสาร ADR และตรวจรับ

**Files:**
- Create: `docs/adr/0014-mail-dispatcher-and-timing-floor.md`, `docs/adr/0015-filtered-password-blocklist.md`
- Modify: `docs/adr/README.md`, `README.md`, `docs/superpowers/specs/2026-10-22-milestone-4-auth-design.md`, `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md`

- [ ] **Step 1: เขียน ADR-0014**

สร้าง `docs/adr/0014-mail-dispatcher-and-timing-floor.md`:

```markdown
# ADR-0014: ส่งอีเมลหลังตอบผ่าน dispatcher เบื้องหลัง และ timing floor ใน core

- **สถานะ:** Accepted
- **วันที่:** 2026-10-22
- **เกี่ยวข้อง:** [milestone 4 design](../superpowers/specs/2026-10-22-milestone-4-auth-design.md) หัวข้อ 2 และ 4.9, [ADR-0006](0006-email-verification-and-registration.md), phase 1 spec หัวข้อ 10

## บริบท
Register, ขอรหัสยืนยันใหม่ และ password reset ต้องตอบเหมือนกันทุกกรณี ไม่ว่าอีเมลนั้นมีบัญชีหรือไม่ แต่ในกรณีที่มีบัญชีเราส่งอีเมลหนึ่งฉบับ ส่วนกรณีที่ไม่มีเราไม่ส่ง (หรือส่งอีเมลชนิดอื่น) ถ้าส่งอีเมลใน request แล้วรอ เวลาตอบจะขึ้นกับ SMTP และบอกผู้โจมตีว่ามีบัญชีหรือไม่ นอกจากนี้ SMTP ที่ช้าหรือล่มไม่ควรทำให้ API ช้าหรือล่มตาม

## ทางเลือกที่พิจารณา
1. ส่งอีเมลใน request แล้วรอผล: เวลาตอบรั่วสถานะบัญชีและผูกกับความเร็วของ SMTP
2. Outbox ใน PostgreSQL ที่มี worker และ retry: ไม่เสียอีเมลเมื่อ API ตาย แต่เพิ่มตาราง worker และนโยบาย retry ซึ่งเกินขอบเขต phase 1
3. Dispatcher ในหน่วยความจำของ adapter ร่วมกับ timing floor ใน core

## การตัดสินใจ
เลือกข้อ 3
- `adapter/mail.Dispatcher` implement `port.Mailer` โดยใส่งานลงคิวขนาด 100 แล้วคืนทันที; worker 2 ตัวส่งผ่าน `SMTPMailer` ด้วย `MAIL_SEND_TIMEOUT`; shutdown หยุดรับงานและรอที่ค้างจน `SHUTDOWN_TIMEOUT` แล้วยกเลิกงานที่ยังส่งอยู่
- `service.Pacer` ใน core ทำให้ทุกคำตอบของ use case ที่ต้องไม่เปิดเผยสถานะบัญชีใช้เวลาอย่างน้อย `AUTH_RESPONSE_FLOOR` (500 ms) บวก jitter สุ่ม 0–`AUTH_RESPONSE_JITTER` (100 ms) ทั้งกรณีสำเร็จและ error โดยรับ `Clock` และฟังก์ชัน sleep เข้ามา จึงทดสอบได้โดยไม่รอจริง; อยู่ใน core เพราะเป็นคุณสมบัติด้านความปลอดภัยของ use case ไม่ใช่ของ HTTP
- log ของ dispatcher มีเฉพาะ `event`, `kind` และ `error_class` (เช่น `smtp_550`, `timeout`) ไม่มี OTP ที่อยู่อีเมล หรือข้อความ error ของเซิร์ฟเวอร์ เพราะข้อความนั้นมักซ้ำที่อยู่ผู้รับ

## ผลที่ตามมา
- เวลาตอบของ register/ขอรหัสใหม่ไม่ขึ้นกับ SMTP และไม่บอกว่ามีบัญชีหรือไม่ (ภายในความแม่นยำของ floor)
- API ตายหรือคิวเต็มระหว่างรออีเมล → อีเมลหาย; ผู้ใช้ขอรหัสใหม่ได้หลัง cooldown 60 วินาที; ผู้ใช้เห็น 202 เหมือนเดิมจึงไม่รู้ ต้องดู log `mail.queue_full` / `mail.send_failed`
- ทุก register ช้าอย่างน้อย 500 ms โดยตั้งใจ
- ถ้าต้องการความทนทานภายหลัง เพิ่ม outbox ได้โดย `port.Mailer` ไม่เปลี่ยน (เขียนแถวใน transaction เดียวกับข้อมูลแล้วให้ worker ส่ง)
```

- [ ] **Step 2: เขียน ADR-0015**

สร้าง `docs/adr/0015-filtered-password-blocklist.md`:

```markdown
# ADR-0015: Blocklist รหัสผ่านที่กรองตามกติกาของเรา

- **สถานะ:** Accepted
- **วันที่:** 2026-10-22
- **เกี่ยวข้อง:** [ADR-0005](0005-password-hashing-and-policy.md) (ส่วนเสริม ไม่ได้แทนที่), milestone 4 design หัวข้อ 4.2, `internal/core/service/NOTICE`

## บริบท
ADR-0005 กำหนดให้ปฏิเสธรหัสที่อยู่ในรายการรหัสยอดนิยมราว 100,000 รายการที่ bundle ใน binary รายการต้นทางเป็นข้อความดิบเกือบ 1 MB ซึ่งส่วนใหญ่ไม่มีทางตรงกับรหัสที่ผ่านกติกาอื่นของเราได้

## ทางเลือกที่พิจารณา
1. ฝังรายการเต็มแล้วเทียบตรง ๆ
2. กรองเหลือเฉพาะรายการที่มีผลกับกติกาของเรา แล้ว commit ผลพร้อมสคริปต์สร้างใหม่และ NOTICE

## การตัดสินใจ
เลือกข้อ 2 รหัสที่ผ่านกติกาต้องมีพิมพ์เล็ก พิมพ์ใหญ่ และอักขระพิเศษ ยาว 6–32 ตัวและเป็น printable ASCII ไม่มีช่องว่าง เมื่อแปลงเป็นพิมพ์เล็กแล้วจึงมีตัวอักษรและอักขระพิเศษเสมอ ดังนั้นรายการต้นทางที่ไม่ยาว 6–32 ตัว ไม่ใช่ printable ASCII มีช่องว่าง ไม่มีตัวอักษร หรือไม่มีอักขระพิเศษ ไม่มีทางตรงกับรหัสที่ผ่านกติกาอื่น การตัดทิ้งจึงไม่เปลี่ยนว่ารหัสใดถูกปฏิเสธ `service.FilterBlocklistEntry` เป็นกติกาเดียวที่ทั้งสคริปต์ `scripts/build_blocklist.go` และ test ของไฟล์ที่ commit ใช้ ไฟล์ `blocklist.txt` จึงสร้างซ้ำได้จากต้นทางที่ pin ไว้ใน NOTICE

## ผลที่ตามมา
- ไฟล์และ binary เล็กลงมาก โหลดเป็น set ครั้งเดียวตอนเริ่มระบบ
- ผลของรหัสที่ผ่านกติกาอื่นแล้วเท่ากับรายการเต็ม แต่รหัสที่ละเมิดกติกาอื่นอยู่แล้ว (เช่น `password` ที่ไม่มีอักขระพิเศษ) จะไม่ได้ reason `password_too_common` เพิ่ม — ผู้ใช้ได้ reason ที่ละเมิดจริงอยู่แล้ว
- ต้องแจกจ่ายข้อความ license ของต้นทางใน NOTICE; เปลี่ยนต้นทางหรือกติกาต้องสร้างไฟล์ใหม่ด้วยสคริปต์และอัปเดต NOTICE
```

- [ ] **Step 3: อัปเดต ADR README, README และสถานะ**

แก้ `docs/adr/README.md` — แทน

```markdown
| [0013](0013-domain-value-types.md) | Domain value types แบบ parse, don't validate และชนิดตัวเลขแยกตามความหมาย | Accepted |
```

ด้วย

```markdown
| [0013](0013-domain-value-types.md) | Domain value types แบบ parse, don't validate และชนิดตัวเลขแยกตามความหมาย | Accepted |
| [0014](0014-mail-dispatcher-and-timing-floor.md) | ส่งอีเมลหลังตอบผ่าน dispatcher เบื้องหลัง และ timing floor ใน core | Accepted |
| [0015](0015-filtered-password-blocklist.md) | Blocklist รหัสผ่านที่กรองตามกติกาของเรา | Accepted |
```

แก้ `README.md` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```text
> **สถานะ:** Milestone 3 — schema และ repositories ครบ แต่ API ยังมีเฉพาะ `/healthz`, `/readyz` และเอกสาร API; auth, พอร์ต และรายการซื้อขายยังไม่เปิดใช้งาน
```

   ด้วย

```text
> **สถานะ:** Milestone 4a — ชิ้นส่วนพื้นฐานของ auth (password policy, Argon2id, OTP, session/CSRF token, rate-limit rules, Mailer) พร้อมแล้ว แต่ API ยังมีเฉพาะ `/healthz`, `/readyz` และเอกสาร API; endpoint ของ auth เปิดใน milestone 4b ส่วนพอร์ตและรายการซื้อขายยังไม่เปิดใช้งาน
```

2. แทน

```text
make test-integration # integration tests กับ PostgreSQL ใน compose (ต้องเปิด stack ก่อน)
```

   ด้วย

```text
make test-integration # integration tests กับ PostgreSQL ใน compose (ต้องเปิด stack ก่อน)
make test-mail        # ส่งอีเมลจริงไป Mailpit ใน compose แล้วอ่านกลับ (docker compose up -d mailpit)
```

3. แทน

```text
## พัฒนาและทดสอบ
```

   ด้วย

```text
## ตัวแปรสภาพแวดล้อมของ auth (milestone 4a)

`.env.example` มีค่า `dev-only-…` สำหรับ `CSRF_SECRET`, `OTP_SECRET`, `RATE_LIMIT_SECRET` และ `OIDC_SECRET` (ยาวอย่างน้อย 32 bytes); production ปฏิเสธค่าที่ขึ้นต้นด้วย `dev-only-` และสี่ค่าต้องไม่ซ้ำกัน ถ้าคุณมี `.env` อยู่แล้วให้คัดลอกสี่บรรทัดนี้ไปเพิ่ม

ตัวแปรอื่นมีค่าเริ่มต้นสำหรับ dev จึงไม่ต้องตั้ง: `SESSION_TTL` (168h), `PASSWORD_HASH_CONCURRENCY` (4), `AUTH_RESPONSE_FLOOR`/`AUTH_RESPONSE_JITTER` (500ms/100ms), `OTP_MAX_ATTEMPTS` (5), `OTP_RESEND_COOLDOWN` (60s), `OTP_REQUESTS_PER_HOUR_EMAIL`/`_IP` (5/20), `LOGIN_ATTEMPTS_PER_EMAIL`/`_IP` (10/30 ต่อ 15 นาที), `TRUSTED_PROXY_CIDRS` (ว่าง = ไม่เชื่อ `X-Forwarded-For`), `SMTP_HOST`/`SMTP_PORT`/`SMTP_FROM`/`SMTP_TLS`/`MAIL_SEND_TIMEOUT` (compose ชี้ไป Mailpit ที่ `mailpit:1025` แบบไม่เข้ารหัส) production ต้องตั้ง `SMTP_HOST`, `SMTP_PORT`, `SMTP_FROM` และ `SMTP_TLS` เป็น `starttls` หรือ `tls` เอง

อีเมลที่ระบบส่ง (รหัสยืนยัน และแจ้งว่ามีบัญชีอยู่แล้ว) เป็นภาษาอังกฤษแบบ ASCII ในตอนนี้

## พัฒนาและทดสอบ
```

แก้ `docs/superpowers/specs/2026-10-22-milestone-4-auth-design.md` — แทน

```text
- **สถานะ:** design ทั้ง 4 ส่วนได้รับการยืนยันในบทสนทนา 2026-10-22; รอผู้ใช้รีวิว spec ฉบับนี้
```

ด้วย

```text
- **สถานะ:** design ทั้ง 4 ส่วนได้รับการยืนยันในบทสนทนา 2026-10-22; รอผู้ใช้รีวิว spec ฉบับนี้ — แผน 4a: [2026-10-22-milestone-4a-auth-building-blocks.md](../plans/2026-10-22-milestone-4a-auth-building-blocks.md) ลงมือครบ รอตรวจรับ
```

แก้ `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. ท้ายย่อหน้า **Status:** แทน `ขั้นต่อไปคือ milestone 4` ด้วย `milestone 4 มี design ([spec](../specs/2026-10-22-milestone-4-auth-design.md)) แบ่งเป็นแผน 4a/4b/4c; แผน 4a ลงมือครบแล้ว รอตรวจรับ ขั้นต่อไปคือแผน 4b`

2. แทน

```text
### 4. Auth + email verification + sessions + Google login + HTTP handlers
```

   ด้วย

```text
### 4. Auth + email verification + sessions + Google login + HTTP handlers

**แผนลงมือ:** [design](../specs/2026-10-22-milestone-4-auth-design.md) — [4a](2026-10-22-milestone-4a-auth-building-blocks.md) (ชิ้นส่วนพื้นฐาน; ลงมือครบ รอตรวจรับ); 4b (AuthService, EmailVerificationService, middleware, endpoints) และ 4c (Google OIDC) ยังไม่มีแผน

**แผน 4a ปิดข้อส่งต่อแล้ว:** `ParseEmail` ปฏิเสธ `"(),:;<>[\]` และอักขระซ่อน (ข้อส่งต่อจาก milestone 2); `service.NowForStorage` ตัดเวลาที่ microsecond (รีวิว 3b); `service.NewRateLimitPolicy` สร้าง rules ผ่าน `domain.NewRateLimitRule` เท่านั้น จึงไม่มี rule ที่ `Limit` เป็นศูนย์ (รีวิว 3b) — ที่เหลือของข้อส่งต่อ 3b (`FOR UPDATE` ของ `OTPChallengeRepository.Lock`, purpose อื่น) ปิดใน 4b ตามหัวข้อ 9 ของ design
```

3. แทน

```text
- [ ] ทำ OTP generation/HMAC แบบมี purpose และ SMTP adapter ไป Mailpit ตั้งแต่ขั้นนี้ เพราะ signup ต้องยืนยันอีเมลก่อน login ได้
```

   ด้วย

```text
- [x] ทำ OTP generation/HMAC แบบมี purpose และ SMTP adapter ไป Mailpit ตั้งแต่ขั้นนี้ เพราะ signup ต้องยืนยันอีเมลก่อน login ได้ (แผน 4a: `GenerateOTP`, `OTPCodec`, `SMTPMailer`, `Dispatcher`; `make test-mail` ส่งถึง Mailpit จริง)
```

4. แทน

```text
- [ ] ทำ password validator (ความยาว, ASCII, พิมพ์เล็ก/ใหญ่/อักขระพิเศษ, blocklist แบบ go:embed, ไม่มีส่วนของอีเมล) พร้อม unit tests ของทุก field reason
```

   ด้วย

```text
- [x] ทำ password validator (ความยาว, ASCII, พิมพ์เล็ก/ใหญ่/อักขระพิเศษ, blocklist แบบ go:embed, ไม่มีส่วนของอีเมล) พร้อม unit tests ของทุก field reason (แผน 4a: `domain.ParsePassword`, `service.PasswordPolicy`, ADR-0015)
```

- [ ] **Step 4: ตรวจทั้ง repo**

Run:
```bash
make check
make test-integration
make test-mail
git status --short
```
Expected: `make check` ผ่านทั้งหมด (tidy, vet, lint, swagger, sqlc, test — `docs/api` ไม่มี diff เพราะไม่มี endpoint ใหม่); `make test-integration` และ `make test-mail` ผ่านโดยไม่มี `SKIP`; `git status` สะอาด (ไม่มีไฟล์ค้างนอกจากที่ ignore)

ตรวจเพิ่มด้วยมือ: `git log --oneline developer..HEAD` แสดง commit ของแผน 4a ทุกอัน และ `git log developer..HEAD --format=%B | grep -ci 'co-authored'` ต้องได้ 0

- [ ] **Step 5: Commit เอกสาร**

```bash
git add docs README.md
git commit -m "docs: record milestone 4a auth building blocks, ADR-0014 and ADR-0015"
```

จากนั้นรายงานผู้ใช้ว่า 4a ลงมือครบ พร้อมผลของ `make check`, `make test-integration`, `make test-mail`, ผล `govulncheck` (หรือบอกว่าข้ามเพราะไม่มี network) แล้วรอการตรวจรับก่อนเริ่มแผน 4b — ห้าม push

---

## หมายเหตุท้ายแผน

**ที่แผนนี้ไม่ทำ (อยู่ในแผน 4b/4c ตาม design):**
- `error.details` ใน error envelope ของ HTTP และการแปลง `domain.Error` เป็น status code (`password` reasons ถูกเตรียมไว้ที่ `domain.Error.Reasons` แล้ว) — 4b พร้อม endpoint แรกที่คืน `invalid_input` ของรหัสผ่าน
- ต่อสาย `PasswordHasher`, `OTPCodec`, `CSRFCodec`, `RateLimitPolicy`, `Pacer`, `Dispatcher` และ `ClientIPResolver` ที่ `cmd/api/main.go` รวมถึง shutdown ที่รอ `Dispatcher.Close` — 4b (ไม่มีโค้ดใน `main.go` ที่ใช้งานจริงจนกว่าจะมี service)
- `AuthService`, `EmailVerificationService`, middleware, endpoints, annotations/Swagger และ HTTP contract tests — 4b
- Google OIDC, `go-oidc`/`oauth2` และ `IdentityProvider` port — 4c
- `SendPasswordReset`, `SendPasswordChanged` ใน `port.Mailer` — milestone 5

**การตัดสินใจของผู้ใช้ (2026-10-22, หลังอ่านแผน):**
- เนื้อหาอีเมลเป็นภาษาอังกฤษ ASCII (ไม่ต้อง encode `Subject` ภาษาไทย)
- ผู้ใช้เพิ่มสี่บรรทัด secret ลง `.env` ในเครื่องเอง; ผู้ลงมือแก้เฉพาะ `.env.example` ตาม Task 10 ห้ามแก้ `.env` และไม่แตะค่าว่างเดิมของ `.env.example` (commit `ea10e70`)
- คงการป้องกันที่เกิน spec ไว้ทั้งสองข้อ เพราะลดโอกาสค่าลับหรือข้อมูลรั่ว: `ParseEmail` ปฏิเสธอักขระซ่อน (Task 2) และ `Config.MarshalJSON` ที่คืนเฉพาะสรุปที่ปลอดภัย (Task 10)
- rate limit ของ IPv6 ใช้ address เต็ม (/128) ตาม spec หัวข้อ 4.7 ไปก่อน; การจัดกลุ่มตาม /64 พิจารณาเป็น ADR ใน 4b
- ยังไม่เลือกวิธีรันแผน (subagent-driven หรือ native) — ห้ามเริ่มลงมือจนกว่าผู้ใช้จะเลือก
