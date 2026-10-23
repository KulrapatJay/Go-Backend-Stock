# Milestone 4 — Auth, email verification, sessions และ Google login (design)

- **สถานะ:** design ทั้ง 4 ส่วนได้รับการยืนยันในบทสนทนา 2026-10-22; รอผู้ใช้รีวิว spec ฉบับนี้ — แผน 4a: [2026-10-22-milestone-4a-auth-building-blocks.md](../plans/2026-10-22-milestone-4a-auth-building-blocks.md) ลงมือครบ รอตรวจรับ
- **อ้างอิง:** [AGENTS.md](../../../AGENTS.md), [phase 1 spec](2026-10-05-phase-1-design.md) หัวข้อ 5, 8, 9, 10, 11 และ 13, [milestone 3 design](2026-10-17-milestone-3-persistence-design.md), [roadmap](../plans/2026-10-05-phase-1-roadmap.md) milestone 4, ADR [0002](../../adr/0002-server-side-sessions.md), [0005](../../adr/0005-password-hashing-and-policy.md), [0006](../../adr/0006-email-verification-and-registration.md), [0007](../../adr/0007-single-otp-challenges-table.md), [0013](../../adr/0013-domain-value-types.md)

phase 1 spec กำหนดรายละเอียดส่วนใหญ่ของ milestone นี้ไว้แล้ว (parameters ของ Argon2id, กติการหัสผ่าน, OTP, session cookie, CSRF, rate limits, OIDC + PKCE และ audit events) เอกสารนี้ไม่ลอกซ้ำทั้งหมด แต่ระบุการแบ่งงาน การตัดสินใจที่เพิ่มขึ้น และรูปแบบที่แน่นอนที่ phase 1 spec ไม่ได้กำหนด หากเอกสารนี้ขัดกับ phase 1 spec ให้ถามผู้ใช้ก่อน

## 1. เป้าหมาย การแบ่งงาน และเกณฑ์ผ่าน

ทำให้ผู้ใช้สมัครด้วยอีเมลและรหัสผ่าน ยืนยันอีเมลด้วย OTP login/logout ด้วย server-side session และ login หรือเชื่อมบัญชีด้วย Google ได้ผ่าน HTTP โดยทดสอบด้วย Swagger UI และ curl ได้โดยไม่ต้องมี frontend; password reset อยู่ milestone 5 แต่ OTP และ Mailer สร้างใน milestone นี้เพราะการยืนยันอีเมลต้องใช้

ผู้ใช้เลือก (2026-10-22): **spec เดียว ลงมือเป็น 3 แผนย่อย** แต่ละแผนตรวจรับแยกกัน

| แผน | ขอบเขต | ผ่านเมื่อ |
| --- | --- | --- |
| 4a | ชิ้นส่วนพื้นฐาน: password policy + blocklist, Argon2id hasher, OTP, session token, CSRF token, rate-limit keys, client IP, timing floor, Mailer (SMTP + ส่งเบื้องหลัง), config ใหม่ และ `ParseEmail` ที่เข้มขึ้น | unit tests ทั้งหมดผ่าน, Mailer ส่งถึง Mailpit ใน compose ได้จริง, `make check` ผ่าน; ยังไม่มี endpoint ใหม่ |
| 4b | `AuthService` (register, login, authenticate, me, logout) + `EmailVerificationService`, middleware session และ CSRF, endpoints, rate limits, audit, error mapping, Swagger + README | integration tests ทุก flow ผ่าน; curl ครบ flow สมัคร → อ่าน OTP จาก Mailpit → ยืนยัน → login → me → logout บน compose ได้จริง; อีเมลซ้ำไม่รั่วผ่าน response; session ที่ logout แล้วใช้ไม่ได้; login ผิดมี audit; ไม่มี secret ใน log |
| 4c | Google OIDC: start, callback, link, `GET /auth/config`, adapter `go-oidc` + Swagger + คู่มือ | tests กับ OIDC server ปลอมผ่านทั้งหมด (replay, audience/issuer/expiry/signature ผิด, nonce ไม่ตรง, link); Google ปิดอย่างชัดเจนเมื่อ config ไม่ครบ; ทดสอบกับ Google จริงเมื่อผู้ใช้สร้าง OAuth client แล้ว (ก่อนปล่อยระบบ) |

## 2. การตัดสินใจที่ยืนยันแล้ว (2026-10-22)

| เรื่อง | ทางเลือกที่ยืนยัน | เหตุผล / ข้อแลก |
| --- | --- | --- |
| การแบ่งงาน | spec เดียว + แผน 4a/4b/4c | งานใหญ่กว่า milestone 3; ตรวจรับเป็นส่วนได้ |
| Libraries ใหม่ | `golang.org/x/crypto/argon2`, `github.com/coreos/go-oidc/v3` + `golang.org/x/oauth2`, `net/smtp` ของ stdlib | ใช้ของที่ดูแลโดยทีม Go/ชุมชนหลักสำหรับ crypto และ OIDC; `net/smtp` ถูก freeze แต่รองรับ STARTTLS/AUTH พอสำหรับ Mailpit และ SMTP จริง; pin เวอร์ชันที่ออกมาแล้วอย่างน้อย 2 สัปดาห์ ตรวจ license และ `govulncheck` ตอน prototype |
| Secrets ตอน dev | ค่า `dev-only-...` ใน `.env.example` | แบบเดียวกับรหัสผ่าน DB ที่มีอยู่; restart แล้ว session/CSRF/OTP/rate limit ยังใช้ได้; production ปฏิเสธค่าเหล่านี้ |
| ส่งอีเมล | หลังตอบ ผ่าน dispatcher เบื้องหลังใน mail adapter + timing floor 500 ms + jitter 0–100 ms | เวลาตอบไม่ขึ้นกับ SMTP; ข้อแลก: API ตายระหว่างส่งแล้วอีเมลหาย ผู้ใช้ขอรหัสใหม่ได้ตาม cooldown (ไม่มี durable queue ใน phase 1 อยู่แล้ว) |
| Blocklist | กรองรายการ 100k ของ SecLists ให้เหลือเฉพาะรายการที่มีผลกับกติกาของเรา แล้ว commit พร้อม NOTICE และสคริปต์สร้างใหม่ | ผลการตรวจเท่ากับรายการเต็ม แต่ไฟล์และ binary เล็กกว่ามาก (หัวข้อ 4.2) |
| โครง core | ports ตาม phase 1 spec (`AuthService`, `EmailVerificationService`) + helper เป็น concrete struct ใน `internal/core/service` | ตรงกับ spec ที่ห้ามสร้าง interface ให้ hasher/crypto helper; milestone 5 ใช้ helper ชุดเดียวกัน |
| Timing floor | อยู่ใน service (core) โดย inject Clock และฟังก์ชัน sleep | เป็นคุณสมบัติด้านความปลอดภัยของ use case ไม่ใช่ของ HTTP; test ไม่ต้องรอจริง |

## 3. โครงสร้าง

```text
internal/core/domain/      auth_input.go (RegisterInput, LoginInput, VerifyEmailInput), principal.go (Principal,
                           RequestMeta), session_grant.go (SessionGrant, OTPReceipt, OTPMail), password.go
                           (Password + reasons), oidc.go (OIDCStart, OIDCRedirect, OIDCCallback, OIDCResult,
                           AuthorizationRequest, CodeExchange, VerifiedIdentity)
internal/core/port/        auth_service.go (AuthService, EmailVerificationService), mailer.go (Mailer),
                           identity.go (IdentityProvider)
internal/core/service/     auth.go, email_verification.go, google.go (4c)
                           password.go (Argon2id + semaphore + dummy hash), blocklist.go + blocklist.txt
                           otp.go, session.go, csrf.go, ratelimit.go, audit.go, timing.go
internal/adapter/http/     auth.go, auth_dto.go, csrf.go, google.go (4c), clientip.go
internal/adapter/http/middleware/  session.go, csrf.go
internal/adapter/mail/     smtp.go, message.go, dispatcher.go
internal/adapter/identity/ google.go (4c)
internal/config/           ค่าใหม่ในหัวข้อ 4.10
cmd/api/main.go            ประกอบทุกอย่าง (DI); shutdown รอ dispatcher ส่งอีเมลที่ค้างอยู่
scripts/                   build_blocklist.go (สร้างไฟล์ blocklist ใหม่จากต้นทางที่ pin)
```

ไฟล์และชื่อที่แน่นอนกำหนดในแผนของแต่ละส่วน; core ยังคง import เฉพาะ stdlib, decimal, uuid และ `golang.org/x/crypto/argon2` (เพิ่มใน `boundary_test.go` ตอน 4a) ส่วน `go-oidc`/`oauth2` อยู่ใน adapter เท่านั้น

## 4. ชิ้นส่วนพื้นฐาน (แผน 4a)

### 4.1 รหัสผ่าน

- `domain.ParsePassword(raw string, email domain.Email) (Password, error)` ใช้กับ register และ reset (milestone 5) ตามกติกาของ phase 1 spec หัวข้อ 10; error เป็น `invalid_input` ที่มี field `password` และรายการ reasons (`password_too_short`, `password_too_long`, `password_non_ascii`, `password_contains_space`, `password_missing_lowercase`, `password_missing_uppercase`, `password_missing_special`, `password_too_common`, `password_contains_email`) คืนได้หลาย reason พร้อมกัน
- `domain.Error` เพิ่ม field reasons แบบ optional ที่ HTTP adapter ส่งออกใน envelope เป็น `error.details` (เพิ่ม field แบบ additive ตาม ADR-0011)
- `password_contains_email`: ส่วนหน้า `@` ของอีเมลที่ยาวอย่างน้อย 4 ตัว ปรากฏในรหัสผ่านเมื่อเทียบแบบไม่สนตัวพิมพ์
- blocklist ไม่ได้อยู่ใน domain (ต้องโหลดไฟล์) จึงตรวจใน service ผ่าน `PasswordPolicy` ที่ inject blocklist; `Password` ไม่เคย serialize หรือ log (`String()` คืน `[REDACTED]`)
- login ไม่ใช้ `ParsePassword`: ตรวจเฉพาะความยาวไม่เกิน 32 ตัว ถ้าเกินตอบ `invalid_credentials` โดยไม่ hash (ไม่ขึ้นกับว่ามีบัญชีหรือไม่จึงไม่รั่วสถานะ)

### 4.2 Blocklist

- ต้นทาง: ไฟล์รายการรหัสผ่านยอดนิยมประมาณ 100,000 รายการใน SecLists (MIT) ที่ pin commit; prototype ตรวจ license ของไฟล์นั้นก่อนใช้และต้องขออนุญาตผู้ใช้ก่อนดาวน์โหลด
- `scripts/build_blocklist.go` อ่านไฟล์ต้นทาง แปลงเป็นพิมพ์เล็ก แล้วเก็บเฉพาะรายการที่ยาว 6–32 ตัว เป็น printable ASCII ไม่มีช่องว่าง มีตัวอักษรอย่างน้อย 1 ตัว และมีอักขระพิเศษอย่างน้อย 1 ตัว เรียงและตัดซ้ำ
- เหตุผลที่ผลเท่ากับรายการเต็ม: รหัสที่ผ่านกติกาต้องมีพิมพ์เล็ก พิมพ์ใหญ่ และอักขระพิเศษ เมื่อแปลงเป็นพิมพ์เล็กแล้วจึงมีตัวอักษรและอักขระพิเศษเสมอ รายการที่ไม่มีอักขระพิเศษหรือไม่มีตัวอักษรจึงไม่มีทางตรงกับรหัสที่ผ่านกติกาอื่นได้
- commit `internal/core/service/blocklist.txt` + `NOTICE` (ที่มา, commit, license, คำสั่งสร้างใหม่); โหลดเป็น set ครั้งเดียวตอนเริ่ม; test ตรวจว่ารายการตัวอย่างในไฟล์ถูกปฏิเสธและไฟล์เป็นไปตามกติกาการกรอง

### 4.3 Password hasher

- Argon2id memory 19 MiB (19456 KiB), iterations 2, parallelism 1, salt 16 bytes, key 32 bytes เก็บเป็น PHC string `$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>` (base64 ไม่มี padding)
- semaphore แบบ buffered channel ขนาด `PASSWORD_HASH_CONCURRENCY` ครอบทุกการ hash/verify รวม dummy hash; รอคิวไม่เกิน 2 วินาทีหรือจน context ถูกยกเลิก แล้วคืน 503 `server_busy` (`Retry-After: 1`) พร้อม warn log ไม่สร้าง audit; ไม่ถือ semaphore ขณะอยู่ใน DB transaction
- dummy hash สร้างครั้งเดียวตอนเริ่มระบบด้วย parameters เดียวกัน; `NeedsRehash(hash)` เทียบ parameters ใน PHC string กับค่าปัจจุบัน
- verify เทียบแบบ constant-time; hash ที่ parse ไม่ได้เป็น internal error ไม่ใช่รหัสผิด

### 4.4 OTP

- สุ่ม 6 หลักแบบสม่ำเสมอด้วย `crypto/rand` (`rand.Int` ช่วง 0–999999) แล้ว format เป็น 6 ตัวรวมศูนย์นำหน้า
- digest = HMAC-SHA-256(`OTP_SECRET`, `"otp" ‖ 0x00 ‖ purpose ‖ 0x00 ‖ challengeID ‖ 0x00 ‖ userID ‖ 0x00 ‖ otp`) เก็บใน `otp_challenges.otp_hash`; เทียบด้วย `hmac.Equal`
- input OTP ต้องเป็นตัวเลข 6 ตัวพอดี ไม่เช่นนั้นตอบ `invalid_input` ก่อนแตะ DB (ไม่นับเป็นการกรอกผิด)
- `OTPMail` (To, OTP, ChallengeID, ExpiresAt) มี `String()` ที่ไม่แสดง OTP

### 4.5 Session token

- สุ่ม 32 bytes ด้วย `crypto/rand` ส่งใน cookie เป็น base64url ไม่มี padding; เก็บ SHA-256 ของ bytes (ไม่ใช่ของ string) ใน `sessions.token_hash`
- cookie ที่ parse ไม่ได้ถือว่าไม่มี session ไม่ใช่ error 500

### 4.6 CSRF token

- รูปแบบ: base64url(`version (1 byte = 1)` ‖ `expiry unix seconds (8 bytes big-endian)` ‖ `salt (16 bytes)` ‖ `mac (32 bytes)`) โดย mac = HMAC-SHA-256(`CSRF_SECRET`, `"csrf" ‖ 0x00 ‖ binding ‖ 0x00 ‖ expiry ‖ salt`)
- binding = `session:<session ID>` เมื่อมี session ที่ใช้ได้ หรือ `pre:<nonce>` ก่อน login โดย nonce สุ่ม 32 bytes อยู่ใน cookie `csrf_nonce` (`__Host-csrf_nonce` บน HTTPS) HttpOnly, SameSite=Lax, Path=/, อายุ 1 ชั่วโมง
- อายุ token 1 ชั่วโมง; token ที่ version/ขนาด/expiry/mac/binding ไม่ถูกต้องตอบ 403 `csrf_failed` แบบเดียวกันทั้งหมด

### 4.7 Rate-limit keys

- key = HMAC-SHA-256(`RATE_LIMIT_SECRET`, `scope ‖ 0x00 ‖ value`) โดย value เป็น normalized email หรือ client IP ที่ผ่าน trusted proxy แล้ว (IP เต็ม ไม่ mask)
- rules สร้างจาก config ผ่าน `domain.NewRateLimitRule` ตอนเริ่มระบบเท่านั้น จึงไม่มี rule ที่ `Limit` เป็นศูนย์ (ปิดข้อเล็กน้อยจากรีวิว 3b)
- ลำดับ admission ของ 3b (key ของ IP ก่อน หยุดที่ key แรกที่ปฏิเสธ) ใช้ตามเดิม

### 4.8 Client IP, audit metadata และเวลา

- client IP: ใช้ remote address ของ connection; ถ้า remote address อยู่ใน `TRUSTED_PROXY_CIDRS` จึงอ่าน `X-Forwarded-For` จากขวาไปซ้ายและใช้ address แรกที่ไม่อยู่ใน trusted CIDRs; ว่าง = ไม่เชื่อ header ใดเลย
- `RequestMeta` = RequestID, ClientIP (เต็ม ใช้กับ rate limit เท่านั้น), MaskedIP (IPv4 /24, IPv6 /48), UserAgent (ตัดที่ 256 bytes และแทนอักขระควบคุม); audit metadata เก็บเฉพาะ MaskedIP, UserAgent และค่าตาม allowlist ของแต่ละ event (method, reason, scope) ไม่เก็บอีเมลดิบ
- ทุกเวลาที่ service ส่งเข้า repository ตัดที่ microsecond (`Truncate(time.Microsecond)`) เพื่อให้ผลตรวจหมดอายุใน Go กับใน PostgreSQL ตรงกัน (ปิดข้อเล็กน้อยจากรีวิว 3b)

### 4.9 Timing floor และ Mailer

- `service.Pacer` ใช้กับ register, ขอรหัสยืนยันใหม่ และ password reset (milestone 5): จับเวลาตั้งแต่ service เริ่ม แล้วรอจนครบ floor + jitter สุ่ม 0..jitter (ค่าเริ่มต้น 500 ms + 100 ms) ทั้งกรณีสำเร็จและ error ที่ต้องไม่เปิดเผยสถานะ; ไม่รอเกิน context deadline
- `port.Mailer` ตาม phase 1 spec หัวข้อ 8 (`SendEmailVerification`, `SendRegistrationAttempt`; `SendPasswordReset`, `SendPasswordChanged` เพิ่มใน milestone 5)
- `adapter/mail`:
  - `SMTPMailer` สร้างข้อความ plain text UTF-8 เอง (From, To, Subject, Date, Message-ID, MIME-Version, Content-Type, Content-Transfer-Encoding: 8bit) และส่งด้วย `net/smtp` ตาม `SMTP_TLS` (`none`, `starttls`, `tls`) พร้อม AUTH PLAIN เมื่อมี username
  - ปฏิเสธที่อยู่หรือ header ที่มี CR/LF หรือ `,;<>"` เป็น internal error (กัน header injection; ไม่ควรเกิดเพราะ `ParseEmail` ปฏิเสธแล้ว)
  - `Dispatcher` implement `port.Mailer` โดยใส่งานลงคิวขนาดจำกัด (100) แล้วคืนทันที; worker ส่งด้วย `MAIL_SEND_TIMEOUT`; คิวเต็มหรือส่งไม่สำเร็จเขียน application error log ที่ไม่มี OTP, ที่อยู่อีเมลเต็ม หรือ secret; shutdown หยุดรับงานแล้วรอส่งที่ค้างจนถึง shutdown timeout
- `ParseEmail` ปฏิเสธ `"(),:;<>[\]` เพิ่มเป็น `invalid_input` (ปิดข้อส่งต่อจาก milestone 2 เรื่องอีเมลที่มี `,;<>`); อีเมลที่บันทึกไปแล้วใน DB ไม่มีอักขระเหล่านี้เพราะยังไม่มี endpoint สมัคร

### 4.10 Config ใหม่

| กลุ่ม | ตัวแปร | ค่าเริ่มต้นและกติกา |
| --- | --- | --- |
| secrets | `CSRF_SECRET`, `OTP_SECRET`, `RATE_LIMIT_SECRET`, `OIDC_SECRET` | dev ใช้ค่า `dev-only-...` ใน `.env.example`; production ต้องยาวอย่างน้อย 32 bytes, ไม่ขึ้นต้นด้วย `dev-only-` และสี่ค่าต้องไม่ซ้ำกัน |
| session | `SESSION_TTL` | 168h (ช่วง 1h–720h); cookie เป็น `__Host-session` + Secure เมื่อ `PUBLIC_ORIGIN` เป็น https; production ต้องเป็น https |
| hashing | `PASSWORD_HASH_CONCURRENCY` | 4 (ช่วง 1–64) |
| timing | `AUTH_RESPONSE_FLOOR`, `AUTH_RESPONSE_JITTER` | 500ms (0–5s), 100ms (0–1s) |
| อีเมล | `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM`, `SMTP_TLS`, `MAIL_SEND_TIMEOUT` | dev: `mailpit`, `1025`, ว่าง, ว่าง, `no-reply@localhost`, `none`, 10s; production ห้าม `SMTP_TLS=none` และต้องมี `SMTP_FROM` |
| client IP | `TRUSTED_PROXY_CIDRS` | ว่าง; รายการ CIDR คั่นด้วย comma |
| rate limits | `OTP_MAX_ATTEMPTS`, `OTP_RESEND_COOLDOWN`, `OTP_REQUESTS_PER_HOUR_EMAIL`, `OTP_REQUESTS_PER_HOUR_IP`, `LOGIN_ATTEMPTS_PER_EMAIL`, `LOGIN_ATTEMPTS_PER_IP` | 5 (1–20 ตาม CHECK ของ DB), 60s, 5, 20, 10/15 นาที, 30/15 นาที; ค่าอื่นของ phase 1 spec หัวข้อ 10 เป็นค่าคงที่ |
| Google (4c) | `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URI` | ครบทั้ง 3 หรือว่างทั้ง 3; ตั้งไม่ครบ API ไม่เริ่มและบอกว่าขาดตัวไหน |

ค่า secret และรหัส SMTP ไม่ถูก log และ `Config` มี `String()`/`LogValue()` ที่ปิดค่าเหล่านี้

## 5. Flow หลัก (แผน 4b)

ทุก flow: parse input → rate limit → งาน CPU (hash) นอก transaction → transaction เดียวที่มีทั้งข้อมูลและ audit → ตอบ → ส่งอีเมลผ่าน dispatcher; rate limit ที่ปฏิเสธตอบ 429 `rate_limited` + `Retry-After` (วินาที ปัดขึ้น) และบันทึก `security.rate_limited` (actor = ผู้ใช้ถ้า login อยู่ ไม่เช่นนั้น NULL, metadata มี scope) ใน transaction แยกเฉพาะเมื่อ `RecordDenialAudit`

| Endpoint (ใต้ `/api/v1`) | Auth | Rate limit | ผล |
| --- | --- | --- | --- |
| `GET /auth/csrf` | optional | — | 200 `{csrf_token, expires_at}` + cookie `csrf_nonce` เมื่อยังไม่ login |
| `POST /auth/register` | CSRF | `signup_ip` 20/ชม. + `email_verification_request_email` (cooldown + 5/ชม.) | 202 `{message, challenge_id, expires_in: 600}` แบบเดียวกันทุกกรณี + timing floor |
| `POST /auth/email-verification/request` | CSRF | `email_verification_request_ip` 20/ชม. + `..._request_email` | 202 แบบเดียวกันทุกกรณี + timing floor |
| `POST /auth/email-verification/confirm` | CSRF | `email_verification_confirm_ip` 20/15 นาที | 204; ผิด/หมดอายุ/ใช้แล้ว/ผิดครบ → 400 `invalid_verification` |
| `POST /auth/login` | CSRF | `login_ip` 30/15 นาที + `login_email` 10/15 นาที | 200 `{user, expires_at}` + session cookie; 401 `invalid_credentials`; 403 `email_not_verified` |
| `GET /auth/me` | required | — | 200 User |
| `POST /auth/logout` | optional + CSRF | — | 204 + ล้าง cookie |

- **Register:** อีเมลใหม่ → insert user (ยังไม่ยืนยัน) + challenge + audit `auth.registered` และ `auth.email_verification_requested` → ส่ง OTP; อีเมลที่มีบัญชีแล้วทุกสถานะ (`Users().Insert` คืน `ErrIdentityConflict`) → ไม่แก้อะไร ไม่สร้าง challenge ไม่มี audit → ส่ง `SendRegistrationAttempt`; ทั้งสองกรณี hash รหัสผ่านก่อนเข้า transaction และตอบ challenge ID (สุ่มเมื่ออีเมลซ้ำ)
- **ขอรหัสใหม่:** เฉพาะบัญชี local ที่ยังไม่ยืนยัน: lock user → `InvalidateOpen` → insert challenge → audit → ส่ง OTP; กรณีอื่นไม่เขียนและไม่ส่ง
- **ยืนยัน:** `ByID` (purpose email_verification) → lock user → lock challenge → ตรวจเปิดอยู่/ไม่หมดอายุ/ผิดน้อยกว่า `OTP_MAX_ATTEMPTS` → เทียบ HMAC; ผิดให้ `RecordFailure` + audit `auth.email_verification_failed` (actor NULL, reason `otp_mismatch`) แล้ว **commit** ก่อนคืน error; challenge ไม่มี/หมดอายุ/ปิดแล้ว/ผิดครบ → audit failed ด้วย reason นั้น; ถูก → `Consume` + `MarkEmailVerified` + audit `auth.email_verified`; ไม่สร้าง session
- **Login:** `CredentialsByEmail` นอก transaction; ไม่มีบัญชีหรือ Google-only → verify dummy hash → audit `auth.login_failed` (method password, actor NULL, reason `invalid_credentials`) → 401; รหัสผิด → audit → 401 (body เดียวกันทุกไบต์); รหัสถูกแต่ยังไม่ยืนยัน → audit reason `email_not_verified` → 403; ถูกและยืนยันแล้ว → (ถ้า `NeedsRehash` hash ใหม่ก่อน) transaction: lock user → อ่าน credentials ใหม่ใต้ lock ถ้า hash เปลี่ยน → 401 → `SetPassword` เมื่อ rehash → insert session → audit `auth.login_succeeded` (method password, session ID) → commit; audit ของ login ที่ล้มเหลวเขียนใน transaction ของตัวเอง ถ้าเขียนไม่ได้ log error แล้วตอบ 500
- **Session middleware:** อ่าน cookie → SHA-256 → `ByTokenHash` → หมดอายุหรือถูก revoke ถือว่าไม่ได้ login; DB ใช้ไม่ได้ตอบ 503; เก็บ `Principal` ที่ copy แล้วใน `fiber.Ctx` locals เฉพาะช่วง request
- **Me:** `Users().ByID(principal.UserID)` → User `{id, email, email_verified, created_at}`
- **Logout:** ถ้ามี session ที่หาได้ → `Revoke(reason logout)`; ได้ `true` จึง audit `auth.logout`; ล้าง session cookie ทุกครั้ง
- **CSRF middleware:** ทุก method ที่ไม่ใช่ GET/HEAD/OPTIONS ต้องมี `X-CSRF-Token` ที่ตรงกับ binding ปัจจุบัน และ Origin ตรงกับ `PUBLIC_ORIGIN` แบบ exact (ไม่มี Origin ใช้ origin ของ Referer; ไม่มีทั้งคู่ → 403); ลำดับ middleware ตาม phase 1 spec หัวข้อ 5
- **Error mapping ที่เพิ่ม:** `invalid_credentials` 401, `unauthenticated` 401, `invalid_verification` 400, `email_not_verified` 403, `csrf_failed` 403, `rate_limited` 429 + `Retry-After`, `server_busy` 503 + `Retry-After: 1`, `feature_disabled` 503, `dependency_unavailable` 503

## 6. Google OIDC (แผน 4c)

ตาม phase 1 spec หัวข้อ 10 โดยมีรายละเอียดเพิ่ม:

- `GET /auth/config` → 200 `{google_enabled}`; เมื่อปิด endpoints ของ Google ตอบ 503 `feature_disabled`
- adapter สร้าง provider (discovery + JWKS ของ `https://accounts.google.com`) ครั้งแรกที่ใช้และ cache ไว้; ล้มเหลว → 503 `dependency_unavailable` โดย API ยังเริ่มได้
- `POST /auth/google/start` (CSRF, `google_start_ip` 20/ชม.) และ `POST /auth/google/link` (login + CSRF): สุ่ม state, nonce, browser binding อย่างละ 32 bytes → insert `oauth_flows` (hash, อายุ 10 นาที, purpose `login`/`link`, user ID เมื่อ link) → `code_challenge = S256(base64url(HMAC(OIDC_SECRET, "pkce" ‖ state ‖ binding)))` → 200 `{authorization_url}` (scope `openid email`) + cookie `oidc_binding` (`__Host-oidc_binding` บน HTTPS) HttpOnly, SameSite=Lax, Path=/, อายุ 10 นาที
- `GET /auth/google/callback?code&state` (ยกเว้น CSRF ของแอป): `Consume(stateHash, bindingHash)` → คำนวณ verifier ใหม่ → `Exchange` นอก transaction (timeout) → adapter ตรวจลายเซ็น/issuer/audience/expiry → service ตรวจ nonce กับ hash ใน flow
  - login + `BySubject` เจอ → transaction: lock user → insert session → audit `auth.login_succeeded` (method google)
  - login + ไม่เจอ: ต้อง `email_verified`; อีเมลมีบัญชีแล้ว → ไม่ link อัตโนมัติ → audit `auth.login_failed` (method google, reason `link_required`); อีเมลใหม่ → transaction: insert user (ยืนยันแล้ว, ไม่มีรหัสผ่าน) + identity + session + audit `auth.registered` (method google) และ `auth.login_succeeded`
  - link: session ปัจจุบันต้องเป็น user ของ flow → `Identities().Insert`; ชน → audit `auth.identity_link_failed` ใน transaction เดิม; สำเร็จ → audit `auth.identity_linked`; ไม่สร้าง session ใหม่
  - Google ตอบ `error` (เช่นผู้ใช้ยกเลิก) หรือ flow/nonce ไม่ผ่าน → audit `auth.login_failed` (method google, reason)
  - ทุกกรณีลบ cookie binding แล้ว 303 ไป `/docs/?google=success|linked|link_required|failed` (ไม่มี token หรือข้อมูลใน URL; เปลี่ยนปลายทางผ่าน config เมื่อมี frontend)

## 7. การทดสอบ

- **Unit (core, ไม่ใช้ DB):** password policy ทุก reason และหลาย reason พร้อมกัน, blocklist, hasher (semaphore เต็ม → `server_busy`, dummy hash ถูกเรียก, `NeedsRehash`), OTP (ศูนย์นำหน้า, ช่วงค่า, HMAC ผูก purpose/challenge/user), session token, CSRF token (แก้ไข/หมดอายุ/binding ผิด/version ผิด), rate-limit key, client IP + trusted proxy, mask IP/user agent, Pacer ด้วย Clock และ sleep ปลอม, config validation ทุกกติกา
- **Adapter:** Mailer ส่งไป SMTP server ปลอมใน test (ตรวจ headers, body, header injection, timeout, คิวเต็ม, shutdown รอ); Google adapter กับ OIDC server ปลอมใน `httptest` (discovery, JWKS ด้วย RSA key ที่สร้างใน test, token endpoint ที่ตรวจ `code_verifier`) ทั้ง token ถูก, audience/issuer ผิด, หมดอายุ, ลายเซ็นผิด และ verifier ผิด
- **Integration ของ flow (`internal/adapter/http` + PostgreSQL จริงผ่าน `make test-integration`):** เรียกผ่าน HTTP จริงรวม cookie, CSRF และ Origin โดยใช้ Mailer, Clock และ IdentityProvider ปลอม
  - status/body ของกรณีที่ห้ามเปิดเผยสถานะต้องเหมือนกันทุกไบต์ (register ใหม่/ซ้ำ, login ไม่มีบัญชี/รหัสผิด/Google-only, ขอรหัสใหม่ทุกกรณี)
  - แถว audit ถูกต้อง (event, actor, outcome, reason) รวม login/OTP ที่ผิดยังมี audit หลังได้ error
  - rollback เมื่อเขียน audit ไม่ได้ (failure injection), logout ซ้ำไม่ audit ซ้ำ, session หมดอายุ/ถูก revoke เข้า `/auth/me` ไม่ได้
  - ยืนยันอีเมลผิดพร้อมกันหลายคำขอนับครบทุกครั้ง (พิสูจน์ `FOR UPDATE` ของ `OTPChallengeRepository.Lock` ที่ค้างจากรีวิว 3b)
  - log ที่จับระหว่าง test ไม่มีรหัสผ่าน, OTP, token, cookie หรือ secret
- **ด้วยมือบน compose:** สคริปต์ curl ใน README ครบ flow ผ่าน Mailpit (ใช้ API ของ Mailpit อ่าน OTP); ลอง Swagger UI ใน browser จริงว่า cookie + CSRF ใช้ได้

## 8. Swagger และเอกสาร

- annotations ของทุก endpoint ใน 4b/4c มี operationId, summary/description, request/response schema, status codes และตัวอย่างกรณีสำเร็จ/ผิดพลาด; `X-CSRF-Token` เป็น header parameter; อธิบาย cookie ใน description เพราะ Swagger 2.0 ไม่มี `in: cookie`; ไม่เพิ่ม Bearer JWT
- contract tests ตรวจ status codes ของ endpoints ที่เปิดแล้วกับ spec ที่ generate
- README: ตัวอย่าง curl แบบ cookie jar ครบ flow, การอ่าน OTP จาก Mailpit, ขั้นตอนตั้งค่า SMTP จริงและ Google OAuth client (redirect URI ของ dev `http://localhost:8080/api/v1/auth/google/callback`) และขั้นตอนลอง Google บน browser
- ADR ใหม่: **ADR-0014** การส่งอีเมลหลังตอบผ่าน dispatcher + timing floor ใน core; **ADR-0015** blocklist ที่กรองตามกติกา (ส่วนเสริมของ ADR-0005 ไม่ได้แทนที่)

## 9. ข้อส่งต่อที่ milestone นี้ปิด

| ข้อ | ปิดที่ |
| --- | --- |
| อีเมลที่มี `,;<>` (milestone 2) | 4a หัวข้อ 4.9 |
| เวลาใน Go (ns) กับ PostgreSQL (µs) ต่างกันในช่วง 1 µs (รีวิว 3b) | 4a/4b หัวข้อ 4.8 |
| `Admit` ที่ได้ rule `Limit: 0` (รีวิว 3b) | 4a หัวข้อ 4.7 (rules มาจาก config ผ่าน `NewRateLimitRule` เท่านั้น) |
| test ของ `FOR UPDATE` ใน `OTPChallengeRepository.Lock` (รีวิว 3b) | 4b หัวข้อ 7 |
| `Lock`/`RecordFailure` กับ purpose อื่น (รีวิว 3b) | 4b: confirm ด้วย challenge ID ของ password reset ต้องได้ `invalid_verification` |

ข้อที่ยังไม่ปิดและคงไว้ใน roadmap: rollback ใน `withinTx` ไม่มี timeout และ request ที่ client ยกเลิกถูกจัดเป็น `internal`

## 10. นอกขอบเขต

- password reset และอีเมลแจ้งเปลี่ยนรหัสผ่าน (milestone 5)
- endpoints ของพอร์ตและรายการซื้อขาย (milestone 6–7)
- เชื่อม Google จริงใน CI; ทดสอบกับ Google จริงทำด้วยมือเมื่อผู้ใช้มี credentials
- frontend และการเปลี่ยนปลายทาง redirect หลัง Google (เมื่อเริ่ม Next.js)
- durable email queue, การ unlink Google และการตั้งรหัสผ่านให้บัญชี Google-only
