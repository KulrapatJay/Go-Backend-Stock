# Milestone 2 — Domain, ports และกติกาข้อมูลพื้นฐาน (design)

- **สถานะ:** ผ่านการตรวจรับ 2026-10-17; implement ตาม [แผน](../plans/2026-10-16-milestone-2-domain-ports.md) และแก้ตามรีวิวทั้ง branch แล้ว
- **อ้างอิง:** [AGENTS.md](../../../AGENTS.md), [phase 1 spec](2026-10-05-phase-1-design.md) หัวข้อ 3, 7, 8, 11, 12 และ [roadmap](../plans/2026-10-05-phase-1-roadmap.md) milestone 2
- **ADR:** [ADR-0013](../../adr/0013-domain-value-types.md)

## 1. เป้าหมายและเกณฑ์ผ่าน

สร้าง `internal/core/domain` และ `internal/core/port` ครั้งแรก ให้ milestone 3 (schema, sqlc, repositories, audit persistence) มีชนิดข้อมูลและ interfaces ที่จะ implement ได้ทันที

**ผ่านเมื่อ** (จาก roadmap): core compile และ test ได้เอง โดยไม่มี import ของ Fiber, pgx, sqlc-generated code หรือ provider SDK; unit tests ครอบคลุม precision/scale, ค่าติดลบ/ศูนย์ และค่าที่เกินช่วง โดยไม่ใช้ DB หรือ Fiber

## 2. การตัดสินใจที่ยืนยันแล้ว (2026-10-16)

| เรื่อง | ทางเลือกที่ยืนยัน | เหตุผล |
| --- | --- | --- |
| ขอบเขต ports | เฉพาะ ports ที่ milestone 3 implement | ตาม phase 1 spec หัวข้อ 3 "ไม่สร้าง empty interfaces ล่วงหน้า"; roadmap ที่เขียนว่าสร้าง ports ทั้งหมดขัดกับข้อนี้ ผู้ใช้เลือกแบบนี้ |
| ชนิดตัวเลข | ชนิดแยกตามความหมาย `Quantity`, `Price`, `Fee` | compiler กันการสลับค่าที่ส่งเข้า |
| ตำแหน่ง validation | parse, don't validate: ค่าดิบแปลงเป็นชนิดที่ตรวจแล้วผ่าน `Parse*`/`New*` เท่านั้น | ค่าที่ไม่ผ่านการตรวจเข้าสู่ service/repository ไม่ได้ และเห็นชัดใน code review |
| signature ของ ports | ใช้ชนิดที่ตรวจแล้วแทน `string` ดิบ (หัวข้อ 5) | repository ไม่ต้องตรวจซ้ำ; ต้องแก้ phase 1 spec หัวข้อ 8 ในชุดงานเดียวกัน |

## 3. ชนิดพื้นฐาน (value types)

ทุกชนิดเก็บค่าใน field ที่ไม่ export, สร้างได้ผ่าน `Parse*`/`New*` เท่านั้น และมี `IsZero()` ให้ repository/service ปฏิเสธ zero value ที่หลุดมา (Go ยังสร้าง `Quantity{}` ได้เสมอ)

| ไฟล์ | ชนิด/ฟังก์ชัน | กติกา |
| --- | --- | --- |
| `id.go` | `ID`, `NewID()`, `ParseID(field, s)` | `NewID` สร้าง UUIDv7; `ParseID` รับเฉพาะ UUID ที่ถูกรูปแบบ; ใช้ `github.com/google/uuid` ที่มีใน go.mod อยู่แล้ว |
| `decimal.go` | `Quantity`, `Price`, `Fee`, `ParseQuantity/ParsePrice/ParseFee(s)` | รับเฉพาะ `^[0-9]+(\.[0-9]+)?$` (ไม่มีเครื่องหมาย, exponent, ช่องว่าง, NaN/Infinity); ≤ 18 หลักก่อนจุดและ ≤ 10 หลักหลังจุด ไม่ปัด; quantity และ price > 0, fee ≥ 0; มี `.Decimal()` และ `.String()` ที่คืนรูปมาตรฐาน (`"0.50"` → `"0.5"`, `"007"` → `"7"`) ใช้เป็น canonical string ของ idempotency hash ภายหลัง |
| `symbol.go` | `Symbol`, `ParseSymbol(s)` | trim + uppercase แล้วตรวจ `^[A-Z][A-Z0-9.-]{0,14}$` |
| `time.go` | `ParseExecutedAt(s, now)` | RFC3339 ที่มี timezone เท่านั้น, แปลงเป็น UTC, ทศนิยมวินาทีเกิน 6 หลักปฏิเสธ (ไม่ปัด), ต้อง ≥ `1970-01-01T00:00:00Z` และไม่อยู่หลัง `now` (เท่ากับ `now` ผ่าน) |
| `version.go` | `Version`, `ParseVersion(n)` | int32 > 0 |
| `email.go` | `Email`, `ParseEmail(s)` | trim + lowercase, 1–254 ตัวอักษร, มี `@` ตัวเดียวและทั้งสองฝั่งไม่ว่าง, ไม่มีช่องว่าง/อักขระควบคุม; ไม่ตัดจุดหรือ `+suffix`; ไม่ใช้ regex RFC 5322 เพราะยืนยันจริงด้วย OTP ทางอีเมล |
| `portfolio.go` | `PortfolioName`, `PortfolioDescription` | ชื่อ trim แล้ว 1–100 ตัวอักษร; คำอธิบายไม่บังคับ (ค่าเริ่มต้น `""`) ≤ 1000 ตัวอักษร; นับเป็นตัวอักษร (rune) ไม่ใช่ byte ให้ตรงกับ `varchar(n)`; ห้ามอักขระควบคุม ยกเว้นคำอธิบายขึ้นบรรทัดใหม่ได้ด้วย `\n` หรือ `\r\n` (ผู้ใช้ตัดสินใจ 2026-10-17); ทั้งสอง field ห้ามอักขระล่องหนหรือเปลี่ยนทิศ (Unicode Cf ยกเว้น ZWJ/ZWNJ, Zl, Zp, Hangul filler) และชื่อต้องมีอักขระที่มองเห็นอย่างน้อยหนึ่งตัว เพื่อไม่ให้ชื่อสองชื่อดูเหมือนกันแต่ผ่านกฎห้ามชื่อซ้ำ (แก้หลังรีวิว 2026-10-17) |
| `page.go` | `Page`, `ParsePage(limit, cursor)` | limit ค่าเริ่มต้น 20 ช่วง 1–100; cursor ≤ 1 KiB; การถอด/ผูก cursor กับ filters เป็นงานของ adapter |

## 4. Entities, enums และ errors

### Entities

struct ธรรมดาที่ field export ได้แต่ใช้ชนิดจากหัวข้อ 3 เช่น `Transaction.Quantity` เป็น `Quantity`; สร้างเฉพาะชนิดที่ repositories ของ milestone 3 ใช้

| ไฟล์ | ชนิด |
| --- | --- |
| `user.go` | `User` (ไม่มี field password hash ตาม phase 1 spec หัวข้อ 7), `Session`, `AuthProvider`, `OIDCFlow`, `RevokeReason` (`logout`, `password_reset`), `IdentityProviderName` (`google`), `PasswordHash` (พิมพ์/marshal เป็น `[REDACTED]` เสมอ), `UserCredentials{User, PasswordHash}` ซึ่งเป็นค่าเดียวที่มี hash (เพิ่มหลังรีวิว 2026-10-17) |
| `otp.go` | `OTPChallenge`, `OTPPurpose` |
| `portfolio.go` | `Portfolio`, `PortfolioPage` |
| `transaction.go` | `Transaction`, `Side`, `TransactionFilter` (From ≤ To), `TransactionPage` |
| `instrument.go` | `Instrument`, `AssetClass`, `InstrumentStatus`, `InstrumentSource` |
| `idempotency.go` | `IdempotencyScope`, `IdempotencyOperation`, `IdempotencyRecord` |
| `audit.go` | `AuditEvent`, `AuditOutcome`, `EventType` (รายการ event ตาม AGENTS.md หัวข้อ "เหตุการณ์ที่ต้องมีในเฟส 1") |
| `ratelimit.go` | `RateLimitScope` (11 ค่าตาม phase 1 spec หัวข้อ 7), `RateLimitRule`, `RateLimitDecision` |

### Enums

ชนิด string ที่มี `Parse*` รับเฉพาะค่าที่ตรงกับ CHECK ใน DB ทุกตัวอักษร เช่น `ParseSide("buy")` ผ่าน แต่ `"BUY"` ไม่ผ่าน

### กติกาข้ามหลาย field

ใช้ constructor เฉพาะชนิดที่มีกติกาข้าม field จริง:

- `Session`, `OTPChallenge`, `OIDCFlow`, `IdempotencyRecord`: `ExpiresAt` ต้องหลัง `CreatedAt`; token/OTP/state/nonce hash ยาว 32 bytes
- `NewAuditEvent`: metadata เป็น JSON object ≤ 8 KiB; event_type ≤ 80, target_type ≤ 40, reason_code ≤ 80 ตัวอักษร
- `IdempotencyRecord`: key 1–128 ตัวอักษร, result ≤ 16 KiB

### Errors (`errors.go`)

- ชนิดเดียว `domain.Error{Code ErrorCode; Message string}` และค่าคงที่ `ErrorCode` ตรงกับตาราง domain error ใน phase 1 spec หัวข้อ 11 ทุกตัว
- sentinel เช่น `domain.ErrNotFound` เทียบด้วย `errors.Is` ได้แม้ข้อความต่างกัน (เทียบที่ `Code`)
- validation error ใช้ code `invalid_input` และข้อความระบุชื่อ field เช่น `quantity must be greater than zero` **ห้ามมีค่าที่ผู้ใช้ส่งมา** ในข้อความ
- การแปลง code เป็น HTTP status เป็นงานของ HTTP adapter ใน milestone ที่เปิด endpoint
- ข้อมูลที่ repository อ่านจาก DB map ผ่าน `Parse*` เช่นกัน ค่าใน DB ที่ผิดกติกาถือเป็น error ภายใน (`internal`) ไม่ใช่ `invalid_input`

## 5. Ports (`internal/core/port`)

| ไฟล์ | ports |
| --- | --- |
| `clock.go` | `Clock` |
| `uow.go` | `UnitOfWork`, `TxRepositories` |
| `auth.go` | `UserRepository`, `SessionRepository`, `IdentityRepository`, `OIDCFlowRepository`, `OTPChallengeRepository` |
| `portfolio.go` | `PortfolioRepository`, `TransactionRepository`, `InstrumentRepository`, `IdempotencyRepository` |
| `audit.go` | `AuditRepository`, `RateLimitRepository` |

Signature ยึดตาม phase 1 spec หัวข้อ 8 ยกเว้นพารามิเตอร์ต่อไปนี้ที่เปลี่ยนเป็นชนิดที่ตรวจแล้ว:

| ตำแหน่ง | เดิม | ใหม่ |
| --- | --- | --- |
| `UserRepository.ByEmail` | `string` | `domain.Email` |
| `InstrumentRepository.BySymbol` | `string` | `domain.Symbol` |
| `SessionRepository.Revoke`, `RevokeAll` (reason) | `string` | `domain.RevokeReason` |
| `IdentityRepository.BySubject` (provider) | `string` | `domain.IdentityProviderName` |
| `UserRepository.SetPassword` (hash) | `string` | `domain.PasswordHash` (เพิ่มหลังรีวิว 2026-10-17) |

เพิ่มหลังรีวิว (ผู้ใช้เลือก 2026-10-17): `UserRepository.CredentialsByEmail(ctx, domain.Email) (domain.UserCredentials, error)` เป็น query เดียวที่อ่าน `password_hash` สำหรับ login, register และ reset; `ByEmail` และ query อื่นยังไม่อ่าน hash; register ใช้ `Insert` + `SetPassword` ใน transaction เดียว

`Clock` มีเฉพาะ interface; ตัวจริง (`time.Now().UTC()`) และตัวปลอมสำหรับ tests มาพร้อม service ตัวแรกที่ใช้ tests ใน milestone นี้ส่ง `now` เป็นพารามิเตอร์ตรง

**เลื่อนไปสร้างใน milestone ที่ใช้จริง:** `AuthService`, `EmailVerificationService`, `PasswordResetService`, `PortfolioService`, `TransactionService`, `ValuationService`, `PriceProvider`, `InstrumentProvider`, `IdentityProvider`, `Mailer` รวมถึงชนิดที่มีเฉพาะ ports เหล่านี้ใช้ เช่น `QuoteBatch`, `PriceQuote`, `OIDCResult`, `SessionGrant`, `Holding`, `PortfolioValuation` และ replay/การคำนวณพอร์ต (milestone 7–8) signature ของ ports เหล่านี้ยังยึดตาม phase 1 spec หัวข้อ 8

## 6. การทดสอบ

Unit tests แบบ table-driven ในแพ็กเกจ `domain` ไม่ใช้ DB, Fiber หรือเครือข่าย:

- **ตัวเลข:** 18 หลักก่อนจุดผ่าน 19 ไม่ผ่าน; 10 หลักหลังจุดผ่าน 11 ไม่ผ่าน; ศูนย์ (quantity/price ไม่ผ่าน, fee ผ่าน); ติดลบ; `+1`, `.5`, `5.`, `1e3`, `NaN`, `Infinity`, ช่องว่างหน้า/หลัง, string ว่าง; `"007"` → `"7"`, `"0.50"` → `"0.5"`
- **symbol:** ตัวพิมพ์เล็กถูกแปลง, `BRK.B` ผ่าน, ขึ้นต้นด้วยตัวเลขไม่ผ่าน, 16 ตัวอักษรไม่ผ่าน
- **เวลา:** `+07:00` แปลงเป็น UTC, ไม่มี timezone ไม่ผ่าน, ทศนิยม 7 หลักไม่ผ่าน, ก่อน 1970 ไม่ผ่าน, หลัง `now` ไม่ผ่าน, เท่ากับ `now` ผ่าน
- **email และชื่อพอร์ต:** trim/lowercase; ชื่อภาษาไทย 100 ตัวอักษรผ่าน 101 ไม่ผ่าน (พิสูจน์ว่านับ rune); อักขระควบคุมไม่ผ่าน
- **enums, constructors ข้าม field และ errors:** `errors.Is` เทียบตาม code; ข้อความ error ทุกกรณีไม่มีค่าที่ส่งเข้า
- **fuzz test** ของ decimal parser: input ใดก็ไม่ panic; ค่าที่ผ่านต้อง `String()` แล้ว parse ซ้ำได้ค่าเท่าเดิม

**Dependency boundary test:** อ่าน import ของทุกไฟล์ (รวม `_test.go`) ใน `internal/core/...` ด้วย `go/parser` แล้วยอมเฉพาะ allowlist = standard library + `github.com/shopspring/decimal` + `github.com/google/uuid` + แพ็กเกจภายใต้ `go-backend-stock/internal/core/` ใช้ allowlist แทน denylist เพื่อจับ dependency ใหม่ที่ไม่ได้ตั้งใจด้วย

## 7. Dependencies และเอกสาร

- เพิ่ม `github.com/shopspring/decimal` โดยตรวจเวอร์ชันล่าสุดที่มีจริงและ pin ตอนเขียนแผน
- แก้ phase 1 spec หัวข้อ 8 ตาม signature ในหัวข้อ 5 และระบุว่า service/provider ports สร้างใน milestone ที่ใช้จริง
- ADR-0013: บันทึก parse, don't validate และชนิดตัวเลขแยกตามความหมาย พร้อม trade-off (zero value ยังสร้างได้, ต้อง map ผ่าน `Parse*` ใน adapter)
- roadmap milestone 2: tick และสถานะเมื่อเสร็จ
- ไม่มีการเปลี่ยน HTTP, DB schema หรือ Swagger ใน milestone นี้

## 8. นอกขอบเขต

- ไม่มี service implementation, repository implementation, migration หรือ HTTP handler
- ไม่มี password validator, OTP/HMAC หรือ crypto helpers (milestone 4)
- ไม่มี replay/การคำนวณพอร์ต (milestone 7–8)
