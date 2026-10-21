# Milestone 3c — Repositories ฝั่งพอร์ต Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** implement `InstrumentRepository`, `PortfolioRepository`, `TransactionRepository` และ `IdempotencyRepository` บน PostgreSQL จริงผ่าน role `app_runtime` พร้อม keyset pagination และให้ `Store` เป็น `port.UnitOfWork` — แผนสุดท้ายจาก 3 แผนของ milestone 3

**Architecture:** ใช้รูปแบบเดียวกับแผน 3b: repository เป็น struct ใน `internal/adapter/postgres` ที่ถือ `*sqlc.Queries` บน pool หรือบน transaction ของ `withinTx`; ทุก query ฝั่งพอร์ตกรองเจ้าของผ่าน `portfolios.user_id`; insert ที่คาดว่าจะชนใช้ `ON CONFLICT DO NOTHING` + จำนวนแถว; update ที่ไม่มีแถวตรงจะตรวจต่อเพื่อแยก version conflict จาก not found; cursor เป็น base64url JSON ที่ผูกกับชนิดของรายการและ filter; แผนนี้แก้ `withinTx` ให้ rollback เมื่อ callback ออกด้วย `runtime.Goexit` ก่อน (พบระหว่าง prototype)

**Tech Stack:** Go 1.27 (go.mod `go 1.27.1`), pgx v5.11.0, sqlc v1.31.1 (cgo), shopspring/decimal v1.4.0, PostgreSQL 17.11, golangci-lint v2.14.0

**Spec:** [docs/superpowers/specs/2026-10-17-milestone-3-persistence-design.md](../specs/2026-10-17-milestone-3-persistence-design.md) หัวข้อ 4.4–4.6, 6 และ 7 (แผน 3c) ซึ่งอ้าง [phase 1 spec](../specs/2026-10-05-phase-1-design.md) หัวข้อ 7–9 และ 11 และ [AGENTS.md](../../../AGENTS.md)

## Global Constraints

- ทุก query ระบุ `app.` หน้าชื่อตาราง; ห้ามแก้ไฟล์ใน `internal/adapter/postgres/sqlc/` ด้วยมือ ให้แก้ query แล้ว `make sqlc`
- ทุก query ที่อ่านหรือแก้ข้อมูลพอร์ต/รายการ/idempotency ต้องกรองเจ้าของผ่าน `user_id` ของ portfolio; ห้ามเชื่อ user_id จาก request body
- ของผู้อื่นหรือไม่มีอยู่ → `domain.ErrNotFound` เหมือนกัน (ไม่เปิดเผยว่ามีอยู่)
- เงิน ราคา ค่าธรรมเนียม และจำนวนหุ้นผ่าน `Parse*` และ `numericString`/`pgNumeric` เท่านั้น ห้ามผ่าน float
- `app_runtime` ไม่มี UPDATE บน `instruments` และ phase 1 ไม่รองรับการเปลี่ยน symbol: asset ที่อยู่ใต้ symbol อื่นแล้ว → `unknown_symbol` (ผู้ใช้เลือก 2026-10-21)
- `Lock*` บน repository ที่สร้างจาก pool ต้องคืน `internal` โดยไม่ส่ง query
- `now`/`at` มาจาก parameter (Clock ของ service) ไม่ใช้ `now()` ของ DB
- ค่าจาก DB ที่ parse ไม่ผ่านเป็น `internal` ผ่าน `fromStored` ไม่ใช่ `invalid_input`; cursor มาจาก client จึงผิดรูปแบบ → `invalid_input` ที่ขึ้นต้นด้วย `cursor`
- ไม่เพิ่ม dependency ใหม่; ไม่มี migration ใหม่ในแผนนี้ (schema ครบตั้งแต่แผน 3a)
- integration tests ต่อเป็น `app_runtime` และข้อมูลสุ่มต่อ test ไม่ล้างตาราง; ในเครื่องต้องเปิด PostgreSQL ของ compose ก่อน (`docker compose up -d postgres migrate`) และใช้ `make test-integration` เสมอ — `go test` เปล่าๆ จะ SKIP integration tests เพราะไม่มี env `TEST_*`
- ต้องเห็น test ล้มก่อนแล้วจึงเขียนโค้ด และตรวจผล `make test-integration` ทุกครั้งว่าไม่มี `SKIP`
- ห้าม `t.Fatal` ภายใน callback ของ transaction ใน test ใหม่ ให้ใช้ `t.Error` แล้ว return error
- commit message ไม่มี `Co-Authored-By` หรือชื่อ Claude; commit ในเครื่องเท่านั้น ห้าม push
- รันทุกคำสั่งที่ root ของ repo บน branch ที่ผู้ใช้ใช้อยู่ (`feature/creat_login_and_register`); Go ในเครื่องคือ `/usr/local/go/bin/go` (1.27.2)

## Review Focus

1. **ข้อมูลรั่วข้ามผู้ใช้** — ผู้ใช้ B อ่าน แก้ ลบ หรือเพิ่มรายการในพอร์ตของ A ได้ หรือเห็น idempotency record ของ A — Task 3 `TestPortfolioOwnershipIsolation`, Task 4 `TestTransactionOwnershipIsolation`, Task 5 `TestIdempotencyKeysAreScoped`
2. **ขายเกินที่ถือเมื่อคำขอพร้อมกัน** — สองคำขอขายพร้อมกันเห็นยอดเดิมทั้งคู่ — Task 5 `TestConcurrentSellsNeverOversell` (พิสูจน์ว่า `LockOwned` serialize จริง)
3. **หน้ารายการซ้ำหรือหายเมื่อเวลาเท่ากัน หรือ cursor ข้าม filter** — Task 3 `TestPortfolioListOwnedPagesWithoutRepeats`, Task 4 `TestTransactionListPagesWithoutRepeats`, `TestTransactionListFilters`, `TestTransactionListRejectsCursorFromOtherFilters`
4. **ทศนิยมเพี้ยนหรือรายการย้ายพอร์ต** — ค่า 18+10 หลักและ `0.0000000001` ต้องได้ค่าเดิม; update ห้ามย้าย portfolio และห้ามเขียนทับด้วย version เก่า — Task 4 `TestTransactionInsertKeepsEveryDigit`, `TestTransactionUpdateChecksVersionAndNeverMovesPortfolio`
5. **ลบพอร์ตที่ยังมีรายการ หรือ symbol ถูกเปลี่ยนชื่อแล้วได้ 500** — Task 4 `TestPortfolioDeleteOwnedEmptyRefusesWhileTradesRemain`, `TestPortfolioDeleteRacingAnUnlockedInsertIsNotEmpty`, Task 2 `TestInstrumentSymbolChangeIsUnknownSymbol`

---

## File Structure

| ไฟล์ | หน้าที่ | Task |
| --- | --- | --- |
| `internal/adapter/postgres/store.go` (+`store_test.go`) | `withinTx` rollback เมื่อ `runtime.Goexit`; accessor ของ repositories; `WithinTransaction` | 1–5 |
| `internal/adapter/postgres/queries/instruments.sql`, `instruments.go` (+test) | `InstrumentRepository` และ helper `randomSymbol`/`newInstrument` | 2 |
| `internal/core/port/portfolio.go` | comment ของ `InstrumentRepository.Upsert` | 2 |
| `internal/adapter/postgres/queries/portfolios.sql`, `portfolios.go`, `cursor.go` (+test) | `PortfolioRepository`, keyset cursor | 3 |
| `internal/adapter/postgres/queries/transactions.sql`, `transactions.go` (+test) | `TransactionRepository`, fingerprint ของ filter; FK mapping ย้ายไป `DeleteOwnedEmpty` (`errors.go`, `errors_test.go`) | 4 |
| `internal/core/domain/transaction.go` (+test) | `NewTransactionFilter` ปฏิเสธค่า filter ว่าง | 4 |
| `internal/adapter/postgres/queries/idempotency.sql`, `idempotency.go` (+test), `uow_test.go` | `IdempotencyRepository`, `Store` เป็น `port.UnitOfWork`, failure injection และขายพร้อมกัน | 5 |
| `internal/adapter/postgres/sqlc/` (generated) | โค้ดที่ `make sqlc` สร้าง | 2–5 |
| spec, roadmap, README | เอกสาร | 6 |

---

### Task 1: `withinTx` จบ transaction เมื่อ callback ออกจาก goroutine

**Files:**
- Modify: `internal/adapter/postgres/store_test.go`, `internal/adapter/postgres/store.go`

**Interfaces:**
- Consumes: `Store.withinTx`, `auditEvent`, `auditExists`, `integrationURL` (แผน 3a)
- Produces: `withinTx` rollback และคืน connection เมื่อ `fn` panic หรือ `runtime.Goexit` (เช่น `t.Fatal` ใน callback)

- [ ] **Step 1: เขียน failing tests**

แก้ `internal/adapter/postgres/store_test.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
```

   ด้วย

```go
import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
```

2. แทน

```go
	"time"

	"go-backend-stock/internal/core/domain"
)
```

   ด้วย

```go
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"go-backend-stock/internal/core/domain"
)
```

3. ต่อท้ายไฟล์ด้วย

```go

// TestWithinTxEndsTransactionWhenCallbackExitsGoroutine: a callback that leaves through
// runtime.Goexit (t.Fatal does this) skips the normal return path; the transaction must still be
// rolled back and its connection returned, or pool.Close would wait forever.
func TestWithinTxEndsTransactionWhenCallbackExitsGoroutine(t *testing.T) {
	// Not openPool: its cleanup would block on the leaked connection instead of reporting it.
	pool, err := pgxpool.New(t.Context(), integrationURL(t, "TEST_RUNTIME_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(pool)
	e := auditEvent(t, `{}`)
	done := make(chan struct{})

	go func() {
		defer close(done)
		_ = store.withinTx(t.Context(), func(r *txRepositories) error {
			if err := r.Audit().Append(t.Context(), e); err != nil {
				return err
			}
			runtime.Goexit()
			return nil
		})
	}()
	<-done

	if n := pool.Stat().AcquiredConns(); n != 0 {
		t.Fatalf("%d connections still held after the callback exited", n)
	}
	pool.Close()
	if auditExists(t, e.ID) {
		t.Fatal("audit committed although the callback never returned")
	}
}
```

- [ ] **Step 2: รันให้เห็นว่าล้มเหลว**

Run: `make test-integration`
Expected: FAIL ด้วย `TestWithinTxEndsTransactionWhenCallbackExitsGoroutine` และ `1 connections still held after the callback exited`

- [ ] **Step 3: เขียนโค้ด**

แก้ `internal/adapter/postgres/store.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
// withinTx runs fn in one READ COMMITTED transaction (row locks serialize writes, ADR-0009).
// The transaction commits only when fn returns nil; an error or a panic rolls it back, so a
// business change and its audit event are never committed apart.
```

   ด้วย

```go
// withinTx runs fn in one READ COMMITTED transaction (row locks serialize writes, ADR-0009).
// The transaction commits only when fn returns nil; an error, a panic or runtime.Goexit rolls it
// back, so a business change and its audit event are never committed apart.
```

2. แทน

```go
	rollback := func() error { return tx.Rollback(context.WithoutCancel(ctx)) }
	defer func() {
		if p := recover(); p != nil {
			_ = rollback()
			panic(p)
		}
	}()

	if err := fn(&txRepositories{q: sqlc.New(tx)}); err != nil {
```

   ด้วย

```go
	rollback := func() error { return tx.Rollback(context.WithoutCancel(ctx)) }
	returned := false
	defer func() {
		// fn panicked or left the goroutine (runtime.Goexit, as t.Fatal does): end the transaction
		// so its connection goes back to the pool. A panic keeps unwinding after this.
		if !returned {
			_ = rollback()
		}
	}()

	err = fn(&txRepositories{q: sqlc.New(tx)})
	returned = true
	if err != nil {
```

- [ ] **Step 4: รัน tests และ lint ให้ผ่าน**

Run: `gofmt -l internal && make test && make test-integration && make lint`
Expected: `gofmt` ไม่พิมพ์อะไร, `ok` ทุกแพ็กเกจ, `ok  	go-backend-stock/internal/adapter/postgres` โดยไม่มี `SKIP` (ตรวจด้วย `GOFLAGS=-v make test-integration 2>&1 | grep -c '^--- SKIP'` ได้ `0`) และ `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add -A internal
git commit -m "fix(db): roll back the transaction when the callback exits its goroutine"
```

- [ ] **Step 6: พิสูจน์ว่า test จับการเขียนผิดได้**

ใน `internal/adapter/postgres/store.go` แทนชั่วคราว

```go
	rollback := func() error { return tx.Rollback(context.WithoutCancel(ctx)) }
	returned := false
	defer func() {
		// fn panicked or left the goroutine (runtime.Goexit, as t.Fatal does): end the transaction
		// so its connection goes back to the pool. A panic keeps unwinding after this.
		if !returned {
			_ = rollback()
		}
	}()

	err = fn(&txRepositories{q: sqlc.New(tx)})
	returned = true
	if err != nil {
```

ด้วย

```go
	rollback := func() error { return tx.Rollback(context.WithoutCancel(ctx)) }
	defer func() {
		if p := recover(); p != nil {
			_ = rollback()
			panic(p)
		}
	}()

	if err := fn(&txRepositories{q: sqlc.New(tx)}); err != nil {
```

Run: `make test-integration` (ดูผลของ `TestWithinTxEndsTransactionWhenCallbackExitsGoroutine`)
Expected: FAIL ด้วย `1 connections still held after the callback exited`

จากนั้นคืนไฟล์: `git checkout -- internal/adapter/postgres/store.go && git status --short` ต้องไม่แสดงอะไร

- [ ] **Step 7: ตรวจทั้ง repo**

Run: `make check`
Expected: ผ่าน (`0 issues.`, swagger valid, tests `ok`, ไม่มี diff ของ `docs/api` และ `internal/adapter/postgres/sqlc` ถูก commit ครบ)

---

### Task 2: `InstrumentRepository`

**Files:**
- Create: `internal/adapter/postgres/queries/instruments.sql`, `internal/adapter/postgres/instruments.go`, `internal/adapter/postgres/instruments_test.go`
- Modify: `internal/adapter/postgres/store.go`, `internal/core/port/portfolio.go`
- Generated by `make sqlc`: `internal/adapter/postgres/sqlc/instruments.sql.go`

**Interfaces:**
- Consumes: `withinTx`, `auditEvent`, `auditExists`, `newID`, `newRuntimeStore` (3a); `now`, `runConcurrently` (3b); `domain.NewInstrument`
- Produces: `Store.Instruments()`, `(*txRepositories).Instruments()`; `errSymbolChanged` (code `unknown_symbol`); test helpers `randomSymbol(t) domain.Symbol`, `newInstrument(t, symbol, assetID) domain.Instrument`

- [ ] **Step 1: เขียน failing tests**

`internal/adapter/postgres/instruments_test.go`:

```go
package postgres

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"go-backend-stock/internal/core/domain"
)

func TestInstrumentUpsertAndBySymbol(t *testing.T) {
	store := newRuntimeStore(t)
	i := newInstrument(t, randomSymbol(t), "asset-"+newID(t).String())

	stored, err := store.Instruments().Upsert(t.Context(), i)
	if err != nil || stored.ID != i.ID || stored.Symbol != i.Symbol || stored.ProviderAssetID != i.ProviderAssetID ||
		stored.Name != i.Name || stored.Exchange != i.Exchange || stored.AssetClass != i.AssetClass ||
		stored.Status != i.Status || stored.Source != i.Source || !stored.VerifiedAt.Equal(i.VerifiedAt) {
		t.Fatalf("Upsert = %+v, %v; want %+v", stored, err, i)
	}
	got, err := store.Instruments().BySymbol(t.Context(), i.Symbol)
	if err != nil || got.ID != i.ID {
		t.Fatalf("BySymbol = %+v, %v", got, err)
	}
	if _, err := store.Instruments().BySymbol(t.Context(), randomSymbol(t)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("BySymbol(unknown) error = %v, want not_found", err)
	}
}

func TestInstrumentDemoSourceNeedsNoProviderAssetID(t *testing.T) {
	store := newRuntimeStore(t)
	at := now()
	i, err := domain.NewInstrument(domain.Instrument{
		ID: newID(t), Symbol: randomSymbol(t), Name: "Demo Corp", Exchange: "DEMO", AssetClass: domain.AssetClassUSEquity,
		Status: domain.InstrumentStatusActive, Source: domain.InstrumentSourceDemo, VerifiedAt: at, CreatedAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.Instruments().Upsert(t.Context(), i)
	if err != nil || got.ProviderAssetID != "" || got.Source != domain.InstrumentSourceDemo {
		t.Fatalf("Upsert(demo) = %+v, %v", got, err)
	}
}

// TestInstrumentConcurrentUpsertReturnsOneRow: requests that verified the same symbol at the same
// time all get the row that won, so their transactions reference one instrument.
func TestInstrumentConcurrentUpsertReturnsOneRow(t *testing.T) {
	store := newRuntimeStore(t)
	symbol := randomSymbol(t)
	assetID := "asset-" + newID(t).String()
	ids := make(chan domain.ID, 10)

	succeeded, errs := runConcurrently(10, func() error {
		got, err := store.Instruments().Upsert(t.Context(), newInstrument(t, symbol, assetID))
		ids <- got.ID
		return err
	})

	if succeeded != 10 {
		t.Fatalf("%d of 10 concurrent upserts succeeded: %v", succeeded, errs)
	}
	close(ids)
	first := <-ids
	for id := range ids {
		if id != first {
			t.Fatalf("concurrent upserts returned different rows: %s and %s", first, id)
		}
	}
}

// TestInstrumentSymbolChangeIsUnknownSymbol: the provider reports a stored asset under a new
// symbol (FB -> META). Phase 1 does not follow symbol changes, so the request gets a readable
// unknown_symbol instead of a 500, and the transaction stays usable.
func TestInstrumentSymbolChangeIsUnknownSymbol(t *testing.T) {
	store := newRuntimeStore(t)
	assetID := "asset-" + newID(t).String()
	if _, err := store.Instruments().Upsert(t.Context(), newInstrument(t, randomSymbol(t), assetID)); err != nil {
		t.Fatal(err)
	}
	renamed := newInstrument(t, randomSymbol(t), assetID)
	e := auditEvent(t, `{}`)

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		_, err := r.Instruments().Upsert(t.Context(), renamed)
		if de := (*domain.Error)(nil); !errors.As(err, &de) || de.Code != domain.CodeUnknownSymbol {
			t.Errorf("Upsert(renamed asset) error = %v, want unknown_symbol", err) // not Fatal inside the callback
		}
		return r.Audit().Append(t.Context(), e)
	})

	if err != nil || !auditExists(t, e.ID) {
		t.Fatalf("transaction after the symbol change: %v", err)
	}
	if _, err := store.Instruments().BySymbol(t.Context(), renamed.Symbol); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("renamed symbol was stored: %v", err)
	}
}

// randomSymbol returns a valid 15-character symbol no other test uses.
func randomSymbol(t *testing.T) domain.Symbol {
	t.Helper()
	b := make([]byte, 7)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	s, err := domain.ParseSymbol("T" + strings.ToUpper(hex.EncodeToString(b)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newInstrument(t *testing.T, symbol domain.Symbol, assetID string) domain.Instrument {
	t.Helper()
	at := now()
	i, err := domain.NewInstrument(domain.Instrument{
		ID: newID(t), Symbol: symbol, ProviderAssetID: assetID, Name: "Test Corp", Exchange: "NASDAQ",
		AssetClass: domain.AssetClassUSEquity, Status: domain.InstrumentStatusActive, Source: domain.InstrumentSourceAlpaca,
		VerifiedAt: at, CreatedAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	return i
}
```

- [ ] **Step 2: รันให้เห็นว่าล้มเหลว**

Run: `make test-integration`
Expected: FAIL ด้วย `[build failed]` และ `store.Instruments undefined (type *Store has no field or method Instruments)`

- [ ] **Step 3: เขียน queries แล้ว generate**

`internal/adapter/postgres/queries/instruments.sql`:

```sql
-- name: GetInstrumentBySymbol :one
SELECT id, symbol, provider_asset_id, name, exchange, asset_class, status, source, verified_at, created_at, updated_at
FROM app.instruments WHERE symbol = $1;

-- ON CONFLICT DO NOTHING covers both the symbol and the (source, provider_asset_id) unique rules,
-- so a concurrent insert of the same instrument keeps the transaction usable.
-- name: InsertInstrument :execrows
INSERT INTO app.instruments (id, symbol, provider_asset_id, name, exchange, asset_class, status, source, verified_at, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
ON CONFLICT DO NOTHING;
```

Run: `make sqlc && ls internal/adapter/postgres/sqlc`
Expected: ไม่มี error และมี `instruments.sql.go` เพิ่ม

- [ ] **Step 4: เขียนโค้ด**

`internal/adapter/postgres/instruments.go`:

```go
package postgres

import (
	"context"
	"errors"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// errSymbolChanged answers a provider that reports a known asset under a new symbol (FB -> META).
// Phase 1 does not support symbol changes (AGENTS.md); the user chose unknown_symbol (2026-10-21).
var errSymbolChanged = domain.NewError(domain.CodeUnknownSymbol,
	"symbol was renamed by the data provider; renamed symbols are not supported yet")

// InstrumentRepository stores verified instruments shared by every user.
type InstrumentRepository struct {
	q *sqlc.Queries
}

var _ port.InstrumentRepository = (*InstrumentRepository)(nil)

// BySymbol returns the instrument with this symbol, or domain.ErrNotFound.
func (r *InstrumentRepository) BySymbol(ctx context.Context, symbol domain.Symbol) (domain.Instrument, error) {
	row, err := r.q.GetInstrumentBySymbol(ctx, symbol.String())
	if err != nil {
		return domain.Instrument{}, mapError(err)
	}
	return instrumentFromRow(row)
}

// Upsert stores an instrument built with domain.NewInstrument and returns the stored row. When
// the symbol already exists (a concurrent request inserted it first) it returns that row. When
// the provider asset is already stored under another symbol it returns unknown_symbol, because
// phase 1 cannot follow a symbol change. Either way the transaction stays usable.
func (r *InstrumentRepository) Upsert(ctx context.Context, i domain.Instrument) (domain.Instrument, error) {
	if _, err := r.q.InsertInstrument(ctx, sqlc.InsertInstrumentParams{
		ID:              pgUUID(i.ID),
		Symbol:          i.Symbol.String(),
		ProviderAssetID: pgText(i.ProviderAssetID),
		Name:            i.Name,
		Exchange:        i.Exchange,
		AssetClass:      string(i.AssetClass),
		Status:          string(i.Status),
		Source:          string(i.Source),
		VerifiedAt:      pgTime(i.VerifiedAt),
		CreatedAt:       pgTime(i.CreatedAt),
	}); err != nil {
		return domain.Instrument{}, mapError(err)
	}
	stored, err := r.BySymbol(ctx, i.Symbol)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Instrument{}, errSymbolChanged
	}
	return stored, err
}

func instrumentFromRow(row sqlc.AppInstrument) (domain.Instrument, error) {
	id, err := idFromPG("instrument.id", row.ID)
	if err != nil {
		return domain.Instrument{}, err
	}
	symbol, err := domain.ParseSymbol(row.Symbol)
	if err != nil {
		return domain.Instrument{}, fromStored(err)
	}
	assetClass, err := domain.ParseAssetClass(row.AssetClass)
	if err != nil {
		return domain.Instrument{}, fromStored(err)
	}
	status, err := domain.ParseInstrumentStatus(row.Status)
	if err != nil {
		return domain.Instrument{}, fromStored(err)
	}
	source, err := domain.ParseInstrumentSource(row.Source)
	if err != nil {
		return domain.Instrument{}, fromStored(err)
	}
	return domain.Instrument{
		ID: id, Symbol: symbol, ProviderAssetID: row.ProviderAssetID.String, Name: row.Name, Exchange: row.Exchange,
		AssetClass: assetClass, Status: status, Source: source,
		VerifiedAt: timeFromPG(row.VerifiedAt), CreatedAt: timeFromPG(row.CreatedAt), UpdatedAt: timeFromPG(row.UpdatedAt),
	}, nil
}
```

แก้ `internal/adapter/postgres/store.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
// RateLimits returns the rate-limit repository. Admit runs its own transaction.
func (s *Store) RateLimits() port.RateLimitRepository { return &RateLimitRepository{store: s} }
```

   ด้วย

```go
// RateLimits returns the rate-limit repository. Admit runs its own transaction.
func (s *Store) RateLimits() port.RateLimitRepository { return &RateLimitRepository{store: s} }

// Instruments returns the instrument repository over the pool.
func (s *Store) Instruments() port.InstrumentRepository {
	return &InstrumentRepository{q: sqlc.New(s.pool)}
}
```

2. แทน

```go
func (r *txRepositories) OTPChallenges() port.OTPChallengeRepository {
	return &OTPChallengeRepository{q: r.q, inTx: true}
}
```

   ด้วย

```go
func (r *txRepositories) OTPChallenges() port.OTPChallengeRepository {
	return &OTPChallengeRepository{q: r.q, inTx: true}
}

// Instruments returns the instrument repository bound to this transaction.
func (r *txRepositories) Instruments() port.InstrumentRepository {
	return &InstrumentRepository{q: r.q}
}
```

แก้ `internal/core/port/portfolio.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
	// Upsert returns the stored row; when a concurrent request inserted the symbol first it
	// returns that existing row.
```

   ด้วย

```go
	// Upsert returns the stored row; when a concurrent request inserted the symbol first it
	// returns that existing row. When the provider asset is already stored under another symbol
	// (the provider renamed it, FB -> META) it returns an unknown_symbol error: phase 1 does not
	// follow symbol changes. Neither case aborts the transaction.
```

- [ ] **Step 5: รัน tests และ lint ให้ผ่าน**

Run: `gofmt -l internal && make test && make test-integration && make lint`
Expected: `gofmt` ไม่พิมพ์อะไร, `ok` ทุกแพ็กเกจ, `ok  	go-backend-stock/internal/adapter/postgres` โดยไม่มี `SKIP` (ตรวจด้วย `GOFLAGS=-v make test-integration 2>&1 | grep -c '^--- SKIP'` ได้ `0`) และ `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add -A internal
git commit -m "feat(db): add instrument repository and reject renamed symbols"
```

- [ ] **Step 7: พิสูจน์ว่า test จับการเขียนผิดได้**

ใน `internal/adapter/postgres/queries/instruments.sql` แทนชั่วคราว

```sql
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
ON CONFLICT DO NOTHING;
```

ด้วย

```sql
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10);
```

Run: `make sqlc && make test-integration` (ดูผลของ `TestInstrumentConcurrentUpsertReturnsOneRow`)
Expected: FAIL ด้วย `1 of 10 concurrent upserts succeeded` และ `instruments_symbol_key`

จากนั้นคืนไฟล์: `git checkout -- internal/adapter/postgres/queries/instruments.sql && make sqlc && git status --short` ต้องไม่แสดงอะไร

- [ ] **Step 8: ตรวจทั้ง repo**

Run: `make check`
Expected: ผ่าน (`0 issues.`, swagger valid, tests `ok`, ไม่มี diff ของ `docs/api` และ `internal/adapter/postgres/sqlc` ถูก commit ครบ)

---

### Task 3: `PortfolioRepository` และ keyset cursor

**Files:**
- Create: `internal/adapter/postgres/queries/portfolios.sql`, `internal/adapter/postgres/cursor.go`, `internal/adapter/postgres/portfolios.go`, `internal/adapter/postgres/portfolios_test.go`
- Modify: `internal/adapter/postgres/store.go`
- Generated by `make sqlc`: `internal/adapter/postgres/sqlc/portfolios.sql.go`

**Interfaces:**
- Consumes: `insertUser`, `now`, `newID`, `auditEvent`, `auditExists` (3a/3b); `errLockOutsideTx` (3b); `domain.ParsePage`, `domain.ParsePortfolioName`
- Produces: `Store.Portfolios()`, `(*txRepositories).Portfolios()` (ตั้ง `inTx`); `cursorPosition`, `encodeCursor`, `decodeCursor(raw, kind, filter)`, `errInvalidCursor`, `cursorKindPortfolios`, `cursorKindTransactions`; test helpers `page(t, limit, cursor)`, `newPortfolio(t, userID, name, createdAt)`, `insertPortfolio(t, store, userID)`, `mustVersion(t, n)`

- [ ] **Step 1: เขียน failing tests**

`internal/adapter/postgres/portfolios_test.go`:

```go
package postgres

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestPortfolioInsertAndGetOwned(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := newPortfolio(t, user.ID, "Long term", now())
	desc, err := domain.ParsePortfolioDescription("Buy and hold.\r\nReview yearly.")
	if err != nil {
		t.Fatal(err)
	}
	p.Description = desc
	if err := store.Portfolios().Insert(t.Context(), p); err != nil {
		t.Fatal(err)
	}

	got, err := store.Portfolios().GetOwned(t.Context(), user.ID, p.ID)
	if err != nil || got.ID != p.ID || got.UserID != user.ID || got.Name.String() != "Long term" ||
		got.Description.String() != desc.String() || got.Currency != domain.CurrencyUSD || got.Version.Int32() != 1 ||
		!got.CreatedAt.Equal(p.CreatedAt) || !got.UpdatedAt.Equal(p.CreatedAt) {
		t.Fatalf("GetOwned = %+v, %v; want %+v at version 1", got, err, p)
	}
	if _, err := store.Portfolios().GetOwned(t.Context(), user.ID, newID(t)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetOwned(unknown) error = %v, want not_found", err)
	}
}

// TestPortfolioNameIsUniquePerOwnerIgnoringCase: "Long term" and "long TERM" collide for one
// owner, the conflict keeps the transaction usable, another owner may use the name, and a deleted
// portfolio frees its name.
func TestPortfolioNameIsUniquePerOwnerIgnoringCase(t *testing.T) {
	store := newRuntimeStore(t)
	owner := insertUser(t, store)
	first := newPortfolio(t, owner.ID, "Long term", now())
	if err := store.Portfolios().Insert(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	e := auditEvent(t, `{}`)

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		if err := r.Portfolios().Insert(t.Context(), newPortfolio(t, owner.ID, "long TERM", now())); !errors.Is(err, domain.ErrPortfolioNameConflict) {
			t.Errorf("same name in another case error = %v, want portfolio_name_conflict", err)
		}
		return r.Audit().Append(t.Context(), e)
	})
	if err != nil || !auditExists(t, e.ID) {
		t.Fatalf("transaction after the name conflict: %v (audit committed: %v)", err, auditExists(t, e.ID))
	}

	other := insertUser(t, store)
	if err := store.Portfolios().Insert(t.Context(), newPortfolio(t, other.ID, "LONG TERM", now())); err != nil {
		t.Errorf("another owner using the name: %v", err)
	}
	if err := store.Portfolios().DeleteOwnedEmpty(t.Context(), owner.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Portfolios().Insert(t.Context(), newPortfolio(t, owner.ID, "long term", now())); err != nil {
		t.Errorf("reusing the name of a deleted portfolio: %v", err)
	}
}

// TestPortfolioOwnershipIsolation: every method of another user's portfolio reads as not found
// and changes nothing.
func TestPortfolioOwnershipIsolation(t *testing.T) {
	store := newRuntimeStore(t)
	alice := insertUser(t, store)
	bob := insertUser(t, store)
	p := insertPortfolio(t, store, alice.ID)

	if _, err := store.Portfolios().GetOwned(t.Context(), bob.ID, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetOwned error = %v, want not_found", err)
	}
	if err := store.withinTx(t.Context(), func(r *txRepositories) error {
		_, err := r.Portfolios().LockOwned(t.Context(), bob.ID, p.ID)
		return err
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("LockOwned error = %v, want not_found", err)
	}
	renamed := p
	renamed.Name, _ = domain.ParsePortfolioName("Taken over")
	renamed.UpdatedAt = now()
	if _, err := store.Portfolios().UpdateOwned(t.Context(), renamed, bob.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdateOwned error = %v, want not_found", err)
	}
	if err := store.Portfolios().DeleteOwnedEmpty(t.Context(), bob.ID, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("DeleteOwnedEmpty error = %v, want not_found", err)
	}
	list, err := store.Portfolios().ListOwned(t.Context(), bob.ID, page(t, 100, ""))
	if err != nil || len(list.Items) != 0 {
		t.Errorf("bob's list = %+v, %v; want empty", list, err)
	}
	if got, err := store.Portfolios().GetOwned(t.Context(), alice.ID, p.ID); err != nil || got.Name != p.Name || got.Version.Int32() != 1 {
		t.Errorf("alice's portfolio changed: %+v, %v", got, err)
	}
}

func TestPortfolioLockOwnedRequiresTransaction(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)

	if _, err := store.Portfolios().LockOwned(t.Context(), user.ID, p.ID); !errors.Is(err, domain.ErrInternal) {
		t.Errorf("LockOwned outside a transaction error = %v, want internal", err)
	}
	if err := store.withinTx(t.Context(), func(r *txRepositories) error {
		got, err := r.Portfolios().LockOwned(t.Context(), user.ID, p.ID)
		if err == nil && got.ID != p.ID {
			t.Errorf("LockOwned = %+v", got)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPortfolioUpdateOwnedChecksVersion(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	edit := p
	edit.Name, _ = domain.ParsePortfolioName("Short term")
	edit.Description, _ = domain.ParsePortfolioDescription("Swing trades")
	edit.UpdatedAt = now().Add(time.Minute)

	got, err := store.Portfolios().UpdateOwned(t.Context(), edit, user.ID)
	if err != nil || got.Version.Int32() != 2 || got.Name.String() != "Short term" || got.Description.String() != "Swing trades" ||
		!got.UpdatedAt.Equal(edit.UpdatedAt) || !got.CreatedAt.Equal(p.CreatedAt) {
		t.Fatalf("UpdateOwned = %+v, %v; want the edit at version 2", got, err)
	}

	stale := edit
	stale.Name, _ = domain.ParsePortfolioName("Lost update")
	if _, err := store.Portfolios().UpdateOwned(t.Context(), stale, user.ID); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("UpdateOwned(version 1 again) error = %v, want version_conflict", err)
	}
	if stored, _ := store.Portfolios().GetOwned(t.Context(), user.ID, p.ID); stored.Name.String() != "Short term" || stored.Version.Int32() != 2 {
		t.Errorf("stale update changed the row: %+v", stored)
	}
}

// TestPortfolioRenameToTakenNameConflicts: the rename conflict aborts the transaction, which is
// fine because the service returns at once (port.UnitOfWork).
func TestPortfolioRenameToTakenNameConflicts(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	taken := insertPortfolio(t, store, user.ID)
	p := insertPortfolio(t, store, user.ID)
	edit := p
	edit.Name, _ = domain.ParsePortfolioName(taken.Name.String())
	edit.UpdatedAt = now()

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		_, err := r.Portfolios().UpdateOwned(t.Context(), edit, user.ID)
		return err
	})

	if !errors.Is(err, domain.ErrPortfolioNameConflict) {
		t.Fatalf("rename to a taken name error = %v, want portfolio_name_conflict", err)
	}
	if got, _ := store.Portfolios().GetOwned(t.Context(), user.ID, p.ID); got.Name != p.Name {
		t.Errorf("name changed to %q", got.Name)
	}
}

// TestPortfolioListOwnedPagesWithoutRepeats: equal created_at values are ordered by id, so
// walking every page returns each portfolio exactly once, newest first.
func TestPortfolioListOwnedPagesWithoutRepeats(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	base := now()
	want := map[domain.ID]bool{}
	for i := range 7 {
		at := base.Add(time.Duration(i/3) * time.Second) // three share each second
		p := newPortfolio(t, user.ID, "Page "+newID(t).String(), at)
		if err := store.Portfolios().Insert(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		want[p.ID] = true
	}

	var got []domain.Portfolio
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("pagination does not end")
		}
		res, err := store.Portfolios().ListOwned(t.Context(), user.ID, page(t, 2, cursor))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, res.Items...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}

	if len(got) != len(want) {
		t.Fatalf("listed %d portfolios, want %d", len(got), len(want))
	}
	for i, p := range got {
		if !want[p.ID] {
			t.Fatalf("portfolio %s listed twice or unknown", p.ID)
		}
		delete(want, p.ID)
		if i > 0 {
			prev := got[i-1]
			if p.CreatedAt.After(prev.CreatedAt) || (p.CreatedAt.Equal(prev.CreatedAt) && p.ID.String() > prev.ID.String()) {
				t.Errorf("item %d is out of order (created_at DESC, id DESC)", i)
			}
		}
	}
}

func TestPortfolioListOwnedRejectsForeignCursors(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	transactionsCursor := encodeCursor(cursorPosition{Kind: cursorKindTransactions, At: now(), Sequence: 1})
	for name, cursor := range map[string]string{
		"not base64":          "%%%",
		"not json":            "bm90IGpzb24",
		"transactions cursor": transactionsCursor,
		"bad id":              encodeCursor(cursorPosition{Kind: cursorKindPortfolios, At: now(), ID: "nope"}),
	} {
		_, err := store.Portfolios().ListOwned(t.Context(), user.ID, page(t, 10, cursor))
		var de *domain.Error
		if !errors.As(err, &de) || de.Code != domain.CodeInvalidInput || de.Message[:6] != "cursor" {
			t.Errorf("%s: error = %v, want invalid_input about the cursor", name, err)
		}
	}
}

func page(t *testing.T, limit int, cursor string) domain.Page {
	t.Helper()
	p, err := domain.ParsePage(strconv.Itoa(limit), cursor)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func newPortfolio(t *testing.T, userID domain.ID, name string, createdAt time.Time) domain.Portfolio {
	t.Helper()
	n, err := domain.ParsePortfolioName(name)
	if err != nil {
		t.Fatal(err)
	}
	return domain.Portfolio{ID: newID(t), UserID: userID, Name: n, Currency: domain.CurrencyUSD, CreatedAt: createdAt, UpdatedAt: createdAt}
}

// insertPortfolio stores a portfolio with a unique name for userID.
func insertPortfolio(t *testing.T, store *Store, userID domain.ID) domain.Portfolio {
	t.Helper()
	p := newPortfolio(t, userID, "Portfolio "+newID(t).String(), now())
	if err := store.Portfolios().Insert(t.Context(), p); err != nil {
		t.Fatalf("insert portfolio: %v", err)
	}
	p.Version = mustVersion(t, 1)
	return p
}

func mustVersion(t *testing.T, n int64) domain.Version {
	t.Helper()
	v, err := domain.ParseVersion(n)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
```

- [ ] **Step 2: รันให้เห็นว่าล้มเหลว**

Run: `make test-integration`
Expected: FAIL ด้วย `[build failed]` และ `store.Portfolios undefined (type *Store has no field or method Portfolios)`

- [ ] **Step 3: เขียน queries แล้ว generate**

`internal/adapter/postgres/queries/portfolios.sql`:

```sql
-- Every query filters by user_id, so another user's portfolio reads as not found.

-- ON CONFLICT DO NOTHING keeps the transaction usable: zero rows inserted means the owner already
-- uses the name in some letter case (portfolios_user_name_unique).
-- name: InsertPortfolio :execrows
INSERT INTO app.portfolios (id, user_id, name, description, currency, version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, 1, $6, $6)
ON CONFLICT (user_id, lower(name)) DO NOTHING;

-- name: GetOwnedPortfolio :one
SELECT id, user_id, name, description, currency, version, created_at, updated_at
FROM app.portfolios WHERE id = $1 AND user_id = $2;

-- name: LockOwnedPortfolio :one
SELECT id, user_id, name, description, currency, version, created_at, updated_at
FROM app.portfolios WHERE id = $1 AND user_id = $2
FOR UPDATE;

-- Newest first; (created_at, id) is unique, so pages never repeat or skip a row. The first page
-- passes NULL for both after_* values.
-- name: ListOwnedPortfolios :many
SELECT id, user_id, name, description, currency, version, created_at, updated_at
FROM app.portfolios
WHERE user_id = @user_id
  AND (sqlc.narg('after_created_at')::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg('after_created_at')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY created_at DESC, id DESC
LIMIT @row_limit;

-- A rename to a name the owner already uses violates portfolios_user_name_unique, which aborts
-- the transaction; the service returns the conflict without writing anything else.
-- name: UpdateOwnedPortfolio :one
UPDATE app.portfolios
SET name = @name, description = @description, version = version + 1, updated_at = @updated_at
WHERE id = @id AND user_id = @user_id AND version = @version
RETURNING id, user_id, name, description, currency, version, created_at, updated_at;

-- name: OwnedPortfolioExists :one
SELECT EXISTS (SELECT 1 FROM app.portfolios WHERE id = $1 AND user_id = $2);

-- Deletes only an empty portfolio. Zero rows means it is missing, not the owner's, or not empty.
-- name: DeleteOwnedEmptyPortfolio :execrows
DELETE FROM app.portfolios p
WHERE p.id = $1 AND p.user_id = $2
  AND NOT EXISTS (SELECT 1 FROM app.transactions t WHERE t.portfolio_id = p.id);
```

Run: `make sqlc && ls internal/adapter/postgres/sqlc`
Expected: ไม่มี error และมี `portfolios.sql.go` เพิ่ม

- [ ] **Step 4: เขียนโค้ด**

`internal/adapter/postgres/cursor.go`:

```go
package postgres

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"time"

	"go-backend-stock/internal/core/domain"
)

// Keyset cursors (milestone 3 spec section 6.1). A cursor is the base64url JSON of the last row's
// sort key plus the list kind and a fingerprint of its filters, so a cursor only continues the
// list it came from. Cursors come from clients: anything that does not decode exactly is
// invalid_input.

const (
	cursorKindPortfolios   = "portfolios"
	cursorKindTransactions = "transactions"
)

var errInvalidCursor = domain.NewError(domain.CodeInvalidInput, "cursor is invalid or does not match the current filters")

type cursorPosition struct {
	Kind     string    `json:"k"`
	Filter   string    `json:"f"`
	At       time.Time `json:"t"`
	ID       string    `json:"i,omitempty"` // portfolios: the last id
	Sequence int64     `json:"s,omitempty"` // transactions: the last sequence
}

func encodeCursor(p cursorPosition) string {
	b, err := json.Marshal(p)
	if err != nil {
		panic("encode cursor: " + err.Error()) // only strings, a time and an int: cannot fail
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor returns the position in raw, or errInvalidCursor when raw is malformed or belongs
// to another list kind or filter.
func decodeCursor(raw, kind, filter string) (cursorPosition, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursorPosition{}, errInvalidCursor
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var p cursorPosition
	if err := dec.Decode(&p); err != nil || dec.More() {
		return cursorPosition{}, errInvalidCursor
	}
	if p.Kind != kind || p.Filter != filter || p.At.IsZero() {
		return cursorPosition{}, errInvalidCursor
	}
	return p, nil
}
```

`internal/adapter/postgres/portfolios.go`:

```go
package postgres

import (
	"context"
	"errors"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// PortfolioRepository stores portfolios. Every query filters by the owner.
type PortfolioRepository struct {
	q    *sqlc.Queries
	inTx bool
}

var _ port.PortfolioRepository = (*PortfolioRepository)(nil)

// Insert stores a new portfolio at version 1. A name the owner already uses in any letter case
// returns domain.ErrPortfolioNameConflict and leaves the transaction usable.
func (r *PortfolioRepository) Insert(ctx context.Context, p domain.Portfolio) error {
	n, err := r.q.InsertPortfolio(ctx, sqlc.InsertPortfolioParams{
		ID:          pgUUID(p.ID),
		UserID:      pgUUID(p.UserID),
		Name:        p.Name.String(),
		Description: p.Description.String(),
		Currency:    string(p.Currency),
		CreatedAt:   pgTime(p.CreatedAt),
	})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrPortfolioNameConflict
	}
	return nil
}

// GetOwned returns the owner's portfolio, or domain.ErrNotFound.
func (r *PortfolioRepository) GetOwned(ctx context.Context, ownerID, portfolioID domain.ID) (domain.Portfolio, error) {
	row, err := r.q.GetOwnedPortfolio(ctx, sqlc.GetOwnedPortfolioParams{ID: pgUUID(portfolioID), UserID: pgUUID(ownerID)})
	if err != nil {
		return domain.Portfolio{}, mapError(err)
	}
	return portfolioFromRow(row)
}

// LockOwned reads the owner's portfolio with FOR UPDATE; transaction-bound instances only.
func (r *PortfolioRepository) LockOwned(ctx context.Context, ownerID, portfolioID domain.ID) (domain.Portfolio, error) {
	if !r.inTx {
		return domain.Portfolio{}, errLockOutsideTx
	}
	row, err := r.q.LockOwnedPortfolio(ctx, sqlc.LockOwnedPortfolioParams{ID: pgUUID(portfolioID), UserID: pgUUID(ownerID)})
	if err != nil {
		return domain.Portfolio{}, mapError(err)
	}
	return portfolioFromRow(row)
}

// ListOwned returns one page of the owner's portfolios, newest first.
func (r *PortfolioRepository) ListOwned(ctx context.Context, ownerID domain.ID, page domain.Page) (domain.PortfolioPage, error) {
	if page.IsZero() {
		return domain.PortfolioPage{}, domain.NewError(domain.CodeInternal, "portfolio list needs a page")
	}
	params := sqlc.ListOwnedPortfoliosParams{UserID: pgUUID(ownerID), RowLimit: int32(page.Limit() + 1)} //nolint:gosec // Limit is at most domain.MaxPageLimit.
	if page.Cursor() != "" {
		pos, err := decodeCursor(page.Cursor(), cursorKindPortfolios, "")
		if err != nil {
			return domain.PortfolioPage{}, err
		}
		afterID, err := domain.ParseID("cursor", pos.ID)
		if err != nil {
			return domain.PortfolioPage{}, errInvalidCursor
		}
		params.AfterCreatedAt, params.AfterID = pgTime(pos.At), pgUUID(afterID)
	}
	rows, err := r.q.ListOwnedPortfolios(ctx, params)
	if err != nil {
		return domain.PortfolioPage{}, mapError(err)
	}
	var out domain.PortfolioPage
	for i, row := range rows {
		if i == page.Limit() {
			last := out.Items[len(out.Items)-1]
			out.NextCursor = encodeCursor(cursorPosition{Kind: cursorKindPortfolios, At: last.CreatedAt, ID: last.ID.String()})
			break
		}
		p, err := portfolioFromRow(row)
		if err != nil {
			return domain.PortfolioPage{}, err
		}
		out.Items = append(out.Items, p)
	}
	return out, nil
}

// UpdateOwned writes name and description when the version matches and returns the row at
// version + 1. A stale version returns domain.ErrVersionConflict, a missing or foreign portfolio
// domain.ErrNotFound. A name the owner already uses returns domain.ErrPortfolioNameConflict and
// aborts the transaction (see port.UnitOfWork).
func (r *PortfolioRepository) UpdateOwned(ctx context.Context, p domain.Portfolio, ownerID domain.ID) (domain.Portfolio, error) {
	row, err := r.q.UpdateOwnedPortfolio(ctx, sqlc.UpdateOwnedPortfolioParams{
		Name:        p.Name.String(),
		Description: p.Description.String(),
		UpdatedAt:   pgTime(p.UpdatedAt),
		ID:          pgUUID(p.ID),
		UserID:      pgUUID(ownerID),
		Version:     p.Version.Int32(),
	})
	if err = mapError(err); errors.Is(err, domain.ErrNotFound) {
		return domain.Portfolio{}, r.missingOrStale(ctx, ownerID, p.ID)
	} else if err != nil {
		return domain.Portfolio{}, err
	}
	return portfolioFromRow(row)
}

// DeleteOwnedEmpty deletes the owner's portfolio when it has no transactions. Idempotency keys
// must be removed first (IdempotencyRepository.DeleteForPortfolio).
func (r *PortfolioRepository) DeleteOwnedEmpty(ctx context.Context, ownerID, portfolioID domain.ID) error {
	n, err := r.q.DeleteOwnedEmptyPortfolio(ctx, sqlc.DeleteOwnedEmptyPortfolioParams{ID: pgUUID(portfolioID), UserID: pgUUID(ownerID)})
	if err != nil {
		return mapError(err)
	}
	if n == 1 {
		return nil
	}
	exists, err := r.q.OwnedPortfolioExists(ctx, sqlc.OwnedPortfolioExistsParams{ID: pgUUID(portfolioID), UserID: pgUUID(ownerID)})
	if err != nil {
		return mapError(err)
	}
	if exists {
		return domain.ErrPortfolioNotEmpty
	}
	return domain.ErrNotFound
}

// missingOrStale tells a version conflict from a portfolio that is missing or not the owner's.
func (r *PortfolioRepository) missingOrStale(ctx context.Context, ownerID, portfolioID domain.ID) error {
	exists, err := r.q.OwnedPortfolioExists(ctx, sqlc.OwnedPortfolioExistsParams{ID: pgUUID(portfolioID), UserID: pgUUID(ownerID)})
	if err != nil {
		return mapError(err)
	}
	if exists {
		return domain.ErrVersionConflict
	}
	return domain.ErrNotFound
}

func portfolioFromRow(row sqlc.AppPortfolio) (domain.Portfolio, error) {
	id, err := idFromPG("portfolio.id", row.ID)
	if err != nil {
		return domain.Portfolio{}, err
	}
	userID, err := idFromPG("portfolio.user_id", row.UserID)
	if err != nil {
		return domain.Portfolio{}, err
	}
	name, err := domain.ParsePortfolioName(row.Name)
	if err != nil {
		return domain.Portfolio{}, fromStored(err)
	}
	description, err := domain.ParsePortfolioDescription(row.Description)
	if err != nil {
		return domain.Portfolio{}, fromStored(err)
	}
	currency, err := domain.ParseCurrency(row.Currency)
	if err != nil {
		return domain.Portfolio{}, fromStored(err)
	}
	version, err := domain.ParseVersion(int64(row.Version))
	if err != nil {
		return domain.Portfolio{}, fromStored(err)
	}
	return domain.Portfolio{
		ID: id, UserID: userID, Name: name, Description: description, Currency: currency, Version: version,
		CreatedAt: timeFromPG(row.CreatedAt), UpdatedAt: timeFromPG(row.UpdatedAt),
	}, nil
}
```

แก้ `internal/adapter/postgres/store.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
// RateLimits returns the rate-limit repository. Admit runs its own transaction.
func (s *Store) RateLimits() port.RateLimitRepository { return &RateLimitRepository{store: s} }
```

   ด้วย

```go
// RateLimits returns the rate-limit repository. Admit runs its own transaction.
func (s *Store) RateLimits() port.RateLimitRepository { return &RateLimitRepository{store: s} }

// Portfolios returns the portfolio repository over the pool.
func (s *Store) Portfolios() port.PortfolioRepository {
	return &PortfolioRepository{q: sqlc.New(s.pool)}
}
```

2. แทน

```go
func (r *txRepositories) OTPChallenges() port.OTPChallengeRepository {
	return &OTPChallengeRepository{q: r.q, inTx: true}
}
```

   ด้วย

```go
func (r *txRepositories) OTPChallenges() port.OTPChallengeRepository {
	return &OTPChallengeRepository{q: r.q, inTx: true}
}

// Portfolios returns the portfolio repository bound to this transaction.
func (r *txRepositories) Portfolios() port.PortfolioRepository {
	return &PortfolioRepository{q: r.q, inTx: true}
}
```

- [ ] **Step 5: รัน tests และ lint ให้ผ่าน**

Run: `gofmt -l internal && make test && make test-integration && make lint`
Expected: `gofmt` ไม่พิมพ์อะไร, `ok` ทุกแพ็กเกจ, `ok  	go-backend-stock/internal/adapter/postgres` โดยไม่มี `SKIP` (ตรวจด้วย `GOFLAGS=-v make test-integration 2>&1 | grep -c '^--- SKIP'` ได้ `0`) และ `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add -A internal
git commit -m "feat(db): add portfolio repository with keyset pagination"
```

- [ ] **Step 7: พิสูจน์ว่า test จับการเขียนผิดได้**

ใน `internal/adapter/postgres/queries/portfolios.sql` แทนชั่วคราว

```sql
OR (created_at, id) < (
```

ด้วย

```sql
OR (created_at, id) <= (
```

Run: `make sqlc && make test-integration` (ดูผลของ `TestPortfolioListOwnedPagesWithoutRepeats`)
Expected: FAIL ด้วย `listed 12 portfolios, want 7`

จากนั้นคืนไฟล์: `git checkout -- internal/adapter/postgres/queries/portfolios.sql && make sqlc && git status --short` ต้องไม่แสดงอะไร

- [ ] **Step 8: ตรวจทั้ง repo**

Run: `make check`
Expected: ผ่าน (`0 issues.`, swagger valid, tests `ok`, ไม่มี diff ของ `docs/api` และ `internal/adapter/postgres/sqlc` ถูก commit ครบ)

---

### Task 4: `TransactionRepository`, ตัวกรองรายการ และการแปลง FK ใน `DeleteOwnedEmpty`

**Files:**
- Create: `internal/adapter/postgres/queries/transactions.sql`, `internal/adapter/postgres/transactions.go`, `internal/adapter/postgres/transactions_test.go`
- Modify: `internal/adapter/postgres/errors_test.go`, `internal/core/domain/portfolio_entities_test.go`, `internal/adapter/postgres/store.go`, `internal/adapter/postgres/portfolios.go`, `internal/adapter/postgres/errors.go`, `internal/core/domain/transaction.go`
- Generated by `make sqlc`: `internal/adapter/postgres/sqlc/transactions.sql.go`

**Interfaces:**
- Consumes: `insertPortfolio`, `page`, `newPortfolio` (Task 3); `randomSymbol`, `newInstrument` (Task 2); `encodeCursor`, `decodeCursor`, `cursorPosition`, `errInvalidCursor`, `cursorKind*` (Task 3); `PortfolioRepository.DeleteOwnedEmpty` (Task 3)
- Produces: `Store.Transactions()`, `(*txRepositories).Transactions()`; `transactionFilterFingerprint`; `mapError` ไม่แปลง `transactions_portfolio_id_fkey` แล้ว; test helpers `newTrade(...)`, `insertTrade(...)`, `insertInstrument(t, store)`, `requireCode(t, err, code)`, `listAll(...)`, `waitForLockWait(t, prefix)`

- [ ] **Step 1: เขียน failing tests**

`internal/adapter/postgres/transactions_test.go`:

```go
package postgres

import (
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// TestTransactionInsertKeepsEveryDigit: numeric(28,10) round-trips the smallest and the largest
// values the API accepts, and the database assigns a sequence.
func TestTransactionInsertKeepsEveryDigit(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	instrument := insertInstrument(t, store)
	trade := newTrade(t, p.ID, instrument, domain.SideBuy, "123456789012345678.0123456789", "0.0000000001", now())

	got := insertTrade(t, store, user.ID, trade)

	if got.Sequence < 1 || got.Symbol != instrument.Symbol || got.Version.Int32() != 1 ||
		got.Quantity.String() != "123456789012345678.0123456789" || got.Price.String() != "0.0000000001" ||
		got.Fee.String() != "1.25" || got.Side != domain.SideBuy || !got.ExecutedAt.Equal(trade.ExecutedAt) {
		t.Fatalf("Insert = %+v", got)
	}
	again, err := store.Transactions().Get(t.Context(), user.ID, p.ID, got.ID)
	if err != nil || again.Quantity.String() != got.Quantity.String() || again.Price.String() != got.Price.String() || again.Sequence != got.Sequence {
		t.Fatalf("Get = %+v, %v; want %+v", again, err, got)
	}
}

// TestTransactionOwnershipIsolation: another user gets not_found from every method on Alice's
// portfolio and trade, and nothing changes.
func TestTransactionOwnershipIsolation(t *testing.T) {
	store := newRuntimeStore(t)
	alice := insertUser(t, store)
	bob := insertUser(t, store)
	p := insertPortfolio(t, store, alice.ID)
	instrument := insertInstrument(t, store)
	trade := insertTrade(t, store, alice.ID, newTrade(t, p.ID, instrument, domain.SideBuy, "2", "10", now()))

	if _, err := store.Transactions().History(t.Context(), bob.ID, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("History error = %v, want not_found", err)
	}
	filter, _ := domain.NewTransactionFilter(page(t, 10, ""), nil, nil, nil, nil)
	if _, err := store.Transactions().List(t.Context(), bob.ID, p.ID, filter); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("List error = %v, want not_found", err)
	}
	if _, err := store.Transactions().Get(t.Context(), bob.ID, p.ID, trade.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get error = %v, want not_found", err)
	}
	if _, err := store.Transactions().Insert(t.Context(), bob.ID, newTrade(t, p.ID, instrument, domain.SideSell, "2", "10", now())); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Insert error = %v, want not_found", err)
	}
	edit := trade
	edit.Quantity, _ = domain.ParseQuantity("999")
	edit.UpdatedAt = now()
	if _, err := store.Transactions().Update(t.Context(), bob.ID, edit); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Update error = %v, want not_found", err)
	}
	if err := store.Transactions().Delete(t.Context(), bob.ID, p.ID, trade.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Delete error = %v, want not_found", err)
	}
	history, err := store.Transactions().History(t.Context(), alice.ID, p.ID)
	if err != nil || len(history) != 1 || history[0].Quantity.String() != "2" || history[0].Version.Int32() != 1 {
		t.Errorf("alice's history changed: %+v, %v", history, err)
	}
}

func TestTransactionHistoryOfEmptyOwnedPortfolio(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)

	history, err := store.Transactions().History(t.Context(), user.ID, p.ID)
	if err != nil || len(history) != 0 {
		t.Fatalf("History(empty portfolio) = %+v, %v; want empty, no error", history, err)
	}
}

// TestTransactionSameSymbolInTwoPortfoliosStaysSeparate: trades of one instrument in two
// portfolios of the same user never mix.
func TestTransactionSameSymbolInTwoPortfoliosStaysSeparate(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	long := insertPortfolio(t, store, user.ID)
	short := insertPortfolio(t, store, user.ID)
	instrument := insertInstrument(t, store)
	insertTrade(t, store, user.ID, newTrade(t, long.ID, instrument, domain.SideBuy, "10", "100", now()))
	insertTrade(t, store, user.ID, newTrade(t, short.ID, instrument, domain.SideBuy, "1", "100", now()))

	for _, tc := range []struct {
		p    domain.Portfolio
		want string
	}{{long, "10"}, {short, "1"}} {
		history, err := store.Transactions().History(t.Context(), user.ID, tc.p.ID)
		if err != nil || len(history) != 1 || history[0].Quantity.String() != tc.want || history[0].PortfolioID != tc.p.ID {
			t.Errorf("history of %s = %+v, %v; want one trade of %s", tc.p.ID, history, err, tc.want)
		}
	}
}

// TestTransactionHistoryOrdersByExecutedAtThenSequence: a backdated trade replays before later
// ones, and trades at the same instant replay in insertion (sequence) order.
func TestTransactionHistoryOrdersByExecutedAtThenSequence(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	instrument := insertInstrument(t, store)
	at := now().Add(-time.Hour)
	first := insertTrade(t, store, user.ID, newTrade(t, p.ID, instrument, domain.SideBuy, "1", "10", at))
	second := insertTrade(t, store, user.ID, newTrade(t, p.ID, instrument, domain.SideBuy, "2", "10", at))
	backdated := insertTrade(t, store, user.ID, newTrade(t, p.ID, instrument, domain.SideBuy, "3", "10", at.Add(-time.Minute)))

	history, err := store.Transactions().History(t.Context(), user.ID, p.ID)
	if err != nil || len(history) != 3 {
		t.Fatalf("History = %+v, %v", history, err)
	}
	for i, want := range []domain.ID{backdated.ID, first.ID, second.ID} {
		if history[i].ID != want {
			t.Errorf("history[%d] = %s, want %s (executed_at, then sequence)", i, history[i].ID, want)
		}
	}
}

func TestTransactionUpdateChecksVersionAndNeverMovesPortfolio(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	other := insertPortfolio(t, store, user.ID)
	trade := insertTrade(t, store, user.ID, newTrade(t, p.ID, insertInstrument(t, store), domain.SideBuy, "1", "10", now()))
	replacement := insertInstrument(t, store)

	edit := trade
	edit.InstrumentID = replacement.ID
	edit.Side = domain.SideSell
	edit.Quantity, _ = domain.ParseQuantity("0.5")
	edit.ExecutedAt = trade.ExecutedAt.Add(-time.Hour)
	edit.UpdatedAt = now().Add(time.Minute)
	got, err := store.Transactions().Update(t.Context(), user.ID, edit)
	if err != nil || got.Version.Int32() != 2 || got.Symbol != replacement.Symbol || got.Side != domain.SideSell ||
		got.Quantity.String() != "0.5" || !got.ExecutedAt.Equal(edit.ExecutedAt) || got.Sequence != trade.Sequence {
		t.Fatalf("Update = %+v, %v; want the edit at version 2 with the same sequence", got, err)
	}

	if _, err := store.Transactions().Update(t.Context(), user.ID, edit); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("Update(stale version) error = %v, want version_conflict", err)
	}
	moved := got
	moved.PortfolioID = other.ID
	if _, err := store.Transactions().Update(t.Context(), user.ID, moved); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update into another portfolio error = %v, want not_found", err)
	}
	stored, err := store.Transactions().Get(t.Context(), user.ID, p.ID, trade.ID)
	if err != nil || stored.Version.Int32() != 2 || stored.PortfolioID != p.ID {
		t.Errorf("trade after rejected updates = %+v, %v", stored, err)
	}
}

func TestTransactionDelete(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	trade := insertTrade(t, store, user.ID, newTrade(t, p.ID, insertInstrument(t, store), domain.SideBuy, "1", "10", now()))

	if err := store.Transactions().Delete(t.Context(), user.ID, p.ID, trade.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transactions().Get(t.Context(), user.ID, p.ID, trade.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get after Delete error = %v, want not_found", err)
	}
	if err := store.Transactions().Delete(t.Context(), user.ID, p.ID, trade.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second Delete error = %v, want not_found", err)
	}
}

// listAll walks every page of a filtered list and returns the trades in order.
func listAll(t *testing.T, store *Store, ownerID, portfolioID domain.ID, limit int, symbol *domain.Symbol, side *domain.Side, from, to *time.Time) []domain.Transaction {
	t.Helper()
	var out []domain.Transaction
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 20 {
			t.Fatal("pagination does not end")
		}
		f, err := domain.NewTransactionFilter(page(t, limit, cursor), symbol, side, from, to)
		if err != nil {
			t.Fatal(err)
		}
		res, err := store.Transactions().List(t.Context(), ownerID, portfolioID, f)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, res.Items...)
		if res.NextCursor == "" {
			return out
		}
		cursor = res.NextCursor
	}
}

// TestTransactionListPagesWithoutRepeats: pages are newest first by (executed_at, sequence) and
// walking them returns every trade exactly once even when executed_at values are equal.
func TestTransactionListPagesWithoutRepeats(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	instrument := insertInstrument(t, store)
	base := now().Add(-24 * time.Hour)
	want := map[domain.ID]bool{}
	for i := range 9 {
		at := base.Add(time.Duration(i/3) * time.Hour) // three share each executed_at
		want[insertTrade(t, store, user.ID, newTrade(t, p.ID, instrument, domain.SideBuy, "1", "10", at)).ID] = true
	}

	got := listAll(t, store, user.ID, p.ID, 2, nil, nil, nil, nil)

	if len(got) != len(want) {
		t.Fatalf("listed %d trades, want %d", len(got), len(want))
	}
	for i, tr := range got {
		if !want[tr.ID] {
			t.Fatalf("trade %s listed twice or unknown", tr.ID)
		}
		delete(want, tr.ID)
		if i > 0 {
			prev := got[i-1]
			if tr.ExecutedAt.After(prev.ExecutedAt) || (tr.ExecutedAt.Equal(prev.ExecutedAt) && tr.Sequence >= prev.Sequence) {
				t.Fatalf("item %d out of order (executed_at DESC, sequence DESC)", i)
			}
		}
	}
}

// TestTransactionListFilters: symbol, side and the inclusive from/to bounds combine. Each trade
// outside the result is excluded by exactly one filter, so ignoring any filter changes the result.
func TestTransactionListFilters(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	aapl, msft := insertInstrument(t, store), insertInstrument(t, store)
	from := now().Add(-24 * time.Hour)
	to := from.Add(2 * time.Hour)
	trade := func(i domain.Instrument, side domain.Side, at time.Time) domain.Transaction {
		return insertTrade(t, store, user.ID, newTrade(t, p.ID, i, side, "1", "10", at))
	}
	atFrom := trade(aapl, domain.SideSell, from)
	inside := trade(aapl, domain.SideSell, from.Add(time.Hour))
	atTo := trade(aapl, domain.SideSell, to)
	trade(msft, domain.SideSell, from.Add(time.Hour))         // other symbol
	trade(aapl, domain.SideBuy, from.Add(time.Hour))          // other side
	trade(aapl, domain.SideSell, from.Add(-time.Microsecond)) // before from
	trade(aapl, domain.SideSell, to.Add(time.Microsecond))    // after to

	sell := domain.SideSell
	got := listAll(t, store, user.ID, p.ID, 1, &aapl.Symbol, &sell, &from, &to)

	want := []domain.ID{atTo.ID, inside.ID, atFrom.ID}
	if len(got) != len(want) {
		t.Fatalf("filtered list has %d trades, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Errorf("filtered[%d] = %s, want %s", i, got[i].ID, want[i])
		}
	}
}

// TestTransactionListRejectsCursorFromOtherFilters: a cursor only continues the list it came from.
func TestTransactionListRejectsCursorFromOtherFilters(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	instrument := insertInstrument(t, store)
	for range 3 {
		insertTrade(t, store, user.ID, newTrade(t, p.ID, instrument, domain.SideBuy, "1", "10", now()))
	}
	unfiltered, _ := domain.NewTransactionFilter(page(t, 1, ""), nil, nil, nil, nil)
	first, err := store.Transactions().List(t.Context(), user.ID, p.ID, unfiltered)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page = %+v, %v", first, err)
	}

	buy := domain.SideBuy
	other, _ := domain.NewTransactionFilter(page(t, 1, first.NextCursor), nil, &buy, nil, nil)
	_, err = store.Transactions().List(t.Context(), user.ID, p.ID, other)
	requireCode(t, err, domain.CodeInvalidInput)

	portfoliosCursor := encodeCursor(cursorPosition{Kind: cursorKindPortfolios, At: now(), ID: newID(t).String()})
	wrongKind, _ := domain.NewTransactionFilter(page(t, 1, portfoliosCursor), nil, nil, nil, nil)
	_, err = store.Transactions().List(t.Context(), user.ID, p.ID, wrongKind)
	requireCode(t, err, domain.CodeInvalidInput)
}

func TestPortfolioDeleteOwnedEmptyRefusesWhileTradesRemain(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	trade := insertTrade(t, store, user.ID, newTrade(t, p.ID, insertInstrument(t, store), domain.SideBuy, "1", "10", now()))

	e := auditEvent(t, `{}`)
	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		if err := r.Portfolios().DeleteOwnedEmpty(t.Context(), user.ID, p.ID); !errors.Is(err, domain.ErrPortfolioNotEmpty) {
			t.Errorf("DeleteOwnedEmpty with a trade error = %v, want portfolio_not_empty", err)
		}
		return r.Audit().Append(t.Context(), e) // the refusal leaves the transaction usable
	})
	if err != nil || !auditExists(t, e.ID) {
		t.Fatalf("transaction after portfolio_not_empty: %v", err)
	}
	if _, err := store.Portfolios().GetOwned(t.Context(), user.ID, p.ID); err != nil {
		t.Fatalf("portfolio was deleted with its trade: %v", err)
	}
	if err := store.Transactions().Delete(t.Context(), user.ID, p.ID, trade.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Portfolios().DeleteOwnedEmpty(t.Context(), user.ID, p.ID); err != nil {
		t.Fatalf("DeleteOwnedEmpty after removing the trade: %v", err)
	}
	if _, err := store.Portfolios().GetOwned(t.Context(), user.ID, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deleted portfolio still readable: %v", err)
	}
}

// TestPortfolioDeleteRacingAnUnlockedInsertIsNotEmpty: a trade inserted by a caller that skipped
// LockOwned makes the DELETE wait on the trade's foreign key lock; once that trade commits, the
// delete fails with the foreign key violation, which DeleteOwnedEmpty reports as not empty.
func TestPortfolioDeleteRacingAnUnlockedInsertIsNotEmpty(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	instrument := insertInstrument(t, store)
	inserted := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- store.withinTx(t.Context(), func(r *txRepositories) error {
			if _, err := r.Transactions().Insert(t.Context(), user.ID, newTrade(t, p.ID, instrument, domain.SideBuy, "1", "10", now())); err != nil {
				return err
			}
			close(inserted)
			<-release
			return nil
		})
	}()
	<-inserted
	deleted := make(chan error, 1)
	go func() { deleted <- store.Portfolios().DeleteOwnedEmpty(t.Context(), user.ID, p.ID) }()
	waitForLockWait(t, "DELETE FROM app.portfolios")
	close(release)

	if err := <-done; err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	if err := <-deleted; !errors.Is(err, domain.ErrPortfolioNotEmpty) {
		t.Fatalf("DeleteOwnedEmpty racing an insert error = %v, want portfolio_not_empty", err)
	}
}

// waitForLockWait polls until a session running a statement that starts with prefix is waiting
// for a lock, so a test can release the blocker at the right moment.
func waitForLockWait(t *testing.T, prefix string) {
	t.Helper()
	admin := openPool(t, "TEST_ADMIN_DATABASE_URL")
	for range 200 {
		var n int
		if err := admin.QueryRow(t.Context(),
			`SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE '%' || $1 || '%'`, prefix).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("statement never waited for a lock")
}

func newTrade(t *testing.T, portfolioID domain.ID, instrument domain.Instrument, side domain.Side, quantity, price string, executedAt time.Time) domain.Transaction {
	t.Helper()
	q, err := domain.ParseQuantity(quantity)
	if err != nil {
		t.Fatal(err)
	}
	p, err := domain.ParsePrice(price)
	if err != nil {
		t.Fatal(err)
	}
	fee, err := domain.ParseFee("1.25")
	if err != nil {
		t.Fatal(err)
	}
	at := now()
	return domain.Transaction{
		ID: newID(t), PortfolioID: portfolioID, InstrumentID: instrument.ID, Side: side, Quantity: q, Price: p, Fee: fee,
		ExecutedAt: executedAt, CreatedAt: at, UpdatedAt: at,
	}
}

func insertTrade(t *testing.T, store *Store, ownerID domain.ID, trade domain.Transaction) domain.Transaction {
	t.Helper()
	got, err := store.Transactions().Insert(t.Context(), ownerID, trade)
	if err != nil {
		t.Fatalf("insert trade: %v", err)
	}
	return got
}

func requireCode(t *testing.T, err error, code domain.ErrorCode) {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

// insertInstrument stores a fresh instrument with a random symbol and provider asset id.
func insertInstrument(t *testing.T, store *Store) domain.Instrument {
	t.Helper()
	got, err := store.Instruments().Upsert(t.Context(), newInstrument(t, randomSymbol(t), "asset-"+newID(t).String()))
	if err != nil {
		t.Fatalf("insert instrument: %v", err)
	}
	return got
}
```

แก้ `internal/adapter/postgres/errors_test.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
		{"portfolio still has trades", &pgconn.PgError{Code: "23503", ConstraintName: "transactions_portfolio_id_fkey"}, domain.ErrPortfolioNotEmpty},
```

   ด้วย

```go
		// Only DeleteOwnedEmpty knows a transactions FK violation means "not empty".
		{"transactions foreign key", &pgconn.PgError{Code: "23503", ConstraintName: "transactions_portfolio_id_fkey"}, domain.ErrInternal},
```

แก้ `internal/core/domain/portfolio_entities_test.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
	_, err = domain.NewTransactionFilter(domain.Page{}, nil, nil, nil, nil)
	requireCode(t, err, domain.CodeInternal)
}
```

   ด้วย

```go
	_, err = domain.NewTransactionFilter(domain.Page{}, nil, nil, nil, nil)
	requireCode(t, err, domain.CodeInternal)
}

// TestNewTransactionFilterRejectsEmptyFilterValues: an absent filter is nil. A pointer to the zero
// Symbol or an empty Side is a bug in the caller, not a filter that matches everything or nothing.
func TestNewTransactionFilterRejectsEmptyFilterValues(t *testing.T) {
	page, err := domain.ParsePage("", "")
	if err != nil {
		t.Fatal(err)
	}
	var noSymbol domain.Symbol
	_, err = domain.NewTransactionFilter(page, &noSymbol, nil, nil, nil)
	requireCode(t, err, domain.CodeInternal)
	noSide := domain.Side("")
	_, err = domain.NewTransactionFilter(page, nil, &noSide, nil, nil)
	requireCode(t, err, domain.CodeInternal)
	short := domain.Side("short")
	_, err = domain.NewTransactionFilter(page, nil, &short, nil, nil)
	requireCode(t, err, domain.CodeInternal)
}
```

- [ ] **Step 2: รันให้เห็นว่าล้มเหลว**

Run: `make test && make test-integration`
Expected: `make test` FAIL ด้วย `TestNewTransactionFilterRejectsEmptyFilterValues` (`error = <nil> (<nil>), want *domain.Error with code internal`) และ `make test-integration` FAIL ด้วย `[build failed]` และ `store.Transactions undefined`

- [ ] **Step 3: เขียน queries แล้ว generate**

`internal/adapter/postgres/queries/transactions.sql`:

```sql
-- Every query joins portfolios on user_id, so another user's transaction reads as not found, and
-- returns the instrument's current symbol. All queries list the same columns in the same order
-- so their rows convert to one Go type (transactionRow).

-- Replay order: executed_at, then the database-assigned sequence.
-- name: OwnedTransactionHistory :many
SELECT t.id, t.portfolio_id, t.sequence, t.instrument_id, i.symbol, t.side, t.quantity, t.price, t.fee,
       t.executed_at, t.version, t.created_at, t.updated_at
FROM app.transactions t
JOIN app.portfolios p ON p.id = t.portfolio_id AND p.user_id = @owner_id
JOIN app.instruments i ON i.id = t.instrument_id
WHERE t.portfolio_id = @portfolio_id
ORDER BY t.executed_at, t.sequence;

-- Newest first by (executed_at, sequence). Filters and the cursor are NULL when unused; from and
-- to are inclusive.
-- name: ListOwnedTransactions :many
SELECT t.id, t.portfolio_id, t.sequence, t.instrument_id, i.symbol, t.side, t.quantity, t.price, t.fee,
       t.executed_at, t.version, t.created_at, t.updated_at
FROM app.transactions t
JOIN app.portfolios p ON p.id = t.portfolio_id AND p.user_id = @owner_id
JOIN app.instruments i ON i.id = t.instrument_id
WHERE t.portfolio_id = @portfolio_id
  AND (sqlc.narg('symbol')::text IS NULL OR i.symbol = sqlc.narg('symbol')::text)
  AND (sqlc.narg('side')::text IS NULL OR t.side = sqlc.narg('side')::text)
  AND (sqlc.narg('from_time')::timestamptz IS NULL OR t.executed_at >= sqlc.narg('from_time')::timestamptz)
  AND (sqlc.narg('to_time')::timestamptz IS NULL OR t.executed_at <= sqlc.narg('to_time')::timestamptz)
  AND (sqlc.narg('after_executed_at')::timestamptz IS NULL
       OR (t.executed_at, t.sequence) < (sqlc.narg('after_executed_at')::timestamptz, sqlc.narg('after_sequence')::bigint))
ORDER BY t.executed_at DESC, t.sequence DESC
LIMIT @row_limit;

-- name: GetOwnedTransaction :one
SELECT t.id, t.portfolio_id, t.sequence, t.instrument_id, i.symbol, t.side, t.quantity, t.price, t.fee,
       t.executed_at, t.version, t.created_at, t.updated_at
FROM app.transactions t
JOIN app.portfolios p ON p.id = t.portfolio_id AND p.user_id = @owner_id
JOIN app.instruments i ON i.id = t.instrument_id
WHERE t.id = @id AND t.portfolio_id = @portfolio_id;

-- Inserts only into the owner's portfolio; no row means the portfolio is missing or not theirs.
-- name: InsertOwnedTransaction :one
WITH ins AS (
    INSERT INTO app.transactions (id, portfolio_id, instrument_id, side, quantity, price, fee, executed_at, version, created_at, updated_at)
    SELECT @id::uuid, p.id, @instrument_id::uuid, @side::text, @quantity::numeric, @price::numeric, @fee::numeric,
           @executed_at::timestamptz, 1, @created_at::timestamptz, @created_at::timestamptz
    FROM app.portfolios p
    WHERE p.id = @portfolio_id AND p.user_id = @owner_id
    RETURNING id, portfolio_id, sequence, instrument_id, side, quantity, price, fee, executed_at, version, created_at, updated_at
)
SELECT ins.id, ins.portfolio_id, ins.sequence, ins.instrument_id, i.symbol, ins.side, ins.quantity, ins.price, ins.fee,
       ins.executed_at, ins.version, ins.created_at, ins.updated_at
FROM ins JOIN app.instruments i ON i.id = ins.instrument_id;

-- portfolio_id is in the WHERE clause and never in SET: a trade cannot move to another portfolio.
-- name: UpdateOwnedTransaction :one
WITH upd AS (
    UPDATE app.transactions t
    SET instrument_id = @instrument_id, side = @side, quantity = @quantity, price = @price, fee = @fee,
        executed_at = @executed_at, version = t.version + 1, updated_at = @updated_at
    FROM app.portfolios p
    WHERE t.id = @id AND t.portfolio_id = @portfolio_id AND t.version = @version
      AND p.id = t.portfolio_id AND p.user_id = @owner_id
    RETURNING t.id, t.portfolio_id, t.sequence, t.instrument_id, t.side, t.quantity, t.price, t.fee,
              t.executed_at, t.version, t.created_at, t.updated_at
)
SELECT upd.id, upd.portfolio_id, upd.sequence, upd.instrument_id, i.symbol, upd.side, upd.quantity, upd.price, upd.fee,
       upd.executed_at, upd.version, upd.created_at, upd.updated_at
FROM upd JOIN app.instruments i ON i.id = upd.instrument_id;

-- name: OwnedTransactionExists :one
SELECT EXISTS (
    SELECT 1 FROM app.transactions t
    JOIN app.portfolios p ON p.id = t.portfolio_id AND p.user_id = @owner_id
    WHERE t.id = @id AND t.portfolio_id = @portfolio_id
);

-- name: DeleteOwnedTransaction :execrows
DELETE FROM app.transactions t
USING app.portfolios p
WHERE t.id = @id AND t.portfolio_id = @portfolio_id AND p.id = t.portfolio_id AND p.user_id = @owner_id;
```

Run: `make sqlc && ls internal/adapter/postgres/sqlc`
Expected: ไม่มี error และมี `transactions.sql.go` เพิ่ม

- [ ] **Step 4: เขียนโค้ด**

`internal/adapter/postgres/transactions.go`:

```go
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// TransactionRepository stores trades. Every query checks the owner through portfolios.user_id
// and returns the instrument's current symbol.
type TransactionRepository struct {
	q *sqlc.Queries
}

var _ port.TransactionRepository = (*TransactionRepository)(nil)

// transactionRow has the columns every transaction query returns, in the same order, so each
// sqlc row type converts to it.
type transactionRow struct {
	ID           pgtype.UUID
	PortfolioID  pgtype.UUID
	Sequence     pgtype.Int8
	InstrumentID pgtype.UUID
	Symbol       string
	Side         string
	Quantity     pgtype.Numeric
	Price        pgtype.Numeric
	Fee          pgtype.Numeric
	ExecutedAt   pgtype.Timestamptz
	Version      int32
	CreatedAt    pgtype.Timestamptz
	UpdatedAt    pgtype.Timestamptz
}

// History returns every trade of the owner's portfolio ordered by (executed_at, sequence) for
// replay. A missing or foreign portfolio returns domain.ErrNotFound.
func (r *TransactionRepository) History(ctx context.Context, ownerID, portfolioID domain.ID) ([]domain.Transaction, error) {
	rows, err := r.q.OwnedTransactionHistory(ctx, sqlc.OwnedTransactionHistoryParams{OwnerID: pgUUID(ownerID), PortfolioID: pgUUID(portfolioID)})
	if err != nil {
		return nil, mapError(err)
	}
	if len(rows) == 0 {
		if err := r.requireOwnedPortfolio(ctx, ownerID, portfolioID); err != nil {
			return nil, err
		}
	}
	out := make([]domain.Transaction, 0, len(rows))
	for _, row := range rows {
		t, err := transactionFromRow(transactionRow(row))
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// List returns one page of the owner's portfolio transactions, newest first, narrowed by the
// filter. A cursor from another list or filter is invalid_input; a missing or foreign portfolio
// domain.ErrNotFound.
func (r *TransactionRepository) List(ctx context.Context, ownerID, portfolioID domain.ID, f domain.TransactionFilter) (domain.TransactionPage, error) {
	if f.Page.IsZero() {
		return domain.TransactionPage{}, domain.NewError(domain.CodeInternal, "transaction list needs a page")
	}
	fingerprint := transactionFilterFingerprint(f)
	params := sqlc.ListOwnedTransactionsParams{
		OwnerID:     pgUUID(ownerID),
		PortfolioID: pgUUID(portfolioID),
		FromTime:    pgOptionalTime(f.From),
		ToTime:      pgOptionalTime(f.To),
		RowLimit:    int32(f.Page.Limit() + 1), //nolint:gosec // Limit is at most domain.MaxPageLimit.
	}
	if f.Symbol != nil {
		params.Symbol = pgtype.Text{String: f.Symbol.String(), Valid: true}
	}
	if f.Side != nil {
		params.Side = pgtype.Text{String: string(*f.Side), Valid: true}
	}
	if f.Page.Cursor() != "" {
		pos, err := decodeCursor(f.Page.Cursor(), cursorKindTransactions, fingerprint)
		if err != nil {
			return domain.TransactionPage{}, err
		}
		if pos.Sequence < 1 {
			return domain.TransactionPage{}, errInvalidCursor
		}
		params.AfterExecutedAt = pgTime(pos.At)
		params.AfterSequence = pgtype.Int8{Int64: pos.Sequence, Valid: true}
	}
	rows, err := r.q.ListOwnedTransactions(ctx, params)
	if err != nil {
		return domain.TransactionPage{}, mapError(err)
	}
	if len(rows) == 0 {
		if err := r.requireOwnedPortfolio(ctx, ownerID, portfolioID); err != nil {
			return domain.TransactionPage{}, err
		}
	}
	var out domain.TransactionPage
	for i, row := range rows {
		if i == f.Page.Limit() {
			last := out.Items[len(out.Items)-1]
			out.NextCursor = encodeCursor(cursorPosition{
				Kind: cursorKindTransactions, Filter: fingerprint, At: last.ExecutedAt, Sequence: last.Sequence,
			})
			break
		}
		t, err := transactionFromRow(transactionRow(row))
		if err != nil {
			return domain.TransactionPage{}, err
		}
		out.Items = append(out.Items, t)
	}
	return out, nil
}

// Get returns one trade of the owner's portfolio, or domain.ErrNotFound.
func (r *TransactionRepository) Get(ctx context.Context, ownerID, portfolioID, transactionID domain.ID) (domain.Transaction, error) {
	row, err := r.q.GetOwnedTransaction(ctx, sqlc.GetOwnedTransactionParams{
		OwnerID: pgUUID(ownerID), PortfolioID: pgUUID(portfolioID), ID: pgUUID(transactionID),
	})
	if err != nil {
		return domain.Transaction{}, mapError(err)
	}
	return transactionFromRow(transactionRow(row))
}

// Insert stores a trade in the owner's portfolio at version 1 and returns it with the sequence
// the database assigned. A missing or foreign portfolio returns domain.ErrNotFound.
func (r *TransactionRepository) Insert(ctx context.Context, ownerID domain.ID, t domain.Transaction) (domain.Transaction, error) {
	quantity, price, fee, err := tradeNumerics(t)
	if err != nil {
		return domain.Transaction{}, err
	}
	row, err := r.q.InsertOwnedTransaction(ctx, sqlc.InsertOwnedTransactionParams{
		ID:           pgUUID(t.ID),
		InstrumentID: pgUUID(t.InstrumentID),
		Side:         string(t.Side),
		Quantity:     quantity,
		Price:        price,
		Fee:          fee,
		ExecutedAt:   pgTime(t.ExecutedAt),
		CreatedAt:    pgTime(t.CreatedAt),
		PortfolioID:  pgUUID(t.PortfolioID),
		OwnerID:      pgUUID(ownerID),
	})
	if err != nil {
		return domain.Transaction{}, mapError(err)
	}
	return transactionFromRow(transactionRow(row))
}

// Update writes the trade fields when the version matches and returns the row at version + 1.
// It matches on t.PortfolioID and never changes it. A stale version returns
// domain.ErrVersionConflict; a missing, foreign or other-portfolio trade domain.ErrNotFound.
func (r *TransactionRepository) Update(ctx context.Context, ownerID domain.ID, t domain.Transaction) (domain.Transaction, error) {
	quantity, price, fee, err := tradeNumerics(t)
	if err != nil {
		return domain.Transaction{}, err
	}
	row, err := r.q.UpdateOwnedTransaction(ctx, sqlc.UpdateOwnedTransactionParams{
		InstrumentID: pgUUID(t.InstrumentID),
		Side:         string(t.Side),
		Quantity:     quantity,
		Price:        price,
		Fee:          fee,
		ExecutedAt:   pgTime(t.ExecutedAt),
		UpdatedAt:    pgTime(t.UpdatedAt),
		ID:           pgUUID(t.ID),
		PortfolioID:  pgUUID(t.PortfolioID),
		Version:      t.Version.Int32(),
		OwnerID:      pgUUID(ownerID),
	})
	if err = mapError(err); errors.Is(err, domain.ErrNotFound) {
		exists, existsErr := r.q.OwnedTransactionExists(ctx, sqlc.OwnedTransactionExistsParams{
			OwnerID: pgUUID(ownerID), ID: pgUUID(t.ID), PortfolioID: pgUUID(t.PortfolioID),
		})
		switch {
		case existsErr != nil:
			return domain.Transaction{}, mapError(existsErr)
		case exists:
			return domain.Transaction{}, domain.ErrVersionConflict
		default:
			return domain.Transaction{}, domain.ErrNotFound
		}
	} else if err != nil {
		return domain.Transaction{}, err
	}
	return transactionFromRow(transactionRow(row))
}

// Delete removes one trade of the owner's portfolio, or returns domain.ErrNotFound. The service
// checks the version under the portfolio lock first.
func (r *TransactionRepository) Delete(ctx context.Context, ownerID, portfolioID, transactionID domain.ID) error {
	n, err := r.q.DeleteOwnedTransaction(ctx, sqlc.DeleteOwnedTransactionParams{
		ID: pgUUID(transactionID), PortfolioID: pgUUID(portfolioID), OwnerID: pgUUID(ownerID),
	})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// requireOwnedPortfolio tells an empty result from a portfolio that is missing or not the owner's.
func (r *TransactionRepository) requireOwnedPortfolio(ctx context.Context, ownerID, portfolioID domain.ID) error {
	exists, err := r.q.OwnedPortfolioExists(ctx, sqlc.OwnedPortfolioExistsParams{ID: pgUUID(portfolioID), UserID: pgUUID(ownerID)})
	if err != nil {
		return mapError(err)
	}
	if !exists {
		return domain.ErrNotFound
	}
	return nil
}

// tradeNumerics encodes the decimal fields. They come from Parse*, so numeric(28,10) stores them
// exactly; a value that bypassed Parse* could be rounded by PostgreSQL.
func tradeNumerics(t domain.Transaction) (quantity, price, fee pgtype.Numeric, err error) {
	if quantity, err = pgNumeric(t.Quantity.String()); err != nil {
		return
	}
	if price, err = pgNumeric(t.Price.String()); err != nil {
		return
	}
	fee, err = pgNumeric(t.Fee.String())
	return
}

func transactionFromRow(row transactionRow) (domain.Transaction, error) {
	id, err := idFromPG("transaction.id", row.ID)
	if err != nil {
		return domain.Transaction{}, err
	}
	portfolioID, err := idFromPG("transaction.portfolio_id", row.PortfolioID)
	if err != nil {
		return domain.Transaction{}, err
	}
	instrumentID, err := idFromPG("transaction.instrument_id", row.InstrumentID)
	if err != nil {
		return domain.Transaction{}, err
	}
	if !row.Sequence.Valid || row.Sequence.Int64 < 1 {
		return domain.Transaction{}, domain.NewError(domain.CodeInternal, "stored transaction.sequence is not positive")
	}
	symbol, err := domain.ParseSymbol(row.Symbol)
	if err != nil {
		return domain.Transaction{}, fromStored(err)
	}
	side, err := domain.ParseSide(row.Side)
	if err != nil {
		return domain.Transaction{}, fromStored(err)
	}
	quantity, err := storedDecimal("transaction.quantity", row.Quantity, domain.ParseQuantity)
	if err != nil {
		return domain.Transaction{}, err
	}
	price, err := storedDecimal("transaction.price", row.Price, domain.ParsePrice)
	if err != nil {
		return domain.Transaction{}, err
	}
	fee, err := storedDecimal("transaction.fee", row.Fee, domain.ParseFee)
	if err != nil {
		return domain.Transaction{}, err
	}
	version, err := domain.ParseVersion(int64(row.Version))
	if err != nil {
		return domain.Transaction{}, fromStored(err)
	}
	return domain.Transaction{
		ID: id, PortfolioID: portfolioID, InstrumentID: instrumentID, Symbol: symbol, Sequence: row.Sequence.Int64,
		Side: side, Quantity: quantity, Price: price, Fee: fee, ExecutedAt: timeFromPG(row.ExecutedAt), Version: version,
		CreatedAt: timeFromPG(row.CreatedAt), UpdatedAt: timeFromPG(row.UpdatedAt),
	}, nil
}

// storedDecimal reads a numeric column through the domain parser without float.
func storedDecimal[T any](field string, n pgtype.Numeric, parse func(string) (T, error)) (T, error) {
	var zero T
	s, err := numericString(field, n)
	if err != nil {
		return zero, err
	}
	v, err := parse(s)
	if err != nil {
		return zero, fromStored(err)
	}
	return v, nil
}

// transactionFilterFingerprint identifies the filters of a transaction list (not the page).
func transactionFilterFingerprint(f domain.TransactionFilter) string {
	h := sha256.New()
	write := func(s string) { h.Write([]byte(s)); h.Write([]byte{0}) }
	if f.Symbol != nil {
		write("symbol=" + f.Symbol.String())
	}
	if f.Side != nil {
		write("side=" + string(*f.Side))
	}
	if f.From != nil {
		write("from=" + f.From.UTC().Format(time.RFC3339Nano))
	}
	if f.To != nil {
		write("to=" + f.To.UTC().Format(time.RFC3339Nano))
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}
```

แก้ `internal/adapter/postgres/store.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go

// Portfolios returns the portfolio repository over the pool.
func (s *Store) Portfolios() port.PortfolioRepository {
	return &PortfolioRepository{q: sqlc.New(s.pool)}
}
```

   ด้วย

```go

// Portfolios returns the portfolio repository over the pool.
func (s *Store) Portfolios() port.PortfolioRepository {
	return &PortfolioRepository{q: sqlc.New(s.pool)}
}

// Transactions returns the transaction repository over the pool.
func (s *Store) Transactions() port.TransactionRepository {
	return &TransactionRepository{q: sqlc.New(s.pool)}
}
```

2. แทน

```go

// Portfolios returns the portfolio repository bound to this transaction.
func (r *txRepositories) Portfolios() port.PortfolioRepository {
	return &PortfolioRepository{q: r.q, inTx: true}
}
```

   ด้วย

```go

// Portfolios returns the portfolio repository bound to this transaction.
func (r *txRepositories) Portfolios() port.PortfolioRepository {
	return &PortfolioRepository{q: r.q, inTx: true}
}

// Transactions returns the transaction repository bound to this transaction.
func (r *txRepositories) Transactions() port.TransactionRepository {
	return &TransactionRepository{q: r.q}
}
```

แก้ `internal/adapter/postgres/portfolios.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
	"errors"

	"go-backend-stock/internal/adapter/postgres/sqlc"
```

   ด้วย

```go
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"go-backend-stock/internal/adapter/postgres/sqlc"
```

2. แทน

```go
	if err != nil {
		return mapError(err)
	}
	if n == 1 {
```

   ด้วย

```go
	if err != nil {
		// A transaction inserted concurrently by a caller that skipped the portfolio lock.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == sqlStateForeignKeyViolation && pgErr.ConstraintName == "transactions_portfolio_id_fkey" {
			return domain.ErrPortfolioNotEmpty
		}
		return mapError(err)
	}
	if n == 1 {
```

แก้ `internal/adapter/postgres/errors.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
)
```

   ด้วย

```go
const (
	sqlStateUniqueViolation = "23505"
	// sqlStateForeignKeyViolation is mapped only where its meaning is known: deleting a portfolio
	// that still has trades (PortfolioRepository.DeleteOwnedEmpty). Elsewhere it is internal; an
	// insert into a missing portfolio, for example, is not "portfolio not empty".
	sqlStateForeignKeyViolation = "23503"
)
```

2. ลบ

```go
		case sqlStateForeignKeyViolation:
			if pgErr.ConstraintName == "transactions_portfolio_id_fkey" {
				return domain.ErrPortfolioNotEmpty
			}
```

แก้ `internal/core/domain/transaction.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
// NewTransactionFilter checks the filter a client asked for: From must not be after To.
func NewTransactionFilter(page Page, symbol *Symbol, side *Side, from, to *time.Time) (TransactionFilter, error) {
	if page.IsZero() {
		return TransactionFilter{}, invariant("filter.page", "is required")
	}
```

   ด้วย

```go
// NewTransactionFilter checks the filter a client asked for: From must not be after To. An absent
// filter is nil; a pointer to the zero Symbol or to a side other than buy/sell is a caller bug.
func NewTransactionFilter(page Page, symbol *Symbol, side *Side, from, to *time.Time) (TransactionFilter, error) {
	if page.IsZero() {
		return TransactionFilter{}, invariant("filter.page", "is required")
	}
	if symbol != nil && symbol.IsZero() {
		return TransactionFilter{}, invariant("filter.symbol", "must be nil or a parsed symbol")
	}
	if side != nil && !isOneOf(*side, SideBuy, SideSell) {
		return TransactionFilter{}, invariant("filter.side", "must be nil, buy or sell")
	}
```

- [ ] **Step 5: รัน tests และ lint ให้ผ่าน**

Run: `gofmt -l internal && make test && make test-integration && make lint`
Expected: `gofmt` ไม่พิมพ์อะไร, `ok` ทุกแพ็กเกจ, `ok  	go-backend-stock/internal/adapter/postgres` โดยไม่มี `SKIP` (ตรวจด้วย `GOFLAGS=-v make test-integration 2>&1 | grep -c '^--- SKIP'` ได้ `0`) และ `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add -A internal
git commit -m "feat(db): add transaction repository and map the trades FK only on portfolio delete"
```

- [ ] **Step 7: พิสูจน์ว่า test จับการเขียนผิดได้**

ใน `internal/adapter/postgres/queries/transactions.sql` แทนชั่วคราว

```sql
WHERE t.id = @id AND t.portfolio_id = @portfolio_id AND t.version = @version
```

ด้วย

```sql
WHERE t.id = @id AND @portfolio_id::uuid IS NOT NULL AND t.version = @version
```

Run: `make sqlc && make test-integration` (ดูผลของ `TestTransactionUpdateChecksVersionAndNeverMovesPortfolio`)
Expected: FAIL ด้วย `Update into another portfolio error = <nil>, want not_found`

จากนั้นคืนไฟล์: `git checkout -- internal/adapter/postgres/queries/transactions.sql && make sqlc && git status --short` ต้องไม่แสดงอะไร

- [ ] **Step 8: ตรวจทั้ง repo**

Run: `make check`
Expected: ผ่าน (`0 issues.`, swagger valid, tests `ok`, ไม่มี diff ของ `docs/api` และ `internal/adapter/postgres/sqlc` ถูก commit ครบ)

---

### Task 5: `IdempotencyRepository` และ `Store` เป็น `port.UnitOfWork`

**Files:**
- Create: `internal/adapter/postgres/queries/idempotency.sql`, `internal/adapter/postgres/idempotency.go`, `internal/adapter/postgres/idempotency_test.go`, `internal/adapter/postgres/uow_test.go`
- Modify: `internal/adapter/postgres/store.go`
- Generated by `make sqlc`: `internal/adapter/postgres/sqlc/idempotency.sql.go`

**Interfaces:**
- Consumes: `insertPortfolio`, `newPortfolio` (Task 3); `insertTrade`, `newTrade`, `insertInstrument` (Task 4); `randomHash`, `runConcurrently` (3b)
- Produces: `(*txRepositories).Idempotency()` (ไม่มีบน pool); `(*Store).WithinTransaction(ctx, func(port.TxRepositories) error) error`; compile-time `var _ port.UnitOfWork = (*Store)(nil)` และ `var _ port.TxRepositories = (*txRepositories)(nil)`; test alias `portTx = port.TxRepositories`

- [ ] **Step 1: เขียน failing tests**

`internal/adapter/postgres/idempotency_test.go`:

```go
package postgres

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func newIdempotencyRecord(t *testing.T, scope domain.IdempotencyScope, key string, createdAt time.Time) domain.IdempotencyRecord {
	t.Helper()
	rec, err := domain.NewIdempotencyRecord(domain.IdempotencyRecord{
		Scope: scope, Key: key, RequestHash: randomHash(t),
		Result:    json.RawMessage(`{"id":"t-1","quantity":"0.5","symbol":"AAPL"}`),
		CreatedAt: createdAt, ExpiresAt: createdAt.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func createScope(t *testing.T, store *Store) domain.IdempotencyScope {
	t.Helper()
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	return domain.IdempotencyScope{UserID: user.ID, PortfolioID: p.ID, Operation: domain.IdempotencyOperationCreateTransaction}
}

func saveRecord(t *testing.T, store *Store, rec domain.IdempotencyRecord) {
	t.Helper()
	if err := store.WithinTransaction(t.Context(), func(r portTx) error { return r.Idempotency().Save(t.Context(), rec) }); err != nil {
		t.Fatalf("save idempotency record: %v", err)
	}
}

func getRecord(t *testing.T, store *Store, scope domain.IdempotencyScope, key string) (domain.IdempotencyRecord, error) {
	t.Helper()
	var got domain.IdempotencyRecord
	err := store.WithinTransaction(t.Context(), func(r portTx) error {
		var err error
		got, err = r.Idempotency().Get(t.Context(), scope, key)
		return err
	})
	return got, err
}

// TestIdempotencySaveAndGet: the stored result is the same JSON value (jsonb may change spacing
// and key order, never the values).
func TestIdempotencySaveAndGet(t *testing.T) {
	store := newRuntimeStore(t)
	rec := newIdempotencyRecord(t, createScope(t, store), "key-1", now())
	saveRecord(t, store, rec)

	got, err := getRecord(t, store, rec.Scope, rec.Key)
	if err != nil || got.Scope != rec.Scope || got.Key != rec.Key || string(got.RequestHash) != string(rec.RequestHash) ||
		!got.CreatedAt.Equal(rec.CreatedAt) || !got.ExpiresAt.Equal(rec.ExpiresAt) {
		t.Fatalf("Get = %+v, %v; want %+v", got, err, rec)
	}
	var want, have any
	if err := json.Unmarshal(rec.Result, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got.Result, &have); err != nil || !reflect.DeepEqual(want, have) {
		t.Fatalf("result = %s, want the same JSON as %s", got.Result, rec.Result)
	}
}

// TestIdempotencyKeysAreScoped: the same key under another user, portfolio or key value is a
// different record.
func TestIdempotencyKeysAreScoped(t *testing.T) {
	store := newRuntimeStore(t)
	rec := newIdempotencyRecord(t, createScope(t, store), "shared-key", now())
	saveRecord(t, store, rec)
	otherScope := createScope(t, store)

	if _, err := getRecord(t, store, otherScope, rec.Key); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get(other scope) error = %v, want not_found", err)
	}
	foreignOwner := rec.Scope
	foreignOwner.UserID = otherScope.UserID
	if _, err := getRecord(t, store, foreignOwner, rec.Key); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get(another user, same portfolio) error = %v, want not_found", err)
	}
	if _, err := getRecord(t, store, rec.Scope, "other-key"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get(other key) error = %v, want not_found", err)
	}
	saveRecord(t, store, newIdempotencyRecord(t, otherScope, rec.Key, now()))
}

// TestIdempotencyConcurrentSaveSucceedsOnce: AGENTS.md "duplicate retries" — the primary key lets
// one of several simultaneous saves of a key win; the rest get idempotency_conflict.
func TestIdempotencyConcurrentSaveSucceedsOnce(t *testing.T) {
	store := newRuntimeStore(t)
	scope := createScope(t, store)

	succeeded, errs := runConcurrently(10, func() error {
		rec := newIdempotencyRecord(t, scope, "retry-key", now())
		return store.WithinTransaction(t.Context(), func(r portTx) error { return r.Idempotency().Save(t.Context(), rec) })
	})

	if succeeded != 1 {
		t.Fatalf("%d concurrent saves succeeded, want exactly 1", succeeded)
	}
	for _, err := range errs {
		if !errors.Is(err, domain.ErrIdempotencyConflict) {
			t.Errorf("losing save error = %v, want idempotency_conflict", err)
		}
	}
}

// TestIdempotencyDeleteExpiredKeepsLiveRecords: a key can be reused only once its record has
// expired; until then DeleteExpired leaves it in place.
func TestIdempotencyDeleteExpiredKeepsLiveRecords(t *testing.T) {
	store := newRuntimeStore(t)
	rec := newIdempotencyRecord(t, createScope(t, store), "ttl-key", now())
	saveRecord(t, store, rec)

	for _, at := range []time.Time{rec.CreatedAt, rec.ExpiresAt.Add(-time.Microsecond)} {
		if err := store.WithinTransaction(t.Context(), func(r portTx) error {
			return r.Idempotency().DeleteExpired(t.Context(), rec.Scope, rec.Key, at)
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := getRecord(t, store, rec.Scope, rec.Key); err != nil {
			t.Fatalf("live record deleted at %v: %v", at, err)
		}
	}
	if err := store.WithinTransaction(t.Context(), func(r portTx) error {
		return r.Idempotency().DeleteExpired(t.Context(), rec.Scope, rec.Key, rec.ExpiresAt)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := getRecord(t, store, rec.Scope, rec.Key); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expired record still there: %v", err)
	}
}

// TestIdempotencyDeleteForPortfolioAllowsPortfolioDelete: records block deleting their portfolio
// (FK RESTRICT) until DeleteForPortfolio removes them; another user cannot remove them.
func TestIdempotencyDeleteForPortfolioAllowsPortfolioDelete(t *testing.T) {
	store := newRuntimeStore(t)
	rec := newIdempotencyRecord(t, createScope(t, store), "delete-key", now())
	saveRecord(t, store, rec)
	stranger := insertUser(t, store)

	if err := store.WithinTransaction(t.Context(), func(r portTx) error {
		return r.Idempotency().DeleteForPortfolio(t.Context(), stranger.ID, rec.Scope.PortfolioID)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := getRecord(t, store, rec.Scope, rec.Key); err != nil {
		t.Fatalf("another user's DeleteForPortfolio removed the record: %v", err)
	}
	err := store.WithinTransaction(t.Context(), func(r portTx) error {
		if err := r.Idempotency().DeleteForPortfolio(t.Context(), rec.Scope.UserID, rec.Scope.PortfolioID); err != nil {
			return err
		}
		return r.Portfolios().DeleteOwnedEmpty(t.Context(), rec.Scope.UserID, rec.Scope.PortfolioID)
	})
	if err != nil {
		t.Fatalf("delete records then portfolio: %v", err)
	}
}
```

`internal/adapter/postgres/uow_test.go`:

```go
package postgres

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

type portTx = port.TxRepositories

// TestWithinTransactionRollsBackPortfolioWhenAuditFails is the failure injection of milestone 3
// spec section 4.5: a business change and its audit commit together or not at all.
func TestWithinTransactionRollsBackPortfolioWhenAuditFails(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := newPortfolio(t, user.ID, "Rolled back "+newID(t).String(), now())
	bad := auditEvent(t, `{}`)
	bad.Metadata = json.RawMessage(`["not","an","object"]`) // bypasses NewAuditEvent; violates audit_events_metadata_object

	err := store.WithinTransaction(t.Context(), func(r portTx) error {
		if err := r.Portfolios().Insert(t.Context(), p); err != nil {
			return err
		}
		return r.Audit().Append(t.Context(), bad)
	})

	if !errors.Is(err, domain.ErrInternal) {
		t.Fatalf("error = %v, want internal from the audit CHECK", err)
	}
	if _, err := store.Portfolios().GetOwned(t.Context(), user.ID, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("portfolio committed although its audit failed: %v", err)
	}
}

func TestWithinTransactionCommitsPortfolioWithAudit(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := newPortfolio(t, user.ID, "Committed "+newID(t).String(), now())
	e := auditEvent(t, `{}`)

	if err := store.WithinTransaction(t.Context(), func(r portTx) error {
		if err := r.Portfolios().Insert(t.Context(), p); err != nil {
			return err
		}
		return r.Audit().Append(t.Context(), e)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Portfolios().GetOwned(t.Context(), user.ID, p.ID); err != nil || !auditExists(t, e.ID) {
		t.Fatalf("portfolio or audit missing after commit: %v", err)
	}
}

var errNotEnoughShares = errors.New("not enough shares")

// TestConcurrentSellsNeverOversell: AGENTS.md "concurrent sells". Ten requests each sell one of
// five held shares; LockOwned serializes them, so exactly five succeed and the holding never goes
// negative. (Counting shares here stands in for the replay of milestone 7.)
func TestConcurrentSellsNeverOversell(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	p := insertPortfolio(t, store, user.ID)
	instrument := insertInstrument(t, store)
	insertTrade(t, store, user.ID, newTrade(t, p.ID, instrument, domain.SideBuy, "5", "10", now()))
	one := decimal.NewFromInt(1)

	succeeded, errs := runConcurrently(10, func() error {
		return store.WithinTransaction(t.Context(), func(r portTx) error {
			if _, err := r.Portfolios().LockOwned(t.Context(), user.ID, p.ID); err != nil {
				return err
			}
			history, err := r.Transactions().History(t.Context(), user.ID, p.ID)
			if err != nil {
				return err
			}
			held := decimal.Zero
			for _, tr := range history {
				if tr.Side == domain.SideBuy {
					held = held.Add(tr.Quantity.Decimal())
				} else {
					held = held.Sub(tr.Quantity.Decimal())
				}
			}
			if held.LessThan(one) {
				return errNotEnoughShares
			}
			_, err = r.Transactions().Insert(t.Context(), user.ID, newTrade(t, p.ID, instrument, domain.SideSell, "1", "11", now()))
			return err
		})
	})

	if succeeded != 5 {
		t.Fatalf("%d of 10 concurrent sells succeeded, want exactly 5", succeeded)
	}
	for _, err := range errs {
		if !errors.Is(err, errNotEnoughShares) {
			t.Errorf("rejected sell error = %v, want not enough shares", err)
		}
	}
}
```

- [ ] **Step 2: รันให้เห็นว่าล้มเหลว**

Run: `make test-integration`
Expected: FAIL ด้วย `[build failed]` และ `store.WithinTransaction undefined (type *Store has no field or method WithinTransaction)`

- [ ] **Step 3: เขียน queries แล้ว generate**

`internal/adapter/postgres/queries/idempotency.sql`:

```sql
-- name: GetIdempotencyKey :one
SELECT portfolio_id, user_id, operation, key, request_hash, result, created_at, expires_at
FROM app.idempotency_keys
WHERE portfolio_id = $1 AND user_id = $2 AND operation = $3 AND key = $4;

-- name: DeleteExpiredIdempotencyKey :exec
DELETE FROM app.idempotency_keys
WHERE portfolio_id = @portfolio_id AND user_id = @user_id AND operation = @operation AND key = @key
  AND expires_at <= @now;

-- A second Save of the same key violates idempotency_keys_pkey and aborts the transaction.
-- name: InsertIdempotencyKey :exec
INSERT INTO app.idempotency_keys (portfolio_id, user_id, operation, key, request_hash, result, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: DeleteIdempotencyKeysForPortfolio :exec
DELETE FROM app.idempotency_keys WHERE portfolio_id = $1 AND user_id = $2;
```

Run: `make sqlc && ls internal/adapter/postgres/sqlc`
Expected: ไม่มี error และมี `idempotency.sql.go` เพิ่ม

- [ ] **Step 4: เขียนโค้ด**

`internal/adapter/postgres/idempotency.go`:

```go
package postgres

import (
	"context"
	"encoding/json"
	"time"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// IdempotencyRepository stores the first result of each idempotent request (24 hours by default,
// set by the service through ExpiresAt).
type IdempotencyRepository struct {
	q *sqlc.Queries
}

var _ port.IdempotencyRepository = (*IdempotencyRepository)(nil)

// Get returns the record for the key, expired or not (the service decides), or domain.ErrNotFound.
func (r *IdempotencyRepository) Get(ctx context.Context, scope domain.IdempotencyScope, key string) (domain.IdempotencyRecord, error) {
	row, err := r.q.GetIdempotencyKey(ctx, sqlc.GetIdempotencyKeyParams{
		PortfolioID: pgUUID(scope.PortfolioID), UserID: pgUUID(scope.UserID), Operation: string(scope.Operation), Key: key,
	})
	if err != nil {
		return domain.IdempotencyRecord{}, mapError(err)
	}
	return idempotencyFromRow(row)
}

// DeleteExpired removes the key's record only if it has expired at now, so an expired key can be
// reused; a live record is left alone.
func (r *IdempotencyRepository) DeleteExpired(ctx context.Context, scope domain.IdempotencyScope, key string, now time.Time) error {
	return mapError(r.q.DeleteExpiredIdempotencyKey(ctx, sqlc.DeleteExpiredIdempotencyKeyParams{
		PortfolioID: pgUUID(scope.PortfolioID), UserID: pgUUID(scope.UserID), Operation: string(scope.Operation), Key: key,
		Now: pgTime(now),
	}))
}

// Save stores a record built with domain.NewIdempotencyRecord. A key the scope already has
// returns domain.ErrIdempotencyConflict and aborts the transaction (see the port).
func (r *IdempotencyRepository) Save(ctx context.Context, rec domain.IdempotencyRecord) error {
	return mapError(r.q.InsertIdempotencyKey(ctx, sqlc.InsertIdempotencyKeyParams{
		PortfolioID: pgUUID(rec.Scope.PortfolioID),
		UserID:      pgUUID(rec.Scope.UserID),
		Operation:   string(rec.Scope.Operation),
		Key:         rec.Key,
		RequestHash: rec.RequestHash,
		Result:      rec.Result,
		CreatedAt:   pgTime(rec.CreatedAt),
		ExpiresAt:   pgTime(rec.ExpiresAt),
	}))
}

// DeleteForPortfolio removes every record of the owner's portfolio, before the portfolio is deleted.
func (r *IdempotencyRepository) DeleteForPortfolio(ctx context.Context, ownerID, portfolioID domain.ID) error {
	return mapError(r.q.DeleteIdempotencyKeysForPortfolio(ctx, sqlc.DeleteIdempotencyKeysForPortfolioParams{
		PortfolioID: pgUUID(portfolioID), UserID: pgUUID(ownerID),
	}))
}

func idempotencyFromRow(row sqlc.AppIdempotencyKey) (domain.IdempotencyRecord, error) {
	portfolioID, err := idFromPG("idempotency.portfolio_id", row.PortfolioID)
	if err != nil {
		return domain.IdempotencyRecord{}, err
	}
	userID, err := idFromPG("idempotency.user_id", row.UserID)
	if err != nil {
		return domain.IdempotencyRecord{}, err
	}
	operation, err := domain.ParseIdempotencyOperation(row.Operation)
	if err != nil {
		return domain.IdempotencyRecord{}, fromStored(err)
	}
	return domain.IdempotencyRecord{
		Scope:       domain.IdempotencyScope{UserID: userID, PortfolioID: portfolioID, Operation: operation},
		Key:         row.Key,
		RequestHash: row.RequestHash,
		Result:      json.RawMessage(row.Result),
		CreatedAt:   timeFromPG(row.CreatedAt),
		ExpiresAt:   timeFromPG(row.ExpiresAt),
	}, nil
}
```

แก้ `internal/adapter/postgres/store.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
// Store owns the connection pool and builds repositories. It becomes the port.UnitOfWork once
// every repository exists (milestone 3c); until then transactions go through withinTx.
type Store struct {
	pool *pgxpool.Pool
}
```

   ด้วย

```go
// Store owns the connection pool, builds repositories and is the port.UnitOfWork.
type Store struct {
	pool *pgxpool.Pool
}

var (
	_ port.UnitOfWork     = (*Store)(nil)
	_ port.TxRepositories = (*txRepositories)(nil)
)
```

2. แทน

```go
// txRepositories are the repositories bound to one database transaction. Plan 3c adds the
// portfolio-side accessors until it satisfies port.TxRepositories.
```

   ด้วย

```go
// txRepositories are the repositories bound to one database transaction (port.TxRepositories).
```

3. แทน

```go

// Transactions returns the transaction repository bound to this transaction.
func (r *txRepositories) Transactions() port.TransactionRepository {
	return &TransactionRepository{q: r.q}
}
```

   ด้วย

```go

// Transactions returns the transaction repository bound to this transaction.
func (r *txRepositories) Transactions() port.TransactionRepository {
	return &TransactionRepository{q: r.q}
}

// Idempotency returns the idempotency repository bound to this transaction. Records are only
// read and written under the portfolio lock, so there is no pool accessor.
func (r *txRepositories) Idempotency() port.IdempotencyRepository {
	return &IdempotencyRepository{q: r.q}
}
```

4. แทน

```go
// Audit returns the audit repository bound to this transaction.
func (r *txRepositories) Audit() port.AuditRepository {
	return &AuditRepository{q: r.q}
}
```

   ด้วย

```go
// Audit returns the audit repository bound to this transaction.
func (r *txRepositories) Audit() port.AuditRepository {
	return &AuditRepository{q: r.q}
}

// WithinTransaction runs fn in one transaction (see withinTx and port.UnitOfWork).
func (s *Store) WithinTransaction(ctx context.Context, fn func(port.TxRepositories) error) error {
	return s.withinTx(ctx, func(r *txRepositories) error { return fn(r) })
}
```

- [ ] **Step 5: รัน tests และ lint ให้ผ่าน**

Run: `gofmt -l internal && make test && make test-integration && make lint`
Expected: `gofmt` ไม่พิมพ์อะไร, `ok` ทุกแพ็กเกจ, `ok  	go-backend-stock/internal/adapter/postgres` โดยไม่มี `SKIP` (ตรวจด้วย `GOFLAGS=-v make test-integration 2>&1 | grep -c '^--- SKIP'` ได้ `0`) และ `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add -A internal
git commit -m "feat(db): add idempotency repository and make Store the unit of work"
```

- [ ] **Step 7: พิสูจน์ว่า test จับการเขียนผิดได้**

ใน `internal/adapter/postgres/queries/portfolios.sql` แทนชั่วคราว

```sql
FROM app.portfolios WHERE id = $1 AND user_id = $2
FOR UPDATE;
```

ด้วย

```sql
FROM app.portfolios WHERE id = $1 AND user_id = $2;
```

Run: `make sqlc && make test-integration` (ดูผลของ `TestConcurrentSellsNeverOversell`)
Expected: FAIL ด้วย `... of 10 concurrent sells succeeded, want exactly 5` (เกิน 5)

จากนั้นคืนไฟล์: `git checkout -- internal/adapter/postgres/queries/portfolios.sql && make sqlc && git status --short` ต้องไม่แสดงอะไร

- [ ] **Step 8: ตรวจทั้ง repo**

Run: `make check`
Expected: ผ่าน (`0 issues.`, swagger valid, tests `ok`, ไม่มี diff ของ `docs/api` และ `internal/adapter/postgres/sqlc` ถูก commit ครบ)

---

### Task 6: เอกสาร — spec, roadmap และ README

**Files:**
- Modify: `docs/superpowers/specs/2026-10-17-milestone-3-persistence-design.md`, `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md`, `README.md`

**Interfaces:**
- Consumes: พฤติกรรมจาก Tasks 1–5
- Produces: เอกสารที่ตรงกับโค้ด (AGENTS.md) และข้อจำกัดของ phase 1 ใน README

- [ ] **Step 1: แก้ spec ของ milestone 3**

แก้ `docs/superpowers/specs/2026-10-17-milestone-3-persistence-design.md` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```text
[2026-10-20-milestone-3b-auth-repositories.md](../plans/2026-10-20-milestone-3b-auth-repositories.md)
```

   ด้วย

```text
[2026-10-20-milestone-3b-auth-repositories.md](../plans/2026-10-20-milestone-3b-auth-repositories.md); แผน 3c: [2026-10-21-milestone-3c-portfolio-repositories.md](../plans/2026-10-21-milestone-3c-portfolio-repositories.md)
```

2. แทน

```text
| `IdempotencyRepository` | `Get` คืน record แม้หมดอายุ (service ตัดสิน); `DeleteExpired` ลบเฉพาะเมื่อหมดอายุ ณ `now`; `Save` ชน key เดิม → `ErrIdempotencyConflict`; `DeleteForPortfolio` ใช้ก่อนลบพอร์ต |
```

   ด้วย

```text
| `IdempotencyRepository` | `Get` คืน record แม้หมดอายุ (service ตัดสิน); `DeleteExpired` ลบเฉพาะเมื่อหมดอายุ ณ `now`; `Save` ชน key เดิม → `ErrIdempotencyConflict`; `DeleteForPortfolio` ใช้ก่อนลบพอร์ต |

รายละเอียดที่ตัดสินในแผน 3c (2026-10-21):

- **conflict กับ transaction:** `PortfolioRepository.Insert` ใช้ `ON CONFLICT (user_id, lower(name)) DO NOTHING` + จำนวนแถว ชื่อซ้ำจึงไม่ทำให้ transaction จบ; การเปลี่ยนชื่อซ้ำใน `UpdateOwned` และ `IdempotencyRepository.Save` ที่ชน key ยังอาศัย unique violation ซึ่งทำให้ transaction จบตาม `port.UnitOfWork` (service คืน error ทันทีโดยไม่ต้องเขียน audit ความล้มเหลว)
- `UpdateOwned`/`Update` ที่ไม่มีแถวตรง จะตรวจต่อว่าแถวของเจ้าของยังอยู่หรือไม่: อยู่ → `ErrVersionConflict`, ไม่อยู่หรือไม่ใช่ของตน → `ErrNotFound`
- `DeleteOwnedEmpty` ลบด้วยเงื่อนไข `NOT EXISTS` ของ transactions (transaction ยังใช้ต่อได้เมื่อพอร์ตไม่ว่าง); FK violation ของ `transactions_portfolio_id_fkey` ที่เกิดเมื่อมีรายการถูก insert พร้อมกันโดยไม่ lock แปลงเป็น `ErrPortfolioNotEmpty` **เฉพาะใน method นี้** และ `mapError` ไม่แปลง FK นี้อีก (ข้อส่งต่อจากรีวิว 3a)
- `History`/`List` ของพอร์ตที่ไม่มีหรือไม่ใช่ของตน → `ErrNotFound`; พอร์ตของตนที่ว่าง → รายการว่าง
- **instrument ที่ provider เปลี่ยน symbol** (ผู้ใช้เลือก 2026-10-21): `Upsert` ใช้ `ON CONFLICT DO NOTHING` ครอบทั้ง unique ของ symbol และ `(source, provider_asset_id)` แล้วอ่านด้วย symbol; ถ้า asset นั้นอยู่ใต้ symbol อื่น คืน `unknown_symbol` (422) พร้อมข้อความว่า symbol ถูกเปลี่ยนชื่อและยังไม่รองรับ; transaction ยังใช้ต่อได้
- ตัวกรองรายการ: `from`/`to` รวมขอบทั้งสองด้าน, `symbol` เทียบกับ symbol ปัจจุบันของ instrument; `NewTransactionFilter` ปฏิเสธ pointer ไปยัง `Symbol{}` หรือ side ที่ไม่ใช่ buy/sell เป็น `internal` (แก้ Minor ในหัวข้อ 7)
- `Idempotency()` มีเฉพาะใน `TxRepositories` เพราะอ่าน/เขียนใต้ portfolio lock เสมอ; `result` ที่อ่านกลับจาก jsonb เป็นค่า JSON เดิมแต่ช่องว่างและลำดับ key อาจต่างจากที่ส่งไป
- `Store` implement `port.UnitOfWork` แล้ว; `withinTx` rollback และคืน connection เมื่อ callback ออกด้วย `runtime.Goexit` ด้วย (พบระหว่าง prototype ของ 3c: `t.Fatal` ใน callback ทำให้ connection ค้างและ `pool.Close` รอตลอดไป)
```

3. แทน

```text
- cursor = base64url ของค่าตำแหน่งสุดท้ายและลายนิ้วมือของ filter (≤ 1 KiB ตาม `domain.Page`); เข้ารหัส/ถอดรหัสใน postgres adapter
```

   ด้วย

```text
- cursor = base64url ของ JSON `{k, f, t, i|s}`: ชนิดของรายการ (`portfolios`/`transactions`), ลายนิ้วมือของ filter (8 ไบต์แรกของ SHA-256 ของ symbol/side/from/to เป็น hex; พอร์ตใช้ค่าว่าง), เวลาของแถวสุดท้าย และ id หรือ sequence ของแถวนั้น (≤ 1 KiB ตาม `domain.Page`); เข้ารหัส/ถอดรหัสใน postgres adapter
```

4. แทน

```text
| `NewTransactionFilter` รับ `Symbol{}`/`Side("")` ว่าง | 3c |
```

   ด้วย

```text
| `NewTransactionFilter` รับ `Symbol{}`/`Side("")` ว่าง | 3c (แก้แล้ว: คืน `internal`) |
```

- [ ] **Step 2: แก้ roadmap**

แก้ `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```text
แผน 3b ผ่านการตรวจรับเมื่อ 2026-10-21 (รีวิวทั้ง branch ด้วย subagent แก้ Important 2 ข้อและ Minor 4 ข้อ; CI บน GitHub ยังไม่ได้รันเพราะยังไม่ push) ขั้นต่อไปคือแผน 3c
```

   ด้วย

```text
แผน 3b ผ่านการตรวจรับเมื่อ 2026-10-21 (รีวิวทั้ง branch ด้วย subagent แก้ Important 2 ข้อและ Minor 4 ข้อ; CI บน GitHub ยังไม่ได้รันเพราะยังไม่ push) แผน 3c ลงมือครบแล้ว รอผู้ใช้ตรวจรับ
```

2. แทน

```text
3c (repositories ฝั่งพอร์ต)
```

   ด้วย

```text
[3c](2026-10-21-milestone-3c-portfolio-repositories.md) (repositories ฝั่งพอร์ต)
```

3. แทน

```text
- [ ] เขียน SQL queries, generate sqlc, map numeric/domain types และทดสอบ repository กับ PostgreSQL จริง (3a ทำแล้ว: sqlc + mapper + `AuditRepository`; 3b ทำแล้ว: repositories ฝั่ง auth ทั้ง 6 ตัว; ยังเหลือฝั่งพอร์ตใน 3c)
```

   ด้วย

```text
- [x] เขียน SQL queries, generate sqlc, map numeric/domain types และทดสอบ repository กับ PostgreSQL จริง (3a: sqlc + mapper + `AuditRepository`; 3b: repositories ฝั่ง auth ทั้ง 6 ตัว; 3c: repositories ฝั่งพอร์ตทั้ง 4 ตัว + keyset pagination)
```

4. แทน

```text
- [ ] สร้าง transaction coordination และ audit append/read พร้อม failure injection ที่พิสูจน์ว่า audit/data rollback ด้วยกัน (3a ทำแล้ว: `withinTx` + failure injection ด้วย audit; ยังเหลือการทำซ้ำกับพอร์ตจริงและ `UnitOfWork` ครบใน 3c)
```

   ด้วย

```text
- [x] สร้าง transaction coordination และ audit append/read พร้อม failure injection ที่พิสูจน์ว่า audit/data rollback ด้วยกัน (3a: `withinTx` + failure injection ด้วย audit; 3c: `Store` เป็น `port.UnitOfWork` + failure injection ด้วยพอร์ตจริง + ขายพร้อมกันไม่เกินที่ถือ)
```

5. แทน

```text
3c ยังต้องตัดสินกับ portfolios และ idempotency_keys
```

   ด้วย

```text
**3c:** portfolios ใช้ `ON CONFLICT` ตอน insert; การเปลี่ยนชื่อซ้ำและ idempotency `Save` ที่ชน ยอมให้ transaction จบตาม `port.UnitOfWork`
```

6. แทน

```text
แผน 3c ต้องแปลงการชน `instruments_provider_asset_unique` เป็น error ที่อ่านเข้าใจ (ไม่ใช่ 500) และระบุข้อจำกัดใน README/UI
```

   ด้วย

```text
แผน 3c ต้องแปลงการชน `instruments_provider_asset_unique` เป็น error ที่อ่านเข้าใจ (ไม่ใช่ 500) และระบุข้อจำกัดใน README/UI — **3c:** คืน `unknown_symbol` (ผู้ใช้เลือก 2026-10-21) และระบุใน README แล้ว; UI ระบุตอนทำ frontend
```

7. แทน

```text
ย้ายการแปลงนี้ไปที่ `DeleteOwnedEmpty` แทนการแปลงทั่วไป (INSERT ลงพอร์ตที่ไม่มีอยู่จะได้ 409 ผิดความหมาย)
```

   ด้วย

```text
ย้ายการแปลงนี้ไปที่ `DeleteOwnedEmpty` แทนการแปลงทั่วไป (INSERT ลงพอร์ตที่ไม่มีอยู่จะได้ 409 ผิดความหมาย) — **3c:** ย้ายแล้ว
```

8. แทน

```text
test identity ครอบเฉพาะ `rate_limit_events` ควรเพิ่ม `transactions.sequence`;
```

   ด้วย

```text
test identity ครอบเฉพาะ `rate_limit_events` ควรเพิ่ม `transactions.sequence` (**3c:** `TestTransactionInsertKeepsEveryDigit` insert ผ่าน `app_runtime` และได้ sequence แล้ว);
```

- [ ] **Step 3: แก้ README**

แก้ `README.md` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```text
> **สถานะ:** Milestone 1 — มีเฉพาะ `/healthz`, `/readyz` และเอกสาร API; auth, พอร์ต และรายการซื้อขายยังไม่เปิดใช้งาน
```

   ด้วย

```text
> **สถานะ:** Milestone 3 — schema และ repositories ครบ แต่ API ยังมีเฉพาะ `/healthz`, `/readyz` และเอกสาร API; auth, พอร์ต และรายการซื้อขายยังไม่เปิดใช้งาน
```

2. แทน

```text
## เอกสารออกแบบ
```

   ด้วย

```text
## ข้อจำกัดของ phase 1

- ไม่รองรับ dividends, stock splits, mergers และการเปลี่ยน symbol: พอร์ตที่ได้รับผลจาก corporate actions เหล่านี้จะคำนวณได้ไม่ครบถ้วน
- ถ้า provider เปลี่ยน symbol ของหุ้นที่ระบบบันทึกไว้แล้ว (เช่น FB → META) การบันทึกรายการด้วย symbol ใหม่จะได้ `422 unknown_symbol` พร้อมข้อความว่า symbol ถูกเปลี่ยนชื่อ
- ผลคำนวณเป็นการติดตามพอร์ตด้วย moving weighted-average cost ไม่ใช่รายงานภาษี

## เอกสารออกแบบ
```

- [ ] **Step 4: ตรวจทั้ง repo**

Run: `make check && make test-integration`
Expected: ผ่านทั้งคู่ ไม่มี `SKIP`

- [ ] **Step 5: Commit**

```bash
git add docs README.md
git commit -m "docs: record milestone 3c portfolio repositories and phase 1 limitations"
```
