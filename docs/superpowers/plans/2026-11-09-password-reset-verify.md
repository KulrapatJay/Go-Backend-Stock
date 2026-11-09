# Password reset แบบสองขั้น (verify → confirm ด้วย reset token)

**ที่มา:** ข้อเสนอจาก frontend `next-frontend-stock/docs/backend-proposal-password-reset-verify.md`
ผู้ใช้ยืนยันแนวทางของ backend เมื่อ 2026-11-09 (verify ใช้รหัสแล้วออก token, อายุ 10 นาที, ผูก `csrf_nonce`, confirm รับ body เดิมต่อ)

**การตัดสินใจ:** [ADR-0022](../../adr/0022-two-step-password-reset.md)

**สถานะ:** ลงมือครบ รอตรวจรับ

## งาน

- [x] migration `00006` เพิ่ม `verified_at`, `reset_token_hash`, `reset_binding_hash`, `reset_token_expires_at` ใน `otp_challenges`
      พร้อม CHECK; migration `00007` (NO TRANSACTION) สร้าง unique index บน `reset_token_hash` แบบ CONCURRENTLY
- [x] sqlc: SELECT รวมคอลัมน์ใหม่, `GrantPasswordReset`, `GetOTPChallengeByResetToken`, `ConsumePasswordResetGrant`
      และ `ConsumeOTPChallenge` ปฏิเสธ challenge ที่มี grant
- [x] domain `ResetGrant`, `VerifyResetInput`, `ResetGrantReceipt`, `ResetWithTokenInput` และ event types ใหม่ 2 ชนิด
- [x] ports: `PasswordResetService.Verify/ResetWithToken`, `OTPChallengeRepository.GrantReset/ByResetTokenHash/ConsumeResetGrant`
- [x] service: แยก `checkResetCode`, `finishReset`, `resetOutcome` ให้ Reset, Verify และ ResetWithToken ใช้ร่วมกัน
- [x] HTTP: `POST /api/v1/auth/password-reset/verify`, confirm รับสอง body, `middleware.CSRFBinding` ที่ export ให้ handler ใช้ binding เดียวกับ CSRF
- [x] Swagger annotations + generated docs, contract tests, `docs/frontend-handoff.md` หัวข้อ 4 และ 7
- [x] spec, `schema.dbml`, roadmap, AGENTS.md (ตาราง audit events)

## Tests

- HTTP + PostgreSQL จริง (`internal/adapter/http/password_reset_verify_test.go`): สองขั้นสำเร็จครั้งเดียวพร้อม audit/revoke/notice,
  รหัสที่ verify แล้วใช้ซ้ำไม่ได้ทั้งสองทาง, attempts นับรวม verify + confirm, รหัสหมดอายุ/ไม่มี/ของ flow อื่น,
  token อายุ 10 นาทีนับจาก verify (อยู่ได้หลังรหัสหมดอายุ), token ใช้ซ้ำ/หมดอายุ, ขอรหัสใหม่แล้ว token เก่าใช้ไม่ได้,
  token จาก browser อื่น, body ผสม/ว่าง/token ผิดรูป, กฎอีเมลไม่เผา token, rate limit ร่วม, verify/confirm พร้อมกันสำเร็จครั้งเดียว
- Repository (`internal/adapter/postgres/reset_grant_test.go`): grant ครั้งเดียว, เฉพาะ challenge ที่เปิด/ไม่หมดอายุ/purpose ถูก,
  consume ด้วย token เท่านั้นและครั้งเดียว, InvalidateOpen ปิด grant, CHECK และ unique index
- Service unit (`internal/core/service/password_reset_test.go`): timing floor ทุกเส้นทาง, grant เก็บ hash ของ token/binding,
  hash รหัสผ่านก่อน transaction และก่อนค้นหา token
- tests เดิมของ confirm แบบ `{challenge_id, otp, new_password}` ผ่านทั้งหมดโดยไม่แก้

## ยังไม่ได้ทำ

- `scripts/e2e/flow.sh` ยังทดสอบ reset แบบขั้นเดียว
- CI บน GitHub ยังไม่ได้รันจนกว่าจะ push
