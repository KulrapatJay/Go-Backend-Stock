# ราคาปิดรายวันย้อนหลัง: ตาราง `price_bars` และ endpoint `bars` (ขั้น 1-2)

**ที่มา:** ข้อเสนอจาก frontend `next-frontend-stock/docs/backend-proposal-logos-and-price-history.md` ข้อ F และ G
ผู้ใช้ยืนยันแบบเมื่อ 2026-11-11 หลังตรวจแผน (ฐานราคา `raw`, เก็บในตาราง, `source` ใน key, กติกาวันที่ปิดแล้ว, symbol ใหม่ตรวจกับ provider)

**การตัดสินใจ:** [ADR-0024](../../adr/0024-daily-price-bars-table.md)

**สถานะ:** ลงมือครบ รอตรวจรับ (2026-11-11)

**ขอบเขต:** ขั้น 1-2 ของแผนสี่ขั้น คือเก็บราคารายวันและเปิด endpoint ราคารายวัน
ขั้น 3-4 (มูลค่าพอร์ตรายวัน และผลตอบแทนแบบ time-weighted เทียบ SPY/QQQ) ต้องมี ADR และแผนแยกก่อนลงมือ
งานนี้ไม่แตะ `Quotes`, valuation, เส้นทางสร้าง/แก้รายการซื้อขาย และไม่มีงาน frontend

## ข้อเท็จจริงของ provider ที่ตรวจกับบัญชีจริงแล้ว (2026-11-11)

`GET {ALPACA_DATA_BASE_URL}/v2/stocks/bars` ด้วย `timeframe=1Day`, `feed=sip`, `adjustment=raw`

- ย้อนหลังได้ถึง 2016-01-04; ขอหลาย symbol ในคำขอเดียวได้; โควตา 200 requests ต่อนาที
- `end` ที่ใหม่กว่า now − 15 นาที ตอบ `403 subscription does not permit querying recent SIP data`
  รวมถึง `end` ที่เป็นวันที่ของวันนี้แบบไม่มีเวลา ต้องส่ง `end` เป็น timestamp ที่ไม่ใหม่กว่า now − 15 นาทีเสมอ
- `t` ของแท่งรายวันคือเที่ยงคืนเวลา New York (`05:00Z` หรือ `04:00Z`) แปลงเป็นวันที่ใน `America/New_York`
- symbol ที่ไม่มีจริงไม่อยู่ในผลลัพธ์และไม่ทำให้ error; ช่วงที่ไม่มีข้อมูลตอบ 200 พร้อม `"bars": {}`; `BRK.B` ใช้ได้
- ยังไม่ได้ตรวจ: ช่วงตลาดเปิดมีแท่งไม่สมบูรณ์ของวันนี้หรือไม่ และแท่งรายวันนับเฉพาะ regular session หรือไม่
  กติกา "เก็บเฉพาะวันที่เก่ากว่าวันนี้ตามเวลา New York" ทำให้ข้อแรกไม่กระทบความถูกต้อง

## แบบที่ยืนยันแล้ว

### Schema (migration `00008_price_bars.sql`)

`app.price_bars`
- `instrument_id uuid NOT NULL` FK ไป `app.instruments (id) ON DELETE RESTRICT`
- `source text NOT NULL` CHECK ใน (`alpaca`, `demo`)
- `bar_date date NOT NULL`
- `open`, `high`, `low`, `close` เป็น `numeric(28,10) NOT NULL` CHECK มากกว่า 0 และน้อยกว่า `Infinity`; CHECK `high >= low`
- `volume numeric(28,0) NOT NULL` CHECK ไม่ติดลบและน้อยกว่า `Infinity`
- `fetched_at timestamptz NOT NULL`
- PRIMARY KEY `(instrument_id, source, bar_date)` ใช้เป็น index ของการอ่านช่วงวันที่ด้วย ไม่ต้องมี index เพิ่ม

`app.price_bar_coverage`
- `instrument_id uuid NOT NULL` FK แบบเดียวกัน, `source text NOT NULL` CHECK เดียวกัน
- `covered_from date NOT NULL`, `covered_through date NOT NULL` CHECK `covered_from <= covered_through`
- `updated_at timestamptz NOT NULL`
- PRIMARY KEY `(instrument_id, source)`

สิทธิ์: `app_runtime` ได้ `SELECT, INSERT` บน `price_bars` และ `SELECT, INSERT, UPDATE` บน `price_bar_coverage`
ไม่มี `DELETE` ให้ runtime มี Down migration สำหรับ dev/test

### Domain และ ports

- `domain.TradingDate`: วันที่ `YYYY-MM-DD` ที่ไม่มีเวลาและ timezone, parse/format/เปรียบเทียบ/บวกวัน
- `domain.DailyBar{Date TradingDate; Open, High, Low, Close Price; Volume}` โดย volume เป็นชนิด decimal ที่ไม่ติดลบ ไม่ผ่าน float
- `domain.DailyBarBatch`: แท่งของแต่ละ symbol ที่ถาม symbol ที่ provider ไม่มีข้อมูลในช่วงนั้นได้ slice ว่าง
- `port.PriceProvider` เพิ่ม
  `DailyBars(ctx context.Context, symbols []domain.Symbol, from, to domain.TradingDate) (domain.DailyBarBatch, error)`
  คืน error เฉพาะเมื่อถาม provider ไม่ได้หรือไม่ตอบ; adapter ไม่แทนด้วยข้อมูลเดโม; ผลลัพธ์ระบุ `source` และ `is_demo`
- `port.PriceBarRepository` (อยู่ใน `Store` ไม่อยู่ใน `UnitOfWork` เพราะไม่ใช่ข้อมูลธุรกิจและไม่มี audit)
  - `Coverage(ctx, instrumentID, source) (domain.BarCoverage, bool, error)`
  - `Range(ctx, instrumentID, source, from, to) ([]domain.DailyBar, error)` เรียงเก่าไปใหม่
  - `Save(ctx, instrumentID, source, bars, fetchedFrom, fetchedThrough, now) error`
    เขียนแท่งด้วย `ON CONFLICT DO NOTHING` และขยาย coverage ด้วย `LEAST`/`GREATEST` ใน DB transaction เดียว
- `port.PriceHistoryService.Bars(ctx, principal, symbol, from, to, meta) (domain.DailyBarSeries, error)`

### พฤติกรรมของ service (`internal/core/service/price_history.go`)

1. ตรวจ `from <= to` และช่วงไม่เกิน 3,660 วัน ไม่เช่นนั้น `invalid_input`
2. หา instrument จาก `Instruments().BySymbol`; ถ้าไม่มีให้ตรวจกับ `InstrumentProvider` แล้ว `Upsert`
   ใช้กติกาและโควตาเดียวกับ `Transactions.instrumentFor` (`RateLimitPolicy.InstrumentLookup`, 30 ต่อผู้ใช้ต่อชั่วโมง)
   แยก logic ส่วนนี้ออกมาใช้ร่วมกัน ไม่คัดลอก; ไม่พบ = `unknown_symbol`, provider ตอบไม่ได้ = `dependency_unavailable`
3. `lastClosed` = วันก่อนวันปัจจุบันตามเวลา New York (จาก `Clock`); ตัด `to` ไม่ให้เกิน `lastClosed`
4. อ่าน coverage ของ (instrument, source ของ provider ที่ใช้อยู่) แล้วหาช่วงที่ขาด
   - ไม่มี coverage: ดึง `[from, to]`
   - `from < covered_from`: ดึง `[from, covered_from − 1]`
   - `to > covered_through`: ดึง `[covered_through + 1, to]`
   ช่วงที่ดึงต้องชนขอบช่วงเดิมเสมอ เพื่อให้ coverage ต่อเนื่อง
5. เรียก `DailyBars` นอก DB transaction แล้ว `Save` พร้อมช่วงที่ถามจริง
6. ถ้า provider ตอบไม่ได้: มีข้อมูลที่เก็บไว้ในช่วงที่ขอให้ตอบจากข้อมูลนั้นและระบุว่าข้อมูลถึงวันไหน พร้อม log error; ไม่มีเลยคืน `dependency_unavailable`
7. อ่าน `Range` แล้วคืน series พร้อม `source`, `is_demo`, `adjustment: raw` และขอบของข้อมูลที่มี

### Adapters

- **alpaca** (`internal/adapter/alpaca/daily_bars.go`): ใช้ `apiClient` เดิม, `timeframe=1Day`, `feed=sip`, `adjustment=raw`, `sort=asc`,
  `limit=10000`, อ่าน `next_page_token` โดยมีเพดานจำนวนหน้า, `start` = เที่ยงคืน New York ของ `from`,
  `end` = ค่าที่น้อยกว่าระหว่างสิ้นวัน New York ของ `to` กับ now − 15 นาที, ราคาเป็น `json.Number` ไม่ผ่าน float,
  ทิ้งแท่งที่วันที่ไม่เก่ากว่าวันปัจจุบันตามเวลา New York
- **demo** (`internal/adapter/demo/daily_bars.go`): สร้างแท่งจากสูตรที่ได้ค่าเดิมทุกครั้งต่อ (symbol, วันที่) เฉพาะวันจันทร์ถึงศุกร์
  ไม่จำลองวันหยุดตลาด; เพิ่ม QQQ ในรายชื่อ instrument และราคาเดโม; `is_demo: true`
- **postgres**: `queries/price_bars.sql` + `sqlc generate`, `price_bars.go` เป็น `PriceBarRepository`, เพิ่มใน `Store`

### HTTP

`GET /api/v1/instruments/{symbol}/bars?from=YYYY-MM-DD&to=YYYY-MM-DD` (operationId `getInstrumentBars`, tag Holdings/Prices)

- ต้องมี session; เป็น GET จึงไม่ต้องมี CSRF header
- `from` และ `to` บังคับ รูปแบบ `YYYY-MM-DD`
- response ตามตัวอย่างในข้อเสนอของ frontend: `symbol`, `instrument_name`, `timeframe: "1d"`, `adjustment: "raw"`, `currency: "USD"`,
  `source`, `feed`, `is_demo`, `data[]` ที่มี `date`, `open`, `high`, `low`, `close`, `volume` เป็น decimal string เรียงเก่าไปใหม่
  วันที่ไม่มีแท่งถูกเว้น ไม่ใส่ 0; เพิ่มฟิลด์บอกขอบของข้อมูลที่มี (`data_from`, `data_through`, เป็น `null` เมื่อไม่มีแท่ง)
- status: `400 invalid_input` (วันที่ผิดรูป, `from > to`, ช่วงเกิน 3,660 วัน, symbol ผิดรูป), `401`, `422 unknown_symbol`,
  `429 rate_limited` (โควตา symbol ใหม่), `503 dependency_unavailable` (provider ตอบไม่ได้และไม่มีข้อมูลที่เก็บไว้)
- `from` ก่อนปี 2016 ไม่ error แท่งแรกเริ่มช้ากว่าที่ขอ

## งาน

ทำตามลำดับ เขียน test ที่พิสูจน์พฤติกรรมก่อน implementation ในแต่ละข้อ

- [x] migration `00008_price_bars.sql` พร้อม Down, grants และ permission tests ของ runtime role (ไม่มี DELETE)
- [x] domain: `TradingDate`, `DailyBar`, `DailyBarBatch`, `BarCoverage`, `DailyBarSeries` พร้อม unit tests
- [x] ports: `PriceProvider.DailyBars`, `PriceBarRepository`, `PriceHistoryService`; เพิ่ม `PriceBars()` ใน `Store`
- [x] postgres: queries + sqlc + repository + integration tests กับ PostgreSQL จริง
- [x] alpaca adapter `DailyBars` + tests ด้วย fake HTTP server; เพิ่มกรณีรายวันใน smoke test ที่ใช้บัญชีจริง (`make smoke-alpaca`)
- [x] demo adapter `DailyBars` + QQQ + tests ว่าได้ค่าเดิมทุกครั้ง
- [x] service `PriceHistory` + unit tests ด้วย fake ของ ports และ `Clock`; แยก logic หา instrument ให้ใช้ร่วมกับ `Transactions`
- [x] HTTP handler, DTO, route, error mapping, Swagger annotations แล้ว `make swagger`
- [x] wiring ใน `cmd/api` (`wire.go`/`main.go`)
- [x] เอกสาร: `docs/database/schema.dbml`, spec หัวข้อ 6, 7, 8, 11, 12, roadmap (เพิ่มหัวข้องานหลังเฟส 1), `docs/frontend-handoff.md`, README
- [x] `make check` และ `make test-integration` ผ่าน
- [ ] เปลี่ยนสถานะ ADR-0024 เป็น Accepted เมื่อผู้ใช้ตรวจรับ

## Tests ที่ต้องมี

- **Repository:** เขียนซ้ำไม่เปลี่ยนแถวเดิม, แท่ง `demo` กับ `alpaca` ของ instrument เดียวกันไม่ปนกัน, coverage ขยายสองทิศและคงต่อเนื่อง,
  `Save` สองคำขอพร้อมกันไม่เสียข้อมูล, CHECK ปฏิเสธราคา 0/ติดลบ/`NaN` และ `high < low`, FK กันลบ instrument
- **Service:** ไม่มี coverage ดึงทั้งช่วง, ขาดด้านหน้า/ด้านหลัง/ทั้งสองด้านดึงเฉพาะส่วนที่ชนขอบ, อยู่ใน coverage ทั้งหมดไม่เรียก provider,
  `to` เป็นวันนี้หรืออนาคตถูกตัดเหลือ `lastClosed` (ทดสอบข้ามเที่ยงคืน New York และช่วงเปลี่ยนเวลา DST ด้วย `Clock` fake),
  ช่วงที่เป็นวันหยุดล้วนถูกบันทึกเป็น coverage และไม่ถาม provider ซ้ำ, provider ล่มตอนมี/ไม่มีข้อมูลที่เก็บไว้,
  symbol ใหม่ถูกตรวจและบันทึก, โควตา symbol ใหม่, symbol ที่ไม่มีจริง, ช่วงเกินเพดาน
- **Alpaca adapter:** `end` ไม่ใหม่กว่า now − 15 นาที, แปลง `t` เป็นวันที่ New York ทั้งช่วง EST และ EDT, pagination, ทิ้งแท่งของวันนี้,
  ราคาไม่ผ่าน float, `403`/`429`/`5xx`/body ไม่ใช่ JSON เป็น error, symbol ที่หายจากผลลัพธ์ได้ slice ว่าง, error ไม่มี key/secret
- **HTTP:** contract ของ response และทุก status ข้างบน, ไม่มี session ได้ 401, decimal เป็น string, เรียงเก่าไปใหม่,
  `BRK.B` ใน path ใช้ได้, route contract test และ Swagger ตรงกับ handler

## ความเสี่ยงและสิ่งที่ยอมรับ

- คำขอพร้อมกันที่ขาดข้อมูลเดียวกันอาจเรียก provider ซ้ำ ผลถูกเพราะเขียนแบบ idempotent
- ราคาปิดของวันนี้ขึ้นกราฟหลังเที่ยงคืนเวลา New York
- instruments ไม่ตาม symbol ที่ถูกเปลี่ยนชื่อ (ข้อจำกัดเดิมของ ADR-0008) แท่งของ symbol เดิมจึงหยุดเพิ่ม
- คำขอแรกของช่วงยาวต้องรอ provider อยู่ภายใต้ deadline ต่อคำขอของ ADR-0016
- ยังไม่ได้วัดขนาดตารางจริงเทียบโควตา Supabase Free

## สิ่งที่ตัดสินระหว่างลงมือ (ต่างจากแผนข้างบนหรือแผนไม่ได้ระบุ)

- **`data_from`/`data_through` คือช่วง coverage** (วันที่ตรวจกับ provider แล้ว) ไม่ใช่วันของแท่งแรก/สุดท้าย และเป็น `null` เมื่อยังไม่เคยตรวจเลย
  แผนเดิมเขียนว่า "null เมื่อไม่มีแท่ง" แต่ช่วงที่มีแต่วันหยุดก็บอกได้ว่าข้อมูลถึงวันไหน ซึ่งเป็นหน้าที่ของฟิลด์นี้เมื่อ provider ล่ม
- **เขียนแท่งด้วย sqlc `:batchexec`** (หนึ่ง round trip ต่อการบันทึก) แทน `unnest` หลาย array ซึ่ง sqlc อ่านไม่ได้ ผลคือ `sqlc.DBTX` มี `SendBatch` เพิ่ม
- **`UpsertPriceBarCoverage` ขยายช่วงเฉพาะเมื่อช่วงใหม่ชนหรือทับช่วงเดิม** (เงื่อนไขใน `ON CONFLICT ... WHERE`) เพื่อไม่ให้การบันทึกสองคำขอที่ชนกันแล้วจดช่องว่างเป็น "ดึงแล้ว" แท่งของช่วงที่ไม่ชนยังถูกเก็บ
- **`Save` ปฏิเสธแท่งที่ลงวันที่นอกช่วงที่ดึง และช่วงที่ to ก่อน from** เป็น internal error ก่อนเปิด transaction
- **adapter ปฏิเสธทั้งคำตอบ** เมื่อมีแท่งที่เก็บไม่ได้ตรงตัว คำตอบที่ไม่มีออบเจ็กต์ `bars` หรือ pagination เกิน 20 หน้า (แผนเดิมไม่ได้ระบุว่าทิ้งแท่งหรือ error) เพราะผู้เรียกจดช่วงนั้นว่าดึงแล้ว
- **`BarSource.Feed()`** อยู่ใน domain (alpaca = `sip`, demo = `demo`) เพื่อให้ตอบ `feed` ได้เมื่อตอบจากข้อมูลที่เก็บไว้โดยไม่เรียก provider
- **ดึงสองช่วงเรียงกัน** (ด้านหน้าก่อน) เมื่อขาดทั้งสองข้าง และหยุดที่ความล้มเหลวแรกของ provider
- **แยก `instrumentVerifier` ออกจาก `Transactions`** ให้ใช้ร่วมกัน พฤติกรรมของ `Transactions` ไม่เปลี่ยน (tests เดิมผ่านโดยไม่แก้)
- **เพิ่ม QQQ ในรายชื่อเดโม** (instrument และราคา 480) ตามแผน ข้อ benchmark ของขั้น 4
- **ไม่แตะ `Quotes` และ valuation**; ไม่เติมย้อนหลังตอนบันทึกรายการ (ทำพร้อมขั้น 3)

## ผลตรวจ (2026-11-11)

- `go vet`, `golangci-lint` (0 issues), `go mod tidy -diff`, `swagger validate` ผ่าน; `go test -race ./...` กับ PostgreSQL จริงผ่านทุกแพ็กเกจ
- `make check` และ `make sqlc-clean` จะผ่านเมื่อ commit ไฟล์ที่ generate แล้ว (ตอนนี้ `git diff --exit-code` เห็นไฟล์ใหม่ที่ยังไม่ commit)
- `make smoke-alpaca` ผ่านกับบัญชีจริง: `to` เป็นวันนี้ไม่ได้ 403, แท่งไม่ปรับ (AAPL ก่อน/หลัง split 4:1 ต่างกันราว 4 เท่า), BRK.B ใช้ได้, แท่งแรกคือ 2016-01-04
- ทดลองครั้งเดียวกับ Alpaca จริง + PostgreSQL จริง + service จริง (QQQ): ครั้งแรก 22 แท่งใช้ 1.6 วินาที, ครั้งที่สองจาก store 2 มิลลิวินาที, ครั้งที่สามขยายด้านหน้าเฉพาะส่วนที่ขาด; แท่งที่เก็บไว้ใน DB dev คือข้อมูลจริงของ QQQ
- `scripts/e2e/flow.sh` เพิ่มขั้นราคารายวัน (ตรวจ syntax แล้ว **ยังไม่ได้รัน** เพราะ stack dev ตั้ง `PRICE_PROVIDER=alpaca` แต่ flow เดิมคาดว่าเป็นเดโม)
