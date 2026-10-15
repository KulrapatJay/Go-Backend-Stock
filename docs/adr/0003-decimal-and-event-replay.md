# ADR-0003: Decimal, transactions เป็นแหล่งความจริง และ replay แบบ position events

- **สถานะ:** Accepted
- **วันที่:** 2026-10-05 (เพิ่มรูปแบบ position events 2026-10-14)
- **เกี่ยวข้อง:** AGENTS.md หัวข้อกติกาพอร์ตและความแม่นยำ, spec หัวข้อ 12

## บริบท
เงินและจำนวนหุ้นต้องแม่นยำ รองรับเศษหุ้น และผู้ใช้แก้/ลบรายการย้อนหลังได้ ในอนาคตต้องรองรับ split/dividend

## ทางเลือกที่พิจารณา
1. float64: ง่ายแต่มี error จากการปัดเลขฐานสอง
2. เก็บยอด holdings แยกตาราง: อ่านเร็ว แต่ต้อง reconcile ทุกครั้งที่แก้ย้อนหลัง
3. shopspring/decimal + numeric(28,10) และคำนวณ holdings จากการ replay transactions

## การตัดสินใจ
ใช้ทางเลือก 3 ด้วย moving weighted-average cost แยกตาม (portfolio, instrument) การหารใช้ 28 ตำแหน่ง round-half-even Replay เป็น pure function ที่รับลำดับ `PositionEvent` ซึ่งเฟส 1 มีเฉพาะ trade (buy/sell) เรียงด้วย (executed_at, sequence)

## ผลที่ตามมา
- ผลคำนวณตรวจสอบได้และแก้ย้อนหลังได้โดยไม่มีข้อมูลซ้ำซ้อน
- พอร์ตที่มีประวัติยาวต้อง replay ทุกครั้ง ถ้าช้าค่อยเพิ่ม snapshot ที่เป็นผลคำนวณ ไม่ใช่แหล่งความจริง
- corporate actions เพิ่มเป็น event ชนิดใหม่ได้โดยไม่แก้ตาราง transactions แต่ต้องมี ADR กำหนดลำดับเมื่อเวลาเท่ากัน
