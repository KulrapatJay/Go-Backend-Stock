# Milestone 6 — หลายพอร์ต + ownership + HTTP handlers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** เพิ่ม `POST/GET /api/v1/portfolios`, `GET/PUT/DELETE /api/v1/portfolios/{portfolio_id}` ให้ผู้ใช้ที่ login สร้าง ดู เปลี่ยนชื่อ/คำอธิบาย และลบพอร์ตว่างของตัวเองได้ โดยทุกเส้นทางตรวจ ownership, PUT/DELETE ใช้ ETag/If-Match และทุกการเปลี่ยนแปลงมี audit ใน transaction เดียวกัน

**Architecture:** core ได้ `domain.PortfolioInput`, port `PortfolioService` และ `service.Portfolios` (use case แยกจาก `Auth`); repositories ของพอร์ตมีครบตั้งแต่ milestone 3c จึงไม่มี migration แต่เพิ่ม query `PortfolioExists` หนึ่งตัวเพื่อแยก "ไม่มีพอร์ตนี้" กับ "พอร์ตของคนอื่น" สำหรับ audit `security.access_denied`; HTTP adapter สร้าง middleware chain ของ session/CSRF ครั้งเดียวแล้วใช้ร่วมกันระหว่าง auth กับ portfolio routes

**Tech Stack:** Go 1.27 (go.mod `go 1.27.1`), Fiber v3.5.0, pgx v5.11.0, sqlc v1.31.1, PostgreSQL 17.11, swaggo/swag v1.16.6 (Swagger 2.0), golangci-lint v2.14.0 — ไม่มี dependency ใหม่

**Spec:** [phase 1 spec](../specs/2026-10-05-phase-1-design.md) หัวข้อ 7 (portfolios, unique `(user_id, lower(name))`, version), 8 (`PortfolioService`, `PortfolioRepository`), 9 (lock + version), 11 (endpoints, error table, ETag/If-Match, lists) และ 14 (tests ของ milestone 6–7) กับ [AGENTS.md](../../../AGENTS.md) หัวข้อ "ผู้ใช้หนึ่งคนสร้างได้หลายพอร์ต", "ความแม่นยำของตัวเลขและความสอดคล้องของข้อมูล", ตาราง audit และ [ADR-0009](../../adr/0009-concurrency-row-lock-and-optimistic-locking.md)

## Global Constraints

- core (`internal/core/**` รวม `_test.go`) import ได้เฉพาะ standard library, `github.com/shopspring/decimal`, `github.com/google/uuid`, `golang.org/x/crypto/argon2` และ `go-backend-stock/internal/core/...`
- ไม่มี migration: ใช้ตาราง `portfolios`, `idempotency_keys`, `transactions` และ event types `portfolio.created`/`portfolio.updated`/`portfolio.deleted`/`security.access_denied` ที่มีอยู่แล้ว; query ใหม่เขียนใน `internal/adapter/postgres/queries/portfolios.sql` แล้ว `make sqlc` และ commit ไฟล์ generated
- Endpoints (ใต้ `/api/v1`, ต้องมี session ทุกตัว; unsafe methods ต้องมี CSRF + Origin): `POST /portfolios` `{name,description?}` → 201 Portfolio + `ETag`; `GET /portfolios?limit&cursor` → 200 `{data:[Portfolio],next_cursor:string|null}`; `GET /portfolios/{portfolio_id}` → 200 Portfolio + `ETag`; `PUT /portfolios/{portfolio_id}` + `If-Match` `{name,description?}` → 200 Portfolio + `ETag` ใหม่; `DELETE /portfolios/{portfolio_id}` + `If-Match` → 204
- Portfolio JSON = `{id,name,description,currency:"USD",version,created_at,updated_at}`; `description` ไม่ส่ง = `""`; เวลาเป็น date-time UTC; `version` เป็น integer
- ETag = `"<version>"` (strong) บน GET/POST/PUT ของพอร์ตเดี่ยว; รายการไม่มี ETag
- If-Match: ไม่ส่ง → 428 `precondition_required`; ต้องเป็น `"<จำนวนเต็มบวก>"` ตรงตัวเท่านั้น (`*`, `W/"3"`, หลายค่า, ไม่มี quote → 400 `invalid_input` field `If-Match`); version ไม่ตรง → 412 `version_conflict` โดยไม่เขียนข้อมูลและไม่เขียน audit
- ชื่อพอร์ตซ้ำแบบไม่สนตัวพิมพ์ภายในผู้ใช้เดียว → 409 `portfolio_name_conflict` (DB เป็นผู้ตัดสิน); ต่างผู้ใช้ใช้ชื่อเดียวกันได้; ลบพอร์ตที่มี transactions → 409 `portfolio_not_empty`
- พอร์ตของผู้อื่นและพอร์ตที่ไม่มีอยู่ตอบ 404 `not_found` แบบเดียวกันทุก method (body เหมือนกันยกเว้น `request_id`); ห้ามเชื่อ `user_id` จาก request
- ทุกการเปลี่ยนแปลง (create/update/delete) เขียน audit ใน DB transaction เดียวกับข้อมูล; เขียน audit ไม่ได้ → rollback และคืน 500 `audit_write_failed`
- Update/Delete: `LockOwned` (row lock ของพอร์ต) ก่อนตรวจ version แล้วจึงเขียน — lock กันการเขียนพร้อมกัน, version กันการเขียนทับจากข้อมูลเก่า
- Lists: limit ค่าเริ่มต้น 20 สูงสุด 100 (`domain.ParsePage`), เรียง `created_at DESC, id DESC`, cursor ทึบที่ adapter postgres ออกให้; cursor ผิด → 400 `invalid_input`
- ห้ามแก้ `.env.example` และ `.env`
- **`make -s test-integration` ล้างทุกตาราง schema `app` ใน dev DB หลังรันทุกครั้ง (`clean-test-data`)** — ข้อมูลที่ผู้ใช้สร้างเองหายด้วย; ใช้ได้ตามที่ผู้ใช้ตกลงแล้ว
- ต้องเห็น test ล้มก่อนแล้วจึงเขียนโค้ดทุก task; จบ task ด้วย `gofmt -l internal cmd` ที่ไม่พิมพ์อะไร, `go vet ./...` และ `make lint` ผ่าน
- task ที่แก้ annotation ของ handler/DTO ต้องรัน `make swagger` แล้ว commit `docs/api/` ในชุดเดียวกัน ห้ามแก้ไฟล์ generated ด้วยมือ
- integration tests ใช้ PostgreSQL จริงผ่าน `make -s test-integration` เท่านั้นและต้องไม่ SKIP; ใช้อีเมล/IP สุ่มต่อ test
- Docker: ใช้ได้เฉพาะ `docker compose ps` และ `docker compose config --quiet`; ห้ามเรียก API ที่ผู้ใช้รันอยู่
- commit message ไม่มี `Co-Authored-By` หรือชื่อ Claude; commit ในเครื่องเท่านั้น ห้าม push; `git add` เฉพาะไฟล์ของ task

## การตัดสินใจที่แผนนี้เสนอ (รอผู้ใช้ยืนยันตอนรีวิวแผน)

1. **`security.access_denied` เขียนเมื่อ PUT/DELETE ไปที่พอร์ตที่มีอยู่จริงแต่เป็นของคนอื่น** (actor = ผู้เรียก, target `portfolio` + ID, reason `not_owner`) แต่ตอบ 404 เหมือนพอร์ตที่ไม่มีอยู่; GET ไม่เขียน audit เพราะ AGENTS.md ห้าม audit ทุก GET และผู้ใช้ที่ login อยู่จะทำให้ตาราง audit โตได้ไม่จำกัดผ่านการอ่าน — ต้องเพิ่ม query `PortfolioExists(id)` ที่ไม่กรอง owner (คืนแค่ bool) ซึ่งเรียกเฉพาะเส้นทางที่ไม่พบพอร์ตของผู้เรียก
2. **If-Match เข้มงวด:** รับเฉพาะ `"<n>"` ตาม spec ("ไม่รองรับ `If-Match: *`"); รูปแบบอื่นเป็น 400 ไม่ใช่ 428 เพราะ client ส่งมาแล้วแต่ผิดรูป
3. **PUT ที่ค่าไม่เปลี่ยนยังเพิ่ม version และเขียน `portfolio.updated`** — ง่ายและคาดเดาได้; ทางเลือกอื่นคือเปรียบเทียบค่าแล้วไม่เขียน ซึ่งทำให้ ETag เดิมยังใช้ได้แต่เพิ่มเงื่อนไข
4. **Audit metadata ของพอร์ต:** `ip`, `user_agent` เหมือน auth และ `portfolio.updated` เพิ่ม `changed` = รายการ field ที่ค่าเปลี่ยน (`name`, `description`) — ไม่เก็บชื่อ/คำอธิบายจริงเพราะเป็นข้อความอิสระของผู้ใช้
5. **ลบพอร์ตลบ idempotency keys ที่ค้างของพอร์ตนั้นใน transaction เดียวกัน** — keys อ้าง portfolio ด้วย FK RESTRICT; ถ้าพอร์ตยังมี transactions การลบทั้งหมด rollback รวมทั้ง keys
6. **ไม่จำกัดจำนวนพอร์ตต่อผู้ใช้และไม่มี rate limit ของ endpoints พอร์ตในเฟส 1** — spec ไม่ได้กำหนด; ทุก endpoint ต้อง login อยู่แล้ว
7. **`portfolio_id` ที่ไม่ใช่ UUID → 400 `invalid_input`** ตาม `domain.ParseID` ที่ใช้ทั้งระบบ (UUID ที่ถูกรูปแต่ไม่พบ → 404)
8. **รวม audit helper ให้ใช้ร่วมกัน:** ย้ายการสร้าง `domain.AuditEvent` และการ append ที่ห่อ `audit_write_failed` ออกจาก method ของ `*Auth` เป็นฟังก์ชันของ package `service` ให้ `Auth` และ `Portfolios` ใช้ตัวเดียวกัน

## Review Focus

1. **PUT พร้อมกันด้วย If-Match เดียวกัน** — ต้องได้ 200 หนึ่งครั้ง 412 หนึ่งครั้ง, version เพิ่มครั้งเดียว และ `portfolio.updated` แถวเดียว — Task 6 `TestConcurrentPortfolioUpdatesWithOneVersionWinOnce`
2. **เดา ID พอร์ตของคนอื่น** — GET/PUT/DELETE ได้ 404 ที่ body เหมือนกับ UUID สุ่ม และข้อมูลเจ้าของไม่เปลี่ยน — Task 6 `TestAnotherUsersPortfolioLooksMissing`
3. **เปลี่ยนชื่อเป็นชื่อเดิมของตัวเองแต่ต่างตัวพิมพ์** (`Long` → `long` บนพอร์ตเดียวกัน) ต้องสำเร็จ แต่เป็นชื่อของอีกพอร์ตตัวเองต่างตัวพิมพ์ต้อง 409 และ version ไม่เปลี่ยน — Task 5 `TestRenamePortfolioCaseRules`
4. **ลบพอร์ตที่มี transaction** — 409 และไม่มีอะไรถูกลบรวมทั้ง idempotency keys — Task 5 `TestDeletePortfolioWithTransactionsIsRefused`
5. **หน้ารายการ** — 25 พอร์ตแบ่งได้ 20 + 5 ไม่ซ้ำไม่ตก, cursor เสียได้ 400, ผู้ใช้อื่นไม่เห็นพอร์ตเรา — Task 4 `TestListPortfoliosPagesNewestFirst`

---

## File Structure

| ไฟล์ | หน้าที่ | Task |
| --- | --- | --- |
| `internal/core/domain/portfolio.go` (+test), `internal/core/port/portfolio_service.go`, `internal/core/service/audit.go` | `PortfolioInput`, `PortfolioService`, audit helper ร่วม | 1 |
| `internal/adapter/postgres/queries/portfolios.sql`, `sqlc/`, `portfolios.go` (+test), `internal/core/port/portfolio.go` | `PortfolioRepository.Exists` | 2 |
| `internal/core/service/portfolio.go`, `portfolio_audit.go` (+tests) | use cases ของพอร์ต | 3 |
| `internal/adapter/http/server.go`, `auth.go`, `meta.go`, `portfolio.go`, `portfolio_dto.go`, `etag.go`, `portfolio_harness_test.go`, `portfolio_test.go`, contract test, `docs/api/` | chain ร่วม, create/list/get | 4 |
| `internal/adapter/http/portfolio.go`, `etag.go` (+test), `portfolio_update_test.go`, contract test, `docs/api/` | PUT/DELETE + If-Match | 5 |
| `internal/adapter/http/portfolio_isolation_test.go` | ผู้ใช้สองคน, การเดา ID, การแข่งกัน, audit rollback | 6 |
| `cmd/api/wire.go` (+test), `cmd/api/main.go`, `README.md`, roadmap, spec | ประกอบ dependency, คู่มือ, สถานะ | 7 |

---

### Task 1: `PortfolioInput`, port `PortfolioService` และ audit helper ร่วม

**Files:**
- Modify: `internal/core/domain/portfolio.go`, `internal/core/domain/portfolio_entities_test.go`, `internal/core/service/audit.go` (+ callers in `internal/core/service/*.go`)
- Create: `internal/core/port/portfolio_service.go`

**Interfaces:**
- Produces:
  - `domain.PortfolioInput{Name PortfolioName; Description PortfolioDescription}` และ `domain.ParsePortfolioInput(name, description string) (PortfolioInput, error)` — รวม error ของทั้งสอง field ไม่ได้ (คืนตัวแรกที่ผิดตามลำดับ name แล้ว description เหมือน parser อื่นใน domain)
  - `port.PortfolioService` ตาม spec หัวข้อ 8: `Create(ctx, p domain.Principal, in domain.PortfolioInput, meta domain.RequestMeta) (domain.Portfolio, error)`, `List(ctx, p, page domain.Page) (domain.PortfolioPage, error)`, `Get(ctx, p, id domain.ID) (domain.Portfolio, error)`, `Update(ctx, p, id domain.ID, expected domain.Version, in domain.PortfolioInput, meta) (domain.Portfolio, error)`, `Delete(ctx, p, id domain.ID, expected domain.Version, meta) error`
  - ใน `service/audit.go`: `func newAuditEvent(clock port.Clock, meta domain.RequestMeta, s auditSpec, metadata any) (domain.AuditEvent, error)` และ `func appendAuditEvent(ctx, tx port.TxRepositories, e domain.AuditEvent) error` (ห่อ `errAuditWriteFailed`); `(*Auth).newAuditEvent/appendAudit/recordFailure` เรียกสองตัวนี้โดยพฤติกรรมเดิมไม่เปลี่ยน (metadata ของ auth ยังเป็น `auditMetadata` เดิม)

- [ ] **Step 1: เขียน failing tests**
  - `TestParsePortfolioInput` — `("  Long term ", "")` ได้ name `"Long term"` description `""`; `("", "x")` → `invalid_input` ที่ message มี `name`; `("A", strings.Repeat("x", 1001))` → `invalid_input` ที่ message มี `description`
  - tests ของ audit ที่มีอยู่ (`TestAuditMetadataKeepsOnlyTheAllowlist` ฯลฯ) ต้องผ่านเหมือนเดิมหลัง refactor — ไม่ต้องเขียนใหม่

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/core/... -count=1` → FAIL (`ParsePortfolioInput` undefined)

- [ ] **Step 3: เขียนโค้ดตาม Interfaces** (refactor audit ไม่เปลี่ยน output ของ event ใดๆ)

- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/... -count=1 -race && make lint`

- [ ] **Step 5: Commit** — `git commit -m "feat(domain): add the portfolio input, the portfolio service port and a shared audit helper"`

---

### Task 2: `PortfolioRepository.Exists`

**Files:**
- Modify: `internal/adapter/postgres/queries/portfolios.sql`, `internal/adapter/postgres/sqlc/` (generated), `internal/adapter/postgres/portfolios.go`, `internal/adapter/postgres/portfolios_test.go`, `internal/core/port/portfolio.go`, fakes ของ `PortfolioRepository` ใน tests ที่มีอยู่ (ถ้ามี)

**Interfaces:**
- Produces: `PortfolioRepository.Exists(ctx context.Context, portfolioID domain.ID) (bool, error)` — "พอร์ต ID นี้มีอยู่ไม่ว่าของใคร" ใช้เฉพาะตัดสินว่าจะเขียน `security.access_denied`; comment ต้องบอกว่าห้ามใช้ตัดสินสิทธิ์อ่าน/เขียน
- SQL: `-- name: PortfolioExists :one` → `SELECT EXISTS (SELECT 1 FROM app.portfolios WHERE id = $1);` (runtime role มี SELECT บน portfolios อยู่แล้ว ไม่ต้องแก้ grants)

- [ ] **Step 1: เขียน failing test** — `TestPortfolioExistsIgnoresTheOwner` ใน `portfolios_test.go`: พอร์ตของ user A → `Exists` = true (เรียกโดยไม่มี owner); UUID สุ่ม → false; หลังลบพอร์ต → false
- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `make -s test-integration` → compile fail
- [ ] **Step 3: เขียน query, `make sqlc`, method และ port**
- [ ] **Step 4: รัน test ให้ผ่าน** — `make sqlc && git status --short internal/adapter/postgres/sqlc; go vet ./... && make -s test-integration && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(postgres): tell whether a portfolio exists for any owner"`

---

### Task 3: `service.Portfolios`

**Files:**
- Create: `internal/core/service/portfolio.go`, `internal/core/service/portfolio_test.go`

**Interfaces:**
- Consumes: Task 1–2; `port.Repositories.Portfolios()`, `port.TxRepositories.Portfolios()/Idempotency()/Audit()`, `NowForStorage`, `domain.NewID`
- Produces:
  - `type PortfolioDeps struct{ Store port.Repositories; UnitOfWork port.UnitOfWork; Clock port.Clock; Logger *slog.Logger }`, `func NewPortfolios(d PortfolioDeps) (*Portfolios, error)` (ทุก field ห้ามเป็น nil), `var _ port.PortfolioService = (*Portfolios)(nil)`
  - `type portfolioMetadata struct{ IP string \`json:"ip,omitempty"\`; UserAgent string \`json:"user_agent,omitempty"\`; Changed []string \`json:"changed,omitempty"\` }`

พฤติกรรม (ลำดับคือข้อกำหนด):
- ทุก method: `p.IsZero()` → `domain.ErrUnauthenticated`
- **Create:** สร้าง Portfolio (`ID` ใหม่, `UserID = p.UserID`, `Currency USD`, `Version 1`, `CreatedAt = UpdatedAt = NowForStorage`) → transaction: `Portfolios().Insert` → audit `portfolio.created` (actor = user, session = p.SessionID, target `portfolio`/ID) → คืนพอร์ต; ชื่อซ้ำคืน `ErrPortfolioNameConflict` (transaction rollback ไม่มี audit)
- **List / Get:** อ่านผ่าน `Store.Portfolios().ListOwned/GetOwned(p.UserID, …)` นอก transaction; ไม่เขียน audit
- **Update:** transaction: `LockOwned(p.UserID, id)` → `ErrNotFound` ให้ไปเส้นทาง denied (ด้านล่าง) → `locked.Version != expected` → `ErrVersionConflict` → `UpdateOwned` ด้วย name/description ใหม่ + `UpdatedAt = now` + `Version = expected` → audit `portfolio.updated` พร้อม `changed` (เทียบกับค่าใน `locked`; ลำดับ `name`, `description`; ว่างได้)
- **Delete:** transaction: `LockOwned` → denied path ถ้าไม่พบ → version ไม่ตรง → `ErrVersionConflict` → `Idempotency().DeleteForPortfolio(p.UserID, id)` → `Portfolios().DeleteOwnedEmpty(p.UserID, id)` (`ErrPortfolioNotEmpty` ส่งกลับตรงๆ ให้ rollback) → audit `portfolio.deleted`
- **Denied path (Update/Delete เมื่อ `LockOwned` คืน `ErrNotFound`):** หลัง transaction นั้นจบ เรียก `Store.Portfolios().Exists(id)`; ถ้า true เขียน `security.access_denied` (actor = user, session, target `portfolio`/ID, outcome `failure`, reason `not_owner`) ใน transaction ของตัวเอง; ไม่ว่าแบบไหนคืน `domain.ErrNotFound`; เขียน audit ไม่ได้ → คืน error นั้น (ไม่กลืน)

- [ ] **Step 1: เขียน failing tests** (fake repositories/UoW ใน `fakes_test.go`; ขยาย fake เท่าที่จำเป็น):
  - `TestCreatePortfolioWritesItsAudit` — event 1 แถว `portfolio.created`, actor = user, session = p.SessionID, `Version` 1, `Currency` USD
  - `TestCreatePortfolioNameConflictWritesNoAudit` — Insert คืน `ErrPortfolioNameConflict` → error เดียวกัน, ไม่มี event ที่ commit
  - `TestPortfolioUseCasesNeedACaller` — principal ศูนย์ → `ErrUnauthenticated` ทุก method และไม่แตะ repository
  - `TestUpdatePortfolioChecksTheVersionUnderTheLock` — `LockOwned` คืน version 3, expected 2 → `ErrVersionConflict`, ไม่เรียก `UpdateOwned`, ไม่มี audit
  - `TestUpdatePortfolioAuditsTheChangedFields` — เปลี่ยนแค่ description → metadata `changed` = `["description"]`; ไม่เปลี่ยนอะไร → ไม่มี key `changed` แต่ยังมี event
  - `TestDeletePortfolioRemovesIdempotencyKeysFirst` — ลำดับการเรียก fake: `LockOwned`, `DeleteForPortfolio`, `DeleteOwnedEmpty`, audit
  - `TestUpdateOrDeleteOfAnotherUsersPortfolioIsDeniedAndAudited` — `LockOwned` → `ErrNotFound`, `Exists` → true: คืน `ErrNotFound` และ event `security.access_denied` reason `not_owner`; `Exists` → false: `ErrNotFound` และไม่มี event

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/core/service/ -count=1` → FAIL (`NewPortfolios` undefined)
- [ ] **Step 3: เขียนโค้ด**
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/... -count=1 -race && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(service): create, list, read, update and delete portfolios with their audit"`

---

### Task 4: chain ร่วม และ `POST /portfolios`, `GET /portfolios`, `GET /portfolios/{id}`

**Files:**
- Create: `internal/adapter/http/portfolio.go`, `internal/adapter/http/portfolio_dto.go`, `internal/adapter/http/etag.go`, `internal/adapter/http/portfolio_harness_test.go`, `internal/adapter/http/portfolio_test.go`
- Modify: `internal/adapter/http/server.go`, `internal/adapter/http/auth.go`, `internal/adapter/http/meta.go`, `internal/adapter/http/auth_harness_test.go`, `internal/adapter/http/auth_contract_test.go` (หรือสร้าง `portfolio_contract_test.go` ด้วยรูปแบบเดียวกัน), `docs/api/`

**Interfaces:**
- Consumes: Task 3 `port.PortfolioService`
- Produces:
  - `Options.Portfolios PortfolioOptions` โดย `type PortfolioOptions struct{ Service port.PortfolioService }` — nil Service ไม่ลงทะเบียน route พอร์ต; route พอร์ตต้องมี `Options.Auth.Service` (ถ้าไม่มี auth แต่มีพอร์ต ให้ `NewServer` panic พร้อมข้อความชัดเจน เพราะเป็น wiring bug)
  - `type authChain struct{ session, csrf fiber.Handler; clientIP *ClientIPResolver }` สร้างครั้งเดียวใน `registerRoutes` แล้วส่งให้ `registerAuthRoutes` และ `registerPortfolioRoutes` (พฤติกรรมของ auth routes เดิมไม่เปลี่ยน)
  - `func requestMeta(c fiber.Ctx, ip *ClientIPResolver) (domain.RequestMeta, error)` แทน method ของ `authHandlers` (handler auth เรียกตัวนี้)
  - DTO: `PortfolioRequest{Name string \`json:"name"\`; Description *string \`json:"description"\`}` (name required); `PortfolioResponse{ID, Name, Description, Currency string; Version int32; CreatedAt, UpdatedAt time.Time}` ตาม JSON ใน Global Constraints; `PortfolioPageResponse{Data []PortfolioResponse \`json:"data"\`; NextCursor *string \`json:"next_cursor"\`}` (ส่ง `[]` ไม่ใช่ `null` เมื่อว่าง)
  - `func setETag(c fiber.Ctx, v domain.Version)` → `ETag: "<n>"`
  - routes: `POST /api/v1/portfolios` (session, RequireAuth, csrf), `GET /api/v1/portfolios` (session, RequireAuth), `GET /api/v1/portfolios/:portfolio_id` (session, RequireAuth); ทุก response ของพอร์ตตั้ง `Cache-Control: no-store`
  - Swagger tag ใหม่ `Portfolios`; operationIds `createPortfolio`, `listPortfolios`, `getPortfolio`; ระบุ 401/403/404/409/400 ตามที่ handler คืนได้ และ header `ETag`
  - harness: `withPortfolios()` option ของ `newAuthHarness` (สร้าง `service.NewPortfolios` บน store/uow ของ harness); browser helpers ใช้ `signedIn(t, h) (*browser, userRow)` ที่มีอยู่แล้วใน `google_link_test.go` (สมัคร + ยืนยัน + login) — ถ้าจะใช้ร่วมหลายไฟล์ให้ย้ายไป `auth_harness_test.go`, `createPortfolio(name, description string) (response, []byte)`, `getPortfolio(id string)`, `listPortfolios(query string)`, `decodePortfolio(t, body) PortfolioResponse`

- [ ] **Step 1: เขียน failing tests** ใน `portfolio_test.go`:
  - `TestCreatePortfolio` — 201, body ตรง JSON shape (currency `USD`, version 1, description `""` เมื่อไม่ส่ง), `ETag: "1"`, `Cache-Control: no-store`, audit `portfolio.created` actor = user; ชื่อ `"  Long term  "` ถูกเก็บเป็น `"Long term"`
  - `TestCreatePortfolioNeedsSessionAndCSRF` — ไม่ login → 401; login แต่ไม่มี CSRF → 403 `csrf_failed`
  - `TestCreatePortfolioNameRules` — ชื่อซ้ำต่างตัวพิมพ์ในผู้ใช้เดียว → 409 `portfolio_name_conflict`; ผู้ใช้อื่นใช้ชื่อเดียวกัน → 201; ชื่อว่าง → 400 `invalid_input`
  - `TestGetPortfolio` — 200 + `ETag: "1"`; UUID สุ่ม → 404; `portfolio_id=abc` → 400
  - `TestListPortfoliosPagesNewestFirst` — 25 พอร์ต (ใช้ `h.clock.Advance(time.Second)` ระหว่างสร้างให้ `created_at` ต่างกัน): หน้าแรก 20 ตัวใหม่สุดก่อน + `next_cursor`, หน้าสอง 5 ตัว + `next_cursor: null`, รวม 25 ID ไม่ซ้ำ; `limit=101` → 400; `cursor=garbage` → 400; ผู้ใช้ที่ไม่มีพอร์ต → `{"data":[],"next_cursor":null}`; พอร์ตของผู้ใช้อื่นไม่ปรากฏ
  - contract table: เพิ่ม `{"post","/api/v1/portfolios",[201,400,401,403,409,500,503]}`, `{"get","/api/v1/portfolios",[200,400,401,503]}`, `{"get","/api/v1/portfolios/{portfolio_id}",[200,400,401,404,503]}`

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `make -s test-integration` → compile fail หรือ 404 ที่ route
- [ ] **Step 3: เขียนโค้ด** (refactor chain โดย auth tests เดิมทั้งหมดต้องผ่าน)
- [ ] **Step 4: รัน test ให้ผ่าน และ generate Swagger** — `docker compose ps; gofmt -l internal cmd; go vet ./... && make swagger && go test ./internal/... -count=1 -race && make -s test-integration && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(http): create, list and read portfolios behind the shared session and CSRF chain"`

---

### Task 5: `PUT` และ `DELETE /portfolios/{id}` กับ If-Match

**Files:**
- Create: `internal/adapter/http/etag_test.go`, `internal/adapter/http/portfolio_update_test.go`
- Modify: `internal/adapter/http/etag.go`, `internal/adapter/http/portfolio.go`, contract test, `docs/api/`

**Interfaces:**
- Consumes: Task 4
- Produces:
  - `func parseIfMatch(c fiber.Ctx) (domain.Version, error)` — ไม่มี header → `domain.NewError(domain.CodePreconditionRequired, "If-Match is required; send the ETag from the last read")`; มีแต่ไม่ใช่ `"<n>"` ตรงตัว (`n` = `domain.ParseVersion`) → `invalid_input` field `If-Match`
  - routes: `PUT /api/v1/portfolios/:portfolio_id` และ `DELETE /api/v1/portfolios/:portfolio_id` (session, RequireAuth, csrf); PUT ตอบ 200 + `ETag` ใหม่; DELETE ตอบ 204
  - operationIds `updatePortfolio`, `deletePortfolio`; Swagger ระบุ `If-Match` เป็น header parameter required, 412/428 และ `ETag` response header
  - browser helpers `updatePortfolio(id, ifMatch string, name, description string)`, `deletePortfolio(id, ifMatch string)` (ifMatch `""` = ไม่ส่ง header)

- [ ] **Step 1: เขียน failing tests**
  - `etag_test.go` `TestParseIfMatch` (table): `"3"` (มี quote) → 3; ไม่มี header → `precondition_required`; `3`, `*`, `W/"3"`, `"3", "4"`, `"0"`, `"-1"`, `"x"`, `""` → `invalid_input`
  - `portfolio_update_test.go`:
    - `TestUpdatePortfolio` — PUT `If-Match: "1"` → 200, version 2, `ETag: "2"`, `updated_at` ใหม่, audit `portfolio.updated` metadata `changed` ถูกต้อง
    - `TestUpdatePortfolioPreconditions` — ไม่ส่ง If-Match → 428 `precondition_required`; `If-Match: "1"` หลังจาก version เป็น 2 แล้ว → 412 `version_conflict`; `If-Match: *` → 400; ทุกกรณีข้อมูลและ version ไม่เปลี่ยนและไม่มี audit ของคำขอนั้น
    - `TestRenamePortfolioCaseRules` — พอร์ต `Long` → เปลี่ยนเป็น `long` (พอร์ตเดียวกัน) → 200; มีอีกพอร์ต `Short` → เปลี่ยน `long` เป็น `SHORT` → 409 `portfolio_name_conflict` และ version คงเดิม
    - `TestDeleteEmptyPortfolio` — DELETE `If-Match: "1"` → 204; GET ต่อมา → 404; audit `portfolio.deleted`; DELETE ซ้ำ → 404
    - `TestDeletePortfolioPreconditions` — 428 และ 412 แบบเดียวกับ PUT; พอร์ตยังอยู่
    - `TestDeletePortfolioWithTransactionsIsRefused` — ใส่ instrument + transaction ผ่าน `h.store` (`Instruments().Upsert`, `Transactions().Insert` ใน `WithinTransaction`) และ idempotency key ผ่าน `Idempotency().Save` → DELETE → 409 `portfolio_not_empty`; พอร์ต, transaction และ idempotency key ยังอยู่ (ตรวจด้วย admin SQL); ไม่มี audit `portfolio.deleted`
    - `TestDeletePortfolioClearsLeftoverIdempotencyKeys` — พอร์ตไม่มี transaction แต่มี idempotency key → 204 และ key หายไป
  - contract table: `{"put","/api/v1/portfolios/{portfolio_id}",[200,400,401,403,404,409,412,428,500,503]}`, `{"delete","/api/v1/portfolios/{portfolio_id}",[204,400,401,403,404,409,412,428,500,503]}`

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/adapter/http/ -run TestParseIfMatch -count=1` → compile fail; `make -s test-integration` → 405/404
- [ ] **Step 3: เขียนโค้ด**
- [ ] **Step 4: รัน test ให้ผ่าน และ generate Swagger** — คำสั่งเดียวกับ Task 4 Step 4
- [ ] **Step 5: Commit** — `git commit -m "feat(http): update and delete portfolios with If-Match and ETag"`

---

### Task 6: ผู้ใช้สองคน, การเดา ID, การแข่งกัน และ audit rollback

task นี้เพิ่มเฉพาะ test: ถ้า test ใดล้มแสดงว่าโค้ดของ Task 3–5 ผิด ให้หาสาเหตุ (superpowers:systematic-debugging) แล้วแก้โค้ดใน commit `fix(…): …` แยก ไม่แก้ test ให้ผ่าน

**Files:**
- Create: `internal/adapter/http/portfolio_isolation_test.go`

- [ ] **Step 1: เขียน tests**
  - `TestAnotherUsersPortfolioLooksMissing` — A สร้างพอร์ต; B (login แยก) ทำ GET/PUT (`If-Match: "1"`)/DELETE (`If-Match: "1"`) ที่ ID ของ A และที่ UUID สุ่ม: ทุกคำขอ 404 และ body (ตัด `request_id` ด้วย `stripJSONField`) ของ ID ของ A เท่ากับของ UUID สุ่มต่อ method; พอร์ตของ A ยัง version 1 ชื่อเดิม; audit ของคำขอ PUT/DELETE ไปที่ ID ของ A คือ `security.access_denied` actor = B reason `not_owner` อย่างละแถว; ของ UUID สุ่มและของ GET ไม่มี audit
  - `TestOneUserManyPortfoliosAreSeparate` — ผู้ใช้หนึ่งคนสร้าง 3 พอร์ต (`ระยะยาว`, `ระยะสั้น`, `เก็งกำไร`); แก้หนึ่งพอร์ตแล้วอีกสองพอร์ต version/ชื่อไม่เปลี่ยน; ลบหนึ่งพอร์ตแล้ว list เหลือ 2
  - `TestConcurrentPortfolioUpdatesWithOneVersionWinOnce` — สอง browser ของผู้ใช้เดียวกัน (คัด cookie `session` ไปอีกตัว แล้ว `fetchCSRF` ของตัวเอง) ส่ง PUT `If-Match: "1"` ชื่อต่างกันพร้อมกันผ่าน goroutines + `tryDo` (ใช้ start barrier `chan struct{}` ปิดหลัง spawn ทั้งคู่): ได้ 200 หนึ่งและ 412 หนึ่ง, version สุดท้าย 2, ชื่อเป็นของผู้ชนะ, `portfolio.updated` หนึ่งแถว
  - `TestConcurrentDeleteAndUpdateLeaveOneOutcome` — DELETE และ PUT `If-Match: "1"` พร้อมกัน: ผลเป็น (DELETE 204, PUT 404) หรือ (PUT 200, DELETE 412) เท่านั้น เพราะทั้งคู่รอ row lock เดียวกัน; ถ้า DELETE 204 พอร์ตต้องหายและไม่มี `security.access_denied` (ผู้เรียกเป็นเจ้าของ) ถ้า PUT 200 พอร์ตต้องเป็น version 2
  - `TestPortfolioChangesWithoutTheirAuditChangeNothing` — harness คู่ `withFailingAudit()` + `withPortfolios()` บน DB เดียวกัน: create → 500 `audit_write_failed` และไม่มีพอร์ต; update และ delete (พอร์ตสร้างผ่าน harness ปกติ) → 500 และพอร์ตยังเป็น version/ชื่อเดิม
  - `TestLoggedOutSessionCannotUsePortfolios` — logout แล้วใช้ cookie เดิม GET list → 401

- [ ] **Step 2: รัน test** — `docker compose ps && go vet ./internal/adapter/http/ && make -s test-integration` (รันสองครั้งเพื่อจับ flakiness)
- [ ] **Step 3: รันชุดเต็มและ lint** — `gofmt -l internal; go test ./... -count=1 -race && make lint`
- [ ] **Step 4: Commit** — `git commit -m "test(portfolio): prove ownership, isolation and optimistic locking under concurrency"`

---

### Task 7: ประกอบใน `cmd/api`, คู่มือและสถานะ

**Files:**
- Modify: `cmd/api/wire.go`, `cmd/api/wire_test.go`, `cmd/api/main.go`, `README.md`, `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md`, `docs/superpowers/specs/2026-10-05-phase-1-design.md` (เฉพาะสถานะ/ข้อความที่เปลี่ยน), `docs/api/`

**Interfaces:**
- Consumes: `service.NewPortfolios` (Task 3), `httpadapter.Options.Portfolios` (Task 4)
- Produces: `func buildPortfolios(cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) (httpadapter.PortfolioOptions, error)` หรือขยาย `buildAuth` ตามรูปแบบใน `wire.go` (ใช้ `postgres.NewStore(pool)` ชุดเดียวกัน)

- [ ] **Step 1: เขียน failing test** — `TestBuildServesThePortfolioRoutes` ใน `wire_test.go`: server ที่ประกอบจาก config ตอบ `GET /api/v1/portfolios` โดยไม่มี session เป็น 401 (ไม่ใช่ 404)
- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./cmd/api/ -count=1` → 404
- [ ] **Step 3: เขียนโค้ดและเอกสาร**
  - `cmd/api/main.go` `@description` บรรทัดที่สอง: `Milestone 6 exposes Health, the Auth endpoints (email and password, email verification, password reset, Google sign-in and linking) and Portfolios; transactions and holdings are not implemented yet.`
  - README: บรรทัดสถานะเป็น milestone 6 ที่เพิ่ม "สร้าง/ดู/แก้/ลบพอร์ตหลายพอร์ต (ETag/If-Match)"; ต่อ curl flow เดิมด้วยขั้น สร้างสองพอร์ต (`POST /portfolios` สองครั้ง) → list → `PUT` พร้อม `-H 'If-Match: "1"'` → `DELETE` พร้อม If-Match ล่าสุด; อธิบาย 428/412 สั้นๆ; ระบุว่า `make test-integration` ล้างข้อมูลใน dev DB ทุกครั้ง ให้สร้างข้อมูลทดลองหลังรัน test
  - roadmap หัวข้อ 6: ทั้ง 4 checkbox เป็น `- [x]` ต่อท้าย ` (แผน 6)`, เพิ่มบรรทัด `แผน: [2026-10-29-milestone-6-portfolios.md](2026-10-29-milestone-6-portfolios.md) ลงมือครบ รอตรวจรับ` ใต้ "**ไฟล์หลัก:**" และอัปเดตบรรทัด Status ด้านบนให้บอกว่าแผน 6 ลงมือครบ รอตรวจรับ
  - spec หัวข้อ 8: เพิ่ม `Exists(context.Context, domain.ID) (bool, error)` ใน `PortfolioRepository` พร้อม comment ว่าใช้เฉพาะตัดสิน audit `security.access_denied`
- [ ] **Step 4: ตรวจรับในเครื่อง**
  ```bash
  docker compose ps
  make swagger && make check
  make -s test-integration
  go build -o /dev/null ./cmd/api && docker compose config --quiet
  go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
  ```
  Expected: ผ่านทั้งหมด; integration ไม่มี SKIP; govulncheck มีเฉพาะสองรายการเดิม (GO-2026-6809, GO-2026-6629) — รายการใหม่ให้หยุดแล้วแจ้ง
- [ ] **Step 5: Commit** — `git commit -m "feat(api): wire portfolios and document the multi-portfolio flow"`
