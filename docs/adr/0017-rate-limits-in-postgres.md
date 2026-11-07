# ADR-0017: เก็บ rate limit ใน PostgreSQL แบบ rolling window

- **สถานะ:** Accepted
- **วันที่:** 2026-10-14
- **เกี่ยวข้อง:** [phase 1 spec](../superpowers/specs/2026-10-05-phase-1-design.md) หัวข้อ 10, [persistence design](../superpowers/specs/2026-10-17-milestone-3-persistence-design.md), [ADR-0007](0007-single-otp-challenges-table.md), [ADR-0021](0021-retention-maintenance-job.md)

## บริบท
Login, สมัคร, ขอรหัส OTP, ยืนยัน/รีเซ็ตรหัส, เริ่ม Google login และการตรวจ symbol ใหม่กับ provider ต้องถูกจำกัดความถี่ต่อบัญชี/อีเมล/IP/ผู้ใช้ โควตาต้องไม่หายเมื่อ API restart (ไม่เช่นนั้นผู้โจมตีทำให้ restart แล้วกรอกรหัสต่อได้) ต้องทำงานถูกต้องเมื่อคำขอมาพร้อมกัน และ AGENTS.md ห้ามเพิ่ม Redis หรือบริการเสียเงินในเฟสแรก

## ทางเลือกที่พิจารณา
1. ตัวนับในหน่วยความจำของ process: ง่ายที่สุด แต่หายเมื่อ restart และแยกกันต่อ instance
2. Redis: เร็วและมี TTL ในตัว แต่ต้องมีบริการเพิ่มซึ่งขัดกับข้อกำหนดเฟสแรก
3. ตัวนับแบบ fixed window ใน PostgreSQL: อ่านง่าย แต่ปล่อยให้ยิงได้สองเท่าของโควตาคร่อมขอบ window
4. บันทึก event ต่อ key ใน PostgreSQL แล้วนับแบบ rolling window

## การตัดสินใจ
เลือกข้อ 4
- `rate_limit_keys` เก็บ key (HMAC-SHA-256 ของ scope + อีเมลที่ normalize แล้ว หรือ IP ที่เชื่อถือได้ ด้วย `RATE_LIMIT_SECRET`) จึงไม่เก็บอีเมลหรือ IP ดิบ; `rate_limit_events` เก็บเวลาของแต่ละครั้งที่ได้รับอนุญาต
- การอนุญาตหนึ่งครั้งอยู่ใน DB transaction เดียว: upsert ทุก key ตามลำดับที่กำหนด (key ของ IP ก่อน แล้วเรียงตาม hash) ซึ่งล็อกแถวของ key จึง serialize คำขอพร้อมกันและไม่เกิด deadlock; key แรกที่ปฏิเสธหยุดการตรวจและไม่บันทึกอะไร จึงไม่สร้าง key ใหม่ของอีเมลสุ่มเมื่อ IP เกินโควตาแล้ว
- เป็น admission limit: คำขอที่ถูกปฏิเสธไม่นับเป็น event ผู้โจมตีได้เท่าโควตาและผู้ใช้จริงที่พิมพ์ผิดไม่ถูกล็อกยาวขึ้น; รหัสผ่านผิดไม่คืนโควตา
- ปฏิเสธด้วย 429 `rate_limited` พร้อม `Retry-After`; การปฏิเสธครั้งแรกของ key ในรอบ 1 นาทีเท่านั้นที่เขียน audit `security.rate_limited` (ป้องกัน audit flood)
- ลบ events/keys ที่หมดอายุด้วย `cmd/maintenance` (ADR-0021)

## ผลที่ตามมา
- โควตาทนต่อ restart และถูกต้องภายใต้ concurrency โดยไม่มีบริการเพิ่ม และอธิบายเวลารอ (`Retry-After`) ได้ตรงจาก event ที่เก่าที่สุดที่ต้องหลุด window
- ทุกคำขอที่ผ่านด่านนี้เขียน DB หนึ่งครั้ง และ key ที่ร้อน (เช่น IP ของ proxy) เป็นจุดที่คำขอต่อแถวกัน; ยอมรับได้ที่ปริมาณของโปรเจกต์นี้ ถ้าโตกว่านี้ให้ย้ายไป store ที่เร็วกว่าโดยเปลี่ยนเฉพาะ `RateLimitRepository`
- ต้องตั้ง `TRUSTED_PROXY_CIDRS` ให้ถูก มิฉะนั้นหลัง reverse proxy ทุกคำขอมี IP เดียวกันและใช้โควตา IP ร่วมกัน (เห็นได้ใน e2e: `make e2e` ใช้ 6 จาก 30 login ต่อ 15 นาทีของ IP)
- เพดานต่างๆ ที่ตั้งค่าได้อยู่ใน config และต้องไม่เกินที่ CHECK ของ DB รองรับ
