# มูลค่าพอร์ตรายวันและผลตอบแทนเทียบ SPY/QQQ (ขั้น 3-4) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** เปิด `GET /api/v1/portfolios/{portfolio_id}/performance` และ `GET /api/v1/performance` ที่ตอบมูลค่าพอร์ตรายวันกับผลตอบแทนแบบ time-weighted เทียบ SPY/QQQ

**Architecture:** pure function ใน domain คำนวณจุดรายวันจากรายการซื้อขายกับแท่งราคาที่เก็บใน `price_bars` service ใหม่ `Performance` อ่านประวัติรายการ เติมแท่งที่ขาดด้วยกลไกเดียวกับ `bars` (นอก DB transaction) แล้วเรียก function นั้น ไม่มีตารางหรือ migration ใหม่ และไม่เก็บผลคำนวณ

**Tech Stack:** Go, Fiber v3, pgx + sqlc, PostgreSQL, shopspring/decimal, swaggo (Swagger 2.0)

**Spec:** [ADR-0025](../../adr/0025-portfolio-performance-time-weighted.md) (สถานะ Proposed ต้องได้รับการยืนยันก่อนเริ่ม Task 1) และข้อเสนอจาก frontend `next-frontend-stock/docs/backend-proposal-performance-and-indicators.md` ข้อ H

**สถานะ:** รอผู้ใช้ตรวจแผน (2026-11-12) ยังไม่ลงมือ

**นอกขอบเขต:** ข้อ I (RSI) และข้อ J (แนวรับ/แนวต้าน) ของข้อเสนอ ต้องมี ADR กับแผนแยกหลังเจ้าของโปรเจกต์ยืนยันว่าเป็นงานของเฟสถัดไป; การตรวจจับ split; จุดรายสัปดาห์; งาน frontend

## Global Constraints

- เงิน ราคา จำนวนหุ้น และเปอร์เซ็นต์ใช้ `shopspring/decimal` เท่านั้น ห้ามผ่าน float; การหารใช้ `divideHalfEven` (28 ตำแหน่ง half-even) ที่มีใน `internal/core/domain/position.go`
- API ส่งตัวเลขเป็น decimal string ไม่ปัด; `_percent` เป็นหน่วยเปอร์เซ็นต์ (`"8.5"` คือ 8.5%); ค่าที่ไม่รู้เป็น `null` ไม่ใช่ `"0"`
- core ไม่ import Fiber, pgx, sqlc; SQL เขียนใน `internal/adapter/postgres/queries/` แล้ว `make sqlc`
- ห้ามถือ DB transaction หรือ lock ระหว่างเรียก provider; ห้ามสลับไปข้อมูลเดโมเมื่อ provider ล้มเหลว
- อ่านและเขียนเฉพาะแถว `price_bars` ของ `source` ที่ตั้งไว้ (`alpaca` หรือ `demo`)
- จุดสุดท้ายคือวันก่อนวันปัจจุบันตามเวลา New York (`domain.MarketDate(now).AddDays(-1)`)
- ทุก query ที่อ่านรายการกรองด้วย `user_id` ของ principal; พอร์ตของผู้อื่นได้ 404 เหมือนพอร์ตที่ไม่มี
- endpoint ใหม่เป็นการเพิ่มใน `/api/v1` (ADR-0011); handler ใหม่ต้องมี Swagger annotations แล้ว `make swagger` ในงานเดียวกัน ห้ามแก้ไฟล์ใน `docs/api/` ด้วยมือ
- ไม่ log secrets หรือข้อความ error ดิบของ provider (ใช้ `errorClass` ใน `service/valuation.go`)
- ข้อความในเอกสารเป็นภาษาไทย โค้ดและ comment เป็นภาษาอังกฤษ

## Review Focus

1. รายการที่ลงวันที่เสาร์อาทิตย์หรือวันหยุด: ต้องนับรวมกับวันถัดไปที่มีมูลค่า ไม่หาย (test ใน Task 2)
2. หุ้นที่ถืออยู่ไม่มีแท่งบางวัน: จุดเป็น `null` และวันถัดไปคิดผลตอบแทนข้ามช่วงโดยรวมเงินเข้าออก ไม่ใช่ `"0"` (test ใน Task 2)
3. ขายหมดแล้วซื้อใหม่: เส้นราบ ไม่หารด้วย 0 และคูณต่อจากค่าเดิม (test ใน Task 2)
4. provider ล่มกลางการเติม: ส่วนที่เติมแล้วถูกเก็บ ตอบ `partial` เมื่อทุกตัวมีข้อมูล และ 503 เมื่อมีตัวที่ไม่มีเลย (test ใน Task 5)
5. หุ้นตัวเดียวกันในสองพอร์ตที่ endpoint ทั้งบัญชี: จำนวนรวมกันและพอร์ตของผู้ใช้อื่นไม่ถูกนับ (test ใน Task 6)

## File Structure

| ไฟล์ | หน้าที่ |
| --- | --- |
| `internal/core/domain/performance.go` (ใหม่) | `PerformanceRange`, `Benchmark`, `Performance`, `ComputePerformance`, `InstrumentsHeldSince` |
| `internal/core/domain/trading_date.go` (แก้) | เพิ่ม `AddMonths`, `StartOfYear` |
| `internal/core/port/price_bars.go` (แก้) | เพิ่ม `Coverages`, `RangeMany` |
| `internal/core/port/performance_service.go` (ใหม่) | inbound port `PerformanceService` |
| `internal/adapter/postgres/queries/price_bars.sql`, `price_bars.go` (แก้) | query และ method ใหม่สองตัว |
| `internal/core/service/bar_filler.go` (ใหม่) | เติมแท่งที่ขาดของหลาย instrument; `PriceHistory.fetch` ย้ายมาใช้ร่วม |
| `internal/core/service/performance.go` (ใหม่) | service `Performance` |
| `internal/adapter/http/performance.go`, `performance_dto.go` (ใหม่) | handlers, DTO, Swagger annotations |
| `cmd/api/wire.go`, `main.go`, `internal/adapter/http/server.go` (แก้) | wiring |

---

### Task 1: ช่วงเวลาและ benchmark ใน domain

**Files:**
- Modify: `internal/core/domain/trading_date.go`
- Create: `internal/core/domain/performance.go`
- Test: `internal/core/domain/trading_date_test.go`, `internal/core/domain/performance_test.go`

**Interfaces:**
- Produces:
  - `func (d TradingDate) AddMonths(n int) TradingDate` (วันที่เกินสิ้นเดือนถูกตัดเป็นวันสุดท้ายของเดือน: 31 มี.ค. − 1 เดือน = 28/29 ก.พ.)
  - `func (d TradingDate) StartOfYear() TradingDate`
  - `type PerformanceRange string` ค่าคงที่ `PerformanceRange1M "1m"`, `3M`, `6M`, `YTD "ytd"`, `1Y`, `5Y`, `All "all"`
  - `func ParsePerformanceRange(s string) (PerformanceRange, error)` (field `range`, รับตรงตัวพิมพ์เล็กเท่านั้น)
  - `func (r PerformanceRange) Start(through, firstTrade TradingDate) TradingDate` คืนวันเริ่มของช่วงที่จบที่ `through`: `1m`/`3m`/`6m` = `through.AddMonths(-n)`, `1y`/`5y` = `AddMonths(-12)`/`AddMonths(-60)`, `ytd` = `through.StartOfYear()`, `all` = `firstTrade` (zero ได้ `through`); ผลทุกกรณีไม่เก่ากว่า `through.AddDays(-(MaxBarRangeDays-1))`
  - `type Benchmark struct { Symbol Symbol; Label string }`
  - `func ParseBenchmarks(s string) ([]Benchmark, error)` (field `benchmarks`; `""` คืน slice ว่าง; รับ `SPY` label `S&P 500 (SPY)` และ `QQQ` label `Nasdaq-100 (QQQ)`; ค่าอื่น ค่าซ้ำ หรือช่องว่างเป็น `invalid_input`; คงลำดับที่ขอ)

- [ ] **Step 1: เขียน test ที่ยังไม่ผ่าน**

```go
func TestAddMonthsClampsToTheEndOfTheMonth(t *testing.T) {
	got := NewTradingDate(2026, 3, 31).AddMonths(-1)
	if got.String() != "2026-02-28" { t.Fatalf("got %s", got) }
	if NewTradingDate(2024, 3, 31).AddMonths(-1).String() != "2024-02-29" { t.Fatal("leap year") }
	if NewTradingDate(2026, 11, 10).AddMonths(-12).String() != "2025-11-10" { t.Fatal("one year") }
}

func TestPerformanceRangeStart(t *testing.T) {
	through, first := NewTradingDate(2026, 11, 10), NewTradingDate(2020, 5, 4)
	cases := map[string]string{"1m": "2026-10-10", "3m": "2026-08-10", "6m": "2026-05-10",
		"ytd": "2026-01-01", "1y": "2025-11-10", "5y": "2021-11-10", "all": "2020-05-04"}
	// each: ParsePerformanceRange(k) then Start(through, first).String() == v
	// all with firstTrade 2010-01-04 == through.AddDays(-3659).String()
	// all with zero firstTrade == "2026-11-10"
}

func TestParsePerformanceRangeRefusesAnythingElse(t *testing.T) // "", "1M", "2y", " 1y" -> CodeInvalidInput

func TestParseBenchmarks(t *testing.T) {
	// ""        -> len 0, no error
	// "SPY,QQQ" -> [{SPY "S&P 500 (SPY)"} {QQQ "Nasdaq-100 (QQQ)"}]
	// "QQQ"     -> [{QQQ ...}]
	// "spy", "SPY,SPY", "SPY, QQQ", "AAPL", "SPY," -> CodeInvalidInput
}
```

- [ ] **Step 2: รันให้เห็นว่าไม่ผ่าน**

Run: `go test ./internal/core/domain/ -run 'AddMonths|PerformanceRange|ParseBenchmarks'`
Expected: FAIL (compile error: undefined AddMonths, ParsePerformanceRange, ParseBenchmarks)

- [ ] **Step 3: เขียนโค้ดตาม Interfaces** ใช้ `parseEnum` และ `invalidInput` ที่มีอยู่

- [ ] **Step 4: รันให้ผ่าน**

Run: `go test ./internal/core/domain/`
Expected: ok

- [ ] **Step 5: Commit** `feat(domain): performance ranges and benchmarks`

---

### Task 2: คำนวณจุดรายวัน (`ComputePerformance`)

**Files:**
- Modify: `internal/core/domain/performance.go`
- Test: `internal/core/domain/performance_test.go`

**Interfaces:**
- Consumes: `Benchmark` (Task 1), `Transaction`, `DailyBar`, `Coverage`, `divideHalfEven`, `MarketDate`
- Produces:

```go
type PerformancePoint struct {
	Date          TradingDate
	MarketValue   *decimal.Decimal // nil: a stock still held has no bar that day
	ReturnPercent *decimal.Decimal // cumulative since From, in percent; nil with MarketValue
}
type BenchmarkPoint struct {
	Date          TradingDate
	ReturnPercent *decimal.Decimal // nil: no bar that day, or none on From
}
type BenchmarkPerformance struct {
	Benchmark
	ReturnPercent       *decimal.Decimal
	ExcessReturnPercent *decimal.Decimal
	Points              []BenchmarkPoint
}
type Performance struct {
	From, To      TradingDate // zero when there is no point
	Coverage      Coverage
	ReturnPercent *decimal.Decimal
	Points        []PerformancePoint       // never nil
	Benchmarks    []BenchmarkPerformance   // never nil, in the order asked
}
type BenchmarkBars struct { Benchmark Benchmark; Bars []DailyBar }
type PerformanceInput struct {
	Trades         []Transaction        // any portfolios, any order
	Bars           map[ID][]DailyBar    // by instrument, oldest first
	Start, Through TradingDate
	Benchmarks     []BenchmarkBars
}
func ComputePerformance(in PerformanceInput) Performance
// InstrumentsHeldSince lists, in order of first appearance, the instruments held at the end of the day
// before from or bought on from or later: the ones whose bars a performance from that date needs.
func InstrumentsHeldSince(trades []Transaction, from TradingDate) []ID
```

อัลกอริทึม (ADR-0025):

```text
sort trades by (ExecutedAt, Sequence); drop trades with MarketDate(ExecutedAt) > Through
dates = sorted union of bar dates within Start..Through over every list in Bars and Benchmarks
qty = map[instrument]decimal; in, out = 0; base not set
for each d in dates:
    for each unapplied trade with MarketDate(ExecutedAt) <= d:
        buy:  qty += q; in  += q*price + fee
        sell: qty -= q; out += q*price - fee
    V = sum(qty * close(d)) over instruments with qty > 0; unknown when one of them has no bar on d
    no base yet:
        V known and V > 0 -> base: From = d, point{d, V, 0}, factor = 1, prev = V, in = out = 0
        otherwise         -> no point; in = out = 0
    V unknown             -> point{d, nil, nil}            (in and out keep adding up)
    otherwise:
        denom = prev + in
        if denom > 0: factor = (factor * divideHalfEven(V + out, denom)).RoundBank(28)
        point{d, V, (factor - 1) * 100}; prev = V; in = out = 0
To = date of the last point
ReturnPercent = return of the last point that has one
Coverage = with points: complete when none is nil, partial otherwise (the base point is never nil)
           without points: unavailable when InstrumentsHeldSince(trades, Start) is not empty
           (something was held but never valued), complete otherwise
benchmark: baseClose = its close on From; point per portfolio point date:
           (close(d) / baseClose - 1) * 100 with divideHalfEven, nil without a bar on d or on From
           ReturnPercent = its return on the date of the last portfolio point that has a return
           ExcessReturnPercent = portfolio ReturnPercent - benchmark ReturnPercent, nil when either is
```

- [ ] **Step 1: เขียน test ที่ยังไม่ผ่าน** ใช้ helper `perfBar(date, close)` (open/high/low = close, volume 0) และ `perfTrade(instrument, side, qty, price, fee, executedAt)` วันที่ D1..D5 = 2026-11-02..06 (จันทร์ถึงศุกร์) รายการลงเวลา 15:00Z ของวันนั้น ค่าที่ตรวจเทียบด้วย `decimal.Equal`

```go
func TestPerformanceIgnoresBuysAndSellsAsReturn(t *testing.T) {
	// AAPL close: D1 100, D2 110, D3 110, D4 121, D5 121.  SPY close: 500, 505, 510, 520, 525
	// D1 buy 10 @ 98 fee 1; D3 buy 10 @ 110 fee 0; D4 sell 5 @ 121 fee 0
	// market_value:   1000, 1100, 2200, 1815, 1815
	// return_percent: 0, 10, 10, 21, 21
	// From D1, To D5, ReturnPercent 21, Coverage complete
	// SPY points: 0, 1, 2, 4, 5; ReturnPercent 5; ExcessReturnPercent 16
}
func TestPerformanceCountsFeesAndTheTradePrice(t *testing.T) {
	// AAPL close 100 on D1..D3; D1 buy 10 @ 100; D2 sell 5 @ 100 fee 50 -> D2 return -5
	// D3 buy 10 @ 60 fee 0: V = 1500, denom = 500 + 600 -> D3 factor 0.95 * 1500/1100
	//   assert D3 return equals (0.95 * divideHalfEven(1500, 1100)).RoundBank(28) - 1, times 100
}
func TestPerformanceStaysFlatWhileNothingIsHeld(t *testing.T) {
	// AAPL close: D1 100, D2 110, D3 110, D4 55; D1 buy 10 @ 100; D2 sell 10 @ 110; D4 buy 10 @ 50
	// market_value 1000, 0, 0, 550; return_percent 0, 10, 10, 21
}
func TestPerformanceStartsOnTheFirstDayWithValue(t *testing.T) {
	// Start D1; SPY has bars D1..D5; AAPL D1..D5 at 100, 100, 100, 110, 110; first buy D3 10 @ 90 fee 5
	// points D3, D4, D5 only; From D3; returns 0, 10, 10; SPY returns measured from its D3 close
}
func TestPerformanceLeavesAGapWhenAHeldStockHasNoBar(t *testing.T) {
	// AAPL bars D1 100, D2 110, D4 121 (none on D3); SPY has D1..D4; D1 buy 10 @ 100; D3 buy 10 @ 110
	// D3 point nil/nil; D4: V = 2420, denom = 1100 + 1100 -> return 21; Coverage partial
}
func TestPerformanceCountsAWeekendTradeOnTheNextTradingDay(t *testing.T) {
	// AAPL close Fri 2026-11-06 100, Mon 2026-11-09 100; buy 10 @ 100 on Fri; buy 10 @ 100 on Sat 2026-11-07
	// Mon: V 2000, return 0
}
func TestPerformanceOfSeveralPortfoliosAddsTheShares(t *testing.T) {
	// two trades of AAPL with different PortfolioID, 10 shares each on D1 @ 100 -> D1 market_value 2000
}
func TestPerformanceWithoutTradesIsEmptyAndComplete(t *testing.T) {
	// no trades, SPY bars D1..D5 -> Points len 0 (not nil), From and To zero, ReturnPercent nil,
	// Coverage complete, SPY Points len 0, SPY ReturnPercent nil
}
func TestPerformanceIsUnavailableWhenNoHeldStockHasABar(t *testing.T) {
	// D1 buy 10 AAPL; AAPL has no bar at all, SPY has D1..D5 -> Points len 0, Coverage unavailable
}
func TestPerformanceIgnoresTradesAfterThrough(t *testing.T)  // a buy dated Through+1 changes nothing
func TestInstrumentsHeldSince(t *testing.T) {
	// A bought and sold out before from -> left out; B held across from -> in; C first bought after from -> in
}
```

- [ ] **Step 2: รันให้เห็นว่าไม่ผ่าน**

Run: `go test ./internal/core/domain/ -run 'Performance|InstrumentsHeldSince'`
Expected: FAIL (undefined ComputePerformance)

- [ ] **Step 3: เขียน `ComputePerformance` และ `InstrumentsHeldSince`** ตามอัลกอริทึมข้างบน

- [ ] **Step 4: รันให้ผ่าน**

Run: `go test -race ./internal/core/domain/`
Expected: ok

- [ ] **Step 5: Commit** `feat(domain): daily portfolio value and time-weighted return`

---

### Task 3: อ่าน coverage และแท่งของหลาย instrument

**Files:**
- Modify: `internal/core/port/price_bars.go`, `internal/adapter/postgres/queries/price_bars.sql`, `internal/adapter/postgres/price_bars.go`, `internal/adapter/postgres/sqlc/*` (generate)
- Test: `internal/adapter/postgres/price_bars_test.go`
- Modify (fake): `internal/core/service/price_history_test.go` (`fakeBarRepo`)

**Interfaces:**
- Produces (เพิ่มใน `port.PriceBarRepository`):

```go
// Coverages returns the coverage of each instrument that has one for the source, keyed by instrument.
// An instrument never asked about is left out. An empty list returns an empty map without querying.
Coverages(ctx context.Context, instrumentIDs []domain.ID, source domain.BarSource) (map[domain.ID]domain.BarCoverage, error)
// RangeMany returns the stored bars of the instruments with from <= date <= to, keyed by instrument,
// each list oldest first. An instrument with no bar in the range is left out.
RangeMany(ctx context.Context, instrumentIDs []domain.ID, source domain.BarSource, from, to domain.TradingDate) (map[domain.ID][]domain.DailyBar, error)
```

- sqlc queries: `ListPriceBarCoverages :many` (`instrument_id = ANY(@instrument_ids::uuid[]) AND source = @source`) และ `ListPriceBarsOfInstruments :many` (เงื่อนไขเดียวกันบวก `bar_date BETWEEN @from_date AND @to_date ORDER BY instrument_id, bar_date`)

- [ ] **Step 1: เขียน integration test ที่ยังไม่ผ่าน**

```go
func TestPriceBarsCoveragesAndRangeManyKeepInstrumentsAndSourcesApart(t *testing.T) {
	// two instruments A and B; Save A alpaca D1..D3 (3 bars), B alpaca D2..D3 (2 bars), A demo D1 (1 bar)
	// Coverages([A, B, C], alpaca) -> A {D1, D3}, B {D2, D3}, no key for C
	// RangeMany([A, B], alpaca, D2, D3) -> A 2 bars oldest first, B 2 bars
	// RangeMany([A], demo, D1, D3) -> A 1 bar
	// Coverages(nil, alpaca) and RangeMany(nil, ...) -> empty map, no error
}
```

- [ ] **Step 2: รันให้เห็นว่าไม่ผ่าน**

Run: `make test-integration`
Expected: FAIL (compile error: Coverages undefined)

- [ ] **Step 3: เพิ่ม queries, `make sqlc`, เขียน method ทั้งสอง** ใช้ `dailyBarFromRow`, `pgDate`, `tradingDateFromPG`, `checkBarKey` ที่มีอยู่; เพิ่ม method ทั้งสองใน `fakeBarRepo` ของ service tests ให้ compile

- [ ] **Step 4: รันให้ผ่าน**

Run: `make test-integration && make sqlc-clean`
Expected: ทุกแพ็กเกจ ok; `sqlc-clean` ไม่มี diff หลัง `git add` ไฟล์ที่ generate

- [ ] **Step 5: Commit** `feat(postgres): read coverage and bars of several instruments`

---

### Task 4: `barFiller` เติมแท่งของหลาย instrument

**Files:**
- Create: `internal/core/service/bar_filler.go`
- Modify: `internal/core/service/price_history.go` (ให้ `fetch` เรียก `barFiller`)
- Test: `internal/core/service/bar_filler_test.go`

**Interfaces:**
- Consumes: `PriceBarRepository.Coverages` (Task 3), `domain.MissingRanges`, `PriceProvider.DailyBars`
- Produces:

```go
const barFillChunk = 10 // symbols per provider request

type barFiller struct {
	store  port.Repositories
	prices port.PriceProvider
	source domain.BarSource
	clock  port.Clock
}
// fetchRange asks the provider for r of all instruments in one request and saves each instrument's bars
// with r as the fetched range. providerErr: the provider failed. err: the answer cannot be trusted
// (another source, a bar outside r) or could not be stored.
func (f barFiller) fetchRange(ctx context.Context, instruments []domain.Instrument, r domain.DateRange) (providerErr, err error)
// fill makes the coverage of every instrument include r where the provider allows: it reads the
// coverages, groups the instruments by the range each is missing (domain.MissingRanges), and calls
// fetchRange for at most barFillChunk instruments at a time. It stops at the first providerErr or err;
// what was saved before stays.
func (f barFiller) fill(ctx context.Context, instruments []domain.Instrument, r domain.DateRange) (providerErr, err error)
```

`fetchRange` ไม่ตั้ง timeout เอง ผู้เรียกส่ง ctx ที่มี deadline; `PriceHistory.fetch` คง `priceTimeout` เดิมแล้วเรียก `fetchRange` ด้วย instrument เดียว

- [ ] **Step 1: เขียน test ที่ยังไม่ผ่าน** ใช้ `fakeBarRepo`, `fakeBarsProvider` ที่มีใน `price_history_test.go` (เพิ่มการจดรายชื่อ symbol ต่อคำขอใน `fakeBarsProvider`)

```go
func TestFillAsksOncePerMissingRangeForAllSymbolsThatShareIt(t *testing.T) {
	// A and B never asked, C covered D1..D3; fill([A, B, C], D1..D5)
	// provider calls: {[A, B], D1..D5} and {[C], D4..D5}; coverage of all three ends D1..D5
}
func TestFillAsksInChunksOfTenSymbols(t *testing.T) // 23 instruments, none covered -> 3 calls of 10, 10, 3
func TestFillAsksNothingWhenEverythingIsCovered(t *testing.T) // 0 provider calls, nil, nil
func TestFillKeepsWhatWasSavedBeforeTheProviderFailed(t *testing.T) {
	// 12 instruments; provider fails on its 2nd call -> providerErr != nil, err == nil,
	// first 10 have coverage, last 2 have none
}
func TestFillRefusesAnAnswerFromAnotherSource(t *testing.T) // batch.Source demo, filler alpaca -> err wraps ErrInternal
```

- [ ] **Step 2: รันให้เห็นว่าไม่ผ่าน**

Run: `go test ./internal/core/service/ -run Fill`
Expected: FAIL (undefined barFiller)

- [ ] **Step 3: เขียน `barFiller` แล้วให้ `PriceHistory` ใช้** (`PriceHistory` เก็บ `filler barFiller` แทน field `prices`)

- [ ] **Step 4: รันให้ผ่าน รวม tests เดิมของ `PriceHistory` โดยไม่แก้ assertion**

Run: `go test -race ./internal/core/service/`
Expected: ok

- [ ] **Step 5: Commit** `refactor(service): share the daily bar filler`

---

### Task 5: service `Performance`

**Files:**
- Create: `internal/core/port/performance_service.go`, `internal/core/service/performance.go`
- Test: `internal/core/service/performance_test.go`

**Interfaces:**
- Consumes: `ComputePerformance`, `InstrumentsHeldSince`, `PerformanceRange.Start` (Task 1-2), `RangeMany`, `Coverages` (Task 3), `barFiller.fill` (Task 4), `instrumentVerifier`, `TransactionRepository.History`/`OwnerHistory`, `PortfolioRepository.ListAllOwned`, `InstrumentRepository.ByIDs`/`BySymbol`/`Upsert`
- Produces:

```go
// port
type PerformanceQuery struct {
	Range      domain.PerformanceRange
	Benchmarks []domain.Benchmark
}
type PerformanceReport struct {
	domain.Performance
	Source      domain.BarSource
	DataThrough domain.TradingDate // zero when nothing was needed or nothing is covered
}
type PerformanceService interface {
	Portfolio(ctx context.Context, p domain.Principal, portfolioID domain.ID, q PerformanceQuery, meta domain.RequestMeta) (PerformanceReport, error)
	Account(ctx context.Context, p domain.Principal, q PerformanceQuery, meta domain.RequestMeta) (PerformanceReport, error)
}
// service
type PerformanceDeps struct { // every field required, same shape as PriceHistoryDeps
	Store port.Repositories; UnitOfWork port.UnitOfWork; Prices port.PriceProvider
	Instruments port.InstrumentProvider; Source domain.BarSource; Clock port.Clock
	Limits *RateLimitPolicy; Logger *slog.Logger
}
func NewPerformance(d PerformanceDeps) (*Performance, error)
```

ลำดับงานของทั้งสอง method:

1. principal ว่าง คืน `domain.ErrUnauthenticated`
2. อ่านรายการ: `Portfolio` ใช้เส้นทางเดียวกับ `Valuation.snapshot` (`Transactions().History` ซึ่งพอร์ตของผู้อื่นได้ `ErrNotFound`); `Account` ใช้ `ListAllOwned` + `OwnerHistory` แล้วตัดรายการของพอร์ตที่ไม่อยู่ในรายการพอร์ต แบบเดียวกับ `Valuation.ValueAll`
3. `through` = `MarketDate(now).AddDays(-1)`; `start` = `q.Range.Start(through, วันที่ New York ของรายการแรก)`
4. instrument ที่ต้องใช้ = `InstrumentsHeldSince(trades, start)` (อ่านด้วย `ByIDs`) บวก benchmark แต่ละตัว (`BySymbol`; ถ้าไม่มีให้ `verifier.verify` แล้ว `Upsert` เหมือน `PriceHistory.instrumentFor`)
5. เติมแท่ง: `filler.fill(fillCtx, instruments, {start, through})` โดย `fillCtx` มี timeout = ค่าน้อยของ 8 วินาที กับ เวลาที่เหลือถึง deadline ของคำขอลบ 2 วินาที; ไม่มีเวลาเหลือให้ข้ามการเติมและถือเป็น `providerErr`
   - `err` คืนทันที; คำขอที่ถูกยกเลิก (`ctx.Err() == context.Canceled`) คืน `ctx.Err()`
   - `providerErr`: log ระดับ warn ด้วย `errorClass` (event `performance.prices_failed`) แล้วทำต่อ
6. อ่าน `Coverages` ของ instrument ที่ต้องใช้: ถ้า `providerErr != nil` และมีตัวใดไม่มี coverage คืน `fmt.Errorf("%w: %w", domain.ErrDependencyUnavailable, providerErr)`; `DataThrough` = ค่าน้อยสุดของ `Through` ทุกตัว (ไม่เกิน `through`)
7. `RangeMany(ids, source, start, through)` แล้ว `ComputePerformance`; ถ้า `providerErr != nil` และ `Coverage` เป็น `complete` ให้เปลี่ยนเป็น `partial`
8. ไม่มี instrument ที่ต้องใช้เลย (ไม่มีรายการและไม่ขอ benchmark): ไม่เรียก provider, คืนผลว่าง `complete`

- [ ] **Step 1: เขียน test ที่ยังไม่ผ่าน** สร้าง harness `newTestPerformance(t)` ตามแบบ `newTestHistory` (นาฬิกา `historyNow` = พุธ 2026-11-11 10:00 New York, `through` = 2026-11-10)

```go
func TestPerformanceNeedsAPrincipal(t *testing.T)                 // ErrUnauthenticated, no repository call
func TestPerformanceOfAnotherUsersPortfolioIsNotFound(t *testing.T) // ErrNotFound, provider never asked
func TestPerformanceFillsBarsOfTheRangeAndComputes(t *testing.T) {
	// AAPL buy 10 @ 100 on 2026-11-02, range 1m, benchmarks SPY
	// provider asked for [AAPL, SPY] 2026-10-10..2026-11-10; report.From 2026-11-02, DataThrough 2026-11-10,
	// Source alpaca, Benchmarks[0].Symbol SPY
}
func TestPerformanceDoesNotAskTheProviderWhenCovered(t *testing.T)  // second call: 0 provider calls, same result
func TestPerformanceNeverAsksForADayThatHasNotClosed(t *testing.T)  // asked To == 2026-11-10, not 11-11
func TestPerformanceWithStoredBarsSurvivesAProviderThatIsDown(t *testing.T) {
	// coverage of AAPL and SPY seeded to 2026-11-06, provider down -> no error, Coverage partial,
	// DataThrough 2026-11-06, one warn log with event performance.prices_failed and no provider text
}
func TestPerformanceWithoutStoredBarsIsDependencyUnavailable(t *testing.T) // errors.Is(err, ErrDependencyUnavailable)
func TestPerformanceOfAnAccountAddsPortfoliosAndLeavesOutOtherUsers(t *testing.T) {
	// caller has two portfolios with 10 AAPL each, another user has 500 AAPL -> first point value is 20 x close
}
func TestPerformanceWithoutTradesOrBenchmarksAsksNothing(t *testing.T) // 0 provider calls, Points len 0, complete
func TestPerformanceVerifiesABenchmarkTheStoreHasNotSeen(t *testing.T) // QQQ not stored -> lookup called once, upserted
func TestPerformanceStopsWhenTheCallerLeft(t *testing.T)          // cancelled ctx -> context.Canceled
```

- [ ] **Step 2: รันให้เห็นว่าไม่ผ่าน**

Run: `go test ./internal/core/service/ -run Performance`
Expected: FAIL (undefined NewPerformance)

- [ ] **Step 3: เขียน port และ service** ตามลำดับงานข้างบน

- [ ] **Step 4: รันให้ผ่าน**

Run: `go test -race ./internal/core/service/`
Expected: ok

- [ ] **Step 5: Commit** `feat(service): portfolio performance against benchmarks`

---

### Task 6: HTTP endpoints, Swagger และ wiring

**Files:**
- Create: `internal/adapter/http/performance.go`, `internal/adapter/http/performance_dto.go`
- Modify: `internal/adapter/http/server.go` (`Options.Performance PerformanceOptions`, `registerPerformanceRoutes`, เงื่อนไข auth chain), `internal/adapter/http/auth_harness_test.go` (`harnessConfig.performance`), `cmd/api/wire.go` (`buildPerformance` ตามแบบ `buildPriceHistory`), `cmd/api/main.go`, wiring tests ใน `cmd/api`
- Generate: `docs/api/*` ด้วย `make swagger`
- Test: `internal/adapter/http/performance_test.go`, `internal/adapter/http/route_contract_test.go`

**Interfaces:**
- Consumes: `port.PerformanceService`, `port.PerformanceQuery`, `port.PerformanceReport` (Task 5), `ParsePerformanceRange`, `ParseBenchmarks` (Task 1)
- Produces: routes `GET`/`HEAD` `/api/v1/portfolios/:portfolio_id/performance` (operationId `getPortfolioPerformance`) และ `/api/v1/performance` (operationId `getAccountPerformance`) tag `Holdings/Prices` หลัง `chain.session` + `middleware.RequireAuth()`; `Cache-Control: no-store`

```go
type PerformanceRangeResponse struct {
	From *string `json:"from" example:"2025-11-12"` // null when there is no point
	To   *string `json:"to" example:"2026-11-10"`
}
type PerformancePointResponse struct {
	Date          string  `json:"date" example:"2025-11-13"`
	MarketValue   *string `json:"market_value" example:"1012.5"`
	ReturnPercent *string `json:"return_percent" example:"1.25"`
}
type PortfolioPerformanceResponse struct {
	ReturnPercent *string                    `json:"return_percent" example:"18.4"`
	Points        []PerformancePointResponse `json:"points"` // [] never null
}
type BenchmarkPointResponse struct {
	Date          string  `json:"date"`
	ReturnPercent *string `json:"return_percent"`
}
type BenchmarkPerformanceResponse struct {
	Symbol              string                   `json:"symbol" example:"SPY"`
	Label               string                   `json:"label" example:"S&P 500 (SPY)"`
	ReturnPercent       *string                  `json:"return_percent" example:"12.1"`
	ExcessReturnPercent *string                  `json:"excess_return_percent" example:"6.3"`
	Points              []BenchmarkPointResponse `json:"points"`
}
type PerformanceResponse struct {
	Currency     string                         `json:"currency" example:"USD"`
	Range        PerformanceRangeResponse       `json:"range"`
	Method       string                         `json:"method" example:"time_weighted"`
	FeesIncluded bool                           `json:"fees_included" example:"true"`
	Adjustment   string                         `json:"adjustment" example:"raw"`
	Interval     string                         `json:"interval" example:"1d"`
	Coverage     string                         `json:"coverage" enums:"complete,partial,unavailable"`
	Source       string                         `json:"source" example:"alpaca"`
	IsDemo       bool                           `json:"is_demo"`
	DataThrough  *string                        `json:"data_through" example:"2026-11-10"`
	Portfolio    PortfolioPerformanceResponse   `json:"portfolio"`
	Benchmarks   []BenchmarkPerformanceResponse `json:"benchmarks"` // [] never null
}
```

status: 200; 400 `invalid_input` (`range` ขาดหรือไม่รู้จัก, `benchmarks` ไม่รู้จัก/ซ้ำ, `portfolio_id` ไม่ใช่ UUID); 401; 404 `not_found` (เฉพาะรายพอร์ต); 429 `rate_limited` (benchmark ที่ต้องตรวจกับ provider เกินโควตา); 500; 503 `dependency_unavailable` หรือ `server_busy`
`@Description` ต้องบอก: สูตร (ซื้อนับต้นวัน ขายนับปลายวัน รวมค่าธรรมเนียม), วันแรกเป็นฐาน 0, ช่วงที่ไม่ถืออะไรเป็นเส้นราบ, จุด `null` กับ `coverage`, ราคา `raw` ไม่ปรับ split, ไม่รวมเงินปันผล, benchmark เป็น ETF, ไม่ใช่คำแนะนำการลงทุน

- [ ] **Step 1: เขียน HTTP test ที่ยังไม่ผ่าน** ใช้ harness เดิม (`withValuation()`, `withBars()` เป็นแบบ; เพิ่ม `withPerformance()`), `b.trade(...)`, `h.setBars(...)`, `h.day(n)`

```go
func TestPerformanceNeedsASession(t *testing.T)                        // both paths 401
func TestPerformanceAnswersExactStringsOldestFirst(t *testing.T) {
	// buy 10 AAPL @ 100 three trading days ago; AAPL closes 100, 110, 121; SPY 500, 505, 510
	// GET .../performance?range=1m&benchmarks=SPY -> 200
	// raw JSON: method "time_weighted", fees_included true, adjustment "raw", interval "1d", currency "USD",
	// coverage "complete", portfolio.points[*].return_percent "0", "10", "21", market_value "1000", "1100", "1210",
	// benchmarks[0].label "S&P 500 (SPY)", points "0", "1", "2", excess_return_percent "19",
	// range.from == points[0].date, Cache-Control no-store
}
func TestPerformanceRefusesABadRequestWith400(t *testing.T) // no range, range=2y, benchmarks=AAPL, benchmarks=SPY,SPY, portfolio_id=abc
func TestPerformanceOfAnotherUsersPortfolioIs404(t *testing.T)
func TestPerformanceOfAnEmptyPortfolioHasEmptyListsNotNull(t *testing.T) // points [], benchmarks [], range.from null, return_percent null
func TestPerformanceShowsAMissingBarAsNull(t *testing.T)   // raw JSON has "market_value":null, coverage "partial"
func TestAccountPerformanceAddsPortfoliosOfTheCallerOnly(t *testing.T) // two own portfolios + another user's
func TestPerformanceWhenTheProviderIsDownAndNothingIsStoredIs503(t *testing.T) // code dependency_unavailable
```

เพิ่มสอง route ใน `route_contract_test.go` ตามแบบของ `bars`

- [ ] **Step 2: รันให้เห็นว่าไม่ผ่าน**

Run: `make test-integration`
Expected: FAIL (compile error: withPerformance undefined)

- [ ] **Step 3: เขียน handlers, DTO, route, wiring แล้ว `make swagger`** handler รายพอร์ตตามแบบ `valuationHandlers.value`; query อ่านด้วย `c.Query("range")`, `c.Query("benchmarks")`; meta ด้วย `requestMeta` แบบเดียวกับ `price_history.go`

- [ ] **Step 4: รันให้ผ่าน**

Run: `make test-integration && make check`
Expected: ทุกแพ็กเกจ ok; lint 0 issues; `swagger validate` ผ่าน; ไม่มี diff ของไฟล์ generate หลัง `git add`

- [ ] **Step 5: Commit** `feat(http): performance endpoints for a portfolio and the account`

---

### Task 7: เอกสารและตรวจกับข้อมูลจริง

**Files:**
- Modify: `docs/frontend-handoff.md` (หัวข้อ 1, 4, 8 เพิ่มหัวข้อย่อย performance), `README.md`, `docs/superpowers/specs/2026-10-05-phase-1-design.md` (หัวข้อ port และ API ที่ขั้น 1-2 แก้ไว้), `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md` (หัวข้อ "งานหลังเฟส 1" เพิ่มข้อ B), `scripts/e2e/flow.sh`, แผนนี้ (สถานะ, ผลตรวจ, สิ่งที่ตัดสินระหว่างลงมือ)

- [ ] **Step 1:** อัปเดตเอกสารข้างบน ให้ handoff บอก contract, กติกา `null`/`coverage`, ข้อความที่ UI ต้องแสดง (ไม่รวมปันผล, ราคาไม่ปรับ split, ดัชนีใช้ ETF, ไม่ใช่คำแนะนำการลงทุน)
- [ ] **Step 2:** เพิ่มขั้น performance ใน `scripts/e2e/flow.sh` (เรียก `/performance?range=1m&benchmarks=SPY,QQQ` ของพอร์ตและของบัญชี ตรวจ 200 และ `method`)
- [ ] **Step 3:** รัน stack โหมดเดโม แล้วรัน `scripts/e2e/flow.sh`

Run: `PRICE_PROVIDER=demo docker compose up -d --build && scripts/e2e/flow.sh`
Expected: flow จบด้วย exit 0 รวมขั้นราคารายวันของขั้น 1-2 ที่ยังไม่เคยรัน; `is_demo` เป็น `true`

- [ ] **Step 4:** ตรวจครั้งเดียวกับ Alpaca จริง (`PRICE_PROVIDER=alpaca`): สร้างพอร์ตที่มี 3 หุ้น ขอ `range=all` กับ `benchmarks=SPY,QQQ` จดเวลาตอบครั้งแรกกับครั้งที่สอง และขนาด response ลงหัวข้อ "ผลตรวจ" ของแผนนี้ (ADR-0025 ระบุว่ายังไม่ได้วัด)
- [ ] **Step 5:** `make check && make test-integration` ผ่าน แล้ว commit `docs: performance endpoints`
- [ ] **Step 6:** เมื่อผู้ใช้ตรวจรับ เปลี่ยน ADR-0025 เป็น Accepted

## ความเสี่ยงและสิ่งที่ยอมรับ

- คำขอแรกของพอร์ตใหญ่ช่วงยาวอาจได้ `partial` หรือ 503 เพราะเติมไม่ทัน 8 วินาที คำขอถัดไปเติมต่อ ยังไม่ได้วัดกับพอร์ตจริง (Task 7 Step 4)
- ผลตอบแทนของวันแรกที่ซื้อไม่อยู่ในเส้น และมูลค่าหลังวันแตกหุ้นผิด (ADR-0025, ADR-0024)
- benchmark ที่ยังไม่อยู่ใน `instruments` ใช้โควตาตรวจ symbol ของผู้ใช้คนแรกที่ขอ หนึ่งครั้งต่อ symbol ตลอดอายุฐานข้อมูล
- ทุกคำขออ่านรายการและแท่งทั้งช่วง ไม่มี cache
