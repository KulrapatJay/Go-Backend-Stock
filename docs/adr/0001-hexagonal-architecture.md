# ADR-0001: Hexagonal (Ports & Adapters) + DI ที่ composition root

- **สถานะ:** Accepted
- **วันที่:** 2026-10-05
- **เกี่ยวข้อง:** AGENTS.md หัวข้อสถาปัตยกรรม, spec หัวข้อ 2–3 และ 8

## บริบท
ระบบต้องเชื่อม PostgreSQL, Alpaca, Google OIDC และ SMTP ซึ่งอาจเปลี่ยนผู้ให้บริการภายหลัง และต้องทดสอบกติกาพอร์ต/OTP โดยไม่พึ่งบริการจริง

## ทางเลือกที่พิจารณา
1. Layered แบบ handler → service → repository ที่ผูก Fiber/sqlc ตรง ๆ
2. Hexagonal: core ประกาศ ports และ adapters implement

## การตัดสินใจ
ใช้ Hexagonal โดย core (domain/port/service) ไม่ import Fiber, pgx, sqlc หรือ SDK ภายนอก ประกอบ dependency เฉพาะใน `cmd/*/main.go` สร้าง interface เฉพาะตรงขอบจริง (DB, ราคา, อีเมล, เวลา, identity) และ inbound ports ของ use case ไม่ทำ generic repository

## ผลที่ตามมา
- เปลี่ยน provider หรือ DB adapter ได้โดยไม่แตะกติกาธุรกิจ และ unit test core ด้วย fakes ได้
- มีโค้ด mapping ระหว่าง domain types กับ sqlc/DTO มากขึ้น
- งาน atomic ข้ามหลาย repository ต้องผ่าน UnitOfWork port (ADR-0009)
