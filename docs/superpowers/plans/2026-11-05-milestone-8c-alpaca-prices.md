# Milestone 8c — ราคาล่าช้าจริงจาก Alpaca Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** เมื่อตั้ง `PRICE_PROVIDER=alpaca` ให้ `GET .../valuation` ใช้ราคาจริงจาก Alpaca: close ของแท่ง 1 นาที (SIP, ไม่ปรับ split/dividend) ที่จบไม่เกิน `ตอนนี้ − 15 นาที` ภายใน regular session ตามปฏิทินตลาดจริง พร้อมบอก `session_status`/`stale`/`cached` ตามความจริง, cache, retry ที่มีขอบเขต และไม่ถอยไปใช้ราคา demo

**Architecture:** adapter `internal/adapter/alpaca` ได้ HTTP client ร่วม (header, timeout 5 วินาที, ไม่ตาม redirect, body ไม่เกินขีดจำกัด, retry หนึ่งครั้งเฉพาะความล้มเหลวชั่วคราว) ที่ Assets ใช้ด้วย, ตัวอ่านปฏิทิน `GET /v3/calendar/XNYS` ของ Trading API, ตัวอ่านแท่ง `GET /v2/stocks/bars` ของ Market Data API และ `alpaca.Prices` ที่ implement `port.PriceProvider` โดยเลือกแท่งอ้างอิงด้วย pure function แล้วเก็บ cache สองชั้น (สด 60 วินาที, สำรองตอน outage ไม่เกิน 7 วัน); core เพิ่มเพียงการไม่เชื่อ quote ที่ผิดรูปและ symbol ที่ provider ไม่ตอบ; `cmd/api` แทน `unavailablePrices` ด้วย adapter จริง

**Tech Stack:** Go 1.27 (go.mod `go 1.27.1`), `net/http` + `encoding/json` (`UseNumber`, ไม่มี float), `net/http/httptest` fixtures, shopspring/decimal, golangci-lint v2.14.0 — ไม่มี dependency ใหม่ และไม่มี migration

**Spec:** [phase 1 spec](../specs/2026-10-05-phase-1-design.md) หัวข้อ 12 ("Alpaca: historical SIP bars `1Min`, `adjustment=raw`, regular session เท่านั้น …", stale policy, calendar unknown, cache 60s/500 และ outage 7 วัน, timeout 5s/รวม 10s, retry 1 ครั้ง) และ 13 (config) กับ [AGENTS.md](../../../AGENTS.md) หัวข้อ "รายละเอียดข้อมูลราคา" และ "ความแม่นยำของตัวเลข"

**ข้อมูล Alpaca ที่ตรวจจากเอกสารทางการแล้ว (2026-11-05):**
- `GET https://data.alpaca.markets/v2/stocks/bars` — `symbols` (คั่นด้วยจุลภาค), `timeframe=1Min`, `start`/`end` (RFC 3339, รวมปลาย), `limit` (ค่าเริ่มต้น 1000 สูงสุด 10000 นับรวมทุก symbol), `adjustment=raw`, `feed=sip`, `sort=asc|desc`, `page_token`; ตอบ `{"bars":{"AAPL":[{"t","o","h","l","c","v","n","vw"}]},"next_page_token":string|null}` ราคาเป็น JSON number; error 400/401/403/429 (`X-RateLimit-*`)/500
- FAQ: `t` คือ **ขอบซ้าย** ของช่วงแท่ง; นาทีที่ไม่มีการซื้อขาย **ไม่มีแท่ง**; ไม่มี subscription ต้องให้ `end` เก่ากว่า 15 นาทีจึงได้ SIP; ไม่ได้บอกว่าแท่งรวม extended hours หรือไม่ (แผนนี้จึงกรองด้วยเวลาของ regular session เอง)
- ปฏิทิน: `GET https://paper-api.alpaca.markets/v3/calendar/XNYS?start=YYYY-MM-DD&end=YYYY-MM-DD&timezone=UTC` ตอบ `{"market":{…},"calendar":[{"date","core_start","core_end",…}]}` เวลาเป็น date-time มี offset
- Basic (ฟรี): historical 200 ครั้ง/นาที
- **ยังไม่ได้ยืนยันกับบัญชีจริง:** สิทธิ์ SIP historical และ `/v3/calendar` ของบัญชี paper ฟรี — Task 6 ยืนยันด้วย smoke test

## Global Constraints

- core (`internal/core/**`) import ได้เฉพาะ standard library, `github.com/shopspring/decimal`, `github.com/google/uuid`, `golang.org/x/crypto/argon2` และ `go-backend-stock/internal/core/...`; adapter Alpaca ห้าม import Fiber, pgx หรือ sqlc
- ห้ามแปลงราคาผ่าน float: decode ด้วย `json.Decoder.UseNumber()` แล้วส่ง `json.Number.String()` เข้า `domain.ParsePrice`; ตัวเลขที่ `ParsePrice` ไม่รับ (exponent, เกิน 10 ตำแหน่ง, ศูนย์/ติดลบ) = ไม่มีราคาสำหรับ symbol นั้น ห้ามปัด
- ค่าคงที่ของ request bars: `timeframe=1Min`, `feed=sip`, `adjustment=raw`, `sort=desc`, `limit=10000`; Source ของ quote = `alpaca`, Feed = `sip`, `IsDemo=false`, Currency `USD`
- แท่งอ้างอิง = แท่งล่าสุดของ symbol ที่ `t ≥ session.Open` และ `t + 1 นาที ≤ min(cutoff ปัดลงเป็นนาที, session.Close)`; `AsOf = t + 1 นาที` (เวลาจบแท่ง ไม่ใช่เวลาเริ่ม); `FetchedAt` = clock ของ adapter
- session อ้างอิง: ถ้า `session.Open ≤ cutoff < session.Close` ของวันใด → `SessionOpen` และเวลาที่คาดหวัง `E = cutoff ปัดลงเป็นนาที`; ไม่งั้นใช้ session ล่าสุดที่ `Close ≤ cutoff` → `SessionMarketClosed` และ `E = session.Close` (ครอบคลุมวันหยุด สุดสัปดาห์ ก่อนเปิดตลาด และวันปิดเร็ว); `stale = E − AsOf > 5 นาที`
- ปฏิทินอ่านไม่ได้ → ไม่เดาวันทำการ: ขอแท่งช่วง `[cutoff − 4 วัน, cutoff − 1 นาที]` ใช้แท่งล่าสุดที่ `t + 1 นาที ≤ cutoff` แล้วติด `SessionUnknown`, `stale=true`
- symbol ที่ไม่มีแท่งที่เข้าเงื่อนไขใน session อ้างอิง → อยู่ใน `Unavailable` (ไม่ย้อนไปหา session ก่อนหน้า)
- ทุก HTTP call: timeout 5 วินาที, ไม่ตาม redirect, body ไม่เกิน 8 MiB สำหรับ bars และ 256 KiB สำหรับ calendar/assets, error ห้ามมี key/secret/body; retry ได้ **หนึ่งครั้ง** เฉพาะ network error, timeout, 429 และ 5xx เมื่อเวลาที่เหลือของ context พอสำหรับรอ (`Retry-After` ถ้ามี ไม่งั้น 250ms) บวกอีกหนึ่ง request; ไม่ retry 400/401/403/404
- pagination: ตาม `next_page_token` สูงสุด 5 หน้าต่อการเรียก `Quotes`; หยุดเมื่อทุก symbol ได้แท่งแล้ว; เกิน 5 หน้า = symbol ที่ยังไม่มีแท่งเป็น `Unavailable`
- cache: สด = quote ที่ fetched ภายใน 60 วินาที ใช้ตอบโดยไม่เรียก Alpaca และติด `Cached=true`; สำรอง = เมื่อเรียก bars ไม่สำเร็จ ใช้ quote ล่าสุดที่อายุไม่เกิน 7 วัน ติด `Cached=true` และ `Stale=true`; เก็บสูงสุด 500 symbol (เกินแล้วลบที่เก่าที่สุด); ปฏิทินเก็บ 6 ชั่วโมงต่อวันที่ของ cutoff (UTC)
- ไม่มี cache และ bars ล้ม → `Quotes` คืน error (service เปลี่ยนเป็น `provider_unavailable`); ห้ามถอยไปใช้ราคา demo
- config ใหม่ `ALPACA_DATA_BASE_URL` ค่าเริ่มต้น `https://data.alpaca.markets` กติกาเดียวกับ `ALPACA_TRADING_BASE_URL` (origin เท่านั้น, production ต้อง https)
- ห้ามเรียก Alpaca จริงใน `go test ./...`, `make check` หรือ CI; smoke test ผ่าน `make smoke-alpaca` เท่านั้น ซึ่งใช้ key ใน `.env` ของผู้ใช้ (ผู้ใช้อนุญาตให้ผู้ลงมือรันแล้วในแผน 8a; ไม่พิมพ์ค่า key)
- ห้ามแก้ `.env`; `.env.example` และ `docker-compose.yml` เพิ่มได้เฉพาะ `ALPACA_DATA_BASE_URL`
- **`make -s test-integration` ล้างทุกตาราง schema `app` ใน dev DB หลังรันทุกครั้ง** — ใช้ได้ตามที่ผู้ใช้ตกลงแล้ว
- ต้องเห็น test ล้มก่อนแล้วจึงเขียนโค้ดทุก task; จบ task ด้วย `gofmt -l internal cmd` ที่ไม่พิมพ์อะไร, `go vet ./...` และ `make lint` ผ่าน
- commit message ไม่มี `Co-Authored-By` หรือชื่อ Claude; commit ในเครื่องเท่านั้น ห้าม push; `git add` เฉพาะไฟล์ของ task

## การตัดสินใจที่แผนนี้เสนอ (รอผู้ใช้ยืนยันตอนรีวิวแผน)

1. **ใช้ราคา SIP แบบ historical ไม่ใช้ endpoint `latest`/snapshot** เพราะ endpoint เหล่านั้นต้อง subscription สำหรับ SIP และ AGENTS.md ห้ามติดป้าย IEX ว่าเป็น delayed SIP
2. **หุ้นที่ไม่มีการซื้อขายเลยใน session อ้างอิง = ไม่มีราคา** (`no_price`) ไม่ย้อนไปหา session ก่อนหน้า เพื่อไม่ต้องขอข้อมูลย้อนหลังหลายวันต่อคำขอ (โควตา 200 ครั้ง/นาที)
3. **ปฏิทินล่มแล้วยังให้ราคา** จากแท่งล่าสุด 4 วันโดยติด `unknown` + `stale` ตาม spec แต่แท่งนั้นอาจมาจาก extended hours เพราะกรองด้วย session ไม่ได้
4. **cache อยู่ในหน่วยความจำของ process** (ไม่เพิ่ม Redis ตาม AGENTS.md) จึงหายเมื่อ restart
5. **เพิ่ม `ALPACA_DATA_BASE_URL`** ใน config, `.env.example` และ compose เพื่อให้ test ใช้ fixture ได้และรองรับ sandbox ภายหลัง
6. **ปิด Minor ของ 8b ที่เกี่ยวกับราคาจริง:** core ไม่เชื่อ quote ที่ราคาเป็นศูนย์ ไม่ใช่ USD symbol ไม่ตรง หรือ `AsOf` หลังเวลาคำนวณ (ถือเป็น `provider_unavailable`) และ symbol ที่ provider ไม่ได้ตอบทั้งใน `Quotes` และ `Unavailable` ถือเป็น `provider_unavailable` ไม่ใช่ `no_price`
7. **HEAD `/valuation` ยังคำนวณเต็ม** แต่ cache 60 วินาทีจำกัดการเรียก Alpaca (ไม่แยกพฤติกรรม HEAD)

## Review Focus

1. **วันหยุด สุดสัปดาห์ และวันปิดเร็ว (เช่น 13:00 ET)** — ต้องใช้แท่งปิดของ session ล่าสุดจริง ติด `market_closed` และ `stale=false` ถ้าแท่งอยู่ใน 5 นาทีสุดท้าย ไม่ใช่ `open` — Task 3 `TestReferenceAcrossHolidayWeekendAndEarlyClose`
2. **แท่ง extended hours ที่ API อาจส่งมา** (ก่อน 09:30 หรือหลังปิด) — ต้องไม่ถูกใช้เป็นราคา regular session — Task 3 `TestReferenceIgnoresBarsOutsideTheRegularSession`
3. **`t` เป็นขอบซ้ายของแท่ง** — แท่ง `t=14:44` จบ `14:45` ใช้ได้ที่ cutoff `14:45:30` แต่แท่ง `t=14:45` ใช้ไม่ได้ และ `as_of` = `14:45` — Task 3 `TestReferenceUsesTheEndOfTheBar`
4. **ราคาที่ JSON ให้มาเป็นตัวเลขยาวหรือ exponent** — ต้องไม่ผ่าน float และไม่ปัด: ใช้ได้ถ้า `ParsePrice` รับ ไม่งั้นไม่มีราคา — Task 3 `TestBarPricesNeverGoThroughFloat`
5. **Alpaca ล่มหลังเคยมีราคา** — ใช้ cache ที่อายุไม่เกิน 7 วันพร้อม `cached` + `stale` และเวลาจริงของแท่ง ไม่ใช่เวลาปัจจุบัน; เกิน 7 วันหรือไม่มี cache = ไม่มีราคา — Task 4 `TestOutageFallsBackToQuotesUpToSevenDaysOld`

---

## File Structure

| ไฟล์ | หน้าที่ | Task |
| --- | --- | --- |
| `internal/config/provider.go` (+test), `cmd/api/wire.go`, `.env.example`, `docker-compose.yml` | `ALPACA_DATA_BASE_URL` | 1 |
| `internal/adapter/alpaca/client.go` (+test), `assets.go`, `calendar.go` (+test) | HTTP client ร่วม + retry, ปฏิทิน | 2 |
| `internal/adapter/alpaca/bars.go` (+test), `reference.go` (+test), `prices.go` (+test) | อ่านแท่ง, เลือกแท่งอ้างอิง, `Prices.Quotes` | 3 |
| `internal/adapter/alpaca/quote_cache.go` (+test), `prices.go` | cache สด/สำรอง | 4 |
| `internal/core/domain/valuation.go` (+test) | ไม่เชื่อ quote ผิดรูป/symbol ที่ไม่ได้ตอบ | 5 |
| `cmd/api/wire.go` (+test), `internal/adapter/alpaca/smoke_test.go`, `README.md`, roadmap, spec | ประกอบ, smoke test กับบัญชีจริง, คู่มือ | 6 |

---

### Task 1: config `ALPACA_DATA_BASE_URL`

**Files:**
- Modify: `internal/config/provider.go`, `internal/config/provider_test.go`, `.env.example`, `docker-compose.yml`

**Interfaces:**
- Produces: `AlpacaConfig.DataBaseURL string` — ค่าเริ่มต้น `https://data.alpaca.markets`; ใช้ `parseAlpacaBaseURL` ตัวเดิม (ปรับให้รับชื่อตัวแปรเพื่อใส่ในข้อความ error); ว่างเมื่อ provider เป็น demo

- [ ] **Step 1: เขียน failing tests** — `TestAlpacaDataBaseURLDefaultsToTheMarketDataAPI` (alpaca ไม่ตั้ง → `https://data.alpaca.markets`), `TestAlpacaDataBaseURLRules` (กติกาเดียวกับ `TestAlpacaTradingBaseURLRules` แต่ข้อความ error ต้องระบุ `ALPACA_DATA_BASE_URL`), `TestPriceProviderDefaultsToDemoOutsideProduction` ยังผ่าน (Alpaca ว่างทั้งก้อน)
- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/config/ -count=1` → compile fail
- [ ] **Step 3: เขียนโค้ด** แล้วเพิ่ม `ALPACA_DATA_BASE_URL=` ใน `.env.example` ต่อจาก `ALPACA_TRADING_BASE_URL` และ `ALPACA_DATA_BASE_URL: ${ALPACA_DATA_BASE_URL:-}` ใน service `api` ของ `docker-compose.yml`
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/config/ ./cmd/... -count=1 && docker compose config --quiet && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(config): add ALPACA_DATA_BASE_URL for the market data API"`

---

### Task 2: HTTP client ร่วม, retry และปฏิทินตลาด

**Files:**
- Create: `internal/adapter/alpaca/client.go`, `client_test.go`, `calendar.go`, `calendar_test.go`
- Modify: `internal/adapter/alpaca/assets.go` (ใช้ client ร่วม โดย test ของ assets ทุกตัวต้องผ่านเหมือนเดิม)

**Interfaces:**
- Produces:
  - `type apiClient struct { … }`, `func newAPIClient(keyID, secret string, timeout time.Duration, httpClient *http.Client) *apiClient` — `httpClient` nil สร้างตัวที่ไม่ตาม redirect (ย้าย `CheckRedirect` จาก assets มาที่นี่)
  - `func (c *apiClient) get(ctx context.Context, rawURL string, maxBytes int64) (status int, body []byte, err error)` — ใส่ header `APCA-API-KEY-ID`, `APCA-API-SECRET-KEY`, `Accept`; timeout ต่อครั้ง; อ่าน body ไม่เกิน `maxBytes` (เกิน = error); retry หนึ่งครั้งตาม Global Constraints (ใช้ `Retry-After` วินาทีถ้ามี); error ระบุสถานะ HTTP แต่ไม่มี key/secret/body; คืน `status` จริงเมื่อไม่ retry ต่อ
  - `type session struct { Date string; Open, Close time.Time }` (UTC)
  - `type calendar struct { … }`, `func newCalendar(c *apiClient, baseURL string, now func() time.Time) *calendar`; `func (k *calendar) sessions(ctx context.Context, cutoff time.Time) ([]session, error)` — ขอ `GET {base}/v3/calendar/XNYS?start=<cutoff−10 วัน>&end=<cutoff>&timezone=UTC` (วันที่ UTC), parse `core_start`/`core_end` ด้วย RFC 3339 แล้วเป็น UTC, เรียงตาม `Open`; cache 6 ชั่วโมงต่อวันที่ UTC ของ cutoff; error ไม่ถูก cache; ตาราง `calendar` ว่าง = error

- [ ] **Step 1: เขียน failing tests**
  - `TestClientRetriesTransientFailuresOnce` — fixture ตอบ 503 แล้ว 200: ได้ 200 และเรียก 2 ครั้ง; ตอบ 429 พร้อม `Retry-After: 1` แล้ว 200 ด้วย context ที่เหลือ 5 วินาที: รอ ≥ 1 วินาทีแล้วได้ 200; ตอบ 500 สองครั้ง: error ที่มี `500` และเรียก 2 ครั้ง (ไม่ใช่ 3)
  - `TestClientDoesNotRetryWhatCannotSucceed` — 400, 401, 403, 404: เรียกครั้งเดียว
  - `TestClientDoesNotRetryWithoutTimeLeft` — 429 พร้อม `Retry-After: 30` ด้วย context ที่เหลือ 2 วินาที: เรียกครั้งเดียวและคืนทันที
  - `TestClientNeverLeaksTheKeyPair` — error ทุกกรณีข้างบนไม่มี key/secret; redirect 302 ไปอีก server: ปลายทางไม่ถูกเรียก
  - `TestClientCapsTheBody` — body ใหญ่กว่า `maxBytes` → error
  - `TestCalendarReadsRegularSessions` — fixture ตอบสองวัน (วันปกติ `core_start 2026-11-03T14:30:00Z`/`core_end 21:00Z` และวันปิดเร็ว `2026-11-27T14:30:00Z`/`18:00:00Z` ในรูปที่มี offset `-05:00`): path `/v3/calendar/XNYS`, query `start`/`end`/`timezone=UTC`, ผลเป็น UTC และเรียงตามวัน
  - `TestCalendarIsCachedForSixHours` — เรียกซ้ำภายใน 6 ชั่วโมง (clock ปลอม) fixture ถูกเรียกครั้งเดียว; 6 ชั่วโมงพอดีเรียกใหม่; error ไม่ถูก cache
  - `TestCalendarRefusesMalformedAnswers` — JSON เสีย, ไม่มี `calendar`, `core_start` ผิดรูป, ตารางว่าง → error
  - test ของ `assets_test.go` ทั้งหมดต้องผ่านโดยไม่แก้
- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/adapter/alpaca/ -count=1` → compile fail
- [ ] **Step 3: เขียน client, ย้าย assets ไปใช้ และเขียน calendar**
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/adapter/alpaca/ -count=1 -race && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(alpaca): share one HTTP client with a single bounded retry and read the market calendar"`

---

### Task 3: แท่งราคา, การเลือกแท่งอ้างอิง และ `Prices.Quotes`

**Files:**
- Create: `internal/adapter/alpaca/bars.go`, `bars_test.go`, `reference.go`, `reference_test.go`, `prices.go`, `prices_test.go`

**Interfaces:**
- Consumes: `apiClient.get`, `calendar.sessions` (Task 2), `domain.ParsePrice`, `domain.NewPriceQuote`, `port.PriceProvider`
- Produces:
  - `type bar struct { Start time.Time; Close string }` — `Close` คือ `json.Number.String()` ของ `c`
  - `func readBars(ctx context.Context, c *apiClient, baseURL string, symbols []string, start, end time.Time) (map[string][]bar, error)` — query ตาม Global Constraints (`start`/`end` RFC 3339 UTC), ตาม `next_page_token` สูงสุด 5 หน้า หยุดเมื่อทุก symbol มีอย่างน้อยหนึ่งแท่ง; แท่งที่ `t` หรือ `c` ผิดรูปถูกข้าม; status ที่ไม่ใช่ 200 = error
  - `type reference struct { Bar bar; AsOf time.Time; Status domain.SessionStatus; Stale bool }`
  - `func pickReference(bars []bar, sessions []session, cutoff time.Time) (reference, bool)` — pure function ตามกติกา session/แท่งใน Global Constraints; `sessions == nil` = โหมดปฏิทินล่ม; คืน `false` เมื่อไม่มีแท่งที่เข้าเงื่อนไข
  - `func requestWindow(sessions []session, cutoff time.Time) (start, end time.Time)` — ช่วงที่ต้องขอแท่ง: `[session.Open, E − 1 นาที]` หรือ `[cutoff − 4 วัน, cutoff − 1 นาที]` เมื่อไม่มีปฏิทิน
  - `type PricesConfig struct { DataBaseURL, TradingBaseURL, KeyID, Secret string; Timeout time.Duration; Now func() time.Time; Client *http.Client }`, `func NewPrices(cfg PricesConfig) (*Prices, error)` (ตรวจค่าแบบ `NewAssets`), `(*Prices).Quotes(ctx, symbols []domain.Symbol, cutoff time.Time) (domain.QuoteBatch, error)` — ปฏิทินล้ม → โหมด unknown; bars ล้ม → error (Task 4 เติม cache สำรอง); ราคาที่ `ParsePrice` ไม่รับ → `Unavailable`

- [ ] **Step 1: เขียน failing tests** (เวลาทั้งหมดเป็น UTC; session ปกติ `14:30–21:00Z`)
  - `TestReferenceUsesTheEndOfTheBar` — cutoff `14:45:30` (session เปิด), แท่ง `t=14:43`, `14:44`, `14:45`: เลือก `14:44`, `AsOf = 14:45`, `SessionOpen`, `stale=false`
  - `TestReferenceIsStaleAfterFiveMissingMinutes` — cutoff `15:00:10`, แท่งล่าสุด `t=14:54` (`AsOf 14:55`, ขาด 5 นาทีพอดี) → `stale=false`; `t=14:53` (`AsOf 14:54`, ขาด 6 นาที) → `stale=true`
  - `TestReferenceAcrossHolidayWeekendAndEarlyClose` — (a) cutoff วันเสาร์: ใช้ session วันศุกร์ แท่ง `t=20:59` → `AsOf 21:00`, `SessionMarketClosed`, `stale=false`; (b) cutoff ก่อน `14:30` ของวันทำการ: ใช้ session วันก่อน → `market_closed`; (c) วันปิดเร็ว close `18:00Z` cutoff `19:00`: แท่ง `t=17:59` → `AsOf 18:00`, `market_closed`, `stale=false`; แท่งล่าสุด `t=17:50` → `stale=true`; (d) วันหยุด (ไม่มี session ของวันนั้นในปฏิทิน): ใช้ session ก่อนหน้า
  - `TestReferenceIgnoresBarsOutsideTheRegularSession` — แท่ง `t=14:29` (pre-market) และ `t=21:05` (after-hours) ไม่ถูกเลือก; ถ้ามีแต่แท่งนอก session → `false`
  - `TestReferenceWithoutACalendarIsUnknownAndStale` — `sessions=nil`, cutoff `15:00:10`, แท่ง `t=14:58`: เลือกได้, `SessionUnknown`, `stale=true`
  - `TestRequestWindow` — session เปิด → `[14:30, 14:44]` สำหรับ cutoff `14:45:30`; ตลาดปิด → `[open, close − 1 นาที]` ของ session ล่าสุด; ไม่มีปฏิทิน → `[cutoff − 96h, cutoff − 1m]`
  - `TestReadBarsAsksForDelayedRawSIPMinuteBars` — fixture บันทึก query: path `/v2/stocks/bars`, `symbols=AAPL,MSFT`, `timeframe=1Min`, `feed=sip`, `adjustment=raw`, `sort=desc`, `limit=10000`, `start`/`end` ตามที่ส่ง; header key/secret
  - `TestReadBarsFollowsPagesUpToFive` — หน้าแรกมีแค่ AAPL + `next_page_token`, หน้าสองมี MSFT → อ่านสองหน้าแล้วหยุด; fixture ที่ให้ token ไม่รู้จบ → หยุดที่ 5 หน้า
  - `TestBarPricesNeverGoThroughFloat` — `c` = `189.8412345678` ได้ `"189.8412345678"`; `c` = `1.5e2` และ `c` = `0.00000000001` → symbol นั้นอยู่ใน `Unavailable` ไม่ใช่ราคาที่ปัด
  - `TestQuotesPricesHeldSymbols` — fixture calendar + bars: AAPL ได้ quote (`Source alpaca`, `Feed sip`, `IsDemo false`, `Cached false`, ผ่าน `domain.NewPriceQuote`), ZZZZ ไม่มีแท่ง → `Unavailable`
  - `TestQuotesWithoutACalendarStillPricesAsUnknown` — calendar ตอบ 500 สองครั้ง: ราคายังมีแต่ `SessionUnknown` + `stale`
  - `TestQuotesFailWhenBarsFail` — bars ตอบ 403: error ที่ไม่ใช่ nil และไม่มี quote (ไม่มีราคา demo)
- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/adapter/alpaca/ -count=1` → compile fail
- [ ] **Step 3: เขียน `bars.go`, `reference.go`, `prices.go`**
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/adapter/alpaca/ -count=1 -race && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(alpaca): price symbols with the last delayed SIP minute bar of the regular session"`

---

### Task 4: cache สดและ cache สำรองตอน outage

**Files:**
- Create: `internal/adapter/alpaca/quote_cache.go`, `quote_cache_test.go`
- Modify: `internal/adapter/alpaca/prices.go`, `prices_test.go`

**Interfaces:**
- Produces: `type quoteCache struct { … }` (`sync.Mutex`), `func newQuoteCache(max int) *quoteCache`, `get(symbol string, now time.Time, maxAge time.Duration) (domain.PriceQuote, bool)`, `put(q domain.PriceQuote)`; `const freshTTL = 60 * time.Second`, `outageMaxAge = 7 * 24 * time.Hour`, `cacheMax = 500`; `Prices.Quotes` ใช้ cache สดก่อน (ขอ Alpaca เฉพาะ symbol ที่ไม่มีใน cache สด) และใช้ cache สำรองเมื่อ bars ล้ม; quote ที่คืนจาก cache มี `Cached=true` และ AsOf/FetchedAt เดิม; จาก cache สำรองมี `Stale=true` ด้วย

- [ ] **Step 1: เขียน failing tests**
  - `TestFreshQuotesAreServedFromCacheForSixtySeconds` — เรียก `Quotes` สองครั้งห่าง 59 วินาที: bars ถูกเรียกครั้งเดียว ครั้งที่สอง `Cached=true` และ `FetchedAt` เดิม; 60 วินาทีพอดีเรียกใหม่
  - `TestOnlyMissingSymbolsAreAskedFor` — AAPL อยู่ใน cache สด, ขอ AAPL+MSFT: query ของ bars มีแค่ `MSFT`
  - `TestOutageFallsBackToQuotesUpToSevenDaysOld` — ได้ราคา AAPL แล้ว bars ล่ม: ที่ 6 วัน 23 ชั่วโมง คืน quote เดิมพร้อม `Cached=true`, `Stale=true`, `AsOf` เดิม และไม่มี error; ที่ 7 วันพอดี → ไม่มีราคา; ถ้าไม่มี symbol ใดอยู่ใน cache เลย → error
  - `TestCacheKeepsAtMostFiveHundredSymbols` — ใส่ 501 symbol ตัวแรกหายไป
  - `TestCacheIsSafeForConcurrentQuotes` — 20 goroutine เรียก `Quotes` พร้อมกันภายใต้ `-race` ไม่มี race และผลถูกต้อง
- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/adapter/alpaca/ -count=1` → FAIL
- [ ] **Step 3: เขียน cache และต่อเข้า `Quotes`**
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/adapter/alpaca/ -count=1 -race && make lint`
- [ ] **Step 5: Commit** — `git commit -m "feat(alpaca): cache quotes for a minute and serve week-old ones during an outage"`

---

### Task 5: core ไม่เชื่อ quote ที่ผิดรูปและ symbol ที่ provider ไม่ตอบ

**Files:**
- Modify: `internal/core/domain/valuation.go`, `internal/core/domain/valuation_test.go`

**Interfaces:**
- Produces: ใน `ValuePortfolio` สำหรับ position ที่ยังถือ: มี quote ใน `batch.Quotes` ที่ `Price` ไม่ใช่ศูนย์, `Currency == "USD"`, `Symbol` ตรงกับ holding และ `AsOf` ไม่หลัง `at` → มีราคา; quote ที่ผิดเงื่อนไขข้อใด → `ReasonProviderUnavailable`; ไม่มี quote แต่อยู่ใน `batch.Unavailable` → `ReasonNoPrice`; ไม่อยู่ทั้งสองที่ → `ReasonProviderUnavailable`; ลายเซ็นไม่เปลี่ยน

- [ ] **Step 1: เขียน failing tests**
  - `TestValueDistrustsMalformedQuotes` — quote ราคาศูนย์, `EUR`, symbol อื่น, `AsOf` หลัง `at`: position นั้นไม่มีราคา reason `provider_unavailable` และ coverage ไม่ใช่ `complete`
  - `TestValueTellsNoPriceFromNoAnswer` — MSFT อยู่ใน `Unavailable` → `no_price`; NVDA ไม่อยู่ทั้งใน `Quotes` และ `Unavailable` → `provider_unavailable`
  - แก้ `TestValuePartiallyPricedPortfolio` ให้ batch ระบุ MSFT/NVDA ใน `Unavailable` (ความหมายเดิม: provider ตอบว่าไม่มีราคา) แล้ว assertion เดิมยังผ่าน
- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./internal/core/domain/ -count=1` → FAIL
- [ ] **Step 3: แก้ `ValuePortfolio`** (เพิ่ม set ของ `Unavailable` และการตรวจ quote; ไม่เปลี่ยน API)
- [ ] **Step 4: รัน test ให้ผ่าน** — `gofmt -l internal cmd; go vet ./... && go test ./internal/core/... ./internal/adapter/... -count=1 -race && make -s test-integration && make lint`
- [ ] **Step 5: Commit** — `git commit -m "fix(domain): value nothing from a malformed quote or an unanswered symbol"`

---

### Task 6: ประกอบ, smoke test กับบัญชีจริง และคู่มือ

**Files:**
- Modify: `cmd/api/wire.go`, `cmd/api/wire_test.go`, `internal/adapter/alpaca/smoke_test.go`, `README.md`, `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md`, `docs/superpowers/specs/2026-10-05-phase-1-design.md`

**Interfaces:**
- Consumes: `alpaca.NewPrices`/`PricesConfig` (Task 3), `config.AlpacaConfig.DataBaseURL` (Task 1)
- Produces: `priceProvider(cfg, clock)` สำหรับ `alpaca` คืน `alpaca.NewPrices(...)` (`Now: clock.Now`, `Timeout: alpaca.DefaultTimeout`) และลบ `unavailablePrices`

- [ ] **Step 1: เขียน failing tests**
  - แก้ `TestPriceProviderFollowsPriceProvider` กรณี alpaca: ใช้ `httptest.Server` สองตัว (calendar ที่ `TradingBaseURL`, bars ที่ `DataBaseURL`) แล้ว `Quotes(AAPL)` ได้ราคาจาก fixture ที่ `IsDemo=false`; ไม่มีราคา demo ในทุกกรณี
  - `TestAlpacaPricesSmoke` ใน `smoke_test.go` (ข้ามเมื่อไม่มี `REQUIRE_ALPACA_SMOKE`) — `Quotes([AAPL, SPY], now − 15m)`: ทั้งสองมีราคา, `Feed sip`, `IsDemo false`, `AsOf ≤ cutoff`, `SessionStatus` ไม่ใช่ `unknown` (ปฏิทินอ่านได้); ไม่ `t.Log` ค่า config; ถ้าได้ 403 ให้ fail ด้วยข้อความว่าบัญชีไม่มีสิทธิ์ SIP historical
- [ ] **Step 2: รัน test ให้เห็นว่าล้ม** — `go test ./cmd/api/ -count=1` → FAIL
- [ ] **Step 3: แก้ wire**
- [ ] **Step 4: แก้เอกสาร**
  - README: หัวข้อ "ตรวจ symbol กับ Alpaca" เป็น "ใช้ข้อมูลจริงจาก Alpaca" — `PRICE_PROVIDER=alpaca` ให้ทั้งการตรวจ symbol และราคาล่าช้า SIP ของแท่ง 1 นาทีใน regular session, `as_of` คือเวลาจบแท่ง, `session_status`/`stale`/`cached` หมายถึงอะไร, ตลาดปิด/วันหยุดใช้แท่งปิดของ session ล่าสุด, Alpaca ล่มใช้ราคาที่เก็บไว้ไม่เกิน 7 วันพร้อม `stale`, โควตาฟรี 200 ครั้ง/นาทีจึงมี cache 60 วินาที; ลบข้อความว่า valuation เป็น `unavailable` จนถึง 8c
  - roadmap: ติ๊ก "เพิ่ม Alpaca PriceProvider …", "เพิ่ม batch/cache, timeout, bounded retry และ quote metadata …", "ทดสอบ … ตลาดปิด/วันหยุด, stale/missing price …" และ "ตรวจ Alpaca จริงแบบ smoke test …" (ข้อหลังเฉพาะเมื่อ smoke ผ่าน), สถานะ 8c และ milestone 8 "ลงมือครบแล้ว รอตรวจรับ"
  - spec หัวข้อ 12–13: endpoint จริง (`/v2/stocks/bars`, `/v3/calendar/XNYS`), `ALPACA_DATA_BASE_URL`, กติกาเลือกแท่ง/stale/cache ตามที่ทำจริง, การตัดสินใจข้อ 2–3
- [ ] **Step 5: รันทั้งหมด** — `make -s test-integration && go test ./... -race -count=1 && go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...` (อนุญาตเฉพาะ GO-2026-6809, GO-2026-6629)
- [ ] **Step 6: Commit** — `git commit -m "feat(api): price portfolios with delayed Alpaca SIP bars when PRICE_PROVIDER is alpaca"`
- [ ] **Step 7: รัน `make smoke-alpaca`** ด้วย key ใน `.env` ของผู้ใช้ (ไม่พิมพ์ค่า) — ผ่าน: ติ๊กข้อ smoke ใน roadmap แล้ว commit `docs: record that the Alpaca paper key reads delayed SIP bars and the calendar`; ไม่ผ่าน: รายงานสถานะ HTTP และขั้นที่ล้ม ไม่ติ๊ก
- [ ] **Step 8: รัน `make check` หลัง commit** — Expected: exit 0
