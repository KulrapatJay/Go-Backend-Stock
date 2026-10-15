# ADR-0009: Portfolio row lock + optimistic locking ด้วย version/ETag

- **สถานะ:** Accepted
- **วันที่:** 2026-10-14
- **เกี่ยวข้อง:** AGENTS.md หัวข้อความสอดคล้องของข้อมูล, spec หัวข้อ 9 และ 11

## บริบท
คำขอขายพร้อมกันต้องไม่ทำให้หุ้นติดลบ และผู้ใช้ที่เปิดหลายแท็บต้องไม่เขียนทับการแก้ไขที่ตนยังไม่เห็น

## ทางเลือกที่พิจารณา
1. Row lock อย่างเดียว: กันการเขียนพร้อมกัน แต่ไม่กัน lost update จากข้อมูลเก่าใน client
2. Optimistic locking อย่างเดียว: ต้อง retry เองเมื่อชนและตรวจ invariant ข้ามรายการยากกว่า
3. ใช้ทั้งสองแบบ

## การตัดสินใจ
ทุก mutation ของพอร์ต/รายการทำใน DB transaction เดียวหลัง `SELECT ... FOR UPDATE` บน portfolio และ portfolios/transactions มี `version` ที่ส่งเป็น ETag PUT/DELETE ต้องส่ง If-Match ไม่ส่งตอบ 428, ไม่ตรงตอบ 412 บังคับตั้งแต่ v1 เพราะการเปลี่ยนเป็นข้อบังคับภายหลังเป็น breaking change ไม่ถือ lock ระหว่างเรียก external API

## ผลที่ตามมา
- ไม่มีหุ้นติดลบจาก race และไม่มีการเขียนทับเงียบ ๆ
- การแก้ในพอร์ตเดียวกันถูก serialize และ client/curl ต้องส่ง If-Match
