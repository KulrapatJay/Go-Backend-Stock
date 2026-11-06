# ADR-0019: Google OpenID Connect ด้วย PKCE และเชื่อมบัญชีแบบ explicit

- **สถานะ:** Accepted
- **วันที่:** 2026-10-22
- **เกี่ยวข้อง:** [milestone 4 design](../superpowers/specs/2026-10-22-milestone-4-auth-design.md) หัวข้อ 6, [แผน 4c](../superpowers/plans/2026-10-27-milestone-4c-google-oidc.md), [ADR-0002](0002-server-side-sessions.md), [ADR-0006](0006-email-verification-and-registration.md)

## บริบท
ผู้ใช้ต้อง login ด้วย Google ได้ ระบบมี session ของตัวเอง (ไม่ใช้ Supabase Auth) และบัญชีด้วยอีเมล+รหัสผ่านอยู่แล้ว สองเรื่องที่ผิดแล้วเสียหายหนัก: callback ที่ถูกปลอมหรือเล่นซ้ำ และการที่คนอื่นยึดบัญชีเดิมได้เพียงเพราะมี Google account ที่อีเมลเดียวกัน

## ทางเลือกที่พิจารณา
1. Implicit flow หรือรับ ID token จาก browser โดยตรง: เสี่ยงต่อ token รั่วและ replay
2. Authorization Code ธรรมดา: ต้องพึ่ง state อย่างเดียว
3. Authorization Code + state + nonce + PKCE S256 โดย flow ผูกกับ browser ที่เริ่ม
4. เชื่อมบัญชีเดิมอัตโนมัติเมื่ออีเมลตรงกัน (สะดวกกว่า) หรือให้ผู้ใช้ยืนยันสิทธิ์บัญชีเดิมก่อน

## การตัดสินใจ
เลือกข้อ 3 และเชื่อมบัญชีแบบ explicit
- เริ่มด้วย `POST /auth/google/start` ซึ่งผ่าน CSRF และคืน `authorization_url`; state, nonce และ browser binding สุ่ม 32 bytes เก็บเฉพาะ hash ใน `oauth_flows` อายุ 10 นาที ใช้ได้ครั้งเดียว (consume ด้วย conditional UPDATE ก่อนแลก code) และ binding อยู่ใน cookie HttpOnly
- PKCE S256 โดย `code_verifier` คำนวณใหม่ตอน callback จาก HMAC(`OIDC_SECRET`, state ‖ binding) จึงไม่ต้องเก็บ verifier เพิ่ม; ยอมรับว่าหมุน `OIDC_SECRET` ระหว่าง flow ที่ค้างอยู่ทำให้ flow นั้นล้ม
- callback `GET` ยกเว้น CSRF ของแอป (Google ไม่ส่ง header ของเรา) แต่ตรวจ state/nonce/binding แทน; adapter ตรวจลายเซ็น issuer audience และ expiry แล้วส่งให้ core เฉพาะ identity ที่ตรวจแล้ว (`provider_subject` จาก `sub`) ไม่ส่ง SDK type หรือ claims ดิบ
- identity ใช้ unique (`provider`, `provider_subject`) ไม่ใช้อีเมลเป็น identity; ผู้ใช้ใหม่ต้องมี `email_verified`
- อีเมลชนบัญชีเดิม → ไม่ link อัตโนมัติ: audit `link_required` แล้วผู้ใช้ login บัญชีเดิมด้วยรหัสผ่านและเรียก `POST /auth/google/link` ใน session นั้น; link สำเร็จไม่สร้าง session ใหม่
- บัญชีที่สร้างจาก Google มี `password_hash` เป็น NULL: login ด้วยรหัสผ่านและ "ลืมรหัสผ่าน" ไม่ใช้กับบัญชีนี้ (ตอบเหมือนอีเมลที่ไม่มีบัญชี)
- ปิดทั้ง flow อย่างชัดเจน (`GET /auth/config` → `google_enabled: false`, endpoint ตอบ 503 `feature_disabled`) เมื่อ config ไม่ครบ ไม่มี login จำลอง

## ผลที่ตามมา
- callback ที่ปลอม เล่นซ้ำ หรือมาจากคนละ browser ล้มเหลว; การยึดบัญชีผ่านอีเมลตรงกันทำไม่ได้
- ผู้ใช้ที่มีบัญชีรหัสผ่านอยู่แล้วต้องทำสองขั้นเพื่อเชื่อม Google
- CI ใช้ OIDC server ปลอม ส่วน Google จริงตรวจด้วยมือบน browser (2026-10-29: link_required, link, login และสร้างบัญชีใหม่ผ่าน) และต้องตรวจซ้ำกับ redirect URI ของ production ใน milestone 11
