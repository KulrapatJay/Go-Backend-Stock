# Architecture Decision Records

บันทึกเหตุผลของการตัดสินใจสำคัญที่มี trade-off หนึ่งไฟล์ต่อหนึ่งเรื่อง

- เขียน ADR ใหม่เมื่อตัดสินใจเรื่อง architecture, schema keys, security หรือ API contract
- ห้ามแก้เนื้อหา ADR ที่ Accepted แล้ว ถ้าเปลี่ยนการตัดสินใจให้เขียนฉบับใหม่และเปลี่ยนสถานะฉบับเดิมเป็น Superseded
- เริ่มจาก [0000-template.md](0000-template.md) และเพิ่มแถวในตารางนี้

| ADR | เรื่อง | สถานะ |
| --- | --- | --- |
| [0001](0001-hexagonal-architecture.md) | Hexagonal (Ports & Adapters) + DI ที่ composition root | Accepted |
| [0002](0002-server-side-sessions.md) | Server-side session ด้วย opaque token ใน HttpOnly cookie | Accepted |
| [0003](0003-decimal-and-event-replay.md) | Decimal, transactions เป็นแหล่งความจริง และ replay แบบ position events | Accepted |
| [0004](0004-database-supabase-private-schema.md) | Supabase เฉพาะ Database, private schema `app` และแยก DB roles | Accepted |
| [0005](0005-password-hashing-and-policy.md) | Argon2id + จำกัดการ hash พร้อมกัน และกติการหัสผ่าน | Accepted |
| [0006](0006-email-verification-and-registration.md) | ยืนยันอีเมลด้วย OTP และตอบ register แบบเดียวกันทุกกรณี | Accepted |
| [0007](0007-single-otp-challenges-table.md) | รวม OTP ของทุก flow ไว้ในตาราง `otp_challenges` | Accepted |
| [0008](0008-instruments-surrogate-key.md) | ตาราง `instruments` ด้วย surrogate key และตรวจ symbol กับ provider | Accepted |
| [0009](0009-concurrency-row-lock-and-optimistic-locking.md) | Portfolio row lock + optimistic locking ด้วย version/ETag | Accepted |
| [0010](0010-swagger-2-via-swaggo.md) | เอกสาร API เป็น Swagger 2.0 generate ด้วย swaggo/swag | Accepted |
| [0011](0011-api-and-migration-evolution.md) | กติกาการเปลี่ยน API และ database migration | Accepted |
| [0012](0012-defer-stock-page-and-charts.md) | เลื่อนหน้าหุ้นรายตัวและกราฟไปเฟสถัดไป | Accepted |
| [0013](0013-domain-value-types.md) | Domain value types แบบ parse, don't validate และชนิดตัวเลขแยกตามความหมาย | Accepted |
