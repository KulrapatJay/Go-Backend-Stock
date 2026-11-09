# ส่งต่อให้ฝั่ง frontend (milestone 10)

เอกสารนี้สรุปสิ่งที่ frontend (Next.js) ต้องรู้เพื่อเชื่อม Go API ของเฟส 1 ให้ถูกต้อง
เขียนจาก backend ณ 2026-11-07 (branch `developer`, หลัง milestone 9 evidence)

**แหล่งความจริงของ contract คือ [`docs/api/swagger.json`](api/swagger.json)** (Swagger 2.0 ที่ generate จาก annotations)
หากเอกสารนี้ขัดกับ swagger.json ให้เชื่อ swagger.json และแจ้งให้แก้เอกสารนี้
ข้อกำหนดหลักของทั้งโปรเจกต์อยู่ใน [`AGENTS.md`](../AGENTS.md) ส่วนลำดับงานอยู่ใน [roadmap](superpowers/plans/2026-10-05-phase-1-roadmap.md) หัวข้อ milestone 10

## 0. เรื่องที่ยังไม่ได้ตัดสินใจ (ถามเจ้าของโปรเจกต์ก่อน ห้ามเลือกเอง)

1. **ที่อยู่ของโค้ด frontend:** roadmap และ AGENTS.md กำหนดให้อยู่ที่ `web/` ใน repo backend
   เพื่อให้ `docker compose up` เปิด web และ API บน origin เดียวกัน และให้ CI ตรวจทั้งสองส่วน
   แต่ตอนนี้มี repo แยก `next-frontend-stock` ถ้าจะแยก repo ต่อต้องเขียน ADR ใหม่และกำหนดว่า
   Compose, reverse proxy และ CI ของ web อยู่ที่ไหน
2. **Dividend:** `PRODUCT.md` ของ frontend ระบุ transaction type buy/sell/dividend
   แต่เฟส 1 ของ backend มีเฉพาะ `buy` และ `sell` (AGENTS.md ไม่รวม dividends, splits, mergers, symbol changes)
   ต้องตัด dividend ออกจาก UI เฟส 1 หรือวางแผนเป็น event ชนิดใหม่ในเฟสถัดไป
3. **ภาษา UI:** อีเมลจาก backend เป็นภาษาไทย ส่วน `message` ใน error เป็นภาษาอังกฤษสำหรับนักพัฒนา
   UI ควรแปลจาก `code` เอง (ดูหัวข้อ 6) แต่ภาษาหลักของ UI ยังไม่ได้ยืนยัน
4. **ก่อนเริ่ม:** milestone 9 ผ่านการตรวจรับแล้ว (2026-11-07) จึงเริ่ม milestone 10 ได้
   แต่ต้องได้คำตอบของข้อ 1–3 ข้างบนก่อนวางแผน milestone 10

## 1. นอกขอบเขตเฟส 1 (ห้ามทำหรือห้ามสมมติว่ามี)

- หน้าหุ้นรายตัว กราฟราคา ราคา live/pre-market/after-hours ([ADR-0012](adr/0012-defer-stock-page-and-charts.md))
- dividend, split, merger, การเปลี่ยน symbol, short selling, margin, FX, ยอดเงินสด, โอนหุ้นระหว่างพอร์ต
- การส่งคำสั่งซื้อขายจริง: ระบบนี้บันทึกรายการที่ผู้ใช้ทำที่อื่นเท่านั้น
- การตั้งรหัสผ่านให้บัญชี Google-only
- frontend เรียก Alpaca, Supabase หรือ data provider ใดๆ โดยตรง

## 2. การเชื่อมต่อ: same-origin เท่านั้น

backend **ไม่เปิด CORS** และทุก POST/PUT/DELETE ต้องมี header `Origin` ตรงกับ `PUBLIC_ORIGIN` ทุกตัวอักษร
browser จึงต้องเรียก API ผ่าน origin เดียวกับหน้าเว็บ:

```text
browser ──► https://<web origin>/            Next.js pages
        └─► https://<web origin>/api/v1/*    proxy ไป Go API (คง path เดิม ไม่ตัด prefix)
            https://<web origin>/docs/, /swagger.json, /swagger.yaml  proxy ไป Go เช่นกัน
```

- ใน Next.js ใช้ `rewrites` (หรือ reverse proxy ข้างหน้า) ส่ง `/api/v1/:path*` ไป Go โดยคง path เดิม
  ห้ามใช้ `NEXT_PUBLIC_API_URL` ที่ชี้ไป `http://localhost:8080` ให้ browser ยิงข้าม origin
- base path ของ API คือ `/api/v1` (ไม่ใช่ `/api`) ยกเว้น `/healthz` และ `/readyz`
- ค่าฝั่ง Go ที่ต้องเปลี่ยนเมื่อมี web (ผู้ดูแล backend เป็นคนแก้ใน `.env` และ Compose):
  - `PUBLIC_ORIGIN` = origin ของ web เช่น `http://localhost:3000` ใน dev
  - `GOOGLE_REDIRECT_URI` = `PUBLIC_ORIGIN` + `/api/v1/auth/google/callback` (API ไม่เริ่มถ้าไม่ตรง)
    และต้องเพิ่ม URI นี้ใน Google Cloud Console ด้วย
  - `TRUSTED_PROXY_CIDRS` = เครือข่ายของ Next/reverse proxy มิฉะนั้น API เห็นทุกคนเป็น IP เดียวกัน
    และ rate limit ต่อ IP จะนับรวมทุกผู้ใช้
- **ต้องทดสอบจริง** ว่า proxy ส่ง `Origin`, `Cookie`, `Idempotency-Key`, `If-Match`, `X-CSRF-Token` ไปถึง Go
  และส่ง `Set-Cookie`, `ETag`, `Retry-After`, `X-Request-ID`, `Location` (303 ของ Google) กลับมาครบ

### Server Components กับ API

- cookie ทั้งหมดเป็น HttpOnly: JavaScript ฝั่ง browser อ่านไม่ได้และห้ามพยายามอ่าน
- คำขอที่เปลี่ยนข้อมูลควรยิงจาก browser ผ่าน `/api/v1` บน origin เดียวกัน เพื่อให้ browser ใส่ `Origin` และ cookie เอง
- ถ้า Server Component หรือ Route Handler เรียก Go จากฝั่ง server ต้องส่งต่อ header `Cookie` ของผู้ใช้เอง
  และ POST จากฝั่ง server จะไม่มี `Origin` ของ browser จึงถูกปฏิเสธ ถ้าจะทำแบบนี้ให้เสนอ design ก่อน
- `proxy.ts` (route guard) ใช้ดูได้แค่ว่ามี cookie หรือไม่ ไม่ใช่หลักฐานว่า session ยังใช้ได้
  การตัดสินว่า login อยู่จริงต้องมาจาก `GET /api/v1/auth/me` (401 = ไม่มี session หรือหมดอายุ)

## 3. Cookies, session และ CSRF

| Cookie | dev (HTTP) | HTTPS | ใช้ทำอะไร |
| --- | --- | --- | --- |
| session | `session` | `__Host-session` | opaque session token อายุ 7 วันแบบ absolute (ไม่ต่ออายุตามการใช้งาน) |
| CSRF nonce | `csrf_nonce` | `__Host-csrf_nonce` | ผูก CSRF token ก่อน login |
| Google binding | `oidc_binding` | `__Host-oidc_binding` | ผูก Google flow กับ browser อายุ 10 นาที |

ทุก cookie เป็น `HttpOnly; SameSite=Lax; Path=/` ใช้ prefix `__Host-` เมื่อ `PUBLIC_ORIGIN` เป็น HTTPS
ดังนั้น `AUTH_COOKIE = "token"` ใน scaffold ปัจจุบันผิด ต้องรองรับทั้งสองชื่อตาม environment

**CSRF flow (ทุก POST/PUT/DELETE ยกเว้น Google callback):**

1. `GET /api/v1/auth/csrf` → `{ "csrf_token": "...", "expires_at": "2026-..." }` อายุ 1 ชั่วโมง
2. ส่ง `X-CSRF-Token: <csrf_token>` ใน header ของคำขอที่เปลี่ยนข้อมูล
3. **ขอ token ใหม่หลัง login และหลัง logout** เพราะ token ผูกกับ session (login แล้ว) หรือ nonce (ยังไม่ login)
4. ถ้าได้ `403 csrf_failed` ให้ขอ token ใหม่แล้วลองอีกครั้งหนึ่งครั้ง ถ้ายังไม่ผ่านให้แสดง error
   `csrf_failed` ใช้ทั้งกรณี token ผิด/หมดอายุ และ `Origin` ไม่ตรง `PUBLIC_ORIGIN`
   ถ้าได้ทุกครั้งแม้ token ใหม่ ให้ตรวจ proxy และ `PUBLIC_ORIGIN` ก่อน

`POST /auth/login` ตอบ `SessionResponse` `{expires_at, user}` ใช้เตือนผู้ใช้ก่อน session หมดได้
ส่วน `GET /auth/me` ตอบ `UserResponse` `{id, email, email_verified, created_at}` โดยไม่มี `expires_at`

## 4. Endpoints

ทุก path อยู่ใต้ `/api/v1` รายละเอียด request/response/status ทั้งหมดดู swagger.json ตาม `operationId`

| Method + path | operationId | สำเร็จ | หมายเหตุ |
| --- | --- | --- | --- |
| GET `/auth/config` | getAuthConfig | 200 `{google_enabled}` | ซ่อนปุ่ม Google เมื่อ `false` |
| GET `/auth/csrf` | getCSRFToken | 200 | ดูหัวข้อ 3 |
| POST `/auth/register` | register | 202 `OTPAcceptedResponse` | ตอบแบบเดียวกันแม้อีเมลมีบัญชีแล้ว ใช้เวลาอย่างน้อย ~0.5 วินาทีโดยตั้งใจ |
| POST `/auth/email-verification/request` | requestEmailVerification | 202 `OTPAcceptedResponse` | ขอรหัสใหม่ เว้น 60 วินาที ไม่เกิน 5 ครั้ง/ชม./อีเมล |
| POST `/auth/email-verification/confirm` | confirmEmailVerification | 204 | `{challenge_id, otp}` |
| POST `/auth/login` | login | 200 `SessionResponse` + Set-Cookie | 401 `invalid_credentials`, 403 `email_not_verified` |
| GET `/auth/me` | getCurrentUser | 200 `UserResponse` | 401 = ไม่ได้ login |
| POST `/auth/logout` | logout | 204 | ล้าง cookie |
| POST `/auth/password-reset/request` | requestPasswordReset | 202 `OTPAcceptedResponse` | ตอบแบบเดียวกันทุกอีเมล |
| POST `/auth/password-reset/verify` | verifyPasswordReset | 200 `{reset_token, expires_in}` | `{challenge_id, otp}` ตรวจรหัสก่อนตั้งรหัสผ่านใหม่ ยังไม่เปลี่ยนอะไรในบัญชี (ADR-0022) |
| POST `/auth/password-reset/confirm` | confirmPasswordReset | 204 | `{reset_token, new_password}` (แนะนำ) หรือ `{challenge_id, otp, new_password}` (แบบเดิม ยังใช้ได้) ห้ามส่งทั้งสองแบบพร้อมกัน แล้วทุก session ของบัญชีถูกออกจากระบบ |
| POST `/auth/google/start` | startGoogleSignIn | 200 `{authorization_url}` | 503 `feature_disabled` ถ้าไม่ได้ตั้ง Google |
| GET `/auth/google/callback` | completeGoogleSignIn | 303 | Google เรียก ไม่ใช่ frontend (ดูหัวข้อ 7) |
| POST `/auth/google/link` | linkGoogleAccount | 200 `{authorization_url}` | ต้อง login อยู่ |
| GET/POST `/portfolios` | listPortfolios / createPortfolio | 200 page / 201 + ETag | |
| GET/PUT/DELETE `/portfolios/{portfolio_id}` | getPortfolio / updatePortfolio / deletePortfolio | 200 + ETag / 200 + ETag / 204 | PUT/DELETE ต้องมี `If-Match` |
| GET/POST `/portfolios/{id}/transactions` | listTransactions / createTransaction | 200 page / 201 + ETag | POST ต้องมี `Idempotency-Key` |
| GET/PUT/DELETE `/portfolios/{id}/transactions/{transaction_id}` | getTransaction / updateTransaction / deleteTransaction | 200 + ETag / 200 + ETag / 204 | PUT/DELETE ต้องมี `If-Match` |
| GET `/portfolios/{id}/holdings` | listHoldings | 200 `{data: HoldingResponse[]}` | ไม่เรียกผู้ให้บริการราคา จึงเร็วและดูได้เสมอ |
| GET `/portfolios/{id}/valuation` | getValuation | 200 `ValuationResponse` | ตอบ 200 แม้ไม่มีราคา (ดูหัวข้อ 8) |

## 5. กติกาข้อมูลที่ frontend ทำผิดแล้วเสียหาย

### ตัวเลข (ADR-0003, ADR-0013)

- `quantity`, `price`, `fee`, `cost_basis`, `average_cost`, `realized_pnl`, `market_value`, `unrealized_pnl`
  และ `sequence` เป็น **decimal string เสมอ** ทั้งใน request และ response
- ส่งเป็น JSON number จะได้ `400 malformed_json`
- ห้ามแปลงเป็น JavaScript `number` เพื่อคำนวณหรือส่งกลับ ให้ใช้ไลบรารี decimal (เช่น decimal.js หรือ big.js)
  หรือเก็บเป็น string แล้วจัดรูปแบบเพื่อแสดงผลเท่านั้น; frontend ไม่คำนวณยอดพอร์ตเอง ใช้ค่าจาก API
- API ส่งค่าเต็มความละเอียด การปัดเป็นหน้าที่ UI: เงินแสดง 2 ตำแหน่ง (round-half-even)
  จำนวนหุ้นแสดงได้ถึง 10 ตำแหน่ง และค่าที่ปัดแล้วห้ามนำกลับไปส่ง API
- เพดาน input: ไม่เกิน 18 หลักก่อนจุดและ 10 หลักหลังจุด; `quantity` และ `price` > 0, `fee` ≥ 0 (ส่ง `"0"` ได้)
  ห้ามปัดค่าที่ผู้ใช้กรอกเงียบๆ ให้แสดง validation error แทน
- `sequence` เป็น string เพื่อไม่เสียความแม่นยำ ห้าม parse เป็น number

### เวลา

- ทุก timestamp เป็น RFC 3339 พร้อม timezone (`date-time`) เก็บเป็น UTC
- `executed_at` ที่ส่งต้องมี timezone ห้ามอยู่ในอนาคต และละเอียดไม่เกิน microsecond
  ถ้าฟอร์มรับเวลาท้องถิ่นของผู้ใช้ต้องแปลงพร้อม offset ให้ถูกก่อนส่ง
- ใน query string เครื่องหมาย `+` ของ offset ต้อง encode เป็น `%2B` (ใช้ `URLSearchParams` จะ encode ให้)

### Optimistic locking (ADR-0009)

- GET/POST/PUT ของ portfolio และ transaction ตอบ header `ETag: "<version>"` (มี double quote)
  ส่วน body มี `version` เป็นตัวเลขเดียวกัน
- PUT/DELETE ต้องส่ง `If-Match` เป็นค่า ETag ล่าสุดที่ผู้ใช้เห็น **รวม quote** เช่น `If-Match: "3"`
  - ไม่ส่ง → `428 precondition_required`
  - ไม่ตรง → `412 version_conflict` และไม่มีอะไรถูกเขียน: ให้บอกผู้ใช้ว่ามีการแก้ไขจากที่อื่น แล้วโหลดข้อมูลใหม่
  - `*`, `W/"3"` หรือหลายค่า → 400
- PUT เป็นการแทนที่ทั้งก้อน: portfolio ต้องส่งทั้ง `name` และ `description` (ส่ง `""` เพื่อล้าง)
  transaction ต้องส่งครบทั้งหกฟิลด์

### Idempotency-Key (ADR-0018)

- `POST /portfolios/{id}/transactions` ต้องมี `Idempotency-Key` (1–128 ตัวอักษร visible ASCII)
- สร้าง key ใหม่ **หนึ่งครั้งต่อการกดบันทึกหนึ่งครั้ง** (เช่น `crypto.randomUUID()` ตอนเปิดหรือส่งฟอร์ม)
  และใช้ key เดิมเมื่อ retry หลัง timeout, network error, `503 server_busy`
- key เดิม + payload เดิม = 201 พร้อม body เดิมโดยไม่บันทึกซ้ำ; key เดิม + payload ต่าง = `409 idempotency_conflict`
- key เก็บผลไว้ 24 ชั่วโมง เมื่อผู้ใช้แก้ฟอร์มแล้วส่งใหม่ให้ถือเป็นการบันทึกใหม่และสร้าง key ใหม่

### Pagination

- list ตอบ `{ "data": [...], "next_cursor": "..." | null }`; `limit` ค่าเริ่มต้น 20 สูงสุด 100
- `cursor` เป็นค่า opaque ห้าม parse และผูกกับ filter ชุดเดิม: เปลี่ยน filter ให้เริ่มจากหน้าแรก
- portfolio เรียงใหม่สุดก่อนตาม `created_at`; transaction เรียงตาม `executed_at` แล้ว `sequence` ใหม่สุดก่อน
- filter ของ transactions: `symbol`, `side` (`buy`|`sell`), `from`, `to` (date-time)

### Validation limits ที่ UI ควรตรวจก่อนส่ง

| ฟิลด์ | กติกา |
| --- | --- |
| password (register, reset) | printable ASCII ไม่มีช่องว่าง 6–32 ตัว ต้องมีพิมพ์เล็ก พิมพ์ใหญ่ และอักขระพิเศษ ห้ามมีอีเมล ห้ามเป็นรหัสยอดนิยม (blocklist ตรวจที่ server เท่านั้น) |
| portfolio `name` | 1–100 ตัวอักษร ซ้ำกันไม่ได้ในผู้ใช้เดียวกัน (ไม่สนตัวพิมพ์เล็ก/ใหญ่) → `409 portfolio_name_conflict` |
| portfolio `description` | ไม่เกิน 1000 ตัวอักษร |
| `symbol` | `^[A-Z][A-Z0-9.-]{0,14}$` หลังแปลงเป็นตัวพิมพ์ใหญ่ เช่น `BRK.B`; โหมดเดโมรู้จักเฉพาะ AAPL, MSFT, NVDA, GOOGL, AMZN, META, TSLA, SPY |
| OTP | ตัวเลข 6 หลัก เป็น string (ขึ้นต้นด้วย 0 ได้ ห้ามแปลงเป็น number) |

UI ตรวจเพื่อช่วยผู้ใช้เท่านั้น server ตรวจซ้ำเสมอและเป็นผู้ตัดสิน

## 6. Errors

ทุก error เป็น:

```json
{ "error": { "code": "invalid_input", "message": "...", "request_id": "0192f0c1-...", "details": ["password_too_short"] } }
```

- แสดงข้อความ UI จาก `code` (และ `details` เมื่อมี) ไม่แสดง `message` ดิบให้ผู้ใช้
- แสดง `request_id` (ตรงกับ header `X-Request-ID`) ในข้อความ error ทั่วไปเพื่อให้ค้น log ได้
- **ต้องรองรับ code ที่ไม่รู้จัก** ด้วยข้อความทั่วไป: v1 เพิ่ม code ใหม่ได้ ([ADR-0011](adr/0011-api-and-migration-evolution.md))
- `429` และ `503 server_busy` มี header `Retry-After` (วินาที) ให้แสดงเวลารอหรือ retry หลังเวลานั้น

| code | status | UI ควรทำ |
| --- | --- | --- |
| `invalid_input` | 400 | แสดงตาม `details` ข้างฟิลด์ |
| `malformed_json` | 400 | bug ฝั่ง frontend (เช่นส่งตัวเลขแทน string) |
| `invalid_verification`, `invalid_reset` | 400 | รหัสผิด/หมดอายุ/ใช้แล้ว ให้กรอกใหม่หรือขอรหัสใหม่ |
| `unauthenticated` | 401 | session หมด ไปหน้า login แล้วกลับมาหน้าเดิม |
| `invalid_credentials` | 401 | "อีเมลหรือรหัสผ่านไม่ถูกต้อง" ห้ามบอกว่ามีบัญชีหรือไม่ |
| `csrf_failed` | 403 | ขอ CSRF token ใหม่แล้วลองอีกครั้ง (ดูหัวข้อ 3 ข้อ 4) |
| `email_not_verified` | 403 | รหัสผ่านถูกแต่ยังไม่ยืนยันอีเมล พาไปขอรหัสใหม่ด้วย `/auth/email-verification/request` |
| `not_found` | 404 | ใช้ทั้งกรณีไม่มีอยู่และเป็นของผู้อื่น ห้ามแยกข้อความ |
| `portfolio_name_conflict` | 409 | ชื่อพอร์ตซ้ำ |
| `portfolio_not_empty` | 409 | ลบพอร์ตที่ยังมีรายการไม่ได้ ต้องลบรายการก่อน |
| `idempotency_conflict` | 409 | key ซ้ำกับรายการอื่น สร้าง key ใหม่ |
| `version_conflict` | 412 | มีการแก้จากที่อื่น โหลดใหม่ |
| `precondition_required` | 428 | bug ฝั่ง frontend (ไม่ได้ส่ง If-Match) |
| `insufficient_shares` | 422 | ขายเกินจำนวนที่ถือ ณ เวลานั้น |
| `invalid_history` | 422 | การแก้/ลบทำให้จำนวนหุ้นติดลบ ณ จุดใดจุดหนึ่งในประวัติ |
| `unknown_symbol` | 422 | ไม่พบ symbol (รวมกรณี provider เปลี่ยนชื่อ symbol เช่น FB → META) |
| `rate_limited` | 429 | รอตาม `Retry-After` |
| `dependency_unavailable` | 503 | ผู้ให้บริการภายนอกล่ม ลองใหม่ภายหลัง |
| `feature_disabled` | 503 | เช่น Google ไม่ได้ตั้งค่า |
| `server_busy` | 503 | ลองใหม่ด้วย `Idempotency-Key` เดิม |
| `internal`, `audit_write_failed` | 500 | error ทั่วไป พร้อม `request_id` |

core ยังมี code `access_denied` และ `identity_conflict` แต่ไม่มี endpoint ใดในเฟส 1 ส่งออกมา
(ผลการเชื่อม Google ที่ไม่สำเร็จไปทาง `?google=failed`) ถ้าเจอให้ใช้ข้อความทั่วไป

`details` ของรหัสผ่านที่มีตอนนี้: `password_too_short`, `password_too_long`, `password_contains_space`,
`password_non_ascii`, `password_missing_lowercase`, `password_missing_uppercase`, `password_missing_special`,
`password_contains_email`, `password_too_common` (อาจเพิ่มได้ ต้องมีข้อความ fallback)

## 7. Auth flows

### สมัครและยืนยันอีเมล

1. `GET /auth/csrf` → `POST /auth/register {email, password}` → 202 `{challenge_id, expires_in: 600, message}`
2. เก็บ `challenge_id` ไว้ฝั่ง client (เช่น state ของหน้า หรือ query) แล้วแสดงฟอร์มกรอกรหัส 6 หลัก
3. `POST /auth/email-verification/confirm {challenge_id, otp}` → 204 แล้วให้ไป login
4. ปุ่ม "ส่งรหัสใหม่" เรียก `/auth/email-verification/request {email}` ได้ `challenge_id` ใหม่ (รหัสเก่าใช้ไม่ได้แล้ว)
   ควร disable ปุ่ม 60 วินาที

ข้อความหลังสมัครต้องเหมือนกันทุกกรณี เพราะ API ไม่บอกว่าอีเมลนั้นมีบัญชีอยู่แล้วหรือไม่

### Login / logout

- `POST /auth/login` → 200 พร้อม Set-Cookie แล้ว **ขอ CSRF token ใหม่**
- `POST /auth/logout` → 204 แล้วขอ CSRF token ใหม่ก่อน POST ถัดไป
- การปิดแท็บไม่ใช่ logout; session หมดเองตาม `expires_at`

### ลืมรหัสผ่าน (สองขั้น, ADR-0022)

1. `POST /auth/password-reset/request {email}` → 202 `{challenge_id, ...}` ข้อความเหมือนกันทุกอีเมล
   (บัญชีที่ไม่มีหรือบัญชี Google-only จะไม่ได้อีเมล แต่ UI ต้องไม่บอก)
2. หน้า "กรอก OTP": `POST /auth/password-reset/verify {challenge_id, otp}` → 200 `{reset_token, expires_in: 600}`
   - รหัสผิด หมดอายุ ใช้แล้ว ครบ 5 ครั้ง หรือ `challenge_id` ไม่มีอยู่ ได้ 400 `invalid_reset` เหมือนกันทุกกรณี
     ให้แสดง error ที่หน้า OTP ทันที (ไม่ต้องแยกสาเหตุ เพราะ API ไม่บอก)
   - รหัสที่ถูกจะถูกใช้ไปแล้ว: กรอกรหัสเดิมซ้ำหรือส่งไป confirm แบบเดิมจะได้ `invalid_reset`
3. หน้า "ตั้งรหัสผ่านใหม่": `POST /auth/password-reset/confirm {reset_token, new_password}` → 204
   - เก็บ `reset_token` ใน state ของหน้าเท่านั้น (memory) ห้ามใส่ใน URL, localStorage, sessionStorage หรือ log
   - token ใช้ได้ครั้งเดียว อายุ 10 นาทีนับจากตอน verify ใช้ได้เฉพาะ browser เดิม (ผูกกับ cookie `csrf_nonce`
     หรือ session ถ้า login อยู่) อย่าล้าง cookie หรือ login/logout ระหว่างสองหน้า
   - ขอรหัสใหม่ (`request`) แล้ว token เดิมใช้ไม่ได้ทันที
   - token ผิด หมดอายุ ใช้แล้ว หรือมาจาก browser อื่น ได้ 400 `invalid_reset`: ให้กลับไปขอรหัสใหม่
   - รหัสผ่านไม่ผ่านกติกา ได้ 400 `invalid_input` พร้อม `details` (รวม `password_contains_email` ที่ตรวจหลังยอมรับ token)
     token ยังใช้ได้ต่อ ให้ผู้ใช้เลือกรหัสใหม่ที่หน้าเดิม
4. ทุก session ของบัญชีถูกออกจากระบบ ให้พาไปหน้า login
5. verify และ confirm ใช้โควตาเดียวกัน 20 ครั้ง/15 นาที/IP; เกินได้ 429 พร้อม `Retry-After`

แบบขั้นเดียว `POST /auth/password-reset/confirm {challenge_id, otp, new_password}` ยังใช้ได้ตาม ADR-0011
แต่ผู้ใช้จะรู้ว่ารหัสผิดหลังตั้งรหัสผ่านเสร็จแล้วเท่านั้น

### Google

1. `GET /auth/config` ถ้า `google_enabled: false` ให้แสดงว่าไม่พร้อมใช้ ห้ามทำ login จำลอง
2. `POST /auth/google/start` (มี CSRF) → `{authorization_url}` แล้ว `window.location.assign(authorization_url)` ในแท็บเดิม
3. Google ส่ง browser ไป `/api/v1/auth/google/callback` (ผ่าน proxy ไป Go) ซึ่งตอบ 303 ไปหน้าผลลัพธ์พร้อม `?google=`
   - `success` = login แล้ว (มี session cookie)
   - `linked` = เชื่อม Google กับบัญชีที่ login อยู่แล้ว
   - `link_required` = อีเมลนี้มีบัญชีอยู่แล้ว: ให้ login ด้วยรหัสผ่านก่อน แล้วเรียก `POST /auth/google/link` แทน start
   - `failed` = ยกเลิก หมดเวลา 10 นาที เปิด callback ซ้ำ หรือคนละ browser
4. **ตอนนี้ปลายทางยัง hardcode เป็น `/docs/?google=…`** (`internal/adapter/http/google.go`)
   frontend ต้องเสนอ path หน้าผลลัพธ์ แล้วให้ฝั่ง backend แก้ปลายทางพร้อม test, annotations และ generated docs
   ระบบไม่เชื่อมบัญชีอัตโนมัติเพราะอีเมลตรงกัน และบัญชี Google-only ใช้ "ลืมรหัสผ่าน" ไม่ได้

## 8. Holdings และ valuation: ห้ามแสดงสิ่งที่ข้อมูลไม่รองรับ (ADR-0020)

- `GET .../holdings` คำนวณจากรายการ ไม่ใช้ราคา จึงแสดง `quantity`, `cost_basis`, `average_cost`, `realized_pnl` ได้เสมอ
  รวมหุ้นที่ขายหมดแล้ว (quantity `"0"`) เพราะยังมี realized P&L
- `GET .../valuation` ใช้ **ราคาล่าช้า**: close ของแท่ง 1 นาทีล่าสุดของ regular session ก่อน "ตอนนี้ − 15 นาที"
  ต้องระบุใน UI ว่าเป็นราคาล่าช้า ไม่ใช่ราคาเสนอซื้อ/ขายหรือราคาที่ซื้อขายได้จริง
- **ราคาที่ไม่มีเป็น `null` ห้ามแสดงเป็น 0**:
  - `coverage`: `complete` (ทุกตัวที่ยังถือมีราคา) | `partial` | `unavailable`
  - เมื่อไม่ใช่ `complete`: `market_value` และ `unrealized_pnl` ระดับพอร์ตเป็น `null`
    ให้แสดง `priced_market_value` (รวมเฉพาะตัวที่มีราคา) พร้อมระบุว่ายอดไม่ครบ และแสดง `missing_symbols`
  - ราย position: `quote`, `market_value`, `unrealized_pnl` เป็น `null` ได้ โดย `unavailable_reason` เป็น
    `no_price` หรือ `provider_unavailable`
- `quote` แสดงที่มาและเวลา:
  - `as_of` = เวลาของแท่งราคา (ไม่ใช่เวลาที่ดึง) และ `fetched_at` = เวลาที่ API ดึง
  - `source`/`feed` เช่น `alpaca`/`sip` หรือ `demo`/`demo`
  - `is_demo: true` = ราคาตัวอย่าง ต้องติดป้าย "ข้อมูลเดโม" ชัดเจน
  - `stale: true` = ราคาเก่ากว่าที่ควร; `cached: true` = ใช้ราคาที่ API เก็บไว้
  - `session_status`: `open` | `market_closed` (ใช้แท่งปิดของ session ล่าสุด ต้องแสดงวันเวลาจริง ไม่ทำเหมือนอัปเดตวันนี้)
    | `unknown` (อ่านปฏิทินตลาดไม่ได้ ราคาอาจมาจากนอก regular session)
- unrealized P&L ไม่หักค่าธรรมเนียมขายในอนาคต
- หน้าพอร์ตต้องระบุว่าไม่รองรับ dividends/splits/mergers/symbol changes จึงอาจคำนวณไม่ครบ
  และผลคำนวณใช้ moving weighted-average cost ไม่ใช่รายงานภาษี

## 9. สิ่งที่ต้องแก้ใน scaffold `next-frontend-stock` ปัจจุบัน

| ไฟล์ | ปัญหา |
| --- | --- |
| `.env.example`, `.env.local`, `src/lib/env.ts` | `NEXT_PUBLIC_API_URL=http://localhost:8080/api` ยิงข้าม origin และ base path ผิด ให้ใช้ `/api/v1` บน origin เดียวกันผ่าน rewrites |
| `src/lib/constants.ts`, `src/proxy.ts` | cookie `token` ไม่มีอยู่จริง ใช้ `session` / `__Host-session` |
| `src/lib/api.ts` | ไม่ส่ง `X-CSRF-Token`, ไม่รองรับ `ETag`/`If-Match`/`Idempotency-Key`/`Retry-After`, อ่าน error ผิดชั้น |
| `src/types/index.ts` | `ApiErrorBody` ต้องเป็น `{ error: { code, message, request_id, details? } }` |
| `PRODUCT.md` | มี dividend และบอกว่าแหล่งราคา/วิธีคำนวณยังไม่รู้ ซึ่งตอนนี้กำหนดแล้ว (หัวข้อ 0, 8) |

## 10. งานฝั่ง backend ที่ค้างสำหรับ milestone 10

ห้าม frontend สร้าง business logic ซ้ำเพื่อแก้ปัญหาเหล่านี้ ให้แจ้งฝั่ง backend แก้พร้อม regression test:

- เปลี่ยนปลายทาง Google callback จาก `/docs/?google=` เป็นหน้า frontend
- Swagger `basePath` ตายตัวเป็น `/` ทำให้ Try it out หลัง proxy ที่มี prefix ยิงผิด URL
- ตั้ง `PUBLIC_ORIGIN`, `GOOGLE_REDIRECT_URI`, `TRUSTED_PROXY_CIDRS` ให้ตรงกับ origin ของ web
- เพิ่ม web service, web Dockerfile และ web CI ตามที่ตัดสินใจในหัวข้อ 0 ข้อ 1

## 11. เปิด backend เพื่อพัฒนา frontend

```bash
cd Go-Backend-Stock
cp .env.example .env      # แล้วแก้ PUBLIC_ORIGIN ให้เป็น origin ของ web เมื่อเชื่อมผ่าน proxy
docker compose up --build
make demo-data            # บัญชีเดโมพร้อมสองพอร์ต (ดู README สำหรับอีเมล/รหัสผ่าน)
```

- Swagger UI: `http://localhost:8080/docs/`; Mailpit (อ่าน OTP): `http://localhost:8025`
- ตัวอย่าง curl ครบ flow อยู่ใน [README](../README.md) หัวข้อ "ลอง flow ของ auth ด้วย curl"
- type ของ TypeScript ควร generate จาก `swagger.json` แทนการเขียนเอง (เป็น Swagger 2.0 เครื่องมือรุ่นใหม่บางตัว
  ต้องแปลงเป็น OpenAPI 3 ก่อน) และ generate ใหม่ทุกครั้งที่ backend เปลี่ยน spec
