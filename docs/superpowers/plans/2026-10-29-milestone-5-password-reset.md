# Milestone 5 — ลืมรหัสผ่านด้วย OTP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** เพิ่ม `POST /api/v1/auth/password-reset/request` และ `POST /api/v1/auth/password-reset/confirm` ให้เจ้าของอีเมลตั้งรหัสผ่านใหม่ด้วย OTP 6 หลักที่ใช้ได้ครั้งเดียว โดย reset สำเร็จจะเปลี่ยน hash, ตั้ง `email_verified_at`, revoke ทุก session และเขียน audit ใน transaction เดียว แล้วส่งอีเมลแจ้งหลัง commit

**Architecture:** use case อยู่ใน `service.PasswordReset` (ชนิดแยกที่ใช้ dependencies ของ `*Auth` ร่วมกัน เพราะ `*Auth` มี `Request`/`Confirm` ของ email verification อยู่แล้ว) ใช้ OTP codec, rate limiter, pacer, mailer และ `recordFailure`/`appendAudit` ชุดเดิม; schema, repositories และ event types มีครบตั้งแต่ milestone 3 จึงไม่มี migration; HTTP adapter เพิ่ม handler สองตัวที่ผ่าน session + CSRF เหมือน email verification

**Tech Stack:** Go 1.27 (go.mod `go 1.27.1`), Fiber v3.5.0, pgx v5.11.0, PostgreSQL 17.11, swaggo/swag v1.16.6 (Swagger 2.0), golangci-lint v2.14.0 — ไม่มี dependency ใหม่

**Spec:** [phase 1 spec](../specs/2026-10-05-phase-1-design.md) หัวข้อ 7 (sessions/otp_challenges/rate_limit_keys), 8 (`PasswordResetService`, `Mailer`), 9 "Auth และ OTP" (ลำดับ lock ของ reset), 10 (rate limits, decoy, `invalid_reset`) และ 11 (endpoints, status) กับ [AGENTS.md](../../../AGENTS.md) หัวข้อ "รายละเอียด Password Reset / OTP" และตาราง audit; ต้นแบบโค้ดคือ email verification ของ [แผน 4b](2026-10-24-milestone-4b-auth-endpoints.md)

## Global Constraints

- core (`internal/core/**` รวม `_test.go`) import ได้เฉพาะ standard library, `github.com/shopspring/decimal`, `github.com/google/uuid`, `golang.org/x/crypto/argon2` และ `go-backend-stock/internal/core/...`
- ไม่มี migration ใหม่: ใช้ `otp_challenges` purpose `password_reset`, `sessions.revoke_reason = 'password_reset'`, scopes `password_reset_request_email`/`password_reset_request_ip`/`password_reset_confirm_ip` และ event types `auth.password_reset_requested`/`auth.otp_verification_failed`/`auth.password_reset_succeeded`/`auth.sessions_revoked` ที่มีอยู่แล้ว
- Endpoints (ใต้ `/api/v1`, session + CSRF + Origin ทั้งคู่): `POST /auth/password-reset/request` `{email}` → 202 `{message,challenge_id,expires_in:600}` รูปแบบเดียวกันทุกกรณี; `POST /auth/password-reset/confirm` `{challenge_id,otp,new_password}` → 204 ไม่สร้าง session
- รหัสผิด/หมดอายุ/ใช้แล้ว/ถูกแทน/กรอกผิดครบ/ID ไม่รู้จัก/ID ของ purpose อื่น → 400 `invalid_reset` ข้อความเดียวกัน (`domain.ErrInvalidReset`)
- OTP: 6 หลักจาก `GenerateOTP`, HMAC ผ่าน `OTPCodec.Digest(domain.OTPPurposePasswordReset, challengeID, userID, code)`, อายุ `otpLifetime` (10 นาที), กรอกผิดได้ `otpMaxAttempts` (ค่าเริ่มต้น 5) ต่อ challenge, หนึ่ง user มี reset challenge ที่เปิดอยู่ได้ครั้งละหนึ่ง (`InvalidateOpen` ก่อน `Insert` ภายใต้ lock ของ user)
- Rate limits: request = cooldown `OTP_RESEND_COOLDOWN` (60 วินาที) + `OTP_REQUESTS_PER_HOUR_EMAIL` (5) ต่อ email ใน scope `password_reset_request_email` และ `OTP_REQUESTS_PER_HOUR_IP` (20) ต่อชั่วโมงต่อ IP ใน scope `password_reset_request_ip` (โควตาแยกจาก email verification); confirm = 20 ครั้ง/15 นาทีต่อ IP ใน scope `password_reset_confirm_ip`; admit ก่อนอ่านบัญชี จึงนับทุกอีเมลรวมอีเมลที่ไม่มีบัญชี
- request กับ confirm รอ `Pacer` (floor + jitter) ทุกเส้นทางรวม error ที่ไม่เปิดเผยสถานะ
- Lock order ของ confirm: lock user → lock challenge (ตามลำดับเดียวกับ use case อื่น); hash รหัสใหม่ก่อนเข้า transaction และไม่ถือ semaphore ของ hasher ใน transaction
- ความล้มเหลวของ OTP (นับ attempt + audit) commit ใน transaction ของตัวเองก่อนคืน `ErrInvalidReset` ห้ามให้ callback คืน domain error จน rollback
- ส่งอีเมล (`SendPasswordReset`, `SendPasswordChanged`) หลัง commit ผ่าน `(*Auth).send`; ส่งไม่สำเร็จแค่ log `auth.mail_not_sent` ไม่ rollback
- ห้าม log หรือส่งออก OTP, digest, รหัสผ่าน, hash, session token; ไม่เก็บอีเมลดิบใน audit metadata
- ห้ามแก้ `.env.example` และ `.env`
- ต้องเห็น test ล้มก่อนแล้วจึงเขียนโค้ดทุก task; จบ task ด้วย `gofmt -l internal cmd` ที่ไม่พิมพ์อะไร, `go vet ./...` และ `make lint` ผ่าน
- task ที่แก้ annotation ของ handler หรือ DTO ต้องรัน `make swagger` แล้ว commit `docs/api/` ในชุดเดียวกัน ห้ามแก้ไฟล์ generated ด้วยมือ
- integration tests ใช้ PostgreSQL จริงผ่าน `make -s test-integration` เท่านั้น (`-s` กัน Makefile echo รหัสผ่าน DB) และต้องไม่ SKIP; ใช้อีเมล/IP สุ่มต่อ test (`uniqueEmail`, `newBrowser`)
- Docker: ใช้ได้เฉพาะ `docker compose ps` และ `docker compose config --quiet`; ถ้า postgres ไม่ได้รันให้หยุดแล้วแจ้ง (ผู้ใช้เปิด stack เอง) ห้าม up/down/stop/restart และห้ามเรียก API ที่ผู้ใช้รันอยู่
- commit message ไม่มี `Co-Authored-By` หรือชื่อ Claude; commit ในเครื่องเท่านั้น ห้าม push; `git add` เฉพาะไฟล์ของ task
- ทำงานบน branch ใหม่ `feature/password_reset` ที่แตกจาก `developer` หลังผู้ใช้ merge งาน milestone 4 แล้ว (ถ้ายังไม่ merge ให้ถามก่อน); `go` คือ `/usr/local/go/bin/go`

## การตัดสินใจที่แผนนี้เสนอ (รอผู้ใช้ยืนยันตอนรีวิวแผน)

1. **บัญชี Google-only ใช้ลืมรหัสผ่านไม่ได้** — ตาม AGENTS.md ("forgot-password ไม่ใช้เพิ่ม local password ให้บัญชีชนิดนี้") จึงได้ decoy เหมือนอีเมลที่ไม่มีบัญชี; README และข้อส่งต่อของแผน 4c ที่เขียนว่าบัญชี Google "ตั้งรหัสผ่านได้ผ่านลืมรหัสผ่านใน milestone 5" **ขัดกับ AGENTS.md** และแผนนี้แก้ให้ตรง (Task 6) — ถ้าต้องการให้บัญชี Google ตั้งรหัสผ่านได้ ต้องเป็น feature แยก (ตั้งรหัสผ่านขณะ login อยู่) ไม่ใช่ forgot-password
2. **`password_contains_email` ตรวจหลัง OTP ถูกเท่านั้น** — handler ตรวจกฎอื่นทั้งหมดด้วย `PasswordPolicy.Parse(raw, domain.Email{})` แล้ว service ตรวจว่ารหัสมีส่วนหน้า @ ของอีเมลบัญชีหรือไม่ **ภายใน transaction หลัง OTP ผ่าน**; ถ้าตรวจก่อน ผู้โจมตีที่ขอ reset ให้อีเมลเหยื่อจะแยก challenge จริง (ได้ `password_contains_email`) กับ decoy (ได้ `invalid_reset`) และรู้ว่ามีบัญชี; เมื่อ OTP ถูกแต่รหัสมีอีเมล → 400 `invalid_input` reason `password_contains_email` โดยไม่ consume, ไม่นับ attempt และไม่เขียน audit จึงใช้รหัส OTP เดิมลองรหัสผ่านใหม่ได้
3. **ชนิด `service.PasswordReset` แยกจาก `*Auth`** — `*Auth` มี method `Request`/`Confirm` ของ `port.EmailVerificationService` แล้ว จึงสร้าง `func (a *Auth) PasswordReset() *PasswordReset` ที่ใช้ dependencies ร่วมกัน; port ใช้ `domain.Email` แทน `string` ตามแบบ `EmailVerificationService` ที่ทำไปแล้ว และ `Mailer.SendPasswordChanged` รับ `domain.Email`
4. **Audit:** `auth.password_reset_requested` เขียนเฉพาะเมื่อออก challenge จริง (actor NULL, target user) — อีเมลที่ไม่มีบัญชี/Google-only ไม่เขียนอะไรเหมือน email verification เพื่อไม่ให้ traffic โจมตีเติม `audit_events`; `auth.password_reset_succeeded` actor = user ที่พิสูจน์ด้วย OTP (เหมือน `auth.email_verified`); `auth.sessions_revoked` reason_code `password_reset` เขียนเฉพาะเมื่อ revoke ได้อย่างน้อย 1 session; failure ใช้ `auth.otp_verification_failed` actor NULL target `otp_challenge` และ reasons ชุดเดียวกับ email verification (`challenge_not_found`, `challenge_closed`, `challenge_expired`, `too_many_attempts`, `otp_mismatch`)
5. **reset สำเร็จ invalidate challenge ยืนยันอีเมลที่ยังเปิดอยู่ด้วย** — บัญชียืนยันแล้ว รหัสยืนยันเก่าไม่ควรใช้ได้ต่อ (เช่นรหัสที่ส่งให้ผู้สมัครชิงอีเมล)
6. **อนุญาตให้รหัสใหม่ซ้ำกับรหัสเดิม** — spec ไม่ได้ห้าม และการตรวจต้อง verify hash เดิมเพิ่มอีกหนึ่งครั้ง
7. **บัญชี local ที่ยังไม่ยืนยันอีเมลใช้ reset ได้** และ reset ตั้ง `email_verified_at` ตาม spec — นี่คือทางแก้ความเสี่ยงสมัครชิงอีเมลที่ยอมรับไว้ใน roadmap (2026-10-27)

## Review Focus

1. **สอง confirm พร้อมกันด้วยรหัสที่ถูก** — ต้องได้ 204 หนึ่งครั้งและ `invalid_reset` หนึ่งครั้ง, `auth.password_reset_succeeded` หนึ่งแถว, hash ตรงกับรหัสของคำขอที่ชนะ — Task 5 `TestConcurrentResetsWithTheRightCodeSucceedOnce`
2. **login ด้วยรหัสเก่าแข่งกับ reset** — หลังทั้งคู่จบต้องไม่มี session ที่เปิดอยู่ซึ่งสร้างด้วยรหัสเก่า — Task 5 `TestLoginRacingAResetLeavesNoOldPasswordSession`
3. **กรอกผิดแล้ว counter/audit ต้องอยู่** — 5 ครั้งผิดถูกนับและ audit, ครั้งที่ 6 ด้วยรหัสที่ถูกยังถูกปฏิเสธ `too_many_attempts` — Task 4 `TestResetWrongCodesCountAndThenLockTheChallenge`
4. **ไม่เปิดเผยว่ามีบัญชี** — request ตอบ body/status เหมือนกันทุกชนิดบัญชี และ confirm ด้วย decoy ID + รหัสผ่านที่มีอีเมลได้ `invalid_reset` ไม่ใช่ `password_contains_email` — Task 3 `TestResetRequestAnswersTheSameForEveryEmail`, Task 4 `TestResetPasswordContainingTheEmailIsCheckedOnlyAfterTheCode`
5. **รหัสเก่าและรหัสของ purpose อื่น** — ขอใหม่แล้วรหัสเก่าใช้ไม่ได้; challenge ID ของ email verification ใช้ reset ไม่ได้และกลับกัน — Task 3 `TestResetRequestReplacesTheOpenCode`, Task 4 `TestResetRefusesAVerificationChallenge`

---

## File Structure

| ไฟล์ | หน้าที่ | Task |
| --- | --- | --- |
| `internal/core/domain/auth_input.go`, `errors.go`, `password.go` (+tests) | `ResetPasswordInput`, `ErrInvalidReset`, `PasswordContainsEmail` | 1 |
| `internal/core/port/auth_service.go`, `port/mailer.go` | `PasswordResetService`, `Mailer.SendPasswordReset/SendPasswordChanged` | 1 |
| `internal/core/service/ratelimit.go` (+test) | `PasswordResetRequest`, `PasswordResetConfirm` | 1 |
| `internal/adapter/mail/message.go`, `smtp.go`, `dispatcher.go` (+tests, mailpit test) | อีเมลรหัส reset และแจ้งเปลี่ยนรหัสผ่าน | 2 |
| `internal/core/service/password_reset.go` (+test), `register.go`, `email_verification.go` | `PasswordReset.Request`, `newOTPChallenge` ที่ใช้ร่วมกัน | 3 |
| `internal/adapter/http/password_reset.go`, `auth.go`, `auth_dto.go`, `password_reset_test.go`, `auth_harness_test.go`, `auth_contract_test.go`, `docs/api/` | handler request + harness/fake mailer | 3 |
| `internal/core/service/password_reset.go` (+test), `internal/adapter/http/password_reset.go`, `password_reset_confirm_test.go`, `auth_contract_test.go`, `docs/api/` | `PasswordReset.Reset` + handler confirm | 4 |
| `internal/adapter/http/password_reset_race_test.go` | การแข่งกันและ audit rollback | 5 |
| `cmd/api/wire.go` (+test), `cmd/api/main.go`, `README.md`, roadmap, spec, `docs/api/` | ประกอบ dependency, คู่มือ, สถานะ | 6 |

---

### Task 1: ชนิดข้อมูล, ports และ rate limits ของ reset

**Files:**
- Modify: `internal/core/domain/auth_input.go`, `internal/core/domain/errors.go`, `internal/core/domain/password.go`, `internal/core/port/auth_service.go`, `internal/core/port/mailer.go`, `internal/core/service/ratelimit.go`
- Test: `internal/core/domain/password_test.go`, `internal/core/domain/errors_test.go`, `internal/core/service/ratelimit_test.go`

**Interfaces:**
- Produces:
  - `domain.ResetPasswordInput{ChallengeID ID; OTP OTPCode; NewPassword Password}`
  - `domain.ErrInvalidReset = NewError(CodeInvalidReset, "reset code is invalid or has expired")` ในบล็อกเดียวกับ `ErrInvalidVerification`
  - `domain.PasswordContainsEmail(p Password, email Email) bool` — ใช้ `containsEmailLocalPart(strings.ToLower(p.Reveal()), email)` ตัวเดียวกับ `passwordReasons`
  - `port.PasswordResetService{ Request(ctx, email domain.Email, meta domain.RequestMeta) (domain.OTPReceipt, error); Reset(ctx, in domain.ResetPasswordInput, meta domain.RequestMeta) error }` ใน `auth_service.go`
  - `port.Mailer` เพิ่ม `SendPasswordReset(ctx, mail domain.OTPMail) error` และ `SendPasswordChanged(ctx, to domain.Email) error`
  - `(*RateLimitPolicy).PasswordResetRequest(email domain.Email, ip netip.Addr) ([]domain.RateLimitRule, error)` — สาม rules: `password_reset_request_email` limit 1 window `OTPResendCooldown`, `password_reset_request_email` limit `OTPRequestsPerHourEmail` window 1 ชม., `password_reset_request_ip` limit `OTPRequestsPerHourIP` window 1 ชม.
  - `(*RateLimitPolicy).PasswordResetConfirm(ip netip.Addr) ([]domain.RateLimitRule, error)` — `password_reset_confirm_ip` limit 20 window 15 นาที (const `resetConfirmIP = 20`)

การเพิ่ม method ใน `port.Mailer` ทำให้ `mail.SMTPMailer`, `mail.Dispatcher` และ `fakeMailer` ของ `internal/adapter/http` ไม่ implement port จนจบ Task 2/3: ใน task นี้ให้เพิ่ม stub สองตัวที่ fake mailer ของ HTTP (`return nil`) และให้ `SMTPMailer`/`Dispatcher` คืน `errors.New("not implemented")` เพื่อให้ build ผ่าน แล้ว Task 2 แทนด้วยของจริง (fake ของ service ฝัง interface ไว้แล้ว ไม่ต้องแก้)

- [ ] **Step 1: เขียน failing tests**

`internal/core/domain/password_test.go`:
```go
func TestPasswordContainsEmail(t *testing.T) {
	email := mustEmail(t, "Alice.Smith@example.com")
	for raw, want := range map[string]bool{
		"Xx!alice.smith9": true,  // the local part, case-insensitive
		"Xx!ALICE.SMITH9": true,
		"Xx!alice9":       false, // a piece of it is not enough
	} {
		p, err := ParsePassword(raw, Email{})
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		if got := PasswordContainsEmail(p, email); got != want {
			t.Errorf("PasswordContainsEmail(%q) = %v, want %v", raw, got, want)
		}
	}
	p, _ := ParsePassword("Xx!alice.smith9", Email{})
	if PasswordContainsEmail(p, Email{}) {
		t.Error("the zero email matches nothing")
	}
}

func TestParsePasswordWithoutAnEmailSkipsOnlyTheEmailRule(t *testing.T) {
	_, err := ParsePassword("short", Email{})
	if got := reasonsOf(t, err); !slices.Contains(got, string(PasswordReasonTooShort)) || slices.Contains(got, string(PasswordReasonContainsEmail)) {
		t.Errorf("reasons = %v", got)
	}
}
```
(ถ้าไฟล์ test ยังไม่มี `mustEmail`/`reasonsOf` ให้ใช้ helper ที่มีอยู่ใน `helpers_test.go`/`password_test.go` ตามชื่อจริง)

`internal/core/domain/errors_test.go` — เพิ่ม case ให้ test ที่ตรวจ sentinel/code อยู่แล้ว: `ErrInvalidReset` มี `Code == CodeInvalidReset` และ `errors.Is(fmt.Errorf("x: %w", ErrInvalidReset), ErrInvalidReset)`

`internal/core/service/ratelimit_test.go`:
```go
func TestPasswordResetRequestRules(t *testing.T) {
	p := newTestPolicy(t) // OTPResendCooldown 1m, 5/h email, 20/h IP
	email := mustEmail(t, "a@example.com")
	rules, err := p.PasswordResetRequest(email, netip.MustParseAddr("203.0.113.9"))
	if err != nil {
		t.Fatal(err)
	}
	// want, in order: (password_reset_request_email, 1, 1m), (…_email, 5, 1h), (password_reset_request_ip, 20, 1h)
	// and the email keys differ from the email-verification keys of the same address
}

func TestPasswordResetConfirmRule(t *testing.T) {
	// one rule: password_reset_confirm_ip, 20, 15m; a zero IP is an internal error like the other rules
}
```
เขียน assertions ของสองฟังก์ชันให้ตรงกับ comment โดยใช้รูปแบบเดียวกับ test ของ `EmailVerificationRequest` ในไฟล์เดียวกัน

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/... -count=1`
Expected: FAIL (compile) — `PasswordContainsEmail`, `ErrInvalidReset`, `PasswordResetRequest`, `PasswordResetConfirm` undefined

- [ ] **Step 3: เขียนโค้ดตาม Interfaces** พร้อม stub ของ mailer ตามย่อหน้าก่อน Step 1

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal cmd; go vet ./... && go test ./internal/... -count=1 -race && make lint`
Expected: ผ่าน

- [ ] **Step 5: Commit**

```bash
git add internal/core internal/adapter/mail internal/adapter/http/auth_harness_test.go
git commit -m "feat(domain): add the password reset input, its error and rate limits"
```

---

### Task 2: อีเมลรหัส reset และอีเมลแจ้งเปลี่ยนรหัสผ่าน

**Files:**
- Modify: `internal/adapter/mail/message.go`, `internal/adapter/mail/smtp.go`, `internal/adapter/mail/dispatcher.go`
- Test: `internal/adapter/mail/message_test.go`, `internal/adapter/mail/dispatcher_test.go`, `internal/adapter/mail/mailpit_integration_test.go`

**Interfaces:**
- Consumes: `port.Mailer` (Task 1)
- Produces: `(*SMTPMailer).SendPasswordReset/SendPasswordChanged`, `(*Dispatcher).SendPasswordReset/SendPasswordChanged` (job kinds `password_reset`, `password_changed`)

ข้อความ (copy ตายตัว):
- `passwordResetSubject = "รหัสสำหรับตั้งรหัสผ่านใหม่"`
- `passwordResetBody(m)`: `"รหัสสำหรับตั้งรหัสผ่านใหม่ของคุณคือ " + code + "\n\n" + "รหัสนี้ใช้ได้ครั้งเดียวถึง " + เวลาไทยตาม otpExpiryLayout + " น. (เวลาประเทศไทย) ห้ามบอกรหัสนี้กับผู้อื่น\n\n" + "รหัสอ้างอิง: " + challengeID + "\n\n" + "หากคุณไม่ได้ขอตั้งรหัสผ่านใหม่ ไม่ต้องทำอะไร รหัสผ่านเดิมยังใช้ได้\n"`
- `passwordChangedSubject = "รหัสผ่านของคุณถูกเปลี่ยนแล้ว"`
- `passwordChangedBody()`: `"รหัสผ่านของบัญชีนี้เพิ่งถูกเปลี่ยน และทุกอุปกรณ์ที่เข้าสู่ระบบอยู่ถูกออกจากระบบแล้ว\n\n" + "หากเป็นคุณ ให้เข้าสู่ระบบด้วยรหัสผ่านใหม่\n" + "หากไม่ใช่คุณ ให้ใช้ \"ลืมรหัสผ่าน\" ตั้งรหัสใหม่ทันที\n"` (ไม่มีรหัสผ่านและไม่มีตัวเลข 6 หลัก)

- [ ] **Step 1: เขียน failing tests**
  - `message_test.go`: `TestPasswordResetBodyCarriesTheCodeAndReference` (มี `"รหัสสำหรับตั้งรหัสผ่านใหม่ของคุณคือ 042731"`, challenge ID และเวลาไทย) และ `TestPasswordChangedBodyHasNoCode` (`sixDigitCode` ไม่ match)
  - `dispatcher_test.go`: ตามแบบ test ของ `SendEmailVerification` — `SendPasswordReset`/`SendPasswordChanged` ส่งงานไปที่ sender ด้วย kind `password_reset`/`password_changed` และ log ของ job ที่ล้มไม่มีรหัสหรืออีเมล
  - `mailpit_integration_test.go`: `TestMailpitReceivesThePasswordResetEmail` (subject `passwordResetSubject`, body มี `042731` และ challenge ID, `assertThaiTransport`) และ `TestMailpitReceivesThePasswordChangedEmail` (subject ถูก, ไม่มีเลข 6 หลัก)

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/adapter/mail/ -count=1`
Expected: FAIL — `passwordResetBody` undefined หรือ stub คืน "not implemented"

- [ ] **Step 3: เขียนโค้ด** — แทน stub ของ Task 1 ด้วย `m.send(ctx, to, subject, body)` และ `d.enqueue(kind, …)` แบบ method ที่มีอยู่

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal; go vet ./... && go test ./internal/adapter/mail/ -count=1 -race && make test-mail && make lint`
Expected: ผ่าน; `make test-mail` ส่งถึง Mailpit จริง (ถ้า mailpit ไม่ได้รันตาม `docker compose ps` ให้หยุดแล้วแจ้ง)

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/mail
git commit -m "feat(mail): send the password reset code and the password changed notice"
```

---

### Task 3: `PasswordReset.Request` และ `POST /auth/password-reset/request`

**Files:**
- Create: `internal/core/service/password_reset.go`, `internal/core/service/password_reset_test.go`, `internal/adapter/http/password_reset.go`, `internal/adapter/http/password_reset_test.go`
- Modify: `internal/core/service/register.go`, `internal/core/service/email_verification.go`, `internal/adapter/http/auth.go`, `internal/adapter/http/auth_harness_test.go`, `internal/adapter/http/auth_contract_test.go`, `docs/api/`

**Interfaces:**
- Consumes: Task 1–2; `(*Auth).admit`, `appendAudit`, `send`, `pacer`, `limits`
- Produces:
  - `type PasswordReset struct{ a *Auth }`, `func (a *Auth) PasswordReset() *PasswordReset`, `var _ port.PasswordResetService = (*PasswordReset)(nil)`
  - `(*Auth).newOTPChallenge(purpose domain.OTPPurpose, userID domain.ID, now time.Time) (domain.OTPCode, domain.OTPChallenge, error)` แทน `newVerificationChallenge` (register และ email verification เรียกด้วย `domain.OTPPurposeEmailVerification`)
  - `httpadapter.AuthOptions.PasswordReset port.PasswordResetService` — nil ไม่ลงทะเบียน routes ของ reset (เหมือน `Verification`)
  - route `POST /api/v1/auth/password-reset/request` (session, csrf) → handler `requestPasswordReset`; body `EmailRequest`; ตอบ `newOTPAccepted(receipt)` 202
  - HTTP test helpers: `(*browser).requestReset(email string) (response, []byte)`; fake mailer `resets map[string][]domain.OTPMail`, `changed map[string]int`, `(*fakeMailer).lastReset(t, email) domain.OTPMail`, `resetCount(email) int`, `changedCount(email) int`; harness ส่ง `PasswordReset: auth.PasswordReset()`

Flow ของ `Request` (ลำดับนี้คือข้อกำหนด):
1. `defer pacer.Wait(ctx, clock.Now())` → `limits.PasswordResetRequest(email, meta.ClientIP)` → `admit(ctx, rules, meta, domain.ID{})`
2. สร้าง decoy receipt (`domain.NewID()`, `otpLifetime`)
3. `store.Users().CredentialsByEmail` — `ErrNotFound` หรือ `!creds.HasPassword()` (Google-only) → คืน decoy
4. `newOTPChallenge(OTPPurposePasswordReset, …)` นอก transaction
5. transaction: `Users().Lock` → `OTPChallenges().InvalidateOpen(user, OTPPurposePasswordReset, now)` → `Insert` → audit `auth.password_reset_requested` (actor NULL, target `user`/user ID)
6. หลัง commit `send(meta, "password_reset", SendPasswordReset(OTPMail{To: email, OTP, ChallengeID, ExpiresAt}))` แล้วคืน receipt จริง

- [ ] **Step 1: เขียน failing tests**

`internal/core/service/password_reset_test.go` (fake repositories ของ `newTestAuth`):
- `TestResetRequestWaitsForTheResponseFloorOnEveryPath` — unknown, Google-only (credentials ไม่มี hash) และบัญชีที่มีรหัสผ่าน ต่างรอ pacer หนึ่งครั้งเท่ากับ floor (แบบ `TestConfirmWaitsForTheResponseFloorOnEveryPath`)
- `TestResetRequestRateLimitedWritesNothing` — limiter ปฏิเสธ → `rate_limited`, ไม่อ่าน users, ไม่มี transaction

`fakeMailer{}` ของ service ฝัง `port.Mailer` ที่เป็น nil จึง panic ถ้าเส้นทางส่งอีเมล: test ที่ไปถึงการส่งให้ตั้ง `d.Mailer` เป็น recorder ผ่าน `mutate` ของ `newTestAuth`; ถ้า fake repositories ยังไม่รองรับ method ที่ flow ใช้ (`Lock`, `InvalidateOpen`, `Insert`) ให้เพิ่มใน `fakes_test.go` แบบ fake ที่มีอยู่

`internal/adapter/http/password_reset_test.go`:
```go
func TestResetRequestSendsACodeToAnAccountWithAPassword(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)

	resp, body := b.requestReset(email)
	if resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("request = %d %s", resp.StatusCode, body)
	}
	out := decodeAccepted(t, body)
	mail := h.mailer.lastReset(t, email)
	if mail.ChallengeID.String() != out.ChallengeID || out.ExpiresIn != 600 {
		t.Errorf("response %+v and email %s must name the same challenge, valid 600s", out, mail.ChallengeID)
	}
	ch := h.challenges(t, u.ID)
	if last := ch[len(ch)-1]; last.Purpose != "password_reset" || !last.Open || last.ID != out.ChallengeID {
		t.Errorf("challenges = %+v", ch)
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "auth.password_reset_requested" || rows[0].Actor != "" || rows[0].TargetID != u.ID {
		t.Errorf("audit = %+v, want password_reset_requested on the user with actor NULL", rows)
	}
}

func TestResetRequestAnswersTheSameForEveryEmail(t *testing.T) {
	// accounts: "unknown" (none), "verified" and "unverified" local, "google-only" (Users().Insert without a
	// password, as in TestRequestAnswersTheSameForEveryOtherEmail).
	// Each request: 202, decodeAccepted ok; stripJSONField(body, "challenge_id") equal across all four;
	// resetCount grows by 1 only for verified and unverified; audit rows only for those two.
}

func TestResetRequestReplacesTheOpenCode(t *testing.T) {
	// request, advance 61s, request again: the first reset challenge is closed, the second open, and
	// two codes were mailed with different challenge IDs (confirming the old code is Task 4's test).
}

func TestResetRequestCooldownAndHourlyQuota(t *testing.T) {
	// second request within 60s → 429 rate_limited with Retry-After;
	// after advancing 61s between requests, the 6th request within the hour → 429;
	// an email-verification request for the same email is not blocked by these (separate scope).
}

func TestResetRequestCountsUnknownEmailsAgainstTheIP(t *testing.T) {
	// one browser (one IP): 20 requests for 20 unknown emails → 202, the 21st → 429.
}
```
เพิ่มใน `auth_contract_test.go`: `{"post", "/api/v1/auth/password-reset/request", []int{202, 400, 403, 429, 500, 503}},`

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/service/ -count=1 && make -s test-integration`
Expected: service tests compile fail (`PasswordReset` undefined); หลังมี stub ว่าง integration ได้ 404 ที่ `/password-reset/request` และ `TestAuthOperationsAreDocumented` ล้ม

- [ ] **Step 3: เขียนโค้ด** — `password_reset.go` ตาม flow ข้างบน; เปลี่ยน `newVerificationChallenge` เป็น `newOTPChallenge` ทุกจุด; handler ทำแบบ `requestVerification` (decode `EmailRequest` → `ParseEmail` → `requestMeta` → `h.reset.Request`); annotations:
  - `@Summary Ask for a password reset code`, `@ID requestPasswordReset`, Tags Auth, `X-CSRF-Token` header, body `EmailRequest`, 202 `OTPAcceptedResponse`, 400/403/429/500/503 แบบเดียวกับ `requestVerification`
  - `@Description` ต้องบอก: ตอบเหมือนกันทุกอีเมล (ไม่บอกว่ามีบัญชี), ส่งรหัสเฉพาะบัญชีที่มีรหัสผ่าน (บัญชีที่สมัครด้วย Google อย่างเดียวไม่ได้รับ), รหัสอายุ 10 นาที, ขอใหม่แล้วรหัสเก่าใช้ไม่ได้, 1 ครั้ง/นาที 5 ครั้ง/ชม. ต่ออีเมล

- [ ] **Step 4: รัน test ให้ผ่าน และ generate Swagger**

Run: `docker compose ps; gofmt -l internal cmd; go vet ./... && make swagger && go test ./internal/... -count=1 -race && make -s test-integration && make lint`
Expected: ผ่านทั้งหมด รวม test ของ register/email verification หลังเปลี่ยนชื่อ helper

- [ ] **Step 5: Commit**

```bash
git add internal/core/service internal/adapter/http docs/api
git commit -m "feat(auth): send a password reset code without revealing whether the account exists"
```

---

### Task 4: `PasswordReset.Reset` และ `POST /auth/password-reset/confirm`

**Files:**
- Create: `internal/adapter/http/password_reset_confirm_test.go`
- Modify: `internal/core/service/password_reset.go`, `internal/core/service/password_reset_test.go`, `internal/adapter/http/password_reset.go`, `internal/adapter/http/auth.go`, `internal/adapter/http/auth_dto.go`, `internal/adapter/http/auth_contract_test.go`, `docs/api/`

**Interfaces:**
- Consumes: Task 1–3; `recordFailure`, `appendAudit`, `hasher.Hash`, `otp.Verify`, repositories `OTPChallenges().ByID/Lock/RecordFailure/Consume/InvalidateOpen`, `Users().ByID/Lock/SetPassword/MarkEmailVerified`, `Sessions().RevokeAll`
- Produces:
  - DTO `ResetPasswordRequest{ChallengeID string \`json:"challenge_id"\`; OTP string \`json:"otp"\`; NewPassword string \`json:"new_password"\`}` (required ทั้งสาม; example ของ `new_password` เป็น `"Zq9!kVx1"`)
  - route `POST /api/v1/auth/password-reset/confirm` (session, csrf) → `confirmPasswordReset` → 204
  - `(*browser).confirmReset(challengeID, otp, newPassword string) (response, []byte)`
  - `func resetFailed(challengeID domain.ID, reason string) auditSpec` (event `auth.otp_verification_failed`, target `otp_challenge`)

Handler: decode → `ParseID("challenge_id")` → `ParseOTP` → `h.passwords.Parse(req.NewPassword, domain.Email{})` (กฎทั้งหมดยกเว้นอีเมล) → `requestMeta` → `h.reset.Reset` → 204

Flow ของ `Reset` (ลำดับนี้คือข้อกำหนด):
1. `defer pacer.Wait` → `limits.PasswordResetConfirm(ip)` → `admit`
2. `store.OTPChallenges().ByID(ctx, OTPPurposePasswordReset, id)` — `ErrNotFound` → `recordFailure(resetFailed(id, "challenge_not_found"))` → `ErrInvalidReset`
3. `store.Users().ByID(found.UserID)` (อีเมลของบัญชีสำหรับกฎอีเมลและอีเมลแจ้ง) แล้ว `hasher.Hash(ctx, in.NewPassword)` — ทั้งคู่นอก transaction
4. transaction (reset ตัวแปรผลทุกครั้งที่ callback เริ่ม เพราะ UoW อาจ retry):
   `Users().Lock` → `OTPChallenges().Lock` (`ErrNotFound` → audit `challenge_not_found`) → ตรวจตามลำดับ `challenge_closed` / `challenge_expired` (`!now.Before(ExpiresAt)`) / `too_many_attempts` / `otp_mismatch` (+`RecordFailure`) แล้ว audit failure และ `return nil`
   → OTP ถูกแต่ `domain.PasswordContainsEmail(in.NewPassword, user.Email)` → ตั้ง flag `weak` แล้ว `return nil` โดยไม่เขียนอะไร
   → `Consume` → `SetPassword(hash, now)` → `MarkEmailVerified(now)` → `InvalidateOpen(user, OTPPurposeEmailVerification, now)` → `n := Sessions().RevokeAll(user, now, RevokeReasonPasswordReset)` → audit `auth.password_reset_succeeded` (actor user, target `user`) → ถ้า `n > 0` audit `auth.sessions_revoked` (actor user, target `user`, reason `password_reset`)
5. หลัง commit: failure → `ErrInvalidReset`; `weak` → `domain.NewReasonsError(domain.CodeInvalidInput, "password does not meet the requirements", string(domain.PasswordReasonContainsEmail))`; สำเร็จ → `send(meta, "password_changed", SendPasswordChanged(user.Email))`

- [ ] **Step 1: เขียน failing tests**

`internal/core/service/password_reset_test.go`:
- `TestResetWaitsForTheResponseFloorOnEveryPath` — decoy ID และ challenge จริงรอ floor เท่ากัน
- `TestResetLockMissAfterByIDIsAnAuditedInvalidReset` — แบบ `TestConfirmLockMissAfterByIDIsAnAuditedInvalidVerification` แต่ event `auth.otp_verification_failed`
- `TestResetHashesBeforeTheTransaction` — hasher ที่บันทึกว่าถูกเรียกขณะ `fakeUoW` อยู่ใน transaction หรือไม่ต้องได้ "ไม่อยู่"

`internal/adapter/http/password_reset_confirm_test.go` (helper ในไฟล์: `resetCode(t, h, b, email) domain.OTPMail` = `requestReset` แล้ว `lastReset`):
```go
func TestResetChangesThePasswordOnceAndRevokesEverySession(t *testing.T) {
	h := newAuthHarness(t)
	email := uniqueEmail(t)
	h.newBrowser(t).signUpAndVerify(email, "Zq9!kVx1")
	phone, laptop := h.newBrowser(t), h.newBrowser(t)
	phone.login(email, "Zq9!kVx1")
	laptop.login(email, "Zq9!kVx1")
	u, _ := h.user(t, email)

	b := h.newBrowser(t)
	mail := resetCode(t, h, b, email)
	resp, body := b.confirmReset(mail.ChallengeID.String(), mail.OTP.Reveal(), "Nw7#pLq2")
	if resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("confirm = %d %s", resp.StatusCode, body)
	}
	if b.jar["session"] != "" {
		t.Error("reset does not sign in")
	}
	for _, old := range []*browser{phone, laptop} {
		if resp, _ := old.me(); resp.StatusCode != nethttp.StatusUnauthorized {
			t.Errorf("an old session still works: %d", resp.StatusCode)
		}
	}
	if resp, _ := h.newBrowser(t).login(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusUnauthorized {
		t.Error("the old password must stop working")
	}
	if resp, body := h.newBrowser(t).login(email, "Nw7#pLq2"); resp.StatusCode != nethttp.StatusOK {
		t.Errorf("new password login = %d %s", resp.StatusCode, body)
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if got := eventTypes(rows); !slices.Equal(got, []string{"auth.password_reset_succeeded", "auth.sessions_revoked"}) {
		t.Fatalf("audit = %v", got)
	}
	if rows[0].Actor != u.ID || rows[1].Reason != "password_reset" {
		t.Errorf("audit rows = %+v", rows)
	}
	if h.mailer.changedCount(email) != 1 {
		t.Error("the owner must be told the password changed")
	}
	after, _ := h.user(t, email)
	again, body := h.newBrowser(t).confirmReset(mail.ChallengeID.String(), mail.OTP.Reveal(), "Other#9pw")
	if again.StatusCode != nethttp.StatusBadRequest || decodeError(t, body).Code != "invalid_reset" {
		t.Errorf("reusing the code = %d %s, want 400 invalid_reset", again.StatusCode, body)
	}
	if now, _ := h.user(t, email); now.PasswordHash != after.PasswordHash {
		t.Error("a used code must not change the password again")
	}
}
```

tests อื่นในไฟล์เดียวกัน (assertions ตามชื่อและ comment):
- `TestResetVerifiesAnUnverifiedAccount` — `register` แล้ว reset โดยไม่ confirm อีเมล → 204, `h.user(…).Verified == true`, challenge ยืนยันอีเมลที่เปิดอยู่ถูกปิด (`h.challenges` ไม่มี `email_verification` ที่ `Open`), login ด้วยรหัสใหม่ได้ 200 — นี่คือกรณีเจ้าของอีเมลยึดบัญชีที่ผู้อื่นสมัครชิงไว้คืน
- `TestResetWrongCodesCountAndThenLockTheChallenge` — รหัสผิด 5 ครั้ง (ใช้รหัสที่ต่างจากรหัสจริง) ได้ 400 `invalid_reset` ทุกครั้ง, `failed_attempts` = 5, audit `auth.otp_verification_failed` reason `otp_mismatch` 5 แถว actor ว่าง; ครั้งที่ 6 ด้วยรหัสถูก → 400 และ audit reason `too_many_attempts`; รหัสผ่านไม่เปลี่ยน
- `TestResetCodeExpiresAfterTenMinutes` — `h.clock.Advance(10 * time.Minute)` แล้วรหัสถูก → 400, audit `challenge_expired`; อีก challenge ที่ advance `10*time.Minute - time.Second` → 204
- `TestResetOldCodeStopsWorkingAfterANewRequest` — ขอ, advance 61s, ขอใหม่; รหัสแรก → 400 audit `challenge_closed`; รหัสที่สอง → 204
- `TestResetRefusesAVerificationChallenge` — challenge ID + รหัสจาก `register` → `confirmReset` ได้ 400 `invalid_reset` audit `challenge_not_found` และ challenge นั้นยังเปิดอยู่ (ใช้ยืนยันอีเมลได้ 204); ID ของ reset ใช้กับ `/email-verification/confirm` ได้ 400 `invalid_verification`
- `TestResetKeepsALeadingZero` — แทน digest ของ challenge ที่เปิดอยู่ด้วยรหัส `"012345"` ผ่าน admin connection (`UPDATE app.otp_challenges SET otp_hash = $1 WHERE id = $2` โดย digest จาก `service.NewOTPCodec([]byte(testOTPSecret)).Digest(domain.OTPPurposePasswordReset, id, userID, code)`; ให้ย้าย `strings.Repeat("o", 32)` ใน `newAuthHarness` เป็น `const testOTPSecret = "oooooooooooooooooooooooooooooooo"` แล้วใช้ทั้งสองที่) แล้ว confirm ด้วย `"012345"` → 204; `"12345"` → 400 `invalid_input` และไม่นับ attempt
- `TestResetPasswordContainingTheEmailIsCheckedOnlyAfterTheCode` — อีเมล `uniqueEmail` ที่ส่วนหน้า @ ยาว ≥ 4; รหัสผ่าน = `"Xx!" + localPart`:
  1. decoy challenge ID (UUID สุ่ม) + รหัสผ่านนั้น → 400 `invalid_reset` (ไม่ใช่ `invalid_input`)
  2. challenge จริง + รหัส OTP ผิด → 400 `invalid_reset`
  3. challenge จริง + รหัส OTP ถูก → 400 `invalid_input` details มี `password_contains_email`; challenge ยังเปิด, `failed_attempts` ไม่เพิ่มจากข้อ 2, ไม่มี audit ของคำขอนี้
  4. รหัส OTP เดิม + `"Nw7#pLq2"` → 204
- `TestResetRejectsAWeakPasswordBeforeTheCode` — `"abc"` → 400 `invalid_input` พร้อม reasons ของความยาว/ตัวพิมพ์; challenge ไม่ถูกแตะ
- `TestResetConfirmIsLimitedPerIP` — browser เดียว 20 คำขอด้วย decoy → 400 ทั้งหมด, ครั้งที่ 21 → 429 พร้อม `Retry-After`

เพิ่มใน `auth_contract_test.go`: `{"post", "/api/v1/auth/password-reset/confirm", []int{204, 400, 403, 429, 500, 503}},`

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/service/ -count=1; make -s test-integration`
Expected: FAIL — `Reset` ยังไม่มี (compile) แล้ว 404 ที่ `/password-reset/confirm`

- [ ] **Step 3: เขียนโค้ด** ตาม flow ข้างบน; annotations ของ `confirmPasswordReset`:
  - `@Summary Set a new password with the reset code`, `@ID confirmPasswordReset`, body `ResetPasswordRequest`, 204 "Password changed", 400 `"invalid_reset, invalid_input (password rules, details lists the reasons) or malformed_json"`, 403 `csrf_failed`, 429 + Retry-After, 500, 503 `"dependency_unavailable or server_busy"`
  - `@Description` บอก: รหัสใช้ได้ครั้งเดียว, ผิดได้ 5 ครั้ง, ทุกกรณีของรหัสที่ใช้ไม่ได้ตอบ `invalid_reset` เหมือนกัน, กติการหัสผ่านเดียวกับ register, สำเร็จแล้วยืนยันอีเมล ออกจากระบบทุกอุปกรณ์ ส่งอีเมลแจ้ง และไม่ login ให้ (เรียก `POST /api/v1/auth/login` ต่อ)

- [ ] **Step 4: รัน test ให้ผ่าน และ generate Swagger**

Run: `docker compose ps; gofmt -l internal cmd; go vet ./... && make swagger && go test ./internal/... -count=1 -race && make -s test-integration && make lint`
Expected: ผ่านทั้งหมด

- [ ] **Step 5: Commit**

```bash
git add internal/core/service internal/adapter/http docs/api
git commit -m "feat(auth): set a new password with the reset code, verify the email and revoke every session"
```

---

### Task 5: การแข่งกันของ reset และ audit rollback

task นี้เพิ่มเฉพาะ test: ถ้า test ใดล้มแสดงว่าโค้ดของ Task 3–4 ผิด ให้หาสาเหตุ (superpowers:systematic-debugging) แล้วแก้โค้ดตัวนั้นใน commit `fix(auth): …` แยก ไม่แก้ test ให้ผ่าน

**Files:**
- Create: `internal/adapter/http/password_reset_race_test.go`

**Interfaces:**
- Consumes: harness, `withFailingAudit`, `requireAuditFailure`, `tryDo`, helpers ของ Task 3–4

- [ ] **Step 1: เขียน tests**

- `TestConcurrentResetsWithTheRightCodeSucceedOnce` — browser สองตัว (สอง IP) ส่ง confirm ด้วย challenge/รหัสเดียวกันแต่ `new_password` ต่างกันพร้อมกันผ่าน goroutines + `tryDo`; ต้องได้ 204 หนึ่งและ 400 `invalid_reset` หนึ่ง; `auth.password_reset_succeeded` ของ user มีหนึ่งแถว (`h.auditByTarget(t, u.ID)`); login ด้วยรหัสของฝั่งที่ได้ 204 ผ่าน และของอีกฝั่งได้ 401; `changedCount` = 1
- `TestLoginRacingAResetLeavesNoOldPasswordSession` — วน 10 รอบ (อีเมลใหม่ทุกรอบ): login ด้วยรหัสเก่าและ confirm reset พร้อมกัน; หลังทั้งคู่จบ ถ้า login ได้ 200 ต้องให้ `me()` ของ browser นั้นได้ 401 (session ถูก revoke) หรือ login ต้องได้ 401; ห้ามมีกรณีที่ login 200 แล้ว `me()` ยัง 200
- `TestConcurrentWrongResetCodesAreAllCounted` — 8 คำขอรหัสผิดพร้อมกันจากคนละ IP; หลังจบ `failed_attempts` = 5 (เพดาน) และ audit `otp_mismatch` 5 แถว + `too_many_attempts` 3 แถว (แบบ `TestConcurrentWrongCodesAreSerializedByTheChallengeLock`)
- `TestResetWithoutItsAuditChangesNothing` — `failing := newAuthHarness(t, withFailingAudit())` ใช้ DB เดียวกับ harness ปกติ: สมัคร/ยืนยัน/login และขอรหัสผ่าน harness ปกติ แล้ว confirm ผ่าน `failing` → `requireAuditFailure`; จาก harness ปกติตรวจว่า hash เดิม, challenge ยังเปิด, session เดิมยังใช้ได้ (`me()` 200) และ `changedCount` ของ `failing.mailer` = 0
- `TestResetLogsHoldNoSecrets` — หลัง request + confirm สำเร็จ, `h.logs.String()` ต้องไม่มีรหัส OTP, รหัสผ่านใหม่, ค่า session cookie เดิม หรืออีเมล และต้องมี `"route":"/api/v1/auth/password-reset/confirm"` (พิสูจน์ว่าเก็บ log ได้จริง)

- [ ] **Step 2: รัน test**

Run: `docker compose ps && go vet ./internal/adapter/http/ && make -s test-integration`
Expected: ผ่าน (ถ้าล้มให้แก้โค้ดตามหมายเหตุต้น task)

- [ ] **Step 3: รันชุดเต็มและ lint**

Run: `gofmt -l internal; go test ./... -count=1 -race && make lint`
Expected: ผ่าน

- [ ] **Step 4: Commit**

```bash
git add internal/adapter/http/password_reset_race_test.go
git commit -m "test(auth): prove password reset under concurrent resets, logins and a failing audit"
```

---

### Task 6: ประกอบใน `cmd/api`, คู่มือและสถานะ

**Files:**
- Modify: `cmd/api/wire.go`, `cmd/api/wire_test.go`, `cmd/api/main.go`, `README.md`, `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md`, `docs/superpowers/specs/2026-10-22-milestone-4-auth-design.md`, `docs/api/` (generated)

**Interfaces:**
- Consumes: `(*Auth).PasswordReset()` (Task 3), `AuthOptions.PasswordReset` (Task 3)

- [ ] **Step 1: เขียน failing test** — ใน `TestBuildAuthServesTheAuthRoutes` เพิ่ม `o.PasswordReset == nil` ในเงื่อนไขที่ `t.Fatalf("options = %+v", o)`

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./cmd/api/ -count=1`
Expected: FAIL ที่ `options = …`

- [ ] **Step 3: เขียนโค้ดและเอกสาร**
  - `buildAuth`: `PasswordReset: auth.PasswordReset()` ใน `httpadapter.AuthOptions`
  - `cmd/api/main.go` `@description` บรรทัดที่สอง: `Milestone 5 exposes Health and the Auth endpoints (email and password, email verification, password reset, Google sign-in and linking); portfolios and transactions are not implemented yet.`
  - README:
    - บรรทัดสถานะ → `> **สถานะ:** Milestone 5 — สมัครด้วยอีเมล+รหัสผ่าน, ยืนยันอีเมลด้วย OTP, login/logout, session ฝั่ง server, ลืมรหัสผ่านด้วย OTP และ Google sign-in/เชื่อมบัญชี (เปิดเมื่อตั้งค่า \`GOOGLE_*\`) ใช้งานผ่าน API ได้แล้ว; พอร์ตและรายการซื้อขายยังไม่เปิดใช้งาน`
    - บรรทัด "ระบบไม่เชื่อมบัญชีให้อัตโนมัติ…" → `ระบบไม่เชื่อมบัญชีให้อัตโนมัติเพียงเพราะอีเมลตรงกัน และบัญชีที่สร้างจาก Google ไม่มีรหัสผ่าน: login ได้ด้วย Google เท่านั้น และ "ลืมรหัสผ่าน" ไม่ส่งรหัสให้บัญชีชนิดนี้`
    - ในส่วนตัวอย่าง curl แบบ cookie jar ของ flow auth (หัวข้อที่มีอยู่จากแผน 4b) เพิ่มขั้น "ลืมรหัสผ่าน": `GET /auth/csrf` → `POST /auth/password-reset/request` `{"email":…}` → อ่านรหัสจาก Mailpit `http://localhost:8025` → `POST /auth/password-reset/confirm` `{"challenge_id":…,"otp":…,"new_password":…}` ได้ 204 → login ด้วยรหัสใหม่ และระบุว่า session เดิมทุกเครื่องถูกออกจากระบบ, มีอีเมลแจ้งใน Mailpit, รหัสใช้ได้ 10 นาที ผิดได้ 5 ครั้ง
  - roadmap:
    - หัวข้อ 5: ทั้ง 4 checkbox เป็น `- [x]` ต่อท้าย ` (แผน 5)` และเพิ่มบรรทัด `แผน: [2026-10-29-milestone-5-password-reset.md](2026-10-29-milestone-5-password-reset.md) ลงมือครบ รอตรวจรับ` ใต้ "**ไฟล์หลัก:**"
    - ข้อส่งต่อของ 4b เรื่องสมัครชิงอีเมล ต่อท้าย ` — **ปิดแล้วในแผน 5**: reset ตั้งรหัสใหม่, ยืนยันอีเมล, revoke ทุก session และปิดรหัสยืนยันเก่า`
    - ข้อส่งต่อของ 4c แทนส่วน `บัญชีที่สร้างจาก Google ตั้งรหัสผ่านได้ผ่านลืมรหัสผ่านใน milestone 5` ด้วย `บัญชี Google-only ใช้ลืมรหัสผ่านไม่ได้ตาม AGENTS.md (แผน 5); การตั้งรหัสผ่านให้บัญชีเหล่านี้เป็น feature แยกที่ยังไม่อยู่ในเฟส 1`
  - spec milestone 4 หัวข้อ 10: `password reset และอีเมลแจ้งเปลี่ยนรหัสผ่าน (milestone 5)` ต่อท้าย ` — ทำแล้วใน [แผน 5](../plans/2026-10-29-milestone-5-password-reset.md)`

- [ ] **Step 4: ตรวจรับในเครื่อง**

Run:
```bash
docker compose ps
make swagger && make check
make -s test-integration
make test-mail
go build -o /dev/null ./cmd/api && docker compose config --quiet
go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
```
Expected: `make check` ผ่าน (รวม swagger ตรงกับที่ commit), integration ไม่มี SKIP, `make test-mail` ส่งถึง Mailpit; govulncheck รายงานเฉพาะสองรายการเดิม (stdlib `crypto/x509` GO-2026-6809 และ `golang.org/x/text` GO-2026-6629) — ถ้าพบรายการใหม่ให้หยุดแล้วแจ้ง ห้ามอัปเกรดเอง

ไม่ทดสอบผ่าน API ที่ผู้ใช้รันอยู่ใน task นี้: ผู้ใช้ลอง flow บน Swagger UI + Mailpit เองหลังตรวจรับ

- [ ] **Step 5: Commit**

```bash
git add cmd/api README.md docs/superpowers/plans/2026-10-05-phase-1-roadmap.md docs/superpowers/specs/2026-10-22-milestone-4-auth-design.md docs/api
git commit -m "feat(api): wire password reset and document the forgot-password flow"
```
