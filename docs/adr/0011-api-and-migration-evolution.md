# ADR-0011: กติกาการเปลี่ยน API และ database migration

- **สถานะ:** Accepted
- **วันที่:** 2026-10-14
- **เกี่ยวข้อง:** AGENTS.md หัวข้อการดูแลและการขยายระบบในอนาคต

## บริบท
ระบบจะถูกขยายต่อหลายเฟส (หน้าหุ้น, ราคา live, corporate actions) และ frontend กับ API อาจ deploy คนละเวลา

## ทางเลือกที่พิจารณา
1. แก้ API/schema ตามสะดวกแล้ว deploy พร้อมกัน
2. กำหนดกติกา additive API และ expand → migrate → contract สำหรับ schema

## การตัดสินใจ
- `/api/v1` เปลี่ยนแบบเพิ่มเท่านั้น การเปลี่ยนที่ไม่เข้ากันทำเป็น `/api/v2` และเปิดคู่กันช่วงหนึ่ง
- client ต้องรองรับ field และ error code ที่ไม่รู้จัก
- goose ใช้เลขลำดับต่อเนื่อง ห้ามแก้ migration ที่รันในสภาพแวดล้อมที่ใช้ร่วมกันแล้ว
- schema เปลี่ยนแบบ expand → migrate → contract ข้าม release, สร้าง index บนตารางที่มีข้อมูลด้วย CONCURRENTLY, production แก้ด้วย forward migration

## ผลที่ตามมา
- deploy และ rollback image ได้โดย API เวอร์ชันก่อนหน้ายังทำงานกับ schema ใหม่
- การลบหรือเปลี่ยนชื่อ column ใช้หลาย release และต้องวางแผนล่วงหน้า
