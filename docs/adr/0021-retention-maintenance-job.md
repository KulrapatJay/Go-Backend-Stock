# ADR-0021: Retention cleanup เป็น one-shot job ด้วย maintenance role

- **สถานะ:** Accepted
- **วันที่:** 2026-11-06
- **เกี่ยวข้อง:** [phase 1 spec](../superpowers/specs/2026-10-05-phase-1-design.md) หัวข้อ 9, [persistence design](../superpowers/specs/2026-10-17-milestone-3-persistence-design.md) หัวข้อ 3.3, [แผน milestone 9](../superpowers/plans/2026-11-06-milestone-9-backend-acceptance.md), [ADR-0004](0004-database-supabase-private-schema.md), [ADR-0017](0017-rate-limits-in-postgres.md), [ADR-0018](0018-idempotency-keys.md)

## บริบท
Sessions, OTP, OIDC flows, idempotency records, rate-limit state และ audit events โตขึ้นเรื่อยๆ และ Supabase Free จำกัดขนาด DB แต่ `audit_events` ต้อง append-only สำหรับ runtime role (AGENTS.md) จึงลบจาก API ไม่ได้ และห้ามให้ API มีสิทธิ์ลบตารางเหล่านี้เพื่อลดความเสียหายหาก API ถูกเจาะ

## ทางเลือกที่พิจารณา
1. ให้ API ลบเองเบื้องหลัง: ต้องให้ runtime role มี DELETE บน audit ซึ่งขัดกับ append-only
2. `pg_cron` หรือฟังก์ชันใน DB: ผูกกับ extension ของ Supabase และอยู่นอก goose
3. Job แยก (`cmd/maintenance`) ที่รันครั้งเดียวแล้วจบ ด้วย role `app_maintenance` ที่อ่านได้เฉพาะคอลัมน์หมดอายุและลบได้ ไม่มี INSERT/UPDATE/DDL

## การตัดสินใจ
เลือกข้อ 3
- core มี `service.Retention` กับ port `RetentionRepository`; adapter postgres ลบทีละ batch 1,000 แถวต่อคำสั่ง (เก่าที่สุดก่อน) จนได้น้อยกว่า batch; composition root อยู่ที่ `cmd/maintenance/main.go` และมี image/compose profile ของตัวเอง
- เกณฑ์ลบเฉพาะแถวที่เวลา**น้อยกว่า** cutoff: audit เก่ากว่า `AUDIT_RETENTION_DAYS` (ค่าเริ่มต้น 90, ช่วง 30–365), idempotency ที่หมดอายุ, sessions/OTP/OIDC flows ที่หมดอายุเกิน 1 วัน, rate events เก่ากว่า 1 ชั่วโมง แล้วลบ rate keys ที่หมดอายุและไม่มี event เหลือ
- job ไม่เขียน audit ของตัวเอง (role ไม่มี INSERT): รายงานจำนวนที่ลบเป็น application log `maintenance.retention_finished` หรือ `maintenance.retention_failed` พร้อม exit code ≠ 0
- rate key ที่ API เพิ่งบันทึก event ชนกับการลบ (FK `23503`) ถือเป็น "ข้ามไว้รอบหน้า" ไม่ใช่ความล้มเหลว เพราะ role ล็อกแถวล่วงหน้าไม่ได้
- เปิดด้วย `make maintenance` ในเครื่อง; production schedule ด้วย GitHub Actions วันละครั้ง + `workflow_dispatch` ทำใน milestone 11 หลังตรวจเครือข่ายและโควตาของ Supabase

## ผลที่ตามมา
- append-only ของ audit ยังจริงสำหรับแอป และการลบตาม retention เป็นข้อยกเว้นที่ทำได้ผ่าน role เดียวที่ไม่มีสิทธิ์อื่น
- ต้องมีตัวรัน job ตามกำหนดจริงก่อนเปิดใช้งานออนไลน์; จนกว่าจะตั้ง ตารางโตต่อไปแต่ไม่เสียหาย
- การลบเป็น batch ละ statement ทำให้ lock สั้นและหยุดกลางทางได้ (ยกเลิกด้วย timeout `MAINTENANCE_TIMEOUT`)
- สิทธิ์ของ `app_maintenance` ทดสอบกับ PostgreSQL จริงใน CI (ทั้งที่ทำได้และที่ทำไม่ได้)
