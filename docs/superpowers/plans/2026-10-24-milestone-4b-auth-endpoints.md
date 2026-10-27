# Milestone 4b — Register, ยืนยันอีเมล, login/logout และ session ผ่าน HTTP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** เปิด endpoint ของ auth ภายใต้ `/api/v1/auth` (csrf, register, ขอรหัสยืนยันใหม่, ยืนยันอีเมล, login, me, logout) ที่ใช้ชิ้นส่วนจากแผน 4a ให้ทำ flow สมัคร → อ่าน OTP จาก Mailpit → ยืนยัน → login → me → logout ได้จริงด้วย curl และ Swagger UI

**Architecture:** use case อยู่ใน `service.Auth` (core) ซึ่ง implement `port.AuthService` และ `port.EmailVerificationService`; อ่านข้อมูลนอก transaction ผ่าน port ใหม่ `port.Repositories` และเขียนข้อมูลพร้อม audit ใน `port.UnitOfWork` เดียว; HTTP adapter แปลง request เป็นชนิดที่ตรวจแล้ว (`domain.RegisterInput` ฯลฯ) ผ่าน middleware ตาม phase 1 spec หัวข้อ 5 (request ID → access log → recover → security headers → resolve session → require auth → CSRF/Origin → handler) และแปลง `domain.Error` เป็น status code ตามตารางเดียว; ประกอบทุกอย่างที่ `cmd/api`

**Tech Stack:** Go 1.27 (go.mod `go 1.27.1`), Fiber v3.5.0, pgx v5.11.0, PostgreSQL 17.11, swaggo/swag v1.16.6 (Swagger 2.0), golangci-lint v2.14.0, Mailpit v1.31.6

**Spec:** [docs/superpowers/specs/2026-10-22-milestone-4-auth-design.md](../specs/2026-10-22-milestone-4-auth-design.md) หัวข้อ 1, 5, 7, 8 และ 9 (แผน 4b) ซึ่งอ้าง [phase 1 spec](../specs/2026-10-05-phase-1-design.md) หัวข้อ 5, 8, 9, 10 และ 11 กับ [AGENTS.md](../../../AGENTS.md); งานส่งต่อจากแผน 4a อยู่ใน [roadmap](2026-10-05-phase-1-roadmap.md) หัวข้อ milestone 4 ("ข้อส่งต่อจากรีวิวทั้ง branch ของแผน 4a ไป 4b")

## Global Constraints

- core (`internal/core/**` รวม `_test.go`) import ได้เฉพาะ standard library, `github.com/shopspring/decimal`, `github.com/google/uuid`, `golang.org/x/crypto/argon2` และ `go-backend-stock/internal/core/...`
- Endpoints (ใต้ `/api/v1`): `GET /auth/csrf` (optional auth) → 200 `{csrf_token, expires_at}` + cookie `csrf_nonce` เมื่อยังไม่ login; `POST /auth/register` (CSRF) → 202 `{message, challenge_id, expires_in: 600}` แบบเดียวกันทุกกรณี + timing floor; `POST /auth/email-verification/request` (CSRF) → 202 แบบเดียวกันทุกกรณี + timing floor; `POST /auth/email-verification/confirm` (CSRF) → 204, ผิด/หมดอายุ/ใช้แล้ว/ผิดครบ → 400 `invalid_verification`; `POST /auth/login` (CSRF) → 200 `{user, expires_at}` + session cookie, 401 `invalid_credentials`, 403 `email_not_verified`; `GET /auth/me` (required) → 200 User; `POST /auth/logout` (optional + CSRF) → 204 + ล้าง cookie
- User = `{id, email, email_verified, created_at}`; ห้ามส่ง password hash; timestamp เป็น RFC 3339 UTC
- Rate limits: `signup_ip` 20/ชม. + `email_verification_request_email` (cooldown 60s + 5/ชม.) สำหรับ register; `email_verification_request_ip` 20/ชม. + `..._request_email` สำหรับขอรหัสใหม่; `email_verification_confirm_ip` 20/15 นาที; `login_ip` 30/15 นาที + `login_email` 10/15 นาที; ถูกปฏิเสธ → 429 `rate_limited` + `Retry-After` (วินาที ปัดขึ้น) และ audit `security.rate_limited` (metadata มี scope) ใน transaction แยกเฉพาะเมื่อ `RecordDenialAudit`
- ทุก flow: parse input → rate limit → งาน CPU (hash) นอก transaction → transaction เดียวที่มีทั้งข้อมูลและ audit → ตอบ → ส่งอีเมลผ่าน Mailer หลัง commit; ไม่ถือ semaphore ของ hasher หรือเรียก Mailer ระหว่าง transaction
- Register: อีเมลใหม่ → insert user (ยังไม่ยืนยัน) + challenge + audit `auth.registered` และ `auth.email_verification_requested` → ส่ง OTP; อีเมลที่มีบัญชีแล้วทุกสถานะ → ไม่แก้อะไร ไม่สร้าง challenge ไม่มี audit → ส่ง `SendRegistrationAttempt`; ทั้งสองกรณี hash รหัสผ่านก่อนเข้า transaction และตอบ challenge ID (สุ่มเมื่ออีเมลซ้ำ)
- ขอรหัสใหม่: เฉพาะบัญชี local ที่ยังไม่ยืนยัน: lock user → `InvalidateOpen` → insert challenge → audit → ส่ง OTP; กรณีอื่นไม่เขียนและไม่ส่ง
- ยืนยัน: `ByID` (purpose email_verification) → lock user → lock challenge → ตรวจเปิดอยู่/ไม่หมดอายุ/ผิดน้อยกว่า `OTP_MAX_ATTEMPTS` → เทียบ HMAC; ผิด → `RecordFailure` + audit `auth.email_verification_failed` (actor NULL, reason `otp_mismatch`) แล้ว **commit** ก่อนคืน error; challenge ไม่มี/หมดอายุ/ปิดแล้ว/ผิดครบ → audit failed ด้วย reason นั้น; ถูก → `Consume` + `MarkEmailVerified` + audit `auth.email_verified`; ไม่สร้าง session
- Login: ไม่ใช้ `ParsePassword`; รหัสยาวเกิน 32 ตัว → `invalid_credentials` โดยไม่ hash; ไม่มีบัญชีหรือ Google-only → verify dummy hash → audit `auth.login_failed` (method password, actor NULL, reason `invalid_credentials`) → 401; รหัสผิด → audit → 401 (body เดียวกันทุกไบต์ ยกเว้น `request_id`); รหัสถูกแต่ยังไม่ยืนยัน → audit reason `email_not_verified` → 403; ถูกและยืนยันแล้ว → (ถ้า `NeedsRehash` hash ใหม่ก่อน) transaction: lock user → อ่าน credentials ใหม่ใต้ lock ถ้า hash เปลี่ยน → 401 → `SetPassword` เมื่อ rehash → insert session → audit `auth.login_succeeded` (method password, session ID) → commit; audit ของ login ที่ล้มเหลวเขียนใน transaction ของตัวเอง ถ้าเขียนไม่ได้ log error แล้วตอบ 500
- Session: token 32 bytes base64url, เก็บ SHA-256; อายุ `SESSION_TTL`; หมดอายุหรือถูก revoke = ไม่ได้ login; DB ใช้ไม่ได้ตอบ 503; cookie `session` (dev HTTP, Secure=false) หรือ `__Host-session` + Secure เมื่อ `PUBLIC_ORIGIN` เป็น https; HttpOnly, SameSite=Lax, Path=/, ไม่มี Domain
- CSRF: ทุก method ที่ไม่ใช่ GET/HEAD/OPTIONS ต้องมี `X-CSRF-Token` ที่ตรงกับ binding ปัจจุบัน (`session:<session ID>` เมื่อ login หรือ `pre:<nonce>` จาก cookie `csrf_nonce`/`__Host-csrf_nonce` ที่เป็น base64url 43 ตัว) และ Origin ตรงกับ `PUBLIC_ORIGIN` แบบ exact (ไม่มี Origin ใช้ origin ของ Referer; ไม่มีทั้งคู่ → 403 `csrf_failed`); cookie nonce: HttpOnly, SameSite=Lax, Path=/, อายุ 1 ชั่วโมง
- Logout: ถ้ามี session ที่หาได้ → `Revoke(reason logout)`; ได้ `true` จึง audit `auth.logout`; ล้าง session cookie ทุกครั้ง
- Error mapping: `invalid_input`/`malformed_json` 400, `unauthenticated` 401, `invalid_credentials` 401, `invalid_verification` 400, `email_not_verified` 403, `csrf_failed` 403, `rate_limited` 429 + `Retry-After`, `server_busy` 503 + `Retry-After: 1`, `feature_disabled` 503, `dependency_unavailable` 503, `internal`/`audit_write_failed` 500 โดยไม่เปิดรายละเอียด; envelope `{"error":{"code","message","request_id","details"?}}` โดย `details` คือ reasons ของ `invalid_input` (เพิ่มแบบ additive ตาม ADR-0011)
- Audit: actor ต้องมาจาก identity ที่ยืนยันแล้ว (login/ยืนยันที่ล้มเหลวใช้ actor NULL); metadata เก็บเฉพาะ MaskedIP (`ip`), UserAgent (`user_agent`) และค่าตาม allowlist (`method`, `scope`) ไม่เก็บอีเมลดิบ; ทุกเวลาที่ส่งเข้า repository ผ่าน `service.NowForStorage`
- Body ไม่รับ unknown fields หรือข้อมูลต่อท้าย → 400 `malformed_json`; body limit 64 KiB ตามเดิม
- ห้าม log รหัสผ่าน, OTP, session token, cookie, CSRF token, nonce หรือ secret; Mailer error ต้องไม่เปลี่ยนคำตอบ 202
- ไม่ทำ Google OIDC (4c), password reset (milestone 5) หรือ endpoint ของพอร์ต
- ไม่แก้ `.env.example` และ `.env` (ผู้ใช้เป็นเจ้าของ; ตอนนี้ไฟล์ที่ commit มีชื่อ secret สี่ตัวแต่ค่าว่าง)
- ต้องเห็น test ล้มก่อนแล้วจึงเขียนโค้ดทุก task; จบ task ด้วย `gofmt -l` ที่ไม่พิมพ์อะไร (ถ้าพิมพ์ให้ `gofmt -w` แล้วดู diff), `go vet` และ `make lint` ผ่าน (รีวิวรายงาน task ของแผน 4a ไม่ได้รัน linter แล้ว `make check` ล้มตอนท้าย)
- task ที่แก้ annotation ของ handler ต้องรัน `make swagger` แล้ว commit `docs/api/` ในชุดเดียวกัน ห้ามแก้ไฟล์ generated ด้วยมือ
- commit message ไม่มี `Co-Authored-By` หรือชื่อ Claude; commit ในเครื่องเท่านั้น ห้าม push; `git add` เฉพาะไฟล์ของ task ห้าม `git add -A`/`git add .`
- Docker: ใช้ได้เฉพาะ `docker compose ps`, `docker compose up -d mailpit`, `docker compose stop mailpit` และคืนสถานะ Mailpit ตามที่เจอ; คำสั่งอื่นต้องถามผู้ใช้ก่อน
- รันทุกคำสั่งที่ root ของ repo บน branch `feature/create_login_and_register_2`; `go` คือ `/usr/local/go/bin/go` (1.27.2)
- integration tests ใช้ PostgreSQL จริงผ่าน `make test-integration` (ต้องให้ postgres และ migrate ของ compose ขึ้นอยู่) และใช้อีเมล/IP สุ่มต่อ test เพราะไม่ล้างข้อมูลระหว่าง test

## Review Focus

1. **อีเมลตัวพิมพ์ต่างกัน** — สมัครด้วย `Mixed@Example.COM` แล้ว login ด้วย `mixed@example.com` ต้องได้บัญชีเดียวกัน และสมัครซ้ำด้วยตัวพิมพ์อื่นต้องเข้าเส้นทาง "มีบัญชีอยู่แล้ว" — Task 9 `TestRegisterTreatsEmailCaseAsTheSameAccount`, Task 11 `TestLoginIgnoresEmailCase`
2. **สมัครอีเมลเดียวกันพร้อมกันสองคำขอ** — ต้องได้บัญชีเดียว คำขอหนึ่ง 202 อีกคำขอ 202 หรือ 429 และไม่มี 500 — Task 9 `TestRegisterSameEmailConcurrentlyCreatesOneAccount`
3. **logout หลัง session หมดอายุ** — browser ยังถือ cookie และ CSRF token ที่ผูก session เก่า; ขอ CSRF ใหม่แล้ว logout ต้องได้ 204 และ cookie ถูกล้างโดยไม่สร้าง audit logout — Task 11 `TestLogoutAfterTheSessionExpired`
4. **OTP ที่พิมพ์เป็นเลขไทยหรือมีช่องว่างผ่าน HTTP** — ต้องได้ 400 `invalid_input` ที่ขึ้นต้นด้วย `otp` และไม่นับเป็นการกรอกผิด — Task 10 `TestConfirmRejectsNonASCIIDigitsWithoutCountingAnAttempt`
5. **รหัสผ่านถูก rehash ตอน login** — login ครั้งแรกเปลี่ยน hash เป็น parameters ใหม่ และ login ครั้งต่อไปด้วยรหัสเดิมยังผ่าน — Task 11 `TestLoginUpgradesAnOldHashAndKeepsWorking`

---

## File Structure

| ไฟล์ | หน้าที่ | Task |
| --- | --- | --- |
| `internal/core/domain/secret.go` (+test), `password.go`, `otp.go`, `user.go`, `internal/config/redact.go` (+test) | ปิดค่าลับทุก verb ของ fmt และกรณีอยู่ใน field ที่ไม่ export | 1 |
| `internal/core/domain/ratelimit.go`, `internal/adapter/postgres/ratelimit.go` (+test) | `RateLimitDecision.Scope` | 2 |
| `internal/core/service/password.go`, `csrf.go`, `timing.go` (+tests) | `DefaultArgon2Params()`, `Rehash`, ตรวจ clock ที่เป็น nil, reason ใน log ของ hasher | 3 |
| `internal/adapter/mail/smtp.go`, `dispatcher.go`, `internal/config/auth.go` (+tests) | Quit/ctx error, recover ใน worker, เพดาน OTP attempts, CIDR แบบ IPv4-mapped | 4 |
| `internal/core/domain/auth_input.go`, `principal.go`, `session_grant.go`, `errors.go` (+test), `internal/core/port/repositories.go`, `internal/adapter/postgres/store.go`, `internal/core/service/csrf.go` | ชนิดข้อมูลของ auth, `Error.RetryAfter`, sentinels, `port.Repositories` | 5 |
| `internal/adapter/http/errors.go`, `server.go`, `middleware/notfound.go` (+tests) | แปลง domain error เป็น HTTP, `details`, `Retry-After`, 405 + `Allow` | 6 |
| `internal/core/port/auth_service.go`, `internal/core/service/auth.go`, `audit.go`, `admission.go` (+tests, `fakes_test.go`) | `service.Auth`, `Authenticate`, audit และ rate-limit helpers | 7 |
| `internal/adapter/http/auth.go`, `cookies.go`, `meta.go`, `middleware/session.go`, `middleware/csrf.go` (+tests) | `AuthOptions`, request meta, cookies, middleware session/CSRF, `GET /auth/csrf` | 8 |
| `internal/core/port/auth_service.go`, `internal/core/service/register.go`, `internal/adapter/http/decode.go`, `register.go`, `auth.go`, `auth_dto.go`, `auth_harness_test.go`, `register_test.go`, `auth_contract_test.go`, `Makefile`, `docs/api/` | integration harness, `Register` + `POST /auth/register` | 9 |
| `internal/core/port/auth_service.go`, `internal/core/service/email_verification.go`, `internal/adapter/http/email_verification.go`, `email_verification_test.go`, `docs/api/` | ขอรหัสใหม่และยืนยันอีเมล | 10 |
| `internal/core/port/auth_service.go`, `internal/core/service/login.go` (+test), `internal/adapter/http/session_auth.go`, `auth_dto.go`, `login_test.go`, `docs/api/` | login, me, logout | 11 |
| `internal/adapter/http/auth_failures_test.go` | audit rollback, mailer ล่ม, log hygiene, session store ล่ม | 12 |
| `cmd/api/wire.go` (+test), `cmd/api/main.go`, `docs/api/` | ประกอบ dependency และ shutdown | 13 |
| `README.md`, `.github/workflows/ci.yml`, spec, roadmap | คู่มือ curl, CI Mailpit, สถานะ และตรวจรับ | 14 |

---

### Task 1: ปิดค่าลับทุก verb และใน field ที่ไม่ export

ข้อส่งต่อจาก 4a: `Password`, `OTPCode` และ `config.Secret` ยังพิมพ์ค่าจริงเมื่อใช้ `%d`/`%x` และเมื่ออยู่ใน field ที่ไม่ export ของ struct อื่น (fmt ไม่เรียก method ของ field ที่ไม่ export) ต้องปิดก่อน `RegisterInput`/`LoginInput` จะถือรหัสผ่าน

**Files:**
- Create: `internal/core/domain/secret.go`, `internal/core/domain/secret_test.go`, `internal/config/redact_test.go`
- Modify: `internal/core/domain/password.go`, `internal/core/domain/otp.go`, `internal/core/domain/user.go`, `internal/config/redact.go`

**Interfaces:**
- Consumes: `redacted` (`"[REDACTED]"` ใน `user.go`)
- Produces:
  - `type SecretText struct{...}`; `func NewSecretText(s string) SecretText`; `(SecretText).Reveal() string`; `(SecretText).IsZero() bool`
  - `Password`, `OTPCode`, `SecretText`, `PasswordHash` implement `fmt.Formatter` (ทุก verb พิมพ์ `[REDACTED]`) และ `slog.LogValuer`; `Password`/`OTPCode`/`SecretText` เก็บค่าหลัง pointer
  - `config.Secret` implement `fmt.Formatter`

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/core/domain/secret_test.go`:

```go
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

// holder keeps the values in unexported fields: fmt prints those through reflection and never
// calls their methods, which is how a value would leak from a request struct in a log line.
type holder struct {
	pw   domain.Password
	otp  domain.OTPCode
	text domain.SecretText
	hash domain.PasswordHash
}

func TestSecretsNeverPrintWithAnyVerb(t *testing.T) {
	pw, err := domain.ParsePassword("Zq9!kVx1", domain.Email{})
	if err != nil {
		t.Fatal(err)
	}
	otp, err := domain.ParseOTP("042731")
	if err != nil {
		t.Fatal(err)
	}
	text := domain.NewSecretText("raw-token-ZQXJ")
	hash := domain.PasswordHash("$argon2id$secret-hash-ZQXJ")
	h := holder{pw: pw, otp: otp, text: text}
	secrets := []string{"Zq9!kVx1", "042731", "raw-token-ZQXJ", "secret-hash-ZQXJ"}

	values := []any{pw, &pw, otp, &otp, text, &text, hash, &hash, h, &h}
	verbs := []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x", "%X", "%10v"}
	var outputs []string
	for _, v := range values {
		for _, verb := range verbs {
			outputs = append(outputs, fmt.Sprintf(verb, v))
		}
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, string(data))
	}
	var logs bytes.Buffer
	for _, handler := range []slog.Handler{slog.NewJSONHandler(&logs, nil), slog.NewTextHandler(&logs, nil)} {
		slog.New(handler).Info("values", "pw", pw, "otp", otp, "text", text, "hash", hash, "holder", h)
	}
	outputs = append(outputs, logs.String())

	for _, out := range outputs {
		for _, s := range secrets {
			if strings.Contains(out, s) {
				t.Errorf("output %q leaks %q", out, s)
			}
		}
	}
}

func TestSecretsStillReveal(t *testing.T) {
	text := domain.NewSecretText("raw-token")
	if text.Reveal() != "raw-token" || text.IsZero() || !(domain.SecretText{}).IsZero() || (domain.SecretText{}).Reveal() != "" {
		t.Error("SecretText Reveal/IsZero misbehave")
	}
	pw, err := domain.ParsePassword("Zq9!kVx1", domain.Email{})
	if err != nil || pw.Reveal() != "Zq9!kVx1" || pw.IsZero() || !(domain.Password{}).IsZero() {
		t.Error("Password Reveal/IsZero misbehave")
	}
	otp, err := domain.ParseOTP("000123")
	if err != nil || otp.Reveal() != "000123" {
		t.Error("OTPCode Reveal misbehaves")
	}
	if (domain.OTPCode{}).Reveal() != "" {
		t.Error("zero OTPCode must reveal an empty string")
	}
}
```

สร้าง `internal/config/redact_test.go`:

```go
package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestSecretNeverPrintsWithAnyVerb(t *testing.T) {
	s := Secret("S3cret-ZQXJ-value")
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x", "%X"} {
		if out := fmt.Sprintf(verb, s); strings.Contains(out, "ZQXJ") {
			t.Errorf("%s printed %q", verb, out)
		}
	}
	if s.Reveal() != "S3cret-ZQXJ-value" {
		t.Error("Reveal must still return the value")
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/domain/ ./internal/config/ -run 'Secret' -count=1`
Expected: build fail — `undefined: domain.NewSecretText`; หลังเพิ่ม `SecretText` แล้ว `%d`/`%x` และ `holder` ต้องล้มด้วยข้อความ "leaks"

- [ ] **Step 3: เขียนโค้ด**

สร้าง `internal/core/domain/secret.go`:

```go
package domain

import (
	"fmt"
	"io"
	"log/slog"
)

// secret keeps a sensitive string behind a pointer. fmt does not call methods on unexported
// struct fields, so a value held in an unexported field of another struct would otherwise be
// printed through reflection; behind a pointer only an address is printed.
type secret struct{ p *string }

func newSecret(s string) secret { return secret{p: &s} }

func (s secret) reveal() string {
	if s.p == nil {
		return ""
	}
	return *s.p
}

// writeRedacted implements fmt.Formatter for the redacting types, so no verb (%d, %x, %q…) can
// print the value.
func writeRedacted(f fmt.State) { _, _ = io.WriteString(f, redacted) }

// SecretText is a sensitive value with no rules of its own, such as a password typed at login or
// a raw session token. It never prints, formats, logs or marshals its value; Reveal returns it.
type SecretText struct{ v secret }

// NewSecretText wraps s.
func NewSecretText(s string) SecretText { return SecretText{v: newSecret(s)} }

func (SecretText) String() string             { return redacted }
func (SecretText) GoString() string           { return redacted }
func (SecretText) Format(f fmt.State, _ rune) { writeRedacted(f) }
func (SecretText) LogValue() slog.Value       { return slog.StringValue(redacted) }

// MarshalJSON keeps the value out of JSON.
func (SecretText) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// Reveal returns the value. Call it only where the value itself is needed (hashing, a cookie).
func (t SecretText) Reveal() string { return t.v.reveal() }

// IsZero reports whether t holds no value.
func (t SecretText) IsZero() bool { return t.v.reveal() == "" }
```

แก้ `internal/core/domain/password.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
import (
	"strings"
	"unicode"
	"unicode/utf8"
)
```

   ด้วย

```go
import (
	"fmt"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"
)
```

2. แทน

```go
type Password struct{ value string }

func (Password) String() string   { return redacted }
func (Password) GoString() string { return redacted }
```

   ด้วย

```go
type Password struct{ v secret }

func (Password) String() string             { return redacted }
func (Password) GoString() string           { return redacted }
func (Password) Format(f fmt.State, _ rune) { writeRedacted(f) }
func (Password) LogValue() slog.Value       { return slog.StringValue(redacted) }
```

3. แทน

```go
func (p Password) Reveal() string { return p.value }
```

   ด้วย

```go
func (p Password) Reveal() string { return p.v.reveal() }
```

4. แทน

```go
func (p Password) IsZero() bool { return p.value == "" }
```

   ด้วย

```go
func (p Password) IsZero() bool { return p.v.reveal() == "" }
```

5. แทน

```go
	return Password{value: raw}, nil
```

   ด้วย

```go
	return Password{v: newSecret(raw)}, nil
```

แก้ `internal/core/domain/otp.go` ตามคู่ต่อไปนี้:

1. แทน

```go
import (
	"strconv"
	"strings"
	"time"
)
```

   ด้วย

```go
import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)
```

2. แทน

```go
type OTPCode struct{ value string }

func (OTPCode) String() string   { return redacted }
func (OTPCode) GoString() string { return redacted }
```

   ด้วย

```go
type OTPCode struct{ v secret }

func (OTPCode) String() string             { return redacted }
func (OTPCode) GoString() string           { return redacted }
func (OTPCode) Format(f fmt.State, _ rune) { writeRedacted(f) }
func (OTPCode) LogValue() slog.Value       { return slog.StringValue(redacted) }
```

3. แทน

```go
func (c OTPCode) Reveal() string { return c.value }
```

   ด้วย

```go
func (c OTPCode) Reveal() string { return c.v.reveal() }
```

4. แทน

```go
	return OTPCode{value: s}, nil
```

   ด้วย

```go
	return OTPCode{v: newSecret(s)}, nil
```

5. แทน

```go
	return OTPCode{value: strings.Repeat("0", OTPLength-len(s)) + s}, nil
```

   ด้วย

```go
	return OTPCode{v: newSecret(strings.Repeat("0", OTPLength-len(s)) + s)}, nil
```

แก้ `internal/core/domain/user.go`:

1. เพิ่ม `"fmt"` และ `"log/slog"` ในบล็อก import (ตอนนี้มี `"time"` และ `"unicode/utf8"`)
2. แทน

```go
func (PasswordHash) String() string   { return redacted }
func (PasswordHash) GoString() string { return redacted }
```

   ด้วย

```go
func (PasswordHash) String() string             { return redacted }
func (PasswordHash) GoString() string           { return redacted }
func (PasswordHash) Format(f fmt.State, _ rune) { writeRedacted(f) }
func (PasswordHash) LogValue() slog.Value       { return slog.StringValue(redacted) }
```

แก้ `internal/config/redact.go`:

1. เพิ่ม `"io"` ในบล็อก import
2. แทน

```go
func (Secret) GoString() string { return redactedValue }
```

   ด้วย

```go
func (Secret) GoString() string { return redactedValue }

// Format makes every fmt verb (%d, %x, %q…) print [REDACTED].
func (Secret) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, redactedValue) }
```

(`config.Secret` ยังเป็นชนิด string จึงยังพิมพ์ค่าได้ถ้าอยู่ใน field ที่ไม่ export ของ struct อื่น; `Config`, `AuthConfig` และ `SMTPConfig` ใช้ field ที่ export ทั้งหมด จึงปลอดภัยในโค้ดปัจจุบัน)

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/core internal/config; go vet ./internal/core/... ./internal/config/ && go test ./internal/... -count=1 -race && make lint`
Expected: ผ่านทั้งหมด (test เดิมที่ใช้ `Reveal`/`IsZero` ยังผ่าน; ถ้ามีโค้ดใดเทียบ `Password`/`OTPCode` ด้วย `==` ให้เปลี่ยนเป็นเทียบ `Reveal()` และบันทึกใน report)

- [ ] **Step 5: Commit**

```bash
git add internal/core/domain internal/config/redact.go internal/config/redact_test.go
git commit -m "fix(domain): redact secrets for every fmt verb and inside unexported fields"
```

---

### Task 2: `RateLimitDecision` บอก scope ที่ปฏิเสธ

spec หัวข้อ 5 กำหนดให้ metadata ของ `security.rate_limited` มี `scope` แต่ `Admit` ไม่บอกว่า key ใดปฏิเสธ

**Files:**
- Modify: `internal/core/domain/ratelimit.go`, `internal/adapter/postgres/ratelimit.go`, `internal/adapter/postgres/ratelimit_test.go`

**Interfaces:**
- Consumes: `rule`, `admit`, `newRuntimeStore`, `randomHash`, `now` (helpers เดิมใน package `postgres` tests)
- Produces: `domain.RateLimitDecision.Scope domain.RateLimitScope` — scope ของ key ที่ปฏิเสธ; `""` เมื่อผ่าน

- [ ] **Step 1: เขียน failing test**

ต่อท้าย `internal/adapter/postgres/ratelimit_test.go`:

```go

// TestRateLimitDenialNamesTheDenyingScope: the use case records the scope in the
// security.rate_limited audit event, so the decision must say which key refused.
func TestRateLimitDenialNamesTheDenyingScope(t *testing.T) {
	store := newRuntimeStore(t)
	ip := rule(t, domain.RateLimitLoginIP, randomHash(t), 5, time.Hour)
	email := rule(t, domain.RateLimitLoginEmail, randomHash(t), 1, time.Hour)
	start := now()

	if d := admit(t, store, start, ip, email); !d.Allowed || d.Scope != "" {
		t.Fatalf("first attempt = %+v, want allowed with no scope", d)
	}
	d := admit(t, store, start.Add(time.Second), ip, email)
	if d.Allowed || d.Scope != domain.RateLimitLoginEmail {
		t.Errorf("second attempt = %+v, want denied by %s", d, domain.RateLimitLoginEmail)
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `make test-integration` (ต้องให้ `docker compose ps` แสดง postgres ทำงานอยู่; ถ้าไม่ทำงานให้หยุดและรายงาน)
Expected: build fail — `d.Scope undefined`

- [ ] **Step 3: เขียนโค้ด**

แก้ `internal/core/domain/ratelimit.go` — แทน

```go
	RecordDenialAudit bool // true at most once per key per minute, so attacks cannot flood audit
}
```

ด้วย

```go
	RecordDenialAudit bool // true at most once per key per minute, so attacks cannot flood audit
	// Scope is the scope of the key that denied, for the security.rate_limited audit metadata;
	// "" when the request was allowed.
	Scope RateLimitScope
}
```

แก้ `internal/adapter/postgres/ratelimit.go` — แทน

```go
			decision = domain.RateLimitDecision{RetryAfter: retryAfter, RecordDenialAudit: claimed > 0}
```

ด้วย

```go
			decision = domain.RateLimitDecision{RetryAfter: retryAfter, RecordDenialAudit: claimed > 0, Scope: k.scope}
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal; go vet ./internal/... && make test-integration && make lint`
Expected: ผ่าน และไม่มี `SKIP` ใน `internal/adapter/postgres`

- [ ] **Step 5: Commit**

```bash
git add internal/core/domain/ratelimit.go internal/adapter/postgres/ratelimit.go internal/adapter/postgres/ratelimit_test.go
git commit -m "feat(ratelimit): report the scope of the key that denied"
```

---

### Task 3: เก็บกวาด service (`DefaultArgon2Params`, `Rehash`, clock nil, reason ใน log)

**Files:**
- Modify: `internal/core/service/password.go`, `internal/core/service/password_test.go`, `internal/core/service/csrf.go`, `internal/core/service/csrf_test.go`, `internal/core/service/timing.go`, `internal/core/service/timing_test.go`

**Interfaces:**
- Consumes: `domain.SecretText` (Task 1); test helpers `newHasher`, `hold`, `requireCode`, `newFakeClock` ที่มีอยู่
- Produces:
  - `func DefaultArgon2Params() Argon2Params` (แทนตัวแปร global ตาม AGENTS.md ที่ห้าม global state)
  - `(*PasswordHasher).Rehash(ctx context.Context, password domain.SecretText) (domain.PasswordHash, error)` — hash รหัสที่เพิ่ง verify ผ่าน ใช้ slot เดียวกับ `Hash`
  - warn log `auth.hash_busy` มี attribute `reason` = `timeout` หรือ `canceled`
  - `NewCSRFCodec` คืน error เมื่อ clock เป็น nil
  - `func NewPacer(clock port.Clock, floor, jitter time.Duration) (*Pacer, error)` — error เมื่อ clock เป็น nil หรือ duration ติดลบ

- [ ] **Step 1: เขียน failing tests**

แก้ `internal/core/service/password_test.go`:

1. แทน

```go
	h, _ := newHasher(t, DefaultArgon2Params, 1, 0)
```

   ด้วย

```go
	h, _ := newHasher(t, DefaultArgon2Params(), 1, 0)
```

2. ต่อท้ายไฟล์ด้วย

```go

func TestRehashProducesAHashThatVerifies(t *testing.T) {
	h, _ := newHasher(t, testArgon2Params, 1, 0)
	hash, err := h.Rehash(t.Context(), domain.NewSecretText("old rules ok"))
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := h.Verify(t.Context(), hash, "old rules ok"); err != nil || !ok {
		t.Errorf("Verify(rehashed) = %v, %v; want true, nil", ok, err)
	}
	if need, err := h.NeedsRehash(hash); err != nil || need {
		t.Errorf("a fresh hash must use the current parameters: need=%v err=%v", need, err)
	}
	_, err = h.Rehash(t.Context(), domain.SecretText{})
	requireCode(t, err, domain.CodeInternal)

	release := hold(t, h)
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err = h.Rehash(ctx, domain.NewSecretText("x"))
	requireCode(t, err, domain.CodeServerBusy)
}

func TestBusyLogSaysWhyTheCallerStoppedWaiting(t *testing.T) {
	h, logs := newHasher(t, testArgon2Params, 1, 20*time.Millisecond)
	release := hold(t, h)
	defer release()

	_, err := h.Hash(t.Context(), mustPassword(t, "Zq9!kVx1"))
	requireCode(t, err, domain.CodeServerBusy)
	if !strings.Contains(logs.String(), `"reason":"timeout"`) {
		t.Errorf("log %q should say the queue timed out", logs.String())
	}

	logs.Reset()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	slow, slowLogs := newHasher(t, testArgon2Params, 1, time.Minute)
	releaseSlow := hold(t, slow)
	defer releaseSlow()
	_, err = slow.Hash(ctx, mustPassword(t, "Zq9!kVx1"))
	requireCode(t, err, domain.CodeServerBusy)
	if !strings.Contains(slowLogs.String(), `"reason":"canceled"`) {
		t.Errorf("log %q should say the caller gave up", slowLogs.String())
	}
}
```

แก้ `internal/core/service/csrf_test.go` — แทน

```go
func TestNewCSRFCodecRejectsShortSecret(t *testing.T) {
	if _, err := NewCSRFCodec([]byte("short"), newFakeClock()); err == nil {
		t.Error("NewCSRFCodec() accepted a secret under 32 bytes")
	}
}
```

ด้วย

```go
func TestNewCSRFCodecRejectsShortSecret(t *testing.T) {
	if _, err := NewCSRFCodec([]byte("short"), newFakeClock()); err == nil {
		t.Error("NewCSRFCodec() accepted a secret under 32 bytes")
	}
	if _, err := NewCSRFCodec([]byte(strings.Repeat("s", 32)), nil); err == nil {
		t.Error("NewCSRFCodec() accepted a nil clock, which would panic on the first request")
	}
}
```

แก้ `internal/core/service/timing_test.go` — แทน

```go
func TestNewPacerIsUsable(t *testing.T) {
	clock := newFakeClock()
	pacer := NewPacer(clock, 0, 0)
	pacer.Wait(t.Context(), clock.Now()) // must neither panic nor block
}
```

ด้วย

```go
func TestNewPacerIsUsable(t *testing.T) {
	clock := newFakeClock()
	pacer, err := NewPacer(clock, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	pacer.Wait(t.Context(), clock.Now()) // must neither panic nor block
}

func TestNewPacerValidatesItsArguments(t *testing.T) {
	if _, err := NewPacer(nil, time.Second, 0); err == nil {
		t.Error("NewPacer() accepted a nil clock")
	}
	if _, err := NewPacer(newFakeClock(), -time.Second, 0); err == nil {
		t.Error("NewPacer() accepted a negative floor")
	}
	if _, err := NewPacer(newFakeClock(), 0, -time.Second); err == nil {
		t.Error("NewPacer() accepted a negative jitter")
	}
}
```

(ถ้า `timing_test.go` ยังไม่ import `time` ให้เพิ่ม; ถ้า `csrf_test.go` ยังไม่ import `strings` ให้เพิ่ม — ทั้งสองไฟล์มี import เหล่านี้อยู่แล้วตามแผน 4a)

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/service/ -count=1`
Expected: build fail — `cannot call non-function DefaultArgon2Params`, `h.Rehash undefined`, `NewPacer` คืนค่าเดียว

- [ ] **Step 3: เขียนโค้ด**

แก้ `internal/core/service/password.go` ตามคู่ต่อไปนี้:

1. แทน

```go
// DefaultArgon2Params are the confirmed phase 1 parameters (ADR-0005): 19 MiB, 2 passes, 1 lane.
var DefaultArgon2Params = Argon2Params{MemoryKiB: 19456, Iterations: 2, Parallelism: 1}
```

   ด้วย

```go
// DefaultArgon2Params returns the confirmed phase 1 parameters (ADR-0005): 19 MiB, 2 passes,
// 1 lane. It is a function so no package-level state can be changed at run time.
func DefaultArgon2Params() Argon2Params {
	return Argon2Params{MemoryKiB: 19456, Iterations: 2, Parallelism: 1}
}
```

2. แทน

```go
// Verify reports whether password matches hash, comparing in constant time.
```

   ด้วย

```go
// Rehash hashes a password that has just been verified against its stored hash, so login can
// upgrade a hash made with older parameters. It takes a slot like Hash. The password may not
// pass today's policy (it was set under older rules), which is why it is not a domain.Password.
func (h *PasswordHasher) Rehash(ctx context.Context, password domain.SecretText) (domain.PasswordHash, error) {
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

// Verify reports whether password matches hash, comparing in constant time.
```

3. แทน

```go
	select {
	case h.slots <- struct{}{}:
		return h.release, nil
	case <-timer.C:
	case <-ctx.Done():
	}
	h.log.Warn("password hashing queue is full", slog.String("event", "auth.hash_busy"))
```

   ด้วย

```go
	reason := "timeout"
	select {
	case h.slots <- struct{}{}:
		return h.release, nil
	case <-timer.C:
	case <-ctx.Done():
		reason = "canceled" // the client went away; not a sign of overload
	}
	h.log.Warn("password hashing queue is full",
		slog.String("event", "auth.hash_busy"), slog.String("reason", reason))
```

แก้ `internal/core/service/csrf.go`:

1. เพิ่ม `"errors"` ในบล็อก import
2. แทน

```go
func NewCSRFCodec(secret []byte, clock port.Clock) (*CSRFCodec, error) {
```

   ด้วย

```go
func NewCSRFCodec(secret []byte, clock port.Clock) (*CSRFCodec, error) {
	if clock == nil {
		return nil, errors.New("CSRF codec needs a clock")
	}
```

แก้ `internal/core/service/timing.go`:

1. เพิ่ม `"errors"` ในบล็อก import
2. แทน

```go
func NewPacer(clock port.Clock, floor, jitter time.Duration) *Pacer {
	return &Pacer{clock: clock, floor: floor, jitter: jitter, sleep: sleepContext, randomJitter: cryptoJitter}
}
```

   ด้วย

```go
func NewPacer(clock port.Clock, floor, jitter time.Duration) (*Pacer, error) {
	switch {
	case clock == nil:
		return nil, errors.New("pacer needs a clock")
	case floor < 0 || jitter < 0:
		return nil, errors.New("pacer floor and jitter must not be negative")
	}
	return &Pacer{clock: clock, floor: floor, jitter: jitter, sleep: sleepContext, randomJitter: cryptoJitter}, nil
}
```

(ถ้า doc comment ของ `NewPacer` บอกค่าเริ่มต้น 500 ms/100 ms ให้เปลี่ยนเป็น "The usual values come from AUTH_RESPONSE_FLOOR and AUTH_RESPONSE_JITTER." เพราะฟังก์ชันไม่ได้ตั้งค่าเริ่มต้นเอง — ข้อเล็กน้อยจากรีวิว 4a)

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/core; go vet ./internal/core/... && go test ./internal/core/... -count=1 -race && make lint`
Expected: ผ่านทั้งหมด

- [ ] **Step 5: Commit**

```bash
git add internal/core/service
git commit -m "refactor(service): add Rehash, guard nil clocks and make the Argon2 defaults a function"
```

---

### Task 4: เก็บกวาด mail และ config

**Files:**
- Modify: `internal/adapter/mail/smtp.go`, `internal/adapter/mail/smtp_test.go`, `internal/adapter/mail/dispatcher.go`, `internal/adapter/mail/dispatcher_test.go`, `internal/config/auth.go`, `internal/config/auth_test.go`

**Interfaces:**
- Consumes: `startFakeSMTP`, `fakeSMTPOptions`, `newTestMailer`, `testOTPMail`, `fakeSender`, `newTestDispatcher`, `syncBuffer` (test helpers เดิม); `domain.MaxOTPFailedAttemptsCeiling`
- Produces:
  - `SMTPMailer` ไม่ถือว่าส่งล้มเหลวเมื่อ `QUIT` ล้มหลัง DATA สำเร็จ และคืน error ที่ `errors.Is(err, context.Canceled)` เมื่อผู้เรียกยกเลิก
  - worker ของ `Dispatcher` recover panic ของ sender, log `event=mail.panic` แล้วทำงานต่อ
  - `OTP_MAX_ATTEMPTS` ใช้เพดาน `domain.MaxOTPFailedAttemptsCeiling`; `TRUSTED_PROXY_CIDRS` ปฏิเสธ prefix แบบ IPv4-mapped (`::ffff:10.0.0.0/104`) ซึ่งไม่มีวันตรงเพราะ address ถูก unmap ก่อนเทียบ

- [ ] **Step 1: เขียน failing tests**

แก้ `internal/adapter/mail/smtp_test.go`:

1. ใน `type fakeSMTPOptions struct` เพิ่ม field

```go
	failQuit    bool // answer QUIT with an error after accepting the message
```

2. ใน `serve` แทน

```go
		case upper == "QUIT":
			say("221 bye")
			return
```

   ด้วย

```go
		case upper == "QUIT":
			if s.opts.failQuit {
				say("554 transaction failed")
				return
			}
			say("221 bye")
			return
```

3. ต่อท้ายไฟล์ด้วย

```go

func TestSMTPMailerTreatsAFailingQuitAfterDataAsDelivered(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{failQuit: true})
	mailer := newTestMailer(t, srv, nil)
	if err := mailer.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatalf("SendEmailVerification() error = %v, but the server had already accepted the message", err)
	}
	if got := srv.messages(); len(got) != 1 {
		t.Errorf("server received %d messages, want 1", len(got))
	}
}

func TestSMTPMailerReportsCancellationAsTheContextError(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{stall: true})
	mailer := newTestMailer(t, srv, nil)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)

	err := mailer.SendEmailVerification(ctx, testOTPMail(t))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want one that wraps context.Canceled so the dispatcher logs it as canceled", err)
	}
}
```

แก้ `internal/adapter/mail/dispatcher_test.go`:

1. ใน `type fakeSender struct` เพิ่ม field

```go
	panics    atomic.Int32 // sends that should panic before doing anything
```

2. ใน `func (f *fakeSender) wait(ctx context.Context) error {` เพิ่มบรรทัดแรกของฟังก์ชัน

```go
	if f.panics.Add(-1) >= 0 {
		panic("sender exploded")
	}
```

3. ต่อท้ายไฟล์ด้วย

```go

func TestDispatcherSurvivesAPanickingSender(t *testing.T) {
	sender := &fakeSender{}
	sender.panics.Store(1)
	d, logs := newTestDispatcher(t, sender, nil)
	if err := d.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatal(err)
	}
	if err := d.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v: a panic must not stop the worker", err)
	}
	if v, _ := sender.counts(); v != 1 {
		t.Errorf("delivered %d emails after the panic, want 1", v)
	}
	if !strings.Contains(logs.String(), `"event":"mail.panic"`) || strings.Contains(logs.String(), "exploded") {
		t.Errorf("log %q should record mail.panic without the panic text", logs.String())
	}
}
```

   (`panics` เริ่มที่ 0 ใน fake อื่น ๆ จึง `Add(-1)` ได้ค่าติดลบและไม่ panic)

แก้ `internal/config/auth_test.go` — ในตารางของ `TestLoadAuthRejectsInvalidValues` แทน

```go
		{"one bad cidr in a list", func(e map[string]string) { e["TRUSTED_PROXY_CIDRS"] = "10.0.0.0/8,garbage" }, "TRUSTED_PROXY_CIDRS"},
```

ด้วย

```go
		{"one bad cidr in a list", func(e map[string]string) { e["TRUSTED_PROXY_CIDRS"] = "10.0.0.0/8,garbage" }, "TRUSTED_PROXY_CIDRS"},
		{"ipv4-mapped cidr never matches", func(e map[string]string) { e["TRUSTED_PROXY_CIDRS"] = "::ffff:10.0.0.0/104" }, "TRUSTED_PROXY_CIDRS"},
```

(ถ้า gofmt จัดตำแหน่งตารางต่างไป ให้หาแถว `"one bad cidr in a list"` แล้วเพิ่มแถวใหม่ต่อจากมัน)

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/adapter/mail/ ./internal/config/ -count=1`
Expected: FAIL — QUIT ที่ล้มทำให้ส่งล้มเหลว, error ไม่ wrap `context.Canceled`, test panic ทำให้ทั้ง process ล้ม (go test รายงาน panic), CIDR แบบ mapped ถูกยอมรับ

- [ ] **Step 3: เขียนโค้ด**

แก้ `internal/adapter/mail/smtp.go`:

1. แทน

```go
func (m *SMTPMailer) deliver(ctx context.Context, to string, raw []byte) error {
```

   ด้วย

```go
func (m *SMTPMailer) deliver(ctx context.Context, to string, raw []byte) (err error) {
	defer func() {
		// A send cut short by the caller reports the context's error, so the dispatcher logs it
		// as canceled or timeout rather than as a network failure.
		if err != nil && ctx.Err() != nil {
			err = fmt.Errorf("%w: %w", ctx.Err(), err)
		}
	}()
```

2. แทน

```go
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
```

   ด้วย

```go
	if err := w.Close(); err != nil {
		return err
	}
	// The server accepted the message (250 after DATA); a failing QUIT does not lose it.
	_ = client.Quit()
	return nil
}
```

แก้ `internal/adapter/mail/dispatcher.go` — แทนทั้งฟังก์ชัน `worker` (ตั้งแต่ `func (d *Dispatcher) worker() {` ถึงวงเล็บปิดของมัน) ด้วย

```go
func (d *Dispatcher) worker() {
	defer d.wg.Done()
	for j := range d.jobs {
		d.run(j)
	}
}

// run sends one email. A panic in the sender is logged and the worker carries on: this goroutine
// is outside Fiber's recover middleware, so an unrecovered panic would stop the whole API.
func (d *Dispatcher) run(j job) {
	ctx, cancel := context.WithTimeout(d.baseCtx, d.timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			d.log.Error("email sender panicked",
				slog.String("event", "mail.panic"), slog.String("kind", j.kind))
		}
	}()
	if err := j.run(ctx); err != nil {
		d.log.Error("sending email failed",
			slog.String("event", "mail.send_failed"),
			slog.String("kind", j.kind),
			slog.String("error_class", classifyError(err)))
	}
}
```

แก้ `internal/config/auth.go`:

1. เพิ่ม `"go-backend-stock/internal/core/domain"` ในบล็อก import (กลุ่มแยกตาม goimports local prefix)
2. แทน

```go
	cfg.OTPMaxAttempts, err = parseIntInRange("OTP_MAX_ATTEMPTS", getenv("OTP_MAX_ATTEMPTS"), 5, 1, 20)
```

   ด้วย

```go
	cfg.OTPMaxAttempts, err = parseIntInRange("OTP_MAX_ATTEMPTS", getenv("OTP_MAX_ATTEMPTS"), 5, 1, domain.MaxOTPFailedAttemptsCeiling)
```

3. แทน

```go
		if err != nil || prefix.Bits() == 0 {
```

   ด้วย

```go
		// An IPv4-mapped prefix (::ffff:10.0.0.0/104) never matches: client addresses are unmapped
		// before the comparison. Refuse it rather than silently trusting nothing.
		if err != nil || prefix.Bits() == 0 || prefix.Addr().Is4In6() {
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/adapter/mail internal/config; go vet ./internal/adapter/mail/ ./internal/config/ && go test ./internal/adapter/mail/ ./internal/config/ -race -count=3 && make lint`
Expected: ผ่านทั้งหมด

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/mail internal/config/auth.go internal/config/auth_test.go
git commit -m "fix(mail): keep delivered mail on QUIT errors, report cancellation and survive sender panics"
```

---

### Task 5: ชนิดข้อมูลของ auth, `Error.RetryAfter`, sentinels และ `port.Repositories`

**Files:**
- Create: `internal/core/domain/auth_input.go`, `internal/core/domain/principal.go`, `internal/core/domain/session_grant.go`, `internal/core/domain/auth_types_test.go`, `internal/core/port/repositories.go`
- Modify: `internal/core/domain/errors.go`, `internal/core/service/csrf.go`, `internal/adapter/postgres/store.go`

**Interfaces:**
- Consumes: `domain.Email`, `domain.Password`, `domain.SecretText`, `domain.OTPCode`, `domain.ID`, `domain.MaskIP`, `domain.SanitizeUserAgent`
- Produces:
  - `domain.RegisterInput{Email Email; Password Password}`, `domain.LoginInput{Email Email; Password SecretText}`, `domain.VerifyEmailInput{ChallengeID ID; OTP OTPCode}`
  - `domain.Principal{UserID, SessionID ID}` + `IsZero()`; `domain.RequestMeta{RequestID ID; ClientIP netip.Addr; MaskedIP string; UserAgent string}`; `func NewRequestMeta(requestID ID, clientIP netip.Addr, userAgent string) RequestMeta`
  - `domain.SessionGrant{User User; Token SecretText; ExpiresAt time.Time}`, `domain.OTPReceipt{ChallengeID ID; ExpiresIn time.Duration}`
  - `domain.Error.RetryAfter time.Duration`; `func NewRetryError(code ErrorCode, message string, retryAfter time.Duration) *Error`
  - sentinels `domain.ErrUnauthenticated`, `ErrInvalidCredentials`, `ErrInvalidVerification`, `ErrEmailNotVerified`, `ErrCSRFFailed` (`service.CSRFCodec` คืน `ErrCSRFFailed`)
  - `port.Repositories` — `Users()`, `Sessions()`, `OTPChallenges()`, `RateLimits()` สำหรับอ่านนอก transaction; `*postgres.Store` implement แล้ว

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/core/domain/auth_types_test.go`:

```go
package domain_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestNewRequestMeta(t *testing.T) {
	id := mustID(t)
	m := domain.NewRequestMeta(id, netip.MustParseAddr("::ffff:203.0.113.77"), "agent\r\nX-Injected: yes")
	if m.RequestID != id || m.ClientIP != netip.MustParseAddr("203.0.113.77") || m.MaskedIP != "203.0.113.0/24" {
		t.Errorf("meta = %+v, want the unmapped IP, its /24 and the request ID", m)
	}
	if strings.ContainsAny(m.UserAgent, "\r\n") {
		t.Errorf("user agent %q was not sanitized", m.UserAgent)
	}

	unknown := domain.NewRequestMeta(id, netip.Addr{}, "")
	if unknown.ClientIP.IsValid() || unknown.MaskedIP != "" {
		t.Errorf("meta without an IP = %+v, want no IP and no mask", unknown)
	}
}

func TestPrincipalIsZero(t *testing.T) {
	if !(domain.Principal{}).IsZero() || (domain.Principal{UserID: mustID(t), SessionID: mustID(t)}).IsZero() {
		t.Error("Principal.IsZero misbehaves")
	}
}

func TestSessionGrantNeverPrintsTheToken(t *testing.T) {
	g := domain.SessionGrant{Token: domain.NewSecretText("raw-session-token-ZQXJ"), ExpiresAt: time.Now()}
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		if out := fmt.Sprintf(verb, g); strings.Contains(out, "ZQXJ") {
			t.Errorf("%s leaks the token: %s", verb, out)
		}
	}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "ZQXJ") {
		t.Errorf("JSON leaks the token: %s", data)
	}
}

func TestRetryErrorAndAuthSentinels(t *testing.T) {
	e := domain.NewRetryError(domain.CodeRateLimited, "too many requests", 1500*time.Millisecond)
	if e.RetryAfter != 1500*time.Millisecond || !errors.Is(e, domain.NewError(domain.CodeRateLimited, "other text")) {
		t.Errorf("NewRetryError() = %+v", e)
	}
	if domain.NewError(domain.CodeInternal, "x").RetryAfter != 0 {
		t.Error("NewError must not set RetryAfter")
	}

	want := map[*domain.Error]domain.ErrorCode{
		domain.ErrUnauthenticated:     domain.CodeUnauthenticated,
		domain.ErrInvalidCredentials:  domain.CodeInvalidCredentials,
		domain.ErrInvalidVerification: domain.CodeInvalidVerification,
		domain.ErrEmailNotVerified:    domain.CodeEmailNotVerified,
		domain.ErrCSRFFailed:          domain.CodeCSRFFailed,
	}
	for sentinel, code := range want {
		if sentinel.Code != code || sentinel.Message == "" {
			t.Errorf("sentinel %v has code %s, want %s", sentinel, sentinel.Code, code)
		}
		if !errors.Is(fmt.Errorf("wrapped: %w", sentinel), domain.NewError(code, "any")) {
			t.Errorf("a wrapped %s must still match its code", code)
		}
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/domain/ -run 'RequestMeta|Principal|SessionGrant|RetryError' -count=1`
Expected: build fail — `undefined: domain.NewRequestMeta`, `domain.SessionGrant`, `domain.NewRetryError`

- [ ] **Step 3: เขียนโค้ด**

สร้าง `internal/core/domain/auth_input.go`:

```go
package domain

// RegisterInput is a registration request whose email is normalized and whose password passed the
// whole policy (service.PasswordPolicy).
type RegisterInput struct {
	Email    Email
	Password Password
}

// LoginInput is a login request. The password is only wrapped: login checks its length and nothing
// else, so an account whose password predates today's rules can still sign in (phase 1 spec
// section 10).
type LoginInput struct {
	Email    Email
	Password SecretText
}

// VerifyEmailInput confirms an email address with the code that was sent to it.
type VerifyEmailInput struct {
	ChallengeID ID
	OTP         OTPCode
}
```

สร้าง `internal/core/domain/principal.go`:

```go
package domain

import "net/netip"

// Principal is the caller of an authenticated request: the account and the session the request came
// with. It comes only from a verified session, never from the request body.
type Principal struct {
	UserID    ID
	SessionID ID
}

// IsZero reports whether p is the zero value (no authenticated caller).
func (p Principal) IsZero() bool { return p.UserID.IsZero() }

// RequestMeta is what a use case may know about the request beyond its input. ClientIP is the full
// address and is used for rate limiting only; audit metadata stores MaskedIP.
type RequestMeta struct {
	RequestID ID
	ClientIP  netip.Addr
	MaskedIP  string
	UserAgent string
}

// NewRequestMeta builds the metadata of one request: the client IP is unmapped and masked and the
// user agent is sanitized for storage.
func NewRequestMeta(requestID ID, clientIP netip.Addr, userAgent string) RequestMeta {
	if clientIP.IsValid() {
		clientIP = clientIP.Unmap().WithZone("")
	}
	return RequestMeta{
		RequestID: requestID,
		ClientIP:  clientIP,
		MaskedIP:  MaskIP(clientIP),
		UserAgent: SanitizeUserAgent(userAgent),
	}
}
```

สร้าง `internal/core/domain/session_grant.go`:

```go
package domain

import "time"

// SessionGrant is the result of a successful login. Token is the raw value for the session cookie;
// only the HTTP adapter reads it, to set the cookie.
type SessionGrant struct {
	User      User
	Token     SecretText
	ExpiresAt time.Time
}

// OTPReceipt is the answer to register and to "send the code again". It has the same shape whether
// or not a code was sent, so it does not reveal whether the email has an account.
type OTPReceipt struct {
	ChallengeID ID
	ExpiresIn   time.Duration
}
```

แก้ `internal/core/domain/errors.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
package domain

// ErrorCode is a stable machine-readable code shared with the API contract (spec section 11).
```

   ด้วย

```go
package domain

import "time"

// ErrorCode is a stable machine-readable code shared with the API contract (spec section 11).
```

2. แทน

```go
	Reasons []string
}
```

   ด้วย

```go
	Reasons []string
	// RetryAfter is how long the client should wait before trying again. It is set on rate_limited
	// errors; the HTTP adapter sends it as the Retry-After header.
	RetryAfter time.Duration
}
```

3. แทน

```go
func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }
```

   ด้วย

```go
// NewRetryError returns an *Error that tells the client how long to wait, such as rate_limited.
func NewRetryError(code ErrorCode, message string, retryAfter time.Duration) *Error {
	return &Error{Code: code, Message: message, RetryAfter: retryAfter}
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }
```

4. แทน

```go
	ErrInternal              = NewError(CodeInternal, "internal error")
)
```

   ด้วย

```go
	ErrInternal              = NewError(CodeInternal, "internal error")

	// Auth outcomes. Their messages are shown to clients, so they never say which check failed.
	ErrUnauthenticated     = NewError(CodeUnauthenticated, "authentication required")
	ErrInvalidCredentials  = NewError(CodeInvalidCredentials, "email or password is incorrect")
	ErrInvalidVerification = NewError(CodeInvalidVerification, "verification code is invalid or has expired")
	ErrEmailNotVerified    = NewError(CodeEmailNotVerified, "email address is not verified; ask for a new verification code")
	ErrCSRFFailed          = NewError(CodeCSRFFailed, "CSRF token is missing, invalid or expired")
)
```

แก้ `internal/core/service/csrf.go` — แทน

```go
func csrfFailed() error {
	return domain.NewError(domain.CodeCSRFFailed, "CSRF token is missing, invalid or expired")
}
```

ด้วย

```go
func csrfFailed() error { return domain.ErrCSRFFailed }
```

สร้าง `internal/core/port/repositories.go`:

```go
package port

// Repositories are the repositories a use case reads outside a transaction (one statement each, no
// row locks), plus the rate limiter, whose Admit runs its own transaction. Writes that must commit
// with their audit event go through UnitOfWork instead. Accessors are added when a use case needs
// them.
type Repositories interface {
	Users() UserRepository
	Sessions() SessionRepository
	OTPChallenges() OTPChallengeRepository
	RateLimits() RateLimitRepository
}
```

แก้ `internal/adapter/postgres/store.go` — แทน

```go
var (
	_ port.UnitOfWork     = (*Store)(nil)
	_ port.TxRepositories = (*txRepositories)(nil)
)
```

ด้วย

```go
var (
	_ port.UnitOfWork     = (*Store)(nil)
	_ port.Repositories   = (*Store)(nil)
	_ port.TxRepositories = (*txRepositories)(nil)
)
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal; go vet ./internal/... && go test ./internal/... -count=1 -race && make lint`
Expected: ผ่านทั้งหมด (`TestCSRFVerifyRejectsEverythingElseWithOneMessage` ยังได้ข้อความเดียว)

- [ ] **Step 5: Commit**

```bash
git add internal/core/domain internal/core/port/repositories.go internal/core/service/csrf.go internal/adapter/postgres/store.go
git commit -m "feat(domain): add auth inputs, principal, request meta, retry errors and the Repositories port"
```

---

### Task 6: แปลง domain error เป็น HTTP และตอบ 405 พร้อม `Allow`

**Files:**
- Modify: `internal/adapter/http/errors.go`, `internal/adapter/http/server.go`, `internal/adapter/http/middleware/notfound.go`
- Create: `internal/adapter/http/errors_mapping_test.go`, `internal/adapter/http/method_not_allowed_test.go`

**Interfaces:**
- Consumes: `domain.Error` (`Code`, `Message`, `Reasons`, `RetryAfter`), sentinels จาก Task 5, `newTestServer`, `doRequest`, `decodeError`, `newTestLogger` (helpers เดิม)
- Produces:
  - `ErrorBody.Details []string` (`json:"details,omitempty"`)
  - ErrorHandler แปลง `*domain.Error` ใน chain ของ error ตามตาราง Global Constraints; ตั้ง `Retry-After` (วินาที ปัดขึ้น ขั้นต่ำ 1) สำหรับ `rate_limited` และ `server_busy` (ค่าเริ่มต้น 1); code 500 ไม่ส่ง message ภายในออกไป
  - `middleware.NotFound(allowed func(path string) []string) fiber.Handler` — path ที่มีอยู่แต่ method ไม่ตรง → 405 `method_not_allowed` + `Allow` (ปิดข้อส่งต่อจาก milestone 1)

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/adapter/http/errors_mapping_test.go`:

```go
package http

import (
	"errors"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
)

func TestErrorHandlerMapsDomainErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		status     int
		code       string
		message    string // "" = the domain message
		retryAfter string
		details    []string
	}{
		{"invalid input lists reasons", domain.NewReasonsError(domain.CodeInvalidInput, "password does not meet the requirements", "password_too_short", "password_missing_special"),
			400, "invalid_input", "", "", []string{"password_too_short", "password_missing_special"}},
		{"malformed json", domain.NewError(domain.CodeMalformedJSON, "request body is not valid"), 400, "malformed_json", "", "", nil},
		{"unauthenticated", domain.ErrUnauthenticated, 401, "unauthenticated", "", "", nil},
		{"invalid credentials", domain.ErrInvalidCredentials, 401, "invalid_credentials", "", "", nil},
		{"invalid verification", domain.ErrInvalidVerification, 400, "invalid_verification", "", "", nil},
		{"email not verified", domain.ErrEmailNotVerified, 403, "email_not_verified", "", "", nil},
		{"csrf failed", domain.ErrCSRFFailed, 403, "csrf_failed", "", "", nil},
		{"not found", domain.ErrNotFound, 404, "not_found", "", "", nil},
		{"identity conflict", domain.ErrIdentityConflict, 409, "identity_conflict", "", "", nil},
		{"rate limited rounds up", domain.NewRetryError(domain.CodeRateLimited, "too many requests", 1500*time.Millisecond), 429, "rate_limited", "", "2", nil},
		{"rate limited waits at least a second", domain.NewRetryError(domain.CodeRateLimited, "too many requests", 10*time.Millisecond), 429, "rate_limited", "", "1", nil},
		{"server busy", domain.NewError(domain.CodeServerBusy, "server is busy, try again shortly"), 503, "server_busy", "", "1", nil},
		{"dependency unavailable", domain.NewError(domain.CodeDependencyUnavailable, "session store is unavailable"), 503, "dependency_unavailable", "", "", nil},
		{"internal hides its message", domain.NewError(domain.CodeInternal, "stored password hash is malformed"), 500, "internal", "Internal server error", "", nil},
		{"audit write failure hides its cause", fmt.Errorf("%w: %w", domain.NewError(domain.CodeAuditWriteFailed, "could not record the audit event"), errors.New("connection reset")),
			500, "audit_write_failed", "Internal server error", "", nil},
		{"wrapped domain error", fmt.Errorf("login: %w", domain.ErrEmailNotVerified), 403, "email_not_verified", "", "", nil},
		{"unknown code is internal", domain.NewError("made_up", "secret detail"), 500, "internal", "Internal server error", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, logs := newTestLogger()
			app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler(log)})
			app.Use(middleware.RequestID())
			app.Get("/x", func(fiber.Ctx) error { return tt.err })

			resp, body := doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/x", nil))
			got := decodeError(t, body)
			wantMessage := tt.message
			if wantMessage == "" {
				var de *domain.Error
				errors.As(tt.err, &de)
				wantMessage = de.Message
			}
			if resp.StatusCode != tt.status || got.Code != tt.code || got.Message != wantMessage {
				t.Errorf("got %d %s %q, want %d %s %q", resp.StatusCode, got.Code, got.Message, tt.status, tt.code, wantMessage)
			}
			if resp.Header.Get("Retry-After") != tt.retryAfter {
				t.Errorf("Retry-After = %q, want %q", resp.Header.Get("Retry-After"), tt.retryAfter)
			}
			if !slices.Equal(got.Details, tt.details) {
				t.Errorf("details = %v, want %v", got.Details, tt.details)
			}
			if got.RequestID == "" || got.RequestID != resp.Header.Get(middleware.HeaderRequestID) {
				t.Errorf("request_id %q must match the X-Request-ID header", got.RequestID)
			}
			if tt.status >= 500 && !strings.Contains(logs.String(), `"event":"http.error"`) {
				t.Errorf("a 5xx must be logged server-side, log = %s", logs.String())
			}
		})
	}
}

func TestErrorBodyOmitsEmptyDetails(t *testing.T) {
	log, _ := newTestLogger()
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler(log)})
	app.Get("/x", func(fiber.Ctx) error { return domain.ErrNotFound })
	_, body := doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/x", nil))
	if strings.Contains(string(body), "details") {
		t.Errorf("body %s must not carry an empty details field", body)
	}
}
```

สร้าง `internal/adapter/http/method_not_allowed_test.go`:

```go
package http

import (
	nethttp "net/http"
	"net/http/httptest"
	"testing"
)

func TestKnownPathWithAnotherMethodIs405WithAllow(t *testing.T) {
	app, logs := newTestServer(t, fakeReadiness{})

	resp, body := doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodPost, "/healthz", nil))
	if resp.StatusCode != nethttp.StatusMethodNotAllowed {
		t.Fatalf("POST /healthz = %d, want 405", resp.StatusCode)
	}
	if got := resp.Header.Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
	}
	if code := decodeError(t, body).Code; code != "method_not_allowed" {
		t.Errorf("code = %q, want method_not_allowed", code)
	}
	for _, e := range accessEntries(t, logs) {
		if e["route"] != "unmatched" {
			t.Errorf("access log route = %v, want unmatched (never the raw path)", e["route"])
		}
	}

	resp, _ = doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/no-such-path", nil))
	if resp.StatusCode != nethttp.StatusNotFound || resp.Header.Get("Allow") != "" {
		t.Errorf("unknown path = %d Allow=%q, want 404 without Allow", resp.StatusCode, resp.Header.Get("Allow"))
	}

	resp, _ = doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodHead, "/healthz", nil))
	if resp.StatusCode != nethttp.StatusOK {
		t.Errorf("HEAD /healthz = %d, want 200", resp.StatusCode)
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/adapter/http/ -run 'ErrorHandlerMaps|ErrorBodyOmits|405' -count=1`
Expected: FAIL — domain errors ตอบ 500, `got.Details` undefined, POST /healthz ตอบ 404

- [ ] **Step 3: เขียนโค้ด**

แทนเนื้อหาทั้งไฟล์ `internal/adapter/http/errors.go` ด้วย:

```go
// Package http is the primary adapter: Fiber server, middleware chain, handlers and DTOs.
package http

import (
	"errors"
	"log/slog"
	"math"
	nethttp "net/http"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
)

// APIError is an HTTP-level error with a stable machine-readable code.
type APIError struct {
	Status     int
	Code       string
	Message    string
	Details    []string
	RetryAfter time.Duration
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// ErrorResponse is the single error envelope used by every endpoint.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody carries the error code, a human-readable message and the request ID.
type ErrorBody struct {
	Code      string `json:"code" example:"not_found"`
	Message   string `json:"message" example:"Not Found"`
	RequestID string `json:"request_id" example:"0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d"`
	// Details lists the rules an input broke, such as password_too_short; only on invalid_input.
	Details []string `json:"details,omitempty" example:"password_too_short,password_missing_special"`
}

// domainStatus maps every domain error code to its HTTP status (phase 1 spec section 11).
var domainStatus = map[domain.ErrorCode]int{
	domain.CodeInvalidInput:          nethttp.StatusBadRequest,
	domain.CodeMalformedJSON:         nethttp.StatusBadRequest,
	domain.CodeUnauthenticated:       nethttp.StatusUnauthorized,
	domain.CodeInvalidCredentials:    nethttp.StatusUnauthorized,
	domain.CodeInvalidReset:          nethttp.StatusBadRequest,
	domain.CodeInvalidVerification:   nethttp.StatusBadRequest,
	domain.CodeCSRFFailed:            nethttp.StatusForbidden,
	domain.CodeAccessDenied:          nethttp.StatusForbidden,
	domain.CodeEmailNotVerified:      nethttp.StatusForbidden,
	domain.CodeVersionConflict:       nethttp.StatusPreconditionFailed,
	domain.CodePreconditionRequired:  nethttp.StatusPreconditionRequired,
	domain.CodeNotFound:              nethttp.StatusNotFound,
	domain.CodeIdentityConflict:      nethttp.StatusConflict,
	domain.CodeIdempotencyConflict:   nethttp.StatusConflict,
	domain.CodePortfolioNotEmpty:     nethttp.StatusConflict,
	domain.CodePortfolioNameConflict: nethttp.StatusConflict,
	domain.CodeInsufficientShares:    nethttp.StatusUnprocessableEntity,
	domain.CodeInvalidHistory:        nethttp.StatusUnprocessableEntity,
	domain.CodeUnknownSymbol:         nethttp.StatusUnprocessableEntity,
	domain.CodeRateLimited:           nethttp.StatusTooManyRequests,
	domain.CodeDependencyUnavailable: nethttp.StatusServiceUnavailable,
	domain.CodeFeatureDisabled:       nethttp.StatusServiceUnavailable,
	domain.CodeServerBusy:            nethttp.StatusServiceUnavailable,
	domain.CodeInternal:              nethttp.StatusInternalServerError,
	domain.CodeAuditWriteFailed:      nethttp.StatusInternalServerError,
}

// ErrorHandler renders every error as ErrorResponse. Server errors are logged with the
// request ID; their details never reach the client.
func ErrorHandler(log *slog.Logger) fiber.ErrorHandler {
	return func(c fiber.Ctx, err error) error {
		apiErr := toAPIError(err)

		// Errors raised before the middleware chain (e.g. body too large) have no ID yet.
		requestID := middleware.GetRequestID(c)
		if requestID == "" {
			if id, idErr := middleware.NewRequestID(); idErr == nil {
				middleware.SetRequestID(c, id)
				requestID = id
			}
		}

		if apiErr.Status >= nethttp.StatusInternalServerError {
			log.Error("request failed",
				slog.String("event", "http.error"),
				slog.String("request_id", requestID),
				slog.String("error_code", apiErr.Code),
				slog.String("error", err.Error()),
			)
		}

		c.Set("Cache-Control", "no-store")
		c.Set("X-Content-Type-Options", "nosniff")
		if apiErr.RetryAfter > 0 {
			c.Set(fiber.HeaderRetryAfter, strconv.Itoa(retryAfterSeconds(apiErr.RetryAfter)))
		}
		return c.Status(apiErr.Status).JSON(ErrorResponse{Error: ErrorBody{
			Code:      apiErr.Code,
			Message:   apiErr.Message,
			RequestID: requestID,
			Details:   apiErr.Details,
		}})
	}
}

func toAPIError(err error) *APIError {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		return fromDomain(domainErr)
	}
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		return &APIError{Status: fiberErr.Code, Code: codeForStatus(fiberErr.Code), Message: nethttp.StatusText(fiberErr.Code)}
	}
	return internalError()
}

// fromDomain maps a domain error. 500s keep their code but never their message, which may name
// stored data; every other message is written for clients (domain.Error contract).
func fromDomain(e *domain.Error) *APIError {
	status, ok := domainStatus[e.Code]
	if !ok {
		return internalError()
	}
	if status == nethttp.StatusInternalServerError {
		return &APIError{Status: status, Code: string(e.Code), Message: "Internal server error"}
	}
	api := &APIError{Status: status, Code: string(e.Code), Message: e.Message, Details: e.Reasons, RetryAfter: e.RetryAfter}
	if e.Code == domain.CodeServerBusy && api.RetryAfter <= 0 {
		api.RetryAfter = time.Second
	}
	return api
}

func internalError() *APIError {
	return &APIError{Status: nethttp.StatusInternalServerError, Code: "internal", Message: "Internal server error"}
}

// retryAfterSeconds rounds up to whole seconds, at least one, so a client never retries too early.
func retryAfterSeconds(d time.Duration) int {
	s := int(math.Ceil(d.Seconds()))
	if s < 1 {
		return 1
	}
	return s
}

func codeForStatus(status int) string {
	switch status {
	case nethttp.StatusNotFound:
		return "not_found"
	case nethttp.StatusMethodNotAllowed:
		return "method_not_allowed"
	case nethttp.StatusRequestTimeout:
		return "request_timeout"
	case nethttp.StatusRequestEntityTooLarge:
		return "payload_too_large"
	case nethttp.StatusTooManyRequests:
		return "rate_limited"
	}
	if status >= nethttp.StatusInternalServerError {
		return "internal"
	}
	return "invalid_input"
}
```

แทนเนื้อหาทั้งไฟล์ `internal/adapter/http/middleware/notfound.go` ด้วย:

```go
package middleware

import (
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// NotFound must be registered last. It marks the route as "unmatched" so the access log never
// records the raw path of an unknown URL. When the path exists with other methods (allowed returns
// them) it answers 405 with an Allow header instead of 404.
func NotFound(allowed func(path string) []string) fiber.Handler {
	return func(c fiber.Ctx) error {
		MarkRoute(c, "unmatched")
		if allowed != nil {
			methods := allowed(c.Path())
			// A request whose method is listed is a route Fiber did not match (for example its
			// automatic HEAD route, added after this catch-all): that is a 404, not a 405.
			if len(methods) > 0 && !slices.Contains(methods, c.Method()) {
				c.Set(fiber.HeaderAllow, strings.Join(methods, ", "))
				return fiber.ErrMethodNotAllowed
			}
		}
		return fiber.ErrNotFound
	}
}
```

แก้ `internal/adapter/http/server.go` ตามคู่ต่อไปนี้:

1. แทน

```go
import (
	"errors"
	"log/slog"
	"net"
	"time"
```

   ด้วย

```go
import (
	"errors"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"time"
```

2. แทน

```go
	app.Use(middleware.NotFound())
	return app
}
```

   ด้วย

```go
	app.Use(middleware.NotFound(staticRouteMethods(app)))
	return app
}

// staticRouteMethods returns, for a registered path without parameters, the methods it accepts.
// The table is built on first use, when every route has been registered.
func staticRouteMethods(app *fiber.App) func(path string) []string {
	var once sync.Once
	var table map[string][]string
	return func(path string) []string {
		once.Do(func() {
			table = map[string][]string{}
			for _, r := range app.GetRoutes(true) {
				if strings.ContainsAny(r.Path, ":*+") || slices.Contains(table[r.Path], r.Method) {
					continue
				}
				table[r.Path] = append(table[r.Path], r.Method)
			}
			for path := range table {
				slices.Sort(table[path])
			}
		})
		return table[path]
	}
}
```

ถ้ามี test เดิมที่คาดว่า method ที่ไม่ตรง (เช่น `POST /healthz`) ได้ 404 ให้เปลี่ยนให้คาด 405 + `Allow` ตามข้อส่งต่อจาก milestone 1 และระบุใน report

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/adapter/http; go vet ./internal/adapter/http/... && go test ./internal/adapter/http/... -count=1 -race && make lint`
Expected: ผ่านทั้งหมด

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/http/errors.go internal/adapter/http/server.go internal/adapter/http/middleware/notfound.go internal/adapter/http/errors_mapping_test.go internal/adapter/http/method_not_allowed_test.go
git commit -m "feat(http): map domain errors to status codes and answer 405 with Allow"
```

---

### Task 7: `service.Auth`, `Authenticate`, audit และ rate-limit helpers

**Files:**
- Create: `internal/core/port/auth_service.go`, `internal/core/service/auth.go`, `internal/core/service/audit.go`, `internal/core/service/admission.go`, `internal/core/service/fakes_test.go`, `internal/core/service/auth_test.go`

**Interfaces:**
- Consumes: `port.Repositories`, `port.UnitOfWork`, `port.TxRepositories`, `port.Clock`, `port.Mailer`; `HashSessionToken`, `NowForStorage`, `PasswordHasher`, `OTPCodec`, `RateLimitPolicy`, `Pacer`; `domain.NewAuditEvent`, `domain.RateLimitDecision.Scope` (Task 2), `domain.RequestMeta`/`Principal`/sentinels (Task 5); test helpers `newHasher`, `testArgon2Params`, `newTestPolicy`, `newFakeClock`, `mustID`, `requireCode`
- Produces:
  - `port.AuthService` (ตอนนี้มีเฉพาะ `Authenticate(ctx, token string) (domain.Principal, error)`; Task 9 และ 11 เพิ่ม method)
  - `service.AuthDeps{Store port.Repositories; UnitOfWork port.UnitOfWork; Clock port.Clock; Hasher *PasswordHasher; OTP *OTPCodec; Limits *RateLimitPolicy; Pacer *Pacer; Mailer port.Mailer; Logger *slog.Logger; SessionTTL time.Duration; OTPMaxAttempts int}`; `func NewAuth(d AuthDeps) (*Auth, error)`
  - `(*Auth).Authenticate` — token ผิดรูป/ไม่รู้จัก/หมดอายุ (`now >= expires_at`)/ถูก revoke → `domain.ErrUnauthenticated` โดยไม่ query เมื่อ token ผิดรูป; store ล้ม → `dependency_unavailable`
  - helpers ภายใน package: `auditSpec`, `(*Auth).appendAudit(ctx, tx, meta, spec) error` (ล้ม → `audit_write_failed`), `(*Auth).recordFailure(ctx, meta, spec) error` (transaction ของตัวเอง, log `audit.write_failed` เมื่อเขียนไม่ได้), `(*Auth).admit(ctx, rules, meta, actor) error`
  - metadata ของ audit เป็น JSON object ที่มีเฉพาะ `ip`, `user_agent`, `method`, `scope` (ว่างแล้วไม่ใส่)

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/core/service/fakes_test.go`:

```go
package service

import (
	"bytes"
	"context"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// The fakes embed the port interface they stand in for: a method a test does not override panics
// when called, which shows at once that a use case touched something unexpected.

type fakeRepositories struct {
	port.Repositories
	sessions *fakeSessions
	limits   *fakeRateLimits
	users    *fakeUsers
}

func (r *fakeRepositories) Sessions() port.SessionRepository    { return r.sessions }
func (r *fakeRepositories) RateLimits() port.RateLimitRepository { return r.limits }
func (r *fakeRepositories) Users() port.UserRepository          { return r.users }

type fakeSessions struct {
	port.SessionRepository
	byHash map[string]domain.Session
	err    error
	calls  int
}

func (f *fakeSessions) ByTokenHash(_ context.Context, hash []byte) (domain.Session, error) {
	f.calls++
	if f.err != nil {
		return domain.Session{}, f.err
	}
	s, ok := f.byHash[string(hash)]
	if !ok {
		return domain.Session{}, domain.ErrNotFound
	}
	return s, nil
}

type fakeUsers struct {
	port.UserRepository
	credentials map[string]domain.UserCredentials
}

func (f *fakeUsers) CredentialsByEmail(_ context.Context, email domain.Email) (domain.UserCredentials, error) {
	c, ok := f.credentials[email.String()]
	if !ok {
		return domain.UserCredentials{}, domain.ErrNotFound
	}
	return c, nil
}

type fakeRateLimits struct {
	decision domain.RateLimitDecision
	err      error
	rules    []domain.RateLimitRule
	at       time.Time
}

func (f *fakeRateLimits) Admit(_ context.Context, rules []domain.RateLimitRule, now time.Time) (domain.RateLimitDecision, error) {
	f.rules, f.at = rules, now
	return f.decision, f.err
}

type fakeAudit struct {
	events []domain.AuditEvent
	err    error
}

func (a *fakeAudit) Append(_ context.Context, e domain.AuditEvent) error {
	if a.err != nil {
		return a.err
	}
	a.events = append(a.events, e)
	return nil
}

type fakeTx struct {
	port.TxRepositories
	audit *fakeAudit
}

func (t fakeTx) Audit() port.AuditRepository { return t.audit }

type fakeUoW struct {
	audit *fakeAudit
	runs  int
}

func (u *fakeUoW) WithinTransaction(_ context.Context, fn func(port.TxRepositories) error) error {
	u.runs++
	return fn(fakeTx{audit: u.audit})
}

type fakeMailer struct{ port.Mailer }

type testAuth struct {
	auth  *Auth
	repos *fakeRepositories
	uow   *fakeUoW
	clock *fakeClock
	logs  *bytes.Buffer
}

func newTestAuth(t *testing.T, mutate func(*AuthDeps)) testAuth {
	t.Helper()
	clock := newFakeClock()
	repos := &fakeRepositories{
		sessions: &fakeSessions{byHash: map[string]domain.Session{}},
		limits:   &fakeRateLimits{decision: domain.RateLimitDecision{Allowed: true}},
		users:    &fakeUsers{credentials: map[string]domain.UserCredentials{}},
	}
	uow := &fakeUoW{audit: &fakeAudit{}}
	hasher, _ := newHasher(t, testArgon2Params, 2, 0)
	otp, err := NewOTPCodec(bytes.Repeat([]byte("o"), 32))
	if err != nil {
		t.Fatal(err)
	}
	pacer, err := NewPacer(clock, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	deps := AuthDeps{
		Store: repos, UnitOfWork: uow, Clock: clock, Hasher: hasher, OTP: otp, Limits: newTestPolicy(t),
		Pacer: pacer, Mailer: fakeMailer{}, Logger: slog.New(slog.NewJSONHandler(logs, nil)),
		SessionTTL: 7 * 24 * time.Hour, OTPMaxAttempts: 5,
	}
	if mutate != nil {
		mutate(&deps)
	}
	auth, err := NewAuth(deps)
	if err != nil {
		t.Fatalf("NewAuth() error = %v", err)
	}
	return testAuth{auth: auth, repos: repos, uow: uow, clock: clock, logs: logs}
}

func testMeta(t *testing.T) domain.RequestMeta {
	t.Helper()
	return domain.NewRequestMeta(mustID(t), netip.MustParseAddr("203.0.113.9"), "test-agent/1.0")
}
```

สร้าง `internal/core/service/auth_test.go`:

```go
package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestNewAuthValidatesDependencies(t *testing.T) {
	for name, mutate := range map[string]func(*AuthDeps){
		"no store":           func(d *AuthDeps) { d.Store = nil },
		"no unit of work":    func(d *AuthDeps) { d.UnitOfWork = nil },
		"no clock":           func(d *AuthDeps) { d.Clock = nil },
		"no hasher":          func(d *AuthDeps) { d.Hasher = nil },
		"no otp codec":       func(d *AuthDeps) { d.OTP = nil },
		"no rate limits":     func(d *AuthDeps) { d.Limits = nil },
		"no pacer":           func(d *AuthDeps) { d.Pacer = nil },
		"no mailer":          func(d *AuthDeps) { d.Mailer = nil },
		"no logger":          func(d *AuthDeps) { d.Logger = nil },
		"no session ttl":     func(d *AuthDeps) { d.SessionTTL = 0 },
		"no otp attempts":    func(d *AuthDeps) { d.OTPMaxAttempts = 0 },
		"too many attempts":  func(d *AuthDeps) { d.OTPMaxAttempts = domain.MaxOTPFailedAttemptsCeiling + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			deps := validAuthDeps(t)
			mutate(&deps)
			if _, err := NewAuth(deps); err == nil {
				t.Error("NewAuth() accepted the dependencies")
			}
		})
	}
}

// validAuthDeps returns the dependencies newTestAuth uses, for tests that change one of them.
func validAuthDeps(t *testing.T) AuthDeps {
	t.Helper()
	var deps AuthDeps
	newTestAuth(t, func(d *AuthDeps) { deps = *d })
	return deps
}

func TestAuthenticate(t *testing.T) {
	ta := newTestAuth(t, nil)
	token, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	now := NowForStorage(ta.clock)
	session := domain.Session{ID: mustID(t), UserID: mustID(t), TokenHash: hash, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	ta.repos.sessions.byHash[string(hash)] = session

	p, err := ta.auth.Authenticate(t.Context(), token)
	if err != nil || p.UserID != session.UserID || p.SessionID != session.ID {
		t.Fatalf("Authenticate(valid) = %+v, %v", p, err)
	}

	calls := ta.repos.sessions.calls
	_, err = ta.auth.Authenticate(t.Context(), "not-a-token")
	requireCode(t, err, domain.CodeUnauthenticated)
	if ta.repos.sessions.calls != calls {
		t.Error("a malformed token must be refused without a database query")
	}

	other, _, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	_, err = ta.auth.Authenticate(t.Context(), other)
	requireCode(t, err, domain.CodeUnauthenticated)

	ta.clock.Advance(time.Hour) // exactly at expires_at the session is over
	_, err = ta.auth.Authenticate(t.Context(), token)
	requireCode(t, err, domain.CodeUnauthenticated)

	revokedAt := now
	session.RevokedAt, session.RevokeReason = &revokedAt, domain.RevokeReasonLogout
	session.ExpiresAt = now.Add(48 * time.Hour)
	ta.repos.sessions.byHash[string(hash)] = session
	_, err = ta.auth.Authenticate(t.Context(), token)
	requireCode(t, err, domain.CodeUnauthenticated)

	ta.repos.sessions.err = errors.New("connection refused")
	_, err = ta.auth.Authenticate(t.Context(), token)
	requireCode(t, err, domain.CodeDependencyUnavailable)
}

func TestAdmitPassesTheRulesAndStorageTime(t *testing.T) {
	ta := newTestAuth(t, nil)
	ta.clock.now = ta.clock.now.Add(123456789 * time.Nanosecond)
	rules, err := ta.auth.limits.EmailVerificationConfirm(testMeta(t).ClientIP)
	if err != nil {
		t.Fatal(err)
	}
	if err := ta.auth.admit(t.Context(), rules, testMeta(t), domain.ID{}); err != nil {
		t.Fatalf("admit() error = %v", err)
	}
	if len(ta.repos.limits.rules) != len(rules) || !ta.repos.limits.at.Equal(NowForStorage(ta.clock)) {
		t.Errorf("Admit got %d rules at %v, want %d at %v", len(ta.repos.limits.rules), ta.repos.limits.at, len(rules), NowForStorage(ta.clock))
	}
	if len(ta.uow.audit.events) != 0 {
		t.Error("an allowed request must not write audit")
	}
}

func TestAdmitDenialReturnsRetryAfterAndAuditsWhenAsked(t *testing.T) {
	ta := newTestAuth(t, nil)
	actor := mustID(t)
	meta := testMeta(t)
	ta.repos.limits.decision = domain.RateLimitDecision{RetryAfter: 90 * time.Second, RecordDenialAudit: true, Scope: domain.RateLimitLoginEmail}

	err := ta.auth.admit(t.Context(), nil, meta, actor)
	de := requireCode(t, err, domain.CodeRateLimited)
	if de.RetryAfter != 90*time.Second {
		t.Errorf("RetryAfter = %v, want 90s", de.RetryAfter)
	}
	if len(ta.uow.audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(ta.uow.audit.events))
	}
	e := ta.uow.audit.events[0]
	if e.EventType != domain.EventSecurityRateLimited || e.Outcome != domain.AuditOutcomeDenied || e.ActorUserID != actor ||
		e.RequestID != meta.RequestID || e.TargetType != "rate_limit" || e.TargetID != string(domain.RateLimitLoginEmail) {
		t.Errorf("audit event = %+v", e)
	}
	var md map[string]string
	if err := json.Unmarshal(e.Metadata, &md); err != nil || md["scope"] != "login_email" {
		t.Errorf("metadata %s must name the scope", e.Metadata)
	}

	ta.repos.limits.decision.RecordDenialAudit = false
	_ = ta.auth.admit(t.Context(), nil, meta, actor)
	if len(ta.uow.audit.events) != 1 {
		t.Error("a denial that is not flagged for audit must not write one (one per key per minute)")
	}
}

func TestAuditMetadataKeepsOnlyTheAllowlist(t *testing.T) {
	ta := newTestAuth(t, nil)
	meta := testMeta(t)
	e, err := ta.auth.newAuditEvent(meta, auditSpec{
		Type: domain.EventAuthLoginFailed, Outcome: domain.AuditOutcomeFailure, TargetType: "login",
		Reason: "invalid_credentials", Method: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	var md map[string]any
	if err := json.Unmarshal(e.Metadata, &md); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"ip": "203.0.113.0/24", "user_agent": "test-agent/1.0", "method": "password"}
	if len(md) != len(want) {
		t.Errorf("metadata = %v, want exactly %v", md, want)
	}
	for k, v := range want {
		if md[k] != v {
			t.Errorf("metadata[%s] = %v, want %v", k, md[k], v)
		}
	}
	if strings.Contains(string(e.Metadata), "203.0.113.9") {
		t.Error("metadata must hold the masked IP, never the full one")
	}
	if !e.ActorUserID.IsZero() || e.ReasonCode != "invalid_credentials" || !e.OccurredAt.Equal(NowForStorage(ta.clock)) {
		t.Errorf("event = %+v", e)
	}
}

func TestRecordFailureLogsWhenTheAuditCannotBeWritten(t *testing.T) {
	ta := newTestAuth(t, nil)
	ta.uow.audit.err = errors.New("disk full")
	err := ta.auth.recordFailure(t.Context(), testMeta(t), auditSpec{
		Type: domain.EventAuthLoginFailed, Outcome: domain.AuditOutcomeFailure, TargetType: "login",
	})
	requireCode(t, err, domain.CodeAuditWriteFailed)
	if !strings.Contains(ta.logs.String(), `"event":"audit.write_failed"`) || !strings.Contains(ta.logs.String(), `"level":"ERROR"`) {
		t.Errorf("log %q should record audit.write_failed at error level", ta.logs.String())
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/service/ -run 'NewAuth|Authenticate|Admit|AuditMetadata|RecordFailure' -count=1`
Expected: build fail — `undefined: NewAuth`, `AuthDeps`, `auditSpec`

- [ ] **Step 3: เขียนโค้ด**

สร้าง `internal/core/port/auth_service.go`:

```go
package port

import (
	"context"

	"go-backend-stock/internal/core/domain"
)

// AuthService is the inbound port of the auth use cases for the HTTP adapter. Methods are added as
// each use case is built (milestone 4 design section 3); Google sign-in arrives in plan 4c.
type AuthService interface {
	// Authenticate resolves a raw session cookie. An unknown, malformed, expired or revoked token
	// returns domain.ErrUnauthenticated; a failing session store returns dependency_unavailable.
	Authenticate(ctx context.Context, token string) (domain.Principal, error)
}
```

สร้าง `internal/core/service/auth.go`:

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// AuthDeps are the dependencies of NewAuth, assembled in cmd/api.
type AuthDeps struct {
	Store          port.Repositories // reads outside a transaction and the rate limiter
	UnitOfWork     port.UnitOfWork   // writes that commit with their audit event
	Clock          port.Clock
	Hasher         *PasswordHasher
	OTP            *OTPCodec
	Limits         *RateLimitPolicy
	Pacer          *Pacer
	Mailer         port.Mailer
	Logger         *slog.Logger
	SessionTTL     time.Duration
	OTPMaxAttempts int
}

// Auth implements the auth use cases (port.AuthService and, from plan 4b task 10,
// port.EmailVerificationService). Safe for concurrent use.
type Auth struct {
	store          port.Repositories
	uow            port.UnitOfWork
	clock          port.Clock
	hasher         *PasswordHasher
	otp            *OTPCodec
	limits         *RateLimitPolicy
	pacer          *Pacer
	mailer         port.Mailer
	log            *slog.Logger
	sessionTTL     time.Duration
	otpMaxAttempts int
}

var _ port.AuthService = (*Auth)(nil)

// NewAuth checks that every dependency is present.
func NewAuth(d AuthDeps) (*Auth, error) {
	switch {
	case d.Store == nil, d.UnitOfWork == nil, d.Clock == nil, d.Hasher == nil, d.OTP == nil,
		d.Limits == nil, d.Pacer == nil, d.Mailer == nil, d.Logger == nil:
		return nil, errors.New("auth service is missing a dependency")
	case d.SessionTTL <= 0:
		return nil, errors.New("session lifetime must be positive")
	case d.OTPMaxAttempts < 1 || d.OTPMaxAttempts > domain.MaxOTPFailedAttemptsCeiling:
		return nil, fmt.Errorf("OTP attempts must be between 1 and %d", domain.MaxOTPFailedAttemptsCeiling)
	}
	return &Auth{
		store: d.Store, uow: d.UnitOfWork, clock: d.Clock, hasher: d.Hasher, otp: d.OTP,
		limits: d.Limits, pacer: d.Pacer, mailer: d.Mailer, log: d.Logger,
		sessionTTL: d.SessionTTL, otpMaxAttempts: d.OTPMaxAttempts,
	}, nil
}

// Authenticate implements port.AuthService.
func (a *Auth) Authenticate(ctx context.Context, token string) (domain.Principal, error) {
	hash, ok := HashSessionToken(token)
	if !ok {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	s, err := a.store.Sessions().ByTokenHash(ctx, hash)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return domain.Principal{}, domain.ErrUnauthenticated
	case err != nil:
		return domain.Principal{}, sessionStoreUnavailable(err)
	}
	if s.RevokedAt != nil || !NowForStorage(a.clock).Before(s.ExpiresAt) {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	return domain.Principal{UserID: s.UserID, SessionID: s.ID}, nil
}

// sessionStoreUnavailable reports a failing session lookup as 503, not as "not signed in": a
// database outage must not look like a logout (phase 1 spec section 10).
func sessionStoreUnavailable(err error) error {
	return fmt.Errorf("%w: %w", domain.NewError(domain.CodeDependencyUnavailable, "session store is unavailable"), err)
}
```

สร้าง `internal/core/service/audit.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// errAuditWriteFailed reports an audit event that could not be stored. A business change never
// commits without its event (AGENTS.md), so the caller's transaction rolls back.
var errAuditWriteFailed = domain.NewError(domain.CodeAuditWriteFailed, "could not record the audit event")

// auditSpec is what a use case says about one event; newAuditEvent adds the time, IDs and metadata.
type auditSpec struct {
	Type       domain.EventType
	Outcome    domain.AuditOutcome
	Actor      domain.ID // only an identity the request proved; zero for failed and anonymous attempts
	SessionID  domain.ID
	TargetType string
	TargetID   string
	Reason     string
	Method     string // "password" or "google" for login events
	Scope      domain.RateLimitScope
}

// auditMetadata is the allowlist of metadata (AGENTS.md): no raw email, no full IP.
type auditMetadata struct {
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	Method    string `json:"method,omitempty"`
	Scope     string `json:"scope,omitempty"`
}

func (a *Auth) newAuditEvent(meta domain.RequestMeta, s auditSpec) (domain.AuditEvent, error) {
	id, err := domain.NewID()
	if err != nil {
		return domain.AuditEvent{}, err
	}
	metadata, err := json.Marshal(auditMetadata{IP: meta.MaskedIP, UserAgent: meta.UserAgent, Method: s.Method, Scope: string(s.Scope)})
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("encode audit metadata: %w", err)
	}
	return domain.NewAuditEvent(domain.AuditEvent{
		ID: id, OccurredAt: NowForStorage(a.clock), EventType: s.Type, ActorUserID: s.Actor,
		RequestID: meta.RequestID, SessionID: s.SessionID, TargetType: s.TargetType, TargetID: s.TargetID,
		Outcome: s.Outcome, ReasonCode: s.Reason, Metadata: metadata,
	})
}

// appendAudit writes the event in tx, the transaction of the change it describes.
func (a *Auth) appendAudit(ctx context.Context, tx port.TxRepositories, meta domain.RequestMeta, s auditSpec) error {
	e, err := a.newAuditEvent(meta, s)
	if err != nil {
		return err
	}
	if err := tx.Audit().Append(ctx, e); err != nil {
		return fmt.Errorf("%w: %w", errAuditWriteFailed, err)
	}
	return nil
}

// recordFailure writes the event of a failed or refused attempt in a transaction of its own: there
// is no business change to commit it with, and it must survive the error the caller returns.
func (a *Auth) recordFailure(ctx context.Context, meta domain.RequestMeta, s auditSpec) error {
	err := a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		return a.appendAudit(ctx, tx, meta, s)
	})
	if err != nil {
		a.log.Error("could not record an audit event",
			slog.String("event", "audit.write_failed"),
			slog.String("request_id", meta.RequestID.String()),
			slog.String("event_type", string(s.Type)))
	}
	return err
}
```

สร้าง `internal/core/service/admission.go`:

```go
package service

import (
	"context"

	"go-backend-stock/internal/core/domain"
)

// admit checks rules before the use case does any work. A refused request becomes rate_limited with
// the wait; the first refusal per key per minute is also audited, in its own transaction (phase 1
// spec section 9). actor is the signed-in user, or the zero ID.
func (a *Auth) admit(ctx context.Context, rules []domain.RateLimitRule, meta domain.RequestMeta, actor domain.ID) error {
	decision, err := a.store.RateLimits().Admit(ctx, rules, NowForStorage(a.clock))
	if err != nil {
		return err
	}
	if decision.Allowed {
		return nil
	}
	if decision.RecordDenialAudit {
		// A lost denial audit is logged by recordFailure; the client still gets its 429.
		_ = a.recordFailure(ctx, meta, auditSpec{
			Type: domain.EventSecurityRateLimited, Outcome: domain.AuditOutcomeDenied, Actor: actor,
			TargetType: "rate_limit", TargetID: string(decision.Scope), Scope: decision.Scope,
		})
	}
	return domain.NewRetryError(domain.CodeRateLimited, "too many requests, try again later", decision.RetryAfter)
}
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/core; go vet ./internal/core/... && go test ./internal/core/... -count=1 -race && make lint`
Expected: ผ่านทั้งหมด รวม `boundary_test` (fakes import เฉพาะ core)

- [ ] **Step 5: Commit**

```bash
git add internal/core/port/auth_service.go internal/core/service
git commit -m "feat(service): add the auth service with session authentication, audit and rate-limit helpers"
```

---

### Task 8: ท่อ HTTP ของ auth — request meta, cookies, middleware session/CSRF และ `GET /auth/csrf`

**Files:**
- Create: `internal/adapter/http/auth.go`, `internal/adapter/http/auth_dto.go`, `internal/adapter/http/cookies.go`, `internal/adapter/http/meta.go`, `internal/adapter/http/middleware/session.go`, `internal/adapter/http/middleware/csrf.go`, `internal/adapter/http/auth_test_helpers_test.go`, `internal/adapter/http/auth_plumbing_test.go`
- Modify: `internal/adapter/http/server.go`, `cmd/api/main.go` (tag ของ Swagger), `docs/api/` (generated)

**Interfaces:**
- Consumes: `port.AuthService` (`Authenticate`, Task 7), `service.SessionCSRFBinding`, `service.PreLoginCSRFBinding`, `service.NewCSRFNonce`, `service.NewCSRFCodec`, `ClientIPResolver`, `domain.NewRequestMeta`, `domain.ErrUnauthenticated`, `domain.ErrCSRFFailed`, `middleware.GetRequestID`
- Produces:
  - `http.CSRFTokens` interface (`Issue(binding) (string, time.Time, error)`, `Verify(token, binding) error`) — `*service.CSRFCodec` implement อยู่แล้ว
  - `http.AuthOptions{Service port.AuthService; CSRF CSRFTokens; ClientIP *ClientIPResolver; PublicOrigin string; SecureCookies bool}` และ `Options.Auth`; `Service` เป็น nil = ไม่ลงทะเบียน route ของ auth
  - `authHandlers` (`auth`, `csrf`, `clientIP`, `cookies cookieJar`) พร้อม `(*authHandlers).requestMeta(c) (domain.RequestMeta, error)` และ `registerAuthRoutes(app, opts)` ที่ Task 9–11 เพิ่ม route
  - `cookieJar{secure bool; sessionName, nonceName string}`; `newCookieJar(secure)` → `session`/`csrf_nonce` หรือ `__Host-session`/`__Host-csrf_nonce`; `(cookieJar).set(c, name, value, expires)`, `(cookieJar).clear(c, name)`
  - middleware: `Authenticator` interface, `ResolveSession(auth Authenticator, cookieName string)`, `RequireAuth()`, `GetPrincipal(c) (domain.Principal, bool)`, `CSRFVerifier` interface, `CSRF(verifier CSRFVerifier, publicOrigin, nonceCookie string)`, `ValidNonce(s string) bool`, `HeaderCSRFToken = "X-CSRF-Token"`
  - `GET /api/v1/auth/csrf` → 200 `CSRFResponse{csrf_token, expires_at}`; ยังไม่ login จะตั้ง cookie nonce ใหม่หรือต่ออายุของเดิม (อายุเท่า token)
  - test helpers (ใช้ต่อใน Task 9–12): `testClock` (`newTestClock`, `Now`, `Advance`, thread-safe), `fakeAuthService` (embed `port.AuthService`), `testOrigin`, `testTrustedProxies`, `syncBuffer`

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/adapter/http/auth_test_helpers_test.go`:

```go
package http

import (
	"bytes"
	"context"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
	"go-backend-stock/internal/core/service"
)

const testOrigin = "http://localhost:8080"

// testTrustedProxies trusts the peer address app.Test uses (0.0.0.0 or loopback), so a test can
// choose its client IP with X-Forwarded-For. If a test sees another peer address, add its range here.
var testTrustedProxies = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("::1/128"),
}

// testClock is a port.Clock that only moves when a test says so. Requests may read it from several
// goroutines.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(start time.Time) *testClock {
	return &testClock{now: start.UTC().Truncate(time.Microsecond)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeAuthService embeds the port: a method a test does not set panics when called.
type fakeAuthService struct {
	port.AuthService
	principals map[string]domain.Principal // session token -> caller
	err        error
}

func (f *fakeAuthService) Authenticate(_ context.Context, token string) (domain.Principal, error) {
	if f.err != nil {
		return domain.Principal{}, f.err
	}
	p, ok := f.principals[token]
	if !ok {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	return p, nil
}

func newTestCSRF(t *testing.T, clock port.Clock) *service.CSRFCodec {
	t.Helper()
	codec, err := service.NewCSRFCodec([]byte(strings.Repeat("c", 32)), clock)
	if err != nil {
		t.Fatal(err)
	}
	return codec
}

// newAuthTestServer builds the real server around a fake auth service.
func newAuthTestServer(t *testing.T, auth port.AuthService, secure bool) (*fiber.App, *service.CSRFCodec) {
	t.Helper()
	codec := newTestCSRF(t, newTestClock(time.Now()))
	log, _ := newTestLogger()
	app := NewServer(Options{Logger: log, Readiness: fakeReadiness{}, Auth: AuthOptions{
		Service: auth, CSRF: codec, ClientIP: NewClientIPResolver(testTrustedProxies),
		PublicOrigin: testOrigin, SecureCookies: secure,
	}})
	return app, codec
}

// syncBuffer is a bytes.Buffer that log handlers on several goroutines can share.
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
```

สร้าง `internal/adapter/http/auth_plumbing_test.go`:

```go
package http

import (
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/service"
)

func cookieNamed(resp response, name string) *nethttp.Cookie {
	for _, c := range (&nethttp.Response{Header: resp.Header}).Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func getCSRF(t *testing.T, app *fiber.App, cookies ...*nethttp.Cookie) (response, CSRFResponse) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/v1/auth/csrf", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, body := doRequest(t, app, req)
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET /api/v1/auth/csrf = %d %s", resp.StatusCode, body)
	}
	var out CSRFResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return resp, out
}

func TestCSRFTokenBeforeLoginSetsANonceCookie(t *testing.T) {
	app, codec := newAuthTestServer(t, &fakeAuthService{}, false)

	resp, out := getCSRF(t, app)
	nonce := cookieNamed(resp, "csrf_nonce")
	if nonce == nil || !middleware.ValidNonce(nonce.Value) {
		t.Fatalf("Set-Cookie = %v, want a csrf_nonce cookie with a fresh nonce", resp.Header.Values("Set-Cookie"))
	}
	raw := strings.ToLower(strings.Join(resp.Header.Values("Set-Cookie"), "\n"))
	for _, want := range []string{"httponly", "samesite=lax", "path=/", "expires="} {
		if !strings.Contains(raw, want) {
			t.Errorf("nonce cookie %q lacks %q", raw, want)
		}
	}
	if strings.Contains(raw, "secure") || strings.Contains(raw, "domain=") {
		t.Errorf("an http origin must not set Secure or Domain: %q", raw)
	}
	if err := codec.Verify(out.CSRFToken, service.PreLoginCSRFBinding(nonce.Value)); err != nil {
		t.Errorf("token does not verify against the cookie's nonce: %v", err)
	}
	if out.ExpiresAt.IsZero() || out.ExpiresAt.Location().String() != "UTC" {
		t.Errorf("expires_at = %v, want a UTC time", out.ExpiresAt)
	}

	// The same browser asks again: the nonce is kept (and its cookie renewed) so older tabs keep working.
	resp2, out2 := getCSRF(t, app, &nethttp.Cookie{Name: "csrf_nonce", Value: nonce.Value})
	if again := cookieNamed(resp2, "csrf_nonce"); again == nil || again.Value != nonce.Value {
		t.Errorf("second call cookie = %v, want the same nonce renewed", again)
	}
	if err := codec.Verify(out2.CSRFToken, service.PreLoginCSRFBinding(nonce.Value)); err != nil {
		t.Errorf("second token does not verify: %v", err)
	}

	// A forged or broken nonce cookie is replaced, not trusted.
	resp3, _ := getCSRF(t, app, &nethttp.Cookie{Name: "csrf_nonce", Value: "not-a-nonce"})
	if fresh := cookieNamed(resp3, "csrf_nonce"); fresh == nil || fresh.Value == "not-a-nonce" || !middleware.ValidNonce(fresh.Value) {
		t.Errorf("broken nonce cookie was kept: %v", fresh)
	}
}

func TestCSRFTokenWhenSignedInIsBoundToTheSession(t *testing.T) {
	principal := domain.Principal{UserID: mustTestID(t), SessionID: mustTestID(t)}
	app, codec := newAuthTestServer(t, &fakeAuthService{principals: map[string]domain.Principal{"session-token": principal}}, false)

	resp, out := getCSRF(t, app, &nethttp.Cookie{Name: "session", Value: "session-token"})
	if err := codec.Verify(out.CSRFToken, service.SessionCSRFBinding(principal.SessionID)); err != nil {
		t.Errorf("token is not bound to the session: %v", err)
	}
	if cookieNamed(resp, "csrf_nonce") != nil {
		t.Error("a signed-in call must not set a nonce cookie")
	}
}

func TestSecureOriginUsesHostPrefixedSecureCookies(t *testing.T) {
	app, _ := newAuthTestServer(t, &fakeAuthService{}, true)
	resp, _ := getCSRF(t, app)
	c := cookieNamed(resp, "__Host-csrf_nonce")
	if c == nil || !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" {
		t.Errorf("cookie = %+v, want __Host-csrf_nonce with Secure, HttpOnly, Path=/ and no Domain", c)
	}
}

func TestCSRFTokenWhenTheSessionStoreIsDown(t *testing.T) {
	down := domain.NewError(domain.CodeDependencyUnavailable, "session store is unavailable")
	app, _ := newAuthTestServer(t, &fakeAuthService{err: down}, false)
	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/v1/auth/csrf", nil)
	req.AddCookie(&nethttp.Cookie{Name: "session", Value: "anything"})
	resp, body := doRequest(t, app, req)
	if resp.StatusCode != nethttp.StatusServiceUnavailable || decodeError(t, body).Code != "dependency_unavailable" {
		t.Errorf("got %d %s, want 503 dependency_unavailable (an outage must not look like a logout)", resp.StatusCode, body)
	}
}

// csrfTestApp is a minimal app with the session and CSRF middleware in the order the server uses.
func csrfTestApp(t *testing.T, auth middleware.Authenticator, codec *service.CSRFCodec) *fiber.App {
	t.Helper()
	log, _ := newTestLogger()
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler(log)})
	app.Use(middleware.RequestID())
	ok := func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) }
	app.Post("/x", middleware.ResolveSession(auth, "session"), middleware.CSRF(codec, testOrigin, "csrf_nonce"), ok)
	app.Get("/x", middleware.ResolveSession(auth, "session"), middleware.CSRF(codec, testOrigin, "csrf_nonce"), ok)
	app.Get("/private", middleware.ResolveSession(auth, "session"), middleware.RequireAuth(), func(c fiber.Ctx) error {
		p, _ := middleware.GetPrincipal(c)
		return c.SendString(p.UserID.String())
	})
	return app
}

func TestCSRFMiddleware(t *testing.T) {
	codec := newTestCSRF(t, newTestClock(time.Now()))
	principal := domain.Principal{UserID: mustTestID(t), SessionID: mustTestID(t)}
	auth := &fakeAuthService{principals: map[string]domain.Principal{"session-token": principal}}
	app := csrfTestApp(t, auth, codec)

	nonce, err := service.NewCSRFNonce()
	if err != nil {
		t.Fatal(err)
	}
	otherNonce, _ := service.NewCSRFNonce()
	preToken, _, _ := codec.Issue(service.PreLoginCSRFBinding(nonce))
	otherToken, _, _ := codec.Issue(service.PreLoginCSRFBinding(otherNonce))
	sessionToken, _, _ := codec.Issue(service.SessionCSRFBinding(principal.SessionID))

	type req struct {
		method, origin, referer, token, nonce, session string
	}
	tests := []struct {
		name   string
		r      req
		status int
	}{
		{"pre-login token from the right origin", req{"POST", testOrigin, "", preToken, nonce, ""}, 204},
		{"referer stands in for a missing origin", req{"POST", "", testOrigin + "/docs/", preToken, nonce, ""}, 204},
		{"neither origin nor referer", req{"POST", "", "", preToken, nonce, ""}, 403},
		{"other origin", req{"POST", "http://evil.example", "", preToken, nonce, ""}, 403},
		{"null origin", req{"POST", "null", "", preToken, nonce, ""}, 403},
		{"origin differs only by port", req{"POST", "http://localhost:8081", "", preToken, nonce, ""}, 403},
		{"referer of another site", req{"POST", "", "http://evil.example/page", preToken, nonce, ""}, 403},
		{"missing token", req{"POST", testOrigin, "", "", nonce, ""}, 403},
		{"token of another nonce", req{"POST", testOrigin, "", otherToken, nonce, ""}, 403},
		{"no nonce cookie", req{"POST", testOrigin, "", preToken, "", ""}, 403},
		{"malformed nonce cookie", req{"POST", testOrigin, "", preToken, "short", ""}, 403},
		{"signed in with the session token", req{"POST", testOrigin, "", sessionToken, "", "session-token"}, 204},
		{"signed in but sending the pre-login token", req{"POST", testOrigin, "", preToken, nonce, "session-token"}, 403},
		{"safe method needs no token", req{"GET", "", "", "", "", ""}, 204},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), tt.r.method, "/x", nil)
			if tt.r.origin != "" {
				r.Header.Set("Origin", tt.r.origin)
			}
			if tt.r.referer != "" {
				r.Header.Set("Referer", tt.r.referer)
			}
			if tt.r.token != "" {
				r.Header.Set(middleware.HeaderCSRFToken, tt.r.token)
			}
			if tt.r.nonce != "" {
				r.AddCookie(&nethttp.Cookie{Name: "csrf_nonce", Value: tt.r.nonce})
			}
			if tt.r.session != "" {
				r.AddCookie(&nethttp.Cookie{Name: "session", Value: tt.r.session})
			}
			resp, body := doRequest(t, app, r)
			if resp.StatusCode != tt.status {
				t.Fatalf("status = %d (%s), want %d", resp.StatusCode, body, tt.status)
			}
			if tt.status == 403 && decodeError(t, body).Code != "csrf_failed" {
				t.Errorf("code = %s, want csrf_failed", decodeError(t, body).Code)
			}
		})
	}
}

func TestRequireAuth(t *testing.T) {
	principal := domain.Principal{UserID: mustTestID(t), SessionID: mustTestID(t)}
	app := csrfTestApp(t, &fakeAuthService{principals: map[string]domain.Principal{"session-token": principal}}, newTestCSRF(t, newTestClock(time.Now())))

	for name, cookie := range map[string]string{"no cookie": "", "unknown session": "stale-token"} {
		r := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/private", nil)
		if cookie != "" {
			r.AddCookie(&nethttp.Cookie{Name: "session", Value: cookie})
		}
		resp, body := doRequest(t, app, r)
		if resp.StatusCode != nethttp.StatusUnauthorized || decodeError(t, body).Code != "unauthenticated" {
			t.Errorf("%s: got %d %s, want 401 unauthenticated", name, resp.StatusCode, body)
		}
	}

	r := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/private", nil)
	r.AddCookie(&nethttp.Cookie{Name: "session", Value: "session-token"})
	resp, body := doRequest(t, app, r)
	if resp.StatusCode != nethttp.StatusOK || string(body) != principal.UserID.String() {
		t.Errorf("signed in: got %d %s, want 200 with the user ID", resp.StatusCode, body)
	}
}

func TestRequestMeta(t *testing.T) {
	h := &authHandlers{clientIP: NewClientIPResolver(testTrustedProxies)}
	log, _ := newTestLogger()
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler(log)})
	app.Use(middleware.RequestID())
	app.Get("/meta", func(c fiber.Ctx) error {
		meta, err := h.requestMeta(c)
		if err != nil {
			return err
		}
		return c.JSON(map[string]string{
			"request_id": meta.RequestID.String(), "client_ip": meta.ClientIP.String(),
			"masked_ip": meta.MaskedIP, "user_agent": meta.UserAgent,
		})
	})

	r := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/meta", nil)
	r.Header.Add("X-Forwarded-For", "6.6.6.6")
	r.Header.Add("X-Forwarded-For", "198.51.100.7")
	r.Header.Set("User-Agent", "agent\r\nX-Injected: yes")
	resp, body := doRequest(t, app, r)
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	if got["request_id"] != resp.Header.Get(middleware.HeaderRequestID) {
		t.Errorf("request_id = %q, want the X-Request-ID %q", got["request_id"], resp.Header.Get(middleware.HeaderRequestID))
	}
	if got["client_ip"] != "198.51.100.7" || got["masked_ip"] != "198.51.100.0/24" {
		t.Errorf("client IP = %q / %q, want the right-most untrusted hop and its /24 (if the peer address of app.Test is not in testTrustedProxies, add it there)", got["client_ip"], got["masked_ip"])
	}
	if strings.ContainsAny(got["user_agent"], "\r\n") {
		t.Errorf("user agent %q was not sanitized", got["user_agent"])
	}
}
```

ต่อท้าย `auth_test_helpers_test.go` ด้วย helper สองตัวที่ test ข้างบนใช้:

```go

func mustTestID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/adapter/http/ -run 'CSRF|RequireAuth|RequestMeta|SecureOrigin' -count=1`
Expected: build fail — `undefined: AuthOptions`, `CSRFResponse`, `middleware.ResolveSession`, `middleware.ValidNonce`

- [ ] **Step 3: เขียนโค้ด**

สร้าง `internal/adapter/http/middleware/session.go`:

```go
package middleware

import (
	"context"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/core/domain"
)

// Authenticator resolves a raw session cookie (port.AuthService implements it).
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (domain.Principal, error)
}

type principalKey struct{}

// ResolveSession stores the caller in the request when the session cookie names an active session.
// No cookie, or an unknown, expired or revoked session, leaves the request anonymous; a failing
// session store is an error (503), never a silent logout.
func ResolveSession(auth Authenticator, cookieName string) fiber.Handler {
	return func(c fiber.Ctx) error {
		token := strings.Clone(c.Cookies(cookieName))
		if token == "" {
			return c.Next()
		}
		p, err := auth.Authenticate(c.Context(), token)
		switch {
		case errors.Is(err, domain.ErrUnauthenticated):
			return c.Next()
		case err != nil:
			return err
		}
		c.Locals(principalKey{}, p)
		return c.Next()
	}
}

// RequireAuth refuses an anonymous request with 401 unauthenticated. Ownership of data is checked
// again by the use case and its queries; this only stops requests that have no caller at all.
func RequireAuth() fiber.Handler {
	return func(c fiber.Ctx) error {
		if _, ok := GetPrincipal(c); !ok {
			return domain.ErrUnauthenticated
		}
		return c.Next()
	}
}

// GetPrincipal returns the caller ResolveSession found, if any. The value is a copy that does not
// refer to the request.
func GetPrincipal(c fiber.Ctx) (domain.Principal, bool) {
	p, ok := c.Locals(principalKey{}).(domain.Principal)
	return p, ok && !p.IsZero()
}
```

สร้าง `internal/adapter/http/middleware/csrf.go`:

```go
package middleware

import (
	"encoding/base64"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/service"
)

// HeaderCSRFToken carries the token from GET /api/v1/auth/csrf on every unsafe request.
const HeaderCSRFToken = "X-CSRF-Token"

// CSRFVerifier checks a CSRF token against its binding (service.CSRFCodec implements it).
type CSRFVerifier interface {
	Verify(token, binding string) error
}

// CSRF protects every request that is not GET, HEAD or OPTIONS: the Origin (or, without one, the
// origin of the Referer) must equal publicOrigin exactly, and X-CSRF-Token must be bound to the
// current session or, before login, to the nonce in the nonceCookie cookie. Every failure is the
// same 403 csrf_failed. Run it after ResolveSession, so a signed-in request uses the session binding.
func CSRF(verifier CSRFVerifier, publicOrigin, nonceCookie string) fiber.Handler {
	return func(c fiber.Ctx) error {
		switch c.Method() {
		case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
			return c.Next()
		}
		if !fromOrigin(c, publicOrigin) {
			return domain.ErrCSRFFailed
		}
		binding, ok := csrfBinding(c, nonceCookie)
		if !ok {
			return domain.ErrCSRFFailed
		}
		if err := verifier.Verify(strings.Clone(c.Get(HeaderCSRFToken)), binding); err != nil {
			return domain.ErrCSRFFailed
		}
		return c.Next()
	}
}

// fromOrigin reports whether the request comes from want. Browsers send Origin on POST; a client
// without one may send a Referer instead; a request with neither is refused.
func fromOrigin(c fiber.Ctx, want string) bool {
	origin := c.Get(fiber.HeaderOrigin)
	if origin == "" {
		referer := c.Get(fiber.HeaderReferer)
		if referer == "" {
			return false
		}
		u, err := url.Parse(referer)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return false
		}
		origin = u.Scheme + "://" + u.Host
	}
	return origin == want
}

func csrfBinding(c fiber.Ctx, nonceCookie string) (string, bool) {
	if p, ok := GetPrincipal(c); ok {
		return service.SessionCSRFBinding(p.SessionID), true
	}
	nonce := strings.Clone(c.Cookies(nonceCookie))
	if !ValidNonce(nonce) {
		return "", false
	}
	return service.PreLoginCSRFBinding(nonce), true
}

// ValidNonce reports whether s has the shape service.NewCSRFNonce produces: 32 bytes as 43
// unpadded base64url characters. A cookie of any other shape is treated as missing.
func ValidNonce(s string) bool {
	if len(s) != 43 {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return err == nil && len(raw) == 32
}
```

สร้าง `internal/adapter/http/cookies.go`:

```go
package http

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
)

// cookieJar names and writes the auth cookies. Over HTTPS the names carry the __Host- prefix, which
// makes the browser insist on Secure, Path=/ and no Domain (phase 1 spec section 10).
type cookieJar struct {
	secure      bool
	sessionName string
	nonceName   string
}

func newCookieJar(secure bool) cookieJar {
	if secure {
		return cookieJar{secure: true, sessionName: "__Host-session", nonceName: "__Host-csrf_nonce"}
	}
	return cookieJar{sessionName: "session", nonceName: "csrf_nonce"}
}

// set writes an HttpOnly, SameSite=Lax cookie for the whole site that expires at expires. It sets no
// Domain, so the cookie belongs to this host only.
func (j cookieJar) set(c fiber.Ctx, name, value string, expires time.Time) {
	c.Cookie(&fiber.Cookie{
		Name: name, Value: value, Path: "/", Expires: expires.UTC(),
		Secure: j.secure, HTTPOnly: true, SameSite: fiber.CookieSameSiteLaxMode,
	})
}

// clear expires the cookie with the same attributes it was set with, so the browser accepts the
// deletion (a __Host- cookie must keep Secure and Path=/).
func (j cookieJar) clear(c fiber.Ctx, name string) {
	c.Cookie(&fiber.Cookie{
		Name: name, Value: "", Path: "/", Expires: fasthttp.CookieExpireDelete,
		Secure: j.secure, HTTPOnly: true, SameSite: fiber.CookieSameSiteLaxMode,
	})
}
```

สร้าง `internal/adapter/http/meta.go`:

```go
package http

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
)

// requestMeta collects what a use case may know about the request. Every value is copied out of the
// request, which Fiber reuses after the handler returns.
func (h *authHandlers) requestMeta(c fiber.Ctx) (domain.RequestMeta, error) {
	id, err := domain.ParseID("request_id", middleware.GetRequestID(c))
	if err != nil {
		return domain.RequestMeta{}, domain.NewError(domain.CodeInternal, "request has no valid request ID")
	}
	var forwarded []string
	for _, v := range c.Request().Header.PeekAll(fiber.HeaderXForwardedFor) {
		forwarded = append(forwarded, string(v))
	}
	ip := h.clientIP.Resolve(c.RequestCtx().RemoteAddr().String(), forwarded)
	return domain.NewRequestMeta(id, ip, strings.Clone(c.Get(fiber.HeaderUserAgent))), nil
}
```

สร้าง `internal/adapter/http/auth_dto.go`:

```go
package http

import "time"

// CSRFResponse is returned by GET /api/v1/auth/csrf.
type CSRFResponse struct {
	CSRFToken string    `json:"csrf_token" example:"AQAAAABnHk3QxJ9b3m0z0bH2l9V1cW5qQk1xU2ZtV0RkY0p0dVhQb0xMZ3hQYnpqUkw"`
	ExpiresAt time.Time `json:"expires_at" example:"2026-10-24T10:00:00Z"`
}
```

สร้าง `internal/adapter/http/auth.go`:

```go
package http

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/port"
	"go-backend-stock/internal/core/service"
)

// CSRFTokens issues and checks CSRF tokens (service.CSRFCodec implements it).
type CSRFTokens interface {
	Issue(binding string) (token string, expiresAt time.Time, err error)
	Verify(token, binding string) error
}

// AuthOptions wires the auth endpoints. A nil Service registers no auth route, so the health and
// docs tests can build the server without it.
type AuthOptions struct {
	Service       port.AuthService
	CSRF          CSRFTokens
	ClientIP      *ClientIPResolver // nil trusts no proxy header
	PublicOrigin  string            // exact origin of unsafe requests, such as http://localhost:8080
	SecureCookies bool              // PublicOrigin is https: __Host- names and the Secure attribute
}

type authHandlers struct {
	auth     port.AuthService
	csrf     CSRFTokens
	clientIP *ClientIPResolver
	cookies  cookieJar
}

// registerAuthRoutes adds the /api/v1/auth routes. Each route lists its middleware explicitly, in
// the order of phase 1 spec section 5: resolve session → require auth → CSRF/Origin → handler.
func registerAuthRoutes(app *fiber.App, opts AuthOptions) {
	if opts.Service == nil {
		return
	}
	resolver := opts.ClientIP
	if resolver == nil {
		resolver = NewClientIPResolver(nil)
	}
	h := &authHandlers{auth: opts.Service, csrf: opts.CSRF, clientIP: resolver, cookies: newCookieJar(opts.SecureCookies)}
	session := middleware.ResolveSession(opts.Service, h.cookies.sessionName)

	app.Get("/api/v1/auth/csrf", session, h.csrfToken)
}

// CSRFToken godoc
// @Summary      Get a CSRF token
// @Description  Returns the token to send in the X-CSRF-Token header of every POST. Signed in, the token is bound to the session; signed out, it is bound to a random nonce that this call keeps in the HttpOnly cookie csrf_nonce (__Host-csrf_nonce over HTTPS). Tokens last one hour. Ask for a new token after login and after logout, because the binding changes. Browsers (and Swagger UI) send the cookie by themselves; curl needs a cookie jar (-b/-c).
// @Tags         Auth
// @ID           getCSRFToken
// @Produce      json
// @Success      200  {object}  CSRFResponse
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable: the session store could not be reached"
// @Header       200  {string}  Set-Cookie  "csrf_nonce=…; Path=/; HttpOnly; SameSite=Lax — only when signed out"
// @Router       /api/v1/auth/csrf [get]
func (h *authHandlers) csrfToken(c fiber.Ctx) error {
	var binding, nonce string
	p, signedIn := middleware.GetPrincipal(c)
	if signedIn {
		binding = service.SessionCSRFBinding(p.SessionID)
	} else {
		nonce = strings.Clone(c.Cookies(h.cookies.nonceName))
		if !middleware.ValidNonce(nonce) {
			fresh, err := service.NewCSRFNonce()
			if err != nil {
				return err
			}
			nonce = fresh
		}
		binding = service.PreLoginCSRFBinding(nonce)
	}
	token, expiresAt, err := h.csrf.Issue(binding)
	if err != nil {
		return err
	}
	if !signedIn {
		// Renewed on every call so the cookie lives exactly as long as the newest token.
		h.cookies.set(c, h.cookies.nonceName, nonce, expiresAt)
	}
	return c.JSON(CSRFResponse{CSRFToken: token, ExpiresAt: expiresAt.UTC()})
}
```

แก้ `internal/adapter/http/server.go` ตามคู่ต่อไปนี้:

1. แทน

```go
type Options struct {
	Logger    *slog.Logger
	Readiness ReadinessChecker
	Docs      DocsOptions
}
```

   ด้วย

```go
type Options struct {
	Logger    *slog.Logger
	Readiness ReadinessChecker
	Docs      DocsOptions
	Auth      AuthOptions
}
```

2. แทน

```go
	registerDocs(app, opts.Docs)
}
```

   ด้วย

```go
	registerDocs(app, opts.Docs)
	registerAuthRoutes(app, opts.Auth)
}
```

แก้ `cmd/api/main.go` — แทน

```go
// @tag.name        Health
// @tag.description Liveness and readiness probes
```

ด้วย

```go
// @tag.name        Health
// @tag.description Liveness and readiness probes
// @tag.name        Auth
// @tag.description Registration, email verification, sessions and CSRF tokens. Every POST needs the X-CSRF-Token header from GET /api/v1/auth/csrf and must come from the API's own origin.
```

- [ ] **Step 4: รัน test ให้ผ่าน และ generate Swagger**

Run: `gofmt -l internal/adapter/http cmd; go vet ./internal/adapter/http/... ./cmd/... && go test ./internal/adapter/http/... -count=1 -race && make swagger && make lint`
Expected: test ผ่าน; `make swagger` สร้าง operation `getCSRFToken` ใน `docs/api/` และ validate ผ่าน; ถ้า `TestRequestMeta` ได้ IP ผิดเพราะ peer address ของ `app.Test` ไม่อยู่ใน `testTrustedProxies` ให้พิมพ์ `c.RequestCtx().RemoteAddr()` ดูแล้วเพิ่มช่วงนั้นใน `testTrustedProxies` และบันทึกใน report

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/http cmd/api/main.go docs/api
git commit -m "feat(http): add session and CSRF middleware, auth cookies and GET /auth/csrf"
```

---

### Task 9: integration harness และ `POST /auth/register`

**Files:**
- Create: `internal/core/service/register.go`, `internal/adapter/http/decode.go`, `internal/adapter/http/register.go`, `internal/adapter/http/auth_harness_test.go`, `internal/adapter/http/register_test.go`, `internal/adapter/http/auth_contract_test.go`
- Modify: `internal/core/port/auth_service.go`, `internal/adapter/http/auth.go`, `internal/adapter/http/auth_dto.go`, `Makefile`, `docs/api/` (generated)

**Interfaces:**
- Consumes: `service.Auth` และ helpers (`admit`, `appendAudit`, Task 7), `RateLimitPolicy.Signup`, `PasswordHasher.Hash`, `GenerateOTP`, `OTPCodec.Digest`, `domain.NewOTPChallenge`, `port.Mailer`; HTTP: `authHandlers`, `requestMeta`, `cookieJar`, `middleware.CSRF` (Task 8); test helpers `testClock`, `testOrigin`, `testTrustedProxies`, `syncBuffer`, `mustTestID`, `decodeError`, `loadSpec`
- Produces:
  - `port.AuthService.Register(ctx, in domain.RegisterInput, meta domain.RequestMeta) (domain.OTPReceipt, error)`
  - `service`: `otpLifetime = 10 * time.Minute`, `(*Auth).newVerificationChallenge(userID, now) (domain.OTPCode, domain.OTPChallenge, error)`, `verificationRequested(userID) auditSpec`, `(*Auth).send(meta, kind, deliver func() error)` — ใช้ต่อใน Task 10
  - HTTP: `PasswordParser` interface (`Parse(raw string, email domain.Email) (domain.Password, error)`), `AuthOptions.Passwords`, `decodeJSON(c, dst) error` (unknown field/ข้อมูลต่อท้าย/ชนิดผิด → `malformed_json`), DTO `RegisterRequest`, `OTPAcceptedResponse`, `newOTPAccepted(domain.OTPReceipt)`, ตัวแปร `csrf` middleware ใน `registerAuthRoutes`
  - `POST /api/v1/auth/register` → 202 `OTPAcceptedResponse`
  - harness (ใช้ต่อใน Task 10–12): `newAuthHarness(t, ...harnessOption) *authHarness` (`app`, `pool`, `admin`, `store`, `mailer`, `clock`, `logs`, `origin`), options `withHasherParams`, `withResponseFloor`, `withFailingAudit`; `(*authHarness).newBrowser(t) *browser` (`do`, `tryDo`, `fetchCSRF`, `register`); `(*authHarness).user`, `challenges`, `auditFor(requestID)`, `auditByTarget(targetID)`; `fakeMailer` (`lastVerification`, `verificationCount`, `attemptCount`); `uniqueEmail`, `stripJSONField`, `failingAuditUoW`
  - `documentedAuthOperations` + `TestAuthOperationsAreDocumented` (Task 10–11 เพิ่มแถว)
  - `make test-integration` รัน `./internal/adapter/http/...` ด้วย

- [ ] **Step 1: เขียน harness และ failing tests**

แก้ `Makefile` — แทน

```make
	go test -race -count=1 ./internal/adapter/postgres/...
```

ด้วย

```make
	go test -race -count=1 ./internal/adapter/postgres/... ./internal/adapter/http/...
```

สร้าง `internal/adapter/http/auth_harness_test.go`:

```go
package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-backend-stock/internal/adapter/postgres"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
	"go-backend-stock/internal/core/service"
)

// integrationURL returns a database URL from the environment. Integration tests skip without it,
// except under REQUIRE_INTEGRATION=1 (CI and make test-integration), where they fail.
func integrationURL(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		if os.Getenv("REQUIRE_INTEGRATION") == "1" {
			t.Fatalf("%s is required when REQUIRE_INTEGRATION=1", key)
		}
		t.Skipf("%s not set; skipping integration test", key)
	}
	return v
}

func openTestPool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// fakeMailer records what the use cases ask to send, by recipient.
type fakeMailer struct {
	mu            sync.Mutex
	verifications map[string][]domain.OTPMail
	attempts      map[string]int
	err           error
}

func newFakeMailer() *fakeMailer {
	return &fakeMailer{verifications: map[string][]domain.OTPMail{}, attempts: map[string]int{}}
}

func (m *fakeMailer) SendEmailVerification(_ context.Context, mail domain.OTPMail) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.verifications[mail.To.String()] = append(m.verifications[mail.To.String()], mail)
	return m.err
}

func (m *fakeMailer) SendRegistrationAttempt(_ context.Context, to domain.Email) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts[to.String()]++
	return m.err
}

func (m *fakeMailer) lastVerification(t *testing.T, email string) domain.OTPMail {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	mails := m.verifications[email]
	if len(mails) == 0 {
		t.Fatalf("no verification email was sent to %s", email)
	}
	return mails[len(mails)-1]
}

func (m *fakeMailer) verificationCount(email string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.verifications[email])
}

func (m *fakeMailer) attemptCount(email string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.attempts[email]
}

// failingAuditUoW runs transactions whose audit repository always fails, to prove that a business
// change never commits without its event.
type failingAuditUoW struct{ port.UnitOfWork }

func (u failingAuditUoW) WithinTransaction(ctx context.Context, fn func(port.TxRepositories) error) error {
	return u.UnitOfWork.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		return fn(failingAuditTx{tx})
	})
}

type failingAuditTx struct{ port.TxRepositories }

func (failingAuditTx) Audit() port.AuditRepository { return failingAudit{} }

type failingAudit struct{}

func (failingAudit) Append(context.Context, domain.AuditEvent) error {
	return errors.New("audit store is down")
}

type harnessConfig struct {
	hasher    service.Argon2Params
	floor     time.Duration
	failAudit bool
}

type harnessOption func(*harnessConfig)

func withHasherParams(p service.Argon2Params) harnessOption {
	return func(c *harnessConfig) { c.hasher = p }
}

func withResponseFloor(d time.Duration) harnessOption {
	return func(c *harnessConfig) { c.floor = d }
}

func withFailingAudit() harnessOption {
	return func(c *harnessConfig) { c.failAudit = true }
}

// authHarness is the real server over a real PostgreSQL (runtime role), with a fake mailer and a
// clock the test moves. admin reads tables the runtime role may not (audit_events).
type authHarness struct {
	app    *fiber.App
	pool   *pgxpool.Pool
	admin  *pgxpool.Pool
	store  *postgres.Store
	mailer *fakeMailer
	clock  *testClock
	logs   *syncBuffer
	origin string
}

func newAuthHarness(t *testing.T, opts ...harnessOption) *authHarness {
	t.Helper()
	runtimeURL := integrationURL(t, "TEST_RUNTIME_DATABASE_URL")
	adminURL := integrationURL(t, "TEST_ADMIN_DATABASE_URL")
	cfg := harnessConfig{hasher: service.Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1}}
	for _, o := range opts {
		o(&cfg)
	}

	pool := openTestPool(t, runtimeURL)
	admin := openTestPool(t, adminURL)
	store := postgres.NewStore(pool)
	clock := newTestClock(time.Now())
	logs := &syncBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	hasher, err := service.NewPasswordHasher(service.PasswordHasherConfig{Params: cfg.hasher, Concurrency: 4, Logger: log})
	must(err)
	blocklist, err := service.NewBlocklist(strings.NewReader("p@ssw0rd\n"))
	must(err)
	policy, err := service.NewPasswordPolicy(blocklist)
	must(err)
	otp, err := service.NewOTPCodec([]byte(strings.Repeat("o", 32)))
	must(err)
	csrf, err := service.NewCSRFCodec([]byte(strings.Repeat("c", 32)), clock)
	must(err)
	keys, err := service.NewRateLimitKeys([]byte(strings.Repeat("r", 32)))
	must(err)
	limits, err := service.NewRateLimitPolicy(keys, service.RateLimitConfig{
		OTPResendCooldown: time.Minute, OTPRequestsPerHourEmail: 5, OTPRequestsPerHourIP: 20,
		LoginAttemptsPerEmail: 10, LoginAttemptsPerIP: 30,
	})
	must(err)
	pacer, err := service.NewPacer(clock, cfg.floor, 0)
	must(err)
	mailer := newFakeMailer()
	var uow port.UnitOfWork = store
	if cfg.failAudit {
		uow = failingAuditUoW{store}
	}
	auth, err := service.NewAuth(service.AuthDeps{
		Store: store, UnitOfWork: uow, Clock: clock, Hasher: hasher, OTP: otp, Limits: limits,
		Pacer: pacer, Mailer: mailer, Logger: log, SessionTTL: 7 * 24 * time.Hour, OTPMaxAttempts: 5,
	})
	must(err)

	app := NewServer(Options{Logger: log, Readiness: pool, Auth: AuthOptions{
		Service: auth, Passwords: policy, CSRF: csrf,
		ClientIP: NewClientIPResolver(testTrustedProxies), PublicOrigin: testOrigin,
	}})
	return &authHarness{app: app, pool: pool, admin: admin, store: store, mailer: mailer, clock: clock, logs: logs, origin: testOrigin}
}

// browser plays one browser: it keeps cookies like a cookie jar, sends Origin and the CSRF token on
// unsafe requests, and comes from its own random IP so tests never share a rate-limit key.
type browser struct {
	h    *authHarness
	t    *testing.T
	ip   string
	jar  map[string]string
	csrf string
}

func (h *authHarness) newBrowser(t *testing.T) *browser {
	t.Helper()
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	raw[0], raw[1], raw[2], raw[3] = 0x20, 0x01, 0x0d, 0xb8 // 2001:db8::/32, the documentation range
	return &browser{h: h, t: t, ip: netip.AddrFrom16(raw).String(), jar: map[string]string{}}
}

// tryDo sends one request without failing the test, so it may run on any goroutine. A string body
// is sent as is; anything else is marshalled to JSON. extra headers override the defaults ("" deletes).
func (b *browser) tryDo(method, path string, body any, extra map[string]string) (response, []byte, error) {
	var reader io.Reader
	switch v := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(v)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return response{}, nil, err
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Forwarded-For", b.ip)
	req.Header.Set("User-Agent", "integration-test/1.0")
	if method != nethttp.MethodGet && method != nethttp.MethodHead {
		req.Header.Set("Origin", b.h.origin)
		if b.csrf != "" {
			req.Header.Set("X-CSRF-Token", b.csrf)
		}
	}
	for name, value := range b.jar {
		req.AddCookie(&nethttp.Cookie{Name: name, Value: value})
	}
	for k, v := range extra {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := b.h.app.Test(req, fiber.TestConfig{Timeout: 30 * time.Second})
	if err != nil {
		return response{}, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return response{}, nil, err
	}
	for _, c := range resp.Cookies() {
		if c.MaxAge < 0 || (!c.Expires.IsZero() && c.Expires.Before(time.Now())) {
			delete(b.jar, c.Name)
		} else {
			b.jar[c.Name] = c.Value
		}
	}
	return response{StatusCode: resp.StatusCode, Header: resp.Header}, data, nil
}

func (b *browser) do(method, path string, body any, extra map[string]string) (response, []byte) {
	b.t.Helper()
	resp, data, err := b.tryDo(method, path, body, extra)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp, data
}

// fetchCSRF asks for a token the way a page would before every POST.
func (b *browser) fetchCSRF() {
	b.t.Helper()
	resp, body := b.do(nethttp.MethodGet, "/api/v1/auth/csrf", nil, nil)
	if resp.StatusCode != nethttp.StatusOK {
		b.t.Fatalf("GET /api/v1/auth/csrf = %d %s", resp.StatusCode, body)
	}
	var out CSRFResponse
	if err := json.Unmarshal(body, &out); err != nil {
		b.t.Fatal(err)
	}
	b.csrf = out.CSRFToken
}

func (b *browser) register(email, password string) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/register", map[string]string{"email": email, "password": password}, nil)
}

func uniqueEmail(t *testing.T) string {
	t.Helper()
	return "user-" + mustTestID(t).String() + "@example.com"
}

// stripJSONField re-encodes body without field, so two responses that differ only in a random value
// can be compared byte for byte.
func stripJSONField(t *testing.T, body []byte, path ...string) string {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	m := v
	for _, key := range path[:len(path)-1] {
		inner, ok := m[key].(map[string]any)
		if !ok {
			t.Fatalf("body %s has no object %q", body, key)
		}
		m = inner
	}
	delete(m, path[len(path)-1])
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

type userRow struct {
	ID           string
	PasswordHash string
	Verified     bool
}

func (h *authHarness) user(t *testing.T, email string) (userRow, bool) {
	t.Helper()
	var u userRow
	err := h.admin.QueryRow(context.Background(),
		`SELECT id::text, COALESCE(password_hash, ''), email_verified_at IS NOT NULL FROM app.users WHERE email = $1`, email,
	).Scan(&u.ID, &u.PasswordHash, &u.Verified)
	if errors.Is(err, pgx.ErrNoRows) {
		return userRow{}, false
	}
	if err != nil {
		t.Fatalf("read user: %v", err)
	}
	return u, true
}

type challengeRow struct {
	ID             string
	Purpose        string
	FailedAttempts int
	Open           bool
}

func (h *authHarness) challenges(t *testing.T, userID string) []challengeRow {
	t.Helper()
	rows, err := h.admin.Query(context.Background(),
		`SELECT id::text, purpose, failed_attempts, consumed_at IS NULL AND invalidated_at IS NULL
		 FROM app.otp_challenges WHERE user_id = $1 ORDER BY created_at, id`, userID)
	if err != nil {
		t.Fatalf("read challenges: %v", err)
	}
	defer rows.Close()
	var out []challengeRow
	for rows.Next() {
		var c challengeRow
		if err := rows.Scan(&c.ID, &c.Purpose, &c.FailedAttempts, &c.Open); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

type auditRow struct {
	EventType string
	Actor     string // "" when NULL
	SessionID string // "" when NULL
	TargetID  string
	Outcome   string
	Reason    string
	Metadata  map[string]any
}

func (h *authHarness) queryAudit(t *testing.T, where string, arg string) []auditRow {
	t.Helper()
	rows, err := h.admin.Query(context.Background(),
		`SELECT event_type, COALESCE(actor_user_id::text, ''), COALESCE(session_id::text, ''),
		        COALESCE(target_id, ''), outcome, COALESCE(reason_code, ''), metadata
		 FROM app.audit_events WHERE `+where+` ORDER BY occurred_at, id`, arg)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		var metadata []byte
		if err := rows.Scan(&r.EventType, &r.Actor, &r.SessionID, &r.TargetID, &r.Outcome, &r.Reason, &metadata); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(metadata, &r.Metadata); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// auditFor returns the events of one request, by the X-Request-ID the response carried.
func (h *authHarness) auditFor(t *testing.T, requestID string) []auditRow {
	t.Helper()
	return h.queryAudit(t, "request_id = $1::uuid", requestID)
}

// auditByTarget returns every event about one target (a user, challenge or session ID).
func (h *authHarness) auditByTarget(t *testing.T, targetID string) []auditRow {
	t.Helper()
	return h.queryAudit(t, "target_id = $1", targetID)
}

func eventTypes(rows []auditRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.EventType
	}
	return out
}
```

สร้าง `internal/adapter/http/register_test.go`:

```go
package http

import (
	"encoding/json"
	nethttp "net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func decodeAccepted(t *testing.T, body []byte) OTPAcceptedResponse {
	t.Helper()
	var out OTPAcceptedResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	if _, err := domain.ParseID("challenge_id", out.ChallengeID); err != nil || out.ExpiresIn != 600 || out.Message == "" {
		t.Fatalf("202 body = %+v, want a UUID challenge_id, expires_in 600 and a message", out)
	}
	return out
}

func TestRegisterNewEmailCreatesAnUnverifiedAccountAndSendsACode(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)

	resp, body := b.register(email, "Zq9!kVx1")
	if resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("register = %d %s, want 202", resp.StatusCode, body)
	}
	out := decodeAccepted(t, body)

	u, ok := h.user(t, email)
	if !ok || u.Verified || !strings.HasPrefix(u.PasswordHash, "$argon2id$") {
		t.Fatalf("user = %+v (found %v), want an unverified account with an Argon2id hash", u, ok)
	}
	ch := h.challenges(t, u.ID)
	if len(ch) != 1 || ch[0].ID != out.ChallengeID || ch[0].Purpose != "email_verification" || !ch[0].Open {
		t.Errorf("challenges = %+v, want the one open email_verification challenge from the response", ch)
	}
	mail := h.mailer.lastVerification(t, email)
	if mail.ChallengeID.String() != out.ChallengeID || len(mail.OTP.Reveal()) != 6 {
		t.Errorf("mail = %v, want the response's challenge and a 6-digit code", mail)
	}

	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if got := eventTypes(rows); !slices.Equal(got, []string{"auth.registered", "auth.email_verification_requested"}) {
		t.Fatalf("audit = %v", got)
	}
	for _, r := range rows {
		if r.Actor != "" || r.Outcome != "success" || r.TargetID != u.ID {
			t.Errorf("audit row %+v: want actor NULL (not verified yet), success, target the user", r)
		}
		if ip, _ := r.Metadata["ip"].(string); !strings.HasSuffix(ip, "::/48") || r.Metadata["user_agent"] != "integration-test/1.0" {
			t.Errorf("metadata %v: want the /48 of the client IP and the user agent", r.Metadata)
		}
		if strings.Contains(string(mustMarshal(t, r.Metadata)), email) {
			t.Errorf("metadata %v holds the raw email", r.Metadata)
		}
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRegisterExistingEmailLooksTheSameAndChangesNothing(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)

	first, firstBody := b.register(email, "Zq9!kVx1")
	if first.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("first register = %d", first.StatusCode)
	}
	before, _ := h.user(t, email)
	h.clock.Advance(61 * time.Second) // past the per-email cooldown

	second, secondBody := b.register(email, "Other!Pass9")
	if second.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("second register = %d %s, want 202 like a new email", second.StatusCode, secondBody)
	}
	if stripJSONField(t, firstBody, "challenge_id") != stripJSONField(t, secondBody, "challenge_id") {
		t.Errorf("bodies differ beyond the challenge ID:\n%s\n%s", firstBody, secondBody)
	}
	if decodeAccepted(t, firstBody).ChallengeID == decodeAccepted(t, secondBody).ChallengeID {
		t.Error("the decoy challenge ID must be fresh, not the real one")
	}
	after, _ := h.user(t, email)
	if after.PasswordHash != before.PasswordHash || len(h.challenges(t, before.ID)) != 1 {
		t.Error("registering an existing email must not change the account or add a challenge")
	}
	if h.mailer.attemptCount(email) != 1 || h.mailer.verificationCount(email) != 1 {
		t.Errorf("mails: %d attempt notices and %d codes, want 1 and 1", h.mailer.attemptCount(email), h.mailer.verificationCount(email))
	}
	if rows := h.auditFor(t, second.Header.Get("X-Request-ID")); len(rows) != 0 {
		t.Errorf("an existing email must not write audit, got %v", eventTypes(rows))
	}
}

func TestRegisterTreatsEmailCaseAsTheSameAccount(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	mixed := "Case-" + mustTestID(t).String() + "@Example.COM"
	lower := strings.ToLower(mixed)

	if resp, body := b.register(mixed, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("register = %d %s", resp.StatusCode, body)
	}
	if _, ok := h.user(t, lower); !ok {
		t.Fatal("the account must be stored under the lower-cased email")
	}
	h.clock.Advance(61 * time.Second)
	if resp, _ := b.register(lower, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("second register = %d", resp.StatusCode)
	}
	if h.mailer.attemptCount(lower) != 1 {
		t.Error("the lower-case spelling must reach the existing account, not create a second one")
	}
}

func TestRegisterSameEmailConcurrentlyCreatesOneAccount(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	browsers := []*browser{h.newBrowser(t), h.newBrowser(t), h.newBrowser(t)}
	for _, b := range browsers {
		b.fetchCSRF()
	}

	var wg sync.WaitGroup
	statuses := make([]int, len(browsers))
	errs := make([]error, len(browsers))
	for i, b := range browsers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _, err := b.tryDo(nethttp.MethodPost, "/api/v1/auth/register", map[string]string{"email": email, "password": "Zq9!kVx1"}, nil)
			statuses[i], errs[i] = resp.StatusCode, err
		}()
	}
	wg.Wait()

	accepted := 0
	for i, s := range statuses {
		if errs[i] != nil {
			t.Fatalf("request %d: %v", i, errs[i])
		}
		switch s {
		case nethttp.StatusAccepted:
			accepted++
		case nethttp.StatusTooManyRequests: // the per-email cooldown serializes the attempts
		default:
			t.Errorf("request %d = %d, want 202 or 429", i, s)
		}
	}
	if accepted == 0 {
		t.Error("no registration was accepted")
	}
	var n int
	if err := h.admin.QueryRow(t.Context(), `SELECT count(*) FROM app.users WHERE email = $1`, email).Scan(&n); err != nil || n != 1 {
		t.Errorf("accounts for the email = %d (%v), want 1", n, err)
	}
}

func TestRegisterRejectsAWeakPasswordWithEveryReason(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)

	resp, body := b.register(email, "abc")
	got := decodeError(t, body)
	if resp.StatusCode != nethttp.StatusBadRequest || got.Code != "invalid_input" ||
		!slices.Equal(got.Details, []string{"password_too_short", "password_missing_uppercase", "password_missing_special"}) {
		t.Errorf("weak password = %d %+v", resp.StatusCode, got)
	}

	_, body = b.register(email, "P@ssw0rd")
	if got := decodeError(t, body); !slices.Equal(got.Details, []string{"password_too_common"}) {
		t.Errorf("common password details = %v, want [password_too_common]", got.Details)
	}
	if _, ok := h.user(t, email); ok || h.mailer.verificationCount(email) != 0 {
		t.Error("a refused password must create nothing and send nothing")
	}
}

func TestRegisterNeedsCSRFAndOrigin(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.fetchCSRF()
	payload := map[string]string{"email": email, "password": "Zq9!kVx1"}

	for name, extra := range map[string]map[string]string{
		"no token":     {"X-CSRF-Token": ""},
		"other origin": {"Origin": "http://evil.example"},
		"no origin":    {"Origin": ""},
	} {
		resp, body := b.do(nethttp.MethodPost, "/api/v1/auth/register", payload, extra)
		if resp.StatusCode != nethttp.StatusForbidden || decodeError(t, body).Code != "csrf_failed" {
			t.Errorf("%s: %d %s, want 403 csrf_failed", name, resp.StatusCode, body)
		}
	}
	if _, ok := h.user(t, email); ok {
		t.Error("a request refused by CSRF must not create an account")
	}
}

func TestRegisterRejectsMalformedJSON(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	b.fetchCSRF()
	for name, raw := range map[string]string{
		"unknown field": `{"email":"a@example.com","password":"Zq9!kVx1","admin":true}`,
		"trailing data": `{"email":"a@example.com","password":"Zq9!kVx1"} {}`,
		"wrong type":    `{"email":5,"password":"Zq9!kVx1"}`,
		"not json":      `email=a@example.com`,
	} {
		resp, body := b.do(nethttp.MethodPost, "/api/v1/auth/register", raw, nil)
		if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "malformed_json" {
			t.Errorf("%s: %d %s, want 400 malformed_json", name, resp.StatusCode, body)
		}
	}
}

func TestRegisterIsRateLimitedPerEmailAndAuditsTheScope(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	if resp, _ := b.register(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("first register = %d", resp.StatusCode)
	}

	resp, body := b.register(email, "Zq9!kVx1")
	if resp.StatusCode != nethttp.StatusTooManyRequests || decodeError(t, body).Code != "rate_limited" {
		t.Fatalf("second register within the cooldown = %d %s, want 429", resp.StatusCode, body)
	}
	wait, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || wait < 1 || wait > 60 {
		t.Errorf("Retry-After = %q, want 1..60 seconds", resp.Header.Get("Retry-After"))
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "security.rate_limited" || rows[0].Outcome != "denied" ||
		rows[0].Metadata["scope"] != "email_verification_request_email" || rows[0].Actor != "" {
		t.Errorf("audit = %+v, want one security.rate_limited denial naming the scope", rows)
	}

	again, _ := b.register(email, "Zq9!kVx1")
	if again.StatusCode != nethttp.StatusTooManyRequests {
		t.Fatalf("third register = %d, want 429", again.StatusCode)
	}
	if rows := h.auditFor(t, again.Header.Get("X-Request-ID")); len(rows) != 0 {
		t.Errorf("a key is audited at most once a minute, got %v", eventTypes(rows))
	}
}

func TestRegisterTakesAtLeastTheResponseFloor(t *testing.T) {
	h := newAuthHarness(t, withResponseFloor(300*time.Millisecond))
	b := h.newBrowser(t)
	b.fetchCSRF()
	start := time.Now()
	resp, _ := b.do(nethttp.MethodPost, "/api/v1/auth/register", map[string]string{"email": uniqueEmail(t), "password": "Zq9!kVx1"}, nil)
	if resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("register = %d", resp.StatusCode)
	}
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Errorf("register answered after %v, want at least the 300ms floor", elapsed)
	}
}
```

สร้าง `internal/adapter/http/auth_contract_test.go`:

```go
package http

import (
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
	{"post", "/api/v1/auth/register", []int{202, 400, 403, 429, 503}},
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
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/adapter/http/ -count=1` แล้ว `make test-integration` (ต้องให้ `docker compose ps` แสดง postgres ทำงานอยู่)
Expected: build fail — `undefined: OTPAcceptedResponse`, `AuthOptions.Passwords`, `service.Auth.Register`

- [ ] **Step 3: เขียนโค้ด**

แก้ `internal/core/port/auth_service.go` — แทน

```go
	Authenticate(ctx context.Context, token string) (domain.Principal, error)
}
```

ด้วย

```go
	Authenticate(ctx context.Context, token string) (domain.Principal, error)
	// Register creates an unverified account and sends it a verification code, or tells the owner
	// of an existing account that someone tried to register; the answer is the same either way.
	Register(ctx context.Context, in domain.RegisterInput, meta domain.RequestMeta) (domain.OTPReceipt, error)
}
```

สร้าง `internal/core/service/register.go`:

```go
package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// otpLifetime is how long a verification code stays valid (phase 1 spec section 10).
const otpLifetime = 10 * time.Minute

// Register implements port.AuthService. A new email gets an unverified account, a challenge and a
// code. An email that already has an account (verified or not, Google-only included) changes
// nothing and gets a notice instead. Both paths hash the password and take the same time (Pacer),
// so the response does not reveal which it was (ADR-0006, ADR-0014).
func (a *Auth) Register(ctx context.Context, in domain.RegisterInput, meta domain.RequestMeta) (domain.OTPReceipt, error) {
	defer a.pacer.Wait(ctx, a.clock.Now())

	rules, err := a.limits.Signup(in.Email, meta.ClientIP)
	if err != nil {
		return domain.OTPReceipt{}, err
	}
	if err := a.admit(ctx, rules, meta, domain.ID{}); err != nil {
		return domain.OTPReceipt{}, err
	}
	hash, err := a.hasher.Hash(ctx, in.Password)
	if err != nil {
		return domain.OTPReceipt{}, err
	}

	now := NowForStorage(a.clock)
	userID, err := domain.NewID()
	if err != nil {
		return domain.OTPReceipt{}, err
	}
	code, challenge, err := a.newVerificationChallenge(userID, now)
	if err != nil {
		return domain.OTPReceipt{}, err
	}
	user := domain.User{ID: userID, Email: in.Email, CreatedAt: now, UpdatedAt: now}

	existing := false
	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		if err := tx.Users().Insert(ctx, user); err != nil {
			if errors.Is(err, domain.ErrIdentityConflict) {
				existing = true // nothing is written for an account that already exists
				return nil
			}
			return err
		}
		if err := tx.Users().SetPassword(ctx, user.ID, hash, now); err != nil {
			return err
		}
		if err := tx.OTPChallenges().Insert(ctx, challenge); err != nil {
			return err
		}
		if err := a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthRegistered, Outcome: domain.AuditOutcomeSuccess,
			TargetType: "user", TargetID: user.ID.String(), Method: "password",
		}); err != nil {
			return err
		}
		return a.appendAudit(ctx, tx, meta, verificationRequested(user.ID))
	})
	if err != nil {
		return domain.OTPReceipt{}, err
	}

	if existing {
		a.send(meta, "registration_attempt", func() error { return a.mailer.SendRegistrationAttempt(ctx, in.Email) })
	} else {
		a.send(meta, "email_verification", func() error {
			return a.mailer.SendEmailVerification(ctx, domain.OTPMail{To: in.Email, OTP: code, ChallengeID: challenge.ID, ExpiresAt: challenge.ExpiresAt})
		})
	}
	// For an existing account the challenge was never stored: its ID is a decoy of the same shape.
	return domain.OTPReceipt{ChallengeID: challenge.ID, ExpiresIn: otpLifetime}, nil
}

// newVerificationChallenge draws a code and builds the challenge that stores only its digest.
func (a *Auth) newVerificationChallenge(userID domain.ID, now time.Time) (domain.OTPCode, domain.OTPChallenge, error) {
	id, err := domain.NewID()
	if err != nil {
		return domain.OTPCode{}, domain.OTPChallenge{}, err
	}
	code, err := GenerateOTP()
	if err != nil {
		return domain.OTPCode{}, domain.OTPChallenge{}, err
	}
	challenge, err := domain.NewOTPChallenge(domain.OTPChallenge{
		ID: id, UserID: userID, Purpose: domain.OTPPurposeEmailVerification,
		OTPHash:   a.otp.Digest(domain.OTPPurposeEmailVerification, id, userID, code),
		CreatedAt: now, ExpiresAt: now.Add(otpLifetime),
	})
	if err != nil {
		return domain.OTPCode{}, domain.OTPChallenge{}, err
	}
	return code, challenge, nil
}

func verificationRequested(userID domain.ID) auditSpec {
	return auditSpec{
		Type: domain.EventAuthEmailVerificationRequested, Outcome: domain.AuditOutcomeSuccess,
		TargetType: "user", TargetID: userID.String(),
	}
}

// send hands an email to the mailer after the transaction committed. A failure is logged without
// the address, the code or the error text (which may repeat the address) and never changes the
// answer: the user can ask for a new code after the cooldown.
func (a *Auth) send(meta domain.RequestMeta, kind string, deliver func() error) {
	if err := deliver(); err != nil {
		a.log.Warn("could not hand an email to the mailer",
			slog.String("event", "auth.mail_not_sent"),
			slog.String("request_id", meta.RequestID.String()),
			slog.String("kind", kind))
	}
}
```

สร้าง `internal/adapter/http/decode.go`:

```go
package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/core/domain"
)

var errMalformedJSON = domain.NewError(domain.CodeMalformedJSON, "request body must be one JSON object with only the documented fields")

// decodeJSON reads the body into dst: exactly one JSON value, no unknown fields, nothing after it
// (phase 1 spec section 11). A missing value is left empty for the Parse* functions to reject.
func decodeJSON(c fiber.Ctx, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(c.Body()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errMalformedJSON
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errMalformedJSON
	}
	return nil
}
```

ต่อท้าย `internal/adapter/http/auth_dto.go` ด้วย (เพิ่ม `"go-backend-stock/internal/core/domain"` ใน import ด้วย):

```go

// otpAcceptedMessage is the same for every outcome, so it reveals nothing about the account.
const otpAcceptedMessage = "If this address can receive email, a verification code has been sent. It expires in 10 minutes."

// RegisterRequest is the body of POST /api/v1/auth/register.
type RegisterRequest struct {
	Email    string `json:"email" example:"user@example.com"`
	Password string `json:"password" example:"Zq9!kVx1"`
}

// OTPAcceptedResponse answers register and "send the code again" with the same shape every time.
type OTPAcceptedResponse struct {
	Message     string `json:"message" example:"If this address can receive email, a verification code has been sent. It expires in 10 minutes."`
	ChallengeID string `json:"challenge_id" example:"0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d"`
	ExpiresIn   int    `json:"expires_in" example:"600"`
}

func newOTPAccepted(r domain.OTPReceipt) OTPAcceptedResponse {
	return OTPAcceptedResponse{Message: otpAcceptedMessage, ChallengeID: r.ChallengeID.String(), ExpiresIn: int(r.ExpiresIn / time.Second)}
}
```

แก้ `internal/adapter/http/auth.go` ตามคู่ต่อไปนี้:

1. เพิ่ม `"go-backend-stock/internal/core/domain"` ในบล็อก import
2. แทน

```go
// AuthOptions wires the auth endpoints.
```

   ด้วย

```go
// PasswordParser applies the whole password policy (service.PasswordPolicy implements it).
type PasswordParser interface {
	Parse(raw string, email domain.Email) (domain.Password, error)
}

// AuthOptions wires the auth endpoints.
```

3. แทน

```go
	Service       port.AuthService
	CSRF          CSRFTokens
```

   ด้วย

```go
	Service       port.AuthService
	Passwords     PasswordParser
	CSRF          CSRFTokens
```

4. แทน

```go
type authHandlers struct {
	auth     port.AuthService
```

   ด้วย

```go
type authHandlers struct {
	auth      port.AuthService
	passwords PasswordParser
```

5. แทน

```go
	h := &authHandlers{auth: opts.Service, csrf: opts.CSRF, clientIP: resolver, cookies: newCookieJar(opts.SecureCookies)}
```

   ด้วย

```go
	h := &authHandlers{auth: opts.Service, passwords: opts.Passwords, csrf: opts.CSRF, clientIP: resolver, cookies: newCookieJar(opts.SecureCookies)}
```

6. แทน

```go
	app.Get("/api/v1/auth/csrf", session, h.csrfToken)
}
```

   ด้วย

```go
	csrf := middleware.CSRF(opts.CSRF, opts.PublicOrigin, h.cookies.nonceName)

	app.Get("/api/v1/auth/csrf", session, h.csrfToken)
	app.Post("/api/v1/auth/register", session, csrf, h.register)
}
```

สร้าง `internal/adapter/http/register.go`:

```go
package http

import (
	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/core/domain"
)

// Register godoc
// @Summary      Register with an email and a password
// @Description  Creates an unverified account and emails a 6-digit verification code. The answer is the same whether or not the email already has an account: the owner of an existing account gets an email notice instead and nothing changes. The response takes at least about half a second on purpose. Needs the pre-login CSRF flow: call GET /api/v1/auth/csrf first, send its token in X-CSRF-Token, and send the csrf_nonce cookie (a browser does this itself). Then confirm the code with POST /api/v1/auth/email-verification/confirm.
// @Tags         Auth
// @ID           register
// @Accept       json
// @Produce      json
// @Param        X-CSRF-Token  header  string           true  "Token from GET /api/v1/auth/csrf"
// @Param        body          body    RegisterRequest  true  "Email, and a password of 6-32 printable ASCII characters without spaces, with a lower-case letter, an upper-case letter and a special character"
// @Success      202  {object}  OTPAcceptedResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input (details lists every broken password rule) or malformed_json"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      429  {object}  ErrorResponse  "rate_limited; wait Retry-After seconds"
// @Failure      503  {object}  ErrorResponse  "server_busy; wait Retry-After seconds"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Header       503  {integer}  Retry-After  "Seconds to wait"
// @Router       /api/v1/auth/register [post]
func (h *authHandlers) register(c fiber.Ctx) error {
	var req RegisterRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	email, err := domain.ParseEmail(req.Email)
	if err != nil {
		return err
	}
	password, err := h.passwords.Parse(req.Password, email)
	if err != nil {
		return err
	}
	meta, err := h.requestMeta(c)
	if err != nil {
		return err
	}
	receipt, err := h.auth.Register(c.Context(), domain.RegisterInput{Email: email, Password: password}, meta)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(newOTPAccepted(receipt))
}
```

- [ ] **Step 4: รัน test ให้ผ่าน และ generate Swagger**

Run:
```bash
gofmt -l internal cmd; go vet ./... && make swagger && go test ./internal/... -count=1 -race && make test-integration && make lint
```
Expected: ทั้งหมดผ่าน; `make test-integration` แสดง test ของ `internal/adapter/http` ที่ชื่อ `TestRegister...` ทำงาน (ไม่ SKIP); `TestAuthOperationsAreDocumented` ผ่านหลัง `make swagger`

- [ ] **Step 5: Commit**

```bash
git add Makefile internal/core/port/auth_service.go internal/core/service/register.go internal/adapter/http docs/api
git commit -m "feat(auth): register with email and password behind CSRF, rate limits and a timing floor"
```

---

### Task 10: ขอรหัสยืนยันใหม่และยืนยันอีเมล

**Files:**
- Create: `internal/core/service/email_verification.go`, `internal/adapter/http/email_verification.go`, `internal/adapter/http/email_verification_test.go`
- Modify: `internal/core/port/auth_service.go`, `internal/adapter/http/auth.go`, `internal/adapter/http/auth_dto.go`, `internal/adapter/http/auth_harness_test.go`, `internal/adapter/http/auth_contract_test.go`, `docs/api/` (generated)

**Interfaces:**
- Consumes: `newVerificationChallenge`, `verificationRequested`, `send`, `otpLifetime` (Task 9), `admit`, `appendAudit`, `recordFailure`; `RateLimitPolicy.EmailVerificationRequest`/`EmailVerificationConfirm`; `OTPCodec.Verify`; repositories `Users().CredentialsByEmail`/`Lock`/`MarkEmailVerified`, `OTPChallenges().ByID`/`Lock`/`InvalidateOpen`/`Insert`/`RecordFailure`/`Consume`; harness ทั้งหมดจาก Task 9
- Produces:
  - `port.EmailVerificationService{Request(ctx, email domain.Email, meta) (domain.OTPReceipt, error); Confirm(ctx, in domain.VerifyEmailInput, meta) error}`; `*service.Auth` implement
  - reason ของ `auth.email_verification_failed`: `challenge_not_found`, `challenge_closed`, `challenge_expired`, `too_many_attempts`, `otp_mismatch` (target = challenge ID, actor NULL); `auth.email_verified` มี actor = user
  - HTTP: `AuthOptions.Verification`, DTO `EmailRequest`, `ConfirmEmailRequest`; `POST /api/v1/auth/email-verification/request` → 202, `POST /api/v1/auth/email-verification/confirm` → 204
  - browser helpers `requestCode(email)`, `confirm(challengeID, otp)`, `signUpAndVerify(email, password)` (ใช้ต่อใน Task 11–12)

- [ ] **Step 1: เขียน failing tests**

แก้ `internal/adapter/http/auth_harness_test.go`:

1. แทน

```go
		Service: auth, Passwords: policy, CSRF: csrf,
```

   ด้วย

```go
		Service: auth, Verification: auth, Passwords: policy, CSRF: csrf,
```

2. ต่อท้ายไฟล์ด้วย

```go

func (b *browser) requestCode(email string) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/email-verification/request", map[string]string{"email": email}, nil)
}

func (b *browser) confirm(challengeID, otp string) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/email-verification/confirm", map[string]string{"challenge_id": challengeID, "otp": otp}, nil)
}

// signUpAndVerify registers email and confirms it with the code the fake mailer received.
func (b *browser) signUpAndVerify(email, password string) {
	b.t.Helper()
	if resp, body := b.register(email, password); resp.StatusCode != nethttp.StatusAccepted {
		b.t.Fatalf("register = %d %s", resp.StatusCode, body)
	}
	mail := b.h.mailer.lastVerification(b.t, email)
	if resp, body := b.confirm(mail.ChallengeID.String(), mail.OTP.Reveal()); resp.StatusCode != nethttp.StatusNoContent {
		b.t.Fatalf("confirm = %d %s", resp.StatusCode, body)
	}
}

// wrongCode returns a 6-digit code that differs from code.
func wrongCode(code string) string {
	first := code[0] - '0'
	return string(rune('0'+(first+1)%10)) + code[1:]
}
```

แก้ `internal/adapter/http/auth_contract_test.go` — แทน

```go
	{"post", "/api/v1/auth/register", []int{202, 400, 403, 429, 503}},
}
```

ด้วย

```go
	{"post", "/api/v1/auth/register", []int{202, 400, 403, 429, 503}},
	{"post", "/api/v1/auth/email-verification/request", []int{202, 400, 403, 429}},
	{"post", "/api/v1/auth/email-verification/confirm", []int{204, 400, 403, 429}},
}
```

สร้าง `internal/adapter/http/email_verification_test.go`:

```go
package http

import (
	"crypto/rand"
	nethttp "net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func failureReasons(rows []auditRow) []string {
	var out []string
	for _, r := range rows {
		if r.EventType == "auth.email_verification_failed" {
			out = append(out, r.Reason)
		}
	}
	return out
}

func TestConfirmVerifiesTheEmailOnce(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	if resp, _ := b.register(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusAccepted {
		t.Fatal("register failed")
	}
	mail := h.mailer.lastVerification(t, email)
	u, _ := h.user(t, email)

	resp, body := b.confirm(mail.ChallengeID.String(), mail.OTP.Reveal())
	if resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("confirm = %d %s, want 204", resp.StatusCode, body)
	}
	if after, _ := h.user(t, email); !after.Verified {
		t.Error("the email must be verified")
	}
	if ch := h.challenges(t, u.ID); ch[0].Open {
		t.Error("the challenge must be consumed")
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "auth.email_verified" || rows[0].Actor != u.ID || rows[0].Outcome != "success" {
		t.Errorf("audit = %+v, want auth.email_verified by the user", rows)
	}
	if b.jar["session"] != "" {
		t.Error("confirming must not sign the user in")
	}

	again, body := b.confirm(mail.ChallengeID.String(), mail.OTP.Reveal())
	if again.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_verification" {
		t.Errorf("second confirm = %d %s, want 400 invalid_verification", again.StatusCode, body)
	}
	if got := failureReasons(h.auditFor(t, again.Header.Get("X-Request-ID"))); len(got) != 1 || got[0] != "challenge_closed" {
		t.Errorf("failure reasons = %v, want [challenge_closed]", got)
	}
}

func TestConfirmWrongCodeCountsAndAuditsEveryAttempt(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.register(email, "Zq9!kVx1")
	mail := h.mailer.lastVerification(t, email)
	u, _ := h.user(t, email)
	wrong := wrongCode(mail.OTP.Reveal())

	for i := 1; i <= 5; i++ {
		resp, body := b.confirm(mail.ChallengeID.String(), wrong)
		if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_verification" {
			t.Fatalf("wrong attempt %d = %d %s", i, resp.StatusCode, body)
		}
		if got := h.challenges(t, u.ID)[0].FailedAttempts; got != i {
			t.Fatalf("failed_attempts after %d wrong codes = %d", i, got)
		}
	}
	resp, _ := b.confirm(mail.ChallengeID.String(), mail.OTP.Reveal())
	if resp.StatusCode != nethttp.StatusBadRequest {
		t.Errorf("the right code after 5 wrong ones = %d, want 400 (the challenge is exhausted)", resp.StatusCode)
	}
	reasons := failureReasons(h.auditByTarget(t, mail.ChallengeID.String()))
	if len(reasons) != 6 || reasons[0] != "otp_mismatch" || reasons[5] != "too_many_attempts" {
		t.Errorf("failure reasons = %v, want 5 x otp_mismatch then too_many_attempts", reasons)
	}
	for _, r := range h.auditByTarget(t, mail.ChallengeID.String()) {
		if r.Actor != "" {
			t.Errorf("a failed verification must have actor NULL, got %s", r.Actor)
		}
	}
	if after, _ := h.user(t, email); after.Verified {
		t.Error("the email must stay unverified")
	}
}

func TestConfirmExpiredCodeIsRefused(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.register(email, "Zq9!kVx1")
	mail := h.mailer.lastVerification(t, email)

	h.clock.Advance(10 * time.Minute) // exactly at expires_at the code is over
	resp, _ := b.confirm(mail.ChallengeID.String(), mail.OTP.Reveal())
	if resp.StatusCode != nethttp.StatusBadRequest {
		t.Fatalf("expired code = %d, want 400", resp.StatusCode)
	}
	if got := failureReasons(h.auditFor(t, resp.Header.Get("X-Request-ID"))); len(got) != 1 || got[0] != "challenge_expired" {
		t.Errorf("failure reasons = %v, want [challenge_expired]", got)
	}
}

func TestConfirmUnknownOrPasswordResetChallengeIsRefused(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.register(email, "Zq9!kVx1")
	u, _ := h.user(t, email)

	// A password-reset challenge of the same user: its ID must never work for email verification.
	userID, err := domain.ParseID("user_id", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	resetID := mustTestID(t)
	hash := make([]byte, domain.HashSize)
	if _, err := rand.Read(hash); err != nil {
		t.Fatal(err)
	}
	now := h.clock.Now()
	reset, err := domain.NewOTPChallenge(domain.OTPChallenge{ID: resetID, UserID: userID, Purpose: domain.OTPPurposePasswordReset,
		OTPHash: hash, CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.OTPChallenges().Insert(t.Context(), reset); err != nil {
		t.Fatal(err)
	}

	for name, id := range map[string]string{"unknown": mustTestID(t).String(), "password reset": resetID.String()} {
		resp, body := b.confirm(id, "123456")
		if resp.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_verification" {
			t.Errorf("%s: %d %s, want 400 invalid_verification", name, resp.StatusCode, body)
		}
		if got := failureReasons(h.auditFor(t, resp.Header.Get("X-Request-ID"))); len(got) != 1 || got[0] != "challenge_not_found" {
			t.Errorf("%s: failure reasons = %v, want [challenge_not_found]", name, got)
		}
	}
}

func TestConfirmRejectsNonASCIIDigitsWithoutCountingAnAttempt(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.register(email, "Zq9!kVx1")
	mail := h.mailer.lastVerification(t, email)
	u, _ := h.user(t, email)

	for _, otp := range []string{"๑๒๓๔๕๖", " 12345", "12345", "1234567"} {
		resp, body := b.confirm(mail.ChallengeID.String(), otp)
		got := decodeError(t, body)
		if resp.StatusCode != nethttp.StatusBadRequest || got.Code != "invalid_input" || !strings.HasPrefix(got.Message, "otp") {
			t.Errorf("otp %q = %d %+v, want 400 invalid_input about otp", otp, resp.StatusCode, got)
		}
	}
	if got := h.challenges(t, u.ID)[0].FailedAttempts; got != 0 {
		t.Errorf("failed_attempts = %d, want 0: a code that is not 6 ASCII digits is not an attempt", got)
	}
	resp, body := b.confirm("not-a-uuid", "123456")
	if resp.StatusCode != nethttp.StatusBadRequest || !strings.HasPrefix(decodeError(t, body).Message, "challenge_id") {
		t.Errorf("bad challenge_id = %d %s", resp.StatusCode, body)
	}
}

// Concurrent wrong codes must all be counted: Lock takes the challenge row FOR UPDATE (review of 3b).
func TestConcurrentWrongCodesAreAllCounted(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	owner := h.newBrowser(t)
	owner.register(email, "Zq9!kVx1")
	mail := h.mailer.lastVerification(t, email)
	u, _ := h.user(t, email)
	wrong := wrongCode(mail.OTP.Reveal())

	browsers := make([]*browser, 5)
	for i := range browsers {
		browsers[i] = h.newBrowser(t)
		browsers[i].fetchCSRF()
	}
	var wg sync.WaitGroup
	statuses := make([]int, len(browsers))
	for i, b := range browsers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _, err := b.tryDo(nethttp.MethodPost, "/api/v1/auth/email-verification/confirm",
				map[string]string{"challenge_id": mail.ChallengeID.String(), "otp": wrong}, nil)
			if err == nil {
				statuses[i] = resp.StatusCode
			}
		}()
	}
	wg.Wait()
	for i, s := range statuses {
		if s != nethttp.StatusBadRequest {
			t.Errorf("request %d = %d, want 400", i, s)
		}
	}
	if got := h.challenges(t, u.ID)[0].FailedAttempts; got != 5 {
		t.Errorf("failed_attempts = %d, want 5", got)
	}
	if got := failureReasons(h.auditByTarget(t, mail.ChallengeID.String())); len(got) != 5 {
		t.Errorf("failure audits = %d, want 5", len(got))
	}
}

func TestRequestSendsANewCodeAndInvalidatesTheOld(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.register(email, "Zq9!kVx1")
	old := h.mailer.lastVerification(t, email)
	u, _ := h.user(t, email)
	h.clock.Advance(61 * time.Second)

	resp, body := b.requestCode(email)
	if resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("request = %d %s", resp.StatusCode, body)
	}
	out := decodeAccepted(t, body)
	fresh := h.mailer.lastVerification(t, email)
	if fresh.ChallengeID.String() != out.ChallengeID || fresh.ChallengeID == old.ChallengeID {
		t.Fatalf("the new email must carry the new challenge from the response")
	}
	ch := h.challenges(t, u.ID)
	if len(ch) != 2 || ch[0].Open || !ch[1].Open {
		t.Errorf("challenges = %+v, want the old one closed and the new one open", ch)
	}
	if rows := h.auditFor(t, resp.Header.Get("X-Request-ID")); len(rows) != 1 || rows[0].EventType != "auth.email_verification_requested" {
		t.Errorf("audit = %v", eventTypes(rows))
	}
	if resp, _ := b.confirm(old.ChallengeID.String(), old.OTP.Reveal()); resp.StatusCode != nethttp.StatusBadRequest {
		t.Error("the old code must stop working")
	}
	if resp, _ := b.confirm(fresh.ChallengeID.String(), fresh.OTP.Reveal()); resp.StatusCode != nethttp.StatusNoContent {
		t.Error("the new code must work")
	}
}

func TestRequestAnswersTheSameForEveryOtherEmail(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)

	verified := uniqueEmail(t)
	b.signUpAndVerify(verified, "Zq9!kVx1")
	googleOnly := uniqueEmail(t)
	gEmail, err := domain.ParseEmail(googleOnly)
	if err != nil {
		t.Fatal(err)
	}
	now := h.clock.Now()
	if err := h.store.Users().Insert(t.Context(), domain.User{ID: mustTestID(t), Email: gEmail, EmailVerifiedAt: &now, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	unverified := uniqueEmail(t)
	b.register(unverified, "Zq9!kVx1")
	h.clock.Advance(61 * time.Second)

	bodies := map[string]string{}
	for name, email := range map[string]string{"unknown": uniqueEmail(t), "verified": verified, "google-only": googleOnly, "unverified": unverified} {
		before := h.mailer.verificationCount(email)
		resp, body := b.requestCode(email)
		if resp.StatusCode != nethttp.StatusAccepted {
			t.Fatalf("%s: %d %s", name, resp.StatusCode, body)
		}
		decodeAccepted(t, body)
		bodies[name] = stripJSONField(t, body, "challenge_id")
		sent := h.mailer.verificationCount(email) - before
		if wantSent := map[bool]int{true: 1, false: 0}[name == "unverified"]; sent != wantSent {
			t.Errorf("%s: %d codes sent, want %d", name, sent, wantSent)
		}
	}
	for name, body := range bodies {
		if body != bodies["unknown"] {
			t.Errorf("%s body %s differs from the unknown-email body %s", name, body, bodies["unknown"])
		}
	}
}

func TestRequestShareTheRegisterCooldown(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.register(email, "Zq9!kVx1")
	resp, body := b.requestCode(email)
	if resp.StatusCode != nethttp.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Errorf("request right after register = %d %s, want 429 with Retry-After", resp.StatusCode, body)
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/adapter/http/ -count=1`
Expected: build fail — `unknown field Verification in struct literal of type AuthOptions`

- [ ] **Step 3: เขียนโค้ด**

แก้ `internal/core/port/auth_service.go` — ต่อท้ายไฟล์ด้วย

```go

// EmailVerificationService confirms that the owner of a local account controls its email address.
type EmailVerificationService interface {
	// Request sends a new code to an unverified local account and does nothing for any other email;
	// the answer is the same either way.
	Request(ctx context.Context, email domain.Email, meta domain.RequestMeta) (domain.OTPReceipt, error)
	// Confirm checks the code. A wrong, expired, used or exhausted challenge returns
	// domain.ErrInvalidVerification; a wrong code is counted and audited before that error returns.
	Confirm(ctx context.Context, in domain.VerifyEmailInput, meta domain.RequestMeta) error
}
```

สร้าง `internal/core/service/email_verification.go`:

```go
package service

import (
	"context"
	"errors"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

var _ port.EmailVerificationService = (*Auth)(nil)

// Request implements port.EmailVerificationService. Only an unverified account with a password gets
// a new code (the old one is invalidated in the same transaction); an unknown, verified or
// Google-only email gets the same answer with a decoy challenge ID and nothing is written or sent.
func (a *Auth) Request(ctx context.Context, email domain.Email, meta domain.RequestMeta) (domain.OTPReceipt, error) {
	defer a.pacer.Wait(ctx, a.clock.Now())

	rules, err := a.limits.EmailVerificationRequest(email, meta.ClientIP)
	if err != nil {
		return domain.OTPReceipt{}, err
	}
	if err := a.admit(ctx, rules, meta, domain.ID{}); err != nil {
		return domain.OTPReceipt{}, err
	}
	decoy, err := domain.NewID()
	if err != nil {
		return domain.OTPReceipt{}, err
	}
	receipt := domain.OTPReceipt{ChallengeID: decoy, ExpiresIn: otpLifetime}

	creds, err := a.store.Users().CredentialsByEmail(ctx, email)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return receipt, nil
	case err != nil:
		return domain.OTPReceipt{}, err
	case !creds.HasPassword() || creds.User.EmailVerifiedAt != nil:
		return receipt, nil
	}

	now := NowForStorage(a.clock)
	code, challenge, err := a.newVerificationChallenge(creds.User.ID, now)
	if err != nil {
		return domain.OTPReceipt{}, err
	}
	issued := false
	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		user, err := tx.Users().Lock(ctx, creds.User.ID)
		if err != nil {
			return err
		}
		if user.EmailVerifiedAt != nil {
			return nil // verified meanwhile: answer like any other verified account
		}
		// The user row lock makes this and the insert atomic against a concurrent request.
		if err := tx.OTPChallenges().InvalidateOpen(ctx, user.ID, domain.OTPPurposeEmailVerification, now); err != nil {
			return err
		}
		if err := tx.OTPChallenges().Insert(ctx, challenge); err != nil {
			return err
		}
		issued = true
		return a.appendAudit(ctx, tx, meta, verificationRequested(user.ID))
	})
	if err != nil {
		return domain.OTPReceipt{}, err
	}
	if !issued {
		return receipt, nil
	}
	a.send(meta, "email_verification", func() error {
		return a.mailer.SendEmailVerification(ctx, domain.OTPMail{To: email, OTP: code, ChallengeID: challenge.ID, ExpiresAt: challenge.ExpiresAt})
	})
	return domain.OTPReceipt{ChallengeID: challenge.ID, ExpiresIn: otpLifetime}, nil
}

// Confirm implements port.EmailVerificationService (phase 1 spec section 9, milestone 4 design
// section 5). A failed attempt is audited and, for a wrong code, counted in the same transaction,
// which commits before the error is returned, so neither is lost with the failure.
func (a *Auth) Confirm(ctx context.Context, in domain.VerifyEmailInput, meta domain.RequestMeta) error {
	rules, err := a.limits.EmailVerificationConfirm(meta.ClientIP)
	if err != nil {
		return err
	}
	if err := a.admit(ctx, rules, meta, domain.ID{}); err != nil {
		return err
	}

	found, err := a.store.OTPChallenges().ByID(ctx, domain.OTPPurposeEmailVerification, in.ChallengeID)
	if errors.Is(err, domain.ErrNotFound) {
		// An unknown ID, or one issued for password reset (ByID filters by purpose).
		if err := a.recordFailure(ctx, meta, verificationFailed(in.ChallengeID, "challenge_not_found")); err != nil {
			return err
		}
		return domain.ErrInvalidVerification
	}
	if err != nil {
		return err
	}

	var failure string
	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		failure = ""
		// Lock order: user, then challenge — the same as every other use case on these rows.
		if _, err := tx.Users().Lock(ctx, found.UserID); err != nil {
			return err
		}
		c, err := tx.OTPChallenges().Lock(ctx, domain.OTPPurposeEmailVerification, found.ID)
		if err != nil {
			return err
		}
		now := NowForStorage(a.clock)
		switch {
		case c.ConsumedAt != nil || c.InvalidatedAt != nil:
			failure = "challenge_closed"
		case !now.Before(c.ExpiresAt):
			failure = "challenge_expired"
		case c.FailedAttempts >= a.otpMaxAttempts:
			failure = "too_many_attempts"
		case !a.otp.Verify(c.OTPHash, domain.OTPPurposeEmailVerification, c.ID, c.UserID, in.OTP):
			failure = "otp_mismatch"
			if err := tx.OTPChallenges().RecordFailure(ctx, domain.OTPPurposeEmailVerification, c.ID); err != nil {
				return err
			}
		}
		if failure != "" {
			return a.appendAudit(ctx, tx, meta, verificationFailed(c.ID, failure))
		}
		if err := tx.OTPChallenges().Consume(ctx, domain.OTPPurposeEmailVerification, c.ID, now); err != nil {
			return err
		}
		if err := tx.Users().MarkEmailVerified(ctx, c.UserID, now); err != nil {
			return err
		}
		return a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthEmailVerified, Outcome: domain.AuditOutcomeSuccess,
			Actor: c.UserID, TargetType: "user", TargetID: c.UserID.String(),
		})
	})
	if err != nil {
		return err
	}
	if failure != "" {
		return domain.ErrInvalidVerification
	}
	return nil
}

func verificationFailed(challengeID domain.ID, reason string) auditSpec {
	return auditSpec{
		Type: domain.EventAuthEmailVerificationFailed, Outcome: domain.AuditOutcomeFailure,
		TargetType: "otp_challenge", TargetID: challengeID.String(), Reason: reason,
	}
}
```

ต่อท้าย `internal/adapter/http/auth_dto.go` ด้วย:

```go

// EmailRequest is the body of POST /api/v1/auth/email-verification/request.
type EmailRequest struct {
	Email string `json:"email" example:"user@example.com"`
}

// ConfirmEmailRequest is the body of POST /api/v1/auth/email-verification/confirm.
type ConfirmEmailRequest struct {
	ChallengeID string `json:"challenge_id" example:"0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d"`
	OTP         string `json:"otp" example:"042731"`
}
```

แก้ `internal/adapter/http/auth.go` ตามคู่ต่อไปนี้:

1. แทน

```go
	Service       port.AuthService
	Passwords     PasswordParser
```

   ด้วย

```go
	Service       port.AuthService
	Verification  port.EmailVerificationService // nil leaves out the email-verification routes
	Passwords     PasswordParser
```

2. แทน

```go
	auth      port.AuthService
	passwords PasswordParser
```

   ด้วย

```go
	auth         port.AuthService
	verification port.EmailVerificationService
	passwords    PasswordParser
```

3. แทน

```go
	h := &authHandlers{auth: opts.Service, passwords: opts.Passwords, csrf: opts.CSRF, clientIP: resolver, cookies: newCookieJar(opts.SecureCookies)}
```

   ด้วย

```go
	h := &authHandlers{
		auth: opts.Service, verification: opts.Verification, passwords: opts.Passwords,
		csrf: opts.CSRF, clientIP: resolver, cookies: newCookieJar(opts.SecureCookies),
	}
```

4. แทน

```go
	app.Post("/api/v1/auth/register", session, csrf, h.register)
}
```

   ด้วย

```go
	app.Post("/api/v1/auth/register", session, csrf, h.register)
	if opts.Verification != nil {
		app.Post("/api/v1/auth/email-verification/request", session, csrf, h.requestVerification)
		app.Post("/api/v1/auth/email-verification/confirm", session, csrf, h.confirmEmail)
	}
}
```

สร้าง `internal/adapter/http/email_verification.go`:

```go
package http

import (
	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/core/domain"
)

// RequestVerification godoc
// @Summary      Send a new verification code
// @Description  Sends a new 6-digit code to an account that registered with a password and is not verified yet, and invalidates its previous code. The answer is the same for every email, so it does not reveal whether an account exists; register and this endpoint share one quota per email (one code a minute, five an hour). Needs X-CSRF-Token and the csrf_nonce cookie.
// @Tags         Auth
// @ID           requestEmailVerification
// @Accept       json
// @Produce      json
// @Param        X-CSRF-Token  header  string        true  "Token from GET /api/v1/auth/csrf"
// @Param        body          body    EmailRequest  true  "The email to send a code to"
// @Success      202  {object}  OTPAcceptedResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input or malformed_json"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      429  {object}  ErrorResponse  "rate_limited; wait Retry-After seconds"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Router       /api/v1/auth/email-verification/request [post]
func (h *authHandlers) requestVerification(c fiber.Ctx) error {
	var req EmailRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	email, err := domain.ParseEmail(req.Email)
	if err != nil {
		return err
	}
	meta, err := h.requestMeta(c)
	if err != nil {
		return err
	}
	receipt, err := h.verification.Request(c.Context(), email, meta)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(newOTPAccepted(receipt))
}

// ConfirmEmail godoc
// @Summary      Confirm an email address
// @Description  Checks the 6-digit code sent by register or by "send a new code". A wrong, expired, already used or exhausted code (5 wrong tries) gives the same 400 invalid_verification. Success verifies the email but does not sign in: call POST /api/v1/auth/login next. Needs X-CSRF-Token and the csrf_nonce cookie.
// @Tags         Auth
// @ID           confirmEmailVerification
// @Accept       json
// @Produce      json
// @Param        X-CSRF-Token  header  string               true  "Token from GET /api/v1/auth/csrf"
// @Param        body          body    ConfirmEmailRequest  true  "challenge_id from the 202 response and the 6 ASCII digits from the email"
// @Success      204  "Email verified"
// @Failure      400  {object}  ErrorResponse  "invalid_verification, invalid_input or malformed_json"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      429  {object}  ErrorResponse  "rate_limited; wait Retry-After seconds"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Router       /api/v1/auth/email-verification/confirm [post]
func (h *authHandlers) confirmEmail(c fiber.Ctx) error {
	var req ConfirmEmailRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	challengeID, err := domain.ParseID("challenge_id", req.ChallengeID)
	if err != nil {
		return err
	}
	otp, err := domain.ParseOTP(req.OTP)
	if err != nil {
		return err
	}
	meta, err := h.requestMeta(c)
	if err != nil {
		return err
	}
	if err := h.verification.Confirm(c.Context(), domain.VerifyEmailInput{ChallengeID: challengeID, OTP: otp}, meta); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
```

- [ ] **Step 4: รัน test ให้ผ่าน และ generate Swagger**

Run:
```bash
gofmt -l internal; go vet ./... && make swagger && go test ./internal/... -count=1 -race && make test-integration && make lint
```
Expected: ทั้งหมดผ่าน ไม่มี SKIP ใน `internal/adapter/http` และ `internal/adapter/postgres`

- [ ] **Step 5: Commit**

```bash
git add internal/core/port/auth_service.go internal/core/service/email_verification.go internal/adapter/http docs/api
git commit -m "feat(auth): resend and confirm the email verification code"
```

---

### Task 11: login, me และ logout

**Files:**
- Create: `internal/core/service/login.go`, `internal/core/service/login_test.go`, `internal/adapter/http/session_auth.go`, `internal/adapter/http/login_test.go`
- Modify: `internal/core/port/auth_service.go`, `internal/adapter/http/auth.go`, `internal/adapter/http/auth_dto.go`, `internal/adapter/http/auth_harness_test.go`, `internal/adapter/http/auth_contract_test.go`, `docs/api/` (generated)

**Interfaces:**
- Consumes: `admit`, `recordFailure`, `appendAudit` (Task 7); `RateLimitPolicy.Login`; `PasswordHasher.Verify`/`VerifyDummy`/`NeedsRehash`/`Rehash` (Task 3); `NewSessionToken`, `domain.NewSession`, `domain.SessionGrant`, `domain.LoginInput` (Task 5); repositories `Users().CredentialsByEmail`/`Lock`/`SetPassword`/`ByID`, `Sessions().Insert`/`Revoke`; HTTP `cookieJar.set`/`clear`, `middleware.RequireAuth`, `middleware.GetPrincipal` (Task 8); harness + `signUpAndVerify` (Task 9–10); service test helpers `newTestAuth`, `testMeta`, `newHasher`, `hold`, `mustEmail`, `mustID`, `requireCode`, `testArgon2Params`
- Produces:
  - `port.AuthService` เพิ่ม `Login(ctx, in domain.LoginInput, meta domain.RequestMeta) (domain.SessionGrant, error)`, `CurrentUser(ctx, p domain.Principal) (domain.User, error)`, `Logout(ctx, p domain.Principal, meta domain.RequestMeta) error` (p เป็นศูนย์ = ไม่มี session ทำอะไรไม่ได้และไม่ error)
  - `service`: `maxLoginPasswordRunes = 32`, `errPasswordChanged`, `(*Auth).loginFailed(ctx, meta, reason, result error) error`
  - audit: `auth.login_failed` (target_type `login`, method `password`, reason `invalid_credentials` หรือ `email_not_verified`, actor NULL), `auth.login_succeeded` (actor = user, session_id, target = user), `auth.logout` (actor = user, session_id, target = session)
  - HTTP: DTO `LoginRequest`, `UserResponse`, `SessionResponse`, `newUserResponse(domain.User)`; `POST /api/v1/auth/login` → 200, `GET /api/v1/auth/me` → 200, `POST /api/v1/auth/logout` → 204
  - browser helpers `login(email, password)`, `me()`, `logout()` (ใช้ต่อใน Task 12)

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/core/service/login_test.go`:

```go
package service

import (
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// With the only hasher slot taken, a login that hashes waits and gets server_busy, while one that
// does not hash answers at once. That shows which paths spend an Argon2 verification.
func TestLoginSpendsAHashWhenThereIsNoPasswordToCheck(t *testing.T) {
	hasher, _ := newHasher(t, testArgon2Params, 1, 20*time.Millisecond)
	ta := newTestAuth(t, func(d *AuthDeps) { d.Hasher = hasher })
	unknown := mustEmail(t, "nobody@example.com")
	googleOnly := mustEmail(t, "google-only@example.com")
	ta.repos.users.credentials[googleOnly.String()] = domain.UserCredentials{User: domain.User{ID: mustID(t), Email: googleOnly}}
	release := hold(t, hasher)
	defer release()

	for _, email := range []domain.Email{unknown, googleOnly} {
		_, err := ta.auth.Login(t.Context(), domain.LoginInput{Email: email, Password: domain.NewSecretText("Zq9!kVx1")}, testMeta(t))
		requireCode(t, err, domain.CodeServerBusy)
	}
	_, err := ta.auth.Login(t.Context(), domain.LoginInput{Email: unknown, Password: domain.NewSecretText(strings.Repeat("a", 32))}, testMeta(t))
	requireCode(t, err, domain.CodeServerBusy) // 32 characters is still a password worth checking
}

func TestLoginRefusesAnOverlongPasswordWithoutHashing(t *testing.T) {
	hasher, _ := newHasher(t, testArgon2Params, 1, 20*time.Millisecond)
	ta := newTestAuth(t, func(d *AuthDeps) { d.Hasher = hasher })
	release := hold(t, hasher)
	defer release()

	for _, pw := range []string{strings.Repeat("a", 33), ""} {
		_, err := ta.auth.Login(t.Context(), domain.LoginInput{Email: mustEmail(t, "nobody@example.com"), Password: domain.NewSecretText(pw)}, testMeta(t))
		requireCode(t, err, domain.CodeInvalidCredentials)
	}
	events := ta.uow.audit.events
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want one login_failed per attempt", len(events))
	}
	for _, e := range events {
		if e.EventType != domain.EventAuthLoginFailed || e.ReasonCode != "invalid_credentials" || !e.ActorUserID.IsZero() || e.TargetType != "login" {
			t.Errorf("event = %+v", e)
		}
	}
}

func TestLogoutWithoutASessionDoesNothing(t *testing.T) {
	ta := newTestAuth(t, nil)
	if err := ta.auth.Logout(t.Context(), domain.Principal{}, testMeta(t)); err != nil {
		t.Fatalf("Logout(no session) = %v, want nil", err)
	}
	if ta.uow.runs != 0 {
		t.Error("logout without a session must not open a transaction")
	}
}
```

ต่อท้าย `internal/adapter/http/auth_harness_test.go` ด้วย:

```go

func (b *browser) login(email, password string) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password}, nil)
}

func (b *browser) me() (response, []byte) {
	b.t.Helper()
	return b.do(nethttp.MethodGet, "/api/v1/auth/me", nil, nil)
}

// logout asks for a token first, as a page would: the binding changed at login.
func (b *browser) logout() (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/logout", nil, nil)
}

func (h *authHarness) sessionsOf(t *testing.T, userID string) int {
	t.Helper()
	var n int
	if err := h.admin.QueryRow(context.Background(), `SELECT count(*) FROM app.sessions WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}
```

แก้ `internal/adapter/http/auth_contract_test.go` — แทน

```go
	{"post", "/api/v1/auth/email-verification/confirm", []int{204, 400, 403, 429}},
}
```

ด้วย

```go
	{"post", "/api/v1/auth/email-verification/confirm", []int{204, 400, 403, 429}},
	{"post", "/api/v1/auth/login", []int{200, 400, 401, 403, 429, 503}},
	{"get", "/api/v1/auth/me", []int{200, 401, 503}},
	{"post", "/api/v1/auth/logout", []int{204, 403, 503}},
}
```

สร้าง `internal/adapter/http/login_test.go`:

```go
package http

import (
	"encoding/json"
	nethttp "net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/service"
)

func decodeUser(t *testing.T, body []byte) UserResponse {
	t.Helper()
	var u UserResponse
	if err := json.Unmarshal(body, &u); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	return u
}

func TestLoginMeLogoutFlow(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)

	resp, body := b.login(email, "Zq9!kVx1")
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("login = %d %s", resp.StatusCode, body)
	}
	var out SessionResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.User.ID != u.ID || out.User.Email != email || !out.User.EmailVerified || out.ExpiresAt.Location() != time.UTC {
		t.Errorf("login body = %+v", out)
	}
	if strings.Contains(string(body), "argon2") || strings.Contains(string(body), "password") {
		t.Errorf("login body %s must not carry the password or its hash", body)
	}
	if wantExpiry := h.clock.Now().Add(7 * 24 * time.Hour); !out.ExpiresAt.Equal(wantExpiry) {
		t.Errorf("expires_at = %v, want %v", out.ExpiresAt, wantExpiry)
	}
	raw := strings.ToLower(strings.Join(resp.Header.Values("Set-Cookie"), "\n"))
	for _, want := range []string{"session=", "httponly", "samesite=lax", "path=/"} {
		if !strings.Contains(raw, want) {
			t.Errorf("Set-Cookie %q lacks %q", raw, want)
		}
	}
	if b.jar["session"] == "" || b.jar["csrf_nonce"] != "" {
		t.Errorf("cookies after login = %v, want a session and no nonce", b.jar)
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "auth.login_succeeded" || rows[0].Actor != u.ID ||
		rows[0].SessionID == "" || rows[0].Metadata["method"] != "password" {
		t.Fatalf("login audit = %+v", rows)
	}
	sessionID := rows[0].SessionID

	resp, body = b.me()
	if resp.StatusCode != nethttp.StatusOK || decodeUser(t, body).ID != u.ID {
		t.Fatalf("me = %d %s", resp.StatusCode, body)
	}

	resp, body = b.logout()
	if resp.StatusCode != nethttp.StatusNoContent || b.jar["session"] != "" {
		t.Fatalf("logout = %d %s, cookies %v; want 204 and the session cookie cleared", resp.StatusCode, body, b.jar)
	}
	rows = h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "auth.logout" || rows[0].Actor != u.ID || rows[0].SessionID != sessionID || rows[0].TargetID != sessionID {
		t.Errorf("logout audit = %+v", rows)
	}
	var reason string
	if err := h.admin.QueryRow(t.Context(), `SELECT COALESCE(revoke_reason, '') FROM app.sessions WHERE id = $1`, sessionID).Scan(&reason); err != nil || reason != "logout" {
		t.Errorf("revoke_reason = %q (%v), want logout", reason, err)
	}
	if resp, _ := b.me(); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("me after logout = %d, want 401", resp.StatusCode)
	}
}

func TestLogoutOfARevokedSessionIsNotAuditedAgain(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	b.login(email, "Zq9!kVx1")
	stolen := b.jar["session"]
	b.logout()

	b.jar["session"] = stolen // a copy of the cookie made before logout
	if resp, _ := b.me(); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Fatalf("me with the revoked cookie = %d, want 401", resp.StatusCode)
	}
	resp, _ := b.logout()
	if resp.StatusCode != nethttp.StatusNoContent || b.jar["session"] != "" {
		t.Errorf("second logout = %d, want 204 and the cookie cleared", resp.StatusCode)
	}
	if rows := h.auditFor(t, resp.Header.Get("X-Request-ID")); len(rows) != 0 {
		t.Errorf("a second logout must not be audited, got %v", eventTypes(rows))
	}
}

func TestSessionExpiresAfterItsLifetime(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	b.login(email, "Zq9!kVx1")

	h.clock.Advance(7*24*time.Hour - time.Second)
	if resp, _ := b.me(); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("me one second before expiry = %d, want 200", resp.StatusCode)
	}
	h.clock.Advance(time.Second)
	resp, body := b.me()
	if resp.StatusCode != nethttp.StatusUnauthorized || decodeError(t, body).Code != "unauthenticated" {
		t.Errorf("me at expires_at = %d %s, want 401 unauthenticated", resp.StatusCode, body)
	}
}

// The browser still holds the expired cookie and a CSRF token bound to the old session.
func TestLogoutAfterTheSessionExpired(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	b.login(email, "Zq9!kVx1")
	b.fetchCSRF() // bound to the session
	h.clock.Advance(7 * 24 * time.Hour)

	resp, body := b.do(nethttp.MethodPost, "/api/v1/auth/logout", nil, nil)
	if resp.StatusCode != nethttp.StatusForbidden || decodeError(t, body).Code != "csrf_failed" {
		t.Fatalf("logout with the old token = %d %s, want 403 csrf_failed (the binding is gone)", resp.StatusCode, body)
	}
	resp, body = b.logout() // a fresh token, now bound to a nonce
	if resp.StatusCode != nethttp.StatusNoContent || b.jar["session"] != "" {
		t.Errorf("logout after expiry = %d %s, want 204 and the cookie cleared", resp.StatusCode, body)
	}
	if rows := h.auditFor(t, resp.Header.Get("X-Request-ID")); len(rows) != 0 {
		t.Errorf("an expired session is not logged out again, got %v", eventTypes(rows))
	}
}

func TestLoginFailuresLookTheSame(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	known := uniqueEmail(t)
	b.signUpAndVerify(known, "Zq9!kVx1")
	googleOnly := uniqueEmail(t)
	gEmail, err := domain.ParseEmail(googleOnly)
	if err != nil {
		t.Fatal(err)
	}
	now := h.clock.Now()
	if err := h.store.Users().Insert(t.Context(), domain.User{ID: mustTestID(t), Email: gEmail, EmailVerifiedAt: &now, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	cases := map[string][2]string{
		"unknown email":  {uniqueEmail(t), "Zq9!kVx1"},
		"wrong password": {known, "Wrong!Pass9"},
		"google-only":    {googleOnly, "Zq9!kVx1"},
		"too long":       {known, "Zq9!kVx1" + strings.Repeat("a", 30)},
	}
	bodies := map[string]string{}
	for name, c := range cases {
		resp, body := h.newBrowser(t).login(c[0], c[1])
		if resp.StatusCode != nethttp.StatusUnauthorized || decodeError(t, body).Code != "invalid_credentials" {
			t.Fatalf("%s: %d %s, want 401 invalid_credentials", name, resp.StatusCode, body)
		}
		if len(resp.Header.Values("Set-Cookie")) != 0 {
			t.Errorf("%s: a failed login must not set cookies", name)
		}
		bodies[name] = stripJSONField(t, body, "error", "request_id")
		rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
		if len(rows) != 1 || rows[0].EventType != "auth.login_failed" || rows[0].Actor != "" ||
			rows[0].Reason != "invalid_credentials" || rows[0].Metadata["method"] != "password" {
			t.Errorf("%s: audit = %+v", name, rows)
		}
	}
	for name, body := range bodies {
		if body != bodies["unknown email"] {
			t.Errorf("%s body %s differs from %s", name, body, bodies["unknown email"])
		}
	}
}

func TestLoginBeforeVerifyingIs403(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.register(email, "Zq9!kVx1")
	u, _ := h.user(t, email)

	resp, body := b.login(email, "Zq9!kVx1")
	if resp.StatusCode != nethttp.StatusForbidden || decodeError(t, body).Code != "email_not_verified" {
		t.Fatalf("login before verifying = %d %s, want 403 email_not_verified", resp.StatusCode, body)
	}
	if b.jar["session"] != "" || h.sessionsOf(t, u.ID) != 0 {
		t.Error("an unverified login must not create a session")
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].Reason != "email_not_verified" || rows[0].Actor != "" {
		t.Errorf("audit = %+v, want login_failed email_not_verified with actor NULL", rows)
	}
	// A wrong password for the same account still says 401: only the right password learns 403.
	if resp, _ := b.login(email, "Wrong!Pass9"); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("wrong password of an unverified account = %d, want 401", resp.StatusCode)
	}
}

func TestLoginIgnoresEmailCase(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	mixed := "Mixed-" + mustTestID(t).String() + "@Example.COM"
	b.signUpAndVerify(mixed, "Zq9!kVx1")
	if resp, body := b.login(strings.ToLower(mixed), "Zq9!kVx1"); resp.StatusCode != nethttp.StatusOK {
		t.Errorf("login with the lower-case email = %d %s", resp.StatusCode, body)
	}
}

func TestLoginUpgradesAnOldHashAndKeepsWorking(t *testing.T) {
	old := newAuthHarness(t) // hashes with m=64
	email := uniqueEmail(t)
	old.newBrowser(t).signUpAndVerify(email, "Zq9!kVx1")
	before, _ := old.user(t, email)

	h := newAuthHarness(t, withHasherParams(service.Argon2Params{MemoryKiB: 128, Iterations: 1, Parallelism: 1}))
	if resp, body := h.newBrowser(t).login(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("first login = %d %s", resp.StatusCode, body)
	}
	after, _ := h.user(t, email)
	if after.PasswordHash == before.PasswordHash || !strings.Contains(after.PasswordHash, "m=128,t=1,p=1") {
		t.Fatalf("hash after login = %s, want it upgraded to m=128", after.PasswordHash)
	}
	if resp, _ := h.newBrowser(t).login(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusOK {
		t.Error("the upgraded hash must still accept the password")
	}
	if again, _ := h.user(t, email); again.PasswordHash != after.PasswordHash {
		t.Error("a hash with the current parameters must not be rehashed")
	}
}

func TestLoginIsRateLimitedPerEmail(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	for i := 0; i < 10; i++ {
		if resp, _ := h.newBrowser(t).login(email, "Wrong!Pass9"); resp.StatusCode != nethttp.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, resp.StatusCode)
		}
	}
	resp, body := h.newBrowser(t).login(email, "Wrong!Pass9")
	if resp.StatusCode != nethttp.StatusTooManyRequests || decodeError(t, body).Code != "rate_limited" {
		t.Fatalf("attempt 11 = %d %s, want 429", resp.StatusCode, body)
	}
	if wait, err := strconv.Atoi(resp.Header.Get("Retry-After")); err != nil || wait < 1 || wait > 900 {
		t.Errorf("Retry-After = %q, want 1..900", resp.Header.Get("Retry-After"))
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if !slices.Equal(eventTypes(rows), []string{"security.rate_limited"}) || rows[0].Metadata["scope"] != "login_email" {
		t.Errorf("audit = %+v, want security.rate_limited for login_email", rows)
	}
}

func TestMeWithoutASessionIs401(t *testing.T) {
	h := newAuthHarness(t)
	resp, body := h.newBrowser(t).me()
	if resp.StatusCode != nethttp.StatusUnauthorized || decodeError(t, body).Code != "unauthenticated" {
		t.Errorf("me = %d %s, want 401 unauthenticated", resp.StatusCode, body)
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/service/ ./internal/adapter/http/ -count=1`
Expected: build fail — `ta.auth.Login undefined`, `undefined: SessionResponse`, `UserResponse`

- [ ] **Step 3: เขียนโค้ด**

แก้ `internal/core/port/auth_service.go` — แทน

```go
	Register(ctx context.Context, in domain.RegisterInput, meta domain.RequestMeta) (domain.OTPReceipt, error)
}
```

ด้วย

```go
	Register(ctx context.Context, in domain.RegisterInput, meta domain.RequestMeta) (domain.OTPReceipt, error)
	// Login checks the password and opens a session. Every failure that must not reveal the account
	// returns domain.ErrInvalidCredentials; the right password of an unverified account returns
	// domain.ErrEmailNotVerified. Failures are audited before the error returns.
	Login(ctx context.Context, in domain.LoginInput, meta domain.RequestMeta) (domain.SessionGrant, error)
	// CurrentUser returns the account of p; an account that no longer exists is ErrUnauthenticated.
	CurrentUser(ctx context.Context, p domain.Principal) (domain.User, error)
	// Logout revokes p's session. A zero p (no active session) does nothing; a session that was
	// revoked meanwhile is not audited again.
	Logout(ctx context.Context, p domain.Principal, meta domain.RequestMeta) error
}
```

สร้าง `internal/core/service/login.go`:

```go
package service

import (
	"context"
	"errors"
	"log/slog"
	"unicode/utf8"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// maxLoginPasswordRunes is the longest password registration accepts. Login refuses a longer one
// before hashing, so a huge body cannot buy an expensive Argon2 run (milestone 4 design section 4).
const maxLoginPasswordRunes = 32

// errPasswordChanged means the hash changed between the check and the transaction (a password
// reset ran in between); the login fails like a wrong password.
var errPasswordChanged = errors.New("password changed during login")

// Login implements port.AuthService (milestone 4 design section 5).
func (a *Auth) Login(ctx context.Context, in domain.LoginInput, meta domain.RequestMeta) (domain.SessionGrant, error) {
	rules, err := a.limits.Login(in.Email, meta.ClientIP)
	if err != nil {
		return domain.SessionGrant{}, err
	}
	if err := a.admit(ctx, rules, meta, domain.ID{}); err != nil {
		return domain.SessionGrant{}, err
	}
	password := in.Password.Reveal()
	if password == "" || utf8.RuneCountInString(password) > maxLoginPasswordRunes {
		return domain.SessionGrant{}, a.loginFailed(ctx, meta, "invalid_credentials", domain.ErrInvalidCredentials)
	}

	creds, err := a.store.Users().CredentialsByEmail(ctx, in.Email)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && !creds.HasPassword()) {
		// Spend the same hash as a real check, so the time does not tell the account apart.
		if err := a.hasher.VerifyDummy(ctx, password); err != nil {
			return domain.SessionGrant{}, err
		}
		return domain.SessionGrant{}, a.loginFailed(ctx, meta, "invalid_credentials", domain.ErrInvalidCredentials)
	}
	if err != nil {
		return domain.SessionGrant{}, err
	}
	ok, err := a.hasher.Verify(ctx, creds.PasswordHash, password)
	if err != nil {
		return domain.SessionGrant{}, err
	}
	if !ok {
		return domain.SessionGrant{}, a.loginFailed(ctx, meta, "invalid_credentials", domain.ErrInvalidCredentials)
	}
	if creds.User.EmailVerifiedAt == nil {
		return domain.SessionGrant{}, a.loginFailed(ctx, meta, "email_not_verified", domain.ErrEmailNotVerified)
	}

	upgraded := a.upgradeHash(ctx, meta, creds.PasswordHash, in.Password)
	token, tokenHash, err := NewSessionToken()
	if err != nil {
		return domain.SessionGrant{}, err
	}
	sessionID, err := domain.NewID()
	if err != nil {
		return domain.SessionGrant{}, err
	}
	now := NowForStorage(a.clock)
	session, err := domain.NewSession(domain.Session{
		ID: sessionID, UserID: creds.User.ID, TokenHash: tokenHash, CreatedAt: now, ExpiresAt: now.Add(a.sessionTTL),
	})
	if err != nil {
		return domain.SessionGrant{}, err
	}

	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		if _, err := tx.Users().Lock(ctx, creds.User.ID); err != nil {
			return err
		}
		current, err := tx.Users().CredentialsByEmail(ctx, in.Email)
		if errors.Is(err, domain.ErrNotFound) {
			return errPasswordChanged
		}
		if err != nil {
			return err
		}
		if current.User.ID != creds.User.ID || current.PasswordHash != creds.PasswordHash {
			return errPasswordChanged
		}
		if upgraded != "" {
			if err := tx.Users().SetPassword(ctx, creds.User.ID, upgraded, now); err != nil {
				return err
			}
		}
		if err := tx.Sessions().Insert(ctx, session); err != nil {
			return err
		}
		return a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthLoginSucceeded, Outcome: domain.AuditOutcomeSuccess, Actor: creds.User.ID,
			SessionID: session.ID, TargetType: "user", TargetID: creds.User.ID.String(), Method: "password",
		})
	})
	if errors.Is(err, errPasswordChanged) {
		return domain.SessionGrant{}, a.loginFailed(ctx, meta, "invalid_credentials", domain.ErrInvalidCredentials)
	}
	if err != nil {
		return domain.SessionGrant{}, err
	}
	return domain.SessionGrant{User: creds.User, Token: domain.NewSecretText(token), ExpiresAt: session.ExpiresAt}, nil
}

// upgradeHash returns a new hash when the stored one uses old parameters, or "" to keep it. A busy
// hasher skips the upgrade: the next login tries again, and this one still succeeds.
func (a *Auth) upgradeHash(ctx context.Context, meta domain.RequestMeta, stored domain.PasswordHash, password domain.SecretText) domain.PasswordHash {
	need, err := a.hasher.NeedsRehash(stored)
	if err != nil || !need {
		return ""
	}
	fresh, err := a.hasher.Rehash(ctx, password)
	if err != nil {
		a.log.Warn("could not upgrade a password hash at login",
			slog.String("event", "auth.rehash_skipped"),
			slog.String("request_id", meta.RequestID.String()))
		return ""
	}
	return fresh
}

// loginFailed audits a failed login in its own transaction and returns result, or the audit error
// when the event could not be stored (milestone 4 design section 5: log it and answer 500).
func (a *Auth) loginFailed(ctx context.Context, meta domain.RequestMeta, reason string, result error) error {
	if err := a.recordFailure(ctx, meta, auditSpec{
		Type: domain.EventAuthLoginFailed, Outcome: domain.AuditOutcomeFailure,
		TargetType: "login", Reason: reason, Method: "password",
	}); err != nil {
		return err
	}
	return result
}

// CurrentUser implements port.AuthService.
func (a *Auth) CurrentUser(ctx context.Context, p domain.Principal) (domain.User, error) {
	if p.IsZero() {
		return domain.User{}, domain.ErrUnauthenticated
	}
	u, err := a.store.Users().ByID(ctx, p.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, domain.ErrUnauthenticated
	}
	return u, err
}

// Logout implements port.AuthService. Revoke reports whether this call revoked the session, so a
// repeated or concurrent logout writes one auth.logout only.
func (a *Auth) Logout(ctx context.Context, p domain.Principal, meta domain.RequestMeta) error {
	if p.IsZero() {
		return nil
	}
	now := NowForStorage(a.clock)
	return a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		revoked, err := tx.Sessions().Revoke(ctx, p.SessionID, now, domain.RevokeReasonLogout)
		if err != nil || !revoked {
			return err
		}
		return a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthLogout, Outcome: domain.AuditOutcomeSuccess, Actor: p.UserID,
			SessionID: p.SessionID, TargetType: "session", TargetID: p.SessionID.String(),
		})
	})
}
```

ต่อท้าย `internal/adapter/http/auth_dto.go` ด้วย:

```go

// LoginRequest is the body of POST /api/v1/auth/login.
type LoginRequest struct {
	Email    string `json:"email" example:"user@example.com"`
	Password string `json:"password" example:"Zq9!kVx1"`
}

// UserResponse is an account as the API shows it. It never carries the password hash.
type UserResponse struct {
	ID            string    `json:"id" example:"0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d"`
	Email         string    `json:"email" example:"user@example.com"`
	EmailVerified bool      `json:"email_verified" example:"true"`
	CreatedAt     time.Time `json:"created_at" example:"2026-10-24T09:30:00Z"`
}

func newUserResponse(u domain.User) UserResponse {
	return UserResponse{ID: u.ID.String(), Email: u.Email.String(), EmailVerified: u.EmailVerifiedAt != nil, CreatedAt: u.CreatedAt.UTC()}
}

// SessionResponse answers a successful login. The session itself travels only in the cookie.
type SessionResponse struct {
	User      UserResponse `json:"user"`
	ExpiresAt time.Time    `json:"expires_at" example:"2026-10-31T09:30:00Z"`
}
```

แก้ `internal/adapter/http/auth.go` — แทน

```go
	if opts.Verification != nil {
```

ด้วย

```go
	app.Post("/api/v1/auth/login", session, csrf, h.login)
	app.Get("/api/v1/auth/me", session, middleware.RequireAuth(), h.me)
	app.Post("/api/v1/auth/logout", session, csrf, h.logout)
	if opts.Verification != nil {
```

สร้าง `internal/adapter/http/session_auth.go`:

```go
package http

import (
	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
)

// Login godoc
// @Summary      Sign in with an email and a password
// @Description  Opens a session and sets it in the HttpOnly cookie session (__Host-session over HTTPS; SameSite=Lax, Path=/); the body only describes it. An unknown email, a wrong password and an account that signs in only with Google all give the same 401. The right password of an account whose email is not verified gives 403 email_not_verified. The pre-login csrf_nonce cookie is cleared: call GET /api/v1/auth/csrf again for a token bound to the new session. Needs X-CSRF-Token.
// @Tags         Auth
// @ID           login
// @Accept       json
// @Produce      json
// @Param        X-CSRF-Token  header  string        true  "Token from GET /api/v1/auth/csrf"
// @Param        body          body    LoginRequest  true  "Email and password"
// @Success      200  {object}  SessionResponse
// @Failure      400  {object}  ErrorResponse  "invalid_input or malformed_json"
// @Failure      401  {object}  ErrorResponse  "invalid_credentials"
// @Failure      403  {object}  ErrorResponse  "email_not_verified or csrf_failed"
// @Failure      429  {object}  ErrorResponse  "rate_limited; wait Retry-After seconds"
// @Failure      503  {object}  ErrorResponse  "server_busy (wait Retry-After seconds) or dependency_unavailable"
// @Header       200  {string}   Set-Cookie   "session=…; Path=/; Expires=…; HttpOnly; SameSite=Lax"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Header       503  {integer}  Retry-After  "Seconds to wait (server_busy)"
// @Router       /api/v1/auth/login [post]
func (h *authHandlers) login(c fiber.Ctx) error {
	var req LoginRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	email, err := domain.ParseEmail(req.Email)
	if err != nil {
		return err
	}
	meta, err := h.requestMeta(c)
	if err != nil {
		return err
	}
	grant, err := h.auth.Login(c.Context(), domain.LoginInput{Email: email, Password: domain.NewSecretText(req.Password)}, meta)
	if err != nil {
		return err
	}
	h.cookies.set(c, h.cookies.sessionName, grant.Token.Reveal(), grant.ExpiresAt)
	h.cookies.clear(c, h.cookies.nonceName)
	return c.JSON(SessionResponse{User: newUserResponse(grant.User), ExpiresAt: grant.ExpiresAt.UTC()})
}

// Me godoc
// @Summary      Get the signed-in account
// @Description  Needs the session cookie from POST /api/v1/auth/login.
// @Tags         Auth
// @ID           getCurrentUser
// @Produce      json
// @Success      200  {object}  UserResponse
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable: the session store could not be reached"
// @Router       /api/v1/auth/me [get]
func (h *authHandlers) me(c fiber.Ctx) error {
	p, _ := middleware.GetPrincipal(c)
	user, err := h.auth.CurrentUser(c.Context(), p)
	if err != nil {
		return err
	}
	return c.JSON(newUserResponse(user))
}

// Logout godoc
// @Summary      Sign out
// @Description  Revokes the current session on the server and clears the session cookie. Without an active session (none, expired or already revoked) it only clears the cookie and still answers 204. Needs X-CSRF-Token: signed in, a token bound to the session; otherwise one bound to the csrf_nonce cookie.
// @Tags         Auth
// @ID           logout
// @Produce      json
// @Param        X-CSRF-Token  header  string  true  "Token from GET /api/v1/auth/csrf"
// @Success      204  "Signed out"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      503  {object}  ErrorResponse  "dependency_unavailable"
// @Header       204  {string}  Set-Cookie  "session=; Path=/; Expires=Tue, 10 Nov 2009 23:00:00 GMT; HttpOnly; SameSite=Lax"
// @Router       /api/v1/auth/logout [post]
func (h *authHandlers) logout(c fiber.Ctx) error {
	h.cookies.clear(c, h.cookies.sessionName)
	p, _ := middleware.GetPrincipal(c)
	meta, err := h.requestMeta(c)
	if err != nil {
		return err
	}
	if err := h.auth.Logout(c.Context(), p, meta); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
```

- [ ] **Step 4: รัน test ให้ผ่าน และ generate Swagger**

Run:
```bash
gofmt -l internal; go vet ./... && make swagger && go test ./internal/... -count=1 -race && make test-integration && make lint
```
Expected: ทั้งหมดผ่าน; test ใน `login_test.go` ทำงานจริง (ไม่ SKIP)

- [ ] **Step 5: Commit**

```bash
git add internal/core/port/auth_service.go internal/core/service/login.go internal/core/service/login_test.go internal/adapter/http docs/api
git commit -m "feat(auth): login with a server-side session, current user and logout"
```

---

### Task 12: ทดสอบกรณีล้มเหลว — audit rollback, mailer ล่ม, log ไม่มีความลับ, session store ล่ม

task นี้เพิ่มเฉพาะ test: ถ้า test ใดล้มแสดงว่าโค้ดของ Task 9–11 ผิด ให้แก้โค้ดตัวนั้น (ไม่แก้ test ให้ผ่าน) และระบุใน report

**Files:**
- Create: `internal/adapter/http/auth_failures_test.go`

**Interfaces:**
- Consumes: harness, `withFailingAudit`, browser helpers และ `sessionsOf` (Task 9–11); `fakeMailer.err`
- Produces: ไม่มี

- [ ] **Step 1: เขียน tests**

สร้าง `internal/adapter/http/auth_failures_test.go`:

```go
package http

import (
	"errors"
	nethttp "net/http"
	"strings"
	"testing"
)

// failing shares the database of h but its audit repository always fails.
func failingTwin(t *testing.T) (h, failing *authHarness) {
	t.Helper()
	return newAuthHarness(t), newAuthHarness(t, withFailingAudit())
}

func requireAuditFailure(t *testing.T, what string, resp response, body []byte) {
	t.Helper()
	got := decodeError(t, body)
	if resp.StatusCode != nethttp.StatusInternalServerError || got.Code != "audit_write_failed" || got.Message != "Internal server error" {
		t.Fatalf("%s = %d %+v, want 500 audit_write_failed without details", what, resp.StatusCode, got)
	}
}

func TestRegisterRollsBackWithoutItsAudit(t *testing.T) {
	_, failing := failingTwin(t)
	email := uniqueEmail(t)
	resp, body := failing.newBrowser(t).register(email, "Zq9!kVx1")
	requireAuditFailure(t, "register", resp, body)
	if _, ok := failing.user(t, email); ok {
		t.Error("the account must be rolled back with its audit")
	}
	if failing.mailer.verificationCount(email) != 0 {
		t.Error("no code may be sent for a rolled-back registration")
	}
}

func TestConfirmRollsBackWithoutItsAudit(t *testing.T) {
	h, failing := failingTwin(t)
	email := uniqueEmail(t)
	h.newBrowser(t).register(email, "Zq9!kVx1")
	mail := h.mailer.lastVerification(t, email)
	u, _ := h.user(t, email)

	resp, body := failing.newBrowser(t).confirm(mail.ChallengeID.String(), mail.OTP.Reveal())
	requireAuditFailure(t, "confirm", resp, body)
	if after, _ := h.user(t, email); after.Verified {
		t.Error("the email must stay unverified")
	}
	if ch := h.challenges(t, u.ID); !ch[0].Open || ch[0].FailedAttempts != 0 {
		t.Errorf("challenge = %+v, want it untouched", ch[0])
	}
}

func TestLoginWithoutItsAuditOpensNoSession(t *testing.T) {
	h, failing := failingTwin(t)
	email := uniqueEmail(t)
	h.newBrowser(t).signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)

	b := failing.newBrowser(t)
	resp, body := b.login(email, "Zq9!kVx1")
	requireAuditFailure(t, "login", resp, body)
	if b.jar["session"] != "" || h.sessionsOf(t, u.ID) != 0 {
		t.Error("a login whose audit failed must not leave a session or a cookie")
	}

	resp, body = failing.newBrowser(t).login(email, "Wrong!Pass9")
	requireAuditFailure(t, "failed login", resp, body)
	if !strings.Contains(failing.logs.String(), `"event":"audit.write_failed"`) {
		t.Error("a lost login_failed audit must be logged")
	}
}

func TestMailerFailureDoesNotChangeTheAnswer(t *testing.T) {
	h := newAuthHarness(t)
	h.mailer.mu.Lock()
	h.mailer.err = errors.New("smtp is down for alice@example.com")
	h.mailer.mu.Unlock()
	email := uniqueEmail(t)

	resp, body := h.newBrowser(t).register(email, "Zq9!kVx1")
	if resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("register with a failing mailer = %d %s, want 202", resp.StatusCode, body)
	}
	logs := h.logs.String()
	if !strings.Contains(logs, `"event":"auth.mail_not_sent"`) || !strings.Contains(logs, `"kind":"email_verification"`) {
		t.Errorf("logs %s should record auth.mail_not_sent with its kind", logs)
	}
	if strings.Contains(logs, email) || strings.Contains(logs, "alice@example.com") {
		t.Error("the mail failure log must not hold an address or the error text")
	}
}

func TestLogsHoldNoSecrets(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	var secrets []string
	collect := func() {
		if b.csrf != "" {
			secrets = append(secrets, b.csrf)
		}
		for _, v := range b.jar {
			secrets = append(secrets, v)
		}
	}

	b.fetchCSRF()
	collect()
	b.register(email, "Zq9!kVx1")
	collect()
	mail := h.mailer.lastVerification(t, email)
	b.confirm(mail.ChallengeID.String(), wrongCode(mail.OTP.Reveal()))
	b.confirm(mail.ChallengeID.String(), mail.OTP.Reveal())
	collect()
	h.newBrowser(t).login(email, "Wrong!Pass9")
	b.login(email, "Zq9!kVx1")
	collect()
	b.me()
	b.logout()
	collect()

	logs := h.logs.String()
	if logs == "" {
		t.Fatal("no logs were captured")
	}
	for _, s := range append(secrets, "Zq9!kVx1", "Wrong!Pass9", email, strings.Repeat("o", 32), strings.Repeat("c", 32), strings.Repeat("r", 32)) {
		if strings.Contains(logs, s) {
			t.Errorf("logs contain a secret value (%d characters)", len(s))
		}
	}
	for _, code := range []string{mail.OTP.Reveal(), wrongCode(mail.OTP.Reveal())} {
		if strings.Contains(logs, `"`+code+`"`) {
			t.Error("logs contain an OTP code")
		}
	}
}

func TestSessionStoreOutageIs503NotALogout(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	b.login(email, "Zq9!kVx1")

	h.pool.Close() // the admin pool stays open
	for _, path := range []string{"/api/v1/auth/me", "/api/v1/auth/csrf"} {
		resp, body := b.do(nethttp.MethodGet, path, nil, nil)
		if resp.StatusCode != nethttp.StatusServiceUnavailable || decodeError(t, body).Code != "dependency_unavailable" {
			t.Errorf("GET %s with the store down = %d %s, want 503 dependency_unavailable", path, resp.StatusCode, body)
		}
	}
	if b.jar["session"] == "" {
		t.Error("an outage must not clear the session cookie")
	}
	if resp, _ := b.do(nethttp.MethodGet, "/readyz", nil, nil); resp.StatusCode != nethttp.StatusServiceUnavailable {
		t.Errorf("readyz = %d, want 503", resp.StatusCode)
	}
}
```

- [ ] **Step 2: รัน test**

Run: `go vet ./internal/adapter/http/ && make test-integration`
Expected: ผ่านทั้งหมด (ถ้าล้ม ให้หาสาเหตุในโค้ดของ Task 9–11 ตาม systematic debugging แก้โค้ด แล้วรันซ้ำ)

- [ ] **Step 3: รันชุดเต็มและ lint**

Run: `gofmt -l internal; go test ./... -count=1 -race && make lint`
Expected: ผ่าน

- [ ] **Step 4: Commit**

```bash
git add internal/adapter/http/auth_failures_test.go
git commit -m "test(auth): prove audit rollback, mail failure, log hygiene and store outage handling"
```

(ถ้าต้องแก้โค้ดของ task ก่อนหน้า ให้ `git add` ไฟล์นั้นด้วยและเปลี่ยน message เป็น `fix(auth): …` ที่บอกสิ่งที่แก้)

---

### Task 13: ประกอบ dependency ใน `cmd/api` และปิดระบบตามลำดับ

**Files:**
- Create: `cmd/api/wire.go`, `cmd/api/wire_test.go`
- Modify: `cmd/api/main.go`, `docs/api/` (generated — description ของ API เปลี่ยน)

**Interfaces:**
- Consumes: `config.Config` (`Auth`, `SMTP`, `PublicOrigin`), `postgres.NewStore`, ทุก constructor ของ service (Task 3, 7), `mail.NewSMTPMailer`, `mail.NewDispatcher`, `httpadapter.AuthOptions`, `httpadapter.NewClientIPResolver`
- Produces: `buildAuth(cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) (authParts, error)` คืน `authParts{options httpadapter.AuthOptions; mail *mail.Dispatcher}`; `systemClock`; `secureCookies(origin string) bool`

- [ ] **Step 1: เขียน failing test**

สร้าง `cmd/api/wire_test.go`:

```go
package main

import (
	"context"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	httpadapter "go-backend-stock/internal/adapter/http"
	"go-backend-stock/internal/config"
)

func wireConfig(t *testing.T, origin string) config.Config {
	t.Helper()
	env := map[string]string{
		"APP_ENV":           "test",
		"PUBLIC_ORIGIN":     origin,
		"DATABASE_URL":      "postgres://app_runtime:unused@127.0.0.1:1/stock?sslmode=disable",
		"CSRF_SECRET":       "dev-only-csrf-secret-0123456789abcdef",
		"OTP_SECRET":        "dev-only-otp-secret-0123456789abcdef",
		"RATE_LIMIT_SECRET": "dev-only-rate-limit-secret-0123456789",
		"OIDC_SECRET":       "dev-only-oidc-secret-0123456789abcdef",
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	return cfg
}

// The pool connects lazily, so the wiring can be checked without a database: GET /auth/csrf
// without a session cookie never queries it.
func TestBuildAuthServesTheAuthRoutes(t *testing.T) {
	cfg := wireConfig(t, "http://localhost:8080")
	pool, err := pgxpool.New(t.Context(), cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	parts, err := buildAuth(cfg, pool, log)
	if err != nil {
		t.Fatalf("buildAuth() error = %v", err)
	}
	defer func() { _ = parts.mail.Close(context.Background()) }()
	o := parts.options
	if o.Service == nil || o.Verification == nil || o.Passwords == nil || o.CSRF == nil || o.ClientIP == nil ||
		o.PublicOrigin != "http://localhost:8080" || o.SecureCookies {
		t.Fatalf("options = %+v", o)
	}

	app := httpadapter.NewServer(httpadapter.Options{Logger: log, Readiness: pool, Auth: o})
	resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/v1/auth/csrf", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != nethttp.StatusOK || !strings.Contains(strings.Join(resp.Header.Values("Set-Cookie"), ";"), "csrf_nonce=") {
		t.Errorf("GET /api/v1/auth/csrf = %d %v, want 200 with a csrf_nonce cookie", resp.StatusCode, resp.Header.Values("Set-Cookie"))
	}
}

func TestSecureCookiesFollowThePublicOrigin(t *testing.T) {
	if secureCookies("http://localhost:8080") || !secureCookies("https://stock.example.com") {
		t.Error("only an https origin may use Secure, __Host- cookies")
	}
}

func TestSystemClockIsUTC(t *testing.T) {
	if (systemClock{}).Now().Location() != time.UTC {
		t.Error("systemClock must return UTC")
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./cmd/api/ -count=1`
Expected: build fail — `undefined: buildAuth`, `secureCookies`, `systemClock`

- [ ] **Step 3: เขียนโค้ด**

สร้าง `cmd/api/wire.go`:

```go
package main

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	httpadapter "go-backend-stock/internal/adapter/http"
	"go-backend-stock/internal/adapter/mail"
	"go-backend-stock/internal/adapter/postgres"
	"go-backend-stock/internal/config"
	"go-backend-stock/internal/core/service"
)

// systemClock is the real port.Clock.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// secureCookies reports whether the API is served over HTTPS, which turns on Secure and the
// __Host- cookie names (dev over plain HTTP keeps them off).
func secureCookies(publicOrigin string) bool { return strings.HasPrefix(publicOrigin, "https://") }

// authParts is what buildAuth assembles: the routes' options, and the mail queue that main closes
// after the HTTP server.
type authParts struct {
	options httpadapter.AuthOptions
	mail    *mail.Dispatcher
}

// buildAuth wires the auth use cases from validated configuration. It opens no connection.
func buildAuth(cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) (authParts, error) {
	fail := func(what string, err error) (authParts, error) {
		return authParts{}, fmt.Errorf("build auth: %s: %w", what, err)
	}
	store := postgres.NewStore(pool)
	clock := systemClock{}

	hasher, err := service.NewPasswordHasher(service.PasswordHasherConfig{
		Params: service.DefaultArgon2Params(), Concurrency: cfg.Auth.PasswordHashConcurrency, Logger: log,
	})
	if err != nil {
		return fail("password hasher", err)
	}
	blocklist, err := service.DefaultBlocklist()
	if err != nil {
		return fail("password blocklist", err)
	}
	passwords, err := service.NewPasswordPolicy(blocklist)
	if err != nil {
		return fail("password policy", err)
	}
	otp, err := service.NewOTPCodec(cfg.Auth.OTPSecret.Bytes())
	if err != nil {
		return fail("otp codec", err)
	}
	csrf, err := service.NewCSRFCodec(cfg.Auth.CSRFSecret.Bytes(), clock)
	if err != nil {
		return fail("csrf codec", err)
	}
	keys, err := service.NewRateLimitKeys(cfg.Auth.RateLimitSecret.Bytes())
	if err != nil {
		return fail("rate-limit keys", err)
	}
	limits, err := service.NewRateLimitPolicy(keys, service.RateLimitConfig{
		OTPResendCooldown:       cfg.Auth.OTPResendCooldown,
		OTPRequestsPerHourEmail: cfg.Auth.OTPRequestsPerHourEmail,
		OTPRequestsPerHourIP:    cfg.Auth.OTPRequestsPerHourIP,
		LoginAttemptsPerEmail:   cfg.Auth.LoginAttemptsPerEmail,
		LoginAttemptsPerIP:      cfg.Auth.LoginAttemptsPerIP,
	})
	if err != nil {
		return fail("rate-limit policy", err)
	}
	pacer, err := service.NewPacer(clock, cfg.Auth.ResponseFloor, cfg.Auth.ResponseJitter)
	if err != nil {
		return fail("response pacer", err)
	}
	smtp, err := mail.NewSMTPMailer(mail.SMTPConfig{
		Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, Username: cfg.SMTP.Username, Password: cfg.SMTP.Password.Reveal(),
		From: cfg.SMTP.From, TLS: mail.TLSMode(cfg.SMTP.TLS), Timeout: cfg.SMTP.Timeout,
	})
	if err != nil {
		return fail("smtp mailer", err)
	}
	dispatcher, err := mail.NewDispatcher(mail.DispatcherConfig{Sender: smtp, Logger: log, SendTimeout: cfg.SMTP.Timeout})
	if err != nil {
		return fail("mail dispatcher", err)
	}
	auth, err := service.NewAuth(service.AuthDeps{
		Store: store, UnitOfWork: store, Clock: clock, Hasher: hasher, OTP: otp, Limits: limits, Pacer: pacer,
		Mailer: dispatcher, Logger: log, SessionTTL: cfg.Auth.SessionTTL, OTPMaxAttempts: cfg.Auth.OTPMaxAttempts,
	})
	if err != nil {
		return fail("auth service", err)
	}
	return authParts{
		options: httpadapter.AuthOptions{
			Service: auth, Verification: auth, Passwords: passwords, CSRF: csrf,
			ClientIP:     httpadapter.NewClientIPResolver(cfg.Auth.TrustedProxies),
			PublicOrigin: cfg.PublicOrigin, SecureCookies: secureCookies(cfg.PublicOrigin),
		},
		mail: dispatcher,
	}, nil
}
```

(ถ้า `NewDispatcher` หรือ `NewSMTPMailer` ของ repo ต้องการ field อื่นที่ไม่ได้ใส่ ให้ดูจาก `internal/adapter/mail/*.go` แล้วเติมให้ครบและระบุใน report; `go vet` ต้องไม่เตือน)

แก้ `cmd/api/main.go` ตามคู่ต่อไปนี้:

1. แทน

```go
// @description     Milestone 1 exposes only the Health endpoints; every other endpoint is not implemented yet.
```

   ด้วย

```go
// @description     Milestone 4b exposes Health and the email/password Auth endpoints; Google sign-in, password reset, portfolios and transactions are not implemented yet.
```

2. แทน

```go
	defer pool.Close()

	app := httpadapter.NewServer(httpadapter.Options{
		Logger:    log,
		Readiness: pool,
```

   ด้วย

```go
	defer pool.Close()

	auth, err := buildAuth(cfg, pool, log)
	if err != nil {
		return err
	}

	app := httpadapter.NewServer(httpadapter.Options{
		Logger:    log,
		Readiness: pool,
		Auth:      auth.options,
```

3. แทน

```go
	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
```

   ด้วย

```go
	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	// After HTTP, so no handler can queue an email any more; queued emails get the time left.
	if err := auth.mail.Close(shutdownCtx); err != nil {
		log.Warn("emails were still queued at the shutdown deadline", "event", "mail.shutdown_incomplete")
	}
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run:
```bash
gofmt -l cmd internal; go vet ./... && make swagger && go test ./... -count=1 -race && make lint && go build -o /dev/null ./cmd/api
```
Expected: ผ่านทั้งหมด

- [ ] **Step 5: Commit**

```bash
git add cmd/api/wire.go cmd/api/wire_test.go cmd/api/main.go docs/api
git commit -m "feat(api): wire the auth service, mail dispatcher and auth routes into the API"
```

---

### Task 14: เอกสาร, CI Mailpit และตรวจรับ

**Files:**
- Modify: `README.md`, `.github/workflows/ci.yml`, `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md`, `docs/superpowers/specs/2026-10-22-milestone-4-auth-design.md`

**Interfaces:**
- Consumes: endpoints ทั้งหมดของ Task 8–11 และ wiring ของ Task 13
- Produces: README มีคู่มือ curl ครบ flow; CI รัน test ส่งอีเมลจริงกับ Mailpit; roadmap/spec บอกสถานะ 4b

- [ ] **Step 1: README**

แก้ `README.md` ตามคู่ต่อไปนี้:

1. แทน

```markdown
> **สถานะ:** Milestone 4a — ชิ้นส่วนพื้นฐานของ auth (password policy, Argon2id, OTP, session/CSRF token, rate-limit rules, Mailer) พร้อมแล้ว แต่ API ยังมีเฉพาะ `/healthz`, `/readyz` และเอกสาร API; endpoint ของ auth เปิดใน milestone 4b ส่วนพอร์ตและรายการซื้อขายยังไม่เปิดใช้งาน
```

   ด้วย

```markdown
> **สถานะ:** Milestone 4b — สมัครด้วยอีเมล+รหัสผ่าน, ยืนยันอีเมลด้วย OTP, login/logout และ session ฝั่ง server ใช้งานผ่าน API ได้แล้ว; Google login (4c), ลืมรหัสผ่าน (milestone 5), พอร์ตและรายการซื้อขายยังไม่เปิดใช้งาน
```

2. แทน

```markdown
## ตัวแปรสภาพแวดล้อมของ auth (milestone 4a)
```

   ด้วย

```markdown
## ตัวแปรสภาพแวดล้อมของ auth
```

3. แทน

```markdown
`MAIL_SEND_TIMEOUT` (compose ชี้ไป Mailpit
```

   ด้วย

```markdown
`MAIL_SEND_TIMEOUT` (10s; compose ชี้ไป Mailpit
```

4. แทน

```markdown
## พัฒนาและทดสอบ
```

   ด้วย

````markdown
## ลอง flow ของ auth ด้วย curl

ต้องตั้งค่าใน `.env` ให้ครบก่อน (`APP_ENV=development`, `PUBLIC_ORIGIN=http://localhost:8080` และ secret สี่ตัวตามหัวข้อก่อนหน้า) แล้ว `docker compose up --build`
ทุก POST ต้องมี header `Origin` ที่ตรงกับ `PUBLIC_ORIGIN` และ `X-CSRF-Token` จาก `GET /api/v1/auth/csrf`; cookie jar เก็บ `csrf_nonce` และ `session` ให้เอง
ตัวอย่างนี้ใช้ bash/zsh และอ่านรหัสจาก Mailpit ผ่าน `http://localhost:8025/view/latest.txt`

```bash
API=http://localhost:8080/api/v1
JAR=$(mktemp)
EMAIL=demo-$RANDOM@example.com
csrf() { curl -s -b "$JAR" -c "$JAR" "$API/auth/csrf" | sed -E 's/.*"csrf_token":"([^"]+)".*/\1/'; }
post() { curl -s -b "$JAR" -c "$JAR" -H "Origin: http://localhost:8080" -H "X-CSRF-Token: $(csrf)" \
  -H 'Content-Type: application/json' -w '\nHTTP %{http_code}\n' -d "$2" "$API$1"; }

# 1) สมัคร → 202 พร้อม challenge_id (ตอบแบบเดียวกันแม้อีเมลมีบัญชีแล้ว)
post /auth/register "{\"email\":\"$EMAIL\",\"password\":\"Zq9!kVx1\"}" | tee /dev/stderr > "$JAR.reg"
CHALLENGE=$(sed -nE 's/.*"challenge_id":"([^"]+)".*/\1/p' "$JAR.reg")

# 2) อ่านรหัส 6 หลักจากอีเมลล่าสุดใน Mailpit (ส่งหลังตอบ 202 อาจรอ 1–2 วินาที)
sleep 2
OTP=$(curl -s http://localhost:8025/view/latest.txt | sed -n 's/^รหัสยืนยันของคุณคือ \([0-9]\{6\}\)$/\1/p')

# 3) ยืนยันอีเมล → 204
post /auth/email-verification/confirm "{\"challenge_id\":\"$CHALLENGE\",\"otp\":\"$OTP\"}"

# 4) login → 200 และได้ cookie session; 5) ดูบัญชี → 200; 6) logout → 204
post /auth/login "{\"email\":\"$EMAIL\",\"password\":\"Zq9!kVx1\"}"
curl -s -b "$JAR" -c "$JAR" -w '\nHTTP %{http_code}\n' "$API/auth/me"
post /auth/logout ''
curl -s -b "$JAR" -w '\nHTTP %{http_code}\n' "$API/auth/me"   # 401 หลัง logout
```

- ขอรหัสใหม่: `post /auth/email-verification/request "{\"email\":\"$EMAIL\"}"` (เว้น 60 วินาทีจากครั้งก่อน และไม่เกิน 5 ครั้งต่อชั่วโมงต่ออีเมล)
- ถูกจำกัดความถี่จะได้ 429 `rate_limited` พร้อม header `Retry-After` (วินาที)
- ใน Swagger UI (`http://localhost:8080/docs/`) ให้เรียก `GET /api/v1/auth/csrf` ก่อน แล้วคัดลอก `csrf_token` ไปใส่ช่อง `X-CSRF-Token` ของ POST; browser ส่ง cookie ให้เอง ขอ token ใหม่หลัง login และหลัง logout เพราะ binding เปลี่ยน
- ข้อความ error ทุกอันมี `request_id` ตรงกับ header `X-Request-ID` ใช้ค้น log ได้

## พัฒนาและทดสอบ
````

- [ ] **Step 2: CI Mailpit**

แก้ `.github/workflows/ci.yml` ตามคู่ต่อไปนี้:

1. แทน

```yaml
          --health-retries 20
    env:
```

   ด้วย

```yaml
          --health-retries 20
      mailpit:
        image: axllent/mailpit:v1.31.6@sha256:23fe39e81bde596a918000b3135c0f85dc8bb4c9ea6be274b051143f3e300387
        ports:
          - 1025:1025
          - 8025:8025
    env:
```

2. แทน

```yaml
      REQUIRE_INTEGRATION: "1"
    steps:
```

   ด้วย

```yaml
      REQUIRE_INTEGRATION: "1"
      TEST_SMTP_ADDR: localhost:1025
      TEST_MAILPIT_API_URL: http://localhost:8025
      REQUIRE_MAIL_INTEGRATION: "1"
    steps:
```

(digest ของ image ต้องตรงกับ `docker-compose.yml` บรรทัด `image: axllent/mailpit:...`; ถ้าไม่ตรงให้ใช้ค่าจาก `docker-compose.yml`)

- [ ] **Step 3: roadmap และ spec**

แก้ `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md` ตามคู่ต่อไปนี้:

1. แทน

```markdown
แผน 4a ลงมือครบแล้ว รอตรวจรับ ขั้นต่อไปคือแผน 4b
```

   ด้วย

```markdown
แผน 4a ลงมือครบแล้ว รอตรวจรับ แผน 4b ลงมือครบแล้ว รอตรวจรับ ขั้นต่อไปคือแผน 4c
```

2. แทน

```markdown
4b (AuthService, EmailVerificationService, middleware, endpoints) และ 4c (Google OIDC) ยังไม่มีแผน
```

   ด้วย

```markdown
[4b](2026-10-24-milestone-4b-auth-endpoints.md) (AuthService, EmailVerificationService, middleware, endpoints; ลงมือครบ รอตรวจรับ); 4c (Google OIDC) ยังไม่มีแผน
```

3. แทนสี่บรรทัดต่อไปนี้ (แต่ละบรรทัดเจอครั้งเดียว)

```markdown
- [ ] ทำ register ที่ตอบ 202 แบบเดียวกันทุกกรณี, confirm/resend รหัสยืนยันอีเมล และปฏิเสธ login ของบัญชีที่ยังไม่ยืนยันด้วย `email_not_verified`
- [ ] ทำ email/password login/logout, current-user endpoint และ session authentication พร้อม expiry/revocation
- [ ] เพิ่ม cookie, CSRF, Origin checks, login rate limit และ error mapping พร้อม audit ของผลสำเร็จ/ล้มเหลว
- [ ] login ของอีเมลที่ไม่มีบัญชี/บัญชี Google-only ตรวจกับ hash จำลองและตอบแบบเดียวกับรหัสผิด
```

   ด้วย

```markdown
- [x] ทำ register ที่ตอบ 202 แบบเดียวกันทุกกรณี, confirm/resend รหัสยืนยันอีเมล และปฏิเสธ login ของบัญชีที่ยังไม่ยืนยันด้วย `email_not_verified` (แผน 4b)
- [x] ทำ email/password login/logout, current-user endpoint และ session authentication พร้อม expiry/revocation (แผน 4b)
- [x] เพิ่ม cookie, CSRF, Origin checks, login rate limit และ error mapping พร้อม audit ของผลสำเร็จ/ล้มเหลว (แผน 4b)
- [x] login ของอีเมลที่ไม่มีบัญชี/บัญชี Google-only ตรวจกับ hash จำลองและตอบแบบเดียวกับรหัสผิด (แผน 4b)
```

4. แทน

```markdown
ตอบ 404 แทน 405 + `Allow` — แก้พร้อม route แบบไม่ใช่ GET ใน milestone 4
```

   ด้วย

```markdown
ตอบ 404 แทน 405 + `Allow` — แก้พร้อม route แบบไม่ใช่ GET ใน milestone 4 — **ปิดแล้วในแผน 4b Task 6**
```

5. ในหัวข้อ "ข้อส่งต่อจากรีวิวทั้ง branch ของแผน 4a ไป 4b" ต่อท้ายบรรทัดที่ขึ้นต้นด้วย `- งานแรกของ 4b:` (สองบรรทัด), `- ตอนต่อสาย 4b:`, `- เก็บกวาด` และ `- ควรเพิ่มใน CI:` ด้วย ` — **ปิดแล้วในแผน 4b**`; บรรทัด `- ต้องถามผู้ใช้ใน 4b:` ต่อท้ายด้วยคำตอบของผู้ใช้ที่ controller ส่งมา (ถ้ายังไม่มีคำตอบ ให้ต่อท้ายว่า ` — ยังรอคำตอบจากผู้ใช้`)

แก้ `docs/superpowers/specs/2026-10-22-milestone-4-auth-design.md` — แทน

```markdown
แผน 4a: [2026-10-22-milestone-4a-auth-building-blocks.md](../plans/2026-10-22-milestone-4a-auth-building-blocks.md) ลงมือครบ รอตรวจรับ
```

ด้วย

```markdown
แผน 4a: [2026-10-22-milestone-4a-auth-building-blocks.md](../plans/2026-10-22-milestone-4a-auth-building-blocks.md) ลงมือครบ รอตรวจรับ; แผน 4b: [2026-10-24-milestone-4b-auth-endpoints.md](../plans/2026-10-24-milestone-4b-auth-endpoints.md) ลงมือครบ รอตรวจรับ
```

- [ ] **Step 4: ตรวจรับในเครื่อง**

Run:
```bash
docker compose ps
make check
make test-integration
make test-mail
```
Expected:
- `make check` ผ่าน (tidy, vet, lint, swagger ตรงกับที่ commit, tests)
- `make test-integration` ผ่านเมื่อ postgres ของ compose ขึ้นอยู่ ถ้า `docker compose ps` ไม่เห็น postgres ทำงาน ห้ามสั่ง `docker compose up` เอง ให้ระบุใน report ว่ายังไม่ได้รันและต้องให้ผู้ใช้เปิด stack
- `make test-mail` ผ่าน: ถ้า Mailpit ไม่ได้รันอยู่ให้ `docker compose up -d mailpit` ก่อน แล้ว `docker compose stop mailpit` หลังรันเสร็จ (คืนสถานะตามที่เจอ)

ข้ามการรันสคริปต์ curl ใน README กับ compose ใน task นี้: ต้องใช้ `docker compose up --build` และ `.env` ที่ผู้ใช้กรอกค่าแล้ว ซึ่งต้องขออนุญาตผู้ใช้ก่อน controller จะเสนอให้ผู้ใช้รันหรือขออนุญาตรันหลังจบทุก task

- [ ] **Step 5: Commit**

```bash
git add README.md .github/workflows/ci.yml docs/superpowers/plans/2026-10-05-phase-1-roadmap.md docs/superpowers/specs/2026-10-22-milestone-4-auth-design.md
git commit -m "docs: add the auth curl walkthrough, run Mailpit in CI and mark plan 4b done"
```

