# Milestone 3a — Schema, roles, sqlc และ audit persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** สร้าง schema ทั้งหมดของ phase 1 (12 ตาราง) ด้วย goose พร้อม GRANT ของแต่ละ role, ตั้ง sqlc, เขียน mapper และการแปลง error ของ PostgreSQL, และ `AuditRepository` ที่ commit/rollback ไปกับข้อมูลใน transaction เดียว — เป็นแผนแรกจาก 3 แผนของ milestone 3

**Architecture:** migrations `00002`–`00004` เป็นแหล่งความจริงของ schema และ sqlc อ่านไฟล์เหล่านี้โดยตรง (ร่วมกับไฟล์ `sqlc_schema_app.sql` ที่ประกาศ schema `app` ซึ่ง bootstrap สร้าง); sqlc generate ชนิด `pgtype` แล้ว mapper ใน `internal/adapter/postgres` แปลงเป็นชนิดของ domain ผ่าน `Parse*` (ADR-0013); `Store.withinTx` เปิด transaction READ COMMITTED และสร้าง `txRepositories` ที่ตอนนี้มีเฉพาะ `Audit()` — แผน 3b/3c เพิ่ม repository จนครบแล้ว `Store` จึง implement `port.UnitOfWork`

**Tech Stack:** Go 1.27 (go.mod `go 1.27.1`), pgx v5.11.0 (`pgtype`, `pgconn`, `pgxpool`), sqlc v1.31.1 (cgo), goose v3.28.0, PostgreSQL 17.11, golangci-lint v2.14.0, actionlint (Docker, ตรวจในเครื่องเท่านั้น)

**Spec:** [docs/superpowers/specs/2026-10-17-milestone-3-persistence-design.md](../specs/2026-10-17-milestone-3-persistence-design.md) หัวข้อ 1–4 และ 7 (แผน 3a) ซึ่งอ้าง [phase 1 spec](../specs/2026-10-05-phase-1-design.md) หัวข้อ 7–9, [schema.dbml](../../database/schema.dbml) และ [AGENTS.md](../../../AGENTS.md)

## Global Constraints

- migrations ใช้เลขต่อเนื่อง (`00002`, `00003`, `00004`) และห้ามแก้ไฟล์ที่รันในสภาพแวดล้อมที่ใช้ร่วมกันแล้ว; ทุก object อยู่ใน schema `app`; ไม่มีอะไรใน `public`
- ทุก FK เป็น `ON DELETE RESTRICT`; `audit_events` ไม่มี FK; ชื่อ constraint ตามไฟล์ migration เพราะ `mapError` อ้างชื่อเหล่านั้น
- `app_runtime` บน `audit_events` มี INSERT เท่านั้น; `app_maintenance` มีเฉพาะ SELECT คอลัมน์ที่ cleanup ใช้ + DELETE; ไม่มีใครนอกจาก `app_migrator` ทำ DDL
- email เทียบตรงตามค่าที่ Go normalize แล้ว: ห้ามใช้ `lower()` กับ email ใน SQL
- เพดาน `octet_length(metadata::text) <= 9216` และ `octet_length(result::text) <= 17408` (เพดานจริง 8/16 KiB ตรวจใน Go)
- sqlc `v1.31.1` ติดตั้งด้วย `CGO_ENABLED=1`; ห้ามแก้ไฟล์ใน `internal/adapter/postgres/sqlc/` ด้วยมือ ให้แก้ query แล้ว `make sqlc`
- ไม่มี float ในเส้นทาง `numeric`; ค่าจาก DB ที่ parse ไม่ผ่านเป็น `internal` ไม่ใช่ `invalid_input`
- ข้อความของ domain error ห้ามมี SQL หรือค่าในแถว (pgx เก็บค่าใน `PgError.Detail`)
- integration tests ต่อเป็น `app_runtime` สำหรับ repository และใช้ admin เฉพาะเตรียม/ตรวจผล; ข้อมูลสุ่มต่อ test ไม่ล้างตาราง
- integration tests ในเครื่องต้องเปิด PostgreSQL ของ compose ก่อน (`docker compose up -d postgres`) และใช้ `make test-integration` ซึ่งอ่านรหัสผ่านจาก `.env`
- commit message ไม่มี `Co-Authored-By` หรือชื่อ Claude; commit ในเครื่องเท่านั้น ห้าม push
- รันทุกคำสั่งที่ root ของ repo บน branch ที่ผู้ใช้ใช้อยู่ (`feature/creat_login_and_register`); Go ในเครื่องคือ `/usr/local/go/bin/go` (1.27.2)

## Review Focus

1. **สิทธิ์ที่ให้เกินหรือขาด** — runtime อ่าน/แก้/ลบ audit ได้ หรือ maintenance อ่าน `metadata`/`token_hash` ได้ จะเป็นช่องรั่วที่ไม่มีใครเห็น — Task 2 `TestRuntimeRolePrivileges`, `TestMaintenanceRolePrivileges` (ตรวจทั้งสิ่งที่ทำได้และไม่ได้)
2. **runtime insert แถวที่มี identity column ไม่ได้** เพราะขาดสิทธิ์บน sequence ซึ่งจะไปพังใน milestone 3c — Task 2 `TestRuntimeCanInsertIdentityColumns`
3. **ข้อความ error รั่วค่าในแถว** เช่นอีเมลจาก `Detail` ของ unique violation — Task 3 `TestMapError`
4. **ตัวเลขเพี้ยนระหว่าง DB กับ domain** ที่ 18+10 หลัก หรือรับ NaN/Infinity — Task 3 `TestNumericRoundTripIsExact`, `TestNumericStringRejectsNonFinite`
5. **transaction commit บางส่วน** เมื่อ audit ล้ม, callback คืน error หรือ panic — Task 4 `TestWithinTxRollsBack*` พร้อม mutation step ที่พิสูจน์ว่า test จับการ commit ผิดได้

---

## File Structure

| ไฟล์ | หน้าที่ | Task |
| --- | --- | --- |
| `internal/core/domain/errors.go` (+test) | `Error.Is` ไม่ panic กับ nil; comment ของ sentinels ตรงกับ ports | 1 |
| `migrations/00002_auth.sql`, `00003_portfolio.sql`, `00004_audit_ratelimit.sql` | 12 ตาราง + GRANT | 2 |
| `internal/adapter/postgres/permissions_test.go`, `migrations_test.go` | สิทธิ์ของ role และตารางครบ | 2 |
| `docs/database/schema.dbml` | ER diagram ให้ตรง migrations | 2 |
| `Makefile`, `.github/workflows/ci.yml` | URL ของ maintenance role (Task 2); sqlc (Task 4) | 2, 4 |
| `internal/adapter/postgres/mapping.go` (+test) | pgtype ↔ domain, `fromStored` | 3 |
| `internal/adapter/postgres/errors.go` (+test) | `mapError` PostgreSQL → domain | 3 |
| `sqlc.yaml`, `internal/adapter/postgres/sqlc_schema_app.sql`, `queries/audit.sql`, `sqlc/` (generated) | sqlc | 4 |
| `internal/adapter/postgres/store.go`, `audit.go`, `store_test.go` | `Store.withinTx`, `AuditRepository` | 4 |
| spec/roadmap | เอกสาร | 5 |

---
### Task 1: `domain.Error.Is` ปลอดภัยกับ nil และ comment ของ sentinels

**Files:**
- Modify: `internal/core/domain/errors.go`
- Test: `internal/core/domain/errors_test.go`

**Interfaces:**
- Consumes: `domain.Error`, sentinels (milestone 2)
- Produces: `(*domain.Error).Is` คืน false เมื่อ target เป็น `(*domain.Error)(nil)`; adapters คืน sentinel ได้ตรงๆ (Task 3 `mapError` ใช้)

- [ ] **Step 1: เพิ่ม failing test** ต่อท้าย `internal/core/domain/errors_test.go`:

```go

func TestErrorIsWithNilTargetDoesNotPanic(t *testing.T) {
	var nilErr *domain.Error
	err := fmt.Errorf("load: %w", domain.ErrNotFound)
	if errors.Is(err, nilErr) {
		t.Error("errors.Is(not_found, (*domain.Error)(nil)) = true, want false")
	}
}
```

- [ ] **Step 2: รันให้เห็นว่าล้มเหลว**

Run: `go test -count=1 -run TestErrorIsWithNilTargetDoesNotPanic ./internal/core/domain/`
Expected: FAIL ด้วย `panic: runtime error: invalid memory address or nil pointer dereference`

- [ ] **Step 3: แก้ `errors.go`**

แก้ `internal/core/domain/errors.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
```

   ด้วย

```go
	t, ok := target.(*Error)
	return ok && t != nil && t.Code == e.Code
```

2. แทน

```go
// Sentinels for errors.Is. Compare by code; do not return these to share their messages.
```

   ด้วย

```go
// Sentinels for errors.Is. Adapters may return them as-is (e.g. a repository returns ErrNotFound);
// callers compare with errors.Is, which matches by code. Never modify a sentinel.
```

- [ ] **Step 4: รัน test และ lint**

Run: `go test -race -count=1 ./internal/core/... && make lint`
Expected: `ok` และ `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/core/domain
git commit -m "fix(core): make domain.Error.Is safe for nil targets and clarify sentinel use"
```

---

### Task 2: Migrations ทุกตาราง, GRANT ของแต่ละ role และ DBML

**Files:**
- Create: `migrations/00002_auth.sql`, `migrations/00003_portfolio.sql`, `migrations/00004_audit_ratelimit.sql`
- Test: `internal/adapter/postgres/permissions_test.go`, `internal/adapter/postgres/migrations_test.go`
- Modify: `Makefile`, `.github/workflows/ci.yml`, `docs/database/schema.dbml`

**Interfaces:**
- Consumes: `openPool`, `integrationURL`, `insufficientPrivilege` (มีอยู่แล้วใน `internal/adapter/postgres/*_test.go`), roles จาก `deploy/db/bootstrap.sql`
- Produces:
  - ตาราง `app.users`, `auth_providers`, `sessions`, `otp_challenges`, `oauth_flows`, `portfolios`, `instruments`, `transactions`, `idempotency_keys`, `audit_events`, `rate_limit_keys`, `rate_limit_events`
  - ชื่อ constraint ที่ Task 3 ใช้: `portfolios_user_name_unique`, `auth_providers_subject_key`, `auth_providers_user_provider_key`, `users_email_key`, `idempotency_keys_pkey`, `transactions_portfolio_id_fkey`, `audit_events_metadata_object`
  - env `TEST_MAINTENANCE_DATABASE_URL` ใน `make test-integration` และ CI

- [ ] **Step 1: เปิด PostgreSQL ของ compose และดู version ปัจจุบัน**

```bash
docker compose up -d postgres
set -a; . ./.env; set +a; ./bin/goose -dir migrations -table app.goose_db_version postgres "postgres://app_migrator:$APP_MIGRATOR_PASSWORD@127.0.0.1:$POSTGRES_HOST_PORT/$POSTGRES_DB?sslmode=disable" status
```

Expected: postgres `healthy` และ migration ล่าสุดที่ applied คือ `00001_baseline.sql` (ถ้า volume ใหม่ ให้ `docker compose up -d postgres migrate` ก่อน)

- [ ] **Step 2: เพิ่ม URL ของ maintenance role ใน Makefile และ CI**

แก้ `Makefile` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```make
TEST_RUNTIME_DATABASE_URL ?= postgres://app_runtime:$(APP_RUNTIME_PASSWORD)@$(LOCAL_DB_HOST)/$(POSTGRES_DB)?sslmode=disable
```

   ด้วย

```make
TEST_RUNTIME_DATABASE_URL ?= postgres://app_runtime:$(APP_RUNTIME_PASSWORD)@$(LOCAL_DB_HOST)/$(POSTGRES_DB)?sslmode=disable
TEST_MAINTENANCE_DATABASE_URL ?= postgres://app_maintenance:$(APP_MAINTENANCE_PASSWORD)@$(LOCAL_DB_HOST)/$(POSTGRES_DB)?sslmode=disable
```

2. แทน

```make
unexport TEST_ADMIN_DATABASE_URL TEST_RUNTIME_DATABASE_URL MIGRATION_DATABASE_URL
```

   ด้วย

```make
unexport TEST_ADMIN_DATABASE_URL TEST_RUNTIME_DATABASE_URL TEST_MAINTENANCE_DATABASE_URL MIGRATION_DATABASE_URL
```

3. แทน

```make
	TEST_RUNTIME_DATABASE_URL="$(TEST_RUNTIME_DATABASE_URL)" \
```

   ด้วย

```make
	TEST_RUNTIME_DATABASE_URL="$(TEST_RUNTIME_DATABASE_URL)" \
	TEST_MAINTENANCE_DATABASE_URL="$(TEST_MAINTENANCE_DATABASE_URL)" \
```

บรรทัดคำสั่งใต้ target ใน Makefile ต้องขึ้นต้นด้วย TAB จริง

แก้ `.github/workflows/ci.yml` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```yaml
      TEST_RUNTIME_DATABASE_URL: postgres://app_runtime:ci_runtime_password@localhost:5432/stock?sslmode=disable
```

   ด้วย

```yaml
      TEST_RUNTIME_DATABASE_URL: postgres://app_runtime:ci_runtime_password@localhost:5432/stock?sslmode=disable
      TEST_MAINTENANCE_DATABASE_URL: postgres://app_maintenance:ci_maintenance_password@localhost:5432/stock?sslmode=disable
```

- [ ] **Step 3: เขียน failing tests**

`internal/adapter/postgres/permissions_test.go`:

```go
package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// Each statement is planned inside a transaction that is rolled back. PostgreSQL checks
// privileges when planning, so "WHERE false" statements prove the grant without touching rows.
type privilegeCase struct {
	sql     string
	allowed bool
}

func checkPrivileges(t *testing.T, urlKey string, cases []privilegeCase) {
	t.Helper()
	pool := openPool(t, urlKey)
	for _, c := range cases {
		t.Run(c.sql, func(t *testing.T) {
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback(t.Context()) }()

			_, err = tx.Exec(t.Context(), c.sql)
			var pgErr *pgconn.PgError
			denied := errors.As(err, &pgErr) && pgErr.Code == insufficientPrivilege
			switch {
			case c.allowed && err != nil:
				t.Fatalf("want allowed, got %v", err)
			case !c.allowed && !denied:
				t.Fatalf("want SQLSTATE %s, got %v", insufficientPrivilege, err)
			}
		})
	}
}

func TestRuntimeRolePrivileges(t *testing.T) {
	checkPrivileges(t, "TEST_RUNTIME_DATABASE_URL", []privilegeCase{
		// audit_events is append-only and unreadable for the runtime role.
		{`INSERT INTO app.audit_events SELECT * FROM app.audit_events WHERE false`, false}, // needs SELECT too
		{`INSERT INTO app.audit_events (id, occurred_at, event_type, request_id, target_type, outcome)
		  SELECT gen_random_uuid(), now(), 'x', gen_random_uuid(), 't', 'success' WHERE false`, true},
		{`SELECT 1 FROM app.audit_events LIMIT 1`, false},
		{`UPDATE app.audit_events SET outcome = 'success' WHERE false`, false},
		{`DELETE FROM app.audit_events WHERE false`, false},
		// No deletes of accounts, sessions, challenges, flows or instruments.
		{`DELETE FROM app.users WHERE false`, false},
		{`DELETE FROM app.sessions WHERE false`, false},
		{`DELETE FROM app.otp_challenges WHERE false`, false},
		{`DELETE FROM app.oauth_flows WHERE false`, false},
		{`UPDATE app.instruments SET name = name WHERE false`, false},
		{`DELETE FROM app.instruments WHERE false`, false},
		{`UPDATE app.auth_providers SET provider = provider WHERE false`, false},
		{`DELETE FROM app.rate_limit_events WHERE false`, false},
		// What the repositories need.
		{`SELECT 1 FROM app.users LIMIT 1`, true},
		{`UPDATE app.users SET updated_at = updated_at WHERE false`, true},
		{`UPDATE app.sessions SET revoked_at = revoked_at WHERE false`, true},
		{`DELETE FROM app.portfolios WHERE false`, true},
		{`DELETE FROM app.transactions WHERE false`, true},
		{`DELETE FROM app.idempotency_keys WHERE false`, true},
		{`UPDATE app.rate_limit_keys SET last_denial_audit_at = now() WHERE false`, true},
		// No schema changes.
		{`CREATE TABLE app.should_not_exist (id int)`, false},
		{`ALTER TABLE app.users ADD COLUMN x int`, false},
	})
}

func TestMaintenanceRolePrivileges(t *testing.T) {
	checkPrivileges(t, "TEST_MAINTENANCE_DATABASE_URL", []privilegeCase{
		// Cleanup: read the expiry columns and delete expired rows.
		{`DELETE FROM app.audit_events WHERE occurred_at < now() - interval '90 days' AND false`, true},
		{`SELECT id, occurred_at FROM app.audit_events LIMIT 1`, true},
		{`DELETE FROM app.sessions WHERE expires_at < now() AND false`, true},
		{`DELETE FROM app.otp_challenges WHERE expires_at < now() AND false`, true},
		{`DELETE FROM app.oauth_flows WHERE expires_at < now() AND false`, true},
		{`DELETE FROM app.idempotency_keys WHERE expires_at < now() AND false`, true},
		{`DELETE FROM app.rate_limit_events WHERE occurred_at < now() AND false`, true},
		{`DELETE FROM app.rate_limit_keys WHERE expires_at < now() AND false`, true},
		// Nothing else: no reading payloads or secrets, no writes, no business tables.
		{`SELECT metadata FROM app.audit_events LIMIT 1`, false},
		{`SELECT token_hash FROM app.sessions LIMIT 1`, false},
		{`SELECT result FROM app.idempotency_keys LIMIT 1`, false},
		{`INSERT INTO app.audit_events (id, occurred_at, event_type, request_id, target_type, outcome)
		  SELECT gen_random_uuid(), now(), 'x', gen_random_uuid(), 't', 'success' WHERE false`, false},
		{`UPDATE app.audit_events SET outcome = 'success' WHERE false`, false},
		{`SELECT 1 FROM app.users LIMIT 1`, false},
		{`SELECT 1 FROM app.portfolios LIMIT 1`, false},
		{`DELETE FROM app.transactions WHERE false`, false},
		{`CREATE TABLE app.should_not_exist (id int)`, false},
	})
}

// TestRuntimeCanInsertIdentityColumns proves the runtime role can insert rows whose id comes
// from an identity sequence (rate_limit_events, and transactions.sequence in milestone 3c).
func TestRuntimeCanInsertIdentityColumns(t *testing.T) {
	pool := openPool(t, "TEST_RUNTIME_DATABASE_URL")
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(),
		`INSERT INTO app.rate_limit_keys (key_hash, scope, expires_at) VALUES ($1, 'login_ip', now() + interval '1 hour')`,
		make([]byte, 32)); err != nil {
		t.Fatalf("insert key: %v", err)
	}
	var id int64
	if err := tx.QueryRow(t.Context(),
		`INSERT INTO app.rate_limit_events (key_hash, occurred_at) VALUES ($1, now()) RETURNING id`,
		make([]byte, 32)).Scan(&id); err != nil || id <= 0 {
		t.Fatalf("insert event as runtime: id=%d err=%v", id, err)
	}
}
```

`internal/adapter/postgres/migrations_test.go`:

```go
package postgres

import "testing"

// TestMigrationsCreateEveryTable checks that migrations 00002-00004 created every phase 1 table in
// schema app (and nothing else there besides goose's version table).
func TestMigrationsCreateEveryTable(t *testing.T) {
	want := []string{
		"audit_events", "auth_providers", "goose_db_version", "idempotency_keys", "instruments", "oauth_flows",
		"otp_challenges", "portfolios", "rate_limit_events", "rate_limit_keys", "sessions", "transactions", "users",
	}
	rows, err := openPool(t, "TEST_ADMIN_DATABASE_URL").Query(t.Context(),
		`SELECT tablename FROM pg_tables WHERE schemaname = 'app' ORDER BY tablename`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if len(got) != len(want) {
		t.Fatalf("tables in app = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tables in app = %v, want %v", got, want)
		}
	}
}
```

- [ ] **Step 4: รันให้เห็นว่าล้มเหลว**

Run: `make test-integration`
Expected: FAIL — subtests ของ `TestRuntimeRolePrivileges`/`TestMaintenanceRolePrivileges` ล้มด้วย `relation "app.audit_events" does not exist` (prototype ล้ม 41 รายการ) และ `TestMigrationsCreateEveryTable` แจ้ง `tables in app = [goose_db_version]`

- [ ] **Step 5: เขียน migrations**

`migrations/00002_auth.sql`:

```sql
-- +goose Up
CREATE TABLE app.users (
    id                uuid         NOT NULL,
    email             varchar(254) NOT NULL,
    password_hash     text,
    email_verified_at timestamptz,
    created_at        timestamptz  NOT NULL,
    updated_at        timestamptz  NOT NULL,
    CONSTRAINT users_pkey PRIMARY KEY (id),
    -- Go normalizes the email (ParseEmail); comparing with lower() here could disagree with Go
    -- for non-ASCII letters, so the stored value is compared exactly.
    CONSTRAINT users_email_key UNIQUE (email),
    CONSTRAINT users_email_length CHECK (char_length(email) BETWEEN 1 AND 254)
);

CREATE TABLE app.auth_providers (
    id               uuid         NOT NULL,
    user_id          uuid         NOT NULL,
    provider         text         NOT NULL,
    provider_subject varchar(255) NOT NULL,
    created_at       timestamptz  NOT NULL,
    CONSTRAINT auth_providers_pkey PRIMARY KEY (id),
    CONSTRAINT auth_providers_user_id_fkey FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
    CONSTRAINT auth_providers_subject_key UNIQUE (provider, provider_subject),
    CONSTRAINT auth_providers_user_provider_key UNIQUE (user_id, provider),
    CONSTRAINT auth_providers_provider_allowed CHECK (provider = 'google'),
    CONSTRAINT auth_providers_subject_length CHECK (char_length(provider_subject) >= 1)
);

CREATE TABLE app.sessions (
    id            uuid        NOT NULL,
    user_id       uuid        NOT NULL,
    token_hash    bytea       NOT NULL,
    created_at    timestamptz NOT NULL,
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz,
    revoke_reason text,
    CONSTRAINT sessions_pkey PRIMARY KEY (id),
    CONSTRAINT sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
    CONSTRAINT sessions_token_hash_key UNIQUE (token_hash),
    CONSTRAINT sessions_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT sessions_hash_length CHECK (octet_length(token_hash) = 32),
    CONSTRAINT sessions_revoke_pair CHECK ((revoked_at IS NULL) = (revoke_reason IS NULL)),
    CONSTRAINT sessions_revoke_reason_allowed CHECK (revoke_reason IN ('logout', 'password_reset'))
);
CREATE INDEX sessions_user_expires_idx ON app.sessions (user_id, expires_at);
CREATE INDEX sessions_expires_idx ON app.sessions (expires_at);

CREATE TABLE app.otp_challenges (
    id              uuid        NOT NULL,
    user_id         uuid        NOT NULL,
    purpose         text        NOT NULL,
    otp_hash        bytea       NOT NULL,
    created_at      timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL,
    failed_attempts smallint    NOT NULL DEFAULT 0,
    consumed_at     timestamptz,
    invalidated_at  timestamptz,
    CONSTRAINT otp_challenges_pkey PRIMARY KEY (id),
    CONSTRAINT otp_challenges_user_id_fkey FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
    CONSTRAINT otp_challenges_purpose_allowed CHECK (purpose IN ('email_verification', 'password_reset')),
    CONSTRAINT otp_challenges_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT otp_challenges_attempts_valid CHECK (failed_attempts BETWEEN 0 AND 20),
    CONSTRAINT otp_challenges_hash_length CHECK (octet_length(otp_hash) = 32)
);
-- One open challenge per user per flow.
CREATE UNIQUE INDEX otp_challenges_one_open_idx ON app.otp_challenges (user_id, purpose)
    WHERE consumed_at IS NULL AND invalidated_at IS NULL;
CREATE INDEX otp_challenges_user_purpose_created_idx ON app.otp_challenges (user_id, purpose, created_at);
CREATE INDEX otp_challenges_expires_idx ON app.otp_challenges (expires_at);

CREATE TABLE app.oauth_flows (
    id                   uuid        NOT NULL,
    purpose              text        NOT NULL,
    user_id              uuid,
    state_hash           bytea       NOT NULL,
    nonce_hash           bytea       NOT NULL,
    browser_binding_hash bytea       NOT NULL,
    created_at           timestamptz NOT NULL,
    expires_at           timestamptz NOT NULL,
    consumed_at          timestamptz,
    CONSTRAINT oauth_flows_pkey PRIMARY KEY (id),
    CONSTRAINT oauth_flows_user_id_fkey FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
    CONSTRAINT oauth_flows_state_hash_key UNIQUE (state_hash),
    CONSTRAINT oauth_flows_purpose_user_valid CHECK (
        (purpose = 'login' AND user_id IS NULL) OR (purpose = 'link' AND user_id IS NOT NULL)),
    CONSTRAINT oauth_flows_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT oauth_flows_state_hash_length CHECK (octet_length(state_hash) = 32),
    CONSTRAINT oauth_flows_nonce_hash_length CHECK (octet_length(nonce_hash) = 32),
    CONSTRAINT oauth_flows_binding_hash_length CHECK (octet_length(browser_binding_hash) = 32)
);
CREATE INDEX oauth_flows_expires_idx ON app.oauth_flows (expires_at);

-- Runtime: only what the auth repositories (milestone 3b) use. No DELETE: rows expire and are
-- removed by the maintenance role.
GRANT SELECT, INSERT, UPDATE ON app.users TO app_runtime;
GRANT SELECT, INSERT ON app.auth_providers TO app_runtime;
GRANT SELECT, INSERT, UPDATE ON app.sessions, app.otp_challenges, app.oauth_flows TO app_runtime;

-- Maintenance: find expired rows and delete them; nothing else.
GRANT SELECT (id, expires_at), DELETE ON app.sessions, app.otp_challenges, app.oauth_flows TO app_maintenance;

-- +goose Down
DROP TABLE app.oauth_flows;
DROP TABLE app.otp_challenges;
DROP TABLE app.sessions;
DROP TABLE app.auth_providers;
DROP TABLE app.users;
```

`migrations/00003_portfolio.sql`:

```sql
-- +goose Up
CREATE TABLE app.portfolios (
    id          uuid          NOT NULL,
    user_id     uuid          NOT NULL,
    name        varchar(100)  NOT NULL,
    description varchar(1000) NOT NULL DEFAULT '',
    currency    char(3)       NOT NULL,
    version     integer       NOT NULL DEFAULT 1,
    created_at  timestamptz   NOT NULL,
    updated_at  timestamptz   NOT NULL,
    CONSTRAINT portfolios_pkey PRIMARY KEY (id),
    CONSTRAINT portfolios_user_id_fkey FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
    CONSTRAINT portfolios_name_trimmed CHECK (name = btrim(name) AND length(name) > 0),
    CONSTRAINT portfolios_currency_usd CHECK (currency = 'USD'),
    CONSTRAINT portfolios_version_positive CHECK (version > 0)
);
-- Case-insensitive unique name per user; the database decides conflicts.
CREATE UNIQUE INDEX portfolios_user_name_unique ON app.portfolios (user_id, lower(name));
CREATE INDEX portfolios_user_created_idx ON app.portfolios (user_id, created_at, id);

CREATE TABLE app.instruments (
    id                uuid         NOT NULL,
    symbol            varchar(15)  NOT NULL,
    provider_asset_id varchar(64),
    name              varchar(200) NOT NULL,
    exchange          varchar(20)  NOT NULL,
    asset_class       text         NOT NULL,
    status            text         NOT NULL,
    source            text         NOT NULL,
    verified_at       timestamptz  NOT NULL,
    created_at        timestamptz  NOT NULL,
    updated_at        timestamptz  NOT NULL,
    CONSTRAINT instruments_pkey PRIMARY KEY (id),
    CONSTRAINT instruments_symbol_key UNIQUE (symbol),
    CONSTRAINT instruments_provider_asset_unique UNIQUE (source, provider_asset_id),
    CONSTRAINT instruments_symbol_format CHECK (symbol ~ '^[A-Z][A-Z0-9.-]{0,14}$'),
    CONSTRAINT instruments_provider_asset_required CHECK (source = 'demo' OR provider_asset_id IS NOT NULL),
    CONSTRAINT instruments_name_length CHECK (char_length(name) >= 1),
    CONSTRAINT instruments_exchange_length CHECK (char_length(exchange) >= 1),
    CONSTRAINT instruments_asset_class_allowed CHECK (asset_class = 'us_equity'),
    CONSTRAINT instruments_status_allowed CHECK (status IN ('active', 'inactive')),
    CONSTRAINT instruments_source_allowed CHECK (source IN ('alpaca', 'demo'))
);

CREATE TABLE app.transactions (
    id            uuid           NOT NULL,
    portfolio_id  uuid           NOT NULL,
    sequence      bigint         GENERATED ALWAYS AS IDENTITY,
    instrument_id uuid           NOT NULL,
    side          text           NOT NULL,
    quantity      numeric(28,10) NOT NULL,
    price         numeric(28,10) NOT NULL,
    fee           numeric(28,10) NOT NULL,
    executed_at   timestamptz    NOT NULL,
    version       integer        NOT NULL DEFAULT 1,
    created_at    timestamptz    NOT NULL,
    updated_at    timestamptz    NOT NULL,
    CONSTRAINT transactions_pkey PRIMARY KEY (id),
    CONSTRAINT transactions_portfolio_id_fkey FOREIGN KEY (portfolio_id) REFERENCES app.portfolios (id) ON DELETE RESTRICT,
    CONSTRAINT transactions_instrument_id_fkey FOREIGN KEY (instrument_id) REFERENCES app.instruments (id) ON DELETE RESTRICT,
    CONSTRAINT transactions_sequence_key UNIQUE (sequence),
    CONSTRAINT transactions_side_allowed CHECK (side IN ('buy', 'sell')),
    -- '< Infinity' also rejects NaN, which PostgreSQL sorts above every number.
    CONSTRAINT transactions_quantity_valid CHECK (quantity > 0 AND quantity < 'Infinity'::numeric),
    CONSTRAINT transactions_price_valid CHECK (price > 0 AND price < 'Infinity'::numeric),
    CONSTRAINT transactions_fee_valid CHECK (fee >= 0 AND fee < 'Infinity'::numeric),
    CONSTRAINT transactions_executed_at_min CHECK (executed_at >= '1970-01-01T00:00:00Z'::timestamptz),
    CONSTRAINT transactions_version_positive CHECK (version > 0)
);
CREATE INDEX transactions_portfolio_instrument_idx ON app.transactions (portfolio_id, instrument_id, executed_at, sequence);
CREATE INDEX transactions_portfolio_executed_idx ON app.transactions (portfolio_id, executed_at, sequence);

CREATE TABLE app.idempotency_keys (
    portfolio_id uuid         NOT NULL,
    user_id      uuid         NOT NULL,
    operation    text         NOT NULL,
    key          varchar(128) NOT NULL,
    request_hash bytea        NOT NULL,
    result       jsonb        NOT NULL,
    created_at   timestamptz  NOT NULL,
    expires_at   timestamptz  NOT NULL,
    -- portfolio_id first so the FK check on portfolio delete can use this index.
    CONSTRAINT idempotency_keys_pkey PRIMARY KEY (portfolio_id, user_id, operation, key),
    CONSTRAINT idempotency_keys_portfolio_id_fkey FOREIGN KEY (portfolio_id) REFERENCES app.portfolios (id) ON DELETE RESTRICT,
    CONSTRAINT idempotency_keys_user_id_fkey FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
    CONSTRAINT idempotency_keys_operation_allowed CHECK (operation IN ('transactions.create')),
    CONSTRAINT idempotency_keys_key_length CHECK (char_length(key) >= 1),
    CONSTRAINT idempotency_keys_hash_length CHECK (octet_length(request_hash) = 32),
    CONSTRAINT idempotency_keys_expiry_valid CHECK (expires_at > created_at),
    -- Go limits the raw result to 16 KiB; jsonb::text re-serializes with extra spaces, so the
    -- database ceiling leaves 1 KiB of headroom for the same value.
    CONSTRAINT idempotency_keys_result_size CHECK (octet_length(result::text) <= 17408)
);
CREATE INDEX idempotency_keys_expires_idx ON app.idempotency_keys (expires_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON app.portfolios, app.transactions TO app_runtime;
GRANT SELECT, INSERT ON app.instruments TO app_runtime;
GRANT SELECT, INSERT, DELETE ON app.idempotency_keys TO app_runtime;

GRANT SELECT (portfolio_id, user_id, operation, key, expires_at), DELETE ON app.idempotency_keys TO app_maintenance;

-- +goose Down
DROP TABLE app.idempotency_keys;
DROP TABLE app.transactions;
DROP TABLE app.instruments;
DROP TABLE app.portfolios;
```

`migrations/00004_audit_ratelimit.sql`:

```sql
-- +goose Up
CREATE TABLE app.audit_events (
    id            uuid        NOT NULL,
    occurred_at   timestamptz NOT NULL,
    event_type    varchar(80) NOT NULL,
    actor_user_id uuid,
    request_id    uuid        NOT NULL,
    session_id    uuid,
    target_type   varchar(40) NOT NULL,
    target_id     text,
    outcome       text        NOT NULL,
    reason_code   varchar(80),
    metadata      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- No foreign keys: audit outlives the users, sessions and targets it mentions.
    CONSTRAINT audit_events_pkey PRIMARY KEY (id),
    CONSTRAINT audit_events_event_type_length CHECK (char_length(event_type) >= 1),
    CONSTRAINT audit_events_target_type_length CHECK (char_length(target_type) >= 1),
    CONSTRAINT audit_events_outcome_allowed CHECK (outcome IN ('success', 'failure', 'denied')),
    CONSTRAINT audit_events_metadata_object CHECK (jsonb_typeof(metadata) = 'object'),
    -- Go limits raw metadata to 8 KiB; jsonb::text adds spaces, so leave 1 KiB of headroom.
    CONSTRAINT audit_events_metadata_size CHECK (octet_length(metadata::text) <= 9216)
);
CREATE INDEX audit_events_actor_occurred_idx ON app.audit_events (actor_user_id, occurred_at);
CREATE INDEX audit_events_occurred_idx ON app.audit_events (occurred_at);
CREATE INDEX audit_events_request_idx ON app.audit_events (request_id);

CREATE TABLE app.rate_limit_keys (
    key_hash             bytea       NOT NULL,
    scope                text        NOT NULL,
    expires_at           timestamptz NOT NULL,
    last_denial_audit_at timestamptz,
    CONSTRAINT rate_limit_keys_pkey PRIMARY KEY (key_hash),
    CONSTRAINT rate_limit_keys_hash_length CHECK (octet_length(key_hash) = 32),
    CONSTRAINT rate_limit_keys_scope_allowed CHECK (scope IN (
        'login_email', 'login_ip', 'signup_ip', 'google_start_ip',
        'email_verification_request_email', 'email_verification_request_ip', 'email_verification_confirm_ip',
        'password_reset_request_email', 'password_reset_request_ip', 'password_reset_confirm_ip',
        'instrument_lookup_user'))
);
CREATE INDEX rate_limit_keys_expires_idx ON app.rate_limit_keys (expires_at);

CREATE TABLE app.rate_limit_events (
    id          bigint      GENERATED ALWAYS AS IDENTITY,
    key_hash    bytea       NOT NULL,
    occurred_at timestamptz NOT NULL,
    CONSTRAINT rate_limit_events_pkey PRIMARY KEY (id),
    CONSTRAINT rate_limit_events_key_hash_fkey FOREIGN KEY (key_hash) REFERENCES app.rate_limit_keys (key_hash) ON DELETE RESTRICT
);
CREATE INDEX rate_limit_events_key_occurred_idx ON app.rate_limit_events (key_hash, occurred_at);
CREATE INDEX rate_limit_events_occurred_idx ON app.rate_limit_events (occurred_at);

-- Runtime may only append audit events: no SELECT, UPDATE or DELETE (AGENTS.md: append-only).
GRANT INSERT ON app.audit_events TO app_runtime;
GRANT SELECT, INSERT, UPDATE ON app.rate_limit_keys TO app_runtime;
GRANT SELECT, INSERT ON app.rate_limit_events TO app_runtime;

GRANT SELECT (id, occurred_at), DELETE ON app.audit_events TO app_maintenance;
GRANT SELECT (key_hash, expires_at), DELETE ON app.rate_limit_keys TO app_maintenance;
GRANT SELECT (id, key_hash, occurred_at), DELETE ON app.rate_limit_events TO app_maintenance;

-- +goose Down
DROP TABLE app.rate_limit_events;
DROP TABLE app.rate_limit_keys;
DROP TABLE app.audit_events;
```

- [ ] **Step 6: migrate แล้วรัน test ให้ผ่าน**

```bash
make migrate-up
make test-integration
```

Expected: goose แสดง `OK   00002_auth.sql`, `OK   00003_portfolio.sql`, `OK   00004_audit_ratelimit.sql` และ `successfully migrated database to version: 4`; tests `ok  	go-backend-stock/internal/adapter/postgres` โดยไม่มี SKIP

- [ ] **Step 7: ยืนยันว่า Down ใช้ได้ (เฉพาะ DB ของเครื่องตัวเอง)**

```bash
set -a; . ./.env; set +a; ./bin/goose -dir migrations -table app.goose_db_version postgres "postgres://app_migrator:$APP_MIGRATOR_PASSWORD@127.0.0.1:$POSTGRES_HOST_PORT/$POSTGRES_DB?sslmode=disable" down-to 1 && make migrate-up
```

Expected: rollback `00004`, `00003`, `00002` สำเร็จแล้ว migrate กลับถึง version 4 ได้ ห้ามรันกับ DB ที่ใช้ร่วมกันหรือ Supabase

- [ ] **Step 8: แก้ `docs/database/schema.dbml` ให้ตรง migrations** (AGENTS.md: แก้ในชุดงานเดียวกับ migration) ไฟล์ทั้งไฟล์หลังแก้:

`docs/database/schema.dbml`:

```dbml
  // Paste this entire file into https://dbdiagram.io
  // Source: docs/superpowers/specs/2026-10-05-phase-1-design.md
  // DBML syntax: https://dbml.dbdiagram.io/docs/
  // Mirrors migrations/00002-00004. Goose migrations are the source of truth: change both together.

  Project stock_portfolio {
    database_type: 'PostgreSQL'
    Note: '''
      Phase 1 backend. Private app schema; Supabase Database only.
      UUIDv7 IDs and timestamps are supplied by the Go application.
      IDs are references, not secrets; tokens use separate crypto/rand values.
      Monetary values use numeric(28,10); API values are decimal strings.
      Holdings are calculated from transactions, not stored in a table.
      PostgreSQL-specific migration requirements are documented in table notes.
      Bootstrap (not goose) creates the roles and schema app owned by app_migrator.
      goose runs with -table app.goose_db_version; nothing is created in public.
    '''
  }

  Table app.users {
    id uuid [pk, not null]
    email varchar(254) [not null, unique, note: 'Normalized by Go ParseEmail (trim, lowercase; dots and plus suffixes kept). Compared exactly: no lower() in SQL, which could disagree with Go for non-ASCII letters.']
    password_hash text [null, note: 'Argon2id PHC string; NULL for Google-only accounts. Selected only by login/register/reset queries.']
    email_verified_at timestamptz [null, note: 'NULL until email OTP confirm, successful password reset, or Google email_verified signup. Never reset to NULL.']
    created_at timestamptz [not null]
    updated_at timestamptz [not null]

    checks {
      `char_length(email) BETWEEN 1 AND 254` [name: 'users_email_length']
    }

    Note: 'A Google-only user and its auth_provider must be created atomically. No account deletion in phase 1.'
  }

  Table app.auth_providers {
    id uuid [pk, not null]
    user_id uuid [not null]
    provider text [not null]
    provider_subject varchar(255) [not null, note: 'Google sub (max 255 chars); email is not the provider identity.']
    created_at timestamptz [not null]

    indexes {
      (provider, provider_subject) [unique, name: 'auth_providers_subject_key']
      (user_id, provider) [unique, name: 'auth_providers_user_provider_key']
    }

    checks {
      `provider = 'google'` [name: 'auth_providers_provider_allowed']
      `char_length(provider_subject) >= 1` [name: 'auth_providers_subject_length']
    }
  }

  Table app.sessions {
    id uuid [pk, not null]
    user_id uuid [not null]
    token_hash bytea [not null, unique, note: 'SHA-256 of random session token; never store the raw token.']
    created_at timestamptz [not null]
    expires_at timestamptz [not null, note: 'Absolute lifetime: 7 days.']
    revoked_at timestamptz [null]
    revoke_reason text [null]

    indexes {
      (user_id, expires_at)
      expires_at
    }

    checks {
      `expires_at > created_at` [name: 'sessions_expiry_valid']
      `octet_length(token_hash) = 32` [name: 'sessions_hash_length']
      `(revoked_at IS NULL) = (revoke_reason IS NULL)` [name: 'sessions_revoke_pair']
      `revoke_reason IN ('logout', 'password_reset')` [name: 'sessions_revoke_reason_allowed']
    }
  }

  Table app.portfolios {
    id uuid [pk, not null]
    user_id uuid [not null]
    name varchar(100) [not null]
    description varchar(1000) [not null, default: '']
    currency char(3) [not null]
    version integer [not null, default: 1, note: 'Optimistic locking for name/description; exposed as ETag, required via If-Match on PUT/DELETE.']
    created_at timestamptz [not null]
    updated_at timestamptz [not null]

    indexes {
      (user_id, created_at, id)
      (user_id, `lower(name)`) [unique, name: 'portfolios_user_name_unique', note: 'Case-insensitive unique name per user.']
    }

    checks {
      `name = btrim(name) AND length(name) > 0` [name: 'portfolios_name_trimmed']
      `currency = 'USD'` [name: 'portfolios_currency_usd']
      `version > 0` [name: 'portfolios_version_positive']
    }

    Note: 'Lock this row before mutations. Delete only when no transactions remain. Currency is immutable.'
  }

  Table app.instruments {
    id uuid [pk, not null, note: 'Surrogate key: symbols can change (FB -> META) and tickers can be reused.']
    symbol varchar(15) [not null, unique, note: 'Current uppercase ticker, e.g. AAPL, BRK.B. Phase 1: unique across the table.']
    provider_asset_id varchar(64) [null, note: 'Stable provider ID (Alpaca asset id); required when source = alpaca.']
    name varchar(200) [not null, note: 'Company/fund name from the provider, e.g. Apple Inc.']
    exchange varchar(20) [not null, note: 'e.g. NASDAQ, NYSE, ARCA.']
    asset_class text [not null]
    status text [not null, note: 'Provider status at verified_at; inactive symbols remain usable for historical trades.']
    source text [not null, note: 'demo rows exist only in dev/test.']
    verified_at timestamptz [not null, note: 'Last time the provider confirmed this symbol. No background refresh in phase 1.']
    created_at timestamptz [not null]
    updated_at timestamptz [not null]

    indexes {
      (source, provider_asset_id) [unique, name: 'instruments_provider_asset_unique']
    }

    checks {
      `symbol ~ '^[A-Z][A-Z0-9.-]{0,14}$'` [name: 'instruments_symbol_format']
      `source = 'demo' OR provider_asset_id IS NOT NULL` [name: 'instruments_provider_asset_required']
      `asset_class = 'us_equity'` [name: 'instruments_asset_class_allowed']
      `status IN ('active', 'inactive')` [name: 'instruments_status_allowed']
      `source IN ('alpaca', 'demo')` [name: 'instruments_source_allowed']
      `char_length(name) >= 1` [name: 'instruments_name_length']
      `char_length(exchange) >= 1` [name: 'instruments_exchange_length']
    }

    Note: '''
      Shared market reference data, not user data. Rows are never deleted.
      Looked up before the portfolio lock; inserted/updated in the same DB
      transaction as the trade that first uses the symbol.
    '''
  }

  Table app.transactions {
    id uuid [pk, not null]
    portfolio_id uuid [not null]
    sequence bigint [not null, unique, increment, note: 'Migration: GENERATED ALWAYS AS IDENTITY; immutable tie-breaker.']
    instrument_id uuid [not null, note: 'FK to instruments.id; API accepts and returns the symbol.']
    side text [not null]
    quantity numeric(28,10) [not null]
    price numeric(28,10) [not null]
    fee numeric(28,10) [not null]
    executed_at timestamptz [not null]
    version integer [not null, default: 1, note: 'Optimistic locking; not incremented by idempotent replay.']
    created_at timestamptz [not null]
    updated_at timestamptz [not null]

    indexes {
      (portfolio_id, instrument_id, executed_at, sequence)
      (portfolio_id, executed_at, sequence)
    }

    checks {
      `side IN ('buy', 'sell')` [name: 'transactions_side_allowed']
      `quantity > 0 AND quantity < 'Infinity'::numeric` [name: 'transactions_quantity_valid']
      `price > 0 AND price < 'Infinity'::numeric` [name: 'transactions_price_valid']
      `fee >= 0 AND fee < 'Infinity'::numeric` [name: 'transactions_fee_valid']
      `executed_at >= '1970-01-01T00:00:00Z'::timestamptz` [name: 'transactions_executed_at_min']
      `version > 0` [name: 'transactions_version_positive']
    }

    Note: 'Replay by executed_at ASC, sequence ASC. Go rejects future executed_at via Clock and excess precision before insertion and rejects any history with negative holdings. No transfer between portfolios.'
  }

  Table app.otp_challenges {
    id uuid [pk, not null]
    user_id uuid [not null]
    purpose text [not null, note: 'email_verification or password_reset; included in the OTP HMAC.']
    otp_hash bytea [not null, note: 'HMAC-SHA-256 bound to purpose, challenge and user; no raw OTP.']
    created_at timestamptz [not null]
    expires_at timestamptz [not null, note: '10-minute lifetime.']
    failed_attempts smallint [not null, default: 0, note: 'Configured limit defaults to 5; config must be 1..20 (DB ceiling).']
    consumed_at timestamptz [null]
    invalidated_at timestamptz [null]

    indexes {
      (user_id, purpose, created_at)
      expires_at
    }

    checks {
      `purpose IN ('email_verification', 'password_reset')` [name: 'otp_challenges_purpose_allowed']
      `expires_at > created_at` [name: 'otp_challenges_expiry_valid']
      `failed_attempts BETWEEN 0 AND 20` [name: 'otp_challenges_attempts_valid']
      `octet_length(otp_hash) = 32` [name: 'otp_challenges_hash_length']
    }

    Note: '''
      Migration 00002 adds the partial UNIQUE index otp_challenges_one_open_idx
      on (user_id, purpose) WHERE consumed_at IS NULL AND invalidated_at IS NULL:
      one open challenge per user per flow.
      Every query filters by purpose. Invalidate the previous open
      challenge of the same purpose before inserting a new one.
      email_verification rows exist only for unverified local accounts.
    '''
  }

  Table app.oauth_flows {
    id uuid [pk, not null]
    purpose text [not null]
    user_id uuid [null, note: 'Set only for account linking.']
    state_hash bytea [not null, unique]
    nonce_hash bytea [not null]
    browser_binding_hash bytea [not null, note: 'PKCE verifier is derived from HMAC(OIDC_SECRET, state + binding); not stored.']
    created_at timestamptz [not null]
    expires_at timestamptz [not null, note: '10-minute lifetime; consume once.']
    consumed_at timestamptz [null]

    indexes {
      expires_at
    }

    checks {
      `(purpose = 'login' AND user_id IS NULL) OR (purpose = 'link' AND user_id IS NOT NULL)` [name: 'oauth_flows_purpose_user_valid']
      `expires_at > created_at` [name: 'oauth_flows_expiry_valid']
      `octet_length(state_hash) = 32` [name: 'oauth_flows_state_hash_length']
      `octet_length(nonce_hash) = 32` [name: 'oauth_flows_nonce_hash_length']
      `octet_length(browser_binding_hash) = 32` [name: 'oauth_flows_binding_hash_length']
    }
  }

  Table app.rate_limit_keys {
    key_hash bytea [pk, not null, note: 'HMAC of scope + normalized email or trusted client IP.']
    scope text [not null]
    expires_at timestamptz [not null]
    last_denial_audit_at timestamptz [null]

    indexes {
      expires_at
    }

    checks {
      `scope IN ('login_email', 'login_ip', 'signup_ip', 'google_start_ip', 'email_verification_request_email', 'email_verification_request_ip', 'email_verification_confirm_ip', 'password_reset_request_email', 'password_reset_request_ip', 'password_reset_confirm_ip', 'instrument_lookup_user')` [name: 'rate_limit_keys_scope_allowed']
    }

    Note: 'Lock keys in key_hash order when admitting a request against multiple rolling-window limits. Do not store raw email or IP.'
  }

  Table app.rate_limit_events {
    id bigint [pk, not null, increment, note: 'Migration: GENERATED ALWAYS AS IDENTITY.']
    key_hash bytea [not null]
    occurred_at timestamptz [not null]

    indexes {
      (key_hash, occurred_at)
      occurred_at
    }

    Note: 'Persist admitted attempts so restarting the API does not reset quotas. Maintenance deletes expired events before unused keys.'
  }

  Table app.idempotency_keys {
    portfolio_id uuid [not null]
    user_id uuid [not null]
    operation text [not null, note: 'Phase 1: transactions.create']
    key varchar(128) [not null]
    request_hash bytea [not null, note: 'Hash of normalized request fields.']
    result jsonb [not null, note: 'Original transaction response snapshot; never cookies or tokens.']
    created_at timestamptz [not null]
    expires_at timestamptz [not null, note: '24 hours from the original creation.']

    indexes {
      (portfolio_id, user_id, operation, key) [pk, note: 'portfolio_id first so the FK check on portfolio delete uses this index.']
      expires_at
    }

    checks {
      `operation IN ('transactions.create')` [name: 'idempotency_keys_operation_allowed']
      `char_length(key) >= 1` [name: 'idempotency_keys_key_length']
      `octet_length(request_hash) = 32` [name: 'idempotency_keys_hash_length']
      `expires_at > created_at` [name: 'idempotency_keys_expiry_valid']
      `octet_length(result::text) <= 17408` [name: 'idempotency_keys_result_size', note: 'Go limits the raw result to 16 KiB; jsonb::text adds spaces, so 1 KiB headroom.']
    }

    Note: 'Commit with transaction mutation and audit. Same key/payload returns the original result; different payload conflicts. Check portfolio ownership in the same transaction.'
  }

  Table app.audit_events {
    id uuid [pk, not null]
    occurred_at timestamptz [not null]
    event_type varchar(80) [not null]
    actor_user_id uuid [null, note: 'Logical user reference; NULL for unauthenticated attempts. No FK.']
    request_id uuid [not null]
    session_id uuid [null, note: 'Logical session ID, not a token or token hash. No FK.']
    target_type varchar(40) [not null]
    target_id text [null, note: 'Logical target reference; survives deletion of the target.']
    outcome text [not null]
    reason_code varchar(80) [null]
    metadata jsonb [not null, default: `'{}'::jsonb`, note: 'Allowlisted fields only; decimal values encoded as strings. Go rejects NUL and invalid UTF-8, which jsonb cannot store.']

    indexes {
      (actor_user_id, occurred_at)
      occurred_at
      request_id
    }

    checks {
      `char_length(event_type) >= 1` [name: 'audit_events_event_type_length']
      `char_length(target_type) >= 1` [name: 'audit_events_target_type_length']
      `outcome IN ('success', 'failure', 'denied')` [name: 'audit_events_outcome_allowed']
      `jsonb_typeof(metadata) = 'object'` [name: 'audit_events_metadata_object']
      `octet_length(metadata::text) <= 9216` [name: 'audit_events_metadata_size', note: 'Go limits raw metadata to 8 KiB; jsonb::text adds spaces, so 1 KiB headroom.']
    }

    Note: 'Runtime role has INSERT only (no SELECT, UPDATE or DELETE). No foreign keys to users, sessions or targets; no cascading deletion. Retain 90 days; cleanup uses the maintenance role (SELECT id, occurred_at + DELETE).'
  }

  Ref: app.auth_providers.user_id > app.users.id [delete: restrict, update: no action]
  Ref: app.sessions.user_id > app.users.id [delete: restrict, update: no action]
  Ref: app.portfolios.user_id > app.users.id [delete: restrict, update: no action]
  Ref: app.transactions.portfolio_id > app.portfolios.id [delete: restrict, update: no action]
  Ref: app.transactions.instrument_id > app.instruments.id [delete: restrict, update: no action]
  Ref: app.otp_challenges.user_id > app.users.id [delete: restrict, update: no action]
  Ref: app.oauth_flows.user_id > app.users.id [delete: restrict, update: no action]
  Ref: app.rate_limit_events.key_hash > app.rate_limit_keys.key_hash [delete: restrict, update: no action]
  Ref: app.idempotency_keys.user_id > app.users.id [delete: restrict, update: no action]
  Ref: app.idempotency_keys.portfolio_id > app.portfolios.id [delete: restrict, update: no action]
```

- [ ] **Step 9: lint และ commit**

Run: `make test lint`
Expected: tests `ok` ทุกแพ็กเกจ และ `0 issues.`

```bash
git add migrations internal/adapter/postgres docs/database/schema.dbml Makefile .github/workflows/ci.yml
git commit -m "feat(db): add phase 1 schema, role grants and permission tests"
```

---

### Task 3: Mapper pgtype ↔ domain และการแปลง error ของ PostgreSQL

**Files:**
- Create: `internal/adapter/postgres/mapping.go`, `internal/adapter/postgres/errors.go`
- Test: `internal/adapter/postgres/mapping_test.go`, `internal/adapter/postgres/errors_test.go`
- Modify: `internal/core/port/portfolio.go`, `internal/core/port/auth.go` (comment เท่านั้น)

**Interfaces:**
- Consumes: `domain.ID`, `domain.ParseID`, `domain.ParseSymbol`, sentinels, `domain.NewError` (milestone 2 + Task 1); ชื่อ constraint จาก Task 2
- Produces (unexported, package `postgres`):
  - `pgUUID(domain.ID) pgtype.UUID`, `idFromPG(field string, pgtype.UUID) (domain.ID, error)`, `optionalIDFromPG(field string, pgtype.UUID) (domain.ID, error)`
  - `pgTime(time.Time) pgtype.Timestamptz`, `pgOptionalTime(*time.Time) pgtype.Timestamptz`, `timeFromPG(pgtype.Timestamptz) time.Time`, `optionalTimeFromPG(pgtype.Timestamptz) *time.Time`
  - `pgText(string) pgtype.Text` (`""` → NULL)
  - `pgNumeric(string) (pgtype.Numeric, error)`, `numericString(field string, pgtype.Numeric) (string, error)`
  - `fromStored(error) error` — `invalid_input` → `internal`
  - `mapError(error) error`
  - test helper `bigInt(t, s) *big.Int`
  - comment ใน ports ระบุ contract ที่ repositories ของ 3b/3c ต้องทำตาม (ไม่เปลี่ยน signature)

- [ ] **Step 1: เขียน failing tests**

`internal/adapter/postgres/mapping_test.go`:

```go
package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"go-backend-stock/internal/core/domain"
)

func TestNumericRoundTripIsExact(t *testing.T) {
	for _, s := range []string{"0.0000000001", "1", "0.5", "123456789012345678.123456789", "999999999999999999.9999999999"} {
		n, err := pgNumeric(s)
		if err != nil {
			t.Fatalf("pgNumeric(%q) error = %v", s, err)
		}
		got, err := numericString("quantity", n)
		if err != nil || got != s {
			t.Errorf("round trip of %q = %q, %v", s, got, err)
		}
	}
	// Trailing zeros from numeric(28,10) disappear in the canonical form.
	n := pgtype.Numeric{Int: bigInt(t, "15000000000"), Exp: -10, Valid: true}
	if got, _ := numericString("price", n); got != "1.5" {
		t.Errorf("numericString(1.5000000000) = %q, want 1.5", got)
	}
}

func TestNumericStringRejectsNonFinite(t *testing.T) {
	for name, n := range map[string]pgtype.Numeric{
		"NULL":      {},
		"NaN":       {NaN: true, Valid: true},
		"+Infinity": {InfinityModifier: pgtype.Infinity, Valid: true},
		"-Infinity": {InfinityModifier: pgtype.NegativeInfinity, Valid: true},
	} {
		_, err := numericString("fee", n)
		if !errors.Is(err, domain.ErrInternal) {
			t.Errorf("%s: error = %v, want internal", name, err)
		}
	}
}

func TestIDMapping(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	back, err := idFromPG("id", pgUUID(id))
	if err != nil || back != id {
		t.Fatalf("round trip = %v, %v; want %v", back, err, id)
	}
	if pgUUID(domain.ID{}).Valid {
		t.Error("zero ID must map to NULL")
	}
	if got, err := optionalIDFromPG("actor_user_id", pgtype.UUID{}); err != nil || !got.IsZero() {
		t.Errorf("NULL optional ID = %v, %v; want zero", got, err)
	}
	if _, err := idFromPG("id", pgtype.UUID{}); !errors.Is(err, domain.ErrInternal) {
		t.Errorf("NULL required ID error = %v, want internal", err)
	}
	// The nil UUID is never a valid ID; reading one is an internal error, not invalid_input.
	if _, err := idFromPG("id", pgtype.UUID{Valid: true}); !errors.Is(err, domain.ErrInternal) {
		t.Errorf("nil UUID error = %v, want internal", err)
	}
}

func TestTimeMappingIsUTC(t *testing.T) {
	bangkok := time.FixedZone("ICT", 7*60*60)
	in := time.Date(2026, 10, 17, 21, 0, 0, 123000, bangkok)
	got := timeFromPG(pgTime(in))
	if !got.Equal(in) || got.Location() != time.UTC {
		t.Errorf("timeFromPG = %v, want %v in UTC", got, in)
	}
	if optionalTimeFromPG(pgtype.Timestamptz{}) != nil || pgOptionalTime(nil).Valid {
		t.Error("NULL timestamps must map to nil and back")
	}
}

func TestFromStoredTurnsInvalidInputIntoInternal(t *testing.T) {
	_, parseErr := domain.ParseSymbol("not a symbol")
	err := fromStored(parseErr)
	if !errors.Is(err, domain.ErrInternal) || errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("fromStored(invalid_input) = %v, want internal only", err)
	}
	if fromStored(nil) != nil {
		t.Error("fromStored(nil) must be nil")
	}
	if got := fromStored(domain.ErrNotFound); !errors.Is(got, domain.ErrNotFound) {
		t.Errorf("fromStored(not_found) = %v, want it unchanged", got)
	}
}

func TestTextMappingStoresEmptyAsNull(t *testing.T) {
	if pgText("").Valid {
		t.Error(`pgText("") must be NULL`)
	}
	if got := pgText("invalid_credentials"); !got.Valid || got.String != "invalid_credentials" {
		t.Errorf("pgText = %+v, want a valid text", got)
	}
}
```

`internal/adapter/postgres/errors_test.go`:

```go
package postgres

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"go-backend-stock/internal/core/domain"
)

func bigInt(t *testing.T, s string) *big.Int {
	t.Helper()
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("bad big.Int %q", s)
	}
	return n
}

func TestMapError(t *testing.T) {
	unique := func(constraint string) error {
		return fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23505", ConstraintName: constraint,
			Message: `duplicate key value violates unique constraint "` + constraint + `"`, Detail: "Key (email)=(secret@example.com) already exists."})
	}
	tests := []struct {
		name string
		in   error
		want *domain.Error
	}{
		{"no rows", fmt.Errorf("get: %w", pgx.ErrNoRows), domain.ErrNotFound},
		{"portfolio name", unique("portfolios_user_name_unique"), domain.ErrPortfolioNameConflict},
		{"google subject", unique("auth_providers_subject_key"), domain.ErrIdentityConflict},
		{"one google per user", unique("auth_providers_user_provider_key"), domain.ErrIdentityConflict},
		{"email race", unique("users_email_key"), domain.ErrIdentityConflict},
		{"idempotency key race", unique("idempotency_keys_pkey"), domain.ErrIdempotencyConflict},
		{"portfolio still has trades", &pgconn.PgError{Code: "23503", ConstraintName: "transactions_portfolio_id_fkey"}, domain.ErrPortfolioNotEmpty},
		{"other unique", unique("sessions_token_hash_key"), domain.ErrInternal},
		{"other foreign key", &pgconn.PgError{Code: "23503", ConstraintName: "idempotency_keys_portfolio_id_fkey"}, domain.ErrInternal},
		{"check violation", &pgconn.PgError{Code: "23514", ConstraintName: "audit_events_metadata_object"}, domain.ErrInternal},
		{"connection error", errors.New("dial tcp 127.0.0.1:5432: connection refused"), domain.ErrInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapError(tt.in)
			if !errors.Is(got, tt.want) {
				t.Fatalf("mapError() = %v, want %s", got, tt.want.Code)
			}
			var de *domain.Error
			if errors.As(got, &de) && strings.Contains(de.Message, "secret@example.com") {
				t.Errorf("domain message leaks row values: %q", de.Message)
			}
		})
	}
	if mapError(nil) != nil {
		t.Error("mapError(nil) must be nil")
	}
	// Internal errors keep the database error for server-side logs.
	raw := &pgconn.PgError{Code: "23514", ConstraintName: "x"}
	if got := mapError(raw); !errors.As(got, new(*pgconn.PgError)) {
		t.Errorf("internal error %v lost the original database error", got)
	}
}
```

- [ ] **Step 2: รันให้เห็นว่าล้มเหลว**

Run: `go test -count=1 ./internal/adapter/postgres/`
Expected: FAIL ด้วย `undefined: mapError`, `undefined: pgNumeric` ฯลฯ

- [ ] **Step 3: เขียนโค้ด**

`internal/adapter/postgres/mapping.go`:

```go
package postgres

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"go-backend-stock/internal/core/domain"
)

// Mapping between sqlc's pgtype values and domain types. Values read from the database go back
// through the domain Parse* functions (ADR-0013); a stored value that fails them is a bug or
// corrupt data, so fromStored turns the error into internal rather than invalid_input.

func pgUUID(id domain.ID) pgtype.UUID {
	if id.IsZero() {
		return pgtype.UUID{} // NULL
	}
	return pgtype.UUID{Bytes: uuid.MustParse(id.String()), Valid: true}
}

// idFromPG maps a NOT NULL uuid column.
func idFromPG(field string, u pgtype.UUID) (domain.ID, error) {
	if !u.Valid {
		return domain.ID{}, domain.NewError(domain.CodeInternal, "stored "+field+" is NULL")
	}
	id, err := domain.ParseID(field, uuid.UUID(u.Bytes).String())
	return id, fromStored(err)
}

// optionalIDFromPG maps a nullable uuid column; NULL becomes the zero ID.
func optionalIDFromPG(field string, u pgtype.UUID) (domain.ID, error) {
	if !u.Valid {
		return domain.ID{}, nil
	}
	return idFromPG(field, u)
}

func pgTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func pgOptionalTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{} // NULL
	}
	return pgTime(*t)
}

// timeFromPG maps a timestamptz column to UTC; NULL becomes the zero time.
func timeFromPG(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// optionalTimeFromPG maps a nullable timestamptz column; NULL becomes nil.
func optionalTimeFromPG(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

// pgText maps an optional string: "" is stored as NULL.
func pgText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

// pgNumeric maps a canonical decimal string (from Quantity/Price/Fee.String()) to numeric.
func pgNumeric(s string) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil {
		return pgtype.Numeric{}, domain.NewError(domain.CodeInternal, "numeric value cannot be encoded")
	}
	return n, nil
}

// numericString returns the exact decimal string of a numeric column without going through
// float. NULL, NaN and infinities are errors: the schema forbids them.
func numericString(field string, n pgtype.Numeric) (string, error) {
	if !n.Valid || n.NaN || n.InfinityModifier != pgtype.Finite || n.Int == nil {
		return "", domain.NewError(domain.CodeInternal, "stored "+field+" is not a finite number")
	}
	return decimal.NewFromBigInt(n.Int, n.Exp).String(), nil
}

// fromStored converts a domain invalid_input error about a stored value into internal: the client
// did not send that value, so it must not see a 400 for it.
func fromStored(err error) error {
	var de *domain.Error
	if errors.As(err, &de) && de.Code == domain.CodeInvalidInput {
		return domain.NewError(domain.CodeInternal, "stored "+de.Message)
	}
	return err
}
```

`internal/adapter/postgres/errors.go`:

```go
package postgres

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"go-backend-stock/internal/core/domain"
)

const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
)

// uniqueConflicts maps unique constraints to the domain errors the core expects. Constraint names
// are set explicitly in migrations/, so renaming one there must update this map.
var uniqueConflicts = map[string]*domain.Error{
	"portfolios_user_name_unique":      domain.ErrPortfolioNameConflict,
	"auth_providers_subject_key":       domain.ErrIdentityConflict,
	"auth_providers_user_provider_key": domain.ErrIdentityConflict,
	// Two signups for one email at the same time; the service answers as for an existing email.
	"users_email_key":       domain.ErrIdentityConflict,
	"idempotency_keys_pkey": domain.ErrIdempotencyConflict,
}

// mapError converts a database error into a domain error. Anything unexpected becomes internal
// while keeping the original error in the chain for server-side logs; the domain message never
// contains SQL or row values.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case sqlStateUniqueViolation:
			if de, ok := uniqueConflicts[pgErr.ConstraintName]; ok {
				return de
			}
		case sqlStateForeignKeyViolation:
			if pgErr.ConstraintName == "transactions_portfolio_id_fkey" {
				return domain.ErrPortfolioNotEmpty
			}
		}
	}
	return fmt.Errorf("%w: %w", domain.ErrInternal, err)
}
```

- [ ] **Step 4: รัน test และ lint**

Run: `go test -race -count=1 ./internal/adapter/postgres/ && make lint`
Expected: `ok` และ `0 issues.` (unit tests ไม่ต้องใช้ DB; ถ้าไม่ได้ตั้ง env ของ integration tests ส่วนนั้นจะ SKIP)

- [ ] **Step 5: เขียน contract ลงใน comment ของ ports** (design หัวข้อ 4.3 และ 7: repositories ของ 3b/3c ใช้เป็นสเปก และ test ของแต่ละ repository ต้องพิสูจน์ข้อความเหล่านี้) — แก้เฉพาะ comment

แก้ `internal/core/port/portfolio.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
// ID arguments are ordered ownerID, portfolioID, transactionID. Every query filters by the
// owner, so another user's portfolio reads as domain.ErrNotFound.
```

   ด้วย

```go
// ID arguments are ordered ownerID, portfolioID, transactionID. Every query filters by the
// owner, so another user's portfolio reads as domain.ErrNotFound. A method that reads one row
// returns domain.ErrNotFound when the row does not exist or is not the owner's. Lists are newest
// first with a unique tie-break (portfolios: created_at, id; transactions: executed_at,
// sequence), so consecutive pages never repeat or skip a row.
```

2. แทน

```go
type PortfolioRepository interface {
	Insert(ctx context.Context, portfolio domain.Portfolio) error
```

   ด้วย

```go
type PortfolioRepository interface {
	// Insert returns domain.ErrPortfolioNameConflict when the owner already has the name in any
	// letter case.
	Insert(ctx context.Context, portfolio domain.Portfolio) error
```

3. แทน

```go
	// the row with version + 1; a mismatch returns domain.ErrVersionConflict.
```

   ด้วย

```go
	// the row with version + 1; a mismatch returns domain.ErrVersionConflict and a name the owner
	// already uses returns domain.ErrPortfolioNameConflict.
```

4. แทน

```go
	// Update returns the row with version + 1; a version mismatch returns domain.ErrVersionConflict.
```

   ด้วย

```go
	// Update changes the trade fields of a transaction of transaction.PortfolioID and returns the
	// row with version + 1; a version mismatch returns domain.ErrVersionConflict. It never changes
	// portfolio_id: a trade cannot move to another portfolio.
```

5. แทน

```go
	Save(ctx context.Context, record domain.IdempotencyRecord) error
```

   ด้วย

```go
	// Save returns domain.ErrIdempotencyConflict when the scope already has the key (a concurrent
	// retry won); the caller then reads the stored record.
	Save(ctx context.Context, record domain.IdempotencyRecord) error
```

แก้ `internal/core/port/auth.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
	Insert(ctx context.Context, user domain.User) error
```

   ด้วย

```go
	// Insert returns domain.ErrIdentityConflict when the email is already registered.
	Insert(ctx context.Context, user domain.User) error
```

2. แทน

```go
// SessionRepository stores server-side sessions by token hash.
```

   ด้วย

```go
// SessionRepository stores server-side sessions by token hash. ByTokenHash returns
// domain.ErrNotFound for an unknown hash and returns expired or revoked sessions as stored.
```

3. แทน

```go
// IdentityRepository stores links to external identities, unique per (provider, subject).
```

   ด้วย

```go
// IdentityRepository stores links to external identities, unique per (provider, subject).
// BySubject returns domain.ErrNotFound for an unknown subject; Insert returns
// domain.ErrIdentityConflict when the subject or the user's link to that provider exists.
```

4. แทน

```go
// OTPChallengeRepository stores OTP challenges. Every method takes the purpose so a challenge
// ID from one flow can never be used in the other.
```

   ด้วย

```go
// OTPChallengeRepository stores OTP challenges. Every method takes the purpose so a challenge
// ID from one flow can never be used in the other. ByID, Lock, RecordFailure and Consume return
// domain.ErrNotFound for an unknown or other-purpose challenge; Consume also does so for a
// challenge that is no longer open.
```

Run: `go build ./... && make lint`
Expected: build ผ่านและ `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add internal/adapter/postgres internal/core/port
git commit -m "feat(db): map pgtype values to domain types and database errors to domain errors"
```

---

### Task 4: sqlc, `Store.withinTx` และ `AuditRepository`

**Files:**
- Create: `sqlc.yaml`, `internal/adapter/postgres/sqlc_schema_app.sql`, `internal/adapter/postgres/queries/audit.sql`, `internal/adapter/postgres/store.go`, `internal/adapter/postgres/audit.go`
- Create (generated by `make sqlc`): `internal/adapter/postgres/sqlc/db.go`, `models.go`, `audit.sql.go`
- Test: `internal/adapter/postgres/store_test.go`
- Modify: `Makefile`, `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: mapper และ `mapError` (Task 3); `openPool` (milestone 1 tests); ตารางจาก Task 2
- Produces:
  - `type postgres.Store`, `func postgres.NewStore(pool *pgxpool.Pool) *Store`
  - unexported `(*Store).withinTx(ctx, fn func(*txRepositories) error) error`, `type txRepositories struct{ q *sqlc.Queries }`, `(*txRepositories).Audit() *AuditRepository` — แผน 3b/3c เพิ่ม accessor ต่อ
  - `type postgres.AuditRepository` ที่ implement `port.AuditRepository`
  - sqlc package `go-backend-stock/internal/adapter/postgres/sqlc` (ชื่อ package `sqlc`), query `InsertAuditEvent`
  - Make targets `sqlc`; `make tools` ติดตั้ง sqlc; `make check` ตรวจ diff ของ `internal/adapter/postgres/sqlc`
  - test helpers `newRuntimeStore(t)`, `newID(t)`, `auditEvent(t, metadata)`, `auditExists(t, id)`

- [ ] **Step 1: เขียน failing tests**

`internal/adapter/postgres/store_test.go`:

```go
package postgres

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// newRuntimeStore returns a Store connected as app_runtime, the role the API uses.
func newRuntimeStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(openPool(t, "TEST_RUNTIME_DATABASE_URL"))
}

func newID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func auditEvent(t *testing.T, metadata string) domain.AuditEvent {
	t.Helper()
	e, err := domain.NewAuditEvent(domain.AuditEvent{
		ID: newID(t), OccurredAt: time.Now().UTC().Truncate(time.Microsecond), EventType: domain.EventPortfolioCreated,
		ActorUserID: newID(t), RequestID: newID(t), TargetType: "portfolio", TargetID: newID(t).String(),
		Outcome: domain.AuditOutcomeSuccess, Metadata: json.RawMessage(metadata),
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// auditExists reads with the admin role: the runtime role cannot SELECT audit_events.
func auditExists(t *testing.T, id domain.ID) bool {
	t.Helper()
	var n int
	if err := openPool(t, "TEST_ADMIN_DATABASE_URL").QueryRow(t.Context(),
		`SELECT count(*) FROM app.audit_events WHERE id = $1`, pgUUID(id)).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n == 1
}

func TestAuditAppendStoresEvent(t *testing.T) {
	store := newRuntimeStore(t)
	e := auditEvent(t, `{"name":"Long term","quantity":"0.5"}`)
	e.SessionID, e.ReasonCode = newID(t), "created"

	if err := store.withinTx(t.Context(), func(r *txRepositories) error {
		return r.Audit().Append(t.Context(), e)
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	var (
		eventType, targetType, outcome, reason, targetID string
		actor, session                                   string
		metadata                                         map[string]string
		occurred                                         time.Time
	)
	err := openPool(t, "TEST_ADMIN_DATABASE_URL").QueryRow(t.Context(), `
		SELECT event_type, target_type, outcome, reason_code, target_id, actor_user_id::text, session_id::text, metadata, occurred_at
		FROM app.audit_events WHERE id = $1`, pgUUID(e.ID)).
		Scan(&eventType, &targetType, &outcome, &reason, &targetID, &actor, &session, &metadata, &occurred)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if eventType != string(e.EventType) || targetType != e.TargetType || outcome != string(e.Outcome) ||
		reason != e.ReasonCode || targetID != e.TargetID || actor != e.ActorUserID.String() ||
		session != e.SessionID.String() || metadata["quantity"] != "0.5" || !occurred.Equal(e.OccurredAt) {
		t.Errorf("stored row does not match the event: %s %s %s %s %v %v", eventType, outcome, actor, session, metadata, occurred)
	}
}

func TestAuditAppendWithoutActorOrSession(t *testing.T) {
	store := newRuntimeStore(t)
	e := auditEvent(t, `{}`)
	e.EventType, e.ActorUserID, e.Outcome, e.TargetID = domain.EventAuthLoginFailed, domain.ID{}, domain.AuditOutcomeFailure, ""

	if err := store.withinTx(t.Context(), func(r *txRepositories) error {
		return r.Audit().Append(t.Context(), e)
	}); err != nil {
		t.Fatalf("append failed-login event: %v", err)
	}
	var actorIsNull, targetIsNull bool
	if err := openPool(t, "TEST_ADMIN_DATABASE_URL").QueryRow(t.Context(),
		`SELECT actor_user_id IS NULL, target_id IS NULL FROM app.audit_events WHERE id = $1`, pgUUID(e.ID)).
		Scan(&actorIsNull, &targetIsNull); err != nil || !actorIsNull || !targetIsNull {
		t.Fatalf("actor/target NULL = %v/%v, err %v; want NULL for unauthenticated attempts", actorIsNull, targetIsNull, err)
	}
}

// TestWithinTxRollsBackEverythingWhenAuditFails is the failure injection of design section 4.5:
// a later write in the same transaction fails a database CHECK, and the earlier write must not
// survive. Milestone 3c repeats it with a portfolio as the earlier write.
func TestWithinTxRollsBackEverythingWhenAuditFails(t *testing.T) {
	store := newRuntimeStore(t)
	first := auditEvent(t, `{}`)
	bad := auditEvent(t, `{}`)
	bad.Metadata = json.RawMessage(`["not","an","object"]`) // bypasses NewAuditEvent; violates audit_events_metadata_object

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		if err := r.Audit().Append(t.Context(), first); err != nil {
			return err
		}
		return r.Audit().Append(t.Context(), bad)
	})

	if !errors.Is(err, domain.ErrInternal) {
		t.Fatalf("error = %v, want internal from the CHECK violation", err)
	}
	if auditExists(t, first.ID) || auditExists(t, bad.ID) {
		t.Fatal("a write from the failed transaction was committed")
	}
}

func TestWithinTxRollsBackOnCallbackError(t *testing.T) {
	store := newRuntimeStore(t)
	e := auditEvent(t, `{}`)
	sentinel := errors.New("business rule failed")

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		if err := r.Audit().Append(t.Context(), e); err != nil {
			return err
		}
		return sentinel
	})

	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want the callback's error", err)
	}
	if auditExists(t, e.ID) {
		t.Fatal("audit committed although the callback failed")
	}
}

func TestWithinTxRollsBackOnPanic(t *testing.T) {
	store := newRuntimeStore(t)
	e := auditEvent(t, `{}`)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic was swallowed")
			}
		}()
		_ = store.withinTx(t.Context(), func(r *txRepositories) error {
			if err := r.Audit().Append(t.Context(), e); err != nil {
				return err
			}
			panic("boom")
		})
	}()

	if auditExists(t, e.ID) {
		t.Fatal("audit committed although the callback panicked")
	}
}

// TestAuditSurvivesDeletionOfItsTarget: audit_events has no foreign keys, so deleting the user and
// portfolio an event mentions leaves the event in place (no cascade).
func TestAuditSurvivesDeletionOfItsTarget(t *testing.T) {
	admin := openPool(t, "TEST_ADMIN_DATABASE_URL")
	userID, portfolioID := newID(t), newID(t)
	now := time.Now().UTC()
	if _, err := admin.Exec(t.Context(),
		`INSERT INTO app.users (id, email, created_at, updated_at) VALUES ($1, $2, $3, $3)`,
		pgUUID(userID), "audit-target-"+userID.String()+"@example.com", now); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := admin.Exec(t.Context(),
		`INSERT INTO app.portfolios (id, user_id, name, currency, created_at, updated_at) VALUES ($1, $2, 'Doomed', 'USD', $3, $3)`,
		pgUUID(portfolioID), pgUUID(userID), now); err != nil {
		t.Fatalf("insert portfolio: %v", err)
	}
	e := auditEvent(t, `{}`)
	e.ActorUserID, e.TargetID = userID, portfolioID.String()
	if err := newRuntimeStore(t).withinTx(t.Context(), func(r *txRepositories) error {
		return r.Audit().Append(t.Context(), e)
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	if _, err := admin.Exec(t.Context(), `DELETE FROM app.portfolios WHERE id = $1`, pgUUID(portfolioID)); err != nil {
		t.Fatalf("delete portfolio: %v", err)
	}
	if _, err := admin.Exec(t.Context(), `DELETE FROM app.users WHERE id = $1`, pgUUID(userID)); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if !auditExists(t, e.ID) {
		t.Fatal("deleting the target removed its audit event")
	}
}
```

- [ ] **Step 2: รันให้เห็นว่าล้มเหลว**

Run: `go test -count=1 ./internal/adapter/postgres/`
Expected: FAIL ด้วย `undefined: Store`, `undefined: NewStore`, `undefined: txRepositories`

- [ ] **Step 3: เพิ่ม sqlc ใน Makefile และ CI**

แก้ `Makefile` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```make
GOLANGCI_LINT_VERSION := v2.14.0
```

   ด้วย

```make
GOLANGCI_LINT_VERSION := v2.14.0
SQLC_VERSION := v1.31.1
```

2. แทน

```make
GOLANGCI_LINT := $(BIN)/golangci-lint
```

   ด้วย

```make
GOLANGCI_LINT := $(BIN)/golangci-lint
SQLC := $(BIN)/sqlc
```

3. แทน

```make
.PHONY: tools swagger
```

   ด้วย

```make
.PHONY: tools swagger sqlc
```

4. แทน

```make
	CGO_ENABLED=0 GOBIN=$(BIN) go install -tags '$(GOOSE_TAGS)' github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)
```

   ด้วย

```make
	CGO_ENABLED=0 GOBIN=$(BIN) go install -tags '$(GOOSE_TAGS)' github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)
	# sqlc parses SQL with a C library, so it needs cgo (a C compiler) to build.
	CGO_ENABLED=1 GOBIN=$(BIN) go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
```

5. แทน

```make
	$(SWAGGER) validate docs/api/swagger.yaml
```

   ด้วย

```make
	$(SWAGGER) validate docs/api/swagger.yaml

sqlc:
	$(SQLC) generate
```

6. แทน

```make
check: tidy-check vet lint swagger test
	git diff --exit-code -- docs/api
```

   ด้วย

```make
check: tidy-check vet lint swagger sqlc test
	git diff --exit-code -- docs/api internal/adapter/postgres/sqlc
```

แก้ `.github/workflows/ci.yml` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```yaml
      - name: Bootstrap database roles
```

   ด้วย

```yaml
      - name: sqlc code is regenerated and committed
        run: |
          make sqlc
          git diff --exit-code -- internal/adapter/postgres/sqlc

      - name: Bootstrap database roles
```

Run: `make tools && ./bin/sqlc version`
Expected: `v1.31.1` (การ build ใช้ cgo ราว 1 นาที; ถ้าแจ้งว่าไม่มี C compiler ให้ติดตั้ง Xcode Command Line Tools ด้วย `xcode-select --install`)

- [ ] **Step 4: ตั้ง sqlc และเขียน query**

`sqlc.yaml`:

```yaml
version: "2"
sql:
  - engine: postgresql
    # The prelude declares schema app, which bootstrap creates outside goose; migrations/ is the schema.
    schema:
      - internal/adapter/postgres/sqlc_schema_app.sql
      - migrations
    queries: internal/adapter/postgres/queries
    gen:
      go:
        package: sqlc
        out: internal/adapter/postgres/sqlc
        sql_package: pgx/v5
```

`internal/adapter/postgres/sqlc_schema_app.sql`:

```sql
-- Read by sqlc only, before migrations/. In real databases the bootstrap (deploy/db/bootstrap.sql)
-- creates schema app before goose runs, so migrations never create it themselves.
CREATE SCHEMA app;
```

`internal/adapter/postgres/queries/audit.sql`:

```sql
-- name: InsertAuditEvent :exec
INSERT INTO app.audit_events (
    id, occurred_at, event_type, actor_user_id, request_id, session_id,
    target_type, target_id, outcome, reason_code, metadata
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
);
```

Run: `make sqlc && ls internal/adapter/postgres/sqlc`
Expected: `audit.sql.go  db.go  models.go` โดยไม่มี error (ถ้าเห็น `schema "app" does not exist` แปลว่า `sqlc_schema_app.sql` ไม่ได้อยู่ลำดับแรกใน `schema:`) และ `InsertAuditEventParams` มี field แบบ `pgtype.UUID`/`pgtype.Timestamptz`/`pgtype.Text`

- [ ] **Step 5: เขียน `store.go` และ `audit.go`**

`internal/adapter/postgres/store.go`:

```go
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
)

// Store owns the connection pool and builds repositories. It becomes the port.UnitOfWork once
// every repository exists (milestone 3c); until then transactions go through withinTx.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store over pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// txRepositories are the repositories bound to one database transaction. Later plans add one
// accessor per repository until it satisfies port.TxRepositories.
type txRepositories struct {
	q *sqlc.Queries
}

// Audit returns the audit repository bound to this transaction.
func (r *txRepositories) Audit() *AuditRepository {
	return &AuditRepository{q: r.q}
}

// withinTx runs fn in one READ COMMITTED transaction (row locks serialize writes, ADR-0009).
// The transaction commits only when fn returns nil; an error or a panic rolls it back, so a
// business change and its audit event are never committed apart.
func (s *Store) withinTx(ctx context.Context, fn func(*txRepositories) error) (err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("%w: begin transaction: %w", domain.ErrInternal, err)
	}
	// Roll back with a context that outlives a cancelled request.
	rollback := func() error { return tx.Rollback(context.WithoutCancel(ctx)) }
	defer func() {
		if p := recover(); p != nil {
			_ = rollback()
			panic(p)
		}
	}()

	if err := fn(&txRepositories{q: sqlc.New(tx)}); err != nil {
		if rbErr := rollback(); rbErr != nil {
			return errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit: %w", domain.ErrInternal, err)
	}
	return nil
}
```

`internal/adapter/postgres/audit.go`:

```go
package postgres

import (
	"context"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// AuditRepository appends audit events. The runtime role has INSERT only on audit_events.
type AuditRepository struct {
	q *sqlc.Queries
}

var _ port.AuditRepository = (*AuditRepository)(nil)

// Append inserts one event. Callers build events with domain.NewAuditEvent.
func (r *AuditRepository) Append(ctx context.Context, e domain.AuditEvent) error {
	return mapError(r.q.InsertAuditEvent(ctx, sqlc.InsertAuditEventParams{
		ID:          pgUUID(e.ID),
		OccurredAt:  pgTime(e.OccurredAt),
		EventType:   string(e.EventType),
		ActorUserID: pgUUID(e.ActorUserID),
		RequestID:   pgUUID(e.RequestID),
		SessionID:   pgUUID(e.SessionID),
		TargetType:  e.TargetType,
		TargetID:    pgText(e.TargetID),
		Outcome:     string(e.Outcome),
		ReasonCode:  pgText(e.ReasonCode),
		Metadata:    e.Metadata,
	}))
}
```

- [ ] **Step 6: รัน integration tests ให้ผ่าน**

Run: `go mod tidy && make test-integration`
Expected: `ok  	go-backend-stock/internal/adapter/postgres` และ `go.mod` ไม่เปลี่ยน (pgx, uuid และ decimal เป็น direct dependency อยู่แล้ว)

- [ ] **Step 7: พิสูจน์ว่า test จับการ commit ผิดได้**

ใน `internal/adapter/postgres/store.go` แทนชั่วคราวในบล็อก `if err := fn(...); err != nil {`:

```go
		if rbErr := rollback(); rbErr != nil {
			return errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
		return err
```

ด้วย

```go
		_ = rollback
		_ = tx.Commit(ctx)
		return errors.Join(err, fmt.Errorf("mutation: committed anyway"))
```

Run: `make test-integration`
Expected: FAIL ด้วย `audit committed although the callback failed` จากนั้นคืนโค้ดเดิมแล้วรัน `git diff --stat -- internal/adapter/postgres/store.go` ต้องไม่มี diff เทียบกับ Step 5 (`TestWithinTxRollsBackEverythingWhenAuditFails` ไม่จับ mutation นี้เพราะ PostgreSQL ไม่ยอม commit transaction ที่มีคำสั่งล้มอยู่แล้ว ซึ่งเป็นการป้องกันอีกชั้น)

- [ ] **Step 8: ตรวจทั้ง repo และ workflow**

Run: `make check && docker run --rm -v "$PWD":/repo -w /repo rhysd/actionlint:latest -color`
Expected: `make check` ผ่าน (`0 issues.`, swagger valid, tests `ok`, `git diff --exit-code -- docs/api internal/adapter/postgres/sqlc` ไม่มี diff) และ actionlint exit 0

- [ ] **Step 9: Commit**

```bash
git add sqlc.yaml Makefile .github/workflows/ci.yml internal/adapter/postgres
git commit -m "feat(db): add sqlc, transaction store and audit repository"
```

---

### Task 5: เอกสาร — design ของ milestone 3, phase 1 spec, milestone 2 design และ roadmap

**Files:**
- Modify: `docs/superpowers/specs/2026-10-17-milestone-3-persistence-design.md`, `docs/superpowers/specs/2026-10-05-phase-1-design.md`, `docs/superpowers/specs/2026-10-16-milestone-2-domain-ports-design.md`, `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md`

**Interfaces:**
- Consumes: ชื่อ constraint, ไฟล์ และพฤติกรรมจาก Tasks 1–4
- Produces: เอกสารที่ตรงกับโค้ด (AGENTS.md)

- [ ] **Step 1: แก้ design ของ milestone 3** (`docs/superpowers/specs/2026-10-17-milestone-3-persistence-design.md`)

1. แทนทุกที่ที่เขียน `portfolios_user_lower_name_key` (2 จุด) ด้วย `portfolios_user_name_unique` ซึ่งเป็นชื่อจริงใน `00003_portfolio.sql` และ `schema.dbml`
2. แทนบรรทัดสถานะ

```text
- **สถานะ:** รอผู้ใช้รีวิว (design ทั้ง 4 ส่วนได้รับการยืนยันในบทสนทนา 2026-10-17)
```

   ด้วย

```text
- **สถานะ:** ผู้ใช้ยืนยันแล้ว 2026-10-17; แผน 3a: [2026-10-17-milestone-3a-schema-sqlc-audit.md](../plans/2026-10-17-milestone-3a-schema-sqlc-audit.md)
```

3. ต่อท้ายบรรทัดในหัวข้อ 4.1 ที่ขึ้นต้นด้วย ``- `sqlc.yaml` อ่าน schema จาก `migrations/` โดยตรง`` ด้วย

```text
 ร่วมกับ `internal/adapter/postgres/sqlc_schema_app.sql` ที่มีเพียง `CREATE SCHEMA app;` ให้ sqlc อ่านก่อน migrations เพราะ schema นี้สร้างโดย bootstrap ไม่ใช่ goose
```

4. ใต้ bullet `- ไม่รองรับ nested transaction` ในหัวข้อ 4.4 เพิ่ม

```text
- **ลำดับการสร้าง:** แผน 3a สร้าง `Store` กับ `withinTx` ภายใน adapter และ `txRepositories` ที่มีเฉพาะ `Audit()`; แผน 3b/3c เพิ่ม accessor ทีละ repository และ `Store` implement `port.UnitOfWork` เมื่อครบในแผน 3c (Go ต้องการ method ครบทุกตัวของ `port.TxRepositories` จึงยังใช้ port ตรงไม่ได้)
```

5. ต่อท้ายย่อหน้าในหัวข้อ 4.5 (ที่ขึ้นต้นด้วย `ใช้ DB จริง ไม่ mock:`) ด้วย

```text
 แผน 3a ทดสอบด้วย audit สองรายการในหนึ่ง transaction (รายการที่สองละเมิด CHECK) เพราะยังไม่มี `PortfolioRepository`; แผน 3c ทำซ้ำตามข้อความข้างต้นด้วยพอร์ตจริง
```

- [ ] **Step 2: แก้ phase 1 spec หัวข้อ 8 และ milestone 2 design** (Minor ที่ส่งต่อมา)

ใน `docs/superpowers/specs/2026-10-05-phase-1-design.md` แทน

```text
`domain.ID` เป็น UUID string ที่ผ่าน validation
```

ด้วย

```text
`domain.ID` ห่อ UUID ที่ผ่าน validation (สร้างด้วย `NewID()` หรือ `ParseID(field, s)`; `String()` คืนรูปมาตรฐานตัวพิมพ์เล็ก)
```

ใน `docs/superpowers/specs/2026-10-16-milestone-2-domain-ports-design.md` แทน `` `ID`, `NewID()`, `ParseID(s)` `` ด้วย `` `ID`, `NewID()`, `ParseID(field, s)` ``

- [ ] **Step 3: แก้ roadmap milestone 3** (`docs/superpowers/plans/2026-10-05-phase-1-roadmap.md`)

1. ใต้หัวข้อ `### 3. Schema + goose + sqlc + repositories + audit persistence` เพิ่มเหนือบรรทัด `**ไฟล์หลัก:**`:

```text
**แผนลงมือ:** [design](../specs/2026-10-17-milestone-3-persistence-design.md) — [3a](2026-10-17-milestone-3a-schema-sqlc-audit.md) (schema, roles, sqlc, audit), 3b (repositories ฝั่ง auth), 3c (repositories ฝั่งพอร์ต)
```

2. tick เป็น `- [x]` สามรายการ: รายการแรก (`เพิ่มตาราง constraints/indexes ...`), รายการที่สี่ (`ทดสอบ runtime role แก้/ลบ audit ไม่ได้ ...`) และรายการที่ห้า (`เปิด CI ตรวจ goose บน DB ว่าง ...`)
3. ต่อท้ายรายการที่สอง (`เขียน SQL queries, generate sqlc ...`) ด้วย ` (3a: sqlc + mapper + audit; repositories ที่เหลืออยู่ใน 3b/3c)` และต่อท้ายรายการที่สาม (`สร้าง transaction coordination ...`) ด้วย ` (3a: withinTx + failure injection ด้วย audit; ทำซ้ำกับพอร์ตใน 3c)`
4. ในบรรทัด `**Status:**` แทน `ขั้นต่อไปคือแตกแผนย่อยของ milestone 3 ยังไม่เริ่ม` ด้วย `milestone 3 แผน 3a ลงมือครบแล้ว รอผู้ใช้ตรวจรับ จากนั้นแผน 3b`

- [ ] **Step 4: ตรวจทั้ง repo**

Run: `make check && make test-integration`
Expected: ผ่านทั้งคู่ (integration tests ไม่มี SKIP)

- [ ] **Step 5: Commit**

```bash
git add docs
git commit -m "docs: record milestone 3a schema, sqlc and audit persistence"
```
