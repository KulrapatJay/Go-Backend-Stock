# ADR-0007: รวม OTP ของทุก flow ไว้ในตาราง `otp_challenges`

- **สถานะ:** Accepted
- **วันที่:** 2026-10-14
- **เกี่ยวข้อง:** spec หัวข้อ 7–8, ADR-0006

## บริบท
Email verification และ password reset ใช้ OTP ที่มี column, constraints, queries และ cleanup เหมือนกันทุกจุด

## ทางเลือกที่พิจารณา
1. แยกตาราง `password_resets` และ `email_verifications`
2. ตารางเดียวพร้อม column `purpose`

## การตัดสินใจ
ใช้ `otp_challenges` ที่มี `purpose` CHECK (`email_verification`, `password_reset`) partial UNIQUE (user_id, purpose) สำหรับ challenge ที่ยังเปิดอยู่ ทุก query และ repository method ระบุ purpose และ purpose ถูกรวมใน HMAC ของ OTP

## ผลที่ตามมา
- SQL/repository ชุดเดียว และ flow OTP ใหม่ในอนาคตเพิ่มได้ด้วยค่า purpose ใหม่ผ่าน migration
- ทุก query ต้องกรอง purpose มิฉะนั้น challenge ของ flow หนึ่งอาจถูกใช้กับอีก flow ต้องมี test ครอบคลุม
