# ADR-0002: Server-side session ด้วย opaque token ใน HttpOnly cookie

- **สถานะ:** Accepted
- **วันที่:** 2026-10-05
- **เกี่ยวข้อง:** AGENTS.md หัวข้อ Auth/Session, spec หัวข้อ 10

## บริบท
ต้อง logout และ revoke ทุก session ได้ทันทีเมื่อ reset password และไม่ให้ JavaScript อ่าน credential ได้

## ทางเลือกที่พิจารณา
1. JWT อายุสั้น + refresh token: ไม่ต้องอ่าน store ทุกคำขอ แต่ revoke ก่อนหมดอายุซับซ้อน
2. Opaque token สุ่ม 32 bytes เก็บ SHA-256 hash ใน PostgreSQL

## การตัดสินใจ
ใช้ opaque token ใน cookie `__Host-session` (HttpOnly, Secure, SameSite=Lax) อายุ absolute 7 วัน ตรวจ expiry/revocation ทุกคำขอ ป้องกัน CSRF ด้วย signed token + Origin check ไม่เพิ่ม Bearer JWT เพื่อความสะดวกของ Swagger

## ผลที่ตามมา
- revoke ได้ทันทีและไม่มี token ใน JavaScript
- ทุกคำขอที่ต้อง login อ่าน DB หนึ่งครั้ง และต้องดูแล CSRF เอง
- Swagger 2.0 อธิบาย cookie auth ได้จำกัด จึงต้องมีตัวอย่าง curl แบบ cookie jar
