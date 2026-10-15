# ADR-0004: Supabase เฉพาะ Database, private schema `app` และแยก DB roles

- **สถานะ:** Accepted
- **วันที่:** 2026-10-14
- **เกี่ยวข้อง:** AGENTS.md หัวข้อ Supabase, spec หัวข้อ 7 (Roles และ Supabase)

## บริบท
ต้อง deploy ฟรี ใช้ auth ของ Go เอง และไม่เปิดข้อมูลผ่าน Supabase Data API

## ทางเลือกที่พิจารณา
1. ใช้ schema `public` + RLS แบบ Supabase Auth: ไม่ตรงกับ session ของ Go
2. Private schema `app` + explicit grants + ownership checks ใน Go/SQL

## การตัดสินใจ
ใช้ schema `app` แยก roles: `app_migrator` (เจ้าของ schema, DDL), `app_runtime` (CRUD ที่จำเป็น, audit INSERT อย่างเดียว), `app_maintenance` (cleanup) ขั้น bootstrap สร้าง roles และ schema ก่อน goose; goose ใช้ `-table app.goose_db_version` จึงไม่มีอะไรใน `public` ปิด Data API และไม่ grant anon/authenticated Local/CI ใช้ PostgreSQL ใน Docker ด้วย migrations ชุดเดียวกัน

## ผลที่ตามมา
- ข้อมูลแอปไม่ถูก expose ผ่าน REST/GraphQL ของ Supabase และ runtime role แก้ audit ไม่ได้
- ต้องดูแล bootstrap runbook และ grants ในแต่ละ migration
- ต้องพิสูจน์ว่า goose รับชื่อตาราง schema-qualified ใน milestone 1
