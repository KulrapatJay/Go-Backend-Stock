# Stock Portfolio Tracker — Backend

Go API สำหรับบันทึกรายการซื้อขายหุ้นสหรัฐฯ และติดตามต้นทุน/กำไรขาดทุนของหลายพอร์ต
ระบบนี้บันทึกการลงทุนที่ทำผ่านช่องทางอื่น ไม่ใช่ระบบส่งคำสั่งซื้อขายจริง

> **สถานะ:** Milestone 4 — สมัครด้วยอีเมล+รหัสผ่าน, ยืนยันอีเมลด้วย OTP, login/logout, session ฝั่ง server และ Google sign-in/เชื่อมบัญชี (เปิดเมื่อตั้งค่า `GOOGLE_*`) ใช้งานผ่าน API ได้แล้ว; ลืมรหัสผ่าน (milestone 5), พอร์ตและรายการซื้อขายยังไม่เปิดใช้งาน

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
ผู้ใช้ zsh ให้รัน `setopt interactive_comments` ก่อนวางบล็อกนี้ (zsh แบบ interactive ไม่ถือ `#` เป็น comment ตามค่าเริ่มต้น) หรือจะบันทึกบล็อกเป็นไฟล์แล้วรันด้วย `bash` ก็ได้

```bash
API=http://localhost:8080/api/v1
JAR=$(mktemp)
EMAIL=demo-$RANDOM@example.com
PASSWORD='Zq9!kVx1'
csrf() { curl -s -b "$JAR" -c "$JAR" "$API/auth/csrf" | sed -E 's/.*"csrf_token":"([^"]+)".*/\1/'; }
post() { curl -s -b "$JAR" -c "$JAR" -H "Origin: http://localhost:8080" -H "X-CSRF-Token: $(csrf)" \
  -H 'Content-Type: application/json' -w '\nHTTP %{http_code}\n' -d "$2" "$API$1"; }

# 1 สมัคร ได้ 202 พร้อม challenge_id และตอบแบบเดียวกันแม้อีเมลมีบัญชีแล้ว
post /auth/register "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}" | tee /dev/stderr > "$JAR.reg"
CHALLENGE=$(sed -nE 's/.*"challenge_id":"([^"]+)".*/\1/p' "$JAR.reg")

# 2 อ่านรหัส 6 หลักจากอีเมลล่าสุดใน Mailpit โดยรอ 2 วินาทีเพราะระบบส่งอีเมลหลังตอบ 202
sleep 2
OTP=$(curl -s http://localhost:8025/api/v1/message/latest | grep -oE 'รหัสยืนยันของคุณคือ [0-9]{6}' | grep -oE '[0-9]{6}')

# 3 ยืนยันอีเมล ได้ 204
post /auth/email-verification/confirm "{\"challenge_id\":\"$CHALLENGE\",\"otp\":\"$OTP\"}"

# 4 login ได้ 200 และได้ cookie session
post /auth/login "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}"

# 5 ดูบัญชีปัจจุบัน ได้ 200
curl -s -b "$JAR" -c "$JAR" -w '\nHTTP %{http_code}\n' "$API/auth/me"

# 6 logout ได้ 204
post /auth/logout ''

# 7 หลัง logout เรียก me ซ้ำจะได้ 401
curl -s -b "$JAR" -w '\nHTTP %{http_code}\n' "$API/auth/me"

rm -f "$JAR" "$JAR.reg"
```

- ขอรหัสใหม่: `post /auth/email-verification/request "{\"email\":\"$EMAIL\"}"` (เว้น 60 วินาทีจากครั้งก่อน และไม่เกิน 5 ครั้งต่อชั่วโมงต่ออีเมล)
- ถูกจำกัดความถี่จะได้ 429 `rate_limited` พร้อม header `Retry-After` (วินาที)
- ใน Swagger UI (`http://localhost:8080/docs/`) ให้เรียก `GET /api/v1/auth/csrf` ก่อน แล้วคัดลอก `csrf_token` ไปใส่ช่อง `X-CSRF-Token` ของ POST; browser ส่ง cookie ให้เอง ขอ token ใหม่หลัง login และหลัง logout เพราะ binding เปลี่ยน
- ข้อความ error ทุกอันมี `request_id` ตรงกับ header `X-Request-ID` ใช้ค้น log ได้

## Google sign-in (ไม่บังคับ)

ถ้าไม่ตั้งค่า Google ระบบยังใช้งานได้ปกติ: `GET /api/v1/auth/config` ตอบ `{"google_enabled":false}` และ endpoint ของ Google ตอบ 503 `feature_disabled`

สร้าง OAuth client (ฟรี ไม่ต้องผูกบัตร):

1. เปิด Google Cloud Console → APIs & Services → OAuth consent screen ตั้งเป็น External และโหมด Testing แล้วเพิ่มอีเมล Google ของตัวเองใน Test users
2. Credentials → Create credentials → OAuth client ID → Web application
3. Authorized redirect URIs ใส่ `http://localhost:8080/api/v1/auth/google/callback` (ต้องเท่ากับ `PUBLIC_ORIGIN` + `/api/v1/auth/google/callback` ทุกตัวอักษร)
4. ใส่ค่าใน `.env` ทั้งสามตัว (ตั้งไม่ครบ API จะไม่เริ่มและบอกว่าขาดตัวไหน) แล้ว `docker compose up -d --build api`

```bash
GOOGLE_CLIENT_ID=1234-abc.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=<client secret จาก Console — ห้าม commit>
GOOGLE_REDIRECT_URI=http://localhost:8080/api/v1/auth/google/callback
```

ลองบน browser (curl และ Swagger UI ทำหน้า login ของ Google แทนไม่ได้):

1. เปิด `http://localhost:8080/docs/` เรียก `GET /api/v1/auth/csrf` แล้วคัดลอก `csrf_token`
2. เรียก `POST /api/v1/auth/google/start` ใส่ token ในช่อง `X-CSRF-Token` แล้วเปิด `authorization_url` ที่ได้ใน**แท็บเดียวกันของ browser เดิม** (cookie `oidc_binding` อยู่ใน browser นี้)
3. login กับ Google แล้ว browser จะกลับมาที่ `/docs/?google=success` พร้อม cookie `session`; เรียก `GET /api/v1/auth/me` ดูได้
4. ผลอื่น: `link_required` = อีเมลนี้มีบัญชีอยู่แล้ว ให้ login บัญชีนั้นด้วยรหัสผ่าน แล้วขอ CSRF ใหม่และเรียก `POST /api/v1/auth/google/link` แทน (ได้ `/docs/?google=linked`); `failed` = ยกเลิก, หมดเวลา 10 นาที, เปิด callback ซ้ำหรือคนละ browser

ระบบไม่เชื่อมบัญชีให้อัตโนมัติเพียงเพราะอีเมลตรงกัน และบัญชีที่สร้างจาก Google ไม่มีรหัสผ่าน (login ด้วยรหัสผ่านไม่ได้จนกว่าจะมีลืมรหัสผ่านใน milestone 5)

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
