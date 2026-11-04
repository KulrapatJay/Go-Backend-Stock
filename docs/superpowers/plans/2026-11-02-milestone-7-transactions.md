# Milestone 7 — Transactions + replay validation + concurrency/idempotency Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** เพิ่ม `POST/GET /api/v1/portfolios/{portfolio_id}/transactions` และ `GET/PUT/DELETE .../transactions/{transaction_id}` ให้ผู้ใช้บันทึกซื้อ/ขายได้เฉพาะ symbol ที่ตรวจแล้ว โดยประวัติหุ้นไม่ติดลบ ณ จุดใดหลังทุก mutation, การสร้างรายการ retry ได้ด้วย Idempotency-Key, คำขอพร้อมกันไม่ข้ามกติกา และ replay เป็นแหล่งคำนวณเดียวที่ milestone 8 ใช้ต่อ พร้อมปิด 3 รายการจากรีวิว milestone 6 ที่ต้องทำก่อน

**Architecture:** เริ่มด้วยการกันไม่ให้ row lock ค้าง (lock/statement timeout, deadline ต่อคำขอ, error ที่ลองใหม่ได้) เพราะ milestone นี้ถือ portfolio lock นานกว่าเดิม จากนั้น domain ได้ `TransactionInput`, snapshot และ `Position`/`ReplayHistory` แบบ pure function; port ได้ `InstrumentProvider` และ `TransactionService`; `service.Transactions` ตรวจ symbol นอก lock แล้วทำทุกอย่างใน transaction เดียวหลัง `LockOwned` (mutate → อ่าน history จริง → replay → audit → idempotency) ถ้า replay ไม่ผ่าน rollback ทั้งหมด; repository ของ transactions, instruments และ idempotency มีครบตั้งแต่ milestone 3 จึงไม่มี migration

**Tech Stack:** Go 1.27 (go.mod `go 1.27.1`), Fiber v3.5.0, pgx v5.11.0, sqlc v1.31.1, shopspring/decimal, PostgreSQL 17.11, swaggo/swag v1.16.6 (Swagger 2.0), golangci-lint v2.14.0 — ไม่มี dependency ใหม่

**Spec:** [phase 1 spec](../specs/2026-10-05-phase-1-design.md) หัวข้อ 7 (transactions, instruments, idempotency_keys), 8 (`TransactionService`, `InstrumentProvider`), 9 (ลำดับขั้น "ซื้อขายและการแก้ย้อนหลัง"), 11 (endpoints, error table, ETag, lists), 12 (replay, ปัดเศษ, demo adapter) และ 14 กับ [AGENTS.md](../../../AGENTS.md) หัวข้อ "กติกาพอร์ตและการคำนวณ", "ความแม่นยำของตัวเลข", ตาราง audit และ ADR [0003](../../adr/0003-decimal-and-event-replay.md), [0008](../../adr/0008-instruments-surrogate-key.md), [0009](../../adr/0009-concurrency-row-lock-and-optimistic-locking.md), [0013](../../adr/0013-domain-value-types.md)

## Global Constraints

- เริ่มงานด้วย `git switch -c feature/transactions feature/multi-portfolio-ownership` (ถ้าผู้ใช้ merge milestone 6 เข้า `developer` แล้วให้แตกจาก `developer`)
- core (`internal/core/**` รวม `_test.go`) import ได้เฉพาะ standard library, `github.com/shopspring/decimal`, `github.com/google/uuid`, `golang.org/x/crypto/argon2` และ `go-backend-stock/internal/core/...`
- ไม่มี migration: ใช้ตาราง `transactions`, `instruments`, `idempotency_keys`, scope `instrument_lookup_user` และ event types `transaction.created/updated/deleted`, `security.access_denied`, `security.rate_limited` ที่มีอยู่แล้ว
- Endpoints (ใต้ `/api/v1`, ต้องมี session ทุกตัว; unsafe methods ต้องมี CSRF + Origin):
  - `POST /portfolios/{portfolio_id}/transactions` + `Idempotency-Key` → 201 Transaction + `ETag`; replay คืน 201 และ body เดิม
  - `GET /portfolios/{portfolio_id}/transactions?limit&cursor&symbol&side&from&to` → 200 `{data:[Transaction],next_cursor:string|null}`
  - `GET /portfolios/{portfolio_id}/transactions/{transaction_id}` → 200 Transaction + `ETag`
  - `PUT .../{transaction_id}` + `If-Match` → 200 Transaction + `ETag` ใหม่; `DELETE .../{transaction_id}` + `If-Match` → 204
- TransactionInput JSON = `{"symbol":"AAPL","side":"buy","quantity":"0.5","price":"200","fee":"1","executed_at":"2026-10-01T14:30:00Z"}` ทุก field บังคับ; ตัวเลขเป็น string เท่านั้น (JSON number → 400 `malformed_json`)
- Transaction JSON = input + `{id,portfolio_id,sequence,version,created_at,updated_at}`; `sequence` เป็น decimal integer string; `symbol` มาจาก instruments ปัจจุบัน; ตัวเลขเป็น canonical string ไม่มีศูนย์ท้าย
- Lists เรียง `executed_at DESC, sequence DESC`; limit ค่าเริ่มต้น 20 สูงสุด 100; from/to รวมปลายทั้งสองด้าน; cursor ผูก portfolio + filters
- Replay เรียง `executed_at ASC, sequence ASC` จาก DB; sequence ไม่เปลี่ยนเมื่อแก้รายการ; version ของรายการเพิ่มทีละ 1 ทุก update สำเร็จ และ portfolio version ไม่เปลี่ยนเมื่อรายการเปลี่ยน
- ต้นทุน: ซื้อ `cost += quantity × price + fee`; ขาย `allocated = cost × sold / held`, `realized += sold × price − fee − allocated`; ขายหมด `allocated = cost` แล้ว quantity/cost/average กลับศูนย์ และ realized ยังอยู่; การหารใช้ 28 ตำแหน่ง round-half-even ด้วย quotient-remainder ไม่ใช้ `DivisionPrecision` หรือ `DivRound`
- ห้ามแปลงเงิน/จำนวนหุ้นผ่าน float; ห้ามถือ portfolio lock ระหว่างเรียก InstrumentProvider
- Idempotency: scope = (user, portfolio, `transactions.create`), key 1–128 ตัว visible ASCII, hash จาก normalized symbol/side/canonical decimals/UTC timestamp, อายุ 24 ชั่วโมงจากครั้งแรกไม่ต่ออายุ; hash ตรงคืน snapshot เดิมโดยไม่เขียนอะไร; hash ต่าง → 409 `idempotency_conflict`; key หมดอายุลบใต้ lock แล้วสร้างใหม่
- ทุก mutation ของรายการเขียน audit ใน DB transaction เดียวกับข้อมูล; เขียนไม่ได้ → rollback และคืน 500 `audit_write_failed`; retry ไม่สร้าง event ซ้ำ
- พอร์ตของผู้อื่นตอบ 404 `not_found` เหมือนพอร์ตที่ไม่มีอยู่; `security.access_denied` เขียนเฉพาะการเขียนไปยังพอร์ตที่มีอยู่จริงของคนอื่น ผ่าน limit `access_denied_user` เดิม และ bookkeeping ล้มเหลวไม่เปลี่ยนคำตอบ 404
- ห้ามแก้ `.env.example` และ `.env`
- **`make -s test-integration` ล้างทุกตาราง schema `app` ใน dev DB หลังรันทุกครั้ง** — ข้อมูลที่ผู้ใช้สร้างเองหายด้วย; ใช้ได้ตามที่ผู้ใช้ตกลงแล้ว
- ต้องเห็น test ล้มก่อนแล้วจึงเขียนโค้ดทุก task; จบ task ด้วย `gofmt -l internal cmd` ที่ไม่พิมพ์อะไร, `go vet ./...` และ `make lint` ผ่าน
- task ที่แก้ annotation ของ handler/DTO ต้องรัน `make swagger` แล้ว commit `docs/api/` ในชุดเดียวกัน ห้ามแก้ไฟล์ generated ด้วยมือ; annotation ห้ามมี `\"\"` ใน `@Param` (swag ตัดข้อความ)
- integration tests ใช้ PostgreSQL จริงผ่าน `make -s test-integration` เท่านั้นและต้องไม่ SKIP; ใช้อีเมล/IP/symbol สุ่มต่อ test
- Docker: ใช้ได้เฉพาะ `docker compose ps` และ `docker compose config --quiet`; ห้ามเรียก API ที่ผู้ใช้รันอยู่
- commit message ไม่มี `Co-Authored-By` หรือชื่อ Claude; commit ในเครื่องเท่านั้น ห้าม push; `git add` เฉพาะไฟล์ของ task

## การตัดสินใจที่แผนนี้เสนอ (ข้อ 4–7 ผู้ใช้ยืนยันแล้วเมื่อ 2026-11-04; ข้ออื่นยังเป็นข้อเสนอที่รอยืนยัน)

1. **ค่า timeout เป็นค่าคงที่ ไม่เพิ่ม env:** `lock_timeout` 3 วินาที, `statement_timeout` 5 วินาที (ตั้งใน `AfterConnect` เพราะ session pooler ไม่รับประกันการส่ง startup parameters) และ deadline ต่อคำขอ 12 วินาที (น้อยกว่า `writeTimeout` 15 วินาที)
2. **ชนกันที่ DB ตอบ 503 `server_busy` + `Retry-After: 1`:** ครอบ `40P01`, `40001`, `55P03`, `57014` และ deadline ของ context; ไม่จัดลำดับ lock เพราะทุกคำขอถือ portfolio lock ตัวเดียว ส่วน deadlock จากการสลับชื่อสองพอร์ตพร้อมกันเกิดได้ยากและลองใหม่ได้
3. **405 ทำทั้งระบบ แต่ HEAD เฉพาะ resource:** route ที่มี parameter ทุกตัวตอบ 405 พร้อม `Allow`; HEAD เปิดเฉพาะ GET ของ portfolios และ transactions ไม่เปิดให้ auth เพราะ `HEAD /auth/google/callback` จะ consume OIDC state
4. **แยก error ตาม operation:** สร้างรายการที่ทำให้ติดลบ → 422 `insufficient_shares`; แก้/ลบที่ทำให้ติดลบ → 422 `invalid_history` (spec ระบุแค่ชื่อ code)
5. **`Idempotency-Key` บังคับบน POST:** ขาด → 400 `invalid_input` (spec ขั้น 2 "require key")
6. **`fee` บังคับส่ง:** ไม่มีค่าธรรมเนียมให้ส่ง `"0"`; ภายหลังผ่อนเป็น optional ได้โดยไม่ breaking แต่กลับทางไม่ได้
7. **Production ยังไม่มี provider จนถึง milestone 8:** demo adapter ใช้เฉพาะนอก production; production ใช้ `noInstrumentProvider` ที่ตอบ 503 สำหรับ symbol ใหม่ ไม่เพิ่ม config `PRICE_PROVIDER` ใน milestone นี้
8. **ตรวจเจ้าของก่อนเรียก provider:** เมื่อ symbol ยังไม่อยู่ใน instruments ให้ `GetOwned` ก่อน rate limit/provider เพื่อไม่ให้ `portfolio_id` ของคนอื่นกินโควตา provider (เพิ่มจาก spec ขั้น 0)
9. **`executed_at` ตรวจที่ HTTP adapter ด้วย Clock ที่ inject:** `TransactionOptions.Clock`, เพราะ `TransactionInput` เป็นค่าที่ parse แล้ว
10. **ทุก mutation replay ทุก instrument ในพอร์ต:** ใช้ `ReplayHistory` ตัวเดียวกับ milestone 8 ครอบกรณีเปลี่ยน symbol โดยไม่ต้องมีเงื่อนไขเพิ่ม แลกกับงาน O(จำนวนรายการในพอร์ต) ต่อ mutation ซึ่ง spec อ่าน history ทั้งพอร์ตอยู่แล้ว
11. **Audit metadata:** `portfolio_id`, `ip`, `user_agent`, `after` (created), `before`+`after` (updated), `before` (deleted); `before`/`after` = `{symbol,side,quantity,price,fee,executed_at}` เป็น string; target = `transaction` + ID
12. **ไม่ refresh instruments และไม่ทำ negative cache ใน milestone นี้:** demo list อยู่ในหน่วยความจำ negative cache 10 นาทีเป็นงานของ Alpaca adapter ใน milestone 8

## Review Focus

1. **ขายย้อนหลังที่ไม่ติดลบ ณ เวลาตัวเอง แต่ทำให้รายการขายถัดไปติดลบ** — ต้องได้ 422, ไม่มีแถวใหม่, key ไม่ถูกใช้ไปและส่งใหม่ด้วย key เดิมได้ — Task 10 `TestBackdatedSellThatBreaksALaterSellIsRefused`
2. **รายการเวลาเท่ากัน** — ลำดับตาม sequence ไม่ใช่ UUID; แก้ `executed_at` ไม่เปลี่ยน sequence — Task 10 `TestTradesAtTheSameTimeReplayInSequenceOrder`
3. **Retry หลังรายการถูกแก้หรือลบไปแล้ว** — ยังคืน snapshot แรก ไม่สร้างแถว/audit/version ใหม่; key หมดอายุแล้วสร้างใหม่ได้ — Task 10 `TestRetryAfterEditOrDeleteReturnsTheFirstResponse`, `TestExpiredKeyCreatesANewTrade`
4. **เปลี่ยน symbol ที่ทำให้ symbol เดิมติดลบ** — 422 `invalid_history`, ข้อมูลเดิมครบ และ symbol ใหม่ที่เพิ่งตรวจไม่เหลือแถวใน instruments — Task 10 `TestSymbolChangeThatBreaksTheOldSymbolRollsBackEverything`
5. **ขายทีละส่วนจนหมดด้วยจำนวนที่หารไม่ลงตัว** — ต้นทุนคงเหลือต้องเป็นศูนย์พอดี ไม่เหลือเศษจากการปัด — Task 4 `TestFullSaleLeavesNoResidualAfterThirds`

---

## File Structure

| ไฟล์ | หน้าที่ | Task |
| --- | --- | --- |
| `internal/adapter/postgres/pool.go`, `errors.go`, `store.go` (+tests), `internal/adapter/http/middleware/timeout.go`, `server.go`, `portfolio.go`, `docs/api/`, `docs/adr/0016-*.md` | timeout + error ที่ลองใหม่ได้ | 1 |
| `internal/adapter/http/server.go`, `portfolio.go`, `method_not_allowed_test.go`, `portfolio_test.go` | 405/HEAD ของ route ที่มี parameter | 2 |
| `internal/core/domain/transaction.go`, `transaction_snapshot.go`, `time.go`, `errors.go` (+tests) | input, hash, snapshot, sentinels | 3 |
| `internal/core/domain/position.go` (+test) | Position, event, replay, การหาร | 4 |
| `internal/core/port/instrument.go`, `transaction_service.go`, `repositories.go`, `internal/adapter/demo/instruments.go` (+test), `internal/core/service/access_denied.go`, `admission.go`, `portfolio.go`, `ratelimit.go` (+tests) | provider, demo, ชิ้นส่วนร่วม | 5 |
| `internal/core/service/transaction.go`, `transaction_test.go` | Create พร้อม idempotency | 6 |
| `internal/core/service/transaction.go`, `transaction_audit.go`, `transaction_test.go` | Update/Delete/Get/List | 7 |
| `internal/adapter/http/transaction.go`, `transaction_dto.go`, `transaction_harness_test.go`, `transaction_test.go`, `portfolio_contract_test.go`, `server.go`, `docs/api/`, `cmd/api/main.go` | POST/GET | 8 |
| `internal/adapter/http/transaction.go`, `transaction_update_test.go`, contract test, `docs/api/` | PUT/DELETE | 9 |
| `internal/adapter/http/transaction_rules_test.go`, `transaction_concurrency_test.go` | กติกาและการแข่งกันกับ PostgreSQL จริง | 10 |
| `cmd/api/wire.go` (+test), `cmd/api/main.go`, `README.md`, roadmap, spec | ประกอบ dependency, คู่มือ, สถานะ | 11 |

---

### Task 1: Timeout ของ DB, deadline ต่อคำขอ และ error ที่ลองใหม่ได้

**Files:**
- Modify: `internal/adapter/postgres/pool.go`, `pool_test.go`, `errors.go`, `errors_test.go`, `store.go`, `uow_test.go`, `internal/adapter/http/server.go`, `server_test.go`, `internal/adapter/http/portfolio.go`, `docs/api/`, `docs/adr/README.md`, roadmap
- Create: `internal/adapter/http/middleware/timeout.go`, `docs/adr/0016-database-timeouts-and-retryable-busy.md`

**Interfaces:**
- Produces:
  - `pool.go`: `const lockTimeout = 3 * time.Second`, `statementTimeout = 5 * time.Second`; `NewPool` คงลายเซ็นเดิม และตั้ง `cfg.AfterConnect` ให้รัน `SET lock_timeout` กับ `SET statement_timeout`
  - `errors.go`: `errDatabaseBusy = domain.NewRetryError(domain.CodeServerBusy, "the database is busy, try again shortly", time.Second)`; `mapError` คืน `fmt.Errorf("%w: %w", errDatabaseBusy, err)` สำหรับ SQLSTATE `40P01`, `40001`, `55P03`, `57014` และ `context.DeadlineExceeded` (`context.Canceled` ยังเป็น internal)
  - `withinTx` ส่ง error ของ Begin/Commit ผ่าน `mapError(fmt.Errorf("begin transaction: %w", err))` และแบบเดียวกันสำหรับ commit
  - `middleware.RequestTimeout(d time.Duration) fiber.Handler`: `context.WithTimeout(c.Context(), d)`, `c.SetContext`, `defer cancel()`, `c.Next()`
  - `server.go`: `const requestTimeout = 12 * time.Second`; `app.Use(middleware.RequestTimeout(requestTimeout))` ต่อจาก `Recover`

- [ ] **Step 1: เขียน failing tests**
  - `TestMapError` เพิ่มเคส `40P01`, `40001`, `55P03`, `57014`, `fmt.Errorf("q: %w", context.DeadlineExceeded)` → `domain.CodeServerBusy` และ `RetryAfter == time.Second`; `context.Canceled` → internal
  - `TestNewPoolSetsLockAndStatementTimeouts` — `SHOW lock_timeout` = `3s`, `SHOW statement_timeout` = `5s` บน connection จาก `NewPool`
  - `TestLockWaitEndsAsServerBusy` — tx A ถือ `LockOwned` ไว้, store จาก `NewPool` เรียก `LockOwned` พอร์ตเดียวกัน → `server_busy` ภายในไม่เกิน 4 วินาที และหลัง A จบเรียกได้ปกติ
  - `TestDeadlockEndsAsServerBusy` — สอง tx ล็อกพอร์ต A/B แล้วสลับกัน (ใช้ channel ให้ลำดับแน่นอน): หนึ่งตัวได้ `server_busy`, อีกตัว commit ได้
  - `TestTransactionPastItsDeadlineIsServerBusy` — `WithinTransaction` ด้วย context ที่หมดเวลาแล้ว → `server_busy`
  - `TestEveryRequestHasADeadline` ใน `server_test.go` — route ทดสอบบน `newApp` อ่าน `c.Context().Deadline()` แล้ว `ok == true` และเหลือไม่เกิน 12 วินาที

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `make -s test-integration` → FAIL ที่เคสใหม่

- [ ] **Step 3: เขียนโค้ดตาม Interfaces** แก้ `@Failure 503` ของ portfolio ทั้งห้า operation เป็น `"dependency_unavailable or server_busy"` แล้วรัน `make swagger`; เขียน ADR-0016 (บริบท: `LockOwned` รอได้ไม่จำกัด, `DB_MAX_CONNS` ต่ำ; ทางเลือก: env config / ค่าคงที่, ลำดับ lock / ลองใหม่; ผลที่ตามมา: คำขอที่รอ lock เกิน 3 วินาทีได้ 503) และเพิ่มแถวใน `docs/adr/README.md`

- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && make -s test-integration && go test ./... -race -count=1 && make lint`

- [ ] **Step 5: ติ๊กสองรายการแรกใต้ "ผลรีวิว (2026-11-02)" ใน roadmap แล้ว commit** — `git commit -m "fix(postgres): bound lock waits and turn database contention into a retryable 503"`

---

### Task 2: 405 และ HEAD ของ route ที่มี parameter

**Files:**
- Modify: `internal/adapter/http/server.go`, `internal/adapter/http/portfolio.go`, `internal/adapter/http/method_not_allowed_test.go`, `internal/adapter/http/portfolio_test.go`, roadmap

**Interfaces:**
- Produces:
  - `func getAndHead(app *fiber.App, path string, handler any, handlers ...any)` — ลงทะเบียนชุด handler เดียวกันให้ GET และ HEAD
  - `func routeMethods(app *fiber.App) func(path string) []string` แทน `staticRouteMethods`: ตารางจาก `app.GetRoutes(true)` เก็บ path แบบแยกส่วนด้วย `/`; ส่วนที่ขึ้นต้น `:` ตรงกับส่วนที่ไม่ว่างใดก็ได้ จำนวนส่วนต้องเท่ากัน; path ที่มี `*` หรือ `+` ยังข้ามเหมือนเดิม; methods เรียงตามตัวอักษรและไม่ซ้ำ

- [ ] **Step 1: เขียน failing tests**
  - `TestParameterRouteWithAnotherMethodIs405WithAllow` — `PATCH /api/v1/portfolios/<uuid>` → 405, `Allow: DELETE, GET, HEAD, PUT`, code `method_not_allowed`, access log route `unmatched`; `POST` บน path เดียวกันก็ 405; `/api/v1/portfolios/<uuid>/nope` → 404 ไม่มี `Allow`
  - `TestHeadOnPortfolioRoutes` — HEAD ของรายการและพอร์ตเดี่ยวตอบ 200 ไม่มี body, พอร์ตเดี่ยวมี `ETag`; ไม่มี session → 401
  - `TestHeadOnGoogleCallbackDoesNotRunIt` — `HEAD /api/v1/auth/google/callback?state=x&code=y` ตอบ 404 (handler ไม่ทำงาน)

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `make -s test-integration` → PATCH ได้ 404, HEAD ได้ 404

- [ ] **Step 3: เขียนโค้ดตาม Interfaces** เปลี่ยน GET สองตัวใน `registerPortfolioRoutes` เป็น `getAndHead`; ไม่แตะ route ของ auth

- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && make -s test-integration && make lint`

- [ ] **Step 5: ติ๊กรายการที่สามใต้ "ผลรีวิว (2026-11-02)" ใน roadmap แล้ว commit** — `git commit -m "fix(http): answer 405 on parameter routes and HEAD on portfolio reads"`

---

### Task 3: `TransactionInput`, hash, snapshot และ sentinels

**Files:**
- Modify: `internal/core/domain/transaction.go`, `internal/core/domain/time.go`, `internal/core/domain/errors.go`, `internal/core/domain/records_test.go`
- Create: `internal/core/domain/transaction_snapshot.go`, `internal/core/domain/transaction_input_test.go`

**Interfaces:**
- Produces:
  - sentinels: `ErrInsufficientShares` (message `"Transaction would make holdings negative"`), `ErrInvalidHistory` (`"Change would make holdings negative at some point in the history"`), `ErrUnknownSymbol` (`"symbol is not a known US stock"`), `ErrDependencyUnavailable` (`"a required service is unavailable, try again later"`)
  - `type TransactionInput struct { Symbol Symbol; Side Side; Quantity Quantity; Price Price; Fee Fee; ExecutedAt time.Time }`
  - `func ParseTransactionInput(symbol, side, quantity, price, fee, executedAt string, now time.Time) (TransactionInput, error)` — คืน error ตัวแรกตามลำดับ field
  - `func (in TransactionInput) RequestHash() []byte` — SHA-256 (32 bytes) ของ `symbol\x00side\x00quantity\x00price\x00fee\x00executed_at` โดย decimals เป็น `String()` และเวลาเป็น UTC `time.RFC3339Nano`
  - `func ParseTimeFilter(field, s string) (time.Time, error)` ใน `time.go` — กติกาเดียวกับ `ParseExecutedAt` แต่ไม่ตรวจอนาคต/ขั้นต่ำ (ใช้ helper ร่วมที่รับ field)
  - `func MarshalTransactionSnapshot(t Transaction) (json.RawMessage, error)`, `func ParseTransactionSnapshot(raw json.RawMessage) (Transaction, error)` — JSON ทุก field ของ `Transaction`; ค่าที่ parse ไม่ได้เป็น invariant (internal)

- [ ] **Step 1: เขียน failing tests**
  - `TestParseTransactionInput` — ตัวอย่างใน spec ผ่าน; symbol `" aapl "` ได้ `AAPL`; side `SELL`, quantity `"0"`, price `""`, fee `"-1"`, `executed_at` ในอนาคตหรือไม่มี timezone → `invalid_input` ที่ message มีชื่อ field
  - `TestRequestHashIgnoresFormatting` — `"0.50"` กับ `"0.5"`, `"2026-10-01T14:30:00Z"` กับ `"2026-10-01T21:30:00+07:00"`, `" aapl"` กับ `"AAPL"` ได้ hash เดียวกันยาว 32 bytes; เปลี่ยนค่าใดค่าหนึ่งใน 6 field → hash ต่าง; `("AB","C")` ไม่ชนกับ `("A","BC")` จากตัวคั่น `\x00`
  - `TestTransactionSnapshotRoundTrip` — ทุก field กลับมาเท่าเดิม รวม `Sequence`, microseconds และ `"0.0000000001"`; JSON ที่ขาด field หรือ decimal ผิด → `internal`
  - `TestParseTimeFilter` — รับเวลาอนาคต, ปฏิเสธ nanoseconds และเวลาไม่มี timezone
  - `TestNewSentinelsMapToTheirCodes` — ทั้งสี่ตัวตรง code เดิม

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/core/domain/ -count=1` → compile fail
- [ ] **Step 3: เขียนโค้ดตาม Interfaces**
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/core/... -count=1 -race && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(domain): add the transaction input, its request hash and the stored snapshot"`

---

### Task 4: `Position` และ replay แบบ pure function

**Files:**
- Create: `internal/core/domain/position.go`, `internal/core/domain/position_test.go`

**Interfaces:**
- Consumes: `Transaction`, `ErrInsufficientShares` จาก Task 3
- Produces:
  - `type Position struct { Quantity, CostBasis, AverageCost, RealizedPnL decimal.Decimal }` — zero value คือยังไม่มีหุ้น
  - `type PositionEvent interface { applyTo(Position) (Position, error) }` — interface ปิดใน package เพื่อให้ corporate actions เพิ่มเป็นชนิดใหม่ภายหลังโดยไม่แก้ transactions
  - `type TradeEvent struct { Side Side; Quantity Quantity; Price Price; Fee Fee }`, `func NewTradeEvent(t Transaction) TradeEvent`
  - `func (p Position) Apply(e PositionEvent) (Position, error)` คืนสถานะทีละขั้น; ขายเกินคืน `ErrInsufficientShares` และ `Position` เดิม
  - `func ReplayPosition(events []PositionEvent) (Position, error)`
  - `func ReplayHistory(history []Transaction) (map[ID]Position, error)` — จัดกลุ่มตาม `InstrumentID` รักษาลำดับที่รับเข้ามา (repository เรียงแล้ว) milestone 8 ใช้ตัวนี้ตัวเดียว
  - `func divideHalfEven(a, b decimal.Decimal) decimal.Decimal` — a ≥ 0, b > 0

การหารใช้อัลกอริทึมนี้ (ลองรันกับ shopspring/decimal แล้วได้ค่าตรงกับ `TestDivideHalfEven` และ `TestFullSaleLeavesNoResidualAfterThirds` ทุกค่า):

```go
q, r := a.QuoRem(b, 28)
switch r.Shift(28).Mul(decimal.NewFromInt(2)).Cmp(b) {
case 1:
    q = q.Add(oneUnit) // oneUnit = decimal.New(1, -28)
case 0:
    if q.Shift(28).Mod(decimal.NewFromInt(2)).IsPositive() { // ตัวสุดท้ายเป็นเลขคี่
        q = q.Add(oneUnit)
    }
}
```

- [ ] **Step 1: เขียน failing tests**
  - `TestPositionGoldenExample` (spec หัวข้อ 12) — ซื้อ 2 × 100 fee 2 → cost 202, average 101; ขาย 0.5 × 120 fee 1 → allocated 50.5, realized 8.5, quantity 1.5, cost 151.5, average 101
  - `TestApplyReturnsEachStep` — สถานะหลังแต่ละ event ถูกต้องและ `Position` เดิมไม่เปลี่ยน
  - `TestSellMoreThanHeldIsRefused` — ถือ 1 ขาย 1.0000000001 → `ErrInsufficientShares`; ไม่มีหุ้นแล้วขาย → `ErrInsufficientShares`
  - `TestFullSaleLeavesNoResidualAfterThirds` — ซื้อ 3 × 10 fee 0.01, ขาย 1, 1, 1 ที่ราคา 12 fee 0 → quantity/cost/average เป็นศูนย์พอดี (`IsZero`), realized = `5.99` พอดี; ซื้อใหม่หลังขายหมดเริ่มต้นทุนจากศูนย์และ realized ยังอยู่
  - `TestRealizedLossIsNegative` — ขายต่ำกว่าทุนได้ realized ติดลบ
  - `TestDivideHalfEven` — `1/3` = 28 ตัว `3`; `2/3` ลงท้าย `7`; `5e-29/1` = 0; `15e-29/1` = `2e-28`; `25e-29/1` = `2e-28`; `35e-29/1` = `4e-28`
  - `TestReplayHistoryKeepsInstrumentsApart` — สอง instrument ในพอร์ตเดียวคำนวณแยก; instrument หนึ่งขายเกินคืน `ErrInsufficientShares`

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/core/domain/ -run 'Position|Apply|Sell|FullSale|Realized|DivideHalfEven|ReplayHistory' -count=1` → compile fail
- [ ] **Step 3: เขียนโค้ดตาม Interfaces**
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/core/... -count=1 -race && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(domain): replay positions with moving weighted-average cost as a pure function"`

---

### Task 5: `InstrumentProvider`, demo adapter และชิ้นส่วนที่ใช้ร่วม

**Files:**
- Create: `internal/core/port/instrument.go`, `internal/core/port/transaction_service.go`, `internal/adapter/demo/instruments.go`, `internal/adapter/demo/instruments_test.go`, `internal/core/service/access_denied.go`
- Modify: `internal/core/port/repositories.go`, `internal/core/service/admission.go`, `internal/core/service/portfolio.go`, `internal/core/service/ratelimit.go`, `ratelimit_test.go`, `portfolio_test.go`

**Interfaces:**
- Produces:
  - `port.InstrumentProvider` — `Lookup(ctx context.Context, symbol domain.Symbol) (domain.Instrument, error)`: คืน `domain.ErrUnknownSymbol` เมื่อไม่พบ, error อื่นหมายถึง provider ล่ม; ผลลัพธ์ไม่มี `ID` และเวลา ให้ service เติม (ใช้ `domain.Symbol` ตาม ADR-0013 แทน string ใน spec)
  - `port.TransactionService` ตาม spec หัวข้อ 8: `Create(ctx, p domain.Principal, portfolioID domain.ID, key string, in domain.TransactionInput, meta domain.RequestMeta) (domain.Transaction, error)`, `List(ctx, p, portfolioID, f domain.TransactionFilter) (domain.TransactionPage, error)`, `Get(ctx, p, portfolioID, transactionID) (domain.Transaction, error)`, `Update(ctx, p, portfolioID, transactionID, expected domain.Version, in, meta) (domain.Transaction, error)`, `Delete(ctx, p, portfolioID, transactionID, expected, meta) error`
  - `port.Repositories` เพิ่ม `Transactions() TransactionRepository` และ `Instruments() InstrumentRepository` (`*postgres.Store` มีอยู่แล้ว)
  - `demo.NewInstruments() *demo.Instruments` — AAPL, MSFT, NVDA, GOOGL, AMZN, META, TSLA, SPY: `Status active`, `AssetClass us_equity`, `Source demo`; symbol อื่น → `domain.ErrUnknownSymbol`
  - `(*RateLimitPolicy).InstrumentLookup(user domain.ID) ([]domain.RateLimitRule, error)` — scope `instrument_lookup_user`, 30 ครั้ง/ชั่วโมง
  - `func admitRules(ctx context.Context, limits port.RateLimitRepository, clock port.Clock, rules []domain.RateLimitRule, onFirstDenial func(domain.RateLimitDecision)) error` ใน `admission.go`; `(*Auth).admit` เรียกตัวนี้โดยพฤติกรรมเดิม
  - `service/access_denied.go`: `type accessDenials struct { store port.Repositories; uow port.UnitOfWork; clock port.Clock; limits *RateLimitPolicy; log *slog.Logger }`, `func (d accessDenials) record(ctx context.Context, p domain.Principal, portfolioID domain.ID, meta domain.RequestMeta)` — ย้ายตรรกะ `recordDenial` จาก `Portfolios` มาทั้งก้อน พร้อม `type securityMetadata struct { IP, UserAgent, Scope }` และ `func rateLimitedSpec(p domain.Principal, d domain.RateLimitDecision) auditSpec`; `portfolioMetadata` ไม่มี `Scope` อีกต่อไป

- [ ] **Step 1: เขียน failing tests**
  - `TestDemoInstrumentsKnowsTheFixedList` — ทั้ง 8 symbol ผ่าน `domain.NewInstrument` เมื่อเติม ID/เวลา; `ZZZZ` → `ErrUnknownSymbol`
  - `TestRateLimitPolicyRules` เพิ่มเคส `"instrument lookup"` → `domain.RateLimitInstrumentLookupUser: {{30, hour}}`
  - tests ของ `admit` และ `TestDeniedAccessIsAuditedOnlyWhileTheUserIsUnderTheLimit`, `TestDenialBookkeepingFailuresDoNotChangeTheAnswer` ต้องผ่านเหมือนเดิมหลังย้ายโค้ด — ไม่ต้องเขียนใหม่

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/core/... ./internal/adapter/demo/ -count=1` → compile fail
- [ ] **Step 3: เขียนโค้ดตาม Interfaces** (refactor ไม่เปลี่ยน output ของ audit event ใดๆ)
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/core/... ./internal/adapter/demo/ -count=1 -race && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(core): add the instrument provider, the demo list and shared denial handling"`

---

### Task 6: `service.Transactions.Create` พร้อม idempotency

**Files:**
- Create: `internal/core/service/transaction.go`, `internal/core/service/transaction_test.go`

**Interfaces:**
- Consumes: `port.TransactionService`, `InstrumentProvider`, `accessDenials`, `admitRules`, `RateLimitPolicy.InstrumentLookup` จาก Task 5; `ReplayHistory` จาก Task 4; `RequestHash`, snapshot จาก Task 3; `commitFailureAudit`, `newAuditEvent`, `appendAuditEvent`
- Produces:
  - `type TransactionDeps struct { Store port.Repositories; UnitOfWork port.UnitOfWork; Clock port.Clock; Limits *RateLimitPolicy; Instruments port.InstrumentProvider; Logger *slog.Logger }`, `func NewTransactions(d TransactionDeps) (*Transactions, error)`
  - `const idempotencyTTL = 24 * time.Hour`
  - `func (s *Transactions) inPortfolio(ctx, p domain.Principal, portfolioID domain.ID, meta domain.RequestMeta, fn func(port.TxRepositories) error) error` — `LockOwned` ใน transaction แล้วเรียก fn; เฉพาะ not-found ของ `LockOwned` เท่านั้นที่เรียก `denials.record` หลัง rollback (not-found ของรายการในพอร์ตตัวเองไม่ใช่การปฏิเสธสิทธิ์)
  - `func (s *Transactions) instrumentFor(ctx, p, portfolioID, symbol domain.Symbol, meta) (instrument domain.Instrument, fresh bool, err error)` — นอก transaction: `Instruments().BySymbol` พบ → คืนเลย; ไม่พบ → `Portfolios().GetOwned` (not-found → `denials.record` แล้วคืน not-found), `admitRules`, `Instruments.Lookup`, ตรวจ `AssetClass == us_equity` ไม่ตรง → `ErrUnknownSymbol`, error อื่นของ provider → `fmt.Errorf("%w: %w", domain.ErrDependencyUnavailable, err)`, เติม `ID`/`VerifiedAt`/`CreatedAt`/`UpdatedAt` แล้วผ่าน `domain.NewInstrument`
  - ลำดับใน transaction: `LockOwned` → `Idempotency.Get` (พบ: หมดอายุ → `DeleteExpired`; hash ตรง → `ParseTransactionSnapshot` แล้วจบ; hash ต่าง → `ErrIdempotencyConflict`) → `Instruments.Upsert` (เมื่อ fresh; ใช้ ID ของแถวที่คืนมา เพราะคำขอพร้อมกันอาจ insert ก่อน) → `Transactions.Insert` → `Transactions.History` → `ReplayHistory` → audit `transaction.created` → `Idempotency.Save`

- [ ] **Step 1: เขียน failing tests** (fakes ของ `TransactionRepository`, `InstrumentRepository`, `IdempotencyRepository`, `InstrumentProvider` ใน `transaction_test.go` โดยใช้ `callLog` และ `fakePortfolios` เดิม)
  - `TestCreateTradeWritesInOneTransaction` — symbol ที่รู้จัก: calls = `[Portfolios.LockOwned, Idempotency.Get, Transactions.Insert, Transactions.History, Audit.Append, Idempotency.Save]`, provider ไม่ถูกเรียก, `ExpiresAt = CreatedAt + 24h`, `Result` parse กลับเป็นรายการที่คืน, audit มี `after` ครบ 6 field และ `portfolio_id`
  - `TestCreateLooksUpANewSymbolBeforeTheTransaction` — fake provider บันทึกว่าถูกเรียกตอน `uow.runs == 0`; calls เริ่มด้วย `Instruments.BySymbol, Portfolios.GetOwned, RateLimits.Admit, Provider.Lookup`; `Instruments.Upsert` อยู่ก่อน `Transactions.Insert`
  - `TestCreateOnAForeignPortfolioNeverReachesTheProvider` — symbol ใหม่บนพอร์ตคนอื่น: not_found, provider ไม่ถูกเรียก, `security.access_denied` หนึ่งแถว
  - `TestCreateSymbolProblems` (table) — provider `ErrUnknownSymbol` → `unknown_symbol`; provider error อื่น → `dependency_unavailable` โดยไม่เขียนอะไร; `status inactive` บันทึกได้; limiter ปฏิเสธ → `rate_limited` พร้อม `RetryAfter`, provider ไม่ถูกเรียก, `security.rate_limited` ต่อเมื่อ `RecordDenialAudit`
  - `TestCreateReplaysTheSameKey` — record ที่ hash ตรงและยังไม่หมดอายุ: คืน snapshot, calls จบที่ `Idempotency.Get`, ไม่มี audit/provider
  - `TestCreateRefusesAKeyUsedForAnotherRequest` — hash ต่าง → `idempotency_conflict`
  - `TestCreateReusesAnExpiredKey` — record หมดอายุ: `Idempotency.DeleteExpired` ก่อน insert แล้วบันทึก record ใหม่
  - `TestCreateRefusesASellPastTheHolding` — history ขายเกิน → `insufficient_shares`, ไม่มี `Audit.Append` และ `Idempotency.Save`
  - `TestCreateFailsWhenTheAuditCannotBeWritten` — `audit_write_failed` และไม่มี `Idempotency.Save`
  - `TestCreateNeedsACaller` — principal ว่าง → `unauthenticated`, ไม่แตะ repository

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/core/service/ -run 'Create' -count=1` → compile fail
- [ ] **Step 3: เขียนโค้ดตาม Interfaces** ฝาก `transactionMetadata{PortfolioID, IP, UserAgent, Before, After}` และ `tradeFields{Symbol, Side, Quantity, Price, Fee, ExecutedAt}` ไว้ใน `transaction_audit.go` ซึ่งเป็น allowlist; ค่าทศนิยมและเวลาเป็น string
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/core/... -count=1 -race && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(service): record a trade atomically with its replay check, audit and idempotency key"`

---

### Task 7: `Update`, `Delete`, `Get`, `List`

**Files:**
- Modify: `internal/core/service/transaction.go`, `internal/core/service/transaction_audit.go`, `internal/core/service/transaction_test.go`

**Interfaces:**
- Consumes: `inPortfolio`, `instrumentFor`, `ReplayHistory`
- Produces (ตาม `port.TransactionService`):
  - `Update`: `instrumentFor` นอก lock → `inPortfolio`: `Transactions.Get` → `Version != expected` คืน `ErrVersionConflict` → `Instruments.Upsert` เมื่อ fresh → `Transactions.Update` (ส่ง `Version` ปัจจุบัน, `Sequence`/`CreatedAt` เดิม, `UpdatedAt` ใหม่) → `History` → `ReplayHistory` → audit `transaction.updated` พร้อม `before`/`after`
  - `Delete`: `inPortfolio`: `Get` → ตรวจ version → `Transactions.Delete` → `History` → `ReplayHistory` → audit `transaction.deleted` พร้อม `before`
  - ทั้งสองแปลง `ErrInsufficientShares` จาก replay เป็น `ErrInvalidHistory`
  - `Get`/`List` อ่านผ่าน `store.Transactions()` นอก transaction ไม่เขียน audit

- [ ] **Step 1: เขียน failing tests**
  - `TestUpdateChecksTheVersionUnderTheLock` — version เก่า → `version_conflict`, calls หยุดหลัง `Transactions.Get`, ไม่มี audit
  - `TestUpdateAuditsBeforeAndAfter` — metadata มี `before`/`after` ครบ 6 field เป็น string และไม่มี field อื่น
  - `TestUpdateToANewSymbolStoresTheInstrumentFirst` — `Upsert` ก่อน `Update`, `InstrumentID` ใหม่ถูกส่งไป repository
  - `TestUpdateAndDeleteRefuseAnImpossibleHistory` — replay ขายเกิน → `invalid_history` ไม่ใช่ `insufficient_shares`, ไม่มี audit
  - `TestDeleteAuditsWhatWasRemoved` — calls = `[LockOwned, Transactions.Get, Transactions.Delete, Transactions.History, Audit.Append]`, metadata มี `before` ไม่มี `after`
  - `TestMissingTradeInOwnPortfolioIsNotAnAccessDenial` — พอร์ตตัวเอง รายการไม่มี: not_found, ไม่เรียก `Portfolios.Exists`, ไม่มี audit
  - `TestUpdateOrDeleteOnAForeignPortfolioIsDenied` — not_found และ `security.access_denied` หนึ่งแถว target `portfolio`
  - `TestGetAndListNeedACallerAndWriteNoAudit`

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/core/service/ -run 'Update|Delete|Missing|GetAndList' -count=1` → FAIL
- [ ] **Step 3: เขียนโค้ดตาม Interfaces**
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/core/... -count=1 -race && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(service): edit, delete and read trades under the portfolio lock"`

---

### Task 8: HTTP สร้าง อ่าน และแสดงรายการ

**Files:**
- Create: `internal/adapter/http/transaction.go`, `transaction_dto.go`, `transaction_harness_test.go`, `transaction_test.go`
- Modify: `internal/adapter/http/server.go`, `auth_harness_test.go`, `portfolio_contract_test.go`, `portfolio_update_test.go`, `cmd/api/main.go`, `docs/api/`

**Interfaces:**
- Consumes: `port.TransactionService`, `parseIfMatch`/`setETag`, `authChain`, `getAndHead`, `requestMeta`, `domain.ParseTransactionInput`, `domain.ParseTimeFilter`, `domain.NewTransactionFilter`
- Produces:
  - `type TransactionOptions struct { Service port.TransactionService; Clock port.Clock }` และ `Options.Transactions`; `Service` เป็น nil ไม่ลงทะเบียน route; `Auth.Service` nil แต่มี transactions ให้ panic เหมือน portfolios
  - `func registerTransactionRoutes(app *fiber.App, opts TransactionOptions, chain authChain)` ใช้ chain เดียวกับ portfolios; GET ผ่าน `getAndHead`
  - DTO: `TransactionRequest{Symbol, Side, Quantity, Price, Fee, ExecutedAt string}` ทุก field `validate:"required"`; `TransactionResponse{ID, PortfolioID, Sequence string; Symbol, Side, Quantity, Price, Fee string; ExecutedAt time.Time; Version int32; CreatedAt, UpdatedAt time.Time}` ที่ `Sequence` เป็น string; `TransactionPageResponse{Data, NextCursor}` แบบเดียวกับ portfolio
  - `func sendTransaction(c fiber.Ctx, status int, t domain.Transaction) error` — `no-store` + `ETag` + JSON
  - Harness: `withTransactions()` เปิด portfolios ด้วย, `fakeInstrumentProvider` (ขึ้นต้น `UNK` → `ErrUnknownSymbol`; `h.instruments.setDown(true)` → error; อื่นๆ คืน demo instrument พร้อมนับจำนวนครั้ง), `func (h *authHarness) freshSymbol(t) string`
  - Swagger tag `Transactions` ใน `cmd/api/main.go` และบรรทัด `@description` ของ milestone

- [ ] **Step 1: เขียน failing tests**
  - แก้ `seedTrade` ให้ใช้ `freshSymbol` ซึ่งเอา **ท้าย** ของ UUIDv7 (`"Z" + ท้าย 12 ตัว`): 8 ตัวแรกเดิมเป็นเวลา จึงชนกันเมื่อ seed ภายในราว 65 วินาที (ต้นเหตุ `instruments_symbol_key` ที่เจอตอนรัน test ซ้ำ)
  - `TestCreateTradeReturnsItsRepresentation` — 201, `ETag: "1"`, ทุก field ตาม Global Constraints, `sequence` เป็น string, `"0.50"` ตอบ `"0.5"`, `Cache-Control: no-store`, instrument ของ symbol ใหม่อยู่ในตาราง
  - `TestCreateTradeRejectsBadRequests` (table) — ไม่มี `Idempotency-Key`, key เกิน 128 ตัว, `quantity` เป็น JSON number → `malformed_json`, unknown field, `executed_at` อนาคตตาม `h.clock`, `portfolio_id` ไม่ใช่ UUID: 400 และไม่มีแถวใหม่
  - `TestCreateTradeNeedsSessionCSRFAndOrigin` — 401, 403 `csrf_failed`
  - `TestCreateTradeSymbolErrors` — `UNK...` → 422 `unknown_symbol`; provider ล่มกับ symbol ใหม่ → 503 `dependency_unavailable`; provider ล่มกับ symbol ที่รู้จักแล้วบันทึกได้ 201 (ไม่เรียก provider)
  - `TestInstrumentLookupIsRateLimitedPerUser` — symbol ใหม่ครั้งที่ 31 ในชั่วโมงได้ 429 + `Retry-After`; symbol ที่มีแล้วยังบันทึกได้; ผู้ใช้อื่นไม่กระทบ
  - `TestGetAndListTrades` — `ETag` บนรายการเดี่ยว, เรียงใหม่สุดก่อน, 25 รายการแบ่ง 20 + 5 ไม่ซ้ำไม่ตก, filter `symbol`, `side`, `from`, `to`, cursor ของ filter อื่น → 400, `side=x` → 400, `from` หลัง `to` → 400, HEAD 200
  - contract: `documentedTransactionOperations` — post `{201,400,401,403,404,409,422,429,500,503}`, get list `{200,400,401,404,500,503}`, get one `{200,400,401,404,500,503}`; `requiredPortfolioFields` เพิ่ม `http.TransactionRequest`, `http.TransactionResponse`, `http.TransactionPageResponse`; `created_at`, `updated_at`, `executed_at` เป็น `date-time`

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `make -s test-integration` → compile fail
- [ ] **Step 3: เขียน handlers, DTO และ annotations** ลำดับใน handler: parse ID → parse Idempotency-Key → decode body → `ParseTransactionInput(..., opts.Clock.Now())` → `requestMeta` → service; `@ID` = `createTransaction`, `listTransactions`, `getTransaction`; ระบุ `@Param Idempotency-Key header` และ `example` แบบ decimal string ของ `quantity`/`price`/`fee`; `symbol`/`side`/`from`/`to` ที่เป็นค่าว่างถือว่าไม่กรอง
- [ ] **Step 4: รัน test ให้ผ่าน** — `make swagger && gofmt -l internal cmd; go vet ./... && make -s test-integration && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(http): record, list and read trades with Idempotency-Key"`

---

### Task 9: HTTP แก้และลบรายการ

**Files:**
- Modify: `internal/adapter/http/transaction.go`, `portfolio_contract_test.go`, `docs/api/`
- Create: `internal/adapter/http/transaction_update_test.go`

**Interfaces:**
- Consumes: `parseIfMatch`, `sendTransaction`, `TransactionRequest`
- Produces: handlers `update`, `delete`; `@ID` = `updateTransaction`, `deleteTransaction`; `@Param If-Match` และ `@Header 200 ETag` แบบเดียวกับ portfolio

- [ ] **Step 1: เขียน failing tests**
  - `TestUpdateTrade` — 200, `ETag: "2"`, sequence/created_at เดิม, updated_at ใหม่, portfolio version ไม่เปลี่ยน, audit `transaction.updated` มี `before`/`after`
  - `TestUpdateTradeToAnotherSymbol` — `symbol` ใน response ใหม่ และ `instrument_id` ใน DB ตรงกับ instruments ของ symbol นั้น
  - `TestUpdateAndDeleteTradePreconditions` (table ทั้ง PUT/DELETE) — ไม่มี If-Match → 428; version เก่า → 412; `*`, `W/"1"`, `"03"` → 400; ทุกเคสข้อมูลและจำนวน audit ไม่เปลี่ยน
  - `TestDeleteTrade` — 204, GET ต่อมาได้ 404, audit `transaction.deleted` target ID เดิม, ลบซ้ำได้ 404
  - `TestUpdateAndDeleteTradeNeedCSRFAndOrigin` — token จริงของอีกเซสชัน, Origin ปลอม, ไม่มี token → 403
  - `TestATradeCannotBeReachedThroughAnotherPortfolio` — path ใช้พอร์ตอื่นของเจ้าของเดียวกัน: GET/PUT/DELETE 404 และรายการไม่ย้าย
  - contract: put `{200,400,401,403,404,412,422,428,429,500,503}`, delete `{204,400,401,403,404,412,422,428,500,503}`

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `make -s test-integration` → 405 บน PUT/DELETE
- [ ] **Step 3: เขียน handlers และ annotations** ลำดับ: parse IDs → `parseIfMatch` → body → input → meta → service
- [ ] **Step 4: รัน test ให้ผ่าน** — `make swagger && gofmt -l internal cmd; go vet ./... && make -s test-integration && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(http): edit and delete trades with If-Match and ETag"`

---

### Task 10: กติกาและการแข่งกันกับ PostgreSQL จริง

**Files:**
- Create: `internal/adapter/http/transaction_rules_test.go`, `internal/adapter/http/transaction_concurrency_test.go`

**Interfaces:**
- Consumes: harness และ helpers จาก Task 8–9; ไม่มีโค้ด production ใหม่ ถ้า test เจอบั๊กให้แก้ใน commit ของ task นี้พร้อมระบุในข้อความ

- [ ] **Step 1: เขียน tests** (`transaction_rules_test.go`)
  - `TestBackdatedSellThatBreaksALaterSellIsRefused` — ซื้อ 10 วันที่ 1, ขาย 10 วันที่ 3; ขาย 1 ย้อนหลังวันที่ 2 → 422 `insufficient_shares`, จำนวนแถวเท่าเดิม, ไม่มี key ใน `idempotency_keys`; ส่งใหม่ด้วย key เดิมหลังลบรายการขายวันที่ 3 ได้ 201
  - `TestEditOrDeleteThatBreaksHistoryIsRefused` — ลด quantity ของรายการซื้อ, เลื่อนรายการซื้อไปหลังรายการขาย, ลบรายการซื้อ → 422 `invalid_history` และข้อมูล/version/audit ไม่เปลี่ยน
  - `TestTradesAtTheSameTimeReplayInSequenceOrder` — ซื้อ A 5 วันที่ 1 (seq ต่ำสุด), ขาย S 5 วันที่ 2, ซื้อ C 5 วันที่ 3 (seq สูงสุด); แก้ C เป็นเวลาเดียวกับ S → 200 และ `sequence` ของ C ไม่เปลี่ยน; ลบ A → 422 `invalid_history` เพราะที่เวลาเดียวกัน S (sequence น้อยกว่า) เรียงก่อน C จึงขายก่อนมีหุ้น; ข้อมูลเดิมครบ (พิสูจน์ว่าลำดับตาม sequence ไม่ใช่ UUID หรือลำดับที่ส่งมา)
  - `TestSameSymbolInTwoPortfoliosStaysSeparate` — ถือ 10 ในพอร์ต A ขายในพอร์ต B ได้ 422
  - `TestSymbolChangeThatBreaksTheOldSymbolRollsBackEverything` — ซื้อ A 10, ขาย A 5; เปลี่ยนรายการซื้อเป็น symbol ใหม่ → 422 `invalid_history`, รายการเดิมครบ, ไม่มีแถว instruments ของ symbol ใหม่
  - `TestRetryAfterEditOrDeleteReturnsTheFirstResponse` — retry key เดิมหลัง PUT และหลัง DELETE: body ตรงกับ response แรกทุก byte, version/จำนวนแถว/จำนวน audit ไม่เปลี่ยน
  - `TestSameKeyInAnotherPortfolioOrUserIsANewRequest`
  - `TestKeyWithAnotherPayloadConflicts` — 409 `idempotency_conflict`; ต่างแค่รูปแบบ `"1.0"` กับ `"1"` เป็น replay
  - `TestExpiredKeyCreatesANewTrade` — `h.clock.Advance(24*time.Hour + time.Second)` แล้ว key เดิมได้แถวใหม่และ record ใหม่; ที่ 23 ชั่วโมงยัง replay
  - `TestTradesAreIsolatedBetweenUsers` — ผู้ใช้อื่น GET/PUT/DELETE/POST ได้ 404 ที่ body เหมือน UUID สุ่ม, ข้อมูลไม่เปลี่ยน, การเขียนเกิด `security.access_denied`, การอ่านไม่เกิด
  - `TestTradeRollsBackWhenTheAuditFails` — `withFailingAudit`: POST/PUT/DELETE ได้ 500 `audit_write_failed`, ไม่มีการเปลี่ยนแปลงและไม่มี key
  - `TestPortfolioWithTradesCannotBeDeletedUntilTheyAreGone` — 409 จนลบรายการครบแล้วลบพอร์ตได้

- [ ] **Step 2: เขียน tests** (`transaction_concurrency_test.go` ใช้ `tryDo` และ goroutine แบบ `portfolio_isolation_test.go`)
  - `TestConcurrentSellsNeverOversell` — ถือ 5, ยิงขายครั้งละ 1 พร้อมกัน 12 คำขอคนละ key: 201 พอดี 5, ที่เหลือ 422 หรือ 503 `server_busy`, ไม่มีสถานะอื่น, replay ทั้งพอร์ตได้ quantity 0
  - `TestConcurrentRetriesOfOneKeyCreateOneTrade` — key เดียว 8 คำขอพร้อมกัน: แถวเดียว, audit `transaction.created` แถวเดียว, ทุก 201 มี body เดียวกัน
  - `TestConcurrentEditsWithOneVersionWinOnce` — 200 หนึ่งครั้ง, 412 หนึ่งครั้ง, version 2
  - `TestConcurrentDeleteOfBuyAndSellLeaveAValidHistory` — ลบรายการซื้อกับเพิ่มรายการขายพร้อมกัน ผลลัพธ์สุดท้าย replay ได้เสมอ
  - `TestConcurrentCreatesOfANewSymbolShareOneInstrument` — symbol ใหม่สองพอร์ตพร้อมกัน: instruments แถวเดียว ทั้งคู่ 201
  - `TestConcurrentDeleteOfPortfolioAndTrade` — ลบพอร์ตว่างกับสร้างรายการพร้อมกัน: (204, 404) หรือ (409, 201)

- [ ] **Step 3: รัน tests** — `make -s test-integration` อย่างน้อย 3 รอบ; ทุกรอบต้องไม่มี SKIP/flake ถ้าเห็นบั๊กให้เขียนเคสที่ reproduce ก่อนแก้
- [ ] **Step 4: ตรวจ race** — `go test ./... -race -count=1 && make lint`
- [ ] **Step 5: Commit** — `git commit -m "test(transaction): prove history, idempotency and isolation rules under concurrency"`

---

### Task 11: ประกอบ dependency, คู่มือ และสถานะ

**Files:**
- Modify: `cmd/api/wire.go`, `cmd/api/wire_test.go`, `cmd/api/main.go`, `README.md`, `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md`, `docs/superpowers/specs/2026-10-05-phase-1-design.md`

**Interfaces:**
- Consumes: `service.NewTransactions`, `demo.NewInstruments`, `buildRateLimits`
- Produces:
  - `func buildTransactions(cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) (httpadapter.TransactionOptions, error)`
  - `func instrumentProvider(cfg config.Config) port.InstrumentProvider` — production คืน `noInstrumentProvider{}` ซึ่ง `Lookup` คืน `domain.ErrDependencyUnavailable` (milestone 8 แทนที่ด้วย Alpaca); นอก production คืน `demo.NewInstruments()`

- [ ] **Step 1: เขียน failing tests**
  - `TestBuildServesTheTransactionRoutes` แบบเดียวกับ `TestBuildServesThePortfolioRoutes`
  - `TestProductionNeverUsesTheDemoInstruments` — config production: `Lookup("AAPL")` → `dependency_unavailable`; development: พบ AAPL

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./cmd/api/ -count=1` → compile fail
- [ ] **Step 3: เขียน wire และแก้ `main.go`**
- [ ] **Step 4: แก้เอกสาร**
  - README: สถานะ milestone, ต่อ curl flow หลังขั้น 15 บนพอร์ต "ระยะสั้น": 16 ซื้อ AAPL ด้วย `Idempotency-Key` ได้ 201, 17 ส่งซ้ำด้วย key เดิมได้ body เดิม, 18 ขายเกินได้ 422 `insufficient_shares`, 19 ดูรายการ, 20 แก้ด้วย If-Match; ระบุข้อจำกัด "symbol ที่ยังไม่อยู่ใน instruments บันทึกไม่ได้ช่วง provider ล่ม", symbol ที่ใช้ได้ในโหมดเดโม และว่า production ยังไม่มี provider จนถึง milestone 8
  - spec: `Lookup(context.Context, domain.Symbol)`, `Repositories` เพิ่ม `Transactions()`/`Instruments()`, ขั้น 0 ของหัวข้อ 9 เพิ่มการตรวจเจ้าของก่อน rate limit, ตาราง error เพิ่มความหมายของ `insufficient_shares`/`invalid_history` และ `server_busy` ของ DB
  - roadmap: ติ๊ก checkbox ของ milestone 7, สถานะ "ลงมือครบแล้ว รอตรวจรับ" และแก้ข้อความของ milestone 6 เรื่อง CI ตามผลจริงบน GitHub ถ้าผู้ใช้แจ้ง

- [ ] **Step 5: รันทั้งหมด** — `make -s test-integration && go test ./... -race -count=1 && go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...` — govulncheck มีเฉพาะ GO-2026-6809, GO-2026-6629 ที่รู้จักอยู่แล้ว ถ้ามีรายการใหม่ให้หยุดแล้วแจ้ง
- [ ] **Step 6: Commit** — `git commit -m "feat(api): wire trades and document the buy and sell flow"`
- [ ] **Step 7: รัน `make check` หลัง commit** — ขั้น swagger ของ `make check` เทียบ `docs/api` กับ HEAD จึงผ่านได้เฉพาะเมื่อ commit ไฟล์ generated แล้ว; Expected: exit 0
