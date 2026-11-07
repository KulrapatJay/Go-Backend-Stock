# Milestone 9 — ตรวจรับ backend ทั้งระบบ Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** พิสูจน์ว่า backend เฟส 1 พร้อมให้ frontend เชื่อม: clone ใหม่แล้ว `cp .env.example .env && docker compose up --build` เปิดเดโมได้ทันที, flow หลักทั้งหมดผ่าน HTTP จริงแบบอัตโนมัติทั้งในเครื่องและ CI, Swagger ตรงกับ routes จริง, มีคำสั่ง retention cleanup ด้วย maintenance role และ README/ADR ครบสำหรับผู้อ่าน portfolio

**Architecture:** เพิ่ม composition root ที่สอง `cmd/maintenance` (one-shot) ซึ่งใช้ core service `service.Retention` ผ่าน port `port.RetentionRepository` ที่ implement ด้วย sqlc + pool ของ `app_maintenance`; ทดสอบ end-to-end ด้วย bash + curl + jq ยิง stack จาก `docker-compose.yml` (image ที่ build แล้ว ไม่ใช่ dev override) อ่าน OTP จาก Mailpit; CI เพิ่ม job `stack` ที่รันสคริปต์เดียวกันบน checkout ใหม่; Swagger ได้ test ที่เทียบ routes ของ Fiber กับ paths ใน spec

**Tech Stack:** Go 1.27.1, Fiber v3.5.0, pgx/sqlc v1.31.1, goose v3.28.0, PostgreSQL 17.11, Docker Compose, bash + curl + jq (มีใน ubuntu-24.04 และ macOS), GitHub Actions — ไม่มี Go dependency ใหม่ และไม่มี migration

**Spec:** [phase 1 spec](../specs/2026-10-05-phase-1-design.md) หัวข้อ 9 "Audit retention" (batch 1,000 แถว/transaction, audit > 90 วัน, idempotency หมดอายุ, sessions/otp_challenges/oauth_flows หมดอายุเกิน 1 วัน, rate events เก่ากว่า window สูงสุด 1 ชั่วโมงแล้วลบ keys ที่ว่างและหมดอายุ, ใช้ maintenance role เท่านั้น; production schedule เป็น GitHub Actions วันละครั้งเปิดใน milestone 11), 13 (Compose/config/CI: `MAINTENANCE_DATABASE_URL`, "ขั้น 9 ครบ backend E2E"), 14 (เกณฑ์ milestone 9) กับ [persistence design](../specs/2026-10-17-milestone-3-persistence-design.md) หัวข้อ 3.3 (สิทธิ์ `app_maintenance`) และ [AGENTS.md](../../../AGENTS.md) หัวข้อ Docker, CI, OpenAPI และ Application log/Audit log

**ผู้ใช้ตัดสินใจแล้ว (2026-11-06):**
1. ใส่ค่า dev กลับใน `.env.example` เพื่อให้ `cp .env.example .env && docker compose up --build` ใช้ได้ตรงตัว (ค่า secret ขึ้นต้น `dev-only-` ซึ่ง production ปฏิเสธอยู่แล้ว)
2. M9 ทำ `cmd/maintenance` + image + compose profile + `make maintenance` พร้อม tests; workflow แบบ schedule ทำใน milestone 11 ตอนมี Supabase
3. Branch protection/required checks: แผนเขียนขั้นตอน ผู้ใช้ตั้งค่าใน GitHub เอง; แผนเพิ่ม badge และ push trigger ของ `developer`
4. M8 merge เข้า `developer` แล้ว (PR #6) และ M9 ทำบน branch `review-code` ที่ผู้ใช้เปิดให้

## Global Constraints

- core (`internal/core/**`) import ได้เฉพาะ standard library, `github.com/shopspring/decimal`, `github.com/google/uuid`, `golang.org/x/crypto/argon2` และ `go-backend-stock/internal/core/...` (`internal/core/boundary_test.go` ตรวจอยู่แล้ว)
- DI ประกอบที่ `cmd/api/main.go` และ `cmd/maintenance/main.go` เท่านั้น ไม่มี global state/logger
- Retention: batch ละ 1,000 แถว = หนึ่งคำสั่ง `DELETE` (autocommit) ต่อ batch; ลบเฉพาะแถวที่เวลา **น้อยกว่า** cutoff (เท่ากับ cutoff ไม่ลบ); cutoff คำนวณจาก `Clock.Now()` ใน UTC ครั้งเดียวต่อการรัน
- cutoff ต่อตาราง: `audit_events.occurred_at < now − AUDIT_RETENTION_DAYS` (ค่าเริ่มต้น 90, ช่วง 30–365), `idempotency_keys.expires_at < now`, `sessions.expires_at < now − 24h`, `otp_challenges.expires_at < now − 24h`, `oauth_flows.expires_at < now − 24h`, `rate_limit_events.occurred_at < now − 1h`, `rate_limit_keys.expires_at < now` และไม่มี event อ้างอิง; ลบ rate events ก่อน rate keys เสมอ
- maintenance ไม่เขียน audit (role ไม่มี INSERT บน `audit_events`) — สรุปผลเป็น application log JSON `event=maintenance.retention_finished` พร้อมจำนวนต่อตารางและ `duration_ms`; ล้มเหลว = `event=maintenance.retention_failed` + exit 1
- ห้าม log/echo connection string, password, secret ทั้งใน Go, สคริปต์ และ CI output
- สคริปต์ E2E ใช้ `docker compose -p <project> -f docker-compose.yml` เสมอ และ**ปฏิเสธ project `stock-portfolio`** (stack dev ของผู้ใช้) — ห้ามแตะ container/volume ของผู้ใช้; `down -v` เฉพาะ project ของสคริปต์เอง
- อ่าน OTP จาก Mailpit ด้วยการค้นตามผู้รับ (`GET /api/v1/search?query=to:<email>`) ไม่ใช้ `message/latest`
- ห้ามแก้ไฟล์ใน `docs/api/` ด้วยมือ; แก้ annotations แล้ว `make swagger`
- **`make -s test-integration` ล้างทุกตาราง schema `app` ใน dev DB** และ `make e2e` เขียนบัญชีทดลองลง dev DB ของผู้ใช้ — บอกผู้ใช้ทุกครั้งที่รัน
- ห้ามแก้ `.env` ของผู้ใช้; ห้าม push หรือเปลี่ยน settings บน GitHub
- ต้องเห็น test ล้มก่อนแล้วจึงเขียนโค้ดทุก task ที่เป็น Go; จบ task ด้วย `gofmt -l internal cmd` ที่ไม่พิมพ์อะไร, `go vet ./...` และ `make lint` ผ่าน
- commit message ไม่มี `Co-Authored-By` หรือชื่อ Claude; commit ในเครื่องเท่านั้น; `git add` เฉพาะไฟล์ของ task

## การตัดสินใจที่แผนนี้เสนอ (รอผู้ใช้ยืนยันตอนรีวิวแผน)

1. **`AUDIT_RETENTION_DAYS` เป็นจำนวนวันเต็ม (30–365)** แทน duration ของ Go เพราะ `time.ParseDuration` ไม่รู้จัก `d` และเพดานล่าง 30 วันกันการตั้งผิดจนลบ audit เกือบหมด
2. **E2E เป็น bash + curl + jq ยิง stack ที่ build จาก `docker-compose.yml`** ไม่ใช่ Go test: ตรวจ image จริง, migrate service, Mailpit และ README flow ได้ในคราวเดียว แลกกับการที่ CI ช้าลงราว 3–5 นาที และ dev override (air) ไม่ถูกตรวจใน CI
3. **ตรวจ failure 3 แบบใน stack check:** migration เสีย (API ไม่เริ่ม), config production ไม่ผ่าน (API ออกพร้อมชื่อตัวแปรแต่ไม่มีค่า), Alpaca เข้าไม่ได้ (valuation `unavailable`, symbol ใหม่ `503`, symbol เดิมบันทึกได้) — ไม่ทดสอบ SMTP ล่มใน stack เพราะ HTTP tests ของ milestone 4–5 ครอบคลุมแล้ว
4. **พอร์ตของ compose ปรับได้ด้วย `API_HOST_PORT`, `MAILPIT_UI_HOST_PORT`, `MAILPIT_SMTP_HOST_PORT`** (ค่าเริ่มต้นเดิม) เพื่อให้ fresh-clone check รันคู่กับ stack dev ของผู้ใช้ได้
5. **จำกัด log ของ Docker** ทุก service ด้วย `json-file` `max-size: 10m`, `max-file: 3` (ไม่เกิน 30 MB ต่อ container)
6. **rate key ที่ API กำลังใช้พร้อมกับ cleanup** (FK `23503`) ไม่ถือเป็นความล้มเหลว: repository คืน `0` แล้ว service หยุดตารางนั้นในรอบนี้ (รอบหน้าค่อยลบ) — role maintenance ไม่มี UPDATE จึงใช้ `FOR UPDATE SKIP LOCKED` ไม่ได้
7. **ข้อมูลเดโม** สร้างผ่าน API จริงด้วย `make demo-data` (บัญชี `demo-investor@example.com` / `Zq9!kVx1`, สองพอร์ต, ราคาเดโม) ไม่ใช่ SQL seed เพื่อให้ผ่าน validation/audit เหมือนผู้ใช้จริง
8. **ADR ใหม่ห้าฉบับ** สำหรับการตัดสินใจของ milestone 4–9 ที่ยังไม่มี ADR (ดู Task 5)
9. **CI:** เพิ่ม `developer` ใน push trigger, ขั้น goose down-to 0 แล้ว up ซ้ำ, build image `maintenance` และ job `stack`; ยังไม่ใส่ govulncheck ใน CI (ยังมีสองรายการที่ยอมรับไว้ GO-2026-6809, GO-2026-6629 ซึ่งต้องมี allowlist; รันด้วยมือเหมือนเดิม)

## Review Focus

1. **Cleanup ชนกับ API ที่กำลังนับ rate limit ของ key เดียวกัน** — maintenance ต้องไม่ล้มด้วย FK และต้องไม่ลบ key ที่มี event ใหม่ — Task 2 `TestIdleRateLimitKeyInUseIsSkipped`
2. **แถวที่อายุพอดี cutoff** (audit อายุ 90 วันพอดี, session หมดอายุมา 24 ชั่วโมงพอดี) — ต้องไม่ถูกลบ; เก่ากว่า 1 ไมโครวินาทีจึงลบ — Task 2 `TestRetentionDeletesOnlyRowsOlderThanTheCutoff`
3. **รัน fresh-clone check ขณะ stack dev ของผู้ใช้เปิดอยู่** — ต้องไม่ชนพอร์ตและไม่ลบ volume `stock-portfolio_pgdata` — Task 4 Step 4 (ตรวจ volume ของผู้ใช้ยังอยู่หลังรัน)
4. **route ที่มีจริงแต่ไม่อยู่ใน Swagger หรือกลับกัน** (รวม HEAD ที่ Fiber สร้างให้) — Task 3 `TestEveryRouteIsDocumented`
5. **Mailpit มีอีเมลหลายฉบับจากหลายบัญชีพร้อมกัน** (รัน e2e ซ้ำ หรือ CI) — flow ต้องได้ OTP ของบัญชีตัวเองเสมอ — Task 4 `flow.sh` อ่านตามผู้รับ และรันสอง flow ซ้อนกันใน Step 4

---

## File Structure

| ไฟล์ | หน้าที่ | Task |
| --- | --- | --- |
| `.env.example`, `docker-compose.yml`, `internal/config/env_example_test.go` | ค่า dev ที่เปิดระบบได้, พอร์ตปรับได้, จำกัด log | 1 |
| `internal/core/port/retention.go`, `internal/core/service/retention.go` (+test), `internal/adapter/postgres/queries/retention.sql`, `internal/adapter/postgres/retention.go` (+test), `internal/config/maintenance.go` (+test), `cmd/maintenance/main.go`, `Dockerfile`, `docker-compose.yml`, `Makefile`, `.env.example` | retention cleanup | 2 |
| `cmd/api/main.go`, `internal/adapter/http/route_contract_test.go`, `internal/adapter/http/contract_test.go`, `docs/api/*` | Swagger ตรงกับ routes และไม่มี credentials | 3 |
| `scripts/e2e/flow.sh`, `scripts/e2e/stack-check.sh`, `scripts/e2e/demo-data.sh`, `Makefile`, `.github/workflows/ci.yml` | E2E, fresh clone, demo data, CI | 4 |
| `docs/adr/0017…0021-*.md`, `docs/adr/README.md`, `README.md`, `docs/runbooks/github-branch-protection.md` | ADRs และคู่มือ | 5 |
| roadmap, spec | สถานะและหลักฐาน | 6 |

---

### Task 1: Fresh clone เปิดเดโมได้ด้วยคำสั่งเดียว

**Files:**
- Modify: `.env.example`, `docker-compose.yml`
- Create: `internal/config/env_example_test.go`

**Interfaces:**
- Produces: `.env.example` ที่มีค่า dev ครบ; compose รับ `API_HOST_PORT` (8080), `MAILPIT_UI_HOST_PORT` (8025), `MAILPIT_SMTP_HOST_PORT` (1025); `x-logging` anchor ที่ทุก service ใช้

- [ ] **Step 1: เขียน failing test** `TestEnvExampleStartsTheAPIInDevelopment` — อ่าน `../../.env.example` (บรรทัด `KEY=VALUE`, ข้าม comment/บรรทัดว่าง) แล้วเติม `DATABASE_URL=postgres://app_runtime:<APP_RUNTIME_PASSWORD>@postgres:5432/<POSTGRES_DB>?sslmode=disable` แบบที่ compose ทำ แล้ว `config.Load` ต้องไม่มี error, `Env == development`, `PriceProvider == demo`, `DocsEnabled == true`; และ `TestEnvExampleSecretsAreRejectedInProduction` — ค่าเดียวกันแต่ `APP_ENV=production` ต้องได้ error ที่มีชื่อ `CSRF_SECRET`, `OTP_SECRET`, `RATE_LIMIT_SECRET`, `OIDC_SECRET` และไม่มีค่า secret ใดในข้อความ
- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/config/ -run TestEnvExample -count=1` → FAIL (`APP_ENV must be one of …`)
- [ ] **Step 3: ใส่ค่าใน `.env.example`** — `APP_ENV=development`, `PUBLIC_ORIGIN=http://localhost:8080`, `LOG_LEVEL=info`, `DOCS_ENABLED=true`, `DOCS_TRY_IT_OUT_ENABLED=true`, `POSTGRES_DB=stock`, `POSTGRES_ADMIN_USER=postgres`, `POSTGRES_ADMIN_PASSWORD=dev_admin_password`, `POSTGRES_HOST_PORT=55432`, `APP_MIGRATOR_PASSWORD=dev_migrator_password`, `APP_RUNTIME_PASSWORD=dev_runtime_password`, `APP_MAINTENANCE_PASSWORD=dev_maintenance_password`, `CSRF_SECRET=dev-only-csrf-secret-0123456789abcdef`, `OTP_SECRET=dev-only-otp-secret-0123456789abcdefgh`, `RATE_LIMIT_SECRET=dev-only-rate-limit-secret-0123456789ab`, `OIDC_SECRET=dev-only-oidc-secret-0123456789abcdefg`, `PRICE_PROVIDER=demo`; Google/Alpaca ว่างเหมือนเดิม; คง comment บรรทัดแรกและเพิ่ม comment ว่าค่าเหล่านี้ใช้ได้เฉพาะเครื่อง dev และ production ปฏิเสธ `dev-only-`
- [ ] **Step 4: แก้ `docker-compose.yml`** — พอร์ต `127.0.0.1:${API_HOST_PORT:-8080}:8080`, `127.0.0.1:${MAILPIT_UI_HOST_PORT:-8025}:8025`, `127.0.0.1:${MAILPIT_SMTP_HOST_PORT:-1025}:1025`; เพิ่ม `x-logging: &logging {driver: json-file, options: {max-size: "10m", max-file: "3"}}` แล้ว `logging: *logging` ทุก service
- [ ] **Step 5: รัน test ให้ผ่าน** — `go test ./internal/config/ -count=1 && docker compose config --quiet && docker compose -f docker-compose.yml config | grep -c 'max-size: 10m'` (Expected: เท่ากับจำนวน service = 4) แล้ว `make lint`
- [ ] **Step 6: Commit** — `git commit -m "chore(env): ship working dev defaults so a fresh clone starts with one command"`

---

### Task 2: Retention cleanup ด้วย `cmd/maintenance`

**Files:**
- Create: `internal/core/port/retention.go`, `internal/core/service/retention.go`, `internal/core/service/retention_test.go`, `internal/adapter/postgres/queries/retention.sql`, `internal/adapter/postgres/retention.go`, `internal/adapter/postgres/retention_test.go`, `internal/config/maintenance.go`, `internal/config/maintenance_test.go`, `cmd/maintenance/main.go`
- Modify: `internal/adapter/postgres/sqlc/*` (generated), `Dockerfile`, `docker-compose.yml`, `Makefile`, `.env.example`

**Interfaces:**
- Consumes: `port.Clock` (มีอยู่แล้ว), `postgres.NewPool(ctx, url, maxConns)`, `logging.New(w, level, service)`
- Produces:
  - `port.RetentionRepository` — ทุก method `(ctx context.Context, before time.Time, limit int) (int64, error)` คืนจำนวนแถวที่ลบ: `DeleteAuditEventsBefore`, `DeleteIdempotencyKeysExpiredBefore`, `DeleteSessionsExpiredBefore`, `DeleteOTPChallengesExpiredBefore`, `DeleteOIDCFlowsExpiredBefore`, `DeleteRateLimitEventsBefore`, `DeleteIdleRateLimitKeys` (`before` = เทียบกับ `expires_at` และต้องไม่มี event)
  - `service.RetentionPolicy struct { AuditRetention time.Duration; BatchSize int }`; `service.NewRetention(repo port.RetentionRepository, clock port.Clock, policy RetentionPolicy) *Retention`
  - `service.RetentionReport struct { AuditEvents, IdempotencyKeys, Sessions, OTPChallenges, OIDCFlows, RateLimitEvents, RateLimitKeys int64 }`
  - `(*Retention).Run(ctx context.Context) (RetentionReport, error)` — ตามลำดับฟิลด์ของ report; แต่ละตารางเรียกซ้ำจนได้น้อยกว่า `BatchSize`; error ห่อด้วยชื่อตาราง (เช่น `retention sessions: …`) และคืน report ที่ทำได้ก่อนหน้า; ctx ยกเลิก = หยุดทันทีพร้อม `ctx.Err()`
  - `postgres.NewRetentionRepository(pool *pgxpool.Pool) *RetentionRepository` — `DeleteIdleRateLimitKeys` ที่ได้ SQLSTATE `23503` คืน `(0, nil)`
  - `config.MaintenanceConfig struct { Env Environment; DatabaseURL string; LogLevel slog.Level; AuditRetention time.Duration; Timeout time.Duration }`, `config.LoadMaintenance(getenv func(string) string) (MaintenanceConfig, error)` — `MAINTENANCE_DATABASE_URL` บังคับ (production ต้อง `sslmode=verify-full`, ใช้กติกาเดียวกับ `parseDatabaseURL` แต่ข้อความระบุชื่อ `MAINTENANCE_DATABASE_URL`), `AUDIT_RETENTION_DAYS` ค่าเริ่มต้น 90 ช่วง 30–365, `MAINTENANCE_TIMEOUT` ค่าเริ่มต้น 10m ช่วง 1m–1h, `LOG_LEVEL`/`APP_ENV` กติกาเดียวกับ API; ไม่อ่าน secret อื่นของ API
  - SQL (ใน `retention.sql`) รูปแบบเดียวกันทุกตาราง: `DELETE FROM app.<table> WHERE <pk> IN (SELECT <pk> FROM app.<table> WHERE <col> < $1 ORDER BY <col> LIMIT $2)`; `idempotency_keys` ใช้ PK สี่คอลัมน์ `(portfolio_id, user_id, operation, key)`; rate keys เพิ่ม `AND NOT EXISTS (SELECT 1 FROM app.rate_limit_events e WHERE e.key_hash = k.key_hash)` — ใช้เฉพาะคอลัมน์ที่ `app_maintenance` มี SELECT

- [ ] **Step 1: เขียน failing tests ของ core และ config**
  - `TestRetentionUsesTheCutoffOfEachTable` (fake repo บันทึก `before` ที่ได้, clock `2026-11-06T03:00:00Z`, policy 90 วัน): audit `2026-08-08T03:00:00Z`, idempotency `2026-11-06T03:00:00Z`, sessions/otp/oidc `2026-11-05T03:00:00Z`, rate events `2026-11-06T02:00:00Z`, rate keys `2026-11-06T03:00:00Z`; `limit` = 1000 ทุกครั้ง
  - `TestRetentionRepeatsUntilABatchIsShort` — sessions คืน 1000, 1000, 3 → เรียก 3 ครั้ง, `report.Sessions == 2003`; ตารางที่คืน 0 เรียกครั้งเดียว
  - `TestRetentionDeletesRateEventsBeforeKeys` — ลำดับการเรียกของ fake ตรงกับลำดับฟิลด์ของ report
  - `TestRetentionStopsAtTheFirstError` — otp คืน error → error มี `otp_challenges`, report มีค่าของ audit/idempotency/sessions และไม่เรียก oidc/rate
  - `TestRetentionStopsWhenTheContextEnds` — ctx ยกเลิกหลัง batch แรกของ audit → `errors.Is(err, context.Canceled)`
  - `TestLoadMaintenanceDefaults` (90 วัน, 10m), `TestLoadMaintenanceRanges` (`AUDIT_RETENTION_DAYS` = 29, 366, `abc` → error ที่มีชื่อตัวแปร; `MAINTENANCE_TIMEOUT=2h` → error), `TestLoadMaintenanceRequiresTheDatabaseURL` (ว่าง → error มี `MAINTENANCE_DATABASE_URL`; production ไม่มี `verify-full` → error; ข้อความไม่มี password ของ URL)
- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/core/service/ ./internal/config/ -run 'Retention|Maintenance' -count=1` → compile fail
- [ ] **Step 3: เขียน port, service และ config**
- [ ] **Step 4: เขียน failing integration tests** ใน `retention_test.go` (admin seed แถวด้วยเวลาที่กำหนดเอง, เรียก repository ด้วย pool ของ `TEST_MAINTENANCE_DATABASE_URL`)
  - `TestRetentionDeletesOnlyRowsOlderThanTheCutoff` — ทุกตาราง: แถวเวลา `cutoff − 1µs` ถูกลบ, แถวเวลา `cutoff` พอดีและใหม่กว่ายังอยู่
  - `TestRetentionBatchRespectsTheLimit` — audit 5 แถวที่เก่า, `limit 2` → คืน 2 เหลือ 3
  - `TestIdleRateLimitKeysKeepKeysWithEvents` — key หมดอายุที่ยังมี event ไม่ถูกลบ; key หมดอายุที่ไม่มี event ถูกลบ; key ที่ยังไม่หมดอายุไม่ถูกลบ
  - `TestIdleRateLimitKeyInUseIsSkipped` — admin เปิด transaction แล้ว insert event ของ key ที่หมดอายุและไม่มี event (ยังไม่ commit); goroutine เรียก `DeleteIdleRateLimitKeys`; commit transaction → ได้ `(0, nil)` และ key กับ event ยังอยู่
  - `TestRuntimeRoleCannotRunRetention` — repository ที่ใช้ pool ของ `TEST_RUNTIME_DATABASE_URL` เรียก `DeleteAuditEventsBefore` → error SQLSTATE `42501`
  - `TestRetentionNeverTouchesBusinessTables` — หลัง `service.Retention.Run` กับ repository จริง จำนวนแถวของ users/portfolios/transactions/instruments เท่าเดิม
- [ ] **Step 5: รัน test ให้เห็นว่าล้ม** — `make -s test-integration` → compile fail (แจ้งผู้ใช้ว่าล้าง dev DB)
- [ ] **Step 6: เขียน `retention.sql`, `make sqlc`, เขียน `retention.go`**
- [ ] **Step 7: เขียน `cmd/maintenance/main.go`** — `run() error`: `LoadMaintenance(os.Getenv)` → `logging.New(os.Stdout, cfg.LogLevel, "stock-maintenance")` → `context.WithTimeout(signal ctx, cfg.Timeout)` → `NewPool(ctx, cfg.DatabaseURL, 2)` → `service.NewRetention(..., RetentionPolicy{AuditRetention: cfg.AuditRetention, BatchSize: 1000}).Run` → log ตาม Global Constraints (key ของ log เป็น snake_case ตามชื่อตาราง เช่น `audit_events`, `rate_limit_keys`); error ของ config พิมพ์ไป stderr แบบ `cmd/api`
- [ ] **Step 8: แพ็กและเรียกใช้** — `Dockerfile`: stage `build` build `./cmd/maintenance` เป็น `/out/maintenance` ด้วย และเพิ่ม stage `maintenance` (distroless nonroot เดียวกับ runtime, `ENTRYPOINT ["/usr/local/bin/maintenance"]`); compose: service `maintenance` (`profiles: [maintenance]`, `build.target: maintenance`, `MAINTENANCE_DATABASE_URL: postgres://app_maintenance:${APP_MAINTENANCE_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable`, `APP_ENV`, `LOG_LEVEL`, `AUDIT_RETENTION_DAYS: ${AUDIT_RETENTION_DAYS:-}`, depends_on `migrate` completed, `restart: "no"`, `logging: *logging`); `.env.example` เพิ่ม `AUDIT_RETENTION_DAYS=`; Makefile `maintenance:` = `docker compose --profile maintenance run --rm --build maintenance`
- [ ] **Step 9: รันทั้งหมด** — `gofmt -l internal cmd; go vet ./... && go test ./... -race -count=1 && make -s test-integration && make sqlc-clean && docker compose config --quiet && make lint` แล้ว `docker compose up -d postgres migrate && make maintenance` (Expected: exit 0 และบรรทัด JSON `"event":"maintenance.retention_finished"`)
- [ ] **Step 10: Commit** — `git commit -m "feat(maintenance): delete expired auth state and audit older than the retention as the maintenance role"`

---

### Task 3: Swagger ตรงกับ routes จริงและไม่มี credentials

**Files:**
- Create: `internal/adapter/http/route_contract_test.go`
- Modify: `internal/adapter/http/contract_test.go`, `cmd/api/main.go` (general annotations), `docs/api/*` (generated)

**Interfaces:**
- Consumes: harness ของ `auth_harness_test.go` พร้อม `withValuation()` (integration) และ `loadSpec(t)` ใน `contract_test.go`

- [ ] **Step 1: เขียน failing tests**
  - `TestEveryRouteIsDocumented` (integration) — ทุก route จาก `app.GetRoutes(true)` ที่ method ไม่ใช่ `HEAD` และ path ไม่ขึ้นต้น `/docs` ต้องมีใน `spec.Paths` ด้วย method เดียวกัน (แปลง `:portfolio_id` → `{portfolio_id}`); และทุก operation ใน spec ต้องมี route จริง; `HEAD` ต้องมีคู่ `GET` เสมอ
  - `TestEveryOperationIsDescribed` — ทุก operation มี `summary`, `description`, tag หนึ่งใน `Health`, `Auth`, `Password Reset`, `Portfolios`, `Transactions`, `Holdings/Prices`; ทุก operation นอก `Health` ประกาศ response 4xx หรือ 5xx อย่างน้อยหนึ่งตัวที่ schema `$ref` เป็น error envelope (definition ที่มี `code`, `message`, `request_id` เป็น required)
  - `TestSpecHasNoNumbersForMoney` — ไม่มี property ใดใน definitions ที่ `type` เป็น `number`; property `integer` อนุญาตเฉพาะ allowlist ที่ไล่จาก spec ปัจจุบัน (เช่น `limit`, `sequence`, `version`, `retry_after` — ตรวจของจริงแล้วเขียนรายการตายตัวใน test)
  - `TestSpecHasNoCredentials` — `swagger.json` ไม่มี regex `(?i)(APCA-|GOCSPX-|postgres://[^"]*:[^"@]*@|-----BEGIN|sk_live_|apps\.googleusercontent\.com)` และไม่มีค่าใดที่ขึ้นต้น `dev-only-`
  - `TestSpecDescribesTheCurrentPhase` — `info.description` ไม่มีคำว่า `not implemented` และไม่มี `Milestone 8b`
- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/adapter/http/ -run 'Spec|Operation' -count=1` → FAIL อย่างน้อยที่ `TestSpecDescribesTheCurrentPhase` (description ปัจจุบันยังเขียนว่า "real prices are not implemented yet"); ถ้า test อื่นล้ม ให้แก้ annotation ของ handler ที่ผิด ไม่ผ่อน test
- [ ] **Step 3: แก้ `@description` ใน `cmd/api/main.go`** — บรรยายขอบเขตเฟส 1 ที่ทำครบแล้ว: auth (email/password, email verification, password reset, Google), portfolios, transactions, holdings และ valuation ด้วยราคาล่าช้า (demo หรือ Alpaca SIP ตาม `PRICE_PROVIDER`); ย้ายสอง operation ของ password reset (`internal/adapter/http/password_reset.go` ตอนนี้เป็น `@Tags Auth`) ไป `@Tags Password Reset` และเพิ่ม `@tag.name`/`@tag.description` ของ `Password Reset` ตามการจัดกลุ่มใน AGENTS.md (เปลี่ยนเฉพาะเอกสาร ไม่เปลี่ยน path/operationId) แล้ว `make swagger`
- [ ] **Step 4: รัน test ให้ผ่าน** — `make swagger && go test ./internal/adapter/http/ -count=1 && make -s test-integration && make lint`
- [ ] **Step 5: Commit** — `git commit -m "test(swagger): pin the spec to the real routes, error envelopes and decimal strings"`

---

### Task 4: E2E ผ่าน HTTP, fresh clone, ข้อมูลเดโม และ CI

**Files:**
- Create: `scripts/e2e/flow.sh`, `scripts/e2e/stack-check.sh`, `scripts/e2e/demo-data.sh`
- Modify: `Makefile`, `.github/workflows/ci.yml`

**Interfaces:**
- Produces:
  - `scripts/e2e/flow.sh full|provider-down` — env `API_ORIGIN` (ค่าเริ่มต้น `http://localhost:8080`), `MAILPIT_URL` (`http://localhost:8025`); ทุก request ส่ง `Origin: $API_ORIGIN` + `X-CSRF-Token` ตาม README; ทุก assertion ล้ม = พิมพ์ step, status ที่คาด/ได้ และ `request_id` แล้ว exit 1; อีเมลแต่ละ run สุ่ม (`e2e-<random>@example.com`); `set -euo pipefail`; ไม่พิมพ์ cookie หรือ OTP
  - `scripts/e2e/stack-check.sh [-p project] <dir>` — ปฏิเสธ project `stock-portfolio`; ค่าเริ่มต้น project `stock-check`, พอร์ต API 18080, Mailpit 18025/11025, Postgres 15432 (เขียนลง `<dir>/.env` ที่ copy จาก `.env.example` พร้อม `PUBLIC_ORIGIN` ที่ตรงพอร์ต); `trap` ที่ `down -v` เฉพาะ project นี้
  - `scripts/e2e/demo-data.sh` — env เดียวกับ `flow.sh`; login `demo-investor@example.com`/`Zq9!kVx1` ได้ = พิมพ์ว่ามีอยู่แล้วแล้วจบ; ไม่งั้นสมัคร → ยืนยัน → สร้างพอร์ต "ระยะยาว" (ซื้อ MSFT, NVDA, SPY) และ "เก็งกำไร" (ซื้อ/ขาย TSLA, AAPL) ด้วย `executed_at` ในอดีต
  - Makefile: `e2e` (`scripts/e2e/flow.sh full` กับ stack ที่เปิดอยู่), `demo-data`, `fresh-clone-check` (`git clone --quiet . <mktemp -d>` แล้ว `stack-check.sh` บน clone นั้น และลบ temp dir)

- [ ] **Step 1: เขียน `flow.sh full`** — ลำดับและผลที่ต้อง assert:
  1. `GET /readyz` 200
  2. register 202 → OTP จาก Mailpit ของผู้รับนี้ → confirm 204 → login ด้วยรหัสก่อนยืนยันของอีกบัญชีหนึ่งได้ 403 `email_not_verified` (สมัครบัญชีที่สองไว้ใช้ข้อ 7)
  3. login 200 มี cookie `session`; `GET /auth/me` 200; POST โดยไม่มี `X-CSRF-Token` ได้ 403 `csrf_failed` และ `Origin` ผิดได้ 403
  4. สร้างพอร์ต "ระยะยาว" และ "ระยะสั้น" 201 `ETag: "1"`; ชื่อซ้ำแบบตัวพิมพ์ต่าง 409 `portfolio_name_conflict`
  5. "ระยะสั้น": ซื้อ AAPL 2 @ 100 fee 2 แล้วขาย 0.5 @ 120 fee 1 (201 ทั้งคู่); ส่งซ้ำด้วย key เดิม payload เดิมได้ body เดิมทุกไบต์; key เดิม payload ต่าง 409 `idempotency_conflict`; ขาย 5 ได้ 422 `insufficient_shares`; "ระยะยาว": ซื้อ MSFT 1 @ 400 fee 0
  6. holdings "ระยะสั้น": AAPL quantity `1.5`, cost_basis `151.5`, average_cost `101`, realized_pnl `8.5` และไม่มี MSFT; valuation: coverage `complete`, market_value `300`, unrealized_pnl `148.5`, `quote.is_demo` true (เทียบเป็น string ตามที่ API ส่ง — ถ้ารูปแบบตัวเลขต่างจากนี้ ให้ยึดของ API และแก้ README ขั้น 22–23 ให้ตรงใน Task 5); holdings "ระยะยาว" มีแค่ MSFT
  7. บัญชีที่สอง (ยืนยันแล้ว login แล้ว) อ่าน/แก้/ลบพอร์ตและรายการของบัญชีแรกได้ 404 `not_found` ทุกเส้น
  8. PUT พอร์ตด้วย `If-Match: "1"` 200 แล้วซ้ำด้วย `"1"` 412; ไม่ส่ง If-Match 428
  9. logout 204 → `me` 401; login อีก cookie jar ไว้ (session B)
  10. password reset request 202 → OTP ของผู้รับ → confirm 204 → session B ได้ 401; รหัสเก่า 401; รหัสใหม่ 200
- [ ] **Step 2: เขียน `flow.sh provider-down`** — ผู้ใช้ใหม่ + พอร์ต: ซื้อ AAPL (instrument มีแล้วจาก `full`) 201; ซื้อ BRK.B 503 `dependency_unavailable`; valuation 200 coverage `unavailable`, `market_value` null; holdings 200
- [ ] **Step 3: เขียน `stack-check.sh`** — ลำดับ: (1) `cp .env.example .env` + พอร์ต/`PUBLIC_ORIGIN` → `up --build -d` → รอ `/readyz` 200 ไม่เกิน 180 วินาที (poll ทุก 2 วินาที; ไม่ใช้ `--wait` เพราะ `migrate` จบแล้ว exit) → `flow.sh full` สองตัวพร้อมกัน (`&` แล้ว `wait` ทั้งคู่ต้อง exit 0) (2) `docker compose --profile maintenance run --rm maintenance` exit 0 และ log มี `maintenance.retention_finished` (3) ตั้ง `PRICE_PROVIDER=alpaca`, `ALPACA_KEY=e2e-fake`, `ALPACA_SECRET=e2e-fake`, `ALPACA_TRADING_BASE_URL=http://127.0.0.1:9`, `ALPACA_DATA_BASE_URL=http://127.0.0.1:9` → `up -d --force-recreate api` → readyz → `flow.sh provider-down` → คืนค่าเดิม (4) `APP_ENV=production` → recreate api → ภายใน 30 วินาที container ไม่ running, exit code ≠ 0, `logs api` มี `CSRF_SECRET` และไม่มี `dev-only-` → คืนค่า (5) `stop api` → สร้าง `migrations/99999_e2e_broken.sql` (`-- +goose Up` + `SELECT broken_on_purpose;`) → `up -d` ต้อง exit ≠ 0, `api` ไม่ running และ `app.goose_db_version` ไม่มี `99999` (อ่านด้วย `exec postgres psql`) → ลบไฟล์ (6) พิมพ์ `stack check passed`
- [ ] **Step 4: รันในเครื่อง** — แจ้งผู้ใช้ก่อนว่าจะ build/รัน stack แยก project `stock-check` บนพอร์ต 18080/18025/11025/15432 และลบเมื่อจบ; `docker volume ls --format '{{.Name}}' > before.txt` → `make fresh-clone-check` (Expected: `stack check passed`, exit 0) → volume `stock-portfolio_pgdata` (ถ้ามีใน before.txt) ยังอยู่ และไม่มี volume `stock-check_*` เหลือ
- [ ] **Step 5: เขียน `demo-data.sh` และรัน** — แจ้งผู้ใช้ว่าจะเขียนบัญชีเดโมลง dev DB ของ stack ที่เปิดอยู่; รันสองครั้ง: ครั้งแรกสร้าง, ครั้งที่สองพิมพ์ว่ามีอยู่แล้ว
- [ ] **Step 6: แก้ CI** — trigger `push.branches: [main, developer]`; job `backend` เพิ่มขั้น "Migrations roll back and apply again" (`$(GOOSE) … down-to 0` แล้ว `make migrate-up`) หลัง "Migrations apply to an empty database" และ "Build maintenance image" (`--target maintenance`); job ใหม่ `stack` (`runs-on: ubuntu-24.04`, `timeout-minutes: 25`, `needs: backend`) — checkout แล้ว `scripts/e2e/stack-check.sh -p stock-ci .`; ตรวจด้วย `actionlint` ถ้ามีในเครื่อง ไม่งั้น `docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:1.7.7`
- [ ] **Step 7: Commit** — `git commit -m "test(e2e): drive the whole backend flow, provider outage, bad config and broken migration through compose"`

---

### Task 5: ADRs และ README

**Files:**
- Create: `docs/adr/0017-rate-limits-in-postgres.md`, `0018-idempotency-keys.md`, `0019-google-oidc-explicit-linking.md`, `0020-delayed-prices-and-valuation-coverage.md`, `0021-retention-maintenance-job.md`, `docs/runbooks/github-branch-protection.md`
- Modify: `docs/adr/README.md`, `README.md`

- [ ] **Step 1: เขียน ADR ทั้งห้าตาม `0000-template.md`** (บริบท → ทางเลือก → การตัดสินใจ → ผลที่ตามมา; สถานะ Accepted พร้อมวันที่ผู้ใช้ยืนยันเดิม; อ้างแผน/spec ที่ตัดสินใจไว้ ไม่คิดเหตุผลใหม่):
  - 0017 — rate limit state ใน PostgreSQL (keys/events, admission limit, รวม `security.rate_limited` ต่อ key ใน 1 นาที) แทน Redis/in-memory; spec หัวข้อ 10 และ persistence design
  - 0018 — Idempotency-Key บังคับตอนสร้างรายการ, ผูก user+portfolio+operation, เก็บ response 24 ชั่วโมง, key เดิม payload ต่าง 409; แผน 7
  - 0019 — Google OIDC: Authorization Code + PKCE S256, state/nonce ผูก browser ใช้ครั้งเดียว, ไม่เชื่อมบัญชีตาม email อัตโนมัติ (`link_required`), Google-only ไม่มีรหัสผ่าน; spec milestone 4 และแผน 4c
  - 0020 — ราคาอ้างอิง = close ของแท่ง 1 นาที SIP ก่อน now−15m ใน regular session ตามปฏิทิน, `session_status`/`stale`/`cached`, cache 60s + outage 7 วัน, ไม่ถอยไปใช้ demo, ราคาที่ไม่มีเป็น null และ `coverage`; แผน 8b/8c
  - 0021 — retention ด้วย one-shot `cmd/maintenance` ใน role แยก ลบ batch 1,000 ไม่เขียน audit ของตัวเอง, schedule ด้วย GitHub Actions ใน milestone 11; แผนนี้
  - เพิ่มห้าแถวใน `docs/adr/README.md`
- [ ] **Step 2: เขียน runbook branch protection** — ขั้นตอนใน GitHub Settings → Branches → rule ของ `developer` และ `main`: require PR, require status checks `backend` และ `stack` (ชื่อ job ใน `ci.yml`), require branches up to date, ไม่อนุญาต force push/delete; บอกว่าต้องให้ CI รันอย่างน้อยหนึ่งครั้งก่อนชื่อ check จะให้เลือก
- [ ] **Step 3: ปรับ README** (เนื้อหาเดิมที่ยังถูกต้องคงไว้; ไม่ใส่ credentials จริง)
  - บรรทัดแรก: badge `![CI](https://github.com/KulrapatJay/<repo>/actions/workflows/ci.yml/badge.svg?branch=developer)` (ชื่อ repo จาก `git remote get-url origin`)
  - สถานะ: backend เฟส 1 ครบ (milestone 9 ลงมือครบ รอตรวจรับ), frontend เริ่ม milestone 10
  - "เริ่มใช้งาน": `cp .env.example .env && docker compose up --build` ใช้ได้ทันทีด้วยราคาเดโม; แยกหัวข้อ dev (โหลด override, air) กับ production-like (`-f docker-compose.yml`) ชัดเจน; `make demo-data` พร้อมบัญชีเดโม
  - "สถาปัตยกรรม": Mermaid `flowchart` (HTTP adapter → core services → ports ← postgres/alpaca/demo/identity/mail adapters; `cmd/api` และ `cmd/maintenance` เป็น composition roots) และ Mermaid `erDiagram` ของตารางใน `docs/database/schema.dbml` พร้อมลิงก์ไฟล์ DBML
  - "API": ตาราง endpoints ตาม tag ชี้ไป `/docs/`; curl flow เดิม (ปรับค่าให้ตรงผลจริงจาก Task 4) และบอกว่า `make e2e` คือ flow เดียวกันแบบอัตโนมัติ
  - "ตรวจจริงกับจำลอง": ตาราง — Google OIDC ตรวจจริงบน browser 2026-10-29; Alpaca Assets 2026-11-04 และราคา SIP/ปฏิทิน 2026-11-05 ด้วย `make smoke-alpaca`; อีเมลตรวจกับ Mailpit เท่านั้น (SMTP จริงตรวจใน milestone 11); CI ใช้ fakes/fixtures ไม่เรียกบริการจริง; **ต้องตรวจบริการจริงครบอีกครั้งก่อนเปิดออนไลน์**
  - "Trade-offs": session vs JWT (ADR-0002), decimal string + numeric(28,10) (ADR-0003/0013), Supabase เฉพาะ DB + private schema + roles (ADR-0004), row lock + version/ETag (ADR-0009), timeouts/503 (ADR-0016) — ย่อหน้าละ 2–4 ประโยค
  - "Maintenance": `make maintenance`, สิ่งที่ลบและ cutoff, `AUDIT_RETENTION_DAYS`, production schedule ใน milestone 11; log จำกัด 10 MB × 3 ไฟล์ต่อ container
  - "พัฒนาและทดสอบ": เพิ่ม `make e2e`, `make fresh-clone-check`, `make demo-data`, `make maintenance` พร้อมคำเตือนว่าอันไหนเขียน/ล้างข้อมูลใน dev DB
  - ลิงก์ runbook branch protection
- [ ] **Step 4: ตรวจ** — ลิงก์สัมพัทธ์ทุกตัวใน README และ ADR ใหม่ชี้ไปไฟล์ที่มีอยู่ (`grep -oE '\]\(([^)#]+)' README.md docs/adr/00{17..21}-*.md` แล้ว `test -e` ทีละไฟล์); รัน curl flow ใน README ทั้งบล็อกด้วย `bash` กับ stack dev หนึ่งรอบ (แจ้งผู้ใช้ก่อนเพราะเขียนบัญชีลง dev DB) ทุกขั้นได้ status ตาม comment
- [ ] **Step 5: Commit** — `git commit -m "docs: record the remaining phase 1 decisions and rewrite the README for the finished backend"`

---

### Task 6: ตรวจรับทั้ง branch และบันทึกสถานะ

**Files:**
- Modify: `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md`, `docs/superpowers/specs/2026-10-05-phase-1-design.md`

- [ ] **Step 1: รันทั้งหมด** — `make check && make -s test-integration && make test-mail && make fresh-clone-check && go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...` (govulncheck อนุญาตเฉพาะ GO-2026-6809, GO-2026-6629; รายการใหม่ให้หยุดแล้วแจ้ง) และ `make smoke-alpaca` ด้วย key ใน `.env` ของผู้ใช้ (ไม่พิมพ์ค่า)
- [ ] **Step 2: ขอให้ผู้ใช้ตรวจบน browser** (ผู้ลงมือไม่ทำแทน): เปิด `http://localhost:8080/docs/` → `GET /auth/csrf` → register/confirm/login ผ่าน Try it out ด้วย cookie ของ browser → `POST /portfolios` ด้วย CSRF token → logout; และ Google login หนึ่งรอบถ้าตั้ง `GOOGLE_*` ไว้ — บันทึกวันที่และผลที่ผู้ใช้แจ้งใน roadmap
- [ ] **Step 3: แก้ roadmap** — เพิ่มบรรทัด "แผนลงมือ" ของ milestone 9 ที่ลิงก์ไฟล์นี้; สถานะ milestone 8 "ผ่านการตรวจรับ (merge เข้า developer ด้วย PR #6)"; ติ๊กข้อของ milestone 9 ที่มีหลักฐานจริงพร้อมวันที่/คำสั่งที่ผ่าน; ข้อ "ตั้ง backend required checks/branch protection" ติ๊กได้เมื่อผู้ใช้แจ้งว่าตั้งตาม runbook แล้วเท่านั้น; ข้อ CI บน GitHub ติ๊กเมื่อผู้ใช้แจ้งว่า job `backend` และ `stack` ผ่านหลัง push; สถานะ milestone 9 "ลงมือครบแล้ว รอตรวจรับ"
- [ ] **Step 4: แก้ spec** — หัวข้อ 9 "Audit retention": `AUDIT_RETENTION_DAYS`, กติกา cutoff "น้อยกว่า", การข้าม key ที่ชน FK, log ของ maintenance; หัวข้อ 13: service `maintenance` (profile), ตัวแปรพอร์ต, การจำกัด log, job `stack` ใน CI
- [ ] **Step 5: Commit** — `git commit -m "docs: record milestone 9 evidence and mark milestone 8 accepted"`
- [ ] **Step 6: รัน `make check` หลัง commit** — Expected: exit 0 และ `git status` สะอาด
