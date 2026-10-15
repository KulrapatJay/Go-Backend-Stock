# Phase 1 — แบบระบบ Backend สำหรับตรวจยืนยัน

**สถานะ:** milestone 0 ผ่านการตรวจรับเมื่อ 2026-10-14 — ผู้ใช้ยืนยันแบบทั้งหมดรวม email verification, portfolio name uniqueness และกติการหัสผ่าน ยังไม่ใช่ implementation

**ข้อกำหนดหลัก:** [AGENTS.md](../../../AGENTS.md) และ [roadmap](../plans/2026-10-05-phase-1-roadmap.md)

## 1. เป้าหมายและขอบเขต

สร้างระบบบันทึกการซื้อขายหุ้นสหรัฐฯ สำหรับ portfolio ผลงาน ผู้ใช้สมัคร/login สร้างหลายพอร์ต บันทึกรายการย้อนหลัง และดูต้นทุน/กำไรขาดทุนเป็น USD ได้ ตรวจ backend ผ่าน HTTP, Swagger และ tests ให้ครบก่อนเริ่ม Next.js ไม่ส่งคำสั่งซื้อขายจริง

สิ่งที่ตกลงแล้ว: Go/Fiber, Hexagonal/Clean Architecture, pgx/sqlc/goose, Supabase เฉพาะ Database ตอน deploy, custom cookie session, Google OIDC, OTP, audit, decimal และบริการฟรีเท่านั้น

ยืนยันแล้วเมื่อ 2026-10-14:

- Argon2id + semaphore จำกัดการ hash พร้อมกัน และรหัสผ่าน printable ASCII ไม่มีช่องว่าง 6–32 ตัว (หัวข้อ 10)
- อายุ idempotency 24 ชั่วโมง, ลำดับรายการเวลาเท่ากันด้วย sequence, signed CSRF token, PKCE แบบ derive verifier, schema สำหรับ OIDC flow/rate limits, ตาราง goose ใน schema app, version pins, rate limits, Swagger 2.0 และขอบเขต middleware ตามที่เสนอในแบบนี้
- หน้าหุ้นรายตัว/กราฟเลื่อนไปเฟสถัดไป: เฟส 1 จบที่ holdings/valuation; เฟสถัดไปเริ่มด้วยหน้าหุ้นพร้อม daily bars แบบ delayed ก่อนเพิ่ม live/extended hours และต้องเสนอ timeframe/ช่วงประวัติ/PriceProvider method ก่อนลงมือ
- ชื่อพอร์ตซ้ำกันไม่ได้ภายในผู้ใช้คนเดียว (หัวข้อ 7)
- ออกแบบเพื่อดูแลและขยายในอนาคต (ยืนยัน 2026-10-14): instruments ใช้ surrogate key `id` และ transactions อ้าง `instrument_id`; optimistic locking ด้วย `version` + ETag/If-Match; กติกา API/migration evolution ใน AGENTS.md; บันทึกการตัดสินใจใน `docs/adr/`; replay รับลำดับ position events เพื่อรองรับ corporate actions ภายหลัง
- symbol ต้องมีอยู่จริงตาม provider: ตรวจผ่านตาราง `instruments` ที่เก็บหุ้นที่ตรวจแล้ว และเรียก provider เมื่อยังไม่เคยตรวจ (หัวข้อ 7–9)
- ต้องยืนยันอีเมลก่อน login ด้วยรหัสผ่านได้ และ register ตอบ 202 แบบเดียวกันทุกกรณีเพื่อไม่เปิดเผยว่ามีอีเมลในระบบ (หัวข้อ 9–11)
- กติการหัสผ่าน: ยาว 6–32 ตัว ห้ามช่องว่าง + blocklist + บังคับพิมพ์เล็ก/พิมพ์ใหญ่/อักขระพิเศษ (หัวข้อ 10)

ไม่รวม live/extended hours, dividends/splits, short/margin, FX, cash balance และการโอนหุ้นระหว่างพอร์ต

## 2. แนวทางและ trade-offs

| เรื่อง | แนวทางที่เสนอ | อีกทางเลือกและเหตุผลที่ยังไม่ใช้ |
| --- | --- | --- |
| ข้อมูลกับ audit | core กำหนด transaction ผ่าน UnitOfWork; repositories ผูกกับ transaction เดียว | repository command ที่รวม use case ทั้งชุดง่ายในช่วงแรก แต่ทำให้กติกาธุรกิจเสี่ยงไหลเข้า adapter |
| Holdings | replay transactions ด้วย pure function | เก็บ balances ซ้ำช่วยอ่านเร็ว แต่ต้องดูแลการแก้ย้อนหลังและ reconciliation เพิ่ม |
| Concurrency | row lock ระดับพอร์ต | แยก lock ต่อ symbol เพิ่ม concurrency แต่ซับซ้อนเมื่อแก้ symbol/ลบพอร์ต |
| Auth | opaque session ใน PostgreSQL | JWT ลดการอ่าน DB แต่ต้องเพิ่มกลไก revoke; ไม่ตรงกับข้อตกลงปัจจุบัน |
| Swagger | annotations + Swagger 2.0 จาก Swaggo stable | generator สาย OpenAPI 3 รุ่น RC แสดง cookie security ได้มากกว่า แต่เพิ่มความเสี่ยง compatibility ในรอบแรก |

Dependency: HTTP adapter → core services → core ports ← PostgreSQL/Google/Alpaca/SMTP adapters ประกอบทุก dependency ที่ `cmd/api/main.go`; maintenance มี composition root ของตัวเองที่ `cmd/maintenance/main.go`

## 3. โครงไฟล์ที่เสนอ

```text
cmd/
  api/main.go
  maintenance/main.go                 # retention cleanup แบบ one-shot
internal/
  config/config.go
  core/
    domain/                          # entities, commands, results, errors, replay
    port/                            # inbound contracts และ external boundaries
    service/                         # auth, otp, portfolio, transaction, valuation
  adapter/
    http/
      server.go, routes.go, errors.go, docs.go
      dto/                           # JSON และ Swagger annotations
      middleware/                    # request ID, access log, recovery, auth, CSRF
      assets/swagger-ui/             # versioned UI files และ license
    postgres/
      queries/                       # handwritten SQL แยกตาม feature
      sqlc/                          # generated; ห้ามแก้ด้วยมือ
      uow.go, roles_test.go           # repositories และ integration tests อยู่ที่นี่
    identity/google.go
    alpaca/provider.go
    demo/provider.go
    mail/smtp.go
migrations/                          # goose SQL; schema app เท่านั้น
deploy/local/initdb/                  # local roles bootstrap ไม่มี production secrets
docs/
  api/                               # generated docs.go/swagger.json/swagger.yaml
  superpowers/specs/
  superpowers/plans/
Dockerfile                           # dev/build/runtime/migrate targets
docker-compose.yml
docker-compose.override.yml
.env.example, .gitignore, .dockerignore, .air.toml
.github/workflows/ci.yml
go.mod, go.sum, sqlc.yaml, .golangci.yml, Makefile, README.md
```

สร้างแต่ละไฟล์เมื่อถึง milestone ที่ใช้งานจริง ไม่สร้าง empty interfaces หรือ handlers ล่วงหน้า และไม่มี `web/`, Node, web Dockerfile/service/CI จนถึง milestone 10

## 4. Versions และ Swagger

Baseline ที่เสนอจาก release/docs ทางการที่ตรวจวันที่ 2026-10-05:

| เครื่องมือ | Pin ที่เสนอ |
| --- | --- |
| Go | 1.27.1 |
| Fiber รวม built-in middleware | github.com/gofiber/fiber/v3 v3.5.0 |
| pgx | github.com/jackc/pgx/v5 v5.11.0 |
| PostgreSQL local/CI | 17.11; ตรวจ major ของ Supabase project จริงก่อน deploy และปรับ local/CI พร้อมกันหากต่าง |
| sqlc | v1.31.1 |
| goose | v3.28.0 |
| golangci-lint | v2.14.0 |
| Swaggo generator | github.com/swaggo/swag v1.16.6; output Swagger 2.0 |
| Swagger UI assets | swagger-api/swagger-ui v5.33.1 |
| Spec validator | go-swagger/go-swagger v0.34.1 |

นี่คือ pins สำหรับนำไปตรวจ build จริงใน milestone 1 ยังไม่อ้างว่า dependency graph หรือ toolchain ทดสอบผ่านแล้ว dependency อื่นและ image digest ต้อง lock เมื่อเพิ่มใน milestone ที่ใช้ ไม่ใช้ `latest` ใน Docker/CI หรือคำสั่งติดตั้งที่ commit

- Generate: `swag init -g cmd/api/main.go -d . --parseInternal -o docs/api --outputTypes go,json,yaml`
- Serve generated JSON/YAML และ bundled UI ผ่าน Fiber handler/static middleware ของ v3.5.0 จึงไม่ต้องพึ่ง Swagger wrapper ที่อาจผูก Fiber คนละ major
- `docs.go` ถูก generate/ตรวจใน CI แต่ API เสิร์ฟไฟล์ JSON/YAML โดยตรง ไม่ import generated global registry มาใช้เป็น application state
- `/docs` redirect ไป `/docs/`; assets อยู่ใต้ `/docs/`; spec อยู่ `/swagger.json`, `/swagger.yaml` ใช้ relative same-origin URL ไม่มี CDN และปิด online validator
- Swagger 2.0 ไม่มี `in: cookie`; ไม่ประกาศ security แบบที่ output ไม่รองรับ ไม่เปลี่ยนระบบเป็น Bearer JWT
- อธิบาย session cookie ใน operation descriptions; ระบุ `X-CSRF-Token` เป็น header parameter; เรียก `/api/v1/auth/csrf` แล้วให้ผู้ทดลองกรอก token ใน Try it out browser จัดการ HttpOnly cookie เอง
- CI generate/check diff, `swagger validate docs/api/swagger.yaml`, ตรวจ operationId ไม่ซ้ำ และตรวจ HTTP response จริงเทียบ schema/status ของ endpoint ที่มีแล้ว
- spec ที่เปิดทดลองมีเฉพาะ routes ที่ implement แล้ว ส่วนรายการ future endpoints อยู่ในแบบนี้ ไม่ประกาศว่าใช้งานได้แล้ว

แหล่งอ้างอิง: [Go](https://go.dev/dl/), [Fiber](https://github.com/gofiber/fiber/releases/tag/v3.5.0), [Fiber module](https://github.com/gofiber/fiber/blob/v3.5.0/go.mod), [PostgreSQL](https://www.postgresql.org/docs/release/), [pgx](https://github.com/jackc/pgx/releases/tag/v5.11.0), [sqlc](https://github.com/sqlc-dev/sqlc/releases/tag/v1.31.1), [goose](https://github.com/pressly/goose/releases/tag/v3.28.0), [golangci-lint](https://github.com/golangci/golangci-lint/releases/tag/v2.14.0), [Swaggo](https://github.com/swaggo/swag/releases/tag/v1.16.6), [Swagger UI](https://github.com/swagger-api/swagger-ui/releases/tag/v5.33.1), [validator](https://github.com/go-swagger/go-swagger/releases/tag/v0.34.1)

## 5. Middleware และเส้นทาง request

```text
Request ID → Access log → Recover → Security headers / optional CORS
  → Route-specific admission limits → Resolve session
  → Require auth (เฉพาะ protected routes) → CSRF/Origin (unsafe requests)
  → Handler/DTO → Use case → Repository transaction → Response
```

- ขั้น 1: request ID, structured access log, recover, error handler, body limit 64 KiB และ server timeouts
- สร้าง request ID เป็น UUIDv7 ใหม่ทุกคำขอ ตอบ `X-Request-ID`; ไม่เชื่อค่าจาก client มาสวมเป็น server ID
- Access log ใช้ route template, method, final status, duration, request ID; error handler ต้องทำงานก่อนบันทึก final status กรณี error/panic เพื่อไม่ log 200 ผิด ต้องมี HTTP test ยืนยัน
- Recover อยู่ภายใน access log เพื่อให้ log เห็น 500; ไม่ส่ง stack trace ให้ client ไม่เก็บ raw URL/query/body/cookies ใน log
- ขั้น 4: session/CSRF/Origin/login limits; ขั้น 5: OTP limits ที่ durable ใน DB
- Session middleware ส่ง Principal ที่มี user/session ID ให้ handler; service/query ตรวจ ownership อีกครั้ง ห้ามใช้ user_id จาก body
- API ใช้ same-origin เป็นค่าเริ่มต้นจึงปิด CORS; หากเพิ่ม origin ต้อง allowlist แบบ exact พร้อม credentials และจัดการ OPTIONS ก่อน auth
- API JSON ส่ง `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`; docs มี CSP ที่รองรับ bundled assets และไม่เปิด remote scripts
- ไม่ทำ audit ทุก request ใน middleware; business audit เกิดใน use case ส่วน security denial ส่งผ่าน audit port/service ด้วยค่าที่ copy แล้ว
- ไม่เก็บ Fiber context หรือข้อมูลอ้างอิงไปใช้ภายหลัง request และไม่ใช้ detached goroutine ทำ DB mutation

## 6. ER diagram

```text
users 1 ─── * auth_providers
users 1 ─── * sessions
users 1 ─── * portfolios 1 ─── * transactions * ─── 1 instruments
users 1 ─── * otp_challenges        (purpose: email_verification / password_reset)
users 0..1 ── * oauth_flows           (เฉพาะ flow เชื่อมบัญชี)
users 1 ─── * idempotency_keys * ─── 1 portfolios
rate_limit_keys 1 ─── * rate_limit_events

audit_events.actor_user_id / session_id / target_id
  = logical references ไม่ cascade และไม่ผูก FK ที่บังคับลบ audit
Holding / PortfolioValuation = computed values ไม่มีตาราง balances
```

## 7. Schema proposal

ใช้ schema `app`, เวลา `timestamptz` UTC, ID แบบ UUIDv7 ที่แอปสร้าง (เรียงตามเวลาจึงเขียน B-tree index ต่อท้ายได้ดีกว่า v4) เว้น sequence ที่ระบุ; ID เป็นตัวอ้างอิงที่ไม่ใช่ความลับและเผยเวลาสร้างได้ การเข้าถึงต้องผ่าน ownership check เสมอ และห้ามใช้ UUID แทน session/OTP/CSRF token ซึ่งต้องสุ่มจาก crypto/rand แยกต่างหาก; ทุก field เป็น NOT NULL ถ้าไม่ได้เขียน `?`; `created_at`/`updated_at` กำหนดจากเวลาที่ส่งเข้ามาอย่างสม่ำเสมอ ไม่เปิดให้ client กำหนด

| ตาราง | Columns |
| --- | --- |
| users | id uuid PK, email varchar(254) UNIQUE, password_hash text?, email_verified_at timestamptz?, created_at, updated_at |
| auth_providers | id uuid PK, user_id uuid FK users RESTRICT, provider text CHECK google, provider_subject varchar(255), created_at; UNIQUE(provider, provider_subject), UNIQUE(user_id, provider) |
| sessions | id uuid PK, user_id uuid FK users RESTRICT, token_hash bytea UNIQUE, created_at, expires_at, revoked_at?, revoke_reason text? CHECK logout/password_reset |
| portfolios | id uuid PK, user_id uuid FK users RESTRICT, name varchar(100), description varchar(1000), currency char(3) CHECK USD, version integer DEFAULT 1 CHECK > 0, created_at, updated_at |
| instruments | id uuid PK, symbol varchar(15) UNIQUE, provider_asset_id varchar(64)?, name varchar(200), exchange varchar(20), asset_class text CHECK us_equity, status text CHECK active/inactive, source text CHECK alpaca/demo, verified_at, created_at, updated_at |
| transactions | id uuid PK, portfolio_id uuid FK portfolios RESTRICT, sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE, instrument_id uuid FK instruments RESTRICT, side text CHECK buy/sell, quantity numeric(28,10), price numeric(28,10), fee numeric(28,10), executed_at, version integer DEFAULT 1 CHECK > 0, created_at, updated_at |
| otp_challenges | id uuid PK, user_id uuid FK users RESTRICT, purpose text CHECK email_verification/password_reset, otp_hash bytea, created_at, expires_at, failed_attempts smallint DEFAULT 0, consumed_at?, invalidated_at? (รวมสอง flow ที่มีโครง/query/cleanup เหมือนกันไว้ตารางเดียว ยืนยัน 2026-10-14) |
| oauth_flows | id uuid PK, purpose text CHECK login/link, user_id uuid? FK users RESTRICT, state_hash bytea UNIQUE, nonce_hash bytea, browser_binding_hash bytea, created_at, expires_at, consumed_at? |
| rate_limit_keys | key_hash bytea PK, scope text CHECK ตามรายการ scope ด้านล่าง, expires_at, last_denial_audit_at? |
| rate_limit_events | id bigint GENERATED ALWAYS AS IDENTITY PK, key_hash bytea FK rate_limit_keys RESTRICT, occurred_at |
| idempotency_keys | portfolio_id uuid FK portfolios RESTRICT, user_id uuid FK users RESTRICT, operation text CHECK transactions.create, key varchar(128), request_hash bytea, result jsonb, created_at, expires_at; PK(portfolio_id, user_id, operation, key) ขึ้นต้นด้วย portfolio_id เพื่อให้ FK check ตอนลบพอร์ตและ DeleteForPortfolio ใช้ index ได้ |
| audit_events | id uuid PK, occurred_at, event_type varchar(80), actor_user_id uuid?, request_id uuid, session_id uuid?, target_type varchar(40), target_id text?, outcome text CHECK success/failure/denied, reason_code varchar(80)?, metadata jsonb |

Constraints/indexes เพิ่มเติม:

- users.email: trim/lowercase ตามกติกาเดียวกันใน Go/DB, ยาว 1–254 ตัว, ไม่แก้ `+suffix`/จุด; password hash NULL ได้เฉพาะบัญชีภายนอกที่สร้างกับ auth_provider ใน transaction เดียว
- users.email_verified_at: NULL = ยังไม่ยืนยันอีเมล; ตั้งค่าเมื่อ confirm OTP ยืนยันอีเมลสำเร็จ, เมื่อ password reset สำเร็จ (พิสูจน์การเข้าถึงอีเมลแล้ว) หรือเมื่อสร้างบัญชีจาก Google ที่ email_verified=true; ไม่ย้อนกลับเป็น NULL
- portfolio name ต้องไม่ว่างและเก็บค่าที่ trim แล้ว (CHECK `name = btrim(name) AND length(name) > 0`); UNIQUE INDEX `(user_id, lower(name))` ทำให้ชื่อซ้ำแบบไม่สนตัวพิมพ์ภายในผู้ใช้เดียวกันไม่ได้ แต่ต่างผู้ใช้ใช้ชื่อเดียวกันได้; DB เป็นผู้ตัดสินความซ้ำ Go แปลง unique violation เป็น `portfolio_name_conflict` ทั้งตอนสร้างและเปลี่ยนชื่อ ลบพอร์ตแล้วนำชื่อกลับมาใช้ได้; description default empty string; currency เปลี่ยนไม่ได้
- quantity/price > 0, fee >= 0; numeric ปฏิเสธ NaN/Infinity; validate สูงสุด 18 หลักก่อนจุดและ 10 หลังจุดก่อน DB เพื่อไม่ให้ DB ปัด input เงียบ ๆ
- symbol normalize uppercase และตรวจ `^[A-Z][A-Z0-9.-]{0,14}$` ทั้งใน Go และ CHECK ของ instruments ก่อนเรียก provider
- instruments (ยืนยัน 2026-10-14): ตารางกลางของหุ้นที่ตรวจกับ provider แล้ว ใช้ร่วมกันทุกผู้ใช้ เพราะเป็นข้อมูลตลาดไม่ใช่ข้อมูลส่วนตัว; transactions อ้าง `instrument_id` จึงบันทึกได้เฉพาะ symbol ที่ตรวจแล้ว; ใช้ surrogate key เพราะ symbol เปลี่ยนได้ (เช่น FB → META) และ ticker อาจถูกนำกลับมาใช้กับบริษัทใหม่ — การเปลี่ยน symbol ในอนาคตจึงแก้แถวเดียวใน instruments ไม่ต้องแก้ประวัติรายการ; `provider_asset_id` เก็บ ID ถาวรของ provider (Alpaca asset id) UNIQUE ร่วมกับ source และต้องมีเมื่อ source=alpaca; เฟส 1 กำหนด symbol UNIQUE ทั้งตาราง ส่วนการรองรับ ticker ซ้ำของ instrument ที่ inactive ทำพร้อม corporate actions ในเฟสถัดไป; รับเฉพาะ asset class หุ้นสหรัฐฯ (us_equity) และรับทั้ง active/inactive เพื่อให้บันทึกรายการย้อนหลังของหุ้นที่หยุดซื้อขายแล้วได้หาก provider ยังมีข้อมูล; ไม่ลบแถว instruments และไม่ refresh เบื้องหลังในเฟส 1 (verified_at บอกเวลาตรวจล่าสุด); แถว source=demo มีได้เฉพาะ dev/test เพราะ production ปฏิเสธ demo provider
- ข้อจำกัดที่ยอมรับ: symbol ที่ยังไม่อยู่ใน instruments บันทึกไม่ได้ช่วง provider ล่ม (symbol ที่เคยตรวจแล้วบันทึกได้ตามปกติ) และหุ้นที่ provider ไม่มีข้อมูลแล้วบันทึกไม่ได้; ระบุใน README/UI
- sessions/OTP/OIDC/idempotency ต้อง expires_at > created_at; token/OTP/state/nonce hashes ยาว 32 bytes
- users.password_hash อยู่ในตาราง users ต่อไป (ยืนยัน 2026-10-14 ไม่แยกตาราง credentials) แต่ query ทั่วไปของ users เช่น current user/ownership ไม่ SELECT column นี้ มีเฉพาะ query ของ login/reset/register ที่อ่านหรือเขียน; domain.User และ User DTO ไม่มี field password hash และ HTTP test ตรวจว่า response ไม่มี hash
- sessions: CHECK `(revoked_at IS NULL) = (revoke_reason IS NULL)` และ revoke_reason อยู่ใน `logout`, `password_reset`; เพิ่ม reason ใหม่ต้องทำ migration
- rate_limit_keys.scope อยู่ในรายการ `login_email`, `login_ip`, `signup_ip`, `google_start_ip`, `email_verification_request_email`, `email_verification_request_ip`, `email_verification_confirm_ip`, `password_reset_request_email`, `password_reset_request_ip`, `password_reset_confirm_ip`, `instrument_lookup_user` (30 ครั้ง/ชั่วโมงต่อผู้ใช้ นับเฉพาะ symbol ที่ยังไม่อยู่ใน instruments เพื่อกันการยิง provider ด้วย symbol สุ่ม; adapter มี negative cache 10 นาทีสำหรับ symbol ที่ไม่พบ); cooldown 60 วินาทีกับโควตาต่อชั่วโมงเป็นสอง rules บน key เดียวกันของ scope `*_request_email`; เพิ่ม scope ใหม่ต้องทำ migration
- transactions.executed_at ต้อง `>= '1970-01-01T00:00:00Z'` และไม่อยู่ในอนาคต (ตรวจอนาคตใน Go ด้วย Clock เพราะ DB CHECK ห้ามใช้ now())
- otp_challenges.failed_attempts มี DB CHECK 0–20 เป็นเพดานตายตัว ส่วนจำนวนที่อนุญาตจริงมาจาก config (ค่าเริ่มต้น 5) ซึ่ง config validation ต้องปฏิเสธค่าที่น้อยกว่า 1 หรือเกิน 20; partial UNIQUE(user_id, purpose) WHERE consumed_at IS NULL AND invalidated_at IS NULL จึงมี challenge เปิดได้หนึ่งอันต่อ flow ต่อผู้ใช้; ก่อนสร้างใหม่ invalidate แถวเดิมของ purpose เดียวกันรวมแถวหมดอายุ; ทุก query ระบุ purpose เพื่อไม่ให้ challenge ID ของ flow หนึ่งใช้กับอีก flow ได้
- oauth_flows purpose link ต้องมี user_id และ purpose login ต้องเป็น NULL; จำกัด flow 10 นาทีและ consume ครั้งเดียว
- optimistic locking: portfolios.version และ transactions.version เริ่มที่ 1 และเพิ่มทีละ 1 ทุกครั้งที่ update สำเร็จ (ไม่เพิ่มเมื่อ idempotent replay); portfolio version ครอบเฉพาะ name/description ไม่เปลี่ยนเมื่อรายการในพอร์ตเปลี่ยน; ใช้คู่กับ portfolio row lock — lock ป้องกันการเขียนพร้อมกัน ส่วน version ป้องกันการเขียนทับข้อมูลที่ผู้ใช้ไม่ได้เห็น
- indexes: portfolios(user_id, created_at, id), UNIQUE portfolios(user_id, lower(name)), otp_challenges(user_id, purpose, created_at), UNIQUE instruments(symbol), UNIQUE instruments(source, provider_asset_id), transactions(portfolio_id, instrument_id, executed_at, sequence), transactions(portfolio_id, executed_at, sequence), sessions(user_id, expires_at), rate_limit_events(key_hash, occurred_at), audit_events(actor_user_id, occurred_at), audit_events(occurred_at), audit_events(request_id); expiration index ของตาราง cleanup
- transaction.sequence ไม่เปลี่ยนเมื่อ edit; replay เรียง executed_at ASC, sequence ASC ไม่เรียงตาม UUID และไม่ใช่ลำดับส่งจาก client
- จำกัด JSON audit metadata 8 KiB ด้วย Go validation และ DB CHECK; idempotency result เก็บเฉพาะ transaction representation ไม่เก็บ cookie/token/headers ทั้งหมด จำกัด 16 KiB
- audit ไม่มี FK แบบ cascade แม้ลบพอร์ต/transaction/session ก็ยังมี event ที่อ้าง logical ID เดิม
- ไม่มี account deletion ในเฟสนี้; ลบพอร์ตที่ว่างให้ลบ idempotency records ของพอร์ตใน transaction เดียวเพื่อไม่ชน FK พร้อม audit ของการลบพอร์ต

### Roles และ Supabase

`app_migrator` เป็นเจ้าของ app schema/tables ใช้ goose; `app_runtime` มีเฉพาะ CRUD ที่จำเป็นและ INSERT audit ไม่มี UPDATE/DELETE audit; `app_maintenance` อ่าน fields ที่ใช้ cleanup และ DELETE ตารางหมดอายุ ไม่มี DDL ไม่ใช้ runtime เป็น table owner/superuser/BYPASSRLS

roles/credentials bootstrap แยกจาก versioned schema migrations; local initdb สร้าง roles ตอน DB ว่าง ส่วน deploy ใช้ admin runbook provisioning ครั้งแรก ไม่แก้ managed schemas

ขั้น bootstrap (local `deploy/local/initdb/` และ Supabase runbook) สร้าง roles และ `CREATE SCHEMA app AUTHORIZATION app_migrator` พร้อม `REVOKE ALL ON SCHEMA app FROM PUBLIC` ก่อน goose รันครั้งแรก เพราะตาราง version ของ goose อยู่ใน schema นี้ goose รันด้วย `-table app.goose_db_version` เสมอ จึงไม่สร้าง `public.goose_db_version` ใน schema ที่ Supabase Data API อาจ expose. Migrations สร้าง/แก้เฉพาะ objects ภายใน `app` และใส่ GRANT ที่ runtime/maintenance ต้องใช้ใน migration เดียวกับตาราง runtime/maintenance ไม่มีสิทธิ์บนตาราง goose. SQL ใน sqlc queries ระบุ `app.` ทุกตาราง ไม่พึ่ง search_path. การรองรับชื่อตาราง goose แบบ schema-qualified ต้องพิสูจน์ใน milestone 1 หากไม่รองรับให้กลับมาเสนอทางเลือกก่อนลงมือ

เลือก private schema + explicit grants + ownership queries ในเฟสนี้ ไม่เปิด Data API และไม่ grant anon/authenticated ไม่ใช้ `auth.uid()` เพราะ session ของเราอยู่ใน Go; RLS ต่อผู้ใช้เป็นงานแยกที่ต้องออกแบบ context propagation ก่อนเพิ่ม ไม่เปิด policy ที่แสร้งว่าป้องกันแล้ว

Direct connection หรือ session pooler ตามเครือข่าย, TLS verify-full, runtime pool เริ่ม max 5 connections/instance และปรับตามโควตาจริง ใช้ migration credential แยก ไม่ย้ายไป transaction pooling โดยไม่ตรวจ pgx/goose

อ้างอิง: [connections](https://supabase.com/docs/guides/database/connecting-to-postgres), [Data API security](https://supabase.com/docs/guides/api/securing-your-api) เงื่อนไข free tier และเวอร์ชัน project จริงตรวจอีกครั้งใน milestone 11

## 8. Ports และชนิดข้อมูล

Signatures นี้เป็นข้อเสนอ interface ใน core ไม่ใช่ไฟล์โค้ดที่สร้างแล้ว ทุกชนิด `domain.*` เป็น plain Go value ไม่มี Fiber/pgx/sqlc/SDK types

ชนิดร่วม: `domain.ID` เป็น UUID string ที่ผ่าน validation, `domain.Principal` มี UserID/SessionID, `domain.RequestMeta` มี RequestID/MaskedIP/UserAgent; ไม่รับ actor ที่ client กำหนด `domain.Page` มี Limit/Cursor; Page result มี Items/NextCursor; `domain.Version` เป็น int32 ที่มากกว่าศูนย์

Commands/results อ้าง field contract ในหัวข้อ API: RegisterInput/LoginInput, ResetPasswordInput, PortfolioInput, TransactionInput; domain.Transaction เพิ่ม ID/PortfolioID/InstrumentID/Symbol/Sequence/Version/CreatedAt/UpdatedAt; domain.Portfolio มี Version; domain.Instrument มี ID/Symbol/ProviderAssetID/Name/Exchange/AssetClass/Status/Source/VerifiedAt; SessionGrant มี User, raw Token, ExpiresAt สำหรับ HTTP adapter ตั้ง cookie เท่านั้น ไม่ serialize/log raw Token

```go
type AuthService interface {
    Register(context.Context, domain.RegisterInput, domain.RequestMeta) (domain.OTPReceipt, error)
    Login(context.Context, domain.LoginInput, domain.RequestMeta) (domain.SessionGrant, error)
    Authenticate(context.Context, string) (domain.Principal, error) // raw cookie token
    CurrentUser(context.Context, domain.Principal) (domain.User, error)
    Logout(context.Context, string, domain.RequestMeta) error
    StartGoogle(context.Context, domain.OIDCStart, domain.RequestMeta) (domain.OIDCRedirect, error)
    CompleteGoogle(context.Context, domain.OIDCCallback, domain.RequestMeta) (domain.OIDCResult, error)
}
type EmailVerificationService interface {
    Request(context.Context, string, domain.RequestMeta) (domain.OTPReceipt, error) // resend
    Confirm(context.Context, domain.VerifyEmailInput, domain.RequestMeta) error
}
type PasswordResetService interface {
    Request(context.Context, string, domain.RequestMeta) (domain.OTPReceipt, error)
    Reset(context.Context, domain.ResetPasswordInput, domain.RequestMeta) error
}
type PortfolioService interface {
    Create(context.Context, domain.Principal, domain.PortfolioInput, domain.RequestMeta) (domain.Portfolio, error)
    List(context.Context, domain.Principal, domain.Page) (domain.PortfolioPage, error)
    Get(context.Context, domain.Principal, domain.ID) (domain.Portfolio, error)
    Update(context.Context, domain.Principal, domain.ID, domain.Version, domain.PortfolioInput, domain.RequestMeta) (domain.Portfolio, error)
    Delete(context.Context, domain.Principal, domain.ID, domain.Version, domain.RequestMeta) error
}
type TransactionService interface {
    Create(context.Context, domain.Principal, domain.ID, string, domain.TransactionInput, domain.RequestMeta) (domain.Transaction, error)
    List(context.Context, domain.Principal, domain.ID, domain.TransactionFilter) (domain.TransactionPage, error)
    Get(context.Context, domain.Principal, domain.ID, domain.ID) (domain.Transaction, error)
    Update(context.Context, domain.Principal, domain.ID, domain.ID, domain.Version, domain.TransactionInput, domain.RequestMeta) (domain.Transaction, error)
    Delete(context.Context, domain.Principal, domain.ID, domain.ID, domain.Version, domain.RequestMeta) error
}
type ValuationService interface {
    Holdings(context.Context, domain.Principal, domain.ID) ([]domain.Holding, error)
    Value(context.Context, domain.Principal, domain.ID) (domain.PortfolioValuation, error)
}
type Clock interface { Now() time.Time }
type PriceProvider interface {
    Quotes(context.Context, []string, time.Time) (domain.QuoteBatch, error) // symbols, cutoff
}
type InstrumentProvider interface { // external lookup: Alpaca Assets API หรือ demo list
    Lookup(context.Context, string) (domain.Instrument, error) // ErrUnknownSymbol / ErrDependencyUnavailable
}
type InstrumentRepository interface {
    BySymbol(context.Context, string) (domain.Instrument, error)
    Upsert(context.Context, domain.Instrument) (domain.Instrument, error) // คืนแถวจริงรวม ID; ชน symbol จาก request พร้อมกันให้คืนแถวที่มีอยู่
}
type IdentityProvider interface {
    AuthorizationURL(domain.AuthorizationRequest) (string, error)
    Exchange(context.Context, domain.CodeExchange) (domain.VerifiedIdentity, error)
}
type Mailer interface {
    SendEmailVerification(context.Context, domain.OTPMail) error
    SendRegistrationAttempt(context.Context, string) error // อีเมลนี้มีบัญชีอยู่แล้ว
    SendPasswordReset(context.Context, domain.OTPMail) error
    SendPasswordChanged(context.Context, string) error
}
type UnitOfWork interface {
    WithinTransaction(context.Context, func(TxRepositories) error) error
}
type TxRepositories interface {
    Users() UserRepository
    Sessions() SessionRepository
    Identities() IdentityRepository
    OTPChallenges() OTPChallengeRepository // ตาราง otp_challenges ทั้งสอง purpose
    Portfolios() PortfolioRepository
    Transactions() TransactionRepository
    Idempotency() IdempotencyRepository
    Instruments() InstrumentRepository
    Audit() AuditRepository
}
type UserRepository interface {
    ByID(context.Context, domain.ID) (domain.User, error)
    ByEmail(context.Context, string) (domain.User, error)
    Lock(context.Context, domain.ID) (domain.User, error)
    Insert(context.Context, domain.User) error
    SetPassword(context.Context, domain.ID, string, time.Time) error
    MarkEmailVerified(context.Context, domain.ID, time.Time) error // no-op ถ้ายืนยันแล้ว
}
type SessionRepository interface {
    ByTokenHash(context.Context, []byte) (domain.Session, error)
    Insert(context.Context, domain.Session) error
    Revoke(context.Context, domain.ID, time.Time, string) (bool, error)
    RevokeAll(context.Context, domain.ID, time.Time, string) (int64, error)
}
type IdentityRepository interface {
    BySubject(context.Context, string, string) (domain.AuthProvider, error)
    Insert(context.Context, domain.AuthProvider) error
}
type OIDCFlowRepository interface {
    Insert(context.Context, domain.OIDCFlow) error
    Consume(context.Context, []byte, []byte, time.Time) (domain.OIDCFlow, error)
}
type OTPChallengeRepository interface { // ทุก method ที่อ่าน/แก้ challenge ระบุ purpose
    ByID(context.Context, domain.OTPPurpose, domain.ID) (domain.OTPChallenge, error)
    Lock(context.Context, domain.OTPPurpose, domain.ID) (domain.OTPChallenge, error)
    InvalidateOpen(context.Context, domain.ID, domain.OTPPurpose, time.Time) error // userID
    Insert(context.Context, domain.OTPChallenge) error
    RecordFailure(context.Context, domain.OTPPurpose, domain.ID) error
    Consume(context.Context, domain.OTPPurpose, domain.ID, time.Time) error
}
type PortfolioRepository interface {
    Insert(context.Context, domain.Portfolio) error
    GetOwned(context.Context, domain.ID, domain.ID) (domain.Portfolio, error)
    LockOwned(context.Context, domain.ID, domain.ID) (domain.Portfolio, error)
    ListOwned(context.Context, domain.ID, domain.Page) (domain.PortfolioPage, error)
    UpdateOwned(context.Context, domain.Portfolio, domain.ID) (domain.Portfolio, error) // version = version + 1
    DeleteOwnedEmpty(context.Context, domain.ID, domain.ID) error
}
type TransactionRepository interface {
    History(context.Context, domain.ID, domain.ID) ([]domain.Transaction, error)
    List(context.Context, domain.ID, domain.ID, domain.TransactionFilter) (domain.TransactionPage, error)
    Get(context.Context, domain.ID, domain.ID, domain.ID) (domain.Transaction, error)
    Insert(context.Context, domain.ID, domain.Transaction) (domain.Transaction, error)
    Update(context.Context, domain.ID, domain.Transaction) (domain.Transaction, error) // version = version + 1
    Delete(context.Context, domain.ID, domain.ID, domain.ID) error
}
type IdempotencyRepository interface {
    Get(context.Context, domain.IdempotencyScope, string) (domain.IdempotencyRecord, error)
    DeleteExpired(context.Context, domain.IdempotencyScope, string, time.Time) error
    Save(context.Context, domain.IdempotencyRecord) error
    DeleteForPortfolio(context.Context, domain.ID, domain.ID) error
}
type AuditRepository interface {
    Append(context.Context, domain.AuditEvent) error
}
type RateLimitRepository interface {
    Admit(context.Context, []domain.RateLimitRule, time.Time) (domain.RateLimitDecision, error)
}
```

ID arguments เรียง ownerID, portfolioID, transactionID เมื่อมีหลาย ID; service Create transaction รับ portfolioID แล้ว idempotency key. TransactionFilter = Page + optional Symbol/Side/From/To; ตรวจช่วง From <= To

`OIDCStart` = Purpose, optional Principal, BrowserBinding; `OIDCRedirect` = URL, BrowserBindingCookie, ExpiresAt. `OIDCCallback` = Code, State, BrowserBinding, optional current Principal; `OIDCResult` = Purpose, optional SessionGrant. AuthorizationRequest มี State/Nonce/RedirectURI/CodeChallenge (S256); CodeExchange มี Code/CodeVerifier/RedirectURI; VerifiedIdentity มี Issuer/Subject/Email/EmailVerified/Nonce ที่ adapter ตรวจลายเซ็นและ claims พื้นฐานแล้ว service ตรวจ nonce กับ flow อีกครั้ง

`OTPReceipt` = ChallengeID/ExpiresIn ใช้ทั้ง register, ขอรหัสยืนยันอีเมลใหม่ และ password reset; `OTPMail` = To/OTP/ChallengeID/ExpiresAt ห้าม log; `OTPChallenge` = ID/UserID/Purpose/OTPHash/CreatedAt/ExpiresAt/FailedAttempts/ConsumedAt/InvalidatedAt โดย Purpose เป็นชนิด `domain.OTPPurpose` ค่า `email_verification` หรือ `password_reset` ตรงกับ CHECK ของ otp_challenges และถูกรวมใน HMAC เพื่อไม่ให้รหัสของ flow หนึ่งใช้กับอีก flow ได้; `VerifyEmailInput` = ChallengeID/OTP. `QuoteBatch` แยก Quotes ตาม symbol และ UnavailableSymbols; `PriceQuote` มี Price/Currency/AsOf/FetchedAt/Source/Feed/IsDemo/Stale/Cached/SessionStatus. `IdempotencyScope` = UserID/PortfolioID/Operation; record มี Key/RequestHash/Result/ExpiresAt. RateLimitRule = Scope/KeyHash/Limit/Window; decision = Allowed/RetryAfter/RecordDenialAudit โดย flag สุดท้ายได้จาก atomic update ของ last_denial_audit_at

Repositories ที่อ่านนอก transaction inject ได้เป็น concrete adapters ผ่าน port เดิม ส่วน mutations ที่ต้อง atomic ใช้เฉพาะ repositories จาก callback. `Lock*` ใช้ได้เฉพาะ transaction-bound instance; ห้ามเปิด nested transactions เอง

ไม่สร้าง PasswordHasher/UUIDFactory/logger interface โดยไม่มีเหตุผล: crypto helper ใช้ concrete implementation, randomness ใช้ crypto/rand และ slog inject เป็นค่า ไม่สร้าง abstraction ให้ทุกฟังก์ชัน

## 9. Atomicity, idempotency และ audit

### ซื้อขายและการแก้ย้อนหลัง

0. ก่อนเปิด DB transaction: normalize symbol แล้วอ่าน instruments; ถ้าไม่มี ให้ตรวจ rate limit `instrument_lookup_user` แล้วเรียก InstrumentProvider นอก lock — ไม่เจอหรือไม่ใช่ us_equity คืน 422 `unknown_symbol`, provider ล่ม/timeout คืน 503 `dependency_unavailable` โดยไม่ fallback ไปข้อมูลเดโม; การแก้รายการที่เปลี่ยน symbol ใช้ขั้นนี้กับ symbol ใหม่ และ retry ด้วย idempotency key เดิมไม่ต้องเรียก provider ซ้ำเพราะ symbol ถูกบันทึกแล้วตั้งแต่ครั้งแรก
1. Begin transaction → `LockOwned(ownerID, portfolioID)` ตรวจสิทธิ์และ serialize การแก้พอร์ต
2. สร้างรายการ: อ่าน idempotency scope `transactions.create`; require key 1–128 ตัว ASCII ที่พิมพ์ได้
3. ถ้า key ยังไม่หมดอายุและ hash ตรง คืน transaction representation ที่เก็บไว้ ไม่เขียน event ซ้ำ; hash ไม่ตรงคืน 409
4. แก้/ลบรายการหรือพอร์ต: หลัง lock ให้เทียบ version ปัจจุบันกับ If-Match ไม่ตรงคืน 412 `version_conflict` โดยไม่เขียนอะไร; Upsert instrument ที่ได้จากขั้น 0 (ถ้าเพิ่งตรวจ) ใน transaction เดียวกับรายการ เพื่อให้ FK ผ่านและไม่ทิ้งแถวที่ไม่มีรายการใช้หาก transaction rollback; อ่าน history ทั้งพอร์ต; mutate ใน transaction แล้ว replay ด้วย executed_at/sequence ของ DB จริง การ insert ก่อน replay ทำได้เพราะ rollback ทั้งหมดหากไม่ผ่าน
5. เขียน audit และ idempotency result แล้ว commit; ถ้า audit ล้มเหลว rollback ทุกอย่าง ไม่คืน success

Hash payload จาก normalized symbol/side/decimal canonical strings/UTC timestamp ไม่ hash raw JSON ที่อาจต่างเพียงลำดับ keys. Replay key เก็บ 24 ชั่วโมงจากการสร้างครั้งแรก ไม่ต่ออายุเมื่อ retry; key หมดอายุใช้สร้างใหม่ได้โดยลบ record เก่าใต้ portfolio lock ก่อน insert. แม้ transaction ถูก edit/delete แล้ว retry ในช่วงอายุ key ก็คืน snapshot เดิม ตาม contract ไม่สร้างรายการใหม่

ทุก mutation ของพอร์ต/transactions ใช้ lock เดียวกัน และ SQL join ตรวจ owner อีกชั้น; delete portfolio ปฏิเสธถ้ามีรายการ. ไม่ถือ lock ระหว่างเรียก Google/SMTP/Alpaca

### Auth และ OTP

- Signup อีเมลใหม่: hash password ก่อนเข้า transaction แล้ว insert user (email_verified_at NULL) + otp_challenges (purpose email_verification) + audit `auth.registered` และ `auth.email_verification_requested` ใน transaction เดียว ส่ง OTP หลัง commit; signup ไม่ login อัตโนมัติ
- Signup อีเมลที่มีบัญชีอยู่แล้ว (ทุกสถานะรวม Google-only): hash password ทิ้งเพื่อให้เวลาใกล้เคียงกัน ไม่แก้ข้อมูลบัญชีเดิม ไม่สร้าง challenge ส่งอีเมล "มีการสมัครด้วยอีเมลนี้" ที่แนะนำให้ login/ใช้ลืมรหัสผ่าน และตอบ 202 รูปแบบเดียวกับอีเมลใหม่พร้อม random challenge ID; ใช้ rate rules เดียวกันเพื่อไม่ให้ใช้ส่งอีเมลรบกวนได้
- Confirm email: อ่าน challenge เพื่อหา user ID → lock user → lock challenge → ตรวจ OTP/expiry/attempts → consume → `MarkEmailVerified` → audit `auth.email_verified` → commit; ไม่สร้าง session ผู้ใช้ login เอง
- ขอรหัสยืนยันใหม่: บัญชี local ที่ยังไม่ยืนยันเท่านั้นที่สร้าง challenge ใหม่ (invalidate อันเดิมแบบ atomic) และส่งอีเมล; unknown/ยืนยันแล้ว/Google-only ตอบ 202 แบบเดียวกันโดยไม่สร้าง record และไม่ส่งอีเมล
- Login: ตรวจ password นอก lock เพื่อลด lock duration จากนั้น lock user และตรวจว่า password hash ไม่เปลี่ยนก่อนสร้าง session/audit; ป้องกัน password reset แข่งกับ login ด้วยรหัสเก่า
- Login ด้วย password ที่ถูกต้องแต่ email_verified_at เป็น NULL: ไม่สร้าง session, audit `auth.login_failed` reason `email_not_verified` actor NULL และคืน 403 `email_not_verified` ให้ UI พาไปขอรหัสยืนยันใหม่; ตรวจ verification หลังรหัสผ่านถูกเท่านั้น จึงไม่เปิดเผยสถานะบัญชีให้คนที่ไม่รู้รหัสผ่าน
- Reset: อ่าน challenge เพื่อหา user ID โดยยังไม่ถือเป็นการยืนยัน OTP; hash รหัสใหม่ก่อนเข้า transaction จากนั้น lock user → lock challenge และอ่านค่าปัจจุบันอีกครั้ง → ตรวจ OTP/expiry/attempts → consume → update hash → `MarkEmailVerified` (OTP ที่ส่งถึงอีเมลพิสูจน์การเข้าถึงแล้ว) → revoke sessions → audit ทั้งสอง event → commit. วิธีนี้ทำให้เจ้าของอีเมลตัวจริงยึดบัญชีที่มีคนอื่นสมัครด้วยอีเมลของตนไว้ก่อนกลับมาได้ โดยรหัสผ่านของผู้สมัครเดิมถูกแทนที่และ sessions ถูก revoke
- OTP ผิด: callback บันทึก attempts/audit แล้วคืน nil ให้ transaction commit ก่อน service คืน authentication error ที่เก็บแยกจาก storage error; ห้ามคืน domain failure จาก callback จนทำให้ attempt rollback
- failed login audit ใช้ actor NULL; logout Revoke คืน changed=false แล้วไม่เพิ่ม success audit ซ้ำ
- ส่ง OTP/notification หลัง commit ด้วย bounded timeout; failure log ไม่มี secrets ไม่ rollback password ที่สำเร็จแล้ว ไม่มี durable email queue ในเฟสนี้ ผู้ใช้ขอ OTP ใหม่ได้ตาม cooldown

### Audit retention

ใช้ event types จาก AGENTS.md รวม `auth.email_verification_requested`, `auth.email_verified` และ `auth.email_verification_failed` ที่เพิ่มสำหรับการยืนยันอีเมล; failed confirm บันทึก attempts/audit แบบเดียวกับ OTP reset; metadata allowlist 8 KiB, user-agent sanitize/truncate 256 bytes, IP mask IPv4 /24 และ IPv6 /48 ไม่บันทึก raw email/token/hash

เก็บ success events ทุกครั้งที่ mutation สำเร็จ ไม่มี sampling. Security denial ที่โดน admission limit ซ้ำให้รวมเหตุการณ์ตาม scope/key ในช่วง 1 นาทีอย่างชัดเจน เพื่อลด flood; rate decision ต้องบอกว่าเป็นการปฏิเสธครั้งแรกของช่วงเพื่อสร้าง `security.rate_limited` เท่านั้น ไม่อ้างว่ามี event ราย request ที่ถูก block ทุกครั้ง

`cmd/maintenance` ลบเป็น batch 1,000 แถว/transaction: audit เก่ากว่า 90 วัน, idempotency หมดอายุ, sessions หมดอายุเกิน 1 วัน, otp_challenges/oauth_flows หมดอายุเกิน 1 วัน, rate events เก่ากว่า window สูงสุด 1 ชั่วโมง แล้วลบ keys ที่ว่างและหมดอายุ ใช้ maintenance role เท่านั้น

Local เรียก one-shot เมื่อทดสอบ; production เสนอ GitHub Actions schedule วันละครั้งด้วย environment secret และ workflow_dispatch สำรอง ไม่รับรองเวลา schedule ตรงวินาที ไม่เชื่อม DB จริงจาก PR CI; เปิด schedule หลังตรวจ network/free quota ใน milestone 11

## 10. Auth, CSRF และ rate limits

- Password hashing (ยืนยันแล้ว 2026-10-14): Argon2id memory 19 MiB, iterations 2, parallelism 1, salt 16 bytes, hash 32 bytes; PHC string เก็บ parameters และ salt เพื่อปรับค่าภายหลังได้ (rehash เมื่อ login สำเร็จด้วย parameters เก่า); benchmark ก่อนเปิดสาธารณะ
- Password rule (ยืนยันแล้ว 2026-10-14): printable ASCII ที่ไม่ใช่ช่องว่างเท่านั้น (0x21–0x7E) ยาว 6–32 ตัว ไม่ trim และไม่ normalize; ตรวจใน Go backend เสมอ ไม่พึ่ง validation ฝั่งฟอร์ม; กติกานี้ใช้กับ register และ password reset ส่วน login ตรวจเฉพาะความยาวสูงสุดก่อน hash เพื่อไม่ให้บัญชีเก่าที่ตั้งก่อนเปลี่ยนกติกา login ไม่ได้และไม่เปิดเผยกติกาผ่าน error ที่ต่างกัน
  - ต้องมีพิมพ์เล็ก (a–z) อย่างน้อย 1, พิมพ์ใหญ่ (A–Z) อย่างน้อย 1 และอักขระพิเศษอย่างน้อย 1 ตัวจาก ASCII punctuation 32 ตัว ``!"#$%&'()*+,-./:;<=>?@[\]^_`{|}~``; ไม่บังคับตัวเลข
  - Blocklist: ปฏิเสธรหัสที่เมื่อแปลงเป็นพิมพ์เล็กแล้วตรงกับรายการรหัสยอดนิยม/รั่วประมาณ 100,000 รายการ ซึ่ง bundle ใน binary ด้วย go:embed (ไม่เรียกบริการภายนอก ไม่ทำให้ CI พึ่ง network) โหลดเป็น set ครั้งเดียวตอนเริ่มระบบ; เลือกรายการที่ license อนุญาตให้แจกจ่าย เช่น SecLists (MIT) และ pin commit/ที่มาในไฟล์ NOTICE ใน milestone 4
  - ปฏิเสธรหัสที่มีส่วนหน้า @ ของอีเมลผู้ใช้ (ไม่สนตัวพิมพ์ เมื่อส่วนนั้นยาวอย่างน้อย 4 ตัว)
  - Error เป็น `invalid_input` พร้อม field reasons ที่ UI ใช้แสดงข้อความได้ตรงจุด: `password_too_short`, `password_too_long`, `password_non_ascii` (ให้ UI บอกให้ตรวจแป้นพิมพ์ภาษา), `password_contains_space`, `password_missing_lowercase`, `password_missing_uppercase`, `password_missing_special`, `password_too_common`, `password_contains_email`; คืนได้หลาย reason พร้อมกัน
  - เหตุผล/trade-off: ผู้ใช้เลือกความยาว 6–32 และกติกาตัวอักษรให้ตรงกับที่ผู้ใช้เว็บทั่วไปคุ้นเคย โดยยอมรับว่าขั้นต่ำ 6 ต่ำกว่าที่ NIST SP 800-63B กำหนด (อย่างน้อย 8) และ NIST ไม่แนะนำ composition rules หรือการห้ามช่องว่าง; ความเสี่ยงจากรหัสสั้นถูกลดด้วย blocklist, rate limits และ Argon2id แต่ไม่ถูกกำจัด; ทั้งหมดอยู่ใน validator เดียวใน core เพื่อปรับภายหลังได้โดยไม่แก้ schema
- Hash concurrency (ยืนยันแล้ว 2026-10-14): semaphore แบบ buffered channel ที่สร้างใน composition root และ inject ให้ hasher ครอบทุกการ hash/verify รวม dummy hash ของ login; `PASSWORD_HASH_CONCURRENCY` ค่าเริ่มต้น 4 (RAM สูงสุดราว 4 × 19 MiB) รอคิวได้ไม่เกิน 2 วินาทีหรือจน request context ถูกยกเลิก แล้วคืน 503 `server_busy` + `Retry-After: 1` และ application log ระดับ warn ไม่สร้าง audit; ไม่ถือ semaphore ขณะอยู่ใน DB transaction
- Session token สุ่ม 32 bytes, SHA-256 hash ใน DB, absolute 7 วัน; cookie `__Host-session` ใน HTTPS, HttpOnly/Secure/SameSite=Lax/Path=/ ไม่มี Domain; dev HTTP ใช้ชื่อ `session` และ Secure=false เฉพาะ dev
- Authenticate ตรวจ expiry/revoked ทุก request, DB ใช้ไม่ได้คืน 503 ไม่แปลงเป็น invalid password; logout ล้าง cookie และ revoke server session ครั้งเดียว
- `/auth/csrf` ออก signed token อายุ 1 ชั่วโมง ผูกกับ session ปัจจุบัน; ก่อน login ผูก random browser nonce ใน HttpOnly cookie อายุ 1 ชั่วโมง. HMAC ใช้ secret เฉพาะ CSRF; token มี salt/expiry/MAC ไม่เปิดเผย session token/hash
- unsafe methods ต้อง `X-CSRF-Token` + Origin ที่อยู่ใน allowlist; CLI/curl ส่งทั้งคู่ได้ หากไม่มี Origin ใช้ Referer origin ที่ตรวจ exact ได้ หากไม่มีทั้งสองปฏิเสธ 403. ทุก login/signup/reset/logout รวมอยู่ในกติกานี้
- หลัง login/logout ให้ขอ CSRF ใหม่; ห้ามรับ pre-login CSRF token สำหรับ session ใหม่
- Google เริ่มด้วย POST ที่ผ่าน CSRF คืน authorization_url ให้ browser navigate; callback GET ยกเว้น app CSRF โดยตรวจ state/nonce และ browser binding แทน
- OIDC flow เก็บเฉพาะ hash ของ state/nonce/binding, อายุ 10 นาที, consume ด้วย conditional UPDATE ก่อน exchange (transaction สั้น); exchange ล้มเหลวให้เริ่ม flow ใหม่ ไม่ replay code เดิม
- ใช้ PKCE S256 ร่วมกับ state/nonce: `code_verifier = base64url(HMAC-SHA-256(OIDC_SECRET, "pkce" ‖ state ‖ browser_binding))` (43 ตัวอักษร) คำนวณใหม่ตอน callback จาก state ใน query และ binding cookie จึงไม่ต้องเก็บ verifier ใน DB หรือ cookie เพิ่ม; ส่งเฉพาะ code_challenge ไป Google. Trade-off: หมุน OIDC_SECRET ระหว่าง flow ที่ค้างอยู่ทำให้ flow นั้นล้มเหลวและผู้ใช้ต้องเริ่มใหม่ ซึ่งยอมรับได้เพราะ flow อายุ 10 นาที
- Google login ใช้ provider+sub เป็นหลัก; ผู้ใช้ใหม่ต้อง email_verified; หาก email ชนบัญชีเดิมให้เข้าสู่บัญชีเดิมก่อนแล้ว explicit link ไม่ auto-link. การ link ต้องมี session ปัจจุบันตรงกับ user ที่เริ่ม flow และตรวจอีกครั้งก่อน commit
- Login ของอีเมลที่ไม่มีบัญชีหรือบัญชี Google-only (password_hash NULL) ต้องตรวจกับ Argon2id hash จำลองที่สร้างครั้งเดียวตอนเริ่มระบบด้วย parameters เดียวกัน แล้วคืน `invalid_credentials` แบบเดียวกัน เพื่อไม่ให้เวลาตอบเปิดเผยสถานะบัญชี; HTTP test เทียบ status/body และ integration test ตรวจว่าเส้นทางนี้เรียก hasher จริง
- Login rate: 10 attempts/15 นาทีต่อ normalized email และ 30/15 นาทีต่อ IP; OTP request: cooldown 60 วินาที, 5/ชั่วโมงต่อ email, 20/ชั่วโมงต่อ IP; OTP reset: 20/15 นาทีต่อ IP และไม่เกิน 5 wrong attempts/challenge
- Email verification ใช้ OTP 6 หลัก/HMAC/อายุ 10 นาที/กรอกผิด 5 ครั้งต่อ challenge และโควตาขอรหัสชุดเดียวกับ OTP request (cooldown 60 วินาที, 5/ชั่วโมงต่อ email, 20/ชั่วโมงต่อ IP) แต่ scope แยกจาก password reset; register นับรวมใน quota ขอรหัสของ email นั้นด้วย; confirm จำกัด 20/15 นาทีต่อ IP. HMAC ของ OTP รวม purpose/challenge ID/user ID
- Signup/Google-start จำกัด 20/ชั่วโมงต่อ IP; limiter เป็น rolling window ใช้ events และล็อก rate_limit_keys เรียง key_hash เพื่อ atomic admit หลาย key; commit quota แยกก่อนธุรกิจ ไม่คืน quota เมื่อ password ผิด
- rate key เป็น HMAC(scope + normalized email หรือ trusted client IP) ด้วย secret แยก; ไม่เก็บ raw email/IP. ตรวจ IP quota ก่อนสร้าง key ของ arbitrary email เพื่อลด storage abuse; trusted proxies ใช้เฉพาะ CIDRs ที่ตั้งไว้
- Request reset ตอบ 202 รูปแบบเดียวกันทั้ง local user/Google-only/unknown; ออก random challenge ID เหมือนกัน แต่ unknown/Google-only ไม่สร้าง reset DB record และไม่ส่งอีเมล ใช้ rate rules เดียวกัน พร้อม response timing floor/jitter และ tests ตรวจไม่เปิดเผยสถานะผ่าน body/status
- invalid/expired/consumed OTP ใช้ error เดียว `invalid_reset` (reset) หรือ `invalid_verification` (ยืนยันอีเมล); เมื่อ DB ไม่พร้อมคืน 503 อย่างสม่ำเสมอ
- Register ตอบ 202 รูปแบบเดียวกันทั้งอีเมลใหม่และอีเมลที่มีบัญชีแล้ว พร้อม response timing floor/jitter ชุดเดียวกับ reset จึงไม่ใช้ 409 `email_conflict` อีก; trade-off คือผู้ที่สมัครซ้ำโดยลืมว่ามีบัญชีจะรู้ผลผ่านอีเมลแทนหน้าจอ

อ้างอิง parameters: [OWASP Password Storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html) และ flow: [Google OpenID Connect](https://developers.google.com/identity/openid-connect/openid-connect)

## 11. API contract

Base path `/api/v1`; JSON UTF-8; UUID เป็น string; timestamp RFC3339 UTC ความละเอียดสูงสุด microseconds (ให้ตรง PostgreSQL); input เวลาที่ละเอียดเกินนี้ปฏิเสธแทนปัดเงียบ ๆ. Body ไม่รับ unknown fields, decimal input ต้องเป็น string รูปแบบเลขฐานสิบปกติ ไม่มี exponent, NaN หรือ Infinity. Error ทุกจุดใช้ envelope เดียวกัน

```json
{"error":{"code":"insufficient_shares","message":"Transaction would make holdings negative","request_id":"2bda4caf-3ba6-4b95-af73-0a204f282ae4"}}
```

| Domain error | HTTP |
| --- | --- |
| invalid_input / malformed_json | 400 |
| unauthenticated / invalid_credentials / invalid_reset / invalid_verification | 401 / 401 / 400 / 400 |
| csrf_failed / access_denied / email_not_verified | 403 |
| version_conflict / precondition_required | 412 / 428 |
| not_found (รวม resource ของผู้ใช้อื่น) | 404 |
| identity_conflict / idempotency_conflict / portfolio_not_empty / portfolio_name_conflict | 409 |
| insufficient_shares / invalid_history / unknown_symbol | 422 |
| rate_limited | 429 + Retry-After จำนวนวินาที |
| dependency_unavailable / feature_disabled / server_busy | 503 (server_busy มี Retry-After) |
| internal / audit_write_failed | 500 โดยไม่เปิด DB details |

| Method + path (ใต้ /api/v1 เว้นระบุ) | Auth | Input → output / success |
| --- | --- | --- |
| GET /healthz (root) | public | 200 `{status:"ok"}` process alive |
| GET /readyz (root) | public | DB ping budget 1s; 200 ready / 503 not_ready ไม่มี connection details |
| GET /docs/, /swagger.json, /swagger.yaml (root) | config | UI/spec; ไม่ข้าม auth ของ API |
| GET /auth/csrf | optional | 200 `{csrf_token,expires_at}` + preauth cookie เมื่อยังไม่ login |
| GET /auth/config | public | 200 `{google_enabled}` |
| POST /auth/register | public+CSRF | `{email,password}` → 202 `{message,challenge_id,expires_in:600}` รูปแบบเดียวกันทุกกรณี; ไม่ set session |
| POST /auth/email-verification/request | public+CSRF | `{email}` → 202 `{message,challenge_id,expires_in:600}` ขอรหัสยืนยันใหม่ |
| POST /auth/email-verification/confirm | public+CSRF | `{challenge_id,otp}` → 204; login เอง |
| POST /auth/login | public+CSRF | `{email,password}` → 200 `{user,expires_at}` + session cookie; ยังไม่ยืนยันอีเมล → 403 `email_not_verified` |
| GET /auth/me | required | 200 User |
| POST /auth/logout | optional+CSRF | ไม่มี body → 204 + clear cookie; ไม่มี session ก็ 204 ไม่เขียน logout ซ้ำ |
| POST /auth/google/start | public+CSRF | ไม่มี body → 200 `{authorization_url}` + browser binding cookie |
| POST /auth/google/link | required+CSRF | ไม่มี body → 200 `{authorization_url}`; flow ผูก current user |
| GET /auth/google/callback | state/binding | query code/state → 303 ไป fixed same-origin `/docs/` ใน backend stage; success login set cookie, link ไม่สร้าง session ใหม่ |
| POST /auth/password-reset/request | public+CSRF | `{email}` → 202 `{message,challenge_id,expires_in:600}` |
| POST /auth/password-reset/confirm | public+CSRF | `{challenge_id,otp,new_password}` → 204; login ใหม่เอง |
| POST /portfolios | required+CSRF | PortfolioInput → 201 Portfolio; ชื่อซ้ำ → 409 `portfolio_name_conflict` |
| GET /portfolios | required | cursor/limit → 200 PortfolioPage |
| GET /portfolios/{portfolio_id} | required | 200 Portfolio |
| PUT /portfolios/{portfolio_id} | required+CSRF+If-Match | PortfolioInput → 200 Portfolio + ETag ใหม่; ชื่อซ้ำ → 409 `portfolio_name_conflict` |
| DELETE /portfolios/{portfolio_id} | required+CSRF+If-Match | 204 เฉพาะพอร์ตว่าง |
| POST /portfolios/{portfolio_id}/transactions | required+CSRF | TransactionInput + Idempotency-Key → 201 Transaction; replay คืน 201/body เดิม |
| GET /portfolios/{portfolio_id}/transactions | required | limit/cursor/symbol/side/from/to → 200 TransactionPage |
| GET /portfolios/{portfolio_id}/transactions/{transaction_id} | required | 200 Transaction |
| PUT /portfolios/{portfolio_id}/transactions/{transaction_id} | required+CSRF+If-Match | TransactionInput ทั้งชุด → 200 Transaction + ETag ใหม่; ย้ายพอร์ตไม่ได้ |
| DELETE /portfolios/{portfolio_id}/transactions/{transaction_id} | required+CSRF+If-Match | 204 หาก history ที่เหลือถูกต้อง |
| GET /portfolios/{portfolio_id}/holdings | required | 200 `{data:[Holding]}` ไม่มี network price dependency |
| GET /portfolios/{portfolio_id}/valuation | required | 200 PortfolioValuation รวมราคาและ freshness/coverage |

User = `{id,email,email_verified,created_at}` ไม่ส่ง password_hash; PortfolioInput = `{name,description}` description optional default empty; Portfolio = input + `{id,currency:"USD",version,created_at,updated_at}`

TransactionInput ตัวอย่าง:

```json
{"symbol":"AAPL","side":"buy","quantity":"0.5","price":"200","fee":"1","executed_at":"2026-10-01T14:30:00Z"}
```

Optimistic locking: GET/POST/PUT ของ Portfolio และ Transaction ตอบ `ETag: "<version>"` (strong ETag) และมี field `version` ใน body; PUT/DELETE ต้องส่ง `If-Match: "<version>"` — ไม่ส่งคืน 428 `precondition_required`, ไม่ตรงคืน 412 `version_conflict` ให้ client โหลดข้อมูลใหม่; ไม่รองรับ `If-Match: *`; Swagger 2.0 ระบุ If-Match เป็น header parameter และ ETag เป็น response header; หากเปิด CORS ภายหลังต้อง expose ETag

Transaction response = input + `{id,portfolio_id,sequence,version,created_at,updated_at}`; symbol ใน response มาจาก instruments ปัจจุบัน; sequence เป็น decimal integer string เพื่อไม่เสียความแม่นยำใน frontend. Reject executed_at ในอนาคตเทียบ Clock.Now; ไม่มี trade scheduling

Lists = `{data:[],next_cursor:null}`; limit default 20 max 100, opaque base64url cursor ที่ผูก filters และ validate ขนาดไม่เกิน 1 KiB. Portfolio list เรียง created_at DESC/id DESC; transactions เรียง executed_at DESC/sequence DESC เพื่ออ่านล่าสุดก่อน (replay ใช้ ASC). ถ้ามี concurrent edits ระหว่างหน้าไม่รับรอง snapshot pagination

Holdings จัดกลุ่มตาม instrument_id แต่แสดง symbol ปัจจุบัน และแสดง symbols ที่เคยมีรายการรวม positions ปิดแล้วเพื่อเก็บ realized P&L; Holding = `{symbol,instrument_name,quantity,cost_basis,average_cost,realized_pnl}` ทุกตัวเลขเป็น string

PortfolioValuation = `{portfolio_id,currency,calculated_at,coverage,market_value,unrealized_pnl,realized_pnl,priced_market_value,missing_symbols,positions}`. coverage = complete/partial/unavailable; ถ้าขาดราคาหุ้นที่ยังถืออยู่ totals market_value/unrealized_pnl เป็น null และ priced_market_value คือ subtotal เท่านั้น ไม่แสดง subtotal ว่าเป็นยอดเต็ม. Position = Holding + `{quote,market_value,unrealized_pnl}`; quote null ได้และมี unavailable reason. พอร์ตว่าง/หุ้นที่ขายหมดมี market value/unrealized เป็น `"0"` โดยไม่ต้องดึงราคา

quote = `{price,currency,as_of,fetched_at,source,feed,is_demo,stale,cached,session_status}`. ทุก optional/null field ต้องระบุ `x-nullable`/description ตาม Swagger 2.0 และ contract tests ตรวจความหมายจริง ไม่ใช้ syntax OAS3 ปนกัน

ไม่มี public audit API หรือ maintenance HTTP endpoint ในเฟสนี้; ตรวจ audit ผ่าน integration tests/authorized SQL. Google callback ที่ provider ส่ง error ต้อง consume/ตรวจ state ก่อนตอบ error แบบปลอดภัย ไม่ echo code/token และไม่มี arbitrary return URL

## 12. การคำนวณและราคา

- Replay เป็น pure function ใน core ที่รับลำดับ `PositionEvent` ของหนึ่ง (portfolio, instrument) ที่เรียงแล้ว และคืนสถานะ position ทีละขั้น; เฟส 1 มี event ชนิดเดียวคือ trade (buy/sell) ที่ map จาก transactions เรียงด้วย (executed_at, sequence); อนาคตเพิ่ม split/dividend/symbol change เป็น event ชนิดใหม่จากตาราง corporate actions โดยกำหนดลำดับเมื่อเวลาเท่ากันใน ADR ของฟีเจอร์นั้น — ไม่ต้องแก้ตาราง transactions หรือเขียนการคำนวณใหม่; ห้ามให้ event ชนิดใหม่เปลี่ยนผลของประวัติที่มีแต่ trade
- Transactions เป็น source of truth, moving weighted average แยก portfolio/instrument; ซื้อเพิ่มต้นทุน quantity × price + fee; ขาย realized = proceeds − fee − allocated cost
- การหารใช้ decimal coefficient/integer quotient-remainder เพื่อ round-half-even ที่ 28 ตำแหน่ง ไม่ใช้ global DivisionPrecision หรือ `DivRound` โดยถือว่าเป็น half-even โดยไม่ตรวจ
- เมื่อขายหมด allocated cost = ต้นทุนคงเหลือทั้งหมด; quantity/cost/average cost กลับศูนย์และยังเก็บ realized
- ตัวอย่างทดสอบ: ซื้อ 2 หุ้น × 100 + fee 2 → ต้นทุน 202; ขาย 0.5 × 120 − fee 1 → allocated cost 50.5, realized 8.5, quantity 1.5, cost 151.5; ราคาอ้างอิง 110 → market value 165, unrealized 13.5
- Valuation อ่าน history เป็น snapshot ใน statement เดียวหรือ read-only transaction แล้วปิดก่อนเรียก provider; ส่ง calculated_at ของ snapshot ไม่อ้างว่า mutation ที่เกิดหลังจากนั้นรวมอยู่แล้ว
- Alpaca: historical SIP bars `1Min`, `adjustment=raw`, regular session เท่านั้น; เลือกแท่งที่จบไม่เกิน now−15 นาที (อย่าใช้ timestamp เริ่มแท่งเป็นเวลาจบ) ใช้ calendar/early close ของตลาดในการหาช่วงเวลาจริง
- `as_of` คือเวลาจบแท่ง, source timestamp เก็บแยกใน adapter ถ้าจำเป็น; ส่งค่า close เป็น decimal โดยไม่ parse ผ่าน float. ขอ batch และอ่าน pagination ให้ครบ ไม่ใช้ IEX ล่าสุดแล้วติดป้าย delayed SIP
- ขณะเปิดตลาด เปรียบเทียบกับแท่งที่คาดว่า complete ณ cutoff; stale เมื่อขาดเกิน 5 trading minutes. ก่อนเปิด/วันหยุดเทียบแท่งปิด regular session ล่าสุด; ถ้าครบส่ง market_closed และเวลาจริง ไม่บอกว่าเป็น live
- ถ้าข้อมูล calendar ไม่พร้อม ส่ง session_status unknown/stale=true และเวลาจริง ไม่เดาว่าวันนั้นเป็นวันทำการ. early-close/วันหยุดต้องมี fixtures
- Cache ใน adapter TTL 60s สูงสุด 500 symbols; bounded latest cache สำหรับ outage ไม่เกิน 7 วัน โดยติด stale/cached ตามจริง; ไม่มี cache และ provider ล้มเหลวคืน quote unavailable
- External request timeout 5s, valuation รวมไม่เกิน 10s; retry read สูงสุด 1 ครั้งเฉพาะ transient failure ภายใน budget เคารพ Retry-After ไม่ retry invalid credentials
- Demo provider deterministic ใน dev/test เท่านั้น `is_demo=true`; ไม่มี silent fallback จาก Alpaca
- Instrument lookup: Alpaca adapter ใช้ Assets API (`GET /v2/assets/{symbol}` ของ Trading API ซึ่งแยกจาก Market Data API) timeout 5 วินาที ไม่ retry 404; demo adapter ใช้รายชื่อตายตัว เช่น AAPL, MSFT, NVDA, GOOGL, AMZN, META, TSLA, SPY; สิทธิ์การใช้ Assets API ด้วยบัญชี paper/free ต้องตรวจด้วยบัญชีจริงใน milestone 8 ก่อนยืนยันว่าใช้ได้

สิทธิ์ historical SIP และ calendar ต้องตรวจด้วยบัญชีจริงก่อนเปิดออนไลน์ ตาม [Alpaca Market Data FAQ](https://docs.alpaca.markets/us/docs/market-data-faq) ไม่ถือว่าการอ่านเอกสารเท่ากับตรวจ entitlement แล้ว

## 13. Compose, config และ CI

| Service | หน้าที่ / dependencies |
| --- | --- |
| postgres | local PG 17.11, volume, pg_isready; local-only roles bootstrap |
| migrate | goose up one-shot หลัง postgres healthy; migration role |
| api | runtime image non-root, port 8080; รอ migrate exit 0; runtime DB role |
| mailpit | dev SMTP 1025/UI 8025, bind UI loopback; credentials จริงไม่เกี่ยวข้อง |

Dev override mount source/hot reload ด้วย Air; production ใช้ config ที่ไม่โหลด override/Mailpit และต่อ Supabase โดยไม่มี local postgres dependency. เพิ่ม web ใน milestone 10 และ proxy `/api/v1`, `/docs/`, `/swagger.json`, `/swagger.yaml` ไป Go บน origin เดียวกัน คง path เดิม

API config: APP_ENV, HTTP_ADDR, PUBLIC_ORIGIN, DATABASE_URL, MIGRATION_DATABASE_URL (เฉพาะ migrate), MAINTENANCE_DATABASE_URL (เฉพาะ job), TRUSTED_PROXY_CIDRS, LOG_LEVEL, CSRF_SECRET, OTP_SECRET, RATE_LIMIT_SECRET, OIDC_SECRET (PKCE verifier derivation), PASSWORD_HASH_CONCURRENCY, PRICE_PROVIDER, GOOGLE_CLIENT_ID/SECRET/REDIRECT_URI, ALPACA_KEY/SECRET, ALPACA_TRADING_BASE_URL (Assets API; ค่าเริ่มต้น paper endpoint), SMTP_*, DOCS_ENABLED, DOCS_TRY_IT_OUT_ENABLED

Production ปฏิเสธ secrets ตัวอย่าง/ว่าง, demo provider และ insecure cookies; local bootstrap สร้าง secrets สุ่มครั้งแรกไว้ใน ignored dev volume/file. `cp .env.example .env && docker compose up --build` ต้องเปิด API/docs/Mailpit โดยไม่ต้อง Google/Alpaca credentials

CI ตั้งแต่ขั้น 1: vet/lint/unit/HTTP tests, Swagger generate/validate/diff, API image build, baseline migration บน empty DB; ขั้น 3 เพิ่ม sqlc diff, repo tests/permissions/rollback; ขั้น 9 ครบ backend E2E. ไม่เพิ่ม Next/Node CI ก่อนขั้น 10

## 14. เกณฑ์ตรวจรับและลำดับถัดไป

| Milestone | หลักฐานที่ต้องมี |
| --- | --- |
| 0 | ผู้ใช้ตรวจแบบนี้และยืนยันขอบเขตกราฟ/ข้อเสนอรายละเอียดก่อนเริ่มโค้ด |
| 1 | Compose backend เปิดได้, migration failure ไม่ปล่อย API, health/readiness, final-status logging/recover, docs offline และ CI ของโครงระบบ |
| 2–3 | core ไม่ import adapter; decimal boundaries; migrations/sqlc; ownership/role/audit rollback ด้วย PG จริง |
| 4 | auth/OIDC/session/CSRF + email verification ผ่าน Mailpit; register ตอบเหมือนกันทุกกรณี, login ก่อนยืนยันถูกปฏิเสธ, callback replay, logout ไม่เพิ่ม audit ซ้ำ |
| 5 | password reset; login/reset race, counters ไม่ rollback, reset ตั้ง email_verified_at |
| 6–7 | If-Match ขาด/ไม่ตรง (428/412) และ version ไม่เพิ่มเมื่อ idempotent replay, portfolio name ซ้ำในผู้ใช้เดียว/ต่างผู้ใช้, cross-user isolation, concurrent sells, retries, แก้ย้อนหลัง/เปลี่ยน symbol, sequence ties |
| 8 | calculation golden examples, stale/missing/closed-market/early-close data, partial totals, demo flags |
| 9 | full backend flow + docs/curl/browser cookies, retention, README และ provider smoke เมื่อมี credentials |
| 10–11 | frontend เชื่อม API ที่ผ่านแล้ว จากนั้น full-stack verification/deploy/backup-restore |

เมื่อยืนยันแบบแล้ว แตก milestone 1 เป็นแผนลงมือที่มีไฟล์/tests/คำสั่งเฉพาะของโครง Go/Docker/CI/middleware/Swagger ไม่แตก implementation ของทั้งระบบในครั้งเดียว และทำใน task ปัจจุบันตามลำดับ

ขณะจัดทำแบบยังไม่มี Git repository, product code, dependency installation หรือ database project ที่สร้างขึ้น จึงยังไม่มี build/test/runtime compatibility result และไม่มี commit
