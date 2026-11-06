# Milestone 8b — Holdings, valuation และ demo PriceProvider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** เพิ่ม `GET /api/v1/portfolios/{portfolio_id}/holdings` และ `GET /api/v1/portfolios/{portfolio_id}/valuation` ให้ผู้ใช้ดูจำนวนหุ้น ต้นทุนเฉลี่ย realized P&L และ (เมื่อมีราคา) market value กับ unrealized P&L ของแต่ละพอร์ต โดยใช้ replay ของ milestone 7 และราคาตัวอย่างที่ติดป้ายว่าเป็น demo; ราคาจริงจาก Alpaca เป็นของแผน 8c

**Architecture:** core ได้ชนิด `PriceQuote`/`QuoteBatch`/`Holding`/`PortfolioValuation`, ฟังก์ชัน pure `BuildHoldings` และ `ValuePortfolio`, port `PriceProvider` (outbound) กับ `ValuationService` (inbound) และ `service.Valuation` ที่อ่าน history ของพอร์ตเป็น snapshot เดียว replay แล้วเรียก provider นอก transaction (ครั้งเดียวแบบ batch เฉพาะ symbol ที่ยังถือ) ถ้าไม่มีราคาผลจะเป็น "ไม่มี" (null) ไม่ใช่ศูนย์ และ realized P&L กับ holdings ยังดูได้เสมอ; adapter ใหม่ `demo.Prices` ใช้ตารางราคาคงที่ใน dev/test ส่วน `PRICE_PROVIDER=alpaca` ใช้ตัวที่ตอบว่าใช้ไม่ได้จนกว่าแผน 8c จะเสร็จ (ไม่ถอยไปใช้ราคา demo); HTTP เพิ่ม handler อ่านอย่างเดียว 2 ตัวที่ใช้ session chain เดิม

**Tech Stack:** Go 1.27 (go.mod `go 1.27.1`), shopspring/decimal, Fiber v3.5.0, pgx v5.11.0, sqlc v1.31.1, swaggo/swag v1.16.6 (Swagger 2.0), golangci-lint v2.14.0 — ไม่มี dependency ใหม่ และไม่มี migration

**Spec:** [phase 1 spec](../specs/2026-10-05-phase-1-design.md) หัวข้อ 8 (`ValuationService`, `PriceProvider`, `QuoteBatch`/`PriceQuote`), 11 (endpoints holdings/valuation, Holding, PortfolioValuation, quote), 12 (replay, ตัวอย่างทดสอบ, snapshot, demo provider) กับ [AGENTS.md](../../../AGENTS.md) หัวข้อ "รายละเอียดข้อมูลราคา", "กติกาพอร์ตและการคำนวณ", "ความแม่นยำของตัวเลข" และ [ADR-0003](../../adr/0003-decimal-and-event-replay.md)

**ลำดับแผน:** 8a (เสร็จ) ตรวจ symbol → **8b (แผนนี้)** → 8c Alpaca PriceProvider จริง (bars SIP, ปฏิทินตลาด, stale policy, cache, retry)

## Global Constraints

- core (`internal/core/**` รวม `_test.go`) import ได้เฉพาะ standard library, `github.com/shopspring/decimal`, `github.com/google/uuid`, `golang.org/x/crypto/argon2` และ `go-backend-stock/internal/core/...`
- ไม่มี migration และไม่มีตารางสำหรับยอดคงเหลือ: Holding เป็นผลคำนวณจาก transactions เท่านั้น (AGENTS.md "ไม่เก็บยอดซ้ำ")
- Endpoints (ใต้ `/api/v1`, ต้องมี session, GET อย่างเดียว ไม่ต้อง CSRF): `GET /portfolios/{portfolio_id}/holdings` → 200 `{data:[Holding]}`; `GET /portfolios/{portfolio_id}/valuation` → 200 PortfolioValuation; พอร์ตของผู้อื่นหรือที่ไม่มี → 404 `not_found` เหมือนกัน และไม่มีการเรียก provider; ทั้งสองตอบ `Cache-Control: no-store` และรองรับ HEAD
- Holding = `{symbol, instrument_name, quantity, cost_basis, average_cost, realized_pnl}` ทุกตัวเลขเป็น decimal string (ไม่มีเลขศูนย์ท้าย, ไม่มี exponent); รวม position ที่ปิดแล้ว (ถือ 0) เพราะต้องเก็บ realized P&L; เรียง `symbol` ตามตัวอักษร; symbol คือ symbol ปัจจุบันของ instrument; ไม่เรียก provider
- PortfolioValuation = `{portfolio_id, currency:"USD", calculated_at, coverage, market_value, unrealized_pnl, realized_pnl, priced_market_value, missing_symbols, positions}`; `coverage` = `complete` (ทุก position ที่ยังถือมีราคา รวมกรณีไม่ถืออะไรเลย) | `partial` | `unavailable` (ยังถืออยู่แต่ไม่มีราคาเลย); ถ้า coverage ไม่ใช่ `complete` → `market_value` และ `unrealized_pnl` เป็น `null` และ `priced_market_value` คือผลรวมเฉพาะ position ที่มีราคา; `realized_pnl` เป็นผลรวมของทุก position เสมอ; `missing_symbols` เรียงตัวอักษร (ว่างเป็น `[]` ไม่ใช่ null)
- Position ใน valuation = Holding + `{quote, market_value, unrealized_pnl, unavailable_reason}`: position ที่ปิดแล้วมี `quote: null`, `market_value: "0"`, `unrealized_pnl: "0"`, `unavailable_reason: null` โดยไม่เรียก provider; position ที่ยังถือและไม่มีราคามี `quote: null`, `market_value: null`, `unrealized_pnl: null` และ `unavailable_reason` = `no_price` (provider ตอบแต่ไม่มีราคา symbol นี้) หรือ `provider_unavailable` (เรียก provider ไม่สำเร็จ)
- quote = `{price, currency, as_of, fetched_at, source, feed, is_demo, stale, cached, session_status}`; `price` decimal string, เวลาเป็น RFC 3339 UTC, `session_status` ∈ `open` | `market_closed` | `unknown`; ห้ามแทนราคาที่ไม่มีด้วยศูนย์เด็ดขาด
- สูตร: `market_value = quantity × price`; `unrealized_pnl = market_value − cost_basis` (ไม่หักค่าธรรมเนียมขายในอนาคต); ใช้ decimal ล้วน ห้าม float; ไม่ปัดผลคูณ/ผลต่าง (ปัดเฉพาะการหาร average cost ตามที่ replay ทำอยู่แล้ว)
- ราคาอ้างอิง = close ของแท่งล่าสุดก่อน cutoff `now − 15 นาที`; service ส่ง cutoff ให้ provider; เรียก provider ครั้งเดียวแบบ batch เฉพาะ symbol ที่ยังถือ ไม่เกิน 10 วินาทีรวม (`context.WithTimeout`)
- provider ล้ม/หมดเวลา → ตอบ 200 พร้อม coverage `unavailable` (หรือ `partial`) และ reason `provider_unavailable`, log ระดับ warn ที่ไม่มี secret; ห้ามถอยไปใช้ราคา demo; context ของคำขอถูกยกเลิก → คืน error ตามปกติ
- demo PriceProvider ใช้ได้เฉพาะ `PRICE_PROVIDER=demo` นอก production และต้องติด `is_demo: true`; ตารางราคาตัวอย่างคงที่: AAPL 200, MSFT 420, NVDA 130, GOOGL 175, AMZN 190, META 520, TSLA 250, SPY 540 (USD, เป็นข้อมูลตัวอย่าง ไม่ใช่ราคาจริง)
- history ของพอร์ตอ่านด้วยคำสั่งเดียว (`TransactionRepository.History`) เป็น snapshot; `calculated_at` คือเวลาที่ได้ snapshot นั้น (`Clock.Now()` UTC) ไม่ใช่เวลาที่ได้ราคา; ไม่ถือ lock ใดๆ และไม่ถือ DB transaction ระหว่างเรียก provider
- ถ้า replay ของ history ที่เก็บไว้ล้ม (ขายเกินที่ถือ) = ข้อมูลเสียหาย → 500 `internal` พร้อม log ที่มี request_id ไม่ใช่ 422
- ห้ามแก้ `.env` และ `.env.example`; Docker: ใช้ได้เฉพาะ `docker compose ps` และ `docker compose config --quiet` (ผู้ใช้อนุญาต rebuild/รัน README แยกต่างหาก)
- **`make -s test-integration` ล้างทุกตาราง schema `app` ใน dev DB หลังรันทุกครั้ง** — ใช้ได้ตามที่ผู้ใช้ตกลงแล้ว
- ต้องเห็น test ล้มก่อนแล้วจึงเขียนโค้ดทุก task; จบ task ด้วย `gofmt -l internal cmd` ที่ไม่พิมพ์อะไร, `go vet ./...` และ `make lint` ผ่าน
- task ที่แก้ annotation ของ handler/DTO ต้องรัน `make swagger` แล้ว commit `docs/api/` ในชุดเดียวกัน; ห้ามใส่ `\"` หรือ backslash ใน `@Param`/`@Header`/`@Failure`/`@Description` (test `TestSwaggerTextHasNoBackslash` จับ)
- commit message ไม่มี `Co-Authored-By` หรือชื่อ Claude; commit ในเครื่องเท่านั้น ห้าม push; `git add` เฉพาะไฟล์ของ task

## การตัดสินใจที่แผนนี้เสนอ (รอผู้ใช้ยืนยันตอนรีวิวแผน)

1. **provider ล้มไม่ใช่ error ของ API:** ตอบ 200 พร้อม `coverage` และ `unavailable_reason` เพราะ AGENTS.md ให้ realized P&L และประวัติดูได้ต่อ ("realized P&L และประวัติรายการยังดูได้")
2. **ก่อนแผน 8c `PRICE_PROVIDER=alpaca` จะได้ราคา "ไม่มี" ทุกตัว** (`provider_unavailable`) ไม่ใช่ราคา demo; production จึงเห็น valuation แบบ `unavailable` ได้จนกว่า 8c จะเสร็จ (holdings ใช้ได้เต็มที่)
3. **`session_status` ของ demo = `unknown`, `stale: false`, `cached: false`** เพราะ demo ไม่รู้ปฏิทินตลาด; นโยบาย stale/ตลาดปิดตามเวลาจริงเป็นของ 8c
4. **ตัวเลขใน holdings/valuation ส่งเต็มความละเอียด** (เช่น average cost 28 ตำแหน่ง) การปัดเป็น 2 ตำแหน่งเป็นหน้าที่ของ UI ตาม AGENTS.md
5. **เพิ่ม `InstrumentRepository.ByIDs`** (query เดียว `id = ANY($1)`) เพื่อเอาชื่อบริษัทมาใส่ Holding แทนการเพิ่ม field `name` ให้ `Transaction`
6. **ไม่มี rate limit ใหม่สำหรับสอง endpoint นี้:** อ่านอย่างเดียว ต้อง login และ provider ถูกเรียกครั้งเดียวต่อคำขอ; ผลกระทบต่อโควตา Alpaca (200 ครั้ง/นาที) ให้ 8c จัดการด้วย cache
7. **ตัวเลือกที่ไม่ทำใน 8b:** ไม่คำนวณ valuation ของทุกพอร์ตรวมกัน, ไม่เก็บ snapshot ของราคา, ไม่แสดงเปอร์เซ็นต์กำไร

## Review Focus

1. **provider ล้ม/ช้า** — 200 พร้อม `coverage: "unavailable"`, `realized_pnl` ครบ, `market_value`/`unrealized_pnl` เป็น null, ไม่มี 5xx, ไม่ใช้ราคา demo — Task 3 `TestValueSurvivesAProviderFailure`
2. **มีราคาแค่บางตัว** — `partial`, totals เป็น null, `priced_market_value` เป็น subtotal ของตัวที่มีราคา, `missing_symbols` เรียงตัวอักษร — Task 2 `TestValuePartiallyPricedPortfolio`
3. **พอร์ตว่าง และ position ที่ขายหมดแล้ว** — `complete`, `"0"` ทุกค่า, ไม่เรียก provider เลย แต่ realized ของตัวที่ปิดแล้วยังนับ — Task 3 `TestEmptyAndClosedPortfoliosNeverCallTheProvider`
4. **พอร์ตของผู้อื่น/ไม่มี** — 404 body เหมือนกัน และ provider ไม่ถูกเรียก — Task 5 `TestHoldingsAndValuationOfAnotherUsersPortfolioLookMissing`
5. **พอร์ตที่ถือหลาย symbol** — เรียก provider ครั้งเดียวด้วย symbol ที่ยังถืออยู่ทั้งหมด (ไม่ใช่ทีละตัว) พร้อม cutoff = `now − 15 นาที` — Task 3 `TestValueAsksTheProviderOnceForEveryOpenSymbol`

---

## File Structure

| ไฟล์ | หน้าที่ | Task |
| --- | --- | --- |
| `internal/core/domain/quote.go`, `quote_test.go` | `PriceQuote`, `QuoteBatch`, `SessionStatus` | 1 |
| `internal/core/domain/valuation.go`, `valuation_test.go` | `Holding`, `BuildHoldings`, `PortfolioValuation`, `ValuePortfolio` (pure) | 2 |
| `internal/core/port/price.go`, `valuation_service.go`, `portfolio.go`; `internal/adapter/postgres/queries/instruments.sql`, `sqlc/`, `instruments.go` (+test); `internal/core/service/valuation.go` (+test) | ports, `InstrumentRepository.ByIDs`, use cases | 3 |
| `internal/adapter/demo/prices.go` (+test), `cmd/api/wire.go` (+test) | demo prices, ตัวเลือก provider | 4 |
| `internal/adapter/http/valuation.go`, `valuation_dto.go`, `server.go`, harness/tests, contract test, `docs/api/`, `cmd/api/main.go` | endpoints | 5 |
| `README.md`, roadmap, spec | คู่มือ, สถานะ | 6 |

---

### Task 1: ชนิดข้อมูลของราคา

**Files:**
- Create: `internal/core/domain/quote.go`, `internal/core/domain/quote_test.go`

**Interfaces:**
- Produces:
  - `type SessionStatus string`; `SessionOpen = "open"`, `SessionMarketClosed = "market_closed"`, `SessionUnknown = "unknown"`; `func ParseSessionStatus(s string) (SessionStatus, error)`
  - `type PriceQuote struct { Symbol Symbol; Price Price; Currency string; AsOf, FetchedAt time.Time; Source, Feed string; IsDemo, Stale, Cached bool; SessionStatus SessionStatus }`
  - `func NewPriceQuote(q PriceQuote) (PriceQuote, error)` — ตรวจค่าที่ adapter สร้าง (ผิด = `invariant`): symbol ไม่ว่าง, `Price` ไม่ใช่ศูนย์, `Currency == "USD"`, `AsOf` และ `FetchedAt` ไม่ใช่ศูนย์และ `AsOf` ไม่หลัง `FetchedAt`, `Source`/`Feed` 1–40 ตัวอักษร printable, `SessionStatus` ถูกต้อง; คืนเวลาเป็น UTC
  - `type QuoteBatch struct { Quotes map[string]PriceQuote; Unavailable []Symbol }` — key ของ `Quotes` คือ `Symbol.String()`

- [ ] **Step 1: เขียน failing tests**
  - `TestNewPriceQuote` — quote ที่ถูกต้อง (price `200`, as_of 14:30 UTC, fetched_at 14:46 UTC, source `demo`, feed `demo`, is_demo true, session `unknown`) ผ่านและเวลาเป็น UTC; แต่ละกรณีนี้ล้มเป็น `internal`: ไม่มี symbol, price ศูนย์, currency `EUR`, as_of หลัง fetched_at, as_of ศูนย์, source ว่าง, session `closed`
  - `TestParseSessionStatus` — สามค่าผ่าน, `OPEN`/`""`/`halted` เป็น `invalid_input`
- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/core/domain/ -count=1` → compile fail
- [ ] **Step 3: เขียนโค้ดตาม Interfaces**
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/core/... -count=1 -race && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(domain): add the price quote types"`

---

### Task 2: Holdings และ valuation แบบ pure function

**Files:**
- Create: `internal/core/domain/valuation.go`, `internal/core/domain/valuation_test.go`

**Interfaces:**
- Consumes: `Position`, `ReplayHistory` (ไม่เรียกในไฟล์นี้), `PriceQuote`, `QuoteBatch`
- Produces:
  - `type Holding struct { InstrumentID ID; Symbol Symbol; InstrumentName string; Quantity, CostBasis, AverageCost, RealizedPnL decimal.Decimal }`
  - `func BuildHoldings(positions map[ID]Position, history []Transaction, names map[ID]string) []Holding` — symbol จาก transaction ล่าสุดของ instrument นั้นใน `history`; เรียง `Symbol` ตามตัวอักษร; instrument ที่ไม่มีชื่อใน `names` ใช้ symbol เป็นชื่อ
  - `type UnavailableReason string`; `ReasonNoPrice = "no_price"`, `ReasonProviderUnavailable = "provider_unavailable"`
  - `type Coverage string`; `CoverageComplete = "complete"`, `CoveragePartial = "partial"`, `CoverageUnavailable = "unavailable"`
  - `type PositionValuation struct { Holding Holding; Quote *PriceQuote; MarketValue, UnrealizedPnL *decimal.Decimal; Reason UnavailableReason }` — `Reason` ว่าง = ไม่มีเหตุผล (ปิดแล้วหรือมีราคา)
  - `type PortfolioValuation struct { PortfolioID ID; CalculatedAt time.Time; Coverage Coverage; MarketValue, UnrealizedPnL *decimal.Decimal; RealizedPnL, PricedMarketValue decimal.Decimal; MissingSymbols []Symbol; Positions []PositionValuation }`
  - `func OpenSymbols(holdings []Holding) []Symbol` — symbol ที่ `Quantity > 0` ตามลำดับของ holdings
  - `func ValuePortfolio(portfolioID ID, at time.Time, holdings []Holding, batch QuoteBatch, failed bool) PortfolioValuation` — `failed` = เรียก provider ไม่สำเร็จทั้งก้อน (ทุก position ที่ถืออยู่ได้ `ReasonProviderUnavailable`); ถ้าไม่ `failed` แต่ไม่มีราคาของ symbol นั้นใน `batch.Quotes` ได้ `ReasonNoPrice`

- [ ] **Step 1: เขียน failing tests**
  - `TestBuildHoldingsSortsAndKeepsClosedPositions` — สอง instrument (MSFT ถือ 3, AAPL ขายหมดแล้วมี realized 8.5): ผลเรียง AAPL ก่อน MSFT, AAPL มี quantity `0` และ realized `8.5`; ชื่อมาจาก `names`
  - `TestValueGoldenExample` (ตัวอย่างใน spec หัวข้อ 12) — ซื้อ 2 × 100 fee 2, ขาย 0.5 × 120 fee 1 (ผ่าน `ReplayHistory` ในตัว test) ราคา 110: quantity `1.5`, cost_basis `151.5`, average_cost `101`, realized `8.5`, market_value `165`, unrealized `13.5`, coverage `complete`, priced_market_value `165`
  - `TestValuePartiallyPricedPortfolio` — AAPL มีราคา 110 (ถือ 1.5, cost 151.5), MSFT ไม่มีราคา, NVDA ไม่มีราคา: coverage `partial`, `MarketValue` และ `UnrealizedPnL` เป็น nil, `PricedMarketValue` = `165`, `MissingSymbols` = [MSFT NVDA], position ของ MSFT มี `Reason` = `no_price` และ quote/market_value/unrealized เป็น nil
  - `TestValueWithNoPricesIsUnavailable` — ถืออยู่สองตัว ไม่มีราคาเลย: coverage `unavailable`, `PricedMarketValue` = `0`, realized ยังเท่ากับผลรวมจริง; ด้วย `failed=true` reason เป็น `provider_unavailable`
  - `TestValueEmptyAndClosedPortfoliosAreCompleteWithZeros` — ไม่มี holding เลย และมีแต่ position ที่ปิดแล้ว: coverage `complete`, `MarketValue` และ `UnrealizedPnL` เป็นตัวชี้ไปที่ `0` (ไม่ใช่ nil), `PricedMarketValue` `0`, position ที่ปิดแล้วมี market_value/unrealized `0` และ quote nil และ reason ว่าง, realized ของตัวที่ปิดแล้วนับรวม
  - `TestValueDoesNotRoundProducts` — ถือ `0.0000000001` ราคา `123.4567890123`: market_value ตรงเลขทศนิยมเต็ม (`0.00000001234567890123`); average_cost `10.0033333333333333333333333333` ถูกส่งต่อโดยไม่ถูกปัดซ้ำ
  - `TestValueLosingPositionHasNegativeUnrealized` — ราคาต่ำกว่าต้นทุน → unrealized ติดลบ
- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/core/domain/ -count=1` → compile fail
- [ ] **Step 3: เขียนโค้ดตาม Interfaces** (ผลคูณ/ผลรวมด้วย decimal ตรงๆ, ไม่มี `Round`)
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/core/... -count=1 -race && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(domain): build holdings and value a portfolio from replayed positions"`

---

### Task 3: ports, `InstrumentRepository.ByIDs` และ `service.Valuation`

**Files:**
- Create: `internal/core/port/price.go`, `internal/core/port/valuation_service.go`, `internal/core/service/valuation.go`, `internal/core/service/valuation_test.go`
- Modify: `internal/core/port/portfolio.go` (`InstrumentRepository.ByIDs`), `internal/adapter/postgres/queries/instruments.sql`, `internal/adapter/postgres/sqlc/` (generated), `internal/adapter/postgres/instruments.go`, `internal/adapter/postgres/instruments_test.go`

**Interfaces:**
- Consumes: `domain.ReplayHistory`, `BuildHoldings`, `ValuePortfolio`, `OpenSymbols`, `QuoteBatch`, `port.Repositories.Transactions()/Instruments()`, `port.Clock`
- Produces:
  - `port.PriceProvider` — `Quotes(ctx context.Context, symbols []domain.Symbol, cutoff time.Time) (domain.QuoteBatch, error)`: คืน error เมื่อเรียก provider ไม่สำเร็จ; symbol ที่ provider ไม่มีราคาอยู่ใน `Unavailable` ไม่ใช่ error
  - `port.ValuationService` — `Holdings(ctx, p domain.Principal, portfolioID domain.ID) ([]domain.Holding, error)`; `Value(ctx, p domain.Principal, portfolioID domain.ID) (domain.PortfolioValuation, error)`
  - `InstrumentRepository.ByIDs(ctx context.Context, ids []domain.ID) (map[domain.ID]domain.Instrument, error)` — id ที่ไม่มีไม่อยู่ใน map ไม่ใช่ error; slice ว่างคืน map ว่างโดยไม่ query
  - SQL: `-- name: GetInstrumentsByIDs :many` → `SELECT … FROM app.instruments WHERE id = ANY(@ids::uuid[]);` (คอลัมน์เดียวกับ `GetInstrumentBySymbol`)
  - `type ValuationDeps struct { Store port.Repositories; Prices port.PriceProvider; Clock port.Clock; Logger *slog.Logger }`; `func NewValuation(d ValuationDeps) (*Valuation, error)` (ขาด dependency = error); `const priceDelay = 15 * time.Minute`, `priceTimeout = 10 * time.Second`; `var _ port.ValuationService = (*Valuation)(nil)`
  - ลำดับใน `Value`: principal ว่าง → `unauthenticated` → `Transactions().History` (ครั้งเดียว; foreign/missing = `not_found`) → `at := Clock.Now()` → `ReplayHistory` (ล้ม = `internal` + log พร้อมไม่มี symbol/จำนวน) → `Instruments().ByIDs` → `BuildHoldings` → ถ้า `OpenSymbols` ว่างข้าม provider → ไม่งั้น `Prices.Quotes(ctxWithTimeout, symbols, at.Add(-priceDelay))` → error ที่ไม่ใช่การยกเลิกของคำขอ = `failed=true` + log warn (`event: "valuation.prices_failed"`, `request_id` ไม่ได้ส่งเข้า service จึงไม่ log) → `ValuePortfolio`; `Holdings` ทำเหมือนกันถึงขั้น `BuildHoldings` และไม่เรียก provider

- [ ] **Step 1: เขียน failing tests**
  - `postgres` `TestInstrumentsByIDs` (integration) — สามแถว: ขอ 2 id ที่มี + 1 ที่ไม่มี → map มี 2; slice ว่าง → map ว่าง; ชื่อ/symbol ตรง
  - `service` (fakes: `fakeTransactions` ที่คืน history ตามที่ตั้ง, `fakeInstruments.ByIDs`, `fakePrices` ที่บันทึก symbols/cutoff/จำนวนครั้ง, `fakeClock`)
    - `TestValueAsksTheProviderOnceForEveryOpenSymbol` — ถือ AAPL, MSFT, NVDA (และ GOOGL ที่ขายหมดแล้ว): provider ถูกเรียก 1 ครั้งด้วย [AAPL MSFT NVDA] (เรียงตามลำดับ holdings) และ cutoff = `Now − 15m` เป๊ะ
    - `TestValueSurvivesAProviderFailure` — provider คืน error: ไม่มี error จาก `Value`, coverage `unavailable`, ทุก position ที่ถือ reason `provider_unavailable`, realized ครบ, log มี `valuation.prices_failed` และไม่มีข้อความ error ของ provider ที่มี secret (error ทดสอบ `errors.New("bad key SECRET123")` ต้องไม่ปรากฏใน log)
    - `TestValueStopsWhenTheRequestIsCancelled` — context ถูกยกเลิก → คืน error ไม่ใช่ coverage
    - `TestEmptyAndClosedPortfoliosNeverCallTheProvider` — history ว่าง และ history ที่ขายหมด: provider ไม่ถูกเรียก, coverage `complete`
    - `TestValueGivesTheProviderAtMostTenSeconds` — provider เห็น context ที่มี deadline ไม่เกิน `Now + 10s`
    - `TestHoldingsNeverCallTheProvider` — `Holdings` ไม่แตะ `fakePrices`
    - `TestValuationOfAnotherUsersPortfolioIsNotFound` — `History` คืน `ErrNotFound`: ผลเป็น `not_found`, provider ไม่ถูกเรียก, ไม่เรียก `ByIDs`
    - `TestCorruptHistoryIsInternal` — history ที่ขายเกิน → `errors.Is(err, domain.ErrInternal)`, log เป็น error, ไม่คืน holdings
    - `TestValuationNeedsACaller` — principal ว่าง → `unauthenticated`, ไม่แตะ repository
- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/core/... -count=1` → compile fail; integration: `make -s test-integration` → compile fail
- [ ] **Step 3: เขียน query, `make sqlc`, `ByIDs`, ports และ service ตาม Interfaces**
- [ ] **Step 4: รัน test ให้ผ่าน** — `make sqlc && git status --short internal/adapter/postgres/sqlc; gofmt -l internal cmd; go vet ./... && go test ./internal/core/... -count=1 -race && make -s test-integration && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(service): value a portfolio from its replayed history and delayed prices"`

---

### Task 4: demo PriceProvider และการเลือก provider

**Files:**
- Create: `internal/adapter/demo/prices.go`, `internal/adapter/demo/prices_test.go`
- Modify: `cmd/api/wire.go`, `cmd/api/wire_test.go`

**Interfaces:**
- Consumes: `port.PriceProvider`, `domain.NewPriceQuote`, `port.Clock`
- Produces:
  - `func NewPrices(clock port.Clock) *Prices`; `(*Prices).Quotes(ctx, symbols []domain.Symbol, cutoff time.Time) (domain.QuoteBatch, error)` — ราคาจากตารางใน Global Constraints; `AsOf = cutoff.UTC().Truncate(time.Minute)`, `FetchedAt = clock.Now()` UTC (ถ้า `FetchedAt` ก่อน `AsOf` ใช้ `AsOf`), `Source`/`Feed` = `demo`, `Currency` USD, `IsDemo` true, `Stale` false, `Cached` false, `SessionStatus` `unknown`; symbol นอกตารางอยู่ใน `Unavailable`; context ที่ถูกยกเลิก → error
  - `cmd/api/wire.go`: `func priceProvider(cfg config.Config, clock port.Clock) (port.PriceProvider, error)` — `demo` นอก production → `demo.NewPrices(clock)`; `alpaca` → `unavailablePrices{}` ที่ `Quotes` คืน `domain.ErrDependencyUnavailable` (แทนที่ด้วย adapter จริงใน 8c); demo ใน production → error เหมือน `instrumentProvider`

- [ ] **Step 1: เขียน failing tests**
  - `TestPricesQuoteTheDemoTable` — ขอ AAPL, MSFT, ZZZZ ที่ cutoff `2026-11-04T14:37:42Z` และ clock 14:55:00Z: AAPL `200`, MSFT `420`, `AsOf` = `14:37:00Z`, `FetchedAt` = `14:55:00Z`, `IsDemo`, `Source` `demo`; ZZZZ ใน `Unavailable`; ทุก quote ผ่าน `domain.NewPriceQuote`
  - `TestPricesNeverGoBackInTime` — clock เก่ากว่า cutoff → `FetchedAt` = `AsOf`
  - `TestPricesStopOnACancelledContext`
  - `TestPriceProviderFollowsPriceProvider` ใน `wire_test.go` — `demo` ได้ราคา demo; `alpaca` ได้ `ErrDependencyUnavailable` จาก `Quotes` (ไม่ใช่ราคา demo); production + demo → error; `PriceProvider` ที่ไม่รู้จัก → error
- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/adapter/demo/ ./cmd/api/ -count=1` → compile fail
- [ ] **Step 3: เขียน `Prices` และ `priceProvider`**
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/adapter/demo/ ./cmd/... -count=1 -race && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(demo): quote sample prices and choose the price provider from PRICE_PROVIDER"`

---

### Task 5: HTTP holdings และ valuation

**Files:**
- Create: `internal/adapter/http/valuation.go`, `internal/adapter/http/valuation_dto.go`, `internal/adapter/http/valuation_test.go`
- Modify: `internal/adapter/http/server.go` (`Options.Valuation`), `internal/adapter/http/auth_harness_test.go` (option `withValuation()` + fake prices), `internal/adapter/http/portfolio_contract_test.go`, `cmd/api/main.go` (tag + description), `cmd/api/wire.go` (`buildValuation`), `cmd/api/wire_test.go`, `docs/api/`

**Interfaces:**
- Consumes: `port.ValuationService`, `getAndHead`, `authChain`, `domain.ParseID`
- Produces:
  - `type ValuationOptions struct { Service port.ValuationService }`; `Options.Valuation ValuationOptions`; `Service` nil = ไม่ลงทะเบียน route; มี `Valuation.Service` แต่ไม่มี `Auth.Service` = panic เหมือน transactions; `func registerValuationRoutes(app *fiber.App, opts ValuationOptions, chain authChain)`
  - DTO: `HoldingResponse{Symbol, InstrumentName, Quantity, CostBasis, AverageCost, RealizedPnL string}`, `HoldingsResponse{Data []HoldingResponse}`, `QuoteResponse{Price, Currency string; AsOf, FetchedAt time.Time; Source, Feed string; IsDemo, Stale, Cached bool; SessionStatus string}`, `PositionResponse{HoldingResponse ฝัง; Quote *QuoteResponse; MarketValue, UnrealizedPnL *string; UnavailableReason *string}`, `ValuationResponse{PortfolioID, Currency string; CalculatedAt time.Time; Coverage string; MarketValue, UnrealizedPnL *string; RealizedPnL, PricedMarketValue string; MissingSymbols []string; Positions []PositionResponse}` — field ที่เป็นตัวชี้ใส่ `extensions:"x-nullable"` และ `json` ไม่มี `omitempty` (ส่ง `null` จริง), `data`/`positions`/`missing_symbols` เป็น `[]` เมื่อว่าง
  - annotations: tag `Holdings/Prices`, `@ID` `listHoldings` และ `getValuation`, `@Failure` 400/401/404/500/503 (`503` = `dependency_unavailable or server_busy` + `@Header 503 Retry-After`), `@Success 200`; ตัวอย่าง (`example`) เป็น decimal string; คำอธิบาย `getValuation` ต้องบอกว่า null = ไม่มีราคา ไม่ใช่ศูนย์ และราคาเป็น delayed bar close ไม่ใช่ราคาเสนอซื้อขาย (AGENTS.md)

- [ ] **Step 1: เขียน failing tests** (harness ใช้ service จริงบน PostgreSQL จริงกับ `fakePrices` ที่ตั้งราคาได้/ตั้งให้ล้มได้/นับการเรียก; ซื้อขายผ่าน endpoint ของ milestone 7)
  - `TestHoldingsListsOpenAndClosedPositions` — ซื้อ AAPL 2 @100 fee 2, ขาย 0.5 @120 fee 1, ซื้อ MSFT 3 @10, ขาย MSFT ทั้งหมด: `data` เรียง AAPL, MSFT; AAPL `quantity "1.5"`, `cost_basis "151.5"`, `average_cost "101"`, `realized_pnl "8.5"`; MSFT `quantity "0"`; `instrument_name` ไม่ว่าง; `Cache-Control: no-store`; HEAD ได้ 200 ไม่มี body; ไม่เรียก fakePrices
  - `TestValuationGoldenExample` — ราคา AAPL 110: `coverage "complete"`, `market_value "165"`, `unrealized_pnl "13.5"`, `realized_pnl "8.5"`, `priced_market_value "165"`, `missing_symbols []`, position AAPL มี quote ครบทุกฟิลด์ (`price "110"`, `is_demo` ตามที่ fake ตั้ง, `as_of` ลงท้าย `Z`) และ `unavailable_reason null`
  - `TestValuationWithoutPricesKeepsRealizedAndSaysUnavailable` — fakePrices ล้ม: 200, `coverage "unavailable"`, `market_value null`, `unrealized_pnl null`, `priced_market_value "0"`, `realized_pnl "8.5"`, position ที่ถือมี `unavailable_reason "provider_unavailable"` และ `quote null`, JSON ดิบมีคำว่า `null` ไม่ใช่ `"0"` ของ market_value; แล้ว holdings ของพอร์ตเดียวกันยังได้ 200
  - `TestValuationOfAnEmptyPortfolio` — พอร์ตใหม่: `complete`, `"0"`, `positions []`, fakePrices ไม่ถูกเรียก
  - `TestHoldingsAndValuationOfAnotherUsersPortfolioLookMissing` — ผู้ใช้อื่นและ UUID สุ่มได้ 404 `not_found` body เหมือนกัน (ยกเว้น request_id), fakePrices ไม่ถูกเรียก, ไม่มี `security.access_denied` (อ่านอย่างเดียว)
  - `TestHoldingsAndValuationNeedASessionAndAValidID` — ไม่มี session 401; `portfolio_id` ไม่ใช่ UUID 400 `invalid_input`
  - `TestValuationSeesOnlyThisPortfoliosTrades` — AAPL ในสองพอร์ตของผู้ใช้เดียวกัน คำนวณแยกกัน
  - contract: `documentedValuationOperations` — get holdings `{200,400,401,404,500,503}`, get valuation `{200,400,401,404,500,503}`; `requiredPortfolioFields` เพิ่ม `http.HoldingResponse`, `http.HoldingsResponse`, `http.ValuationResponse`, `http.PositionResponse` พร้อม `calculated_at`/`as_of`/`fetched_at` เป็น `date-time` และ `market_value`/`unrealized_pnl` ของ `ValuationResponse` ไม่อยู่ใน `required` แต่มี `x-nullable`
- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/adapter/http/ -count=1` → compile fail
- [ ] **Step 3: เขียน handlers, DTO, annotations, `buildValuation` และต่อเข้า `main.go`** ส่ง `c.Context()` ให้ service, clone ค่าที่ต้องใช้ออกจาก Fiber ctx; `make swagger` แล้ว commit `docs/api/`; เพิ่ม `TestBuildServesTheValuationRoutes` (GET โดยไม่มี session ได้ 401 ไม่ใช่ 404)
- [ ] **Step 4: รัน test ให้ผ่าน** — `make swagger && gofmt -l internal cmd; go vet ./... && make -s test-integration && go test ./... -race -count=1 && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(http): list holdings and value a portfolio with delayed prices"`

---

### Task 6: คู่มือและสถานะ

**Files:**
- Modify: `README.md`, `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md`, `docs/superpowers/specs/2026-10-05-phase-1-design.md`

- [ ] **Step 1: README** — ต่อ curl flow หลังขั้น 21 บนพอร์ต "ระยะสั้น" (ซื้อ AAPL ใหม่ก่อนเพราะขั้น 21 ลบรายการ): ขั้น 22 `GET .../holdings` (ได้ 200, จำนวน/ต้นทุน), ขั้น 23 `GET .../valuation` (ได้ 200, `coverage: complete`, ราคา demo 200 สำหรับ AAPL พร้อม `is_demo: true`); อธิบาย: ราคาที่เห็นเป็นข้อมูลตัวอย่าง (`PRICE_PROVIDER=demo`), `null` ของ market_value/unrealized_pnl แปลว่าไม่มีราคา (ไม่ใช่ศูนย์), `coverage`, `unavailable_reason`, ราคาจริงกับนโยบาย stale/ตลาดปิดมาใน 8c, `PRICE_PROVIDER=alpaca` ตอนนี้ได้ valuation แบบ `unavailable` จน 8c เสร็จ, ผลคำนวณเป็นการติดตามพอร์ตด้วย moving weighted-average ไม่ใช่รายงานภาษี และไม่รวม dividends/splits; บล็อกต้องยังเป็น bash block เดียวที่รันต่อจากบนลงล่างได้ (ผู้ลงมือเช็กด้วย `bash -n`)
- [ ] **Step 2: roadmap** — milestone 8: ติ๊ก "ใช้ replay จาก milestone 7 สรุป holdings/cost/realized P&L …" และส่วน demo PriceProvider ของข้อ "เพิ่ม demo PriceProvider …" (แยกบรรทัดให้เหลือ Alpaca adapter ของ 8c), ระบุสถานะ 8b "ลงมือครบแล้ว รอตรวจรับ" ทั้งใน Status paragraph และหัวข้อ milestone; ไม่ติ๊กข้อที่เป็นของ 8c (batch/cache/retry, stale/ตลาดปิด) และไม่เขียนว่า CI ผ่านถ้ายังไม่รู้
- [ ] **Step 3: spec** — หัวข้อ 8: `PriceProvider.Quotes(context.Context, []domain.Symbol, time.Time)` และ `Holding`/`PositionValuation` ตามที่สร้างจริง; หัวข้อ 11: เพิ่ม `unavailable_reason` ใน Position และกติกา null/`"0"` ของ valuation; หัวข้อ 12: demo prices ตารางคงที่และ `PRICE_PROVIDER=alpaca` ก่อน 8c
- [ ] **Step 4: รันทั้งหมด** — `make -s test-integration && go test ./... -race -count=1 && go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...` (อนุญาตเฉพาะ GO-2026-6809, GO-2026-6629)
- [ ] **Step 5: Commit** — `git commit -m "docs: document holdings and valuation"`
- [ ] **Step 6: รัน `make check` หลัง commit** — Expected: exit 0
