# ตั้ง branch protection และ required checks บน GitHub

ขั้นตอนนี้เป็นการเปลี่ยนค่าบน GitHub ของเจ้าของ repository จึงทำด้วยมือ (โค้ดและ workflow ไม่เปลี่ยนค่าให้)

## ก่อนเริ่ม

- Workflow `.github/workflows/ci.yml` รันตอนเปิด PR และตอน push เข้า `main` หรือ `developer` ชื่อ check ที่ต้องเลือกคือชื่อ job: **`backend`** (vet, lint, sqlc, Swagger, migrations, tests, image build) และ **`stack`** (เปิดระบบจาก checkout ใหม่แล้วตรวจ flow ทั้งหมดด้วย `scripts/e2e/stack-check.sh`)
- GitHub ให้เลือก check ได้เมื่อ job นั้นเคยรันสำเร็จใน repository อย่างน้อยหนึ่งครั้ง จึงต้องเปิด PR หรือ push `developer` ให้ CI ผ่านก่อน

## ตั้งค่า (ทำกับ `developer` แล้วทำซ้ำกับ `main`)

1. Settings → Branches → Add branch ruleset (หรือ Add classic branch protection rule)
2. ชื่อ rule ใส่ `developer` และ target เป็นกิ่งนั้น
3. เปิด **Require a pull request before merging**
4. เปิด **Require status checks to pass before merging** แล้วเลือก `backend` และ `stack`
5. เปิด **Require branches to be up to date before merging**
6. เปิด **Block force pushes** และไม่อนุญาตให้ลบกิ่ง
7. Save แล้วทำซ้ำสำหรับ `main`

## ตรวจว่าได้ผล

- เปิด PR ที่ทำให้ `go vet` ล้ม (เช่นเพิ่มตัวแปรที่ไม่ได้ใช้) → ปุ่ม Merge ต้องถูกปิดจน check ผ่าน
- badge ที่ต้นของ README ต้องแสดง `passing` สำหรับกิ่ง `developer`

## หมายเหตุ

- ไม่ใส่ secret ของ production ใน CI: workflow นี้ไม่เรียก Google, Alpaca หรือบริการส่งอีเมลจริง
