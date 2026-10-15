คุณคือ senior backend engineer ที่เชี่ยวชาญ Go, PostgreSQL, Hexagonal/Clean Architecture,
Docker, CI/CD ด้วย GitHub Actions และการออกแบบระบบ auth ที่ปลอดภัย
ช่วยฉันสร้างโปรเจค "เว็บติดตามพอร์ตหุ้นสหรัฐฯ" สำหรับเก็บไว้เป็น portfolio
 
## เป้าหมายของโปรเจค
เว็บที่ผู้ใช้บันทึกรายการซื้อ/ขายหุ้น ดูต้นทุนเฉลี่ยและกำไร-ขาดทุนของพอร์ต
และดูหน้าหุ้นรายตัวพร้อมกราฟราคา (เริ่มเฟสถัดไปจากเฟส 1 ด้วยกราฟราคาแบบ delayed แล้วจึงเพิ่มราคา live, pre-market, after-hours, overnight)
ผู้ใช้หนึ่งคนสร้างพอร์ตของตัวเองได้หลายพอร์ต เช่น พอร์ตระยะยาว พอร์ตระยะสั้น และพอร์ตเก็งกำไร
เป็นระบบบันทึกและติดตามการลงทุนที่ผู้ใช้ทำผ่านช่องทางอื่น ไม่ใช่ระบบส่งคำสั่งซื้อขายหุ้นจริง
- ลำดับที่ยืนยันแล้ว: ทำ backend/API พร้อม Swagger, audit และ tests ให้ผ่านการตรวจรับก่อน แล้วจึงเริ่ม frontend
  ระหว่างทำ backend ยังไม่สร้าง Next.js skeleton, web Dockerfile/service หรือ web CI
  ใช้ Swagger UI, curl และ HTTP/integration tests ตรวจการทำงานได้โดยไม่ต้องมี frontend
 
## Tech stack (ตัดสินใจแล้ว)
- Backend: Go + Fiber + pgx + sqlc + goose + PostgreSQL (ไม่ใช้ ORM)
- Database ที่ deploy: Supabase PostgreSQL Free plan ใช้เฉพาะ Database
  ส่วน local development และ CI ใช้ PostgreSQL ใน Docker
- Auth, Google OpenID Connect, OTP และ server-side sessions ยังคงพัฒนาใน Go ไม่ใช้ Supabase Auth
- Logging: Go log/slog แบบ JSON สำหรับ application log และตาราง audit_events สำหรับ audit log
- API documentation: swaggo/swag generate จาก comments/annotations ใน Go + Swagger UI แบบ self-hosted
  เลือกรูปแบบ Swagger/OpenAPI และเวอร์ชันเครื่องมือที่เข้ากันได้ใน design proposal
- Frontend: Next.js + TypeScript + TradingView Lightweight Charts เริ่มหลัง backend ผ่านการตรวจรับ
- แหล่งข้อมูลราคา: Alpaca Basic/Free โดยเลือก feed และ endpoint ให้ตรงกับสิทธิ์ของบัญชี
  - ข้อจำกัด 30 symbols เป็นข้อจำกัดของ WebSocket subscription ไม่ใช่จำนวนหุ้นที่บันทึกในพอร์ต
  - REST latest ของ IEX กับ historical SIP มีเงื่อนไขต่างกัน ห้ามเหมารวมว่า REST ทุก endpoint delayed 15 นาที
  - เฟส 1 ใช้ historical SIP ที่กำหนด end เก่าอย่างน้อย 15 นาที และตรวจสิทธิ์ด้วยบัญชีจริงก่อนเชื่อมใช้งาน
- Infra: Docker + docker-compose, CI ด้วย GitHub Actions
- ห้ามใช้บริการเสียเงินในเฟสนี้
 
## สถาปัตยกรรม: Hexagonal (Ports & Adapters) + Clean Architecture
- หลักการ: dependency ต้องชี้เข้าหา core เสมอ core ไม่รู้จัก Fiber, sqlc, pgx, Alpaca หรือ Supabase
- Core = domain (entities) + ports (interfaces) + services (use cases)
- Primary adapters: Fiber HTTP handlers (แปลง request/response, validate input)
- Secondary adapters: Postgres repositories (sqlc), Alpaca PriceProvider, Mailer, Google IdentityProvider
- Port ถูกประกาศและเป็นเจ้าของโดย core (ไม่ใช่ adapter)
- สร้าง interface เฉพาะตรงขอบจริง: DB, ราคา, อีเมล, เวลา (Clock), ผู้ให้บริการ identity ภายนอก
- อนุญาต inbound ports เช่น AuthService และ PortfolioService เพื่อกำหนดสัญญาของ use case กับ HTTP adapter
  ไม่สร้าง interface ให้ทุก struct หรือทุก helper และไม่ทำ generic repository
- Core กำหนดขอบเขตงานที่ต้อง atomic ผ่าน port; adapter เป็นผู้จัดการ DB transaction/lock
  ห้ามส่ง pgx.Tx หรือ sqlc-generated types เข้าสู่ core
- AuditEvent เป็น domain type และ AuditRepository เป็น port สำหรับ persistence
  เหตุการณ์ธุรกิจเกิดจาก use case; HTTP adapter แนบ request context ที่จำเป็นเป็นค่าธรรมดา
  การเขียนข้อมูลธุรกิจพร้อม audit ต้องใช้ขอบเขต atomic เดียวกันผ่าน port ไม่เรียกสอง repository แบบแยก commit
- ประกอบ dependency (DI) ที่ composition root ใน cmd/*/main.go เท่านั้น (api และ maintenance) ไม่ใช้ global state
 
## โครงโฟลเดอร์
```text
cmd/
  api/main.go
  maintenance/main.go   # retention cleanup แบบ one-shot ด้วย maintenance role
internal/
  config/          # โหลดและ validate config แยก dev/production
  core/
    domain/        # User, Session, Portfolio, Transaction, Instrument, Holding, Money, PriceQuote, AuditEvent
    port/          # inbound: AuthService, EmailVerificationService, PasswordResetService, PortfolioService,
                   #   TransactionService, ValuationService
                   # outbound: UnitOfWork/TxRepositories, UserRepository, SessionRepository,
                   #   IdentityRepository, OIDCFlowRepository,
                   #   OTPChallengeRepository (otp_challenges: email_verification, password_reset),
                   #   PortfolioRepository, TransactionRepository, IdempotencyRepository,
                   #   RateLimitRepository, AuditRepository,
                   #   InstrumentRepository, PriceProvider, InstrumentProvider, IdentityProvider, Mailer, Clock
                   # signatures ที่ยืนยันอยู่ใน design spec (ดูหัวข้อเอกสารออกแบบ)
    service/       # auth, otp, portfolio, transaction, valuation (use cases)
  adapter/
    http/          # Fiber handlers, middleware, DTO
    postgres/      # sqlc-generated code + repositories รวม audit; ใช้กับ local PostgreSQL/Supabase
    alpaca/        # PriceProvider + InstrumentProvider (Assets API) implementation
    identity/      # Google OpenID Connect adapter
    mail/          # Mailer implementation (SMTP/Resend)
    demo/          # PriceProvider + InstrumentProvider ใช้ข้อมูลตัวอย่างเฉพาะโหมดเดโม
migrations/        # goose; สร้างเฉพาะ objects ใน schema app
deploy/local/initdb/  # bootstrap roles + schema app สำหรับ local/CI ไม่มี production secrets
docs/api/          # generated docs.go, swagger.json, swagger.yaml จาก swaggo/swag
docs/superpowers/  # design spec และ roadmap/plans
docs/database/     # schema.dbml สำหรับดู ER diagram
docs/adr/          # Architecture Decision Records หนึ่งไฟล์ต่อหนึ่งการตัดสินใจ
web/               # Next.js สร้างเมื่อเริ่ม frontend หลังตรวจ backend ผ่าน
Dockerfile         # API (multi-stage)
docker-compose.yml
docker-compose.override.yml   # dev
.env.example
.dockerignore
.github/workflows/ci.yml
sqlc.yaml
```

## Supabase Database และการแยก environment

- เส้นทางข้อมูลคือ Next.js → Go API → PostgreSQL; frontend ไม่เชื่อม Supabase Database/Data API โดยตรง
  ใช้ pgx + sqlc ผ่าน Postgres connection ตามเดิม ไม่ต้องใช้ Supabase SDK สำหรับ query
- ใช้ schema ของแอป เช่น app สำหรับ users, sessions, portfolios และตารางอื่นของระบบ
  ไม่ใช้หรือแก้ไขตาราง managed schema เช่น auth.users/auth.sessions ของ Supabase
  migration ของแอปต้องไม่พยายามสร้าง แก้ไข หรือลบ managed schemas
- ปิด Supabase Data API เมื่อไม่ได้ใช้ และไม่ expose schema ของแอปผ่าน REST/GraphQL
  ไม่ grant สิทธิ์ตารางแอปให้ anon/authenticated; ตารางใน exposed schema ต้องเปิด RLS พร้อม policy ที่เหมาะสม
  ส่วน private schema ใช้ grants จำกัดสิทธิ์ และพิจารณา RLS เป็นการป้องกันเพิ่มเติม
- Go ต้องตรวจ session และ ownership ทุกครั้งตามเดิม; custom session ของ Go ไม่ผูกกับ auth.uid() อัตโนมัติ
  ถ้าใช้ RLS สำหรับ direct SQL ต้องออกแบบการส่ง user context และสิทธิ์ของ DB role ให้ชัดเจนก่อนลงมือ
- แยก runtime DB role ที่มีเฉพาะสิทธิ์จำเป็นออกจาก migration role ที่มีสิทธิ์ DDL
  runtime role ต้องไม่เป็นเจ้าของตาราง ไม่เป็น superuser และไม่มี BYPASSRLS
  ใช้ DATABASE_URL สำหรับ API และ MIGRATION_DATABASE_URL สำหรับ goose โดยเก็บ credentials ใน env/secrets
- Go API ที่รันต่อเนื่องใช้ direct connection เมื่อเครือข่ายรองรับ หรือ session pooler สำหรับ IPv4-only
  กำหนด TLS พร้อมตรวจ certificate/hostname, connection limit และ timeout ให้ตรงกับโควตา
  ห้ามเปลี่ยนเป็น transaction pooler โดยไม่ทบทวน prepared statements และ session-level state ของ pgx/goose
- migrations ใช้ goose เป็นแหล่งประวัติเดียวทั้ง local/CI/Supabase ไม่ใช้ Supabase migrations ซ้อนกัน
  รันผ่าน direct connection หรือ session pooler ที่ตรวจ compatibility แล้วด้วย migration role
  ห้ามแก้ schema ของแอปผ่าน Dashboard โดยไม่มี goose migration ใน repository
  ตาราง version ของ goose อยู่ใน schema app (ไม่ใช้ public.goose_db_version ซึ่งอยู่ใน schema ที่ Data API อาจ expose)
  schema app และ roles สร้างในขั้น bootstrap ก่อนรัน goose; migrations สร้าง/แก้เฉพาะ objects ภายใน app
- Local/CI ใช้ schema และ migrations ชุดเดียวกับ Supabase และใช้ PostgreSQL major version ที่สอดคล้องกัน
  CI ปกติต้องไม่เชื่อม production database หรือใช้ production credentials
- ใช้ Supabase Free ตามข้อกำหนดไม่เสียเงิน ตรวจ storage/connection quotas และ inactivity pause ก่อน deploy
  จัดทำวิธีสำรองและกู้คืนข้อมูลแอปที่ทดสอบแล้ว ไม่สมมติว่า Free plan มี automatic backups ให้
  ห้ามเก็บ database dumps หรือ credentials ใน Git
 
## Docker / docker-compose
- Dockerfile ของ API เป็น multi-stage (build ด้วย golang แล้วคัดลอก binary ไปรันบน alpine/distroless)
  รันด้วย non-root user; Dockerfile ของ web แบบ multi-stage เพิ่มเมื่อเริ่ม frontend เท่านั้น
- docker-compose.yml ในช่วง backend ประกอบด้วย:
    postgres (มี volume + healthcheck)
    migrate (รัน goose up แบบ one-shot แล้วจบ)
    api (รอ migrate เสร็จก่อนเริ่ม)
    mailpit (กล่องจดหมายปลอมสำหรับทดสอบ OTP ตอน dev)
- เพิ่ม service web เมื่อเริ่ม frontend หลัง backend ผ่านการตรวจรับแล้ว
- docker-compose.override.yml สำหรับ dev: mount โค้ด, hot reload ด้วย air
- มี .env.example (commit) และ .env (ห้าม commit) และ .dockerignore
- API มี endpoint /healthz ไว้ให้ healthcheck
- สร้าง /healthz ตั้งแต่ scaffold แรก โดยใช้ตรวจว่า process ทำงาน; /readyz ตรวจการเชื่อมต่อ DB
- ใน local Compose ให้ migrate รอ postgres healthy และ api รอ migrate จบสำเร็จ
  ห้ามเริ่ม API ต่อเมื่อ migration ล้มเหลว
- container เรียกกันด้วยชื่อ service (เช่น postgres:5432) ไม่ใช้ localhost
- เป้าหมาย: cp .env.example .env && docker compose up แล้วใช้งานโหมดเดโมได้ในครั้งเดียว
  ช่วง backend ใช้ Swagger/curl ทดสอบ API ร่วมกับ Mailpit และราคาตัวอย่างที่ระบุว่าเป็นข้อมูลเดโม
  หลังเพิ่ม frontend คำสั่งเดียวกันต้องเปิด web ที่เชื่อม API ได้ด้วย
- ค่าเริ่มต้นสำหรับ dev ใช้ PRICE_PROVIDER=demo; การใช้ Alpaca ต้องตั้ง credentials และเปลี่ยนเป็น alpaca เอง
  ห้ามสลับจากราคาจริงเป็นราคาตัวอย่างอัตโนมัติเมื่อ provider ล้มเหลว
- Google login เปิดใช้เมื่อมี client ID, client secret และ redirect URI ครบเท่านั้น
  ถ้ายังไม่ตั้งค่า ให้ UI แสดงว่าไม่พร้อมใช้และ API ปฏิเสธ flow นี้อย่างชัดเจน ห้ามทำ login จำลองแทน
- Mailpit, hot reload และ source mounts ใช้เฉพาะ dev; production ต้องใช้ image ที่ build แล้ว
  README ต้องแยกคำสั่ง dev/production เพื่อไม่โหลด dev override โดยไม่ตั้งใจ
- แยก configuration สำหรับ dev/production และ validate ตอนเริ่มระบบ; production ต้องปฏิเสธค่า secret ว่าง
  ค่า secret ตัวอย่าง และ demo provider ส่วน dev สร้าง secret สุ่มเมื่อเริ่มครั้งแรกและเก็บนอก Git ได้
- README อธิบายการตั้งค่า Google/Alpaca/อีเมลจริงเพิ่มเติมจากขั้นตอนเปิดเดโม
- การ deploy ที่ใช้ Supabase ไม่ต้องรัน service postgres หรือรอ local postgres healthcheck
  ใช้ deployment configuration แยกจาก local Compose และรัน goose กับ Supabase สำเร็จก่อนเปิด API เวอร์ชันใหม่
 
## CI (GitHub Actions) รันเมื่อเปิด PR และ push ไป main
- Go: go vet, golangci-lint, sqlc generate แล้ว git diff --exit-code,
  go test ./... (ใช้ Postgres service container สำหรับ integration test),
  ทดสอบ goose migration บน DB ว่าง, build Docker image (ยังไม่ต้อง push)
- Next.js: เพิ่ม lint, typecheck, build และ web image build เมื่อเริ่ม frontend เท่านั้น
- pin เวอร์ชัน Go, PostgreSQL, sqlc, goose, golangci-lint และเครื่องมือ Swagger ให้ local/Docker/CI ใช้ตรงกัน
  เพิ่มการ pin Node.js และเครื่องมือ frontend เมื่อเริ่มทำ web
- CI ไม่เรียก Google, Alpaca หรือบริการส่งอีเมลจริง ใช้ fake ของ port; repository integration test ใช้ PostgreSQL จริง
- ตรวจ migrations, DB role permissions และการบันทึก audit กับ PostgreSQL service container
  ไม่ใช้ Supabase project จริงเป็นฐานข้อมูลสำหรับ integration tests ใน CI ปกติ
- Generate Swagger จาก annotations ใหม่ใน CI แล้วตรวจว่า generated files ตรงกับที่ commit ไว้
  Validate/lint generated spec และตรวจ request/response/status codes ของ endpoints ที่ implement แล้ว
  การ generate หรือ parse YAML ผ่านอย่างเดียวไม่ถือว่าเอกสารตรงกับ API
- cache Go modules ตั้งแต่ช่วง backend และเพิ่ม npm cache เมื่อเริ่ม frontend
  ตั้ง branch protection ให้ merge ได้เมื่อ checks ของส่วนที่มีอยู่จริงผ่าน และใส่ badge ใน README
- เสริมได้: govulncheck และ Dependabot
 
## CD (ทำตอนจบเฟส 1 ยังไม่ต้องทำในตอนแรก)
- Database ใช้ Supabase PostgreSQL; เลือกแพลตฟอร์มสำหรับ web/API ที่ deploy จาก GitHub เมื่อเริ่มทำ CD
  โดยตรวจ free tier, sleep policy, ค่าใช้งานเกินโควตา และข้อจำกัดการส่งอีเมลในขณะนั้น
- Vercel สำหรับ web และ Railway/Render/Fly.io สำหรับ API เป็นตัวเลือกสำหรับประเมิน
  ไม่ใช่การรับรองว่าโฮสต์ทั้งระบบได้ฟรีระยะยาว
  ต้องรักษาเงื่อนไขไม่ใช้บริการเสียเงิน; ห้ามเปิดแผนชำระเงินหรือ auto-upgrade โดยไม่ได้รับอนุญาต
- ขั้นต่อไป: build image แล้ว push ขึ้น GHCR ด้วย tag เป็น git SHA, รัน goose up ก่อนเปิดเวอร์ชันใหม่,
  ตรวจ /healthz หลัง deploy และ rollback ได้
- secret เก็บใน GitHub Secrets / environment ของแพลตฟอร์ม ห้ามใส่ในโค้ดหรือ image
 
## ขอบเขตเฟส 1 (ทำเฉพาะนี้ก่อน)
1. Auth: สมัครด้วยอีเมล+รหัสผ่าน (Argon2id + จำกัดการ hash พร้อมกัน; รหัสผ่าน printable ASCII ไม่มีช่องว่าง 6–32 ตัว ต้องมีพิมพ์เล็ก/พิมพ์ใหญ่/อักขระพิเศษ
   และไม่อยู่ใน blocklist รหัสยอดนิยมที่ bundle ใน image)
   และต้องยืนยันอีเมลด้วย OTP ก่อน login ด้วยรหัสผ่านได้, login, login ด้วย Google (OpenID Connect), logout
2. ลืมรหัสผ่านด้วย OTP ทางอีเมล:
   - OTP 6 หลักจาก crypto/rand, เก็บเป็น hash, หมดอายุใน 10 นาที
   - จำกัดจำนวนครั้งที่กรอกผิด และจำกัดการขอ OTP ต่อชั่วโมง
3. Server-side session ด้วย opaque token ใน HttpOnly cookie และเก็บ session ใน PostgreSQL
   - เปรียบเทียบ server-side session กับ JWT สั้น ๆ: session revoke ได้ทันทีแต่ต้องอ่าน session store;
     JWT ตรวจลายเซ็นได้โดยไม่อ่าน store แต่การ revoke ก่อนหมดอายุซับซ้อนขึ้น
   - Cookie เป็นวิธีส่ง token และ JWT เป็นรูปแบบ token จึงไม่ใช่ตัวเลือกที่อยู่คนละฝั่งโดยตรง
4. ผู้ใช้หนึ่งคนสร้างได้หลายพอร์ต:
   - ตาราง portfolios เก็บเจ้าของพอร์ต (user_id), ชื่อ และคำอธิบายที่ไม่บังคับกรอก
   - ผู้ใช้ตั้งชื่อพอร์ตเองได้ เช่น ระยะยาว ระยะสั้น เก็งกำไร ไม่กำหนดประเภทพอร์ตตายตัวในเฟสแรก
   - ชื่อพอร์ตซ้ำกันไม่ได้ภายในผู้ใช้คนเดียว (ไม่สนตัวพิมพ์เล็ก/ใหญ่) แต่ต่างผู้ใช้ใช้ชื่อเดียวกันได้
   - ความสัมพันธ์: users 1:N portfolios และ portfolios 1:N transactions
   - ตรวจสิทธิ์เจ้าของพอร์ตทุกครั้งก่อนอ่านหรือแก้ไขพอร์ต รวมถึงรายการและผลคำนวณภายในพอร์ต
5. ตาราง transactions (portfolio_id, instrument_id, ซื้อ/ขาย, จำนวน, ราคา, ค่าธรรมเนียม, วันที่)
   - instrument_id อ้าง instruments ด้วย surrogate key เพราะ symbol เปลี่ยนได้; API รับและตอบเป็น symbol
   - แต่ละรายการเป็นของพอร์ตเดียวผ่าน portfolio_id และเป็นข้อมูลที่ผู้ใช้บันทึก ไม่ใช่คำสั่งซื้อขายจริง
   - symbol ต้องมีอยู่จริง: ตรวจกับตาราง instruments ก่อน ถ้ายังไม่มีให้ตรวจกับ provider ผ่าน InstrumentProvider port
     แล้วบันทึกลง instruments; ไม่พบคืน unknown_symbol, provider ล่มคืน error ให้ลองใหม่ ห้าม fallback ไปข้อมูลเดโม
     ห้ามถือ portfolio lock ระหว่างเรียก provider
6. คำนวณจำนวนหุ้น ต้นทุนเฉลี่ย และกำไร-ขาดทุน (realized/unrealized) จากราคาแบบ delayed แยกตามพอร์ต
   - หุ้น symbol เดียวกันในคนละพอร์ตมีจำนวนและต้นทุนแยกกัน การบันทึกรายการในพอร์ตหนึ่งไม่กระทบอีกพอร์ต
7. ดึงราคาผ่าน port PriceProvider เพื่อสลับ provider ภายหลังได้
8. Application log และ audit log สำหรับ auth, ความปลอดภัย, พอร์ต และรายการซื้อขายตามข้อกำหนดด้านล่าง
9. เอกสาร Swagger/OpenAPI ที่ generate จาก Go และ Swagger UI สำหรับดู API พร้อมตัวอย่างการเรียกใช้งาน

## OpenAPI / Swagger UI

- ใช้ comments/annotations ที่ HTTP handlers และ DTO เป็นแหล่งเอกสารหลักตาม API design ที่ยืนยัน
  ใช้ swaggo/swag และคำสั่ง swag init ที่กำหนด entrypoint/output ของโปรเจกต์เพื่อ generate ลง docs/api/
  commit generated docs.go, swagger.json และ swagger.yaml; ห้ามแก้ไฟล์ generated ด้วยมือหรือดูแล YAML อีกชุดซ้ำ
  annotations และ Swagger integration อยู่ฝั่ง HTTP adapter/entrypoint ไม่ให้ core ขึ้นกับ Swagger library
- เลือกและ pin swaggo/swag, middleware ที่ตรงกับ Fiber, Swagger UI และ validator ใน design proposal
  ตรวจรูปแบบ output จริงของ generator; Swagger 2.0 กับ OpenAPI 3.x ไม่ใช่รูปแบบเดียวกัน
  ห้ามใช้ syntax เช่น in: cookie ที่ไม่รองรับในรูปแบบที่เลือก หรือรับรองความสามารถจากชื่อเครื่องมืออย่างเดียว
  หาก generator แสดง cookie auth ได้ไม่ครบ ให้บันทึก flow ใน descriptions/examples และทดสอบ cookie/CSRF จริง
- เสิร์ฟ Swagger UI ที่ /docs และ generated spec ที่ /swagger.json และ /swagger.yaml บน Go API
  รองรับ API base path/reverse proxy ของเว็บ และให้ README ระบุ URL ที่เปิดได้จริงของแต่ละ environment
  bundle spec และ UI assets ไว้ใน image เพื่อให้เปิดเอกสาร local ได้โดยไม่ต้องโหลดจาก CDN
- จัดกลุ่ม Health, Auth, Password Reset, Portfolios, Transactions และ Holdings/Prices
  แต่ละ operation ต้องมี operationId ที่ไม่ซ้ำ, summary/description, parameters, request schema,
  response schema/status codes และตัวอย่างทั้งกรณีสำเร็จ/ข้อผิดพลาดที่เกี่ยวข้อง
- ระบุ required/nullable fields, validation limits, pagination, Idempotency-Key, rate-limit responses
  และ error envelope ที่มี code/message/request_id ตาม API contract
  เงิน ราคา และจำนวนหุ้นต้องระบุเป็น string พร้อมตัวอย่าง decimal; เวลาใช้ date-time พร้อม timezone
- อธิบาย cookie session, Set-Cookie, CSRF header และ endpoints ที่ไม่ต้อง login ให้ตรงกับระบบจริง
  ไม่เพิ่ม Bearer JWT authentication เพียงเพื่อให้ปุ่ม Authorize ของ Swagger ใช้ง่ายขึ้น
- การ Try it out ต้องใช้ browser-managed session cookie บน origin ที่ออกแบบไว้และส่ง CSRF token ตาม flow จริง
  ห้ามอ่าน HttpOnly cookie ด้วย JavaScript หรือปิด CSRF เพื่อให้เอกสารเรียก API ได้
  ตรวจข้อจำกัดของ Swagger UI ใน browser จริง; มีตัวอย่าง curl แบบ cookie jar + CSRF และขั้นตอน Google redirect
  สำหรับ flow ที่ลองผ่าน Swagger UI ไม่สะดวก โดยไม่ใส่ credentials จริงใน spec
- เปิดอ่านเอกสารใน dev/demo และรองรับ config เปิด/ปิด docs กับ Try it out แยกกันตอน deploy
  การเปิดเอกสารไม่ข้าม authentication/authorization/rate limits ของ API และไม่ expose maintenance operations
  ใช้ข้อมูลตัวอย่างสังเคราะห์ ปิด online validator ของ UI และ validate spec ใน CI แทน
- อัปเดต handlers/DTO, annotations และ contract tests แล้ว generate เอกสารใหม่ในชุดงานเดียวกันเมื่อเปลี่ยน endpoint
  ในช่วงทยอยพัฒนาให้ระบุ operation ที่ยังไม่พร้อมอย่างชัดเจน ไม่แสดงว่าทดลองเรียกได้แล้วทั้งหมด
  README ต้องมีลิงก์เอกสารและตัวอย่าง flow สมัคร → ยืนยันอีเมล → login → สร้างพอร์ต → ซื้อ/ขาย → logout

## กติกาพอร์ตและการคำนวณในเฟส 1
- หลายพอร์ตต่อผู้ใช้ตามขอบเขตข้างต้น ทุกพอร์ตใช้ USD และรองรับ fractional shares
  ไม่รองรับ short selling, margin, FX, การโอนหุ้นระหว่างพอร์ต หรือยอดเงินสดในเฟสนี้
- ใช้ moving weighted-average cost แยกตาม (portfolio_id, instrument_id)
  การคำนวณเป็น pure function ที่รับลำดับ position events ที่เรียงแล้ว; เฟส 1 มีเฉพาะ trade (buy/sell)
  corporate actions ในอนาคตเพิ่มเป็น event ชนิดใหม่โดยไม่แก้ตาราง transactions และไม่เปลี่ยนผลของประวัติที่มีแต่ trade
  - ซื้อ: เพิ่มจำนวนหุ้น และเพิ่มต้นทุนคงเหลือด้วย quantity × price + buy fee
  - ขาย: realized P&L = quantity × sell price − sell fee − ต้นทุนที่จัดสรรให้หุ้นที่ขาย
  - ต้นทุนที่จัดสรร = ต้นทุนคงเหลือก่อนขาย × จำนวนที่ขาย / จำนวนที่ถือก่อนขาย
  - หลังขาย ลดจำนวนและต้นทุนคงเหลือตามส่วน; ขายหมดให้ใช้ต้นทุนคงเหลือทั้งหมดเพื่อไม่เหลือเศษจากการปัด
  - average cost = ต้นทุนคงเหลือ / จำนวนที่ถือ; เมื่อไม่มีหุ้นให้จำนวน ต้นทุน และ average cost เป็นศูนย์
  - unrealized P&L = จำนวนที่ถือ × ราคาอ้างอิง − ต้นทุนคงเหลือ โดยไม่สมมติค่าธรรมเนียมขายในอนาคต
- อนุญาตเพิ่ม แก้ไข และลบรายการย้อนหลัง แต่ต้อง replay รายการของหุ้นนั้นในพอร์ตตามลำดับเวลา
  และปฏิเสธทั้งการเปลี่ยนแปลงหากทำให้จำนวนหุ้นติดลบ ณ จุดใดก็ตามในประวัติ
  หากแก้ symbol ต้องตรวจประวัติทั้ง symbol เดิมและใหม่ และห้ามเปลี่ยน portfolio_id ของรายการเดิม
- เก็บ executed_at เป็น timestamptz ใช้ UTC และมีลำดับรองที่คงที่สำหรับรายการเวลาเดียวกัน
  ต้องกำหนด tie-breaker ใน schema และใช้ลำดับเดียวกันใน SQL, core และ tests
- Transactions เป็นแหล่งข้อมูลหลัก; Holding เป็นผลคำนวณ ไม่เก็บยอดซ้ำเป็นอีกแหล่งความจริงในเฟสแรก
- ไม่รวม dividends, stock splits, mergers และ symbol changes ในเฟส 1
  ต้องระบุใน UI/README ว่าพอร์ตที่ได้รับผลจาก corporate actions เหล่านี้ยังคำนวณได้ไม่ครบถ้วน
  และผลคำนวณเป็นการติดตามพอร์ตตามวิธีที่กำหนด ไม่ใช่รายงานภาษี
- ลบพอร์ตได้เฉพาะพอร์ตที่ไม่มี transactions; ห้าม cascade ลบประวัติรายการโดยเงียบ ๆ

## ความแม่นยำของตัวเลขและความสอดคล้องของข้อมูล
- เงิน ราคา ค่าธรรมเนียม และจำนวนหุ้นใช้ shopspring/decimal ใน Go และ numeric ใน PostgreSQL
  ห้ามแปลงผ่าน float ระหว่าง parse ข้อมูล provider, คำนวณ, map repository หรือ serialize API
- ค่าที่ผู้ใช้บันทึก quantity, price และ fee ใช้ numeric(28,10); quantity และ price ต้องมากกว่าศูนย์
  ส่วน fee ต้องไม่ติดลบ Validate ช่วงค่าและจำนวนทศนิยมก่อนเขียน DB และปฏิเสธค่าที่เกินแทนการปัดเงียบ ๆ
- คูณ/บวก/ลบด้วย decimal; การหารสำหรับต้นทุนใช้ความละเอียด 28 ตำแหน่งหลังจุดทศนิยม
  ใช้ round-half-even สำหรับการหารและการแสดงผล ไม่แก้ global division precision ของ library
  UI แสดงเงินปกติ 2 ตำแหน่ง และแสดงจำนวนหุ้นได้ถึง 10 ตำแหน่ง โดยค่าที่แสดงไม่ถูกนำกลับไปคำนวณ
- API รับและส่งเงิน/ราคา/จำนวนหุ้นเป็น decimal string; frontend ไม่คำนวณยอดพอร์ตด้วย JavaScript number
  อนุญาตแปลงเป็น number เฉพาะตอนส่งค่าพิกัดให้ไลบรารีกราฟ และห้ามใช้ค่านั้นคำนวณหรือบันทึกธุรกรรม
- การตรวจสิทธิ์ อ่านประวัติ ตรวจยอด และเขียนการเปลี่ยนแปลงของพอร์ตต้องอยู่ใน DB transaction เดียว
  ใช้ row lock บน portfolio เพื่อ serialize การแก้ไขในพอร์ตเดียวกัน และทุกเส้นทางที่แก้รายการต้องใช้ lock เดียวกัน
  ยอมแลก concurrency ภายในพอร์ตเพื่อให้ถูกต้องและเรียบง่ายในเฟสแรก; ห้ามถือ lock ระหว่างเรียก external API
- ป้องกันการเขียนทับข้อมูลที่ผู้ใช้ไม่ได้เห็น (lost update) ด้วย optimistic locking: portfolios/transactions มี version
  response ส่ง ETag และ PUT/DELETE ต้องส่ง If-Match; ไม่ส่งคืน 428, version ไม่ตรงคืน 412 โดยไม่เขียนข้อมูล
  row lock กับ version ทำงานคู่กัน: lock กันการเขียนพร้อมกัน ส่วน version กันการแก้จากข้อมูลเก่าที่ client ถืออยู่
- การสร้าง transaction ต้องรองรับ Idempotency-Key โดยผูกกับผู้ใช้ พอร์ต และ operation
  key เดิมกับ payload เดิมคืนผลเดิม; key เดิมกับ payload ต่างกันคืน conflict
  บันทึก key และผลการสร้างแบบ atomic พร้อม unique constraint เพื่อรองรับ concurrent retries
  ต้องรวมที่เก็บ idempotency ใน schema proposal และกำหนดอายุการเก็บให้ชัดเจนก่อนลงมือ
- DB ต้องมี foreign keys, unique constraints, CHECK constraints และ indexes ตาม access patterns
  query ที่เข้าถึงข้อมูลผู้ใช้ต้องตรวจ ownership ผ่าน user_id ของ portfolio ห้ามเชื่อ user_id จาก request body

## Middleware และ HTTP boundary

- เริ่ม request ID, structured access log และ panic recovery ใน milestone 1 พร้อมโครง Go
  log ต้องบันทึก final HTTP status หลัง error handler ทำงาน รวมกรณี panic; ไม่ log secrets หรือ raw request body
- เพิ่ม session authentication, CSRF/Origin checks และ login rate limit พร้อม auth ใน milestone 4
  เพิ่ม OTP limits ใน milestone 5 โดยใช้ durable state ใน PostgreSQL ตามข้อกำหนดด้านล่าง
- Protected routes ต้องผ่าน authentication; ownership ของพอร์ต/รายการต้องตรวจซ้ำใน service และ query
  middleware ไม่ทดแทนการตรวจสิทธิ์ระดับข้อมูล และห้ามเชื่อ user_id ที่ client ส่งมา
- ใช้ same-origin เป็นค่าเริ่มต้น เปิด CORS เฉพาะเมื่อมีความจำเป็นและกำหนด exact allowlist
- Business audit เกิดใน use case ที่รู้ผลจริงและอยู่ใน DB transaction เดียวกับ mutation
  access-log middleware ไม่สร้าง success audit ของทุก request แทน use case
- โค้ดอยู่ใน internal/adapter/http/middleware/; เลือก built-in middleware ให้ตรงกับ Fiber major
  และทดสอบลำดับการทำงานของ middleware ผ่าน HTTP tests

## รายละเอียด Auth, Session และ Google identity
- ตาราง sessions มี user_id, token_hash, created_at, expires_at และข้อมูลสำหรับ revoke
  สุ่ม opaque token ด้วย crypto/rand อย่างน้อย 32 bytes และเก็บเฉพาะ hash ใน DB
- อายุ session เริ่มต้น 7 วันแบบ absolute expiry ปรับผ่าน config ได้; ตรวจ expiry/revocation ทุกคำขอ
  สร้าง session token ใหม่เมื่อ login สำเร็จ; logout revoke ฝั่ง server และล้าง cookie
- Production cookie ใช้ HttpOnly, Secure, SameSite=Lax, Path=/ และไม่กำหนด Domain หากไม่จำเป็น
  dev บน HTTP ใช้ Secure=false ได้เฉพาะ dev config; ห้าม log session token, password, OTP หรือ OAuth tokens
- ป้องกัน CSRF สำหรับคำขอที่เปลี่ยนข้อมูลด้วย CSRF token และตรวจ Origin ตาม allowlist
  ไม่ถือว่า HttpOnly หรือ SameSite อย่างเดียวเพียงพอ; GET ต้องไม่เปลี่ยนข้อมูลธุรกิจ
- วาง web/API ให้ browser เรียกผ่าน origin เดียวกัน เช่น /api ผ่าน reverse proxy
  หากต้องแยก origin ให้เสนอ cookie/CORS/CSRF design ก่อน และห้ามใช้ wildcard origin พร้อม credentials
- จำกัดความถี่ login ตามบัญชีและ IP; ใช้ข้อความ login ล้มเหลวที่ไม่เปิดเผยว่ามีบัญชีหรือไม่
  อีเมลที่ไม่มีบัญชีหรือบัญชี Google-only ต้องตรวจ password hash จำลองด้วย parameters เดียวกัน
  เพื่อไม่ให้เวลาตอบเปิดเผยสถานะบัญชี
  กำหนด trusted proxies ก่อนใช้ forwarded IP เพื่อป้องกันการปลอม IP หลบ rate limit
- Normalize email ด้วย trim และกติกา case-insensitive ที่ใช้สม่ำเสมอพร้อม unique constraint
  ห้ามลบจุดหรือ +suffix ตามกติกาของผู้ให้บริการบางรายโดยอัตโนมัติ
- Google ใช้ OpenID Connect Authorization Code Flow; ตรวจ state, nonce, signature, issuer, audience และ expiry
  state/nonce ต้องผูกกับ browser ที่เริ่ม flow มีอายุสั้น และใช้ได้ครั้งเดียว
  OIDC callback ใช้การตรวจ state/nonce ตาม protocol ไม่คาดหวัง CSRF header ของแอปจาก Google
  ใช้ PKCE (S256) ร่วมด้วยแม้เป็น confidential client ตามแนวปฏิบัติ OAuth 2.0 Security BCP (RFC 9700)
- auth_providers ใช้ unique (provider, provider_subject) โดย Google provider_subject มาจาก sub
  ห้ามใช้ email เป็น provider identity และห้ามเชื่อมบัญชีเดิมอัตโนมัติเพียงเพราะ email ตรงกัน
- การเชื่อม Google กับบัญชีเดิมต้องยืนยันสิทธิ์บัญชีเดิมก่อนและยืนยัน Google identity ใน flow เดียวกัน
  บัญชี Google-only มี password_hash เป็น NULL; forgot-password ไม่ใช้เพิ่ม local password ให้บัญชีชนิดนี้
- Core รับ identity ที่ adapter ตรวจแล้วผ่าน IdentityProvider port โดยไม่รับ Google SDK types หรือ raw ID token claims

## รายละเอียด Password Reset / OTP
- OTP เป็น string ตัวเลข 6 หลัก รวมกรณีขึ้นต้นด้วยศูนย์ สุ่มแบบสม่ำเสมอด้วย crypto/rand และหมดอายุใน 10 นาที
- เก็บ HMAC-SHA-256 ของ OTP ที่ผูกกับ reset challenge และ user โดยใช้ secret แยกจาก DB ผ่าน env
  ห้ามเก็บ OTP ดิบหรือใช้ plain fast hash อย่างเดียว เพราะรหัส 6 หลักมีจำนวนค่าที่เดาได้จำกัด
  เปรียบเทียบ digest แบบ constant-time และไม่ส่ง digest/OTP กลับใน API response
- otp_challenges (purpose password_reset หรือ email_verification) ต้องมีข้อมูลสำหรับ expires_at, consumed_at, invalidated_at และ failed_attempts
  หนึ่งบัญชีมี challenge ที่ใช้งานได้ครั้งละหนึ่งรายการ; ขอใหม่ต้อง invalidate รหัสเก่าแบบ atomic
- ค่าเริ่มต้น: กรอกผิดได้ไม่เกิน 5 ครั้งต่อ challenge, ขอใหม่เว้นอย่างน้อย 60 วินาที,
  ขอได้ไม่เกิน 5 ครั้งต่อชั่วโมงต่อบัญชี/อีเมล และ 20 ครั้งต่อชั่วโมงต่อ IP; ปรับผ่าน config ได้
  ค่า config ต้องอยู่ในเพดานที่ DB CHECK รองรับ (เช่น failed_attempts) และ validate ตอนเริ่มระบบ
  การออก challenge ใหม่ต้องไม่ล้างประวัติ rate limit ต่อบัญชี/IP และ counters ต้องอัปเดตแบบ atomic
- เก็บ rate-limit state ใน PostgreSQL สำหรับเฟสแรกเพื่อไม่ให้ restart API แล้วข้ามโควตาได้
  รวมที่เก็บ counters/events และนโยบายล้างข้อมูลหมดอายุใน schema proposal; ไม่เพิ่ม Redis ในเฟสนี้
- Forgot-password ตอบข้อความและ status แบบเดียวกันสำหรับอีเมลที่มี/ไม่มีบัญชีหรือเป็น Google-only
  ลดความต่างของเวลาตอบ และใช้กติกา rate limit แบบเดียวกันเพื่อไม่เปิดเผยสถานะบัญชี
- การตรวจ OTP, consume challenge, เปลี่ยน password hash และ revoke sessions ทั้งหมดต้อง atomic
  ใช้ challenge ได้ครั้งเดียวแม้มีคำขอพร้อมกัน; ห้ามใช้ค่า verified จาก client เป็นหลักฐานผ่าน OTP
  หากแยก verify/reset เป็นคนละ endpoint ต้องออก reset token อายุสั้น ใช้ครั้งเดียว และใช้ได้เฉพาะเปลี่ยนรหัสผ่าน
- หลัง reset สำเร็จให้ login ใหม่และส่งอีเมลแจ้งการเปลี่ยนรหัสผ่านโดยไม่ส่ง password
  การส่งอีเมลเกิดนอก DB transaction; หากส่งไม่สำเร็จต้องบันทึก error ที่ไม่มี secret
  อย่าถือว่า DB กับการส่งอีเมลเป็น atomic operation หรือ rollback รหัสผ่านที่เปลี่ยนสำเร็จแล้วเพราะแจ้งเตือนไม่สำเร็จ

## Application log และ Audit log

- แยก application log สำหรับวิเคราะห์การทำงานออกจาก audit log สำหรับประวัติการกระทำ
  Supabase Database logs ไม่ทดแทน audit ของ use case และไม่ใช้ Supabase Auth Audit Logs เพราะระบบนี้ใช้ Go Auth
- Application log ใช้ log/slog แบบ structured JSON ไป stdout และให้ Docker/hosting จัดเก็บ
  สร้าง logger และ inject จาก cmd/*/main.go; ไม่ใช้ global logger และไม่เพิ่ม ELK/Loki หรือบริการเสียเงินในเฟสแรก
- Application log มี timestamp UTC, level, service, event/message และ request_id
  HTTP access log เก็บ method, route template, status และ duration; error log เพิ่ม error_code และ context ที่จำเป็น
  ใช้เชื่อมโยง request ช้า, DB errors, provider timeout/rate limit และการส่งอีเมลล้มเหลว
- request_id ต้องสร้างหรือ validate ความยาว/รูปแบบก่อนใช้ร่วมกันใน HTTP response, application log และ audit
  log level ปรับผ่าน config ได้ โดย production ไม่เปิด debug ที่มีรายละเอียดเกินจำเป็น
- ห้าม log password, OTP, password/OTP hash, session token, cookie, Authorization header,
  OAuth code/token, API keys, connection strings ที่มี credentials หรือ request/response body ทั้งก้อน
  ใช้ metadata แบบ allowlist จำกัดความยาวและ sanitize ข้อมูลจากผู้ใช้เพื่อป้องกัน log injection
- Audit เก็บใน audit_events ด้วย SQL/sqlc ผ่าน repository และ goose migration ตามสถาปัตยกรรมเดิม
  ฟิลด์ขั้นต่ำ: id, occurred_at แบบ timestamptz/UTC, event_type, actor_user_id ที่ nullable,
  request_id, session_id ที่ nullable, target_type, target_id, outcome, reason_code และ metadata แบบ jsonb
  session_id เป็น ID อ้างอิงภายในที่ไม่ใช้ authenticate ห้ามใช้ session token หรือ token hash แทน
- actor_user_id ต้องมาจาก identity ที่ยืนยันแล้ว; login ที่ล้มเหลวใช้ actor เป็น NULL
  แม้ค้นพบอีเมลที่ตรงกับบัญชี ห้ามบันทึกว่าเจ้าของบัญชีนั้นเป็นผู้กระทำที่ยืนยันแล้ว
- เก็บ client IP/user-agent เฉพาะที่จำเป็น โดยปิดบัง IP และจำกัดความยาว user-agent ตามนโยบายที่ระบุใน design
  ใช้ trusted proxy config สำหรับหา IP จริง ห้ามเชื่อ X-Forwarded-For จาก client โดยตรง
  ไม่เก็บอีเมลดิบหรือข้อมูลระบุตัวบุคคลซ้ำใน metadata โดยไม่มีเหตุผล

### เหตุการณ์ที่ต้องมีในเฟส 1

| กลุ่ม | Event types และความหมาย |
| --- | --- |
| สมัคร/เข้าสู่ระบบ | auth.registered, auth.login_succeeded, auth.login_failed โดยระบุ method เป็น password/google |
| ยืนยันอีเมล | auth.email_verification_requested, auth.email_verified, auth.email_verification_failed โดยไม่เก็บตัวรหัส |
| ออกจากระบบ | auth.logout, auth.sessions_revoked โดยระบุเหตุผล เช่น password_reset |
| กู้รหัสผ่าน | auth.password_reset_requested, auth.otp_verification_failed, auth.password_reset_succeeded โดยไม่เก็บตัวรหัส |
| เชื่อม identity | auth.identity_linked, auth.identity_link_failed โดยระบุ provider |
| พอร์ต | portfolio.created, portfolio.updated, portfolio.deleted |
| รายการซื้อขาย | transaction.created, transaction.updated, transaction.deleted |
| ความปลอดภัย | security.access_denied, security.rate_limited |

- Login สำเร็จหมายถึงสร้าง session สำเร็จฝั่ง server; logout หมายถึง revoke session สำเร็จ
  บันทึกเวลาที่ server ทำสำเร็จ ไม่อ้างว่าเป็นเวลาที่ browser รับ cookie หรือผู้ใช้เลิกใช้งานจริง
- การปิด browser/แท็บไม่ใช่ logout; ใช้ sessions.expires_at เป็นเวลาหมดอายุและแยกจากการ revoke
  ไม่สร้าง logout event ปลอมเมื่อ session หมดอายุ และไม่รับประกันว่าจะมี background event ตรงวินาทีที่หมดอายุ
- auth.password_reset_requested หมายถึงรับคำขอ reset ไม่ใช่หลักฐานว่าอีเมลส่งถึงผู้ใช้แล้ว
  คำขอจากผู้ที่ยังไม่ยืนยันตัวตนต้องใช้ actor เป็น NULL และไม่เปลี่ยนข้อความตอบกลับให้เปิดเผยสถานะบัญชี
- การแก้ไข/ลบ transactions เก็บ before/after เฉพาะฟิลด์ธุรกิจที่อนุญาต เช่น symbol, side,
  quantity, price, fee และ executed_at; ค่าทศนิยมใน metadata ใช้ string ตาม API
  target_id ต้องยังอ้างอิงเหตุการณ์ได้หลังลบข้อมูลต้นทาง และห้าม cascade ลบ audit ตาม transaction/portfolio/session
- Event ของการเปลี่ยนข้อมูลสำเร็จ เช่น สร้าง/revoke session, reset password, เชื่อม identity,
  แก้พอร์ต และแก้รายการ ต้อง commit ใน DB transaction เดียวกับข้อมูลนั้น
  หากเขียน audit ไม่สำเร็จ ให้ rollback การเปลี่ยนแปลงและคืน domain error; ห้ามรายงานสำเร็จโดยไม่มี audit
- เหตุการณ์ล้มเหลว เช่น login/OTP ไม่ผ่าน ต้องบันทึกโดยไม่ถูก rollback หายไปกับ operation ที่ไม่สำเร็จ
  attempt counters และ audit ของการลองผิดต้อง commit ได้แม้ผล use case เป็น authentication failure
  หาก DB ใช้งานไม่ได้ ให้เขียน application error พร้อม request_id โดยไม่กลืน error หรืออ้างว่ามี durable audit แล้ว
- Idempotent retry ต้องไม่สร้าง success audit ซ้ำสำหรับการเปลี่ยนแปลงเดียวกัน
  การ logout session ที่ revoke ไปแล้วต้องไม่สร้างเหตุการณ์ว่า revoke สำเร็จใหม่ทุกครั้ง
- audit_events เป็น append-only สำหรับ runtime role: ไม่ให้ UPDATE/DELETE และไม่ให้ผู้ใช้เขียน event เอง
  การอ่านผ่านแอปต้องผ่านการตรวจสิทธิ์; ไม่เปิดตาราง audit ให้ browser หรือ public Data API เข้าถึง
  สิทธิ์นี้ป้องกันการแก้จากแอป ไม่ใช่การรับรองว่า DB administrator แก้ไขข้อมูลไม่ได้
- Retention เริ่มต้น 90 วัน ปรับผ่าน config ได้ มี cleanup แบบ batch ผ่าน maintenance role ที่แยกจาก runtime
  ให้ระบุวิธี schedule, index สำหรับค้นตามผู้ใช้/เวลา และขนาด metadata สูงสุดใน design proposal
  ติดตามขนาดตารางและ indexes ให้เหมาะกับ free tier; การล้างตาม retention เป็นข้อยกเว้นของ append-only
- ไม่เก็บ audit สำหรับทุก GET หรือทุก healthcheck ใช้ application access log สำหรับการเข้าถึงทั่วไป
  ไม่ทำ sampling ของ success audit ที่เปลี่ยนข้อมูลธุรกิจ; ต้องจำกัดปริมาณ/ขนาด log จาก traffic ที่โจมตีอย่างชัดเจน

## รายละเอียดข้อมูลราคา
- PriceProvider คืน PriceQuote ที่มี symbol, price แบบ decimal, currency, as_of, source/feed และสถานะข้อมูลเดโม
  metadata ใน core ใช้ชนิดข้อมูลกลาง ไม่ผูกกับ Alpaca response types
- เฟส 1 ใช้ close ของแท่งราคาล่าสุดที่อยู่ก่อน cutoff now − 15 นาทีเป็นราคาอ้างอิง
  ระบุใน UI ว่าเป็น delayed bar close ไม่ใช่ราคาเสนอซื้อ/ขายหรือราคาที่รับประกันว่าจะซื้อขายได้
- as_of ต้องเป็นเวลาของข้อมูลต้นทาง ไม่ใช่เวลาที่ server เพิ่ง fetch; ถ้า provider ส่งเวลาเริ่มแท่ง
  ให้ adapter เก็บความหมายของช่วงแท่งอย่างถูกต้อง และแยก fetched_at สำหรับอายุ cache
- แสดงแหล่งราคาและ as_of บนหน้าพอร์ต; ต้องกำหนด stale policy ตาม trading session และวันหยุดตลาด
  ราคาวันทำการก่อนหน้าเมื่อปิดตลาดต้องแสดงเวลาจริง ไม่แสดงเหมือนมีการอัปเดตราคาวันนี้
- เริ่มด้วย batch fetch และ cache อายุสั้นใน adapter ที่สร้างผ่าน DI พร้อม timeout, rate-limit handling
  และ bounded retry/backoff เฉพาะ read requests ที่ retry ได้; ไม่เพิ่ม WebSocket/Redis ในเฟส 1
- เมื่อดึงราคาไม่ได้ ใช้ cache ล่าสุดได้โดยระบุว่าเป็น cached/stale พร้อมเวลา
  หากไม่มีราคา ให้ unrealized P&L ของหุ้นนั้นเป็น unavailable ไม่แทนราคาด้วยศูนย์
  ยอดรวมที่ขาดราคาบางหุ้นต้องระบุว่าไม่ครบ; realized P&L และประวัติรายการยังดูได้
- live/pre-market/after-hours/overnight อยู่เฟสถัดไป; ต้องตรวจ feed, สิทธิ์ และขอบเขตข้อมูลอีกครั้งก่อนเพิ่ม
  หน้าหุ้นรายตัวและกราฟอยู่เฟสถัดไป (ยืนยัน 2026-10-14) ให้เสนอ timeframe, ช่วงประวัติ และ PriceProvider method ก่อนลงมือ
 
## ข้อกำหนดด้านโค้ด
- ใช้ shopspring/decimal กับเงินและจำนวนหุ้นตามกติกาด้านบน ห้ามใช้ float ในเส้นทางคำนวณหรือจัดเก็บ
- SQL เขียนเองใน query files แล้ว generate ด้วย sqlc
- migration ด้วย goose ทุกครั้งที่เปลี่ยน schema
- ระวัง fiber.Ctx ถูกนำกลับมาใช้ซ้ำ: ห้ามเก็บ Ctx หรือค่าอ้างอิงจาก Ctx ไปใช้ใน goroutine
  ให้ copy ค่าที่ต้องใช้ออกมาก่อน
- ห้ามให้ frontend เรียก data provider ตรง และห้ามฝัง API key ในโค้ด (ใช้ env)
- error handling ชัดเจน ไม่กลืน error ใช้ domain errors ที่ core กำหนด
  แล้วให้ HTTP adapter แปลงเป็น status code
- เขียน unit test ให้ core (คำนวณพอร์ต, OTP) โดยใช้ mock/fake ของ port
  และ integration test ให้ repository กับ Postgres จริง
- ทดสอบการแยกข้อมูลและผลคำนวณระหว่างพอร์ต รวมถึงการป้องกันผู้ใช้เข้าถึงพอร์ตของผู้อื่น
- เพิ่ม tests สำหรับ fractional shares, fees, partial/full sale, rounding, รายการย้อนหลัง และราคา unavailable
- ทดสอบ OTP expiry ด้วย Clock fake, resend invalidation, attempt limits, single-use และ session revocation
- Integration tests ต้องครอบคลุม concurrent sells, duplicate retries, concurrent OTP reset และ ownership checks
- ทำ HTTP tests ของ auth/cookie/CSRF และแต่ละ use case เมื่อเชื่อม handler ไม่รอรวมทดสอบตอนท้ายทั้งหมด
- ตรวจ Swagger UI โหลด spec ได้ และตัวอย่าง/HTTP contract ตรงกับ endpoints ที่เปิดใช้งานจริง
- ทดสอบ audit event/actor/outcome ให้ตรงกับผลจริง, secret redaction, atomic commit/rollback,
  failed login/OTP ที่ยังมี audit, idempotent retry ที่ไม่เพิ่ม success event ซ้ำ และสิทธิ์ append-only/retention cleanup
 
## การดูแลและการขยายระบบในอนาคต

- หลักคิด: column ที่เป็น NULL ได้ ตารางใหม่ และ endpoint ใหม่เพิ่มภายหลังได้ง่าย ไม่ต้องเพิ่มล่วงหน้า
  ส่วนที่แก้ภายหลังยาก ได้แก่ primary/foreign keys, ความหมายของ column, รูปแบบ API ที่ client ใช้อยู่ และกติกา migration
  ต้องตัดสินใจอย่างรอบคอบและบันทึกเป็น ADR
- API evolution: ภายใน /api/v1 เปลี่ยนแบบเพิ่มเท่านั้น — เพิ่ม endpoint, response field หรือ request field ที่ไม่บังคับได้
  ห้ามลบ/เปลี่ยนชื่อ field, เปลี่ยนชนิดหรือความหมาย, ทำ field ที่ไม่บังคับให้บังคับ หรือเปลี่ยน status code ของกรณีเดิม
  client ต้องไม่ล้มเมื่อเจอ response field หรือ error code ที่ไม่รู้จัก; การเปลี่ยนที่ไม่เข้ากันต้องทำเป็น /api/v2
  และเปิดคู่กับ v1 ช่วงหนึ่งพร้อมประกาศ deprecation ใน Swagger/README
- Migration: goose SQL ใช้เลขลำดับต่อเนื่อง (goose create -s) ห้ามแก้ migration ที่รันในสภาพแวดล้อมที่ใช้ร่วมกันแล้ว
  ให้แก้ด้วย migration ใหม่เสมอ; เปลี่ยน schema แบบ expand → migrate → contract ข้าม release
  เพื่อให้ API เวอร์ชันก่อนหน้ายังทำงานกับ schema ใหม่ได้ระหว่าง deploy และ rollback image ได้โดยไม่ rollback DB
  สร้าง index บนตารางที่มีข้อมูลแล้วด้วย CONCURRENTLY ใน migration ที่ไม่ครอบ transaction
  เขียน Down migration สำหรับ dev/test; production แก้ปัญหาด้วย forward migration ไม่พึ่ง Down ที่ทำลายข้อมูล
- ADR: การตัดสินใจด้าน architecture, schema keys, security หรือ API contract ที่มี trade-off ให้บันทึกใน docs/adr/
  หนึ่งไฟล์ต่อหนึ่งเรื่อง (บริบท → ทางเลือก → การตัดสินใจ → ผลที่ตามมา) ห้ามแก้เนื้อหา ADR ที่ Accepted แล้ว
  ถ้าเปลี่ยนการตัดสินใจให้เขียน ADR ใหม่และทำเครื่องหมาย Superseded ที่ฉบับเดิม

## เอกสารออกแบบและลำดับความสำคัญ

- Design spec: [docs/superpowers/specs/2026-10-05-phase-1-design.md](docs/superpowers/specs/2026-10-05-phase-1-design.md)
  — schema, port signatures, API contract, version pins และรายละเอียดที่ไฟล์นี้ไม่ได้กำหนด
- Roadmap: [docs/superpowers/plans/2026-10-05-phase-1-roadmap.md](docs/superpowers/plans/2026-10-05-phase-1-roadmap.md)
  — milestones, เกณฑ์ตรวจรับ และสถานะปัจจุบัน; แผนย่อยของแต่ละ milestone เก็บใน docs/superpowers/plans/
- ER diagram: [docs/database/schema.dbml](docs/database/schema.dbml) สำหรับ dbdiagram.io
- ไฟล์นี้กำหนดข้อตกลงหลัก; spec ที่ผู้ใช้ยืนยันแล้วเป็นแหล่งรายละเอียด หาก spec ขัดกับไฟล์นี้ให้ถามก่อน ห้ามเลือกเอง
  ข้อเสนอใน spec ที่ยังไม่ได้รับการยืนยันใช้เป็นแนวทางได้ แต่ห้ามถือว่าเป็นข้อตกลงแล้ว
- goose migrations เป็นแหล่งความจริงของ schema; schema.dbml และตาราง schema ใน spec ต้องอัปเดตในชุดงานเดียวกับ migration
  ที่เปลี่ยน columns, constraints, indexes หรือ references เพื่อไม่ให้เอกสารกับ DB จริงไม่ตรงกัน
- เมื่อจบหรือเปลี่ยนสถานะ milestone ให้อัปเดต checkbox/สถานะใน roadmap และ spec ไปพร้อมกัน
- ADRs: [docs/adr/](docs/adr/README.md) — เหตุผลของการตัดสินใจสำคัญ อ่านก่อนเสนอเปลี่ยนเรื่องที่มี ADR อยู่แล้ว

## วิธีทำงาน
1. ก่อนเขียนโค้ด ให้เสนอ: โครงโฟลเดอร์ที่ปรับแล้ว (รวมไฟล์ Docker/CI), ER diagram แบบข้อความ,
   schema ของตาราง (users, auth_providers, sessions, portfolios, transactions, otp_challenges, audit_events
   รวมที่เก็บ rate limit และ idempotency),
   รายการ ports (พร้อม method signatures), API endpoints/request-response contract และรายการ services ใน docker-compose
   รวมวิธี generate Swagger จาก Go annotations และแยกงาน backend/frontend ให้ชัดเจน
   แล้วรอให้ฉันยืนยัน
2. หลังยืนยัน ทำทีละส่วนตามลำดับ:
   backend docker-compose + โครง Go + /healthz + structured logger + Swagger + CI เบื้องต้น → domain+ports
   → schema+migration+sqlc รวม audit repository → auth/session + handlers → OTP + handlers
   → portfolios + handlers → transactions + handlers
   → PriceProvider/คำนวณพอร์ต + handlers → ตรวจ backend ทั้งระบบพร้อม Swagger/คู่มือ
   → เริ่ม Next.js พร้อม web Docker/CI และเชื่อม API → ตรวจทั้งระบบและ deploy
   แต่ละ use case ต้องเพิ่ม audit, Swagger annotations/generated docs และ contract tests ไปพร้อมกัน
   ช่วง backend เชื่อมและทดสอบผ่าน HTTP ทีละส่วนโดยไม่ต้องสร้าง frontend รอไว้
3. แต่ละส่วนให้อธิบายสั้น ๆ ว่าทำไมออกแบบแบบนี้ และมี trade-off อะไร
4. ถ้ามีจุดไหนคลุมเครือ ให้ถามฉันก่อนเดา

## เอกสารอ้างอิงสำหรับรายละเอียดที่อาจเปลี่ยนตามบริการ
- [Alpaca plans](https://docs.alpaca.markets/us/v1.1/docs/about-market-data-api)
- [Alpaca feeds/ข้อจำกัด historical SIP](https://docs.alpaca.markets/us/docs/market-data-faq)
- [Session/cookie](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
- [Password reset](https://cheatsheetseries.owasp.org/cheatsheets/Forgot_Password_Cheat_Sheet.html)
- [Google OpenID Connect](https://developers.google.com/identity/openid-connect/openid-connect)
- [Supabase PostgreSQL connections](https://supabase.com/docs/guides/database/connecting-to-postgres)
- [Supabase Data API security](https://supabase.com/docs/guides/api/securing-your-api)
- [Supabase pricing/free tier](https://supabase.com/pricing)
- [Go log/slog](https://pkg.go.dev/log/slog)
- [OWASP Logging](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)
- [Swagger cookie authentication](https://swagger.io/docs/specification/v3_0/authentication/cookie-authentication/)
- [Swagger UI configuration](https://swagger.io/docs/open-source-tools/swagger-ui/usage/configuration/)
- [Swaggo generator](https://github.com/swaggo/swag)
- [Fiber Swagger integration](https://docs.gofiber.io/recipes/swagger/)
- [ตัวอย่างแนวทาง annotations ที่ผู้ใช้เลือก](https://mikelopster.dev/docs/c/goapi-essential/chapter-3/swagger)
- ตรวจเงื่อนไขบริการจากเอกสารทางการอีกครั้งตอนเชื่อม provider หรือ deploy จริง ไม่ยึดราคา/free tier แบบถาวร
 
ตอบเป็นภาษาไทย ส่วนโค้ดและชื่อตัวแปรเป็นภาษาอังกฤษ
