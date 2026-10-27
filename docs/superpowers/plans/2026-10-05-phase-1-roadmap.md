# Phase 1 Implementation Plan — Roadmap

> **For agentic workers:** เมื่อแบบระบบได้รับการยืนยันแล้ว ใช้ superpowers:executing-plans สำหรับการทำงานทีละส่วน หรือ superpowers:subagent-driven-development หากเลือกวิธีใช้ subagents ก่อนเริ่ม execution แตก milestone ที่กำลังทำเป็นแผนย่อยพร้อม interfaces และ tests ที่อ้างอิงแบบที่ยืนยันแล้ว

**Goal:** สร้างเว็บ portfolio หุ้นสหรัฐฯ ที่สมัคร/login ได้ มีหลายพอร์ต บันทึกซื้อขาย คำนวณต้นทุนและกำไรขาดทุนด้วยราคาหน่วงเวลา พร้อม audit และเดโมที่เปิดผ่าน Docker ได้

**Architecture:** Next.js เรียก Go API ซึ่งแยก core ออกจาก Fiber, PostgreSQL และผู้ให้บริการภายนอก ใช้ PostgreSQL ใน Docker สำหรับ local/CI และ Supabase เฉพาะ Database ตอน deploy Auth/OTP/session อยู่ใน Go; audit ของการเปลี่ยนข้อมูล commit พร้อมข้อมูลธุรกิจ

**Tech Stack:** Go, Fiber, pgx, sqlc, goose, shopspring/decimal, PostgreSQL, Docker Compose, GitHub Actions, Alpaca, swaggo/swag + Swagger UI; เพิ่ม Next.js/TypeScript หลัง backend ผ่าน milestone 9

**Spec:** [แบบระบบสำหรับตรวจยืนยัน](../specs/2026-10-05-phase-1-design.md) โดยยึดข้อกำหนดใน [AGENTS.md](../../../AGENTS.md)

**Status:** ผู้ใช้ยืนยันให้เริ่มตามลำดับแล้ว จัดทำข้อเสนอ milestone 0 ใน spec ที่ลิงก์ด้านบนแล้ว ผู้ใช้ยืนยันข้อเสนอเดิมและเลื่อนหน้าหุ้นรายตัว/กราฟไปเฟสถัดไปแล้ว (2026-10-14) และยืนยันแบบ email verification, portfolio name uniqueness และกติการหัสผ่านแล้ว milestone 0 ผ่านการตรวจรับ; milestone 1 ผ่านการตรวจรับเมื่อ 2026-10-15 (ลงมือครบทุก task, self-review ทั้ง branch ไม่พบ Critical/Important, CI บน GitHub ผ่าน) milestone 2 ผ่านการตรวจรับเมื่อ 2026-10-17 (ลงมือครบทุก task, รีวิวทั้ง branch ด้วย subagent แก้ Important ครบ 4 ข้อ) milestone 3 แผน 3a ผ่านการตรวจรับเมื่อ 2026-10-20 (รีวิวทั้ง branch ด้วย subagent แก้ Important ครบ 4 ข้อ, CI บน GitHub ผ่าน) แผน 3b ผ่านการตรวจรับเมื่อ 2026-10-21 (รีวิวทั้ง branch ด้วย subagent แก้ Important 2 ข้อและ Minor 4 ข้อ; CI บน GitHub ยังไม่ได้รันเพราะยังไม่ push) แผน 3c ผ่านการตรวจรับเมื่อ 2026-10-22 (รีวิวทั้ง branch ด้วย subagent แก้ Important 1 ข้อและ Minor 6 ข้อ) milestone 3 ครบทุกแผน; CI บน GitHub ของ 3b/3c ยังไม่ได้รันเพราะยังไม่ push milestone 4 มี design ([spec](../specs/2026-10-22-milestone-4-auth-design.md)) แบ่งเป็นแผน 4a/4b/4c; แผน 4a ลงมือครบแล้ว รอตรวจรับ แผน 4b ลงมือครบแล้ว รอตรวจรับ ขั้นต่อไปคือแผน 4c

## Global Constraints

- หลายพอร์ตต่อผู้ใช้ตามขอบเขตข้างต้น ทุกพอร์ตใช้ USD และรองรับ fractional shares
- Database ที่ deploy: Supabase PostgreSQL Free plan ใช้เฉพาะ Database ส่วน local development และ CI ใช้ PostgreSQL ใน Docker
- Auth, Google OpenID Connect, OTP และ server-side sessions ยังคงพัฒนาใน Go ไม่ใช้ Supabase Auth
- ห้ามใช้บริการเสียเงินในเฟสนี้
- ประกอบ dependency (DI) ที่ main.go เท่านั้น ไม่ใช้ global state
- ค่าที่ผู้ใช้บันทึก quantity, price และ fee ใช้ numeric(28,10); การหารสำหรับต้นทุนใช้ความละเอียด 28 ตำแหน่งหลังจุดทศนิยมและ round-half-even
- อายุ session เริ่มต้น 7 วันแบบ absolute expiry; OTP 6 หลักหมดอายุใน 10 นาที กรอกผิดได้ไม่เกิน 5 ครั้งต่อ challenge
- ขอ OTP ใหม่เว้นอย่างน้อย 60 วินาที ขอได้ไม่เกิน 5 ครั้งต่อชั่วโมงต่อบัญชี/อีเมล และ 20 ครั้งต่อชั่วโมงต่อ IP
- Audit retention เริ่มต้น 90 วัน; แอปไม่มีสิทธิ์ UPDATE/DELETE audit และไม่ log secrets
- แต่ละฟีเจอร์ต้องมี handler, audit และ tests ที่เกี่ยวข้องก่อนถือว่าเสร็จ
- ทำ backend ถึง milestone 9 ให้ผ่านก่อนเริ่ม frontend ใน milestone 10; ไม่สร้าง Next.js skeleton, web Dockerfile/service หรือ web CI ล่วงหน้า
- Swagger ใช้ Go handler/DTO annotations เป็นหลักแล้ว generate ด้วย swaggo/swag ลง docs/api/; ไม่แก้ generated spec ด้วยมือ
- อัปเดต annotations, generated docs และ contract tests พร้อมแต่ละ endpoint
- ข้อกำหนดทั้งหมดใน AGENTS.md ยังคงมีผล; รายการนี้ย้ำข้อกำหนดร่วม ไม่ได้ยกเลิกรายละเอียดที่ไม่ได้คัดลอกมา

## Review Focus

1. Cookie/CSRF/Google callback และ session revocation ถูกต้อง — ตรวจ HTTP/Swagger/browser ฝั่ง API ใน milestone 4 และ 9 แล้วตรวจซ้ำผ่าน Next.js same-origin routing ใน milestone 10
2. แก้รายการย้อนหลังหรือส่งคำขอขายพร้อมกันแล้วไม่เกิดยอดติดลบ รวมถึงไม่กระทบอีกพอร์ต — ตรวจใน milestone 7
3. ราคาเก่า/ไม่มีราคาไม่ทำให้แสดง unrealized P&L เป็นค่าที่ชวนเข้าใจผิด — ตรวจ API ใน milestone 8 และ 9 แล้วตรวจหน้าจอใน milestone 10
4. Audit insert ล้มเหลวแล้วข้อมูลธุรกิจไม่สำเร็จแยกกัน แต่ failed login/OTP ยังมีบันทึก — ตรวจใน milestone 3, 4, 5 และ 7
5. Local/CI ใช้ role/schema ตรงกับรูปแบบที่ deploy โดยไม่แตะ managed schemas หรือเปิดเผยข้อมูลผ่าน Supabase — ตรวจใน milestone 3 และ 11

## ลำดับและเกณฑ์ตรวจรับ

### 0. ออกแบบระบบให้ตรวจทานได้ก่อนลงมือ

**ผลลัพธ์:** `docs/superpowers/specs/2026-10-05-phase-1-design.md`

- [x] เสนอโครงไฟล์ รวม Docker/CI, ER diagram แบบข้อความ และ schema ของ users, auth_providers, sessions, portfolios, transactions, otp_challenges, audit_events, rate-limit state และ idempotency
- [x] เสนอ ports พร้อม method signatures, domain errors, HTTP status mapping, API endpoints และรูปแบบ decimal strings/pagination
- [x] ออกแบบ API contract รวม cookie/CSRF/errors/examples และวิธีเขียน Go annotations เพื่อ generate เอกสารด้วย swaggo/swag
- [x] เสนอเวอร์ชัน generator, Fiber middleware, Swagger UI และ validator จากเอกสารทางการ เลือก Swagger 2.0 พร้อมข้อจำกัด cookie auth; generation/build/browser compatibility ต้องพิสูจน์ใน milestone 1
- [x] กำหนดวิธีประสาน repositories ใน DB transaction เดียว พร้อมกติกา rollback, counters และ audit สำหรับผลล้มเหลว
- [x] กำหนด Compose backend-only, runtime/migration/maintenance roles และออกแบบ same-origin routing ที่ frontend จะเชื่อมในภายหลัง โดยยังไม่สร้าง web
- [x] เสนอ password hashing/parameters, tie-breaker ของ executed_at, อายุ idempotency, ขนาด audit metadata, IP masking และวิธี schedule cleanup ให้ตรวจพร้อมแบบ
- [x] ระบุ timeframe/cutoff/stale policy ของราคา และยืนยันว่าหน้าหุ้นรายตัว/กราฟเลื่อนไปเฟสถัดไป (2026-10-14)
- [x] ตรวจเวอร์ชันจากเอกสารทางการแล้ว pin เวอร์ชันที่จะใช้ และผู้ใช้ยืนยันข้อเสนอเดิม (2026-10-14)
- [x] ผู้ใช้ยืนยันแบบ email verification (register ตอบ 202), portfolio name uniqueness และกติการหัสผ่าน (2026-10-14)
- [x] ยืนยันการออกแบบเพื่ออนาคต: instruments surrogate key, optimistic locking, กติกา API/migration evolution, ADRs และ replay แบบ position events (2026-10-14); เขียน ADR ชุดแรกใน `docs/adr/`

**ผ่านเมื่อ:** ผู้ใช้ตรวจโครงสร้าง/ข้อมูล/API ได้ครบ ไม่มีการเปลี่ยนขอบเขตที่ยังไม่ได้ตกลง และยืนยันแบบระบบแล้ว

### 1. โครง Go + Docker/PostgreSQL + CI + application log + Swagger

**แผนลงมือ:** [2026-10-14-milestone-1-backend-foundation.md](2026-10-14-milestone-1-backend-foundation.md)

**ไฟล์หลัก:** `cmd/api/main.go`, `go.mod`, `internal/adapter/http/health.go`, `Dockerfile`, `docker-compose.yml`, `docker-compose.override.yml`, `.env.example`, `.gitignore`, `.dockerignore`, `.air.toml`, `.github/workflows/ci.yml`, `README.md`

**เอกสาร API:** annotations ใน entrypoint/HTTP handlers/DTO, generated files ใน `docs/api/`, `internal/adapter/http/docs.go` และ Swagger UI assets ที่ bundle ใน API image

- [x] สร้าง Go API, config validation, JSON logger/request ID และ graceful shutdown โดยไม่สร้าง Next.js
- [x] เพิ่ม access-log/recover middleware และ error handler พร้อม tests ที่ตรวจ final status ของ success/error/panic และ secret redaction
- [x] สร้าง /healthz, /readyz และ Compose postgres/migrate/api/mailpit พร้อม Go hot reload และ non-root API image
- [x] เพิ่ม health endpoint annotations และคำสั่ง swag init ของโปรเจกต์เพื่อ generate docs.go/swagger.json/swagger.yaml ลง docs/api/
- [x] เปิด /docs, /swagger.json และ /swagger.yaml ผ่าน Go API; ใน CI generate ซ้ำ/ตรวจ generated diff และ validate spec พร้อมระบุ endpoints ที่ยังไม่พร้อม
- [x] สร้าง local bootstrap (`deploy/local/initdb/`) สำหรับ roles และ schema `app` แล้วรัน goose ด้วย `-table app.goose_db_version` พร้อม baseline migration เพื่อทดสอบ migrate service; พิสูจน์ว่า goose รองรับชื่อตารางแบบ schema-qualified และไม่มีตารางใดถูกสร้างใน `public`; ตารางธุรกิจเพิ่มใน milestone 3
- [x] ตั้ง CI เริ่มต้นสำหรับ Go vet/lint/test, API image build และ Swagger checks; เพิ่ม sqlc consistency และ repository tests ใน milestone 3 โดยยังไม่มี web jobs (เขียน workflow และตรวจ syntax ด้วย actionlint แล้ว; ขั้นตอนเทียบเท่าทั้งหมดรันผ่านในเครื่องด้วย `make check` และ `make test-integration`; **ผู้ใช้ยืนยันแล้ว (2026-10-15) ว่า CI บน GitHub ผ่าน** หลังสร้าง remote repository)
- [x] บันทึกวิธีเปิดระบบด้วย `cp .env.example .env && docker compose up --build` และวิธีดู application log

**ผ่านเมื่อ:** เปิด backend stack ได้โดยไม่มี web service, migration จบก่อน API เริ่ม, health/readiness และ Swagger UI ใช้งานได้, API image build และ checks ที่ตั้งไว้ผ่าน ยังไม่อ้างว่า auth/พอร์ตใช้งานได้ใน milestone นี้

**ตรวจรับแล้ว 2026-10-15** — ข้อที่เลื่อนไปทำใน milestone ถัดไป (ผลจาก self-review, ไม่ขวางการตรวจรับ):
- method ไม่ตรงกับ route ที่มีอยู่ (เช่น POST /healthz) ตอบ 404 แทน 405 + `Allow` — แก้พร้อม route แบบไม่ใช่ GET ใน milestone 4 — **ปิดแล้วในแผน 4b Task 6**
- Swagger `basePath` ตายตัว `/` ทำให้ Try it out หลัง reverse proxy ที่มี prefix ยิง URL ผิด — แก้ใน milestone 10
- README: เปลี่ยนรหัสผ่านใน `.env` หลังรันครั้งแรกไม่มีผลจนกว่าจะ `docker compose down -v`; Makefile อ่าน `.env` เป็นไวยากรณ์ make (ค่าที่มีเครื่องหมายคำพูด/`#`/ช่องว่างจะพัง)
- CI ยังไม่ทดสอบ compose stack (migrate ก่อน API, migration พัง); ยังไม่มี test ของ `main.go run()` และ timeout mapping (408)
- ความเรียบร้อยของ log: `/readyz` ล้ม 1 ครั้งเขียน 3 บรรทัด, "server started" ถูก log ก่อน bind สำเร็จ, access log แสดง method `GET` เมื่อ request อ่านไม่ได้, `Recover` log ค่า panic (ตัดที่ 512 ตัวอักษร)
- `swaggo/swag` ถูก link เข้า binary production จาก `docs/api/docs.go` (generated ตาม AGENTS.md)

### 2. Domain + ports + กติกาข้อมูลพื้นฐาน

**แผนลงมือ:** [2026-10-16-milestone-2-domain-ports.md](2026-10-16-milestone-2-domain-ports.md)

**ไฟล์หลัก:** `internal/core/domain/` (value types, entities, errors), `internal/core/port/` (repository ports, UnitOfWork, Clock), `internal/core/boundary_test.go`

- [x] สร้าง entities/value types และ validation สำหรับ quantity/price/fee, symbol, เวลา และ decimal strings
- [x] สร้าง service/repository/provider ports, Clock และขอบเขต atomic operations ตามแบบที่ยืนยัน (สร้างเฉพาะ repository ports, UnitOfWork และ Clock ที่ milestone 3 ใช้; service/provider ports สร้างใน milestone ที่ใช้จริง ตามที่ผู้ใช้ยืนยัน 2026-10-16)
- [x] เพิ่ม unit tests สำหรับ precision/scale, ค่าติดลบ/ศูนย์ และค่าที่เกินช่วง โดยไม่ต้องใช้ DB หรือ Fiber

**ผ่านเมื่อ:** core compile/test ได้เองและไม่มี import ของ Fiber, pgx, sqlc-generated code หรือ provider SDK

**ข้อที่ส่งต่อให้ milestone 3** (จากรีวิวทั้ง branch 2026-10-17 ระดับ Minor):
- ทำ helper แปลง error จาก `Parse*` ของข้อมูลที่อ่านจาก DB ให้เป็น `internal` (ไม่ให้ข้อมูลเสียใน DB กลายเป็น 400) และให้ `ID` map กับ pgx ได้โดยไม่ต้องผ่าน string
- เขียน contract ของ ports ให้ครบใน comment และ repository tests: unique violation → `ErrPortfolioNameConflict`/`ErrIdentityConflict`, not-found ของทุก method, `TransactionRepository.Update` ต้องกรองด้วย portfolio_id และห้ามแก้ portfolio_id, tie-break ของการเรียง list, พฤติกรรม `IdempotencyRepository.Save` เมื่อชน
- DB CHECK ขนาด audit metadata ต้องนับแบบเดียวกับ Go (raw bytes ≤ 8 KiB) เพราะ `jsonb::text` เพิ่มช่องว่าง; email ต้องเก็บ/ค้นด้วยค่าที่ Go normalize แล้ว ไม่พึ่ง `lower()` ใน SQL
- `NewTransactionFilter` ยังรับ `Symbol{}`/`Side("")` ที่ว่าง; entity ที่ไม่มี constructor (Transaction/Portfolio/User) ยังส่ง zero value ถึง repository ได้ (DB CHECK/FK จับเป็น internal)
- `ParseExecutedAt` รับทศนิยม 7 หลักที่หลักท้ายเป็น 0 และ `,` แทน `.` (ไม่ปัดค่า แต่ไม่ตรง design §3); comment ของ sentinel errors ขัดกับ comment ใน ports; `errors.Is(err, (*domain.Error)(nil))` panic
- อีเมลรับอักขระพิเศษของ mail syntax (`,;<>`) และ format characters: Mailer adapter (milestone 4) ต้อง validate ด้วย `net/mail` ก่อนใส่ header
- เอกสาร: spec หัวข้อ 8 ยังเขียนว่า `domain.ID` เป็น UUID string และ design §3 เขียน `ParseID(s)` (โค้ดจริงคือ `ParseID(field, s)`)

### 3. Schema + goose + sqlc + repositories + audit persistence

**แผนลงมือ:** [design](../specs/2026-10-17-milestone-3-persistence-design.md) — [3a](2026-10-17-milestone-3a-schema-sqlc-audit.md) (schema, roles, sqlc, audit), [3b](2026-10-20-milestone-3b-auth-repositories.md) (repositories ฝั่ง auth), [3c](2026-10-21-milestone-3c-portfolio-repositories.md) (repositories ฝั่งพอร์ต)

**ไฟล์หลัก:** `migrations/`, `sqlc.yaml`, `internal/adapter/postgres/queries/`, `internal/adapter/postgres/sqlc/`, `internal/adapter/postgres/` สำหรับ repository implementations/tests

- [x] เพิ่มตาราง constraints/indexes และ DB role permissions ตามแบบ รวม instruments (surrogate key) และ version columns; ไม่แก้ managed schema ของ Supabase; migration ใช้เลขลำดับต่อเนื่องและไม่แก้ไฟล์ที่รันแล้ว
- [x] เขียน SQL queries, generate sqlc, map numeric/domain types และทดสอบ repository กับ PostgreSQL จริง (3a: sqlc + mapper + `AuditRepository`; 3b: repositories ฝั่ง auth ทั้ง 6 ตัว; 3c: repositories ฝั่งพอร์ตทั้ง 4 ตัว + keyset pagination)
- [x] สร้าง transaction coordination และ audit append/read พร้อม failure injection ที่พิสูจน์ว่า audit/data rollback ด้วยกัน (3a: `withinTx` + failure injection ด้วย audit; 3c: `Store` เป็น `port.UnitOfWork` + failure injection ด้วยพอร์ตจริง + ขายพร้อมกันไม่เกินที่ถือ)
- [x] ทดสอบ runtime role แก้/ลบ audit ไม่ได้, maintenance cleanup จำกัดเฉพาะสิทธิ์ที่จำเป็น และ audit ไม่ถูก cascade ลบตามข้อมูลต้นทาง
- [x] เปิด CI ตรวจ goose บน DB ว่าง, repository integration tests และ `sqlc generate` ตามด้วย `git diff --exit-code`

**ผ่านเมื่อ:** schema สร้างจากศูนย์ได้, generated code ไม่มี diff, role permissions ถูกต้อง และ integration tests ผ่านโดยไม่ใช้ Supabase จริง

**ข้อที่ส่งต่อให้แผน 3b/3c** (จากรีวิว 3a 2026-10-20; ต้องตัดสินใจในแผนก่อนเขียน repository):
- **conflict ทำให้ transaction จบ:** unique violation ที่แปลงเป็น `ErrPortfolioNameConflict`/`ErrIdentityConflict`/`ErrIdempotencyConflict` ไม่ทำให้ transaction กลับมาใช้ได้ (PostgreSQL abort แล้ว) แต่ละ repository ต้องเลือก `INSERT ... ON CONFLICT DO NOTHING` + ตรวจจำนวนแถว (ใช้กับ index บน `lower(name)` ได้) หรือ savepoint และ service ที่ต้องบันทึก audit หลัง conflict ต้องทำใน transaction ใหม่ — **3b:** users/auth_providers ใช้ `ON CONFLICT DO NOTHING` แล้ว; **3c:** portfolios ใช้ `ON CONFLICT` ตอน insert; การเปลี่ยนชื่อซ้ำและ idempotency `Save` ที่ชน ยอมให้ transaction จบตาม `port.UnitOfWork`
- **instrument ที่ provider เปลี่ยน symbol** (FB → META โดย asset id เดิม): **ผู้ใช้เลือก (2026-10-20) บันทึกเป็นข้อจำกัดของ phase 1** ตาม AGENTS.md ที่ไม่รวม symbol changes — runtime ยังไม่มีสิทธิ์ UPDATE `instruments`; แผน 3c ต้องแปลงการชน `instruments_provider_asset_unique` เป็น error ที่อ่านเข้าใจ (ไม่ใช่ 500) และระบุข้อจำกัดใน README/UI — **3c:** คืน `unknown_symbol` (ผู้ใช้เลือก 2026-10-21) และระบุใน README แล้ว; UI ระบุตอนทำ frontend
- **`transactions_portfolio_id_fkey` แปลงเป็น `ErrPortfolioNotEmpty` ทั้งสองทิศ:** ย้ายการแปลงนี้ไปที่ `DeleteOwnedEmpty` แทนการแปลงทั่วไป (INSERT ลงพอร์ตที่ไม่มีอยู่จะได้ 409 ผิดความหมาย) — **3c:** ย้ายแล้ว
- **OTP ขอพร้อมกัน:** `otp_challenges_one_open_idx` ชนแล้วเป็น `internal` ให้ lock แถว user ก่อน `InvalidateOpen` + `Insert` ตาม phase 1 spec หัวข้อ 9 — **3b:** `TestOTPChallengeConcurrentReissueUnderUserLock` พิสูจน์ลำดับ lock นี้ที่ระดับ repository แล้ว; service ของ milestone 5 ต้องทำตาม
- **เล็กน้อยจากรีวิว 3b (2026-10-20, เลื่อนไปทำใน milestone 4–5 ตอนที่ service เริ่มใช้ repositories ฝั่ง auth):** test ยังไม่คุม `FOR UPDATE` ของ `OTPChallengeRepository.Lock` และ `Lock`/`RecordFailure` กับ purpose อื่น (พฤติกรรมจริงถูกต้อง); เวลาใน Go (ns) กับใน PostgreSQL (µs) ต่างกันได้ในช่วง 1 µs ตอนตรวจหมดอายุ; `Admit` ที่ได้ rule `Limit: 0` คืน `not_found` แทน `internal` (rule ควรมาจาก `NewRateLimitRule` เสมอ); ~~`TestOTPChallengeConcurrentReissueUnderUserLock` เรียก `t.Fatal` จาก goroutine~~ (แก้ในรอบรีวิว 3c: สร้าง challenge ไว้ก่อน)
- **เล็กน้อย:** rollback ใน `withinTx` ยังไม่มี timeout; request ที่ client ยกเลิกถูกจัดเป็น `internal` (log เป็น 500); test identity ครอบเฉพาะ `rate_limit_events` ควรเพิ่ม `transactions.sequence` (**3c:** `TestTransactionInsertKeepsEveryDigit` insert ผ่าน `app_runtime` และได้ sequence แล้ว); integration tests ทิ้งแถว audit ใน DB dev (runtime ลบไม่ได้ ล้างด้วย `docker compose down -v`); `numeric(28,10)` ปัดเงียบถ้าข้าม `Parse*` ทุก repository test ต้องส่งค่าผ่าน `Parse*` เสมอ

### 4. Auth + email verification + sessions + Google login + HTTP handlers

**แผนลงมือ:** [design](../specs/2026-10-22-milestone-4-auth-design.md) — [4a](2026-10-22-milestone-4a-auth-building-blocks.md) (ชิ้นส่วนพื้นฐาน; ลงมือครบ รอตรวจรับ); [4b](2026-10-24-milestone-4b-auth-endpoints.md) (AuthService, EmailVerificationService, middleware, endpoints; ลงมือครบ รอตรวจรับ); 4c (Google OIDC) ยังไม่มีแผน

**แผน 4a ปิดข้อส่งต่อแล้ว:** `ParseEmail` ปฏิเสธ `"(),:;<>[\]` และอักขระซ่อน (ข้อส่งต่อจาก milestone 2); `service.NowForStorage` ตัดเวลาที่ microsecond (รีวิว 3b); `service.NewRateLimitPolicy` สร้าง rules ผ่าน `domain.NewRateLimitRule` เท่านั้น จึงไม่มี rule ที่ `Limit` เป็นศูนย์ (รีวิว 3b) — ที่เหลือของข้อส่งต่อ 3b (`FOR UPDATE` ของ `OTPChallengeRepository.Lock`, purpose อื่น) ปิดใน 4b ตามหัวข้อ 9 ของ design

**ข้อส่งต่อจากรีวิวทั้ง branch ของแผน 4a ไป 4b** (ledger ของแผนเป็น git-ignored จึงจดไว้ที่นี่):

- ก่อน merge: `.env.example` ที่ commit ต้องมี `CSRF_SECRET`, `OTP_SECRET`, `RATE_LIMIT_SECRET`, `OIDC_SECRET` ที่มีค่า `dev-only-…` ยาวอย่างน้อย 32 bytes และต่างกันทั้งสี่ตัว (ตอนนี้ `config.Load` ต้องใช้ค่าเหล่านี้ใน dev จึงทำให้ `cp .env.example .env && docker compose up` ล้มจนกว่าจะมี) — ผู้ใช้เป็นเจ้าของไฟล์นี้
- งานแรกของ 4b: `domain.RateLimitDecision` ต้องบอก scope ที่ปฏิเสธ (เพิ่ม field `Scope` และตั้งจาก `k.scope` ใน `postgres/ratelimit.go`) เพื่อใส่ `scope` ใน metadata ของ `security.rate_limited` ตาม spec หัวข้อ 5 — **ปิดแล้วในแผน 4b**
- งานแรกของ 4b: เสริมการปิดค่าลับก่อนที่ `RegisterInput`/`LoginInput` จะถือ `Password`: `Password`, `OTPCode` และ `config.Secret` ยังพิมพ์ค่าจริงเมื่อใช้ `%d` หรือเมื่ออยู่ใน field ที่ไม่ export ของ struct อื่น (fmt ไม่เรียก `String()` ที่นั่น) — พิจารณาเก็บค่าหลัง pointer + `fmt.Formatter` และชนิดที่ปิดค่าลับสำหรับ session token/CSRF token/nonce พร้อม test ที่ format input จริง — **ปิดแล้วในแผน 4b**
- ตอนต่อสาย 4b: ส่ง `service.NowForStorage(clock)` เป็น `now` ของ `RateLimitRepository.Admit` ด้วย; ต่อ `ClientIPResolver` ด้วย `RemoteAddr().String()` และ header `X-Forwarded-For` ทุกบรรทัดตามลำดับ (ปิด trust proxy ของ Fiber เอง); ตรวจ cookie `csrf_nonce` ว่าเป็น base64url 43 ตัวก่อนสร้าง binding; login ต้องตัดรหัสผ่านเกิน 32 ตัวก่อน hash และมี test; ปิด HTTP ก่อนแล้วจึงเรียก `Dispatcher.Close` ด้วยเวลาที่เหลือของ `SHUTDOWN_TIMEOUT`; `ErrQueueFull`/`ErrDispatcherClosed` ต้อง log แล้วไปต่อ ห้ามเปลี่ยนคำตอบ 202 — **ปิดแล้วในแผน 4b**
- เก็บกวาด (ทำเมื่อแตะไฟล์นั้น): `DefaultArgon2Params` เป็นตัวแปร global ที่แก้ได้ → เปลี่ยนเป็นฟังก์ชัน; `NewCSRFCodec`/`NewPacer` ต้องปฏิเสธ clock ที่เป็น nil; `deliver` ควรไม่นับ error ของ `Quit` หลัง DATA สำเร็จ และคืน `ctx.Err()` เมื่อถูก cancel เพื่อให้ Dispatcher จัดเป็น `canceled`; worker ของ `Dispatcher` ต้องมี `recover` ที่ log `mail.panic`; ใช้ `domain.MaxOTPFailedAttemptsCeiling` แทนเลข 20 ใน `config/auth.go`; `parseCIDRs` ควร normalise/ปฏิเสธ prefix แบบ IPv4-mapped; warn log ของ hasher ควรแยก timeout กับ cancel; เติมค่าเริ่มต้นของ `MAIL_SEND_TIMEOUT` (10s) ใน README — **ปิดแล้วในแผน 4b**
- ต้องถามผู้ใช้ใน 4b: rate limit ของ IPv6 ใช้ address เต็ม (/128) ตาม spec 4.7 ซึ่งผู้ใช้ที่มี prefix /64 หมุน address เพื่อเลี่ยงโควตาได้ — จะจัดกลุ่มเป็น /64 หรือไม่ (ถ้าเปลี่ยนให้เขียน ADR) — **ตอบแล้ว (2026-10-24): คง /128 ตาม spec**
- ควรเพิ่มใน CI: service Mailpit และ `REQUIRE_MAIL_INTEGRATION=1` เพื่อให้ test ส่งอีเมลจริงไม่พังเงียบ ๆ — **ปิดแล้วในแผน 4b**

**ข้อส่งต่อจากแผน 4b ไปแผนถัดไป:**

- docs/api: เพิ่ม `format: date-time` และ `required` list ของ DTO ตาม AGENTS.md — **ปิดแล้วในรอบแก้หลังรีวิวทั้ง branch ของ 4b**
- login ซ้อน (re-login ขณะมี session) ยังไม่ revoke session เดิม
- สอง login พร้อมกันบนบัญชีที่ hash เก่าอาจได้ 401 หนึ่งครั้ง (fails closed)
- test `TestConcurrentWrongCodesAreAllCounted` ส่ง 5 คำขอเท่ากับเพดาน ควรเพิ่มเป็น 8 — **ปิดแล้ว: เปลี่ยนเป็น `TestConcurrentWrongCodesAreSerializedByTheChallengeLock` ส่ง 8 คำขอ**
- ยอมรับความเสี่ยงการสมัครชิงอีเมล (ตัดสินใจ 2026-10-27): ผู้โจมตีสมัครอีเมลของเหยื่อไว้ก่อนด้วยรหัสของตัวเอง ถ้าเหยื่อกด Resend แล้วยืนยันอีเมล บัญชีจะยืนยันโดยยังใช้รหัสผ่านของผู้โจมตี — คง Resend แบบรับอีเมลอย่างเดียวไว้; ทางแก้คือ forgot-password ใน milestone 5 (reset เปลี่ยนรหัสและ revoke ทุก session) และทบทวนอีกครั้งก่อน deploy
- ต้อง rotate รหัสผ่านของ dev DB ถ้าแชร์ transcript เพราะ Makefile echo คำสั่งตอนรัน `make test-integration` โดยไม่ใส่ `-s` (การเพิ่ม `@` ใน recipe ไม่อยู่ในงานของ 4b แค่จดไว้)

**ไฟล์หลัก:** `internal/core/service/auth*.go`, `internal/core/service/otp*.go`, `internal/core/service/email_verification*.go`, `internal/adapter/http/auth*.go`, `internal/adapter/http/middleware/`, `internal/adapter/identity/`, `internal/adapter/mail/`

- [x] ทำ OTP generation/HMAC แบบมี purpose และ SMTP adapter ไป Mailpit ตั้งแต่ขั้นนี้ เพราะ signup ต้องยืนยันอีเมลก่อน login ได้ (แผน 4a: `GenerateOTP`, `OTPCodec`, `SMTPMailer`, `Dispatcher`; `make test-mail` ส่งถึง Mailpit จริง)
- [x] ทำ register ที่ตอบ 202 แบบเดียวกันทุกกรณี, confirm/resend รหัสยืนยันอีเมล และปฏิเสธ login ของบัญชีที่ยังไม่ยืนยันด้วย `email_not_verified` (แผน 4b)
- [x] ทำ email/password login/logout, current-user endpoint และ session authentication พร้อม expiry/revocation (แผน 4b)
- [x] เพิ่ม cookie, CSRF, Origin checks, login rate limit และ error mapping พร้อม audit ของผลสำเร็จ/ล้มเหลว (แผน 4b)
- [ ] เพิ่ม Google OIDC, state/nonce validation, PKCE S256 และ explicit account linking; ปิด flow ชัดเจนเมื่อ config ไม่ครบ
- [x] login ของอีเมลที่ไม่มีบัญชี/บัญชี Google-only ตรวจกับ hash จำลองและตอบแบบเดียวกับรหัสผิด (แผน 4b)
- [x] ทำ password validator (ความยาว, ASCII, พิมพ์เล็ก/ใหญ่/อักขระพิเศษ, blocklist แบบ go:embed, ไม่มีส่วนของอีเมล) พร้อม unit tests ของทุก field reason (แผน 4a: `domain.ParsePassword`, `service.PasswordPolicy`, ADR-0015)
- [ ] ทดสอบ fake identity provider, replayed callback, invalid audience/issuer, session หมดอายุ/revoked และการ rollback เมื่อ audit insert ล้มเหลว
- [ ] เพิ่ม auth annotations/examples แล้ว generate เอกสารใหม่ ตรวจ Swagger UI/browser cookie/CSRF โดยไม่ลดความปลอดภัย พร้อม curl cookie-jar examples และขั้นตอน Google login สำหรับ flow ที่ UI ไม่รองรับ ทั้งหมดทดสอบได้โดยไม่ใช้ Next.js

**ผ่านเมื่อ:** ทดสอบ flow สมัคร → ยืนยันอีเมลจาก Mailpit → login ผ่าน HTTP ได้ครบ, อีเมลซ้ำไม่เปิดเผยผ่าน response, logout แล้ว session เดิมเข้าถึง endpoint ที่ป้องกันไม่ได้, login ผิดยังมี audit และไม่มี secret ใน log; Google จริงตรวจเพิ่มเมื่อมี credentials ก่อนปล่อยระบบ

### 5. Password reset + HTTP handlers

**ไฟล์หลัก:** `internal/core/service/password_reset*.go`, `internal/adapter/http/password_reset*.go`

- [ ] ใช้ OTP/Mailer จาก milestone 4 ทำ request/reset endpoints และตั้ง email_verified_at เมื่อ reset สำเร็จ
- [ ] ทำ resend invalidation, cooldown และ account/IP limits ที่ไม่หายเมื่อ API restart
- [ ] ทำ password update, OTP consume และ session revocation พร้อม audit ใน transaction เดียว โดยส่งอีเมลนอก transaction
- [ ] ใช้ Clock fake ทดสอบ 10 นาที/60 วินาที/โควตาต่อชั่วโมง, กรอกผิด 5 ครั้ง, เลขศูนย์นำหน้า และ concurrent reset

**ผ่านเมื่อ:** reset จากอีเมลใน Mailpit ได้ครั้งเดียว, session เก่าถูก revoke, กรอกผิดแล้ว counters/audit ยังคงอยู่ และ Google-only/unknown email ไม่เปิดเผยสถานะผ่าน response

### 6. จัดการหลายพอร์ต + ownership + HTTP handlers

**ไฟล์หลัก:** `internal/core/service/portfolio*.go`, `internal/adapter/http/portfolio*.go`

- [ ] ทำสร้าง/รายการ/รายละเอียด/เปลี่ยนชื่อ/คำอธิบาย/ลบพอร์ต พร้อม audit; ชื่อพอร์ตซ้ำแบบไม่สนตัวพิมพ์ภายในผู้ใช้เดียวคืน 409 `portfolio_name_conflict` ส่วนต่างผู้ใช้ใช้ชื่อเดียวกันได้
- [ ] ตรวจ ownership ทุกเส้นทาง และปฏิเสธการลบพอร์ตที่มี transactions
- [ ] ส่ง ETag/version และบังคับ If-Match สำหรับ PUT/DELETE พร้อม HTTP tests ของ 428/412
- [ ] ทดสอบด้วยผู้ใช้สองคนและหลายพอร์ตว่าการเดา ID ไม่ทำให้อ่านหรือแก้ข้อมูลข้ามผู้ใช้ได้

**ผ่านเมื่อ:** ผู้ใช้คนเดียวมีหลายพอร์ตได้และขอบเขตสิทธิ์ถูกต้องทั้ง API/repository

### 7. Transactions + replay validation + concurrency/idempotency

**ไฟล์หลัก:** `internal/core/service/transaction*.go`, `internal/core/domain/position*.go`, `internal/adapter/http/transaction*.go`

- [ ] ทำ InstrumentProvider port, demo adapter และ fake สำหรับ tests; ตรวจ symbol กับ instruments ก่อน แล้วเรียก provider นอก lock เมื่อยังไม่เคยตรวจ (unknown_symbol 422, provider ล่ม 503, rate limit `instrument_lookup_user`)
- [ ] ทำ CRUD/list ซื้อขาย, decimal validation, stable chronological ordering และ audit before/after ตาม allowlist
- [ ] ทำ replay จำนวนหุ้นและต้นทุนแบบ pure function ที่รับลำดับ position events (เฟส 1 มีแค่ trade) ตั้งแต่ขั้นนี้ เพราะการตรวจขายเกิน/แก้ย้อนหลังต้องใช้กติกาเดียวกับผลคำนวณพอร์ต
- [ ] ล็อก portfolio ระหว่างอ่านประวัติ/ตรวจยอด/แก้รายการ/idempotency/audit และไม่ถือ lock ขณะเรียก external services
- [ ] ทดสอบซื้อ/ขายเศษหุ้น, ขายบางส่วน/ทั้งหมด, ขายเกิน, แก้/ลบย้อนหลัง, เวลาเท่ากัน, เปลี่ยน symbol และพอร์ตแยกกัน
- [ ] ทดสอบ concurrent sells, key เดิม/payload เดิม, key เดิม/payload ต่างกัน และ retry ที่ไม่เพิ่ม transaction, version หรือ success audit ซ้ำ
- [ ] ทดสอบ If-Match ของ transaction update/delete (428/412) และว่าเปลี่ยน symbol แล้ว instrument_id ถูกต้อง

**ผ่านเมื่อ:** บันทึกได้เฉพาะ symbol ที่ตรวจแล้ว, ประวัติหุ้นไม่ติดลบ ณ จุดใดหลัง mutation, concurrent requests ไม่ข้ามกติกา และ replay เป็นแหล่งคำนวณเดียวที่ milestone 8 ใช้ต่อ

### 8. ราคาตัวอย่าง/Alpaca + holdings + realized/unrealized P&L

**ไฟล์หลัก:** `internal/adapter/demo/`, `internal/adapter/alpaca/`, `internal/core/service/valuation*.go`, `internal/adapter/http/holding*.go`

- [ ] เพิ่ม Alpaca InstrumentProvider ผ่าน Assets API พร้อม HTTP fixtures (พบ/ไม่พบ/inactive/ไม่ใช่ us_equity/timeout) และตรวจสิทธิ์ด้วยบัญชีจริงเมื่อมี credentials
- [ ] เพิ่ม demo PriceProvider ที่ติดป้ายชัดเจน และ Alpaca adapter ตาม feed/cutoff ที่ยืนยัน โดยใช้ HTTP fixtures ใน tests
- [ ] ใช้ replay จาก milestone 7 สรุป holdings/cost/realized P&L แล้วเติมราคาเพื่อคำนวณ market value/unrealized P&L
- [ ] เพิ่ม batch/cache, timeout, bounded retry และ quote metadata ได้แก่ as_of/source/feed/fetched_at
- [ ] ทดสอบ fees/rounding/full-sale residual, ตลาดปิด/วันหยุด, stale/missing price และ partial portfolio valuation
- [ ] ตรวจ Alpaca จริงแบบ smoke test เมื่อมี credentials โดยไม่ทำให้ CI หรือโหมดเดโมพึ่งบริการจริง

**ผ่านเมื่อ:** ผลคำนวณตรงตัวอย่างที่ตรวจด้วยมือ, ราคา unavailable ไม่ถูกแทนด้วยศูนย์, realized ยังดูได้เมื่อ provider ล้มเหลว และไม่มีการ fallback ไปข้อมูลเดโมเงียบ ๆ

### 9. ตรวจ backend ทั้งระบบ + Swagger + คู่มือใช้งาน

**ไฟล์หลัก:** `README.md`, `.github/workflows/ci.yml`, `docs/api/`, HTTP/integration tests และคำสั่ง maintenance cleanup ตามแบบที่ยืนยัน

- [ ] ทดสอบ fresh clone/fresh DB ตาม README รวม failure ของ migrations, configuration และ external services โดยไม่ต้องมี Next.js
- [ ] ตรวจ backend CI ครบ: Go vet/lint/tests, sqlc consistency, migrations, API Docker build และ Swagger generation/validation/contract checks
- [ ] ตรวจ endpoint coverage, decimal/error schemas และตัวอย่างจาก annotations ให้ตรงกับ API จริง โดยไม่มี credentials ใน generated docs
- [ ] ทดสอบ flow สมัคร → ยืนยันอีเมล → login → สร้างสองพอร์ต → ซื้อ/ขาย → ดูผล → logout และ reset password ผ่าน HTTP/Swagger/curl พร้อมตรวจ browser cookie/CSRF และ Google callback โดยไม่สร้าง frontend
- [ ] ตรวจ provider adapters ด้วย fixtures/fakes และทำ smoke test ของ Google/Alpaca เมื่อมี credentials; แยกผลที่ตรวจจริงกับผลจำลองในคู่มือ และตรวจบริการจริงให้ครบก่อนเปิดใช้งานออนไลน์
- [ ] ทดสอบ audit retention 90 วันด้วย maintenance role และวิธี schedule จริงที่เลือก พร้อมจำกัดขนาด/ปริมาณ logs
- [ ] เพิ่มข้อมูลเดโม, architecture/ER diagram, API usage และ trade-offs ของ session, decimal, Supabase และ locking ใน README พร้อมลิงก์ ADRs; ตรวจว่า ADR ครอบคลุมการตัดสินใจที่เกิดระหว่าง milestones 1–8
- [ ] ตั้ง backend required checks/branch protection และ CI badge เมื่อมี GitHub repository และสิทธิ์พร้อม

**ผ่านเมื่อ:** API ขอบเขต backend ครบและผ่าน tests, เปิดเดโม/Swagger ตามคู่มือได้โดยไม่มี frontend, generated docs ตรงกับ API และไม่มีปัญหาที่ขวางการเชื่อมต่อ frontend จึงเริ่ม milestone 10

### 10. เริ่ม Next.js และเชื่อม API ที่ผ่านการตรวจรับแล้ว

**ไฟล์หลัก:** `web/` รวม `web/src/app/`, `web/src/components/`, `web/src/lib/`, `web/Dockerfile`, `docker-compose.yml`, `docker-compose.override.yml`, `.github/workflows/ci.yml`, `README.md`

- [ ] สร้าง Next.js skeleton และ pin Node.js/dependencies เป็นครั้งแรกในขั้นนี้ พร้อม web Dockerfile แบบ multi-stage, service web และ web dev configuration
- [ ] เพิ่ม web lint/typecheck/build, web image build และ npm cache ใน CI พร้อมเพิ่ม required checks เมื่อพร้อมใช้งานจริง
- [ ] ออกแบบหน้าจอและ implement signup/login/reset, รายการพอร์ต, รายละเอียดพอร์ต และฟอร์มซื้อขายจาก API ที่ตรวจแล้ว
- [ ] เชื่อม Go API ผ่าน same-origin routing; แสดง validation errors, loading/empty/error states และค่าตัวเลขที่ไม่ผ่าน float calculation
- [ ] แสดงต้นทุน/P&L พร้อมที่มาและเวลาราคา รวมราคาเก่า/ไม่ครบ/เดโม และข้อจำกัด corporate actions
- [ ] ทดสอบ browser flow สมัคร → ยืนยันอีเมล → login → สร้างสองพอร์ต → ซื้อ/ขาย → ดูผล → logout และ reset password พร้อม expired session/CSRF/Google redirect และ responsive layout
- [ ] ตรวจ Swagger UI ผ่าน reverse proxy และอัปเดต README ให้คำสั่ง Compose เปิดทั้ง web/API ได้; ไม่ทำหน้าหุ้นรายตัว/กราฟในเฟสนี้ตามที่ยืนยันใน milestone 0
- [ ] หากการเชื่อม frontend พบ API bug ให้แก้ backend พร้อม regression test/annotations/generated docs ไม่สร้าง business logic ซ้ำใน frontend

**ผ่านเมื่อ:** ผู้ใช้ทำ flow หลักผ่าน UI ได้, web checks และ browser verification ผ่าน, backend checks ยังผ่าน และ Compose เปิดทั้งระบบได้ตามคู่มือ

### 11. ตรวจทั้งระบบและ deploy + Supabase compatibility + recovery

**ไฟล์หลัก:** deployment configuration/runbook, environment examples และ deployment workflow เมื่อเลือก hosting แล้ว

- [ ] ตรวจ release จาก clean environment พร้อม backend/frontend CI และ end-to-end flow ทั้งระบบก่อน deploy
- [ ] เลือก hosting web/API ที่รักษาเงื่อนไขฟรี ตรวจ same-origin routing และการส่งอีเมลจริง
- [ ] ตั้ง Supabase Database, private schema/Data API settings, roles และ TLS โดยไม่ใช้ Supabase Auth
- [ ] รัน migrations ด้วย migration role และตรวจ API queries/permissions ด้วย runtime role; CI ปกติยังใช้ local PostgreSQL
- [ ] ตั้ง Google redirect URI, Alpaca และอีเมลจริงผ่าน secrets ของแต่ละ environment แล้วตรวจ flow จริงครบก่อนเปิดให้ใช้
- [ ] กำหนด docs/Try it out config และ public base URL ให้ตรง deployment พร้อมตรวจว่า /docs โหลด assets/spec ได้
- [ ] ตรวจ /healthz, /readyz และ business smoke test; ทดลอง backup/restore ใน DB แยกและระบุวิธีกู้คืนเมื่อ deploy ล้มเหลว
- [ ] ทำ GHCR image tag ตาม SHA และ CD ขั้นต่อไปเมื่อถึงขอบเขตนั้น โดยใช้ migrations ที่เข้ากันได้กับเวอร์ชันก่อนหน้า ไม่สมมติว่าการ rollback image จะ rollback DB ด้วย

**ผ่านเมื่อ:** ระบบออนไลน์ผ่าน flow หลัก, ไม่มีข้อมูลเปิดเผยผ่าน Data API, runtime role ทำงานได้จริง และมีขั้นตอน backup/restore/rollback ที่ตรวจแล้วภายใต้งบฟรี

## วิธีเดินงานในแต่ละ milestone

- แตกเป็นแผนย่อยของ subsystem ที่กำลังทำ โดยระบุไฟล์จริง, signatures, test cases และคำสั่งตรวจตาม design ที่ยืนยันแล้ว
- สำหรับ business logic/security เขียน test ที่พิสูจน์พฤติกรรมก่อน implementation และใช้ PostgreSQL จริงเมื่อทดสอบ transaction/locking
- ทำ handler/DTO/audit/Swagger annotations/generated docs/contract tests ในส่วนเดียวกัน อธิบายผลและ trade-off เมื่อจบแต่ละ milestone และ commit เป็นชุดที่ทบทวนได้เมื่อมี Git repository
- เริ่มที่ milestone 0 ตามลำดับ; การยืนยันลำดับงานยังไม่แทนการยืนยันแบบระบบก่อนเขียนโค้ด
- Milestones 0–9 เป็น backend-first และ milestone 10 จึงเริ่ม frontend; ไม่ใช้ความจำเป็นในการทดลอง API เป็นเหตุผลสร้าง Next.js ล่วงหน้า
- หน้าหุ้นรายตัว/กราฟ (เริ่มด้วย daily bars แบบ delayed), live/extended-hours, dividends/splits และฟีเจอร์เพิ่มเติมนอกขอบเขตที่ยืนยัน เก็บไว้หลังเฟส 1
