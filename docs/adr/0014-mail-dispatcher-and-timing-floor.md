# ADR-0014: ส่งอีเมลหลังตอบผ่าน dispatcher เบื้องหลัง และ timing floor ใน core

- **สถานะ:** Accepted
- **วันที่:** 2026-10-22
- **เกี่ยวข้อง:** [milestone 4 design](../superpowers/specs/2026-10-22-milestone-4-auth-design.md) หัวข้อ 2 และ 4.9, [ADR-0006](0006-email-verification-and-registration.md), phase 1 spec หัวข้อ 10

## บริบท
Register, ขอรหัสยืนยันใหม่ และ password reset ต้องตอบเหมือนกันทุกกรณี ไม่ว่าอีเมลนั้นมีบัญชีหรือไม่ แต่ในกรณีที่มีบัญชีเราส่งอีเมลหนึ่งฉบับ ส่วนกรณีที่ไม่มีเราไม่ส่ง (หรือส่งอีเมลชนิดอื่น) ถ้าส่งอีเมลใน request แล้วรอ เวลาตอบจะขึ้นกับ SMTP และบอกผู้โจมตีว่ามีบัญชีหรือไม่ นอกจากนี้ SMTP ที่ช้าหรือล่มไม่ควรทำให้ API ช้าหรือล่มตาม

## ทางเลือกที่พิจารณา
1. ส่งอีเมลใน request แล้วรอผล: เวลาตอบรั่วสถานะบัญชีและผูกกับความเร็วของ SMTP
2. Outbox ใน PostgreSQL ที่มี worker และ retry: ไม่เสียอีเมลเมื่อ API ตาย แต่เพิ่มตาราง worker และนโยบาย retry ซึ่งเกินขอบเขต phase 1
3. Dispatcher ในหน่วยความจำของ adapter ร่วมกับ timing floor ใน core

## การตัดสินใจ
เลือกข้อ 3
- `adapter/mail.Dispatcher` implement `port.Mailer` โดยใส่งานลงคิวขนาด 100 แล้วคืนทันที; worker 2 ตัวส่งผ่าน `SMTPMailer` ด้วย `MAIL_SEND_TIMEOUT`; shutdown หยุดรับงานและรอที่ค้างจน `SHUTDOWN_TIMEOUT` แล้วยกเลิกงานที่ยังส่งอยู่
- `service.Pacer` ใน core ทำให้ทุกคำตอบของ use case ที่ต้องไม่เปิดเผยสถานะบัญชีใช้เวลาอย่างน้อย `AUTH_RESPONSE_FLOOR` (500 ms) บวก jitter สุ่ม 0–`AUTH_RESPONSE_JITTER` (100 ms) ทั้งกรณีสำเร็จและ error โดยรับ `Clock` และฟังก์ชัน sleep เข้ามา จึงทดสอบได้โดยไม่รอจริง; อยู่ใน core เพราะเป็นคุณสมบัติด้านความปลอดภัยของ use case ไม่ใช่ของ HTTP
- log ของ dispatcher มีเฉพาะ `event`, `kind` และ `error_class` (เช่น `smtp_550`, `timeout`) ไม่มี OTP ที่อยู่อีเมล หรือข้อความ error ของเซิร์ฟเวอร์ เพราะข้อความนั้นมักซ้ำที่อยู่ผู้รับ

## ผลที่ตามมา
- เวลาตอบของ register/ขอรหัสใหม่ไม่ขึ้นกับ SMTP และไม่บอกว่ามีบัญชีหรือไม่ (ภายในความแม่นยำของ floor)
- API ตายหรือคิวเต็มระหว่างรออีเมล → อีเมลหาย; ผู้ใช้ขอรหัสใหม่ได้หลัง cooldown 60 วินาที; ผู้ใช้เห็น 202 เหมือนเดิมจึงไม่รู้ ต้องดู log `mail.queue_full` / `mail.send_failed`
- ทุก register ช้าอย่างน้อย 500 ms โดยตั้งใจ
- ถ้าต้องการความทนทานภายหลัง เพิ่ม outbox ได้โดย `port.Mailer` ไม่เปลี่ยน (เขียนแถวใน transaction เดียวกับข้อมูลแล้วให้ worker ส่ง)
