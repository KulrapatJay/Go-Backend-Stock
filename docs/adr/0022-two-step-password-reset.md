# ADR-0022: Password reset แบบสองขั้นด้วย reset token ที่ผูกกับ browser

- **สถานะ:** Accepted
- **วันที่:** 2026-11-09
- **เกี่ยวข้อง:** [phase 1 spec](../superpowers/specs/2026-10-05-phase-1-design.md) หัวข้อ 5 และ API, [แผน](../superpowers/plans/2026-11-09-password-reset-verify.md), [ADR-0007](0007-single-otp-challenges-table.md), [ADR-0011](0011-api-and-migration-evolution.md), [ADR-0014](0014-mail-dispatcher-and-timing-floor.md), [ADR-0017](0017-rate-limits-in-postgres.md), [ADR-0019](0019-google-oidc-explicit-linking.md), ข้อเสนอจาก frontend `next-frontend-stock/docs/backend-proposal-password-reset-verify.md`

## บริบท
`POST /auth/password-reset/confirm` รับ `{challenge_id, otp, new_password}` ในคำขอเดียว ตรวจรหัสและเปลี่ยนรหัสผ่านพร้อมกัน
ผู้ใช้จึงรู้ว่ารหัสผิดหรือหมดอายุหลังตั้งรหัสผ่านใหม่เสร็จแล้วเท่านั้น frontend ขอให้ตรวจรหัสได้ก่อน
AGENTS.md อนุญาตให้แยก verify/reset ได้ หากออก reset token อายุสั้น ใช้ครั้งเดียว และใช้ได้เฉพาะเปลี่ยนรหัสผ่าน
และ ADR-0011 กำหนดว่าใน `/api/v1` เปลี่ยนได้แบบเพิ่มเท่านั้น

## ทางเลือกที่พิจารณา
1. คง flow เดิม: endpoint น้อยกว่า ไม่มี token ใหม่ให้ดูแล แต่ UX แย่ตามที่ frontend แจ้ง
2. endpoint ตรวจรหัสอย่างเดียวที่ไม่ใช้รหัส แล้วให้ confirm ตรวจซ้ำ: เปิดช่องให้เดารหัสผ่าน verify ได้โดยไม่เสียรหัส
   และต้องนับ attempts สองที่ จึงไม่เลือก
3. verify ใช้รหัสแล้วออก reset token เก็บ token ไว้ในตารางใหม่ (`password_reset_grants`):
   แยกความหมายชัด แต่ต้องมี repository, grants, retention และการ invalidate เมื่อขอรหัสใหม่เพิ่มอีกชุด
4. verify ใช้รหัสแล้วออก reset token เก็บเป็นคอลัมน์ nullable ใน `otp_challenges` โดย challenge ยังเปิดอยู่จน confirm
5. ส่ง token เป็น HttpOnly cookie แทน body: JavaScript อ่านไม่ได้ แต่หน้า OTP ให้ JavaScript กรอกรหัสอยู่แล้ว
   XSS บนหน้านั้นจึงได้รหัสไปใช้เองได้อยู่ดี และทำให้ contract กับ curl/Swagger ซับซ้อนขึ้น

## การตัดสินใจ
เลือกข้อ 4 โดยคง body เดิมของ confirm ไว้ (ADR-0011)

- `POST /auth/password-reset/verify {challenge_id, otp}` → 200 `{reset_token, expires_in: 600}` พร้อม `Cache-Control: no-store`
  ตรวจรหัสด้วยกติกาเดียวกับ confirm: รหัสผิดนับ attempts ใน challenge เดียวกัน (รวม 5 ครั้งทั้งสอง endpoint),
  ทุกความล้มเหลวเป็น 400 `invalid_reset`, timing floor เดียวกัน และ rate limit scope `password_reset_confirm_ip` ร่วมกัน
- รหัสที่ถูกถูกใช้โดยบันทึก grant บน challenge: `verified_at`, `reset_token_hash` (SHA-256 ของ 32 bytes สุ่ม),
  `reset_binding_hash` และ `reset_token_expires_at` = verified_at + 10 นาที; CHECK บังคับว่าทั้งสี่ค่าว่างหรือมีครบ
  และมีได้เฉพาะ purpose `password_reset`; unique index บน `reset_token_hash` สร้างด้วย CONCURRENTLY ใน migration แยก
- challenge ที่มี grant ยังเปิดอยู่ (`consumed_at` NULL) จึงยังนับเป็น challenge เดียวของผู้ใช้ และการขอรหัสใหม่
  invalidate challenge นั้นพร้อม token ด้วย query เดิม; รหัสของ challenge ที่มี grant ใช้กับทั้ง verify และ confirm แบบเดิมไม่ได้
- token ผูกกับ CSRF binding ของคำขอ verify (nonce ใน cookie `csrf_nonce` หรือ session เมื่อ login อยู่)
  เก็บเป็น SHA-256 ที่มี label `password_reset`; confirm ด้วย binding อื่นถูกปฏิเสธโดยไม่เผา token
  nonce คงเดิมตลอดอายุ cookie 1 ชั่วโมงและต่ออายุทุกครั้งที่ขอ CSRF token จึงไม่เปลี่ยนกลาง flow 10 นาที
- `POST /auth/password-reset/confirm` รับ `{reset_token, new_password}` หรือ `{challenge_id, otp, new_password}` อย่างใดอย่างหนึ่ง
  ส่งทั้งสองแบบหรือไม่ส่งเลยเป็น 400 `invalid_input`; แบบ token hash รหัสผ่านก่อนค้นหา token เหมือนแบบรหัส
  ตรวจกฎอีเมลหลังยอมรับ token และไม่เผา token เมื่อรหัสผ่านไม่ผ่านกฎนั้น สำเร็จแล้วทำเหมือนเดิมทุกอย่าง
- audit: verify สำเร็จ `auth.password_reset_verified` (actor = เจ้าของบัญชี), verify ไม่ผ่านใช้ `auth.otp_verification_failed` เดิม,
  token ถูกปฏิเสธ `auth.password_reset_failed` (actor NULL) reason `reset_token_not_found`, `reset_token_expired`,
  `challenge_closed` หรือ `browser_mismatch`; สำเร็จใช้ `auth.password_reset_succeeded` และ `auth.sessions_revoked` เดิม

## ผลที่ตามมา
- frontend แสดงรหัสผิดที่หน้า OTP ได้ทันทีโดยไม่ลดความปลอดภัย: จำนวนการเดารหัสต่อ challenge และต่อ IP ยังเท่าเดิม
- token รั่วจาก log/history/referrer ใช้จาก browser อื่นไม่ได้ และใช้ได้ครั้งเดียวภายใน 10 นาที
- ยอมรับ: login/logout ระหว่าง verify กับ confirm ทำให้ binding เปลี่ยนและต้องเริ่มใหม่; การ reset หนึ่งครั้งใช้โควตา IP สองหน่วย
- ยอมรับ: token อาจอยู่เกิน `expires_at` ของรหัสได้ไม่เกิน 10 นาที ซึ่งยังอยู่ในช่วง 1 วันก่อน retention cleanup ลบแถว (ADR-0021)
- client เดิมที่ใช้ body `{challenge_id, otp, new_password}` ยังทำงาน; ถ้าจะเลิกใช้ต้องประกาศ deprecation และทำใน `/api/v2`
