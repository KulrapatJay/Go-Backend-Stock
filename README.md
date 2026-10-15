# Stock Portfolio Tracker — Backend

Go API สำหรับบันทึกรายการซื้อขายหุ้นสหรัฐฯ และติดตามต้นทุน/กำไรขาดทุนของหลายพอร์ต
ระบบนี้บันทึกการลงทุนที่ทำผ่านช่องทางอื่น ไม่ใช่ระบบส่งคำสั่งซื้อขายจริง

> **สถานะ:** Milestone 1 — มีเฉพาะ `/healthz`, `/readyz` และเอกสาร API; auth, พอร์ต และรายการซื้อขายยังไม่เปิดใช้งาน

## เริ่มใช้งาน (dev พร้อม hot reload)

```bash
cp .env.example .env
docker compose up --build
```

`docker compose up` โหลด `docker-compose.override.yml` อัตโนมัติ: mount source และ rebuild ด้วย air

## เปิดแบบ production-like ในเครื่อง (ไม่โหลด dev override)

```bash
docker compose -f docker-compose.yml up --build
```

ใช้ image ที่ build แล้วและรันเป็น non-root; production จริงใช้ Supabase และ deployment config แยก (milestone 11)

## URLs

| URL | ใช้ทำอะไร |
| --- | --- |
| http://localhost:8080/healthz | process ทำงานอยู่ |
| http://localhost:8080/readyz | เชื่อม database ได้ |
| http://localhost:8080/docs/ | Swagger UI (bundle ใน image ไม่โหลด CDN) |
| http://localhost:8080/swagger.json, /swagger.yaml | Swagger 2.0 spec |
| http://localhost:8025 | Mailpit กล่องจดหมายปลอมสำหรับ dev |

ทุก response มี `X-Request-ID` ใช้ค้น log ของคำขอนั้นได้

## Application log

```bash
docker compose logs -f api
```

Log เป็น JSON บรรทัดละ record (`time` UTC, `level`, `service`, `msg`, `event`, `request_id`)
Access log เก็บ method, route template, status และ duration โดยไม่เก็บ query string, cookies, headers หรือ body

## Database

- `deploy/db/bootstrap.sql` สร้าง roles `app_migrator` (เจ้าของ schema `app`), `app_runtime` และ `app_maintenance` ตอน volume ว่าง
- service `migrate` รัน goose ด้วย `app_migrator` และเก็บประวัติใน `app.goose_db_version`; `api` จะเริ่มเมื่อ migrate สำเร็จเท่านั้น
- PostgreSQL เปิดให้เครื่อง host ที่ `127.0.0.1:55432` (เปลี่ยนได้ด้วย `POSTGRES_HOST_PORT`)
- ล้างข้อมูลทั้งหมดแล้วเริ่มใหม่: `docker compose down -v`

## พัฒนาและทดสอบ

```bash
make tools            # ติดตั้ง swag, go-swagger, goose ตามเวอร์ชันที่ pin ลง ./bin
make lint-install     # ติดตั้ง golangci-lint ลง ./bin
make test             # unit/HTTP/contract tests (integration tests จะ skip)
make test-integration # integration tests กับ PostgreSQL ใน compose (ต้องเปิด stack ก่อน)
make swagger          # generate docs/api จาก annotations แล้ว validate
make check            # tidy + vet + lint + swagger + tests + ตรวจว่า docs/api commit แล้ว
```

ห้ามแก้ไฟล์ใน `docs/api/` ที่ generate แล้วด้วยมือ ให้แก้ annotations แล้วรัน `make swagger`

## เอกสารออกแบบ

- [AGENTS.md](AGENTS.md) — ข้อกำหนดของโปรเจกต์
- [Design spec](docs/superpowers/specs/2026-10-05-phase-1-design.md) และ [roadmap](docs/superpowers/plans/2026-10-05-phase-1-roadmap.md)
- [ADRs](docs/adr/README.md) — เหตุผลของการตัดสินใจสำคัญ
- [ER diagram (DBML)](docs/database/schema.dbml)
