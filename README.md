# Stock Portfolio Tracker — Backend

Go API สำหรับบันทึกรายการซื้อขายหุ้นสหรัฐฯ และติดตามต้นทุน/กำไรขาดทุนของหลายพอร์ต
ระบบนี้บันทึกการลงทุนที่ทำผ่านช่องทางอื่น ไม่ใช่ระบบส่งคำสั่งซื้อขายจริง

> **สถานะ:** Milestone 4b — สมัครด้วยอีเมล+รหัสผ่าน, ยืนยันอีเมลด้วย OTP, login/logout และ session ฝั่ง server ใช้งานผ่าน API ได้แล้ว; Google login (4c), ลืมรหัสผ่าน (milestone 5), พอร์ตและรายการซื้อขายยังไม่เปิดใช้งาน

## เริ่มใช้งาน (dev พร้อม hot reload)

```bash
cp .env.example .env
docker compose up --build
```

`docker compose up` โหลด `docker-compose.override.yml` อัตโนมัติ: mount source และ rebuild ด้วย air

## เปิดแบบ production-like ในเครื่อง (ไม่โหลด dev override)

```bash
docker compose -f docker-compose.yml up --build
```

ใช้ image ที่ build แล้วและรันเป็น non-root; production จริงใช้ Supabase และ deployment config แยก (milestone 11)

## URLs

| URL | ใช้ทำอะไร |
| --- | --- |
| http://localhost:8080/healthz | process ทำงานอยู่ |
| http://localhost:8080/readyz | เชื่อม database ได้ |
| http://localhost:8080/docs/ | Swagger UI (bundle ใน image ไม่โหลด CDN) |
| http://localhost:8080/swagger.json, /swagger.yaml | Swagger 2.0 spec |
| http://localhost:8025 | Mailpit กล่องจดหมายปลอมสำหรับ dev |

ทุก response มี `X-Request-ID` ใช้ค้น log ของคำขอนั้นได้

## Application log

```bash
docker compose logs -f api
```

Log เป็น JSON บรรทัดละ record (`time` UTC, `level`, `service`, `msg`, `event`, `request_id`)
Access log เก็บ method, route template, status และ duration โดยไม่เก็บ query string, cookies, headers หรือ body

## Database

- `deploy/db/bootstrap.sql` สร้าง roles `app_migrator` (เจ้าของ schema `app`), `app_runtime` และ `app_maintenance` ตอน volume ว่าง
- service `migrate` รัน goose ด้วย `app_migrator` และเก็บประวัติใน `app.goose_db_version`; `api` จะเริ่มเมื่อ migrate สำเร็จเท่านั้น
- PostgreSQL เปิดให้เครื่อง host ที่ `127.0.0.1:55432` (เปลี่ยนได้ด้วย `POSTGRES_HOST_PORT`)
- ล้างข้อมูลทั้งหมดแล้วเริ่มใหม่: `docker compose down -v`

## ตัวแปรสภาพแวดล้อมของ auth

`CSRF_SECRET`, `OTP_SECRET`, `RATE_LIMIT_SECRET` และ `OIDC_SECRET` ต้องตั้งใน `.env` เป็นค่ายาวอย่างน้อย 32 bytes (สำหรับ dev ใช้ค่า placeholder `dev-only-…` เช่น `dev-only-csrf-secret-0123456789abcdef` ได้); production ปฏิเสธค่าที่ขึ้นต้นด้วย `dev-only-` และสี่ค่าต้องไม่ซ้ำกัน

ตัวแปรอื่นมีค่าเริ่มต้นสำหรับ dev จึงไม่ต้องตั้ง: `SESSION_TTL` (168h), `PASSWORD_HASH_CONCURRENCY` (4), `AUTH_RESPONSE_FLOOR`/`AUTH_RESPONSE_JITTER` (500ms/100ms), `OTP_MAX_ATTEMPTS` (5), `OTP_RESEND_COOLDOWN` (60s), `OTP_REQUESTS_PER_HOUR_EMAIL`/`_IP` (5/20), `LOGIN_ATTEMPTS_PER_EMAIL`/`_IP` (10/30 ต่อ 15 นาที), `TRUSTED_PROXY_CIDRS` (ว่าง = ไม่เชื่อ `X-Forwarded-For`), `SMTP_HOST`/`SMTP_PORT`/`SMTP_FROM`/`SMTP_TLS`/`MAIL_SEND_TIMEOUT` (10s; compose ชี้ไป Mailpit ที่ `mailpit:1025` แบบไม่เข้ารหัส) production ต้องตั้ง `SMTP_HOST`, `SMTP_PORT`, `SMTP_FROM` และ `SMTP_TLS` เป็น `starttls` หรือ `tls` เอง

อีเมลที่ระบบส่ง (รหัสยืนยัน และแจ้งว่ามีบัญชีอยู่แล้ว) เป็นภาษาไทย ส่งแบบ UTF-8 + quoted-printable โดยหัวเรื่อง encode ตาม RFC 2047

## ลอง flow ของ auth ด้วย curl

ต้องตั้งค่าใน `.env` ให้ครบก่อน (`APP_ENV=development`, `PUBLIC_ORIGIN=http://localhost:8080` และ secret สี่ตัวตามหัวข้อก่อนหน้า) แล้ว `docker compose up --build`
ทุก POST ต้องมี header `Origin` ที่ตรงกับ `PUBLIC_ORIGIN` และ `X-CSRF-Token` จาก `GET /api/v1/auth/csrf`; cookie jar เก็บ `csrf_nonce` และ `session` ให้เอง
ตัวอย่างนี้ใช้ bash/zsh และอ่านรหัสจาก Mailpit ผ่าน REST API (`http://localhost:8025/api/v1/message/latest` ซึ่งตอบ JSON ที่มีฟิลด์ `Text`)

```bash
API=http://localhost:8080/api/v1
JAR=$(mktemp)
EMAIL=demo-$RANDOM@example.com
csrf() { curl -s -b "$JAR" -c "$JAR" "$API/auth/csrf" | sed -E 's/.*"csrf_token":"([^"]+)".*/\1/'; }
post() { curl -s -b "$JAR" -c "$JAR" -H "Origin: http://localhost:8080" -H "X-CSRF-Token: $(csrf)" \
  -H 'Content-Type: application/json' -w '\nHTTP %{http_code}\n' -d "$2" "$API$1"; }

# 1) สมัคร → 202 พร้อม challenge_id (ตอบแบบเดียวกันแม้อีเมลมีบัญชีแล้ว)
post /auth/register "{\"email\":\"$EMAIL\",\"password\":\"Zq9!kVx1\"}" | tee /dev/stderr > "$JAR.reg"
CHALLENGE=$(sed -nE 's/.*"challenge_id":"([^"]+)".*/\1/p' "$JAR.reg")

# 2) อ่านรหัส 6 หลักจากอีเมลล่าสุดใน Mailpit (ส่งหลังตอบ 202 อาจรอ 1–2 วินาที)
sleep 2
OTP=$(curl -s http://localhost:8025/api/v1/message/latest | grep -oE 'รหัสยืนยันของคุณคือ [0-9]{6}' | grep -oE '[0-9]{6}')

# 3) ยืนยันอีเมล → 204
post /auth/email-verification/confirm "{\"challenge_id\":\"$CHALLENGE\",\"otp\":\"$OTP\"}"

# 4) login → 200 และได้ cookie session; 5) ดูบัญชี → 200; 6) logout → 204
post /auth/login "{\"email\":\"$EMAIL\",\"password\":\"Zq9!kVx1\"}"
curl -s -b "$JAR" -c "$JAR" -w '\nHTTP %{http_code}\n' "$API/auth/me"
post /auth/logout ''
curl -s -b "$JAR" -w '\nHTTP %{http_code}\n' "$API/auth/me"   # 401 หลัง logout
```

- ขอรหัสใหม่: `post /auth/email-verification/request "{\"email\":\"$EMAIL\"}"` (เว้น 60 วินาทีจากครั้งก่อน และไม่เกิน 5 ครั้งต่อชั่วโมงต่ออีเมล)
- ถูกจำกัดความถี่จะได้ 429 `rate_limited` พร้อม header `Retry-After` (วินาที)
- ใน Swagger UI (`http://localhost:8080/docs/`) ให้เรียก `GET /api/v1/auth/csrf` ก่อน แล้วคัดลอก `csrf_token` ไปใส่ช่อง `X-CSRF-Token` ของ POST; browser ส่ง cookie ให้เอง ขอ token ใหม่หลัง login และหลัง logout เพราะ binding เปลี่ยน
- ข้อความ error ทุกอันมี `request_id` ตรงกับ header `X-Request-ID` ใช้ค้น log ได้

## พัฒนาและทดสอบ

```bash
make tools            # ติดตั้ง swag, go-swagger, goose ตามเวอร์ชันที่ pin ลง ./bin
make lint-install     # ติดตั้ง golangci-lint ลง ./bin
make test             # unit/HTTP/contract tests (integration tests จะ skip)
make test-integration # integration tests กับ PostgreSQL ใน compose (ต้องเปิด stack ก่อน)
make test-mail        # ส่งอีเมลจริงไป Mailpit ใน compose แล้วอ่านกลับ (docker compose up -d mailpit)
make swagger          # generate docs/api จาก annotations แล้ว validate
make check            # tidy + vet + lint + swagger + tests + ตรวจว่า docs/api commit แล้ว
```

ห้ามแก้ไฟล์ใน `docs/api/` ที่ generate แล้วด้วยมือ ให้แก้ annotations แล้วรัน `make swagger`

## ข้อจำกัดของ phase 1

- ไม่รองรับ dividends, stock splits, mergers และการเปลี่ยน symbol: พอร์ตที่ได้รับผลจาก corporate actions เหล่านี้จะคำนวณได้ไม่ครบถ้วน
- ถ้า provider เปลี่ยน symbol ของหุ้นที่ระบบบันทึกไว้แล้ว (เช่น FB → META) การบันทึกรายการด้วย symbol ใหม่จะได้ `422 unknown_symbol` พร้อมข้อความว่า symbol ถูกเปลี่ยนชื่อ
- ผลคำนวณเป็นการติดตามพอร์ตด้วย moving weighted-average cost ไม่ใช่รายงานภาษี

## เอกสารออกแบบ

- [AGENTS.md](AGENTS.md) — ข้อกำหนดของโปรเจกต์
- [Design spec](docs/superpowers/specs/2026-10-05-phase-1-design.md) และ [roadmap](docs/superpowers/plans/2026-10-05-phase-1-roadmap.md)
- [ADRs](docs/adr/README.md) — เหตุผลของการตัดสินใจสำคัญ
- [ER diagram (DBML)](docs/database/schema.dbml)
