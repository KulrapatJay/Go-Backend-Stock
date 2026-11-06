# Milestone 8a — ตรวจ symbol กับ Alpaca Assets API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ให้ระบบตรวจ symbol ใหม่กับ Alpaca Assets API จริงเมื่อตั้ง `PRICE_PROVIDER=alpaca` (dev ยังใช้ demo ได้) โดย production ต้องใช้ Alpaca พร้อม credentials และตอบ 503 อย่างชัดเจนเมื่อ Alpaca ใช้ไม่ได้ ไม่ fallback ไปข้อมูลเดโม

**Architecture:** เพิ่ม config `PRICE_PROVIDER` + `ALPACA_*` ใน `internal/config` (เลือก provider ของทั้ง instrument และราคาในแผนถัดไป), adapter ใหม่ `internal/adapter/alpaca` ที่ implement `port.InstrumentProvider` ด้วย `GET /v2/assets/{symbol}` พร้อม timeout 5 วินาทีและ negative cache 10 นาที และเปลี่ยน `cmd/api/wire.go` ให้เลือก demo หรือ Alpaca ตาม config แทน `noInstrumentProvider`; core และ `service.Transactions` ไม่เปลี่ยน เพราะแปลง error ของ provider เป็น `unknown_symbol`/`dependency_unavailable` ไว้แล้วใน milestone 7

**Tech Stack:** Go 1.27 (go.mod `go 1.27.1`), `net/http` ของ standard library (ไม่เพิ่ม Alpaca SDK), `net/http/httptest` สำหรับ fixtures, Fiber v3.5.0, golangci-lint v2.14.0 — ไม่มี dependency ใหม่

**Spec:** [phase 1 spec](../specs/2026-10-05-phase-1-design.md) หัวข้อ 7 (instruments, `instrument_lookup_user`, negative cache 10 นาที), 9 ขั้น 0, 12 (บรรทัด "Instrument lookup: Alpaca adapter ใช้ Assets API … timeout 5 วินาที ไม่ retry 404") และ 13 (config `PRICE_PROVIDER`, `ALPACA_KEY/SECRET`, `ALPACA_TRADING_BASE_URL` ค่าเริ่มต้น paper endpoint; production ปฏิเสธ demo provider) กับ [AGENTS.md](../../../AGENTS.md) หัวข้อ Docker ("ค่าเริ่มต้นสำหรับ dev ใช้ PRICE_PROVIDER=demo …"), CI ("CI ไม่เรียก … Alpaca"), "ห้ามฝัง API key ในโค้ด" และ [ADR-0008](../../adr/0008-instruments-surrogate-key.md)

**แผนถัดไปของ milestone 8 (ยังไม่เขียน):** 8b holdings + valuation + demo PriceProvider + endpoints `GET .../holdings`, `GET .../valuation`; 8c Alpaca PriceProvider (historical SIP bars, calendar, stale policy, cache, retry)

**ข้อมูล Alpaca ที่ตรวจจากเอกสารทางการแล้ว (2026-11-04):** paper `https://paper-api.alpaca.markets`, live `https://api.alpaca.markets`; `GET /v2/assets/{symbol_or_asset_id}`; header `APCA-API-KEY-ID`, `APCA-API-SECRET-KEY`; asset object มี `id` (UUID), `class` (`us_equity`, `crypto`, … ), `exchange` (เช่น `NASDAQ`, `NYSE`, `NYSEARCA`, `OTC`), `symbol`, `name`, `status` (`active`/`inactive`), `tradable`; symbol ที่ไม่มีคืน 404 โดยเอกสารไม่กำหนด body — สิทธิ์ของบัญชี paper/free ยังต้องยืนยันด้วย smoke test ใน Task 4

## Global Constraints

- core (`internal/core/**`) ห้ามแก้ใน milestone นี้ ยกเว้นที่ Task ระบุ; adapter Alpaca ห้าม import Fiber, pgx หรือ sqlc
- ห้ามฝัง key/secret ในโค้ด, test fixtures, เอกสาร หรือ commit; ใช้ชนิด `config.Secret` (แสดง `[REDACTED]`) สำหรับ `ALPACA_KEY` และ `ALPACA_SECRET`; error, log และ panic message ห้ามมีค่า key/secret หรือ response body ทั้งก้อน
- `PRICE_PROVIDER` รับเฉพาะ `demo` หรือ `alpaca`; ไม่ตั้งค่า = `demo` นอก production; production ต้องเป็น `alpaca` และต้องมี `ALPACA_KEY`, `ALPACA_SECRET`
- `ALPACA_TRADING_BASE_URL` ค่าเริ่มต้น `https://paper-api.alpaca.markets`; ต้องเป็น absolute URL ไม่มี path อื่นนอกจาก `/`, ไม่มี query/fragment/user info; production ต้องเป็น `https`
- Assets lookup: timeout 5 วินาทีต่อคำขอ, ไม่ retry, อ่าน body ไม่เกิน 64 KiB, ใช้ `url.PathEscape(symbol)`; 404 หรือ `class != us_equity` = `domain.ErrUnknownSymbol`; อย่างอื่นที่ไม่ใช่ 200 = error ที่ลองใหม่ได้ (service แปลงเป็น 503 `dependency_unavailable` อยู่แล้ว)
- negative cache: เก็บเฉพาะผล "ไม่พบ/ไม่ใช่ us_equity" อายุ 10 นาที สูงสุด 1,000 symbol ต่อ process; ห้าม cache error ของเครือข่าย/credentials และห้าม cache ผลที่พบ (DB เก็บแล้ว)
- ห้ามเรียก Alpaca จริงใน `go test ./...`, `make check` หรือ CI; ใช้ `httptest.Server`; smoke test รันเฉพาะ `make smoke-alpaca` ที่ผู้ใช้สั่งเองโดยอ่าน key จาก `.env` ของผู้ใช้
- ห้ามแก้ `.env`; `.env.example` และ `docker-compose.yml` เพิ่มได้เฉพาะชื่อตัวแปร 4 ตัวในแผนนี้แบบค่าว่าง/ค่าเริ่มต้นที่ไม่ใช่ความลับ (รอผู้ใช้อนุญาต — การตัดสินใจข้อ 6)
- **`make -s test-integration` ล้างทุกตาราง schema `app` ใน dev DB หลังรันทุกครั้ง** — ใช้ได้ตามที่ผู้ใช้ตกลงแล้ว
- ต้องเห็น test ล้มก่อนแล้วจึงเขียนโค้ดทุก task; จบ task ด้วย `gofmt -l internal cmd` ที่ไม่พิมพ์อะไร, `go vet ./...` และ `make lint` ผ่าน
- Docker: ใช้ได้เฉพาะ `docker compose ps` และ `docker compose config --quiet`; ห้ามเรียก API ที่ผู้ใช้รันอยู่
- commit message ไม่มี `Co-Authored-By` หรือชื่อ Claude; commit ในเครื่องเท่านั้น ห้าม push; `git add` เฉพาะไฟล์ของ task

## การตัดสินใจที่แผนนี้เสนอ (รอผู้ใช้ยืนยันตอนรีวิวแผน)

1. **แบ่ง milestone 8 เป็น 8a/8b/8c** ตามรายการข้างบน 8a เล็กและปลดล็อก production ให้บันทึก symbol ได้จริง
2. **`PRICE_PROVIDER` ตัวเดียวเลือกทั้ง instrument provider และ price provider** ตาม AGENTS.md ("PRICE_PROVIDER=demo … เปลี่ยนเป็น alpaca เอง") ไม่เพิ่ม `INSTRUMENT_PROVIDER` แยก
3. **production จะเริ่มไม่ได้ถ้าไม่มี Alpaca credentials** เข้มกว่าตอนนี้ที่ production เริ่มได้แต่ตอบ 503 ทุก symbol ใหม่ เพราะ AGENTS.md ให้ production ปฏิเสธ demo provider และ config ต้อง validate ตอนเริ่มระบบ; `noInstrumentProvider` ถูกลบ
4. **ไม่ retry และไม่แยกจัดการ 429/401/403:** lookup เกิดเฉพาะ symbol ที่ยังไม่อยู่ใน instruments และจำกัด 30 ครั้ง/ชั่วโมงต่อผู้ใช้อยู่แล้ว ผู้ใช้กดใหม่ได้เมื่อได้ 503; error message ระบุสถานะ HTTP เพื่อให้ application log แยกสาเหตุได้
5. **ชื่อบริษัทยาวเกิน 200 ตัวอักษรตัดให้เหลือ 200** แทนการปฏิเสธ (ตาม `instruments.name varchar(200)`); symbol ที่ Alpaca ตอบกลับแต่ไม่ผ่าน `domain.ParseSymbol` = `unknown_symbol`
6. **เพิ่ม `PRICE_PROVIDER`, `ALPACA_KEY`, `ALPACA_SECRET`, `ALPACA_TRADING_BASE_URL` ใน `.env.example` (ค่าว่าง) และส่งเข้า service `api` ใน `docker-compose.yml`** (`${PRICE_PROVIDER:-demo}`, `${ALPACA_KEY:-}` ฯลฯ) — แผน milestone 6/7 ห้ามแตะไฟล์นี้ แผนนี้ขออนุญาตเพิ่มเฉพาะ 4 บรรทัดนี้
7. **smoke test ใช้ key ของผู้ใช้ผ่าน `make smoke-alpaca` เท่านั้น:** ผู้ใช้ใส่ค่าใน `.env` เอง ผู้ลงมือไม่อ่านหรือพิมพ์ค่า และไม่ส่งค่าในแชท

## Review Focus

1. **symbol ที่มีจุดหรือขีด เช่น `BRK.B`, `BF-B`** — ต้องเรียก path ที่ escape ถูกต้องและพบหุ้น — Task 2 `TestLookupEscapesTheSymbolInThePath` และ smoke test ใน Task 4
2. **Alpaca ค้างหรือช้า** — ต้องจบภายใน timeout ของ adapter หรือ deadline ของคำขอ แล้วเป็น error ที่ลองใหม่ได้ (503) ไม่ใช่ 500 หรือรอจน request deadline — Task 2 `TestLookupGivesUpAfterTheTimeout`
3. **ถาม symbol ที่ไม่มีซ้ำๆ** — ครั้งที่สองภายใน 10 นาทีไม่เรียก Alpaca, หลัง 10 นาทีเรียกใหม่, error ของเครือข่ายไม่ถูก cache — Task 2 `TestUnknownSymbolsAreCachedForTenMinutes`
4. **key ผิดหรือหมดอายุ (401/403)** — เป็น error ที่ลองใหม่ได้, ไม่ cache, และข้อความ error ไม่มีค่า key/secret — Task 2 `TestRejectedCredentialsAreNotCachedAndNotLeaked`
5. **body ผิดรูป ใหญ่เกิน หรือขาด field** — เป็น error ไม่ panic และไม่อ่านเกิน 64 KiB — Task 2 `TestMalformedAnswersAreErrors`

---

## File Structure

| ไฟล์ | หน้าที่ | Task |
| --- | --- | --- |
| `internal/config/provider.go`, `provider_test.go`, `config.go`, tests ของ config เดิม, `.env.example`, `docker-compose.yml` | `PRICE_PROVIDER` + `ALPACA_*` | 1 |
| `internal/adapter/alpaca/assets.go`, `assets_test.go`, `negative_cache.go` | `port.InstrumentProvider` ผ่าน Assets API | 2 |
| `cmd/api/wire.go`, `wire_test.go` | เลือก demo/Alpaca ตาม config | 3 |
| `internal/adapter/alpaca/smoke_test.go`, `Makefile`, `README.md`, roadmap, spec | smoke test กับบัญชีจริง, คู่มือ, สถานะ | 4 |

---

### Task 1: config `PRICE_PROVIDER` และ `ALPACA_*`

**Files:**
- Create: `internal/config/provider.go`, `internal/config/provider_test.go`
- Modify: `internal/config/config.go` (field ใหม่ใน `Config`, เรียก loader), tests ของ config ที่สร้าง production config (เติม Alpaca), `.env.example`, `docker-compose.yml`

**Interfaces:**
- Produces:
  - `type ProviderName string`; `const ProviderDemo ProviderName = "demo"`, `ProviderAlpaca ProviderName = "alpaca"`
  - `type AlpacaConfig struct { Key Secret; Secret Secret; TradingBaseURL string }`
  - `Config.PriceProvider ProviderName`, `Config.Alpaca AlpacaConfig`
  - `func loadProvider(getenv func(string) string, prod bool) (ProviderName, AlpacaConfig, []error)` เรียกจาก `Load` แบบเดียวกับ `loadGoogle`

- [ ] **Step 1: เขียน failing tests** (`provider_test.go`, table-driven ผ่าน `Load` แบบที่ test config เดิมใช้)
  - `TestPriceProviderDefaultsToDemoOutsideProduction` — development/test ไม่ตั้งค่า → `demo`, `Alpaca` ว่าง
  - `TestProductionNeedsAlpaca` — production ไม่ตั้ง, ตั้ง `demo`, หรือ `alpaca` แต่ขาด key/secret → `Load` error ที่ระบุชื่อตัวแปรและไม่มีค่าที่ตั้ง; ตั้งครบ → ผ่าน และ `TradingBaseURL` = `https://paper-api.alpaca.markets`
  - `TestPriceProviderRejectsUnknownValues` — `Alpaca`, `alpaca ` (มีช่องว่างหลัง trim แล้วผ่าน), `iex` → error สำหรับค่าที่ไม่ใช่ `demo`/`alpaca`
  - `TestAlpacaTradingBaseURLRules` — `http://localhost:9999` ผ่านใน development, ไม่ผ่านใน production; `https://paper-api.alpaca.markets/v2`, `…?x=1`, `https://u:p@host`, `not a url` → error
  - `TestAlpacaSecretsAreRedacted` — `fmt.Sprintf("%v %+v %#v", cfg.Alpaca, cfg.Alpaca, cfg)` ไม่มีค่า key/secret ที่ตั้ง
  - เพิ่ม Alpaca ให้ fixture ของ production config ที่ test เดิมใช้ เพื่อให้ test เดิมยังผ่าน

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/config/ -count=1` → compile fail
- [ ] **Step 3: เขียน loader ตาม Interfaces และกติกาใน Global Constraints** แล้วเพิ่ม `PRICE_PROVIDER=`, `ALPACA_KEY=`, `ALPACA_SECRET=`, `ALPACA_TRADING_BASE_URL=` ต่อท้ายกลุ่ม Google ใน `.env.example` พร้อม comment หนึ่งบรรทัด ("demo หรือ alpaca; alpaca ต้องมี key จากบัญชี paper ของ Alpaca") และเพิ่ม `PRICE_PROVIDER: ${PRICE_PROVIDER:-demo}`, `ALPACA_KEY: ${ALPACA_KEY:-}`, `ALPACA_SECRET: ${ALPACA_SECRET:-}`, `ALPACA_TRADING_BASE_URL: ${ALPACA_TRADING_BASE_URL:-}` ใน `environment` ของ service `api`
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/config/ ./cmd/... -count=1 && docker compose config --quiet && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(config): choose the instrument provider with PRICE_PROVIDER and require Alpaca in production"`

---

### Task 2: Alpaca Assets adapter

**Files:**
- Create: `internal/adapter/alpaca/assets.go`, `internal/adapter/alpaca/negative_cache.go`, `internal/adapter/alpaca/assets_test.go`

**Interfaces:**
- Consumes: `port.InstrumentProvider`, `domain.ParseSymbol`, `domain.ErrUnknownSymbol`, ชนิดของ `domain.Instrument`
- Produces:
  - `type AssetsConfig struct { BaseURL string; KeyID, Secret string; Timeout time.Duration; NegativeTTL time.Duration; NegativeMax int; Now func() time.Time; Client *http.Client }` — `Client` nil ใช้ `&http.Client{Timeout: Timeout}`; `Now` nil ใช้ `time.Now`
  - `const DefaultTimeout = 5 * time.Second`, `DefaultNegativeTTL = 10 * time.Minute`, `DefaultNegativeMax = 1000`
  - `func NewAssets(cfg AssetsConfig) (*Assets, error)` — error เมื่อ BaseURL/KeyID/Secret ว่างหรือ Timeout/NegativeTTL/NegativeMax ไม่บวก
  - `func (a *Assets) Lookup(ctx context.Context, symbol domain.Symbol) (domain.Instrument, error)` — ผลที่พบ: `Symbol` (จาก response ผ่าน `ParseSymbol`), `ProviderAssetID` = `id`, `Name` (ตัดเหลือ 200 rune), `Exchange`, `AssetClass` us_equity, `Status` จาก `status`, `Source` alpaca; ไม่มี ID/เวลา
  - `negativeCache` ภายใน package: `seen(symbol string, now time.Time) bool`, `remember(symbol string, now time.Time)`; เต็มแล้วลบรายการหมดอายุก่อน ถ้ายังเต็มลบรายการเก่าสุด; ปลอดภัยกับ goroutine หลายตัว (`sync.Mutex`)

- [ ] **Step 1: เขียน failing tests** (`httptest.NewServer` เป็น fixture; บันทึก method, path และ header ที่ได้รับ)
  - `TestLookupFindsAnActiveUSEquity` — fixture ตอบ `{"id":"b0b6dd9d-8b9b-48a9-ba46-b9d54906e415","class":"us_equity","exchange":"NASDAQ","symbol":"AAPL","name":"Apple Inc. Common Stock","status":"active","tradable":true}`: method GET, path `/v2/assets/AAPL`, header `APCA-API-KEY-ID`/`APCA-API-SECRET-KEY` ตรงกับ config, ผลลัพธ์ผ่าน `domain.NewInstrument` เมื่อเติม ID/เวลา และ field ตรงทุกตัว
  - `TestLookupKeepsInactiveAssets` — `status":"inactive"` → `InstrumentStatusInactive`
  - `TestLookupEscapesTheSymbolInThePath` — `BRK.B` ไปที่ `/v2/assets/BRK.B`, `BF-B` ไปที่ `/v2/assets/BF-B` (ตรวจ `r.URL.EscapedPath()`)
  - `TestNotFoundAndOtherClassesAreUnknown` — 404 (body ว่างหรือ JSON ใดก็ได้) และ `class":"crypto"` → `errors.Is(err, domain.ErrUnknownSymbol)`; symbol ใน response ที่ไม่ผ่าน `ParseSymbol` → unknown
  - `TestUnknownSymbolsAreCachedForTenMinutes` — 404 สองครั้งภายใน 9 นาที (ใช้ `Now` ปลอม) fixture ถูกเรียกครั้งเดียว; ที่ 10 นาทีพอดีเรียกใหม่; 500 ไม่ถูก cache (เรียกสองครั้ง); cache เต็มที่ `NegativeMax` = 2 แล้ว symbol ที่สามยังทำงาน
  - `TestRejectedCredentialsAreNotCachedAndNotLeaked` — 401 และ 403 → error ที่ไม่ใช่ `ErrUnknownSymbol`, ไม่ cache, `err.Error()` ไม่มีค่า KeyID/Secret และมีสถานะ HTTP
  - `TestLookupGivesUpAfterTheTimeout` — fixture รอนานกว่า `Timeout` 50ms → error ภายในไม่เกิน 1 วินาที; context ที่ถูกยกเลิกก่อนเรียก → error ทันที
  - `TestMalformedAnswersAreErrors` — body ไม่ใช่ JSON, JSON ที่ขาด `id`/`symbol`/`name`/`exchange`, `status` นอก active/inactive, body ใหญ่กว่า 64 KiB → error ที่ไม่ใช่ `ErrUnknownSymbol` และไม่ panic
  - `TestLongNamesAreCutTo200Characters` — ชื่อ 250 ตัวอักษรไทย → 200 rune และผ่าน `domain.NewInstrument`
  - `TestNewAssetsValidatesItsConfig` — ค่าว่าง/ไม่บวกแต่ละตัว → error ที่ไม่มีค่า secret

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/adapter/alpaca/ -count=1` → compile fail
- [ ] **Step 3: เขียน adapter ตาม Interfaces และ Global Constraints** ตรวจ negative cache ก่อนส่งคำขอ; decode ด้วย `json.Decoder` บน `io.LimitReader(body, 64<<10+1)` แล้วถ้าเกินให้เป็น error; ปิด body ทุกทาง
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/adapter/alpaca/ -count=1 -race && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(alpaca): verify symbols with the Alpaca Assets API"`

---

### Task 3: ประกอบ provider ตาม config

**Files:**
- Modify: `cmd/api/wire.go`, `cmd/api/wire_test.go`

**Interfaces:**
- Consumes: `config.Config.PriceProvider`/`Alpaca` (Task 1), `alpaca.NewAssets`/`AssetsConfig`/`DefaultTimeout`/`DefaultNegativeTTL`/`DefaultNegativeMax` (Task 2), `demo.NewInstruments`
- Produces: `func instrumentProvider(cfg config.Config) (port.InstrumentProvider, error)` — `demo` → `demo.NewInstruments()`; `alpaca` → `alpaca.NewAssets(...)` จาก `cfg.Alpaca` (`Reveal()` ค่า secret เฉพาะตอนสร้าง); `buildTransactions` ส่งต่อ error; ลบ `noInstrumentProvider` และ test ของมัน

- [ ] **Step 1: เขียน failing tests**
  - `TestInstrumentProviderFollowsPriceProvider` — `demo` → พบ AAPL จาก demo list; `alpaca` กับ `TradingBaseURL` ของ `httptest.Server` → `Lookup("AAPL")` ไปถึง fixture และพบหุ้น; `alpaca` ที่ key ว่าง (สร้าง `config.Config` ตรงๆ) → error
  - แทน `TestProductionNeverUsesTheDemoInstruments` ด้วย `TestProductionConfigCannotReachTheDemoInstruments` — production config ที่ผ่าน `config.Load` ได้ provider ที่ไม่ใช่ `*demo.Instruments`
  - `TestBuildServesTheTransactionRoutes` ยังผ่าน

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./cmd/api/ -count=1` → FAIL
- [ ] **Step 3: แก้ wire ตาม Interfaces**
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && make -s test-integration && go test ./... -race -count=1 && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(api): use the Alpaca instrument provider when PRICE_PROVIDER is alpaca"`

---

### Task 4: smoke test กับบัญชีจริง, คู่มือ และสถานะ

**Files:**
- Create: `internal/adapter/alpaca/smoke_test.go`
- Modify: `Makefile`, `README.md`, `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md`, `docs/superpowers/specs/2026-10-05-phase-1-design.md`

**Interfaces:**
- Consumes: `alpaca.NewAssets` และค่าคงที่จาก Task 2
- Produces: `make smoke-alpaca` → `REQUIRE_ALPACA_SMOKE=1 ALPACA_KEY="$(ALPACA_KEY)" ALPACA_SECRET="$(ALPACA_SECRET)" ALPACA_TRADING_BASE_URL="$(ALPACA_TRADING_BASE_URL)" go test -count=1 -run TestAlpacaSmoke ./internal/adapter/alpaca/` (ค่าว่างของ base URL ใช้ paper endpoint)

- [ ] **Step 1: เขียน `TestAlpacaSmoke`** — ไม่มี `REQUIRE_ALPACA_SMOKE` → `t.Skip`; มีแต่ key/secret ว่าง → `t.Fatal` ที่ไม่พิมพ์ค่า; มีครบ: `AAPL` พบ `us_equity` `active` และ `ProviderAssetID` ไม่ว่าง, `BRK.B` พบ, `ZZZZZZZZ` → `ErrUnknownSymbol`; ห้าม `t.Log` ค่า config
- [ ] **Step 2: รัน test ให้เห็นว่าข้ามได้** — `go test ./internal/adapter/alpaca/ -run TestAlpacaSmoke -v -count=1` → `SKIP` (CI และ `make check` ไม่เรียก Alpaca)
- [ ] **Step 3: เพิ่ม target `smoke-alpaca` ใน `Makefile`** แบบเดียวกับ `test-mail`
- [ ] **Step 4: แก้เอกสาร**
  - README: หัวข้อ "ตรวจ symbol กับ Alpaca" — สมัครบัญชี paper ฟรี ห้ามเปิดแผนเสียเงิน, สร้าง key ของ paper trading, ใส่ `PRICE_PROVIDER=alpaca`, `ALPACA_KEY`, `ALPACA_SECRET` ใน `.env` เอง (ห้าม commit), รัน `make smoke-alpaca`, แล้ว `docker compose up -d --build api`; ระบุว่าราคาและ P&L ยังใช้ไม่ได้จนถึงแผน 8b/8c และ production ต้องตั้ง `PRICE_PROVIDER=alpaca` พร้อม key มิฉะนั้น API ไม่เริ่ม
  - roadmap: แตก milestone 8 เป็น 8a/8b/8c ตามแผนนี้, ติ๊ก checkbox แรก (Alpaca InstrumentProvider) ให้เหลือเฉพาะ "ตรวจสิทธิ์ด้วยบัญชีจริง" จนกว่า smoke test ผ่าน, สถานะ 8a "ลงมือครบแล้ว รอตรวจรับ"
  - spec หัวข้อ 12–13: `PRICE_PROVIDER` เลือกทั้ง instrument และ price provider; production ต้องเป็น `alpaca`; negative cache 10 นาทีสูงสุด 1,000 symbol; ไม่ retry
- [ ] **Step 5: รันทั้งหมด** — `make -s test-integration && go test ./... -race -count=1 && go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...` (อนุญาตเฉพาะ GO-2026-6809, GO-2026-6629)
- [ ] **Step 6: Commit** — `git commit -m "docs(alpaca): add the smoke test and the Alpaca setup steps"`
- [ ] **Step 7: รัน `make check` หลัง commit** — Expected: exit 0
- [ ] **Step 8 (ผู้ใช้เป็นคนสั่ง):** หลังผู้ใช้ใส่ key ใน `.env` แล้ว รัน `make smoke-alpaca`; ผ่านแล้วจึงติ๊ก "ตรวจสิทธิ์ด้วยบัญชีจริง" ใน roadmap; ไม่ผ่านให้รายงานสถานะ HTTP ที่ได้โดยไม่พิมพ์ค่า key
