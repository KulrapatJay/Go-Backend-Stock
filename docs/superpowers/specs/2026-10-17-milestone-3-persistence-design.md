# Milestone 3 — Schema, sqlc, repositories และ audit persistence (design)

- **สถานะ:** ผู้ใช้ยืนยันแล้ว 2026-10-17; แผน 3a ผ่านการตรวจรับ 2026-10-20: [2026-10-17-milestone-3a-schema-sqlc-audit.md](../plans/2026-10-17-milestone-3a-schema-sqlc-audit.md); แผน 3b ผ่านการตรวจรับ 2026-10-21: [2026-10-20-milestone-3b-auth-repositories.md](../plans/2026-10-20-milestone-3b-auth-repositories.md); แผน 3c ผ่านการตรวจรับ 2026-10-22: [2026-10-21-milestone-3c-portfolio-repositories.md](../plans/2026-10-21-milestone-3c-portfolio-repositories.md)
- **อ้างอิง:** [AGENTS.md](../../../AGENTS.md), [phase 1 spec](2026-10-05-phase-1-design.md) หัวข้อ 4, 7, 8, 9, 11, [milestone 2 design](2026-10-16-milestone-2-domain-ports-design.md), [ADR-0013](../../adr/0013-domain-value-types.md), [roadmap](../plans/2026-10-05-phase-1-roadmap.md) milestone 3 รวม "ข้อที่ส่งต่อให้ milestone 3" ใต้ milestone 2

## 1. เป้าหมาย การแบ่งงาน และเกณฑ์ผ่าน

สร้าง schema ทั้งหมดของ phase 1 ใน PostgreSQL ด้วย goose, generate query ด้วย sqlc และ implement repository ports ที่ milestone 2 สร้างไว้ทุกตัว พร้อม `UnitOfWork` และ audit ที่ commit/rollback ไปกับข้อมูลธุรกิจ ยังไม่มี HTTP endpoint ใหม่

ผู้ใช้เลือก (2026-10-17): **spec เดียว ลงมือเป็น 3 แผนย่อย** แต่ละแผนตรวจรับแยกกัน

| แผน | ขอบเขต | ผู้ใช้ต่อ |
| --- | --- | --- |
| 3a | migrations ทุกตาราง, roles/GRANT, sqlc toolchain, mapper, การแปลง error, `UnitOfWork`, `AuditRepository`, tests สิทธิ์และ failure injection, CI | ทุก milestone |
| 3b | `UserRepository`, `SessionRepository`, `IdentityRepository`, `OIDCFlowRepository`, `OTPChallengeRepository`, `RateLimitRepository` | milestone 4–5 |
| 3c | `PortfolioRepository`, `TransactionRepository`, `InstrumentRepository`, `IdempotencyRepository`, keyset pagination | milestone 6–7 |

**ผ่านเมื่อ** (roadmap): schema สร้างจากศูนย์ได้, generated code ไม่มี diff, role permissions ถูกต้อง และ integration tests ผ่านโดยไม่ใช้ Supabase จริง; เพิ่มเติมจาก design นี้: ทุก method ของ ports มี integration test กับ PostgreSQL จริงผ่าน role `app_runtime`

## 2. การตัดสินใจที่ยืนยันแล้ว (2026-10-17)

| เรื่อง | ทางเลือกที่ยืนยัน | เหตุผล |
| --- | --- | --- |
| การแบ่งงาน | spec เดียว + แผน 3a/3b/3c | milestone ใหญ่ราว 3–4 เท่าของ milestone 2; รีวิวและตรวจรับเป็นส่วนได้ |
| การแปลงชนิด DB ↔ domain | ชนิดมาตรฐานของ sqlc/pgx (`pgtype.Numeric`, `pgtype.UUID` ฯลฯ) + mapper เขียนเองที่ parse ผ่าน `Parse*` | ตรง ADR-0013; ไม่เพิ่ม dependency (`pgx-shopspring-decimal`) ที่มีจุดพังตอน runtime |
| SQL | เขียนเองใน query files แล้ว generate ด้วย sqlc ทั้งหมด รวม rate limit | AGENTS.md |

## 3. Migrations, roles และสิทธิ์ (แผน 3a)

### 3.1 ลำดับไฟล์

goose แบบเลขต่อเนื่อง (`goose create -s`), ห้ามแก้ไฟล์ที่รันแล้ว, columns/CHECK/index ตาม phase 1 spec หัวข้อ 7 ทุกตัว

| ไฟล์ | ตาราง |
| --- | --- |
| `migrations/00002_auth.sql` | `app.users`, `app.auth_providers`, `app.sessions`, `app.otp_challenges`, `app.oauth_flows` |
| `migrations/00003_portfolio.sql` | `app.portfolios`, `app.instruments`, `app.transactions`, `app.idempotency_keys` |
| `migrations/00004_audit_ratelimit.sql` | `app.audit_events`, `app.rate_limit_keys`, `app.rate_limit_events` |

แต่ละไฟล์มี Down สำหรับ dev/test ตาม AGENTS.md (production แก้ด้วย forward migration)

### 3.2 รายละเอียดที่เพิ่มจาก phase 1 spec

- **ขนาด audit metadata:** เพดานจริงคือ 8 KiB (raw bytes) ที่ `domain.NewAuditEvent` ตรวจ ส่วน CHECK `octet_length(metadata::text) <= 9216` ใน DB เป็น**ตัวกันสำรองที่นับต่างวิธี ไม่ใช่เพดานเดียวกัน** เพราะ `jsonb::text` เพิ่มช่องว่างหลังทุก `,` และ `:` (วัดจริง: 8101 ไบต์กลายเป็น 9900) จึงมี metadata ที่ Go ยอมรับแต่ DB ปฏิเสธและ rollback เป็น `internal` (ทดสอบแล้ว `TestAuditMetadataSizeIsCountedDifferentlyInGoAndDatabase`); metadata ที่ระบบสร้างเองเล็กและเก็บตัวเลขเป็น string จึงไม่เกิดจริง (ผู้ใช้เลือกคงไว้แบบนี้ 2026-10-20); idempotency result ใช้หลักเดียวกัน 16 KiB → `octet_length(result::text) <= 17408`
- **email:** UNIQUE บน `users.email` ตรงๆ โดยค่าที่เก็บคือค่าที่ Go `ParseEmail` normalize แล้ว; ไม่มี CHECK หรือ index บน `lower(email)` และ query ห้ามใช้ `lower()` กับ email เพื่อไม่ให้ต่างจาก `strings.ToLower` ของ Go กับอักษรนอก ASCII
- **ชื่อพอร์ต:** UNIQUE INDEX `(user_id, lower(name))` ตาม spec (ชื่อผ่าน `ParsePortfolioName` แล้ว จึงไม่มีอักขระล่องหน)
- **FK:** ทุก FK เป็น `ON DELETE RESTRICT`; `audit_events` ไม่มี FK ไปตารางใด จึงลบข้อมูลต้นทางได้โดย audit ยังอยู่
- **ชื่อ constraint/index** ตั้งชื่อชัดเจนทุกตัว (เช่น `portfolios_user_name_unique`) เพราะการแปลง unique violation เป็น domain error อ้างชื่อ constraint

### 3.3 สิทธิ์ของแต่ละ role

ใส่ GRANT ใน migration เดียวกับตาราง ตาม phase 1 spec หัวข้อ 7

| ตาราง | `app_runtime` | `app_maintenance` |
| --- | --- | --- |
| users | SELECT, INSERT, UPDATE | ไม่มี |
| auth_providers | SELECT, INSERT | ไม่มี |
| instruments | SELECT, INSERT (ไม่มี UPDATE: ดูข้อสังเกตด้านล่าง) | ไม่มี |
| portfolios, transactions | SELECT, INSERT, UPDATE, DELETE | ไม่มี |
| sessions, otp_challenges, oauth_flows | SELECT, INSERT, UPDATE (ไม่มี DELETE) | SELECT `(id, expires_at)` + DELETE |
| idempotency_keys | SELECT, INSERT, DELETE (ไม่มี UPDATE) | SELECT `(portfolio_id, user_id, operation, key, expires_at)` + DELETE |
| audit_events | **INSERT เท่านั้น** (ไม่มี SELECT/UPDATE/DELETE) | SELECT `(id, occurred_at)` + DELETE |
| rate_limit_keys | SELECT, INSERT, UPDATE | SELECT `(key_hash, expires_at)` + DELETE |
| rate_limit_events | SELECT, INSERT (ไม่มี UPDATE/DELETE) | SELECT `(id, key_hash, occurred_at)` + DELETE |
| sequences ของ identity columns | ไม่ต้องให้สิทธิ์: INSERT ลงตารางที่มี identity ได้เลย (ผู้รีวิวยืนยันกับ `transactions.sequence`) | ไม่มี |

- `app_runtime` ไม่มี SELECT บน `audit_events` เพราะไม่มี API อ่าน audit ใน phase 1 (phase 1 spec หัวข้อ 11: ตรวจผ่าน integration tests/authorized SQL) จึงลดความเสียหายหากมี SQL injection; tests อ่าน audit ด้วย admin
- `app_maintenance` ไม่มี INSERT/UPDATE และไม่มี DDL; คำสั่ง cleanup จริง (`cmd/maintenance`) อยู่ใน milestone 9 milestone นี้พิสูจน์เฉพาะสิทธิ์
- สิทธิ์แคบกว่าที่ร่างไว้เดิมโดยตั้งใจ (รีวิว 2026-10-20 ยืนยันว่าพอสำหรับทุก method ของทุก port): ไม่มี DELETE บน sessions/otp/oauth/rate limit เพราะ cleanup เป็นหน้าที่ของ maintenance role
- **instrument ที่ provider เปลี่ยน symbol (ผู้ใช้ตัดสินใจ 2026-10-20):** `app_runtime` ไม่มี UPDATE บน `instruments` และ phase 1 ไม่รองรับการเปลี่ยน symbol ตาม AGENTS.md (ไม่รวม symbol changes) ดังนั้น symbol ใหม่ที่ provider ส่ง asset id เดิมมา (เช่น FB → META) จะ insert ไม่ได้เพราะชน `instruments_provider_asset_unique`; แผน 3c ต้องแปลงกรณีนี้เป็น error ที่อ่านเข้าใจแทน 500 และระบุข้อจำกัดใน README/UI; การรองรับจริงทำพร้อม corporate actions ในเฟสถัดไป

### 3.4 Tests ของแผน 3a

- migrations ใหม่รันบน DB ว่างได้ และ Down แล้ว Up ซ้ำได้ในเครื่อง
- ตารางสิทธิ์ในหัวข้อ 3.3 ทดสอบด้วยการเชื่อมต่อเป็น role นั้นจริง ทั้งสิ่งที่ทำได้และทำไม่ได้ (คาด SQLSTATE `42501`)
- `app_runtime` UPDATE/DELETE/SELECT `audit_events` ไม่ได้; ลบ portfolio/session ต้นทางแล้ว audit ที่อ้าง ID นั้นยังอยู่
- ไม่มี object ของ app roles ใน `public` (ต่อจาก test ของ milestone 1)

## 4. sqlc, mapper, `UnitOfWork` และการแปลง error (แผน 3a)

### 4.1 sqlc

- sqlc `v1.31.1` (phase 1 spec หัวข้อ 4) ติดตั้งผ่าน `make tools`; ต้องมี C compiler (cgo) ซึ่งเครื่อง dev และ runner Ubuntu มีอยู่แล้ว — ยืนยันตอน prototype ของแผน 3a
- `sqlc.yaml` อ่าน schema จาก `migrations/` โดยตรง (sqlc ข้ามส่วน goose Down) จึงไม่มี schema ซ้ำสองที่; queries อยู่ที่ `internal/adapter/postgres/queries/*.sql` แยกไฟล์ตาม feature; generate ลง `internal/adapter/postgres/sqlc/` (ห้ามแก้มือ) ด้วย `sql_package: pgx/v5`ร่วมกับ `internal/adapter/postgres/sqlc_schema_app.sql` ที่มีเพียง `CREATE SCHEMA app;` ให้ sqlc อ่านก่อน migrations เพราะ schema นี้สร้างโดย bootstrap ไม่ใช่ goose
- ทุก query ระบุ `app.` หน้าชื่อตาราง ไม่พึ่ง search_path
- เพิ่ม `make sqlc` และ CI step `make sqlc` + `git diff --exit-code`; golangci-lint ไม่ตรวจโฟลเดอร์ generated

### 4.2 Mapper (`internal/adapter/postgres/mapping.go`)

- `numeric` → `Quantity`/`Price`/`Fee` แปลงตรงจาก `big.Int` + exponent เป็น decimal string แล้วผ่าน `Parse*`; ไม่มี float
- `uuid` ↔ `domain.ID`; `timestamptz` → `time.Time` UTC; NULL ↔ zero `ID`, `""` หรือ pointer `nil` ตามชนิดใน domain
- ค่าที่อ่านจาก DB แล้ว `Parse*` ไม่ผ่าน → error `internal` เสมอ (ไม่ใช่ `invalid_input`) ผ่าน helper ตัวเดียวใน adapter (Minor ที่ส่งต่อจาก milestone 2)

### 4.3 การแปลง error ของ PostgreSQL → domain (`internal/adapter/postgres/errors.go`)

| จาก DB | domain error |
| --- | --- |
| ไม่พบแถว (`pgx.ErrNoRows`) | `ErrNotFound` |
| unique violation `portfolios_user_name_unique` | `ErrPortfolioNameConflict` |
| unique violation ของ `auth_providers` (provider+subject หรือ user+provider) | `ErrIdentityConflict` |
| unique violation `users_email_key` (สมัครพร้อมกัน) | `ErrIdentityConflict`; service ของ milestone 4 ตอบ 202 แบบเดียวกับกรณีอื่นตาม phase 1 spec |
| unique violation ของ `idempotency_keys` PK | `ErrIdempotencyConflict` |
| UPDATE ที่ version ไม่ตรงจนไม่มีแถวเปลี่ยน (แต่แถวยังอยู่) | `ErrVersionConflict` |
| FK violation จาก `transactions` ตอนลบพอร์ต | `ErrPortfolioNotEmpty` |
| อื่นๆ | `internal` ที่ห่อ error เดิมไว้ให้ log ฝั่ง server; ข้อความของ domain error ไม่มี SQL หรือค่าข้อมูล |

### 4.4 `UnitOfWork`

- `WithinTransaction` เปิด transaction READ COMMITTED (row lock ตาม ADR-0009) แล้วสร้าง `TxRepositories` ที่ผูกกับ transaction นั้น
- fn คืน nil → commit; คืน error → rollback แล้วคืน error เดิม; panic → rollback แล้ว panic ต่อ
- repositories สำหรับอ่านนอก transaction สร้างจาก pool; `Lock*` บน instance ที่ไม่ได้อยู่ใน transaction คืน error `internal` ทันที (phase 1 spec หัวข้อ 8)
- ไม่รองรับ nested transaction
- **ลำดับการสร้าง:** แผน 3a สร้าง `Store` กับ `withinTx` ภายใน adapter และ `txRepositories` ที่มีเฉพาะ `Audit()`; แผน 3b/3c เพิ่ม accessor ทีละ repository และ `Store` implement `port.UnitOfWork` เมื่อครบในแผน 3c (Go ต้องการ method ครบทุกตัวของ `port.TxRepositories` จึงยังใช้ port ตรงไม่ได้)

### 4.5 Failure injection ของ audit

ใช้ DB จริง ไม่ mock: ใน `WithinTransaction` เดียว insert พอร์ต แล้ว `Audit().Append` event ที่ละเมิด CHECK ของ DB (สร้าง struct ตรงๆ ข้าม `NewAuditEvent`) → ต้องได้ error และพอร์ตต้องไม่อยู่ใน DB หลัง transaction จบแผน 3a ทดสอบด้วย audit สองรายการในหนึ่ง transaction (รายการที่สองละเมิด CHECK) เพราะยังไม่มี `PortfolioRepository`; แผน 3c ทำซ้ำตามข้อความข้างต้นด้วยพอร์ตจริง

### 4.6 Integration tests (ทุกแผน)

- repositories ต่อ DB เป็น `app_runtime` จริง เพื่อทดสอบ GRANT ไปพร้อมกัน; การเตรียมข้อมูลและตรวจผลที่ runtime อ่านไม่ได้ (เช่น audit) ใช้ admin
- ข้อมูลของแต่ละ test ไม่ชนกันด้วย ID, อีเมล และ symbol แบบสุ่ม จึงไม่ล้างตารางและรันขนานได้
- local ต้องเปิด compose ก่อน (`make test-integration`); CI ใช้ PostgreSQL service และ `REQUIRE_INTEGRATION=1` เหมือน milestone 1

## 5. Repositories ฝั่ง auth (แผน 3b)

| Repository | พฤติกรรม |
| --- | --- |
| `UserRepository` | `ByID`/`ByEmail` ไม่ SELECT `password_hash`; `CredentialsByEmail` เป็น query เดียวที่อ่าน hash (NULL → `""`); `Lock` ใช้ `FOR UPDATE`; `SetPassword` เขียน hash และ `updated_at`; `MarkEmailVerified` ไม่เขียนทับเมื่อยืนยันแล้ว |
| `SessionRepository` | `ByTokenHash` คืนแถวแม้หมดอายุหรือถูก revoke (service ตัดสิน); `Revoke` คืน `false` เมื่อ revoke ไปแล้ว; `RevokeAll` revoke เฉพาะแถวที่ยังใช้งานได้ ณ `at` (ยังไม่ถูก revoke และยังไม่หมดอายุ) และคืนจำนวน |
| `IdentityRepository` | ค้นด้วย (provider, subject); ซ้ำ → `ErrIdentityConflict` |
| `OIDCFlowRepository` | `Consume` เป็น UPDATE เดียว: ตรงทั้ง state hash และ browser binding hash, `consumed_at IS NULL`, `expires_at > at` แล้ว RETURNING; ไม่ผ่าน → `ErrNotFound` |
| `OTPChallengeRepository` | ทุก query ระบุ purpose; `Lock` ใช้ `FOR UPDATE`; `InvalidateOpen` ปิดแถวที่ยังเปิดรวมที่หมดอายุ; `RecordFailure` เพิ่ม `failed_attempts` แบบ atomic และหยุดที่ 20 (เพดานของ CHECK) เพื่อไม่ให้ transaction พัง; `Consume` สำเร็จครั้งเดียว ครั้งต่อไป → `ErrNotFound` |
| `RateLimitRepository` | `Admit` ดูหัวข้อ 5.1 |

รายละเอียดที่ตัดสินในแผน 3b (2026-10-20):

- **conflict ไม่ทำให้ transaction จบ** (ข้อส่งต่อจากรีวิว 3a): `UserRepository.Insert` และ `IdentityRepository.Insert` ใช้ `INSERT ... ON CONFLICT DO NOTHING` แล้วตรวจจำนวนแถว ศูนย์แถว → `ErrIdentityConflict` โดย transaction ยังเขียนต่อได้ (service บันทึก audit ใน callback เดิมได้); การแปลง `users_email_key`/`auth_providers_*` ใน `mapError` คงไว้เป็นทางสำรอง
- **OTP `Consume` ต้องยังไม่หมดอายุ** (`expires_at > at`) เหมือน OIDC `Consume`; comment ของ port อัปเดตให้ตรง
- `Revoke` ของ session ที่ไม่มีอยู่คืน `false`; `SetPassword` ที่ hash ว่าง → `internal`; `Admit` ที่ไม่มี rule หรือ key เดียวใช้กับสอง scope → `internal`
- `Lock*` บน repository จาก pool คืน `internal` (`errLockOutsideTx`) ก่อนส่ง query
- **ลำดับ lock ตอนออก OTP ใหม่:** service ต้อง `Users().Lock` ก่อน `InvalidateOpen` + `Insert` ในทุก flow (ข้อส่งต่อจากรีวิว 3a); ไม่เช่นนั้นคำขอพร้อมกันชน `otp_challenges_one_open_idx`

### 5.1 `RateLimitRepository.Admit`

ทำใน transaction ของตัวเอง (ไม่อยู่ใน `TxRepositories` ตาม phase 1 spec):

1. เรียง key เป็นลำดับเดียวกันทุกคำขอ: key ของ client IP (scope ลงท้าย `_ip`) ก่อน แล้วจึงเรียงตาม `key_hash` เพื่อกัน deadlock เมื่อคำขอพร้อมกันใช้หลาย key; แต่ละ key ถูก upsert (ซึ่ง lock แถว) แล้วตรวจทันทีก่อนไปยัง key ถัดไป; `expires_at` = now + window ที่ยาวที่สุดของ key นั้น
2. นับ `rate_limit_events` ที่ `occurred_at > now − window` ของแต่ละ rule; หลาย rule ใช้ key เดียวกันได้ (cooldown 60 วินาที + 5 ครั้ง/ชั่วโมงของ `*_request_email`)
3. ผ่านทุก key → insert event หนึ่งแถวต่อ key (ไม่ซ้ำแม้หลาย rule ใช้ key เดียวกัน) แล้วคืน `Allowed = true`
4. key ใดไม่ผ่าน → หยุดทันที ไม่ insert event และ**ไม่สร้าง key ที่เหลือ** (ตอบข้อกำหนดของ phase 1 spec หัวข้อ 10: ตรวจ IP quota ก่อนสร้าง key ของอีเมลใดๆ เพื่อลด storage abuse ซึ่งตัดสินแบบนี้เมื่อ 2026-10-20 หลังรีวิว 3b); `RetryAfter` = ค่าที่มากที่สุดในบรรดา rule ของ key นั้นที่ปฏิเสธ (ของแต่ละ rule คือเวลาที่ event ลำดับที่ count − limit + 1 นับจากเก่าสุดออกจาก window) จึงครอบคลุมเฉพาะ key ที่ตรวจถึงจุดนั้น — key ที่ยังไม่ได้ตรวจอาจเต็มด้วย แล้วครั้งถัดไป client จะได้ `RetryAfter` ใหม่; `RecordDenialAudit = true` เฉพาะเมื่อ UPDATE `last_denial_audit_at` (ค่าเดิมเป็น NULL หรือเก่ากว่า 1 นาที) สำเร็จ ตาม phase 1 spec หัวข้อ 9
5. ใช้ `now` ที่ service ส่งมาจาก `Clock` ไม่ใช้ `now()` ของ DB

คำขอที่ถูกปฏิเสธไม่นับเป็น event (admission limit): ผู้โจมตีได้เพียงอัตราตาม limit และผู้ใช้จริงที่พิมพ์ผิดไม่ถูกล็อกยาวขึ้นเรื่อยๆ; การลบ event/key ที่หมดอายุเป็นงานของ `cmd/maintenance` ใน milestone 9

### 5.2 Tests ภายใต้คำขอพร้อมกัน

- `OIDCFlowRepository.Consume` flow เดียวกันจาก 10 goroutine → สำเร็จครั้งเดียว
- `OTPChallengeRepository.Consume` challenge เดียวกันพร้อมกัน → สำเร็จครั้งเดียว
- `RateLimitRepository.Admit` limit 5 จาก 20 goroutine → ผ่านพอดี 5
- `SessionRepository.Revoke` สองครั้ง → `true` แล้ว `false`
- ออก OTP ใหม่ของผู้ใช้เดียวกันจาก 10 goroutine โดย `Users().Lock` ก่อน `InvalidateOpen` + `Insert` → สำเร็จทั้ง 10 และเหลือ challenge ที่เปิดอยู่หนึ่งรายการ

## 6. Repositories ฝั่งพอร์ต (แผน 3c)

| Repository | พฤติกรรม |
| --- | --- |
| `PortfolioRepository` | ทุก query กรอง `user_id` (ของผู้อื่น → `ErrNotFound`); `LockOwned` ใช้ `FOR UPDATE`; `UpdateOwned` เขียน name/description เมื่อ version ตรงแล้ว version + 1 (ไม่ตรง → `ErrVersionConflict`, ไม่มีหรือไม่ใช่ของตน → `ErrNotFound`); ชื่อซ้ำ → `ErrPortfolioNameConflict`; `DeleteOwnedEmpty` มีรายการค้าง → `ErrPortfolioNotEmpty` |
| `TransactionRepository` | ทุก method ตรวจเจ้าของผ่าน join `portfolios.user_id`; `Insert` คืนแถวพร้อม `sequence` จาก DB; `Update` กรองด้วย `portfolio_id` และไม่มี `portfolio_id` ใน SET; `History` เรียง `executed_at ASC, sequence ASC` และใช้ symbol ปัจจุบันจาก instruments; `Delete` ไม่รับ version ตาม signature (service ตรวจ version ใต้ lock ก่อน) |
| `InstrumentRepository` | `BySymbol`; `Upsert` ใช้ `ON CONFLICT DO NOTHING` แล้วอ่านแถวที่มีอยู่ คำขอพร้อมกันได้แถวเดียวกัน |
| `IdempotencyRepository` | `Get` คืน record แม้หมดอายุ (service ตัดสิน); `DeleteExpired` ลบเฉพาะเมื่อหมดอายุ ณ `now`; `Save` ชน key เดิม → `ErrIdempotencyConflict`; `DeleteForPortfolio` ใช้ก่อนลบพอร์ต |

รายละเอียดที่ตัดสินในแผน 3c (2026-10-21):

- **conflict กับ transaction:** `PortfolioRepository.Insert` ใช้ `ON CONFLICT (user_id, lower(name)) DO NOTHING` + จำนวนแถว ชื่อซ้ำจึงไม่ทำให้ transaction จบ; การเปลี่ยนชื่อซ้ำใน `UpdateOwned` และ `IdempotencyRepository.Save` ที่ชน key ยังอาศัย unique violation ซึ่งทำให้ transaction จบตาม `port.UnitOfWork` (service คืน error ทันทีโดยไม่ต้องเขียน audit ความล้มเหลว)
- `UpdateOwned`/`Update` ที่ไม่มีแถวตรง จะตรวจต่อว่าแถวของเจ้าของยังอยู่หรือไม่: อยู่ → `ErrVersionConflict`, ไม่อยู่หรือไม่ใช่ของตน → `ErrNotFound`
- `DeleteOwnedEmpty` ลบด้วยเงื่อนไข `NOT EXISTS` ของ transactions (transaction ยังใช้ต่อได้เมื่อพอร์ตไม่ว่าง); FK violation ของ `transactions_portfolio_id_fkey` ที่เกิดเมื่อมีรายการถูก insert พร้อมกันโดยไม่ lock แปลงเป็น `ErrPortfolioNotEmpty` **เฉพาะใน method นี้** และ `mapError` ไม่แปลง FK นี้อีก (ข้อส่งต่อจากรีวิว 3a)
- `History`/`List` ของพอร์ตที่ไม่มีหรือไม่ใช่ของตน → `ErrNotFound`; พอร์ตของตนที่ว่าง → รายการว่าง
- **instrument ที่ provider เปลี่ยน symbol** (ผู้ใช้เลือก 2026-10-21): `Upsert` ใช้ `ON CONFLICT DO NOTHING` ครอบทั้ง unique ของ symbol และ `(source, provider_asset_id)` แล้วอ่านด้วย symbol; ถ้า asset นั้นอยู่ใต้ symbol อื่น คืน `unknown_symbol` (422) พร้อมข้อความว่า symbol ถูกเปลี่ยนชื่อและยังไม่รองรับ; transaction ยังใช้ต่อได้
- ตัวกรองรายการ: `from`/`to` รวมขอบทั้งสองด้าน, `symbol` เทียบกับ symbol ปัจจุบันของ instrument; `NewTransactionFilter` ปฏิเสธ pointer ไปยัง `Symbol{}` หรือ side ที่ไม่ใช่ buy/sell เป็น `internal` (แก้ Minor ในหัวข้อ 7)
- `IdempotencyRepository.Save` บันทึกได้เฉพาะใต้พอร์ตของผู้ใช้ใน scope (INSERT ... SELECT จาก `portfolios`) พอร์ตที่ไม่มีหรือไม่ใช่ของตน → `ErrNotFound`; ไม่เช่นนั้น record ของผู้อื่นจะล็อกพอร์ตด้วย FK RESTRICT จน `DeleteForPortfolio` ของเจ้าของลบไม่ได้ (รีวิว 3c)
- `Idempotency()` มีเฉพาะใน `TxRepositories` เพราะอ่าน/เขียนใต้ portfolio lock เสมอ; `result` ที่อ่านกลับจาก jsonb เป็นค่า JSON เดิมแต่ช่องว่างและลำดับ key อาจต่างจากที่ส่งไป
- `InstrumentRepository.Upsert` คืนแถวของ symbol ที่มีอยู่ตามที่เก็บไว้ แม้ source หรือ provider asset id ต่าง (ticker reuse และ demo/alpaca ปนกันอยู่นอกขอบเขต phase 1); `DeleteOwnedEmpty` ที่ชน FK เพราะมีรายการถูก insert พร้อมกันโดยไม่ lock คืน `ErrPortfolioNotEmpty` แต่ transaction จบแล้ว (กรณีที่ผู้เรียก lock ถูกต้องไม่เจอ)
- `Store` implement `port.UnitOfWork` แล้ว; `withinTx` rollback และคืน connection เมื่อ callback ออกด้วย `runtime.Goexit` ด้วย (พบระหว่าง prototype ของ 3c: `t.Fatal` ใน callback ทำให้ connection ค้างและ `pool.Close` รอตลอดไป)

### 6.1 Keyset pagination

- พอร์ตเรียง `created_at DESC, id DESC`; รายการเรียง `executed_at DESC, sequence DESC` คีย์ท้ายทำให้ลำดับคงที่แม้เวลาเท่ากัน
- cursor = base64url ของ JSON `{k, f, t, i|s}`: ชนิดของรายการ (`portfolios`/`transactions`), ลายนิ้วมือของขอบเขตของรายการ (8 ไบต์แรกของ SHA-256 เป็น hex: รายการพอร์ตผูกกับเจ้าของ, รายการซื้อขายผูกกับ portfolio id และ symbol/side/from/to) จึงใช้ cursor ข้ามพอร์ตหรือข้ามผู้ใช้ไม่ได้, เวลาของแถวสุดท้าย และ id หรือ sequence ของแถวนั้น (≤ 1 KiB ตาม `domain.Page`); เข้ารหัส/ถอดรหัสใน postgres adapter
- cursor เสียหรือไม่ตรงกับ filter ปัจจุบัน → `invalid_input` (`cursor ...`) เพราะ cursor มาจาก client
- `NextCursor` เป็น `""` เมื่อหมดหน้า

### 6.2 Tests สำคัญ

- ผู้ใช้ B เรียกทุก method กับพอร์ตของผู้ใช้ A → `ErrNotFound` ทั้งหมด; list ของ B ไม่มีของ A
- symbol เดียวกันในสองพอร์ตไม่ปนกัน
- เขียนแล้วอ่านกลับ `0.0000000001` และค่า 18 หลัก + 10 ตำแหน่งได้ค่าเดิมทุกหลัก
- `"Long term"` กับ `"long TERM"` ของผู้ใช้เดียวกันชนกัน ต่างผู้ใช้ได้ ลบแล้วใช้ชื่อเดิมได้
- ขายพร้อมกัน (AGENTS.md "concurrent sells"): 10 goroutine ใน `WithinTransaction` ทำ `LockOwned` → `History` → ขายเมื่อหุ้นพอ; จำนวนที่ขายรวมไม่เกินที่ถือ (นับหุ้นแบบง่ายใน test เพราะ replay อยู่ milestone 7)
- `IdempotencyRepository.Save` key เดียวกันพร้อมกัน → สำเร็จครั้งเดียว
- `UpdateOwned`/`Update` ด้วย version เก่า → `ErrVersionConflict` และข้อมูลไม่เปลี่ยน
- pagination: หน้าต่อหน้าไม่ซ้ำไม่ขาดเมื่อเวลาเท่ากัน; cursor ข้าม filter → `invalid_input`

## 7. Minor ที่ส่งต่อจาก milestone 2 และที่ที่จัดการ

| Minor | จัดการที่ |
| --- | --- |
| helper แปลง error จากข้อมูลใน DB เป็น `internal` | 3a หัวข้อ 4.2 |
| contract ของ ports: unique violation, not-found, `Update` ห้ามแก้ `portfolio_id`, tie-break ของการเรียง, `Save` เมื่อชน | 3a หัวข้อ 4.3 (comment ใน ports อัปเดตพร้อมกัน), 3b, 3c |
| CHECK ขนาด metadata นับแบบเดียวกับ Go, email ไม่พึ่ง `lower()` | 3a หัวข้อ 3.2 |
| comment ของ sentinel errors ขัดกับ ports; `errors.Is(err, (*domain.Error)(nil))` panic | 3a (แก้ comment ใน `errors.go` และให้ `Is` คืน false กับ nil) |
| `NewTransactionFilter` รับ `Symbol{}`/`Side("")` ว่าง | 3c (แก้แล้ว: คืน `internal`) |
| `ParseExecutedAt` รับทศนิยม 7 หลักที่หลักท้ายเป็น 0 และ `,` | ยังไม่แก้ (ไม่กระทบ persistence) — คงไว้ใน roadmap |
| อีเมลที่มี `,;<>` | milestone 4 (Mailer) |
| เอกสาร `ParseID(field, s)`/`domain.ID` | 3a (แก้เอกสารพร้อม spec หัวข้อ 8) |

## 8. นอกขอบเขต

- HTTP endpoints และ services (milestone 4 เป็นต้นไป)
- คำสั่ง `cmd/maintenance` ที่ลบข้อมูลจริงและการ schedule (milestone 9)
- replay/การคำนวณพอร์ต (milestone 7–8)
- การตรวจกับ Supabase จริง (milestone 11)
