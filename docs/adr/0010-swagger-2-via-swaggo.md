# ADR-0010: เอกสาร API เป็น Swagger 2.0 generate ด้วย swaggo/swag

- **สถานะ:** Accepted
- **วันที่:** 2026-10-14
- **เกี่ยวข้อง:** AGENTS.md หัวข้อ OpenAPI, spec หัวข้อ 4

## บริบท
ผู้ใช้เลือกเขียนเอกสารด้วย annotations ใน Go และต้องตรวจเอกสารให้ตรง API ใน CI

## ทางเลือกที่พิจารณา
1. swaggo/swag v1 (stable) ซึ่งออก Swagger 2.0
2. Generator สาย OpenAPI 3 รุ่น RC ที่รองรับ cookie security ได้ดีกว่า
3. เขียน OpenAPI YAML ด้วยมือ

## การตัดสินใจ
ใช้ swaggo/swag v1.16.6 ออก Swagger 2.0 เสิร์ฟ JSON/YAML และ Swagger UI ที่ bundle เองผ่าน Fiber v3 โดยไม่ใช้ wrapper ที่อาจผูก Fiber คนละ major CI generate ซ้ำ ตรวจ diff และ validate ด้วย go-swagger

## ผลที่ตามมา
- เอกสารอยู่ติดโค้ดและเครื่องมือเสถียร
- Swagger 2.0 ไม่มี `in: cookie` จึงต้องอธิบาย session/CSRF ใน descriptions และมีตัวอย่าง curl
- ย้ายไป OpenAPI 3 ได้ภายหลังเมื่อเครื่องมือเสถียร โดยต้องมี ADR ใหม่
