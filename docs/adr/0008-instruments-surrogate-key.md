# ADR-0008: ตาราง `instruments` ด้วย surrogate key และตรวจ symbol กับ provider

- **สถานะ:** Accepted
- **วันที่:** 2026-10-14
- **เกี่ยวข้อง:** spec หัวข้อ 7–9 และ 12, ADR-0003

## บริบท
ผู้ใช้ต้องการให้บันทึกได้เฉพาะหุ้นที่มีอยู่จริงเพื่อกันพิมพ์ผิด ในอนาคต symbol อาจเปลี่ยน (FB → META) หรือ ticker ถูกนำกลับมาใช้กับบริษัทใหม่ และหน้าหุ้นรายตัวต้องใช้ข้อมูลบริษัท

## ทางเลือกที่พิจารณา
1. ตรวจเฉพาะรูปแบบ symbol และเก็บ symbol ใน transactions
2. ตาราง instruments โดยใช้ symbol เป็น primary key
3. ตาราง instruments โดยใช้ `id` uuid เป็น key และ transactions อ้าง `instrument_id`

## การตัดสินใจ
ใช้ทางเลือก 3 ตรวจ instruments ก่อน ถ้ายังไม่มีให้เรียก InstrumentProvider (Alpaca Assets API หรือ demo) นอก portfolio lock ไม่พบคืน 422 `unknown_symbol`, provider ล่มคืน 503 ไม่ fallback ไปข้อมูลเดโม เก็บ `provider_asset_id` ถาวรของ provider API ยังรับและตอบเป็น symbol จำกัดการเรียก provider ด้วย rate limit `instrument_lookup_user`

## ผลที่ตามมา
- กันพิมพ์ผิดและได้ชื่อบริษัท การเปลี่ยน symbol ในอนาคตแก้แถวเดียว และ price bars/corporate actions อ้าง instrument_id ได้
- query รายการต้อง JOIN instruments และ symbol ใหม่บันทึกไม่ได้ช่วง provider ล่ม
- เฟส 1 กำหนด symbol UNIQUE ทั้งตาราง การรองรับ ticker ที่ถูกนำกลับมาใช้ต้องผ่อน constraint พร้อม ADR ใหม่
- ต้องตรวจสิทธิ์ Assets API ด้วยบัญชีจริงใน milestone 8
