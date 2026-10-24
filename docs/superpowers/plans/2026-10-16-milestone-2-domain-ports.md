# Milestone 2 — Domain + ports Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** สร้าง `internal/core/domain` (value types, entities, enums, errors) และ `internal/core/port` (repository ports, `UnitOfWork`, `Clock`) ที่ milestone 3 จะ implement โดย core compile/test ได้เองและ import ได้เฉพาะ stdlib, `shopspring/decimal` และ `google/uuid`

**Architecture:** parse, don't validate — ค่าดิบจากภายนอกแปลงเป็นชนิดที่ตรวจแล้วผ่าน `Parse*` (คืน `invalid_input`) ส่วนค่าที่ server สร้างเองตรวจด้วย `New*` (คืน `internal` เพราะเป็นบั๊ก) ชนิดตัวเลขแยกตามความหมาย (`Quantity`, `Price`, `Fee`) ห่อ `decimal.Decimal`; ports ยึด signature ของ phase 1 spec หัวข้อ 8 ยกเว้น 4 พารามิเตอร์ที่เปลี่ยนเป็นชนิดที่ตรวจแล้ว; test แยกบังคับ allowlist ของ import ใน core

**Tech Stack:** Go 1.27 (go.mod `go 1.27.1`), `github.com/shopspring/decimal v1.4.0`, `github.com/google/uuid v1.6.0` (มีอยู่แล้ว), standard library `testing` + native fuzzing, golangci-lint v2.14.0 ตาม `.golangci.yml` เดิม

**Spec:** [docs/superpowers/specs/2026-10-16-milestone-2-domain-ports-design.md](../specs/2026-10-16-milestone-2-domain-ports-design.md) ซึ่งอ้าง [phase 1 spec](../specs/2026-10-05-phase-1-design.md) หัวข้อ 7, 8, 11 และ [AGENTS.md](../../../AGENTS.md); เป็น milestone 2 ใน [roadmap](2026-10-05-phase-1-roadmap.md)

## Global Constraints

- core (`internal/core/...`) import ได้เฉพาะ standard library, `github.com/shopspring/decimal`, `github.com/google/uuid` และแพ็กเกจใต้ `go-backend-stock/internal/core/` — บังคับด้วย test ใน Task 7
- `shopspring/decimal` pin ที่ `v1.4.0` (ไม่ใช่ v1.5.0 ที่ออก 2026-10-05 เพราะยังใหม่เกินไปสำหรับ library คำนวณเงิน; `String()` ของทั้งสองเวอร์ชันให้ผลเหมือนกัน)
- ห้ามใช้ float ในเส้นทางตัวเลขใดๆ; decimal รับเฉพาะ `^[0-9]+(\.[0-9]+)?$`, ≤ 18 หลักก่อนจุด, ≤ 10 หลักหลังจุด, ปฏิเสธแทนการปัด
- `Parse*` = ค่าจากผู้ใช้ → `invalid_input` ข้อความขึ้นต้นด้วยชื่อ field; `New*` = ค่าที่ server สร้าง → `internal`
- ข้อความ error ทุกตัว **ห้ามมีค่าที่ผู้ใช้ส่งมา**
- enum รับเฉพาะค่าที่ตรงกับ CHECK ใน DB ทุกตัวอักษร (case-sensitive)
- tests อยู่ในแพ็กเกจ `domain_test` (black-box) ไม่ใช้ DB, Fiber หรือเครือข่าย
- ไม่มี `//nolint` เว้นแต่มีเหตุผลกำกับในบรรทัดเดียวกัน (แผนนี้มีจุดเดียว: gosec G101 false positive ใน `errors.go`)
- commit message ไม่มี `Co-Authored-By` หรือชื่อ Claude (ข้อกำหนดของผู้ใช้); commit ในเครื่องเท่านั้น ห้าม push
- รันทุกคำสั่งที่ root ของ repo (`/Users/markedtr/Docs/MyCode-work/Go-Backend-Stock`) บน branch `feature/creat_login_and_register`

## Review Focus

1. **symbol ที่มีตัวอักษรนอก ASCII ซึ่งเมื่อทำตัวพิมพ์ใหญ่แล้วกลายเป็น ASCII** (`"ıbm"` → `"IBM"`, `"ſpy"` → `"SPY"`) ต้องถูกปฏิเสธ ไม่ใช่บันทึกเป็นหุ้นอีกตัว — Task 3 `TestParseSymbol`
2. **ชื่อพอร์ตที่มี newline/tab/NEL ที่หัวท้าย** ต้องถูก trim แล้วผ่าน แต่อักขระควบคุมที่อยู่ตรงกลางต้องถูกปฏิเสธ — Task 3 `TestParsePortfolioName`
3. **เวลาที่มี offset ซึ่งข้ามขอบ 1970 หรือ now หลังแปลงเป็น UTC** (`1970-01-01T06:59:59+07:00`, `2026-10-16T19:00:01+07:00`) ต้องถูกปฏิเสธ — Task 3 `TestParseExecutedAt`
4. **ข้อความ error สะท้อนค่าที่ผู้ใช้ส่ง** ซึ่งจะไปถึง client และ log — Task 2/3 `TestErrorsNeverEchoInput` (ใช้ marker เพราะ input สั้นอย่าง `"1"` อยู่ในข้อความคงที่ได้โดยบังเอิญ)
5. **core เผลอ import framework หรือ adapter** — Task 7 `TestCoreImportsOnlyAllowedPackages` พร้อม step ที่พิสูจน์ด้วยไฟล์ละเมิดชั่วคราว

**ส่งต่อให้ milestone 3 (ทดสอบไม่ได้ใน milestone นี้เพราะต้องมี DB):** `ParseEmail` ใช้ `strings.ToLower` ของ Go ซึ่งอาจต่างจาก `lower()` ของ PostgreSQL กับอักษรนอก ASCII — repository ต้องเก็บและค้นด้วยค่าที่ Go normalize แล้ว ห้ามพึ่ง `lower()` ใน SQL สำหรับ email

---

## File Structure

| ไฟล์ | หน้าที่ | Task |
| --- | --- | --- |
| `internal/core/domain/errors.go` | `Error`, `ErrorCode` 25 ตัว, sentinels, `invalidInput` | 1 |
| `internal/core/domain/id.go` | `ID` (UUIDv7, canonical) | 1 |
| `internal/core/domain/version.go` | `Version` (int32 > 0) | 1 |
| `internal/core/domain/page.go` | `Page`, `isDigits` | 1 |
| `internal/core/domain/decimal.go` | `Quantity`, `Price`, `Fee` | 2 |
| `internal/core/domain/symbol.go` | `Symbol` | 3 |
| `internal/core/domain/email.go` | `Email` | 3 |
| `internal/core/domain/time.go` | `ParseExecutedAt` | 3 |
| `internal/core/domain/portfolio.go` | `PortfolioName`, `PortfolioDescription` (Task 3); `Currency`, `Portfolio`, `PortfolioPage` (Task 5) | 3, 5 |
| `internal/core/domain/text.go` | `hasControl`, `isASCII` (Task 3); `isText` (Task 5) | 3, 5 |
| `internal/core/domain/rules.go` | `HashSize`, `invariant`, `parseEnum`, `isOneOf`, `require*`, `firstError` | 4 |
| `internal/core/domain/user.go` | `User`, `Session`, `AuthProvider`, `OIDCFlow`, `RevokeReason`, `IdentityProviderName`, `OIDCPurpose` | 4 |
| `internal/core/domain/otp.go` | `OTPChallenge`, `OTPPurpose`, `MaxOTPFailedAttemptsCeiling` | 4 |
| `internal/core/domain/instrument.go` | `Instrument`, `AssetClass`, `InstrumentStatus`, `InstrumentSource` | 5 |
| `internal/core/domain/transaction.go` | `Transaction`, `Side`, `TransactionFilter`, `TransactionPage` | 5 |
| `internal/core/domain/idempotency.go` | `IdempotencyScope`, `IdempotencyOperation`, `IdempotencyRecord`, `ParseIdempotencyKey` | 6 |
| `internal/core/domain/audit.go` | `AuditEvent`, `AuditOutcome`, `EventType` 21 ตัว | 6 |
| `internal/core/domain/ratelimit.go` | `RateLimitScope` 11 ตัว, `RateLimitRule`, `RateLimitDecision` | 6 |
| `internal/core/port/{clock,uow,auth,portfolio,audit}.go` | ports ที่ milestone 3 implement | 7 |
| `internal/core/boundary_test.go` | allowlist ของ import ใน core | 7 |
| `docs/adr/0013-domain-value-types.md`, phase 1 spec หัวข้อ 8, roadmap, ADR index | เอกสาร | 8 |

---
### Task 1: Errors, ID, Version และ Page

**Files:**
- Create: `internal/core/domain/errors.go`, `id.go`, `version.go`, `page.go`
- Test: `internal/core/domain/helpers_test.go`, `errors_test.go`, `id_test.go`, `version_page_test.go`

**Interfaces:**
- Consumes: ไม่มี (ใช้ `github.com/google/uuid v1.6.0` ที่อยู่ใน go.mod แล้ว)
- Produces:
  - `type domain.ErrorCode string` + ค่าคงที่ `domain.Code*` 25 ตัว (เช่น `CodeInvalidInput`, `CodeNotFound`, `CodeInternal`)
  - `type domain.Error struct{ Code ErrorCode; Message string }`, `func domain.NewError(code ErrorCode, message string) *Error`, เมธอด `Error()`, `Is(error) bool` (เทียบที่ Code)
  - sentinels `domain.ErrInvalidInput`, `ErrNotFound`, `ErrVersionConflict`, `ErrPortfolioNameConflict`, `ErrPortfolioNotEmpty`, `ErrIdentityConflict`, `ErrIdempotencyConflict`, `ErrInternal`
  - unexported `invalidInput(field, rule string) *Error`, `isDigits(s string) bool`
  - `type domain.ID`, `func domain.NewID() (ID, error)`, `func domain.ParseID(field, s string) (ID, error)`, `ID.String() string`, `ID.IsZero() bool`
  - `type domain.Version`, `func domain.ParseVersion(n int64) (Version, error)`, `Version.Int32() int32`, `Version.IsZero() bool`
  - `type domain.Page`, `func domain.ParsePage(rawLimit, cursor string) (Page, error)`, `Page.Limit() int`, `Page.Cursor() string`, `Page.IsZero() bool`, ค่าคงที่ `DefaultPageLimit = 20`, `MaxPageLimit = 100`, `MaxCursorBytes = 1024`
  - test helpers (แพ็กเกจ `domain_test`): `requireCode(t, err, code) string`, `requireInvalidInput(t, err, field)`, `mustID(t) domain.ID`

- [ ] **Step 1: เขียน test helpers และ failing tests**

`internal/core/domain/helpers_test.go`:

```go
package domain_test

import (
	"errors"
	"strings"
	"testing"

	"go-backend-stock/internal/core/domain"
)

// requireCode fails unless err is a *domain.Error with the wanted code, and returns its message.
func requireCode(t *testing.T, err error, want domain.ErrorCode) string {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) {
		t.Fatalf("error = %v (%T), want *domain.Error with code %s", err, err, want)
	}
	if de.Code != want {
		t.Fatalf("code = %s, want %s (message %q)", de.Code, want, de.Message)
	}
	return de.Message
}

// requireInvalidInput fails unless err is invalid_input and its message starts with field.
// Whether messages echo input is checked separately with a marker (TestErrorsNeverEchoInput),
// because short inputs such as "1" legitimately appear in fixed messages.
func requireInvalidInput(t *testing.T, err error, field string) {
	t.Helper()
	msg := requireCode(t, err, domain.CodeInvalidInput)
	if !strings.HasPrefix(msg, field+" ") {
		t.Errorf("message %q does not start with field %q", msg, field)
	}
}

func mustID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	return id
}
```

`internal/core/domain/errors_test.go`:

```go
package domain_test

import (
	"errors"
	"fmt"
	"testing"

	"go-backend-stock/internal/core/domain"
)

func TestErrorIsMatchesByCode(t *testing.T) {
	err := fmt.Errorf("load portfolio: %w", domain.NewError(domain.CodeNotFound, "portfolio not found"))

	if !errors.Is(err, domain.ErrNotFound) {
		t.Error("errors.Is(wrapped not_found, ErrNotFound) = false, want true")
	}
	if errors.Is(err, domain.ErrVersionConflict) {
		t.Error("errors.Is(not_found, ErrVersionConflict) = true, want false")
	}
	if errors.Is(err, errors.New("not_found: portfolio not found")) {
		t.Error("errors.Is matched a non-domain error with the same text")
	}
}

func TestErrorString(t *testing.T) {
	err := domain.NewError(domain.CodeInvalidInput, "quantity must be greater than zero")
	if got, want := err.Error(), "invalid_input: quantity must be greater than zero"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestErrorCodesMatchAPIContract(t *testing.T) {
	// Spec section 11 lists these codes; HTTP adapters map them to statuses.
	want := map[domain.ErrorCode]string{
		domain.CodeInvalidInput: "invalid_input", domain.CodeMalformedJSON: "malformed_json",
		domain.CodeUnauthenticated: "unauthenticated", domain.CodeInvalidCredentials: "invalid_credentials",
		domain.CodeInvalidReset: "invalid_reset", domain.CodeInvalidVerification: "invalid_verification",
		domain.CodeCSRFFailed: "csrf_failed", domain.CodeAccessDenied: "access_denied",
		domain.CodeEmailNotVerified: "email_not_verified", domain.CodeVersionConflict: "version_conflict",
		domain.CodePreconditionRequired: "precondition_required", domain.CodeNotFound: "not_found",
		domain.CodeIdentityConflict: "identity_conflict", domain.CodeIdempotencyConflict: "idempotency_conflict",
		domain.CodePortfolioNotEmpty: "portfolio_not_empty", domain.CodePortfolioNameConflict: "portfolio_name_conflict",
		domain.CodeInsufficientShares: "insufficient_shares", domain.CodeInvalidHistory: "invalid_history",
		domain.CodeUnknownSymbol: "unknown_symbol", domain.CodeRateLimited: "rate_limited",
		domain.CodeDependencyUnavailable: "dependency_unavailable", domain.CodeFeatureDisabled: "feature_disabled",
		domain.CodeServerBusy: "server_busy", domain.CodeInternal: "internal",
		domain.CodeAuditWriteFailed: "audit_write_failed",
	}
	if len(want) != 25 {
		t.Fatalf("test lists %d codes, spec section 11 has 25", len(want))
	}
	for code, s := range want {
		if string(code) != s {
			t.Errorf("code %q, want %q", code, s)
		}
	}
}
```

`internal/core/domain/id_test.go`:

```go
package domain_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"go-backend-stock/internal/core/domain"
)

func TestNewIDIsUUIDv7(t *testing.T) {
	id := mustID(t)
	u, err := uuid.Parse(id.String())
	if err != nil || u.Version() != 7 {
		t.Fatalf("NewID() = %q, want a UUIDv7 (err %v)", id, err)
	}
	if id.IsZero() {
		t.Error("NewID().IsZero() = true")
	}
	if mustID(t) == id {
		t.Error("two NewID() calls returned the same ID")
	}
}

func TestParseID(t *testing.T) {
	const canonical = "0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d"

	id, err := domain.ParseID("portfolio_id", strings.ToUpper(canonical))
	if err != nil {
		t.Fatalf("ParseID(upper case) error = %v", err)
	}
	if id.String() != canonical {
		t.Errorf("String() = %q, want lowercase %q", id.String(), canonical)
	}

	for _, bad := range []string{
		"",
		"not-a-uuid",
		"00000000-0000-0000-0000-000000000000", // nil UUID
		"0192f0c17a8e7cc49d2b3f6c1a2b4c5d",     // no hyphens
		"{0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d}",
		"urn:uuid:0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d",
		" " + canonical,
	} {
		_, err := domain.ParseID("portfolio_id", bad)
		requireInvalidInput(t, err, "portfolio_id")
	}
}

func TestZeroIDIsEmpty(t *testing.T) {
	var id domain.ID
	if !id.IsZero() || id.String() != "" {
		t.Errorf("zero ID: IsZero=%v String=%q, want true and empty", id.IsZero(), id.String())
	}
}
```

`internal/core/domain/version_page_test.go`:

```go
package domain_test

import (
	"math"
	"strings"
	"testing"

	"go-backend-stock/internal/core/domain"
)

func TestParseVersion(t *testing.T) {
	for _, n := range []int64{1, 2, math.MaxInt32} {
		v, err := domain.ParseVersion(n)
		if err != nil || int64(v.Int32()) != n || v.IsZero() {
			t.Errorf("ParseVersion(%d) = %v, %v", n, v.Int32(), err)
		}
	}
	for _, n := range []int64{0, -1, math.MaxInt32 + 1} {
		_, err := domain.ParseVersion(n)
		requireInvalidInput(t, err, "version")
	}
	var zero domain.Version
	if !zero.IsZero() {
		t.Error("zero Version.IsZero() = false")
	}
}

func TestParsePage(t *testing.T) {
	p, err := domain.ParsePage("", "")
	if err != nil || p.Limit() != domain.DefaultPageLimit || p.Cursor() != "" || p.IsZero() {
		t.Fatalf("ParsePage(\"\", \"\") = limit %d cursor %q err %v, want default 20", p.Limit(), p.Cursor(), err)
	}
	for raw, want := range map[string]int{"1": 1, "100": 100, "007": 7} {
		p, err := domain.ParsePage(raw, "abc")
		if err != nil || p.Limit() != want || p.Cursor() != "abc" {
			t.Errorf("ParsePage(%q) = %d, %v, want %d", raw, p.Limit(), err, want)
		}
	}
	for _, raw := range []string{"0", "101", "-1", "+5", "1.5", "abc", " 5", "1000000000000000000000"} {
		_, err := domain.ParsePage(raw, "")
		requireInvalidInput(t, err, "limit")
	}
	if _, err := domain.ParsePage("", strings.Repeat("a", domain.MaxCursorBytes)); err != nil {
		t.Errorf("1024-byte cursor rejected: %v", err)
	}
	_, err = domain.ParsePage("", strings.Repeat("a", domain.MaxCursorBytes+1))
	requireInvalidInput(t, err, "cursor")

	var zero domain.Page
	if !zero.IsZero() {
		t.Error("zero Page.IsZero() = false")
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้มเหลว**

Run: `go test ./internal/core/domain/`
Expected: FAIL ด้วย `no non-test Go files in .../internal/core/domain` (ยังไม่มีโค้ดของแพ็กเกจ)

- [ ] **Step 3: เขียนโค้ด**

`internal/core/domain/errors.go`:

```go
// Package domain holds the core's entities, value types and errors. It depends only on the
// standard library, github.com/shopspring/decimal and github.com/google/uuid.
//
// Values that come from outside the core are converted with Parse* functions, which return
// invalid_input errors. New* constructors check values the server builds itself and return
// internal errors, because a violation there is a bug rather than bad user input.
package domain

// ErrorCode is a stable machine-readable code shared with the API contract (spec section 11).
type ErrorCode string

const (
	CodeInvalidInput          ErrorCode = "invalid_input"
	CodeMalformedJSON         ErrorCode = "malformed_json"
	CodeUnauthenticated       ErrorCode = "unauthenticated"
	CodeInvalidCredentials    ErrorCode = "invalid_credentials" //nolint:gosec // G101 false positive: an error code name, not a credential.
	CodeInvalidReset          ErrorCode = "invalid_reset"
	CodeInvalidVerification   ErrorCode = "invalid_verification"
	CodeCSRFFailed            ErrorCode = "csrf_failed"
	CodeAccessDenied          ErrorCode = "access_denied"
	CodeEmailNotVerified      ErrorCode = "email_not_verified"
	CodeVersionConflict       ErrorCode = "version_conflict"
	CodePreconditionRequired  ErrorCode = "precondition_required"
	CodeNotFound              ErrorCode = "not_found"
	CodeIdentityConflict      ErrorCode = "identity_conflict"
	CodeIdempotencyConflict   ErrorCode = "idempotency_conflict"
	CodePortfolioNotEmpty     ErrorCode = "portfolio_not_empty"
	CodePortfolioNameConflict ErrorCode = "portfolio_name_conflict"
	CodeInsufficientShares    ErrorCode = "insufficient_shares"
	CodeInvalidHistory        ErrorCode = "invalid_history"
	CodeUnknownSymbol         ErrorCode = "unknown_symbol"
	CodeRateLimited           ErrorCode = "rate_limited"
	CodeDependencyUnavailable ErrorCode = "dependency_unavailable"
	CodeFeatureDisabled       ErrorCode = "feature_disabled"
	CodeServerBusy            ErrorCode = "server_busy"
	CodeInternal              ErrorCode = "internal"
	CodeAuditWriteFailed      ErrorCode = "audit_write_failed"
)

// Error is the single domain error type. Message is safe to return to clients: it names the
// field and the rule, and never contains the value the client sent.
type Error struct {
	Code    ErrorCode
	Message string
}

// NewError returns an *Error with the given code and client-safe message.
func NewError(code ErrorCode, message string) *Error {
	return &Error{Code: code, Message: message}
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// Is reports whether target is an *Error with the same Code, so errors.Is(err, ErrNotFound)
// matches whatever the message says.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// Sentinels for errors.Is. Compare by code; do not return these to share their messages.
var (
	ErrInvalidInput          = NewError(CodeInvalidInput, "invalid input")
	ErrNotFound              = NewError(CodeNotFound, "resource not found")
	ErrVersionConflict       = NewError(CodeVersionConflict, "resource was changed by another request")
	ErrPortfolioNameConflict = NewError(CodePortfolioNameConflict, "portfolio name already exists")
	ErrPortfolioNotEmpty     = NewError(CodePortfolioNotEmpty, "portfolio still has transactions")
	ErrIdentityConflict      = NewError(CodeIdentityConflict, "identity is already linked")
	ErrIdempotencyConflict   = NewError(CodeIdempotencyConflict, "idempotency key was used with a different request")
	ErrInternal              = NewError(CodeInternal, "internal error")
)

// invalidInput reports a rule a client-supplied value broke. field is the API field name.
func invalidInput(field, rule string) *Error {
	return NewError(CodeInvalidInput, field+" "+rule)
}
```

`internal/core/domain/id.go`:

```go
package domain

import (
	"fmt"

	"github.com/google/uuid"
)

const uuidLength = 36

// ID identifies an entity. It is a UUID in canonical lowercase form; the zero value is "no ID".
type ID struct{ value uuid.UUID }

// NewID returns a new UUIDv7, which sorts by creation time and keeps B-tree inserts appending.
func NewID() (ID, error) {
	u, err := uuid.NewV7()
	if err != nil {
		return ID{}, fmt.Errorf("generate id: %w", err)
	}
	return ID{value: u}, nil
}

// ParseID accepts only the canonical 36-character form (any letter case) and rejects the nil UUID.
// field names the value in error messages, e.g. "portfolio_id".
func ParseID(field, s string) (ID, error) {
	if len(s) != uuidLength {
		return ID{}, invalidInput(field, "must be a UUID")
	}
	u, err := uuid.Parse(s)
	if err != nil || u == uuid.Nil {
		return ID{}, invalidInput(field, "must be a UUID")
	}
	return ID{value: u}, nil
}

// String returns the canonical lowercase form, or "" for the zero ID.
func (id ID) String() string {
	if id.IsZero() {
		return ""
	}
	return id.value.String()
}

// IsZero reports whether id is the zero value (no ID).
func (id ID) IsZero() bool { return id.value == uuid.Nil }
```

`internal/core/domain/version.go`:

```go
package domain

import "math"

// Version is the optimistic-locking version of a portfolio or transaction. It starts at 1.
type Version struct{ value int32 }

// ParseVersion accepts 1..math.MaxInt32.
func ParseVersion(n int64) (Version, error) {
	if n < 1 || n > math.MaxInt32 {
		return Version{}, invalidInput("version", "must be a positive integer")
	}
	return Version{value: int32(n)}, nil
}

// Int32 returns the version number; 0 only for the zero value.
func (v Version) Int32() int32 { return v.value }

// IsZero reports whether v is the zero value, which is never a valid version.
func (v Version) IsZero() bool { return v.value == 0 }
```

`internal/core/domain/page.go`:

```go
package domain

import "strconv"

const (
	DefaultPageLimit = 20
	MaxPageLimit     = 100
	MaxCursorBytes   = 1024
)

// Page selects one page of a list. The cursor is opaque here: adapters encode and decode it.
type Page struct {
	limit  int
	cursor string
}

// ParsePage reads the limit and cursor query values. An empty limit means DefaultPageLimit.
func ParsePage(rawLimit, cursor string) (Page, error) {
	limit := DefaultPageLimit
	if rawLimit != "" {
		if len(rawLimit) > 3 || !isDigits(rawLimit) {
			return Page{}, invalidInput("limit", "must be an integer from 1 to 100")
		}
		n, err := strconv.Atoi(rawLimit)
		if err != nil || n < 1 || n > MaxPageLimit {
			return Page{}, invalidInput("limit", "must be an integer from 1 to 100")
		}
		limit = n
	}
	if len(cursor) > MaxCursorBytes {
		return Page{}, invalidInput("cursor", "must be at most 1024 bytes")
	}
	return Page{limit: limit, cursor: cursor}, nil
}

// Limit returns the page size; 0 only for the zero value.
func (p Page) Limit() int { return p.limit }

// Cursor returns the opaque cursor, or "" for the first page.
func (p Page) Cursor() string { return p.cursor }

// IsZero reports whether p is the zero value, which has no limit and must not be used.
func (p Page) IsZero() bool { return p.limit == 0 }

// isDigits reports whether s is non-empty and only ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
```

`//nolint:gosec` ใน `errors.go` มีเหตุผลกำกับ: gosec G101 เห็นคำว่า credentials ในชื่อ error code แล้วเข้าใจผิดว่าเป็นรหัสผ่าน

- [ ] **Step 4: รัน test, vet และ lint ให้ผ่าน**

Run: `go test -race -count=1 ./internal/core/... && go vet ./internal/core/... && make lint`
Expected: `ok  	go-backend-stock/internal/core/domain` และ golangci-lint `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/core/domain
git commit -m "feat(core): add domain errors, ID, version and page types"
```

---

### Task 2: ชนิดตัวเลข Quantity, Price, Fee

**Files:**
- Create: `internal/core/domain/decimal.go`
- Test: `internal/core/domain/decimal_test.go`, `internal/core/domain/echo_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `invalidInput`, `isDigits` (Task 1); `requireInvalidInput`, `requireCode` (Task 1 test helpers)
- Produces:
  - `type domain.Quantity`, `domain.Price`, `domain.Fee` (ห่อ `decimal.Decimal`)
  - `func domain.ParseQuantity(s string) (Quantity, error)`, `ParsePrice(s string) (Price, error)`, `ParseFee(s string) (Fee, error)`
  - เมธอด `Decimal() decimal.Decimal`, `String() string` (รูปมาตรฐาน) ทั้งสามชนิด; `IsZero() bool` เฉพาะ `Quantity` และ `Price` (zero value ของ `Fee` คือค่าธรรมเนียม 0 ที่ถูกต้อง)
  - test `TestErrorsNeverEchoInput` ที่มี `map parsers` ให้ Task 3 เพิ่มรายการ

- [ ] **Step 1: เพิ่ม dependency**

```bash
go get github.com/shopspring/decimal@v1.4.0
```

Expected: `go: added github.com/shopspring/decimal v1.4.0`

- [ ] **Step 2: เขียน failing tests**

`internal/core/domain/decimal_test.go`:

```go
package domain_test

import (
	"strings"
	"testing"

	"go-backend-stock/internal/core/domain"
)

var (
	max18Integer  = strings.Repeat("9", 18)
	max10Fraction = "0." + strings.Repeat("9", 10)
)

func TestParseDecimalsAccept(t *testing.T) {
	tests := []struct{ in, want string }{
		{"1", "1"},
		{"0.5", "0.5"},
		{"0.50", "0.5"},
		{"007", "7"},
		{"7.0", "7"},
		{"200.15", "200.15"},
		{"0.0000000001", "0.0000000001"},
		{max18Integer, max18Integer},
		{max10Fraction, max10Fraction},
		{max18Integer + "." + strings.Repeat("9", 10), max18Integer + "." + strings.Repeat("9", 10)},
		{"000" + max18Integer, max18Integer}, // leading zeros are not significant digits
	}
	for _, tt := range tests {
		q, err := domain.ParseQuantity(tt.in)
		if err != nil || q.String() != tt.want {
			t.Errorf("ParseQuantity(%q) = %q, %v; want %q", tt.in, q.String(), err, tt.want)
		}
		p, err := domain.ParsePrice(tt.in)
		if err != nil || p.String() != tt.want {
			t.Errorf("ParsePrice(%q) = %q, %v; want %q", tt.in, p.String(), err, tt.want)
		}
		f, err := domain.ParseFee(tt.in)
		if err != nil || f.String() != tt.want {
			t.Errorf("ParseFee(%q) = %q, %v; want %q", tt.in, f.String(), err, tt.want)
		}
	}
}

func TestParseDecimalsRejectBadFormat(t *testing.T) {
	bad := []string{
		"", " ", " 1", "1 ", "+1", "-1", "-0", ".5", "5.", "1.2.3", "1,000", "1_000",
		"1e3", "1E3", "0x10", "NaN", "nan", "Infinity", "-Infinity", "Inf",
		"١", // Arabic-Indic digit one: not ASCII
	}
	for _, s := range bad {
		_, err := domain.ParseQuantity(s)
		requireInvalidInput(t, err, "quantity")
		_, err = domain.ParsePrice(s)
		requireInvalidInput(t, err, "price")
		_, err = domain.ParseFee(s)
		requireInvalidInput(t, err, "fee")
	}
}

func TestParseDecimalsRejectOutOfRange(t *testing.T) {
	tooLong := []string{
		"1" + max18Integer,             // 19 integer digits
		"0." + strings.Repeat("1", 11), // 11 fraction digits
		"1." + strings.Repeat("0", 11), // 11 fraction digits even though they are zeros
		"1" + max18Integer + "." + "0", // 19 integer digits with a fraction
	}
	for _, s := range tooLong {
		_, err := domain.ParseQuantity(s)
		requireInvalidInput(t, err, "quantity")
		_, err = domain.ParseFee(s)
		requireInvalidInput(t, err, "fee")
	}
}

func TestZeroIsOnlyValidForFee(t *testing.T) {
	for _, s := range []string{"0", "0.0", "000", "0.0000000000"} {
		_, err := domain.ParseQuantity(s)
		requireInvalidInput(t, err, "quantity")
		_, err = domain.ParsePrice(s)
		requireInvalidInput(t, err, "price")
		f, err := domain.ParseFee(s)
		if err != nil || f.String() != "0" {
			t.Errorf("ParseFee(%q) = %q, %v; want \"0\"", s, f.String(), err)
		}
	}
}

func TestDecimalZeroValues(t *testing.T) {
	var q domain.Quantity
	var p domain.Price
	var f domain.Fee
	if !q.IsZero() || !p.IsZero() {
		t.Error("zero Quantity/Price must report IsZero")
	}
	if f.String() != "0" || !f.Decimal().IsZero() {
		t.Errorf("zero Fee = %q, want a valid fee of 0", f.String())
	}
	if q2, _ := domain.ParseQuantity("0.1"); q2.IsZero() {
		t.Error("parsed quantity reports IsZero")
	}
}

func TestDecimalArithmeticKeepsPrecision(t *testing.T) {
	q, _ := domain.ParseQuantity("0.1")
	p, _ := domain.ParsePrice("0.2")
	if got := q.Decimal().Add(p.Decimal()).String(); got != "0.3" {
		t.Errorf("0.1 + 0.2 = %s, want exactly 0.3 (no float)", got)
	}
}

// FuzzParseQuantity checks that no input panics and that every accepted value survives a
// round trip through its canonical string.
func FuzzParseQuantity(f *testing.F) {
	for _, seed := range []string{"1", "0.5", "007", "1e3", "-1", "", ".5", max18Integer, max10Fraction, "0.0000000001"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q, err := domain.ParseQuantity(s)
		if err != nil {
			return
		}
		again, err := domain.ParseQuantity(q.String())
		if err != nil {
			t.Fatalf("canonical %q of %q does not parse: %v", q.String(), s, err)
		}
		if !again.Decimal().Equal(q.Decimal()) || again.String() != q.String() {
			t.Fatalf("round trip of %q: %q != %q", s, again.String(), q.String())
		}
	})
}
```

`internal/core/domain/echo_test.go`:

```go
package domain_test

import (
	"strings"
	"testing"

	"go-backend-stock/internal/core/domain"
)

// echoMarker never appears in a fixed error message, so finding it means the input leaked.
const echoMarker = "ZQXJ"

// TestErrorsNeverEchoInput feeds every parser an invalid value carrying echoMarker and checks
// the message does not contain it: messages reach clients and logs.
func TestErrorsNeverEchoInput(t *testing.T) {
	parsers := map[string]func(string) error{
		"ParseID":       func(s string) error { _, err := domain.ParseID("id", s); return err },
		"ParsePage":     func(s string) error { _, err := domain.ParsePage(s, ""); return err },
		"ParseQuantity": func(s string) error { _, err := domain.ParseQuantity(s); return err },
		"ParsePrice":    func(s string) error { _, err := domain.ParsePrice(s); return err },
		"ParseFee":      func(s string) error { _, err := domain.ParseFee(s); return err },
	}
	for name, parse := range parsers {
		for _, in := range []string{echoMarker, "1" + echoMarker, echoMarker + "@", strings.Repeat(echoMarker, 300)} {
			err := parse(in)
			if err == nil {
				t.Fatalf("%s(%q) accepted an invalid value", name, in)
			}
			if strings.Contains(strings.ToUpper(err.Error()), echoMarker) {
				t.Errorf("%s error %q echoes the input", name, err)
			}
		}
	}
}
```

- [ ] **Step 3: รัน test ให้เห็นว่าล้มเหลว**

Run: `go test ./internal/core/domain/`
Expected: FAIL ด้วย `undefined: domain.ParseQuantity` และ `undefined: domain.ParsePrice`

- [ ] **Step 4: เขียน `decimal.go`**

`internal/core/domain/decimal.go`:

```go
package domain

import (
	"strings"

	"github.com/shopspring/decimal"
)

// Limits of numeric(28,10): user-entered values are rejected, never rounded, when they exceed them.
const (
	maxIntegerDigits  = 18
	maxFractionDigits = 10
)

// Quantity is a number of shares; fractional shares are allowed. Always greater than zero.
type Quantity struct{ value decimal.Decimal }

// Price is a per-share price in USD. Always greater than zero.
type Price struct{ value decimal.Decimal }

// Fee is a trade fee in USD. Zero or greater; the zero value is a valid fee of 0.
type Fee struct{ value decimal.Decimal }

// ParseQuantity parses a plain decimal string such as "0.5".
func ParseQuantity(s string) (Quantity, error) {
	d, err := parsePositive("quantity", s)
	return Quantity{value: d}, err
}

// ParsePrice parses a plain decimal string such as "200.15".
func ParsePrice(s string) (Price, error) {
	d, err := parsePositive("price", s)
	return Price{value: d}, err
}

// ParseFee parses a plain decimal string such as "1" or "0".
func ParseFee(s string) (Fee, error) {
	d, err := parseDecimal("fee", s)
	if err != nil {
		return Fee{}, err
	}
	return Fee{value: d}, nil
}

// Decimal returns the value for arithmetic.
func (q Quantity) Decimal() decimal.Decimal { return q.value }

// String returns the canonical form: no exponent and no trailing zeros ("0.50" -> "0.5").
func (q Quantity) String() string { return q.value.String() }

// IsZero reports whether q is the zero value, which is never a valid quantity.
func (q Quantity) IsZero() bool { return q.value.IsZero() }

// Decimal returns the value for arithmetic.
func (p Price) Decimal() decimal.Decimal { return p.value }

// String returns the canonical form: no exponent and no trailing zeros.
func (p Price) String() string { return p.value.String() }

// IsZero reports whether p is the zero value, which is never a valid price.
func (p Price) IsZero() bool { return p.value.IsZero() }

// Decimal returns the value for arithmetic.
func (f Fee) Decimal() decimal.Decimal { return f.value }

// String returns the canonical form: no exponent and no trailing zeros.
func (f Fee) String() string { return f.value.String() }

func parsePositive(field, s string) (decimal.Decimal, error) {
	d, err := parseDecimal(field, s)
	if err != nil {
		return decimal.Decimal{}, err
	}
	if !d.IsPositive() {
		return decimal.Decimal{}, invalidInput(field, "must be greater than zero")
	}
	return d, nil
}

// parseDecimal accepts only ^[0-9]+(\.[0-9]+)?$ within numeric(28,10): no sign, exponent,
// whitespace, NaN or Infinity, at most 18 significant integer digits and 10 fraction digits.
func parseDecimal(field, s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Decimal{}, invalidInput(field, "is required")
	}
	intPart, fracPart, hasDot := strings.Cut(s, ".")
	if !isDigits(intPart) || (hasDot && !isDigits(fracPart)) {
		return decimal.Decimal{}, invalidInput(field, "must be a plain decimal number such as 12.5")
	}
	if len(strings.TrimLeft(intPart, "0")) > maxIntegerDigits {
		return decimal.Decimal{}, invalidInput(field, "must have at most 18 digits before the decimal point")
	}
	if len(fracPart) > maxFractionDigits {
		return decimal.Decimal{}, invalidInput(field, "must have at most 10 digits after the decimal point")
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		// Unreachable after the checks above; never wrap err because it repeats the input.
		return decimal.Decimal{}, invalidInput(field, "must be a plain decimal number such as 12.5")
	}
	return d, nil
}
```

- [ ] **Step 5: รัน test ให้ผ่าน แล้วรัน fuzz สั้นๆ**

Run: `go test -race -count=1 ./internal/core/...`
Expected: `ok  	go-backend-stock/internal/core/domain`

Run: `go test -run '^$' -fuzz FuzzParseQuantity -fuzztime 20s ./internal/core/domain/`
Expected: จบด้วย `PASS` และ `ok` (prototype รันได้ ~5 ล้านครั้งใน 20 วินาทีโดยไม่พบ input ที่ทำให้ล้ม) ถ้าพบ ให้เก็บไฟล์ใน `testdata/fuzz/` ไว้เป็น regression แล้วแก้โค้ดตาม systematic-debugging

- [ ] **Step 6: ยืนยันว่า `TestErrorsNeverEchoInput` จับการรั่วได้จริง**

แก้ `decimal.go` ชั่วคราว: ในบรรทัด `invalidInput(field, "must be a plain decimal number such as 12.5")` ตัวแรก ต่อท้ายข้อความด้วย `+ ", got " + s`
Run: `go test -count=1 -run TestErrorsNeverEchoInput ./internal/core/domain/`
Expected: FAIL ด้วย `ParseQuantity error "... got ZQXJ" echoes the input`
แล้วแก้กลับ และรัน `git diff --stat internal/core/domain/decimal.go` ต้องไม่มี diff จากไฟล์ใน Step 4

- [ ] **Step 7: tidy, lint และ commit**

Run: `go mod tidy && grep shopspring go.mod && make lint`
Expected: `github.com/shopspring/decimal v1.4.0` (ไม่มี `// indirect`) และ `0 issues.`

```bash
git add go.mod go.sum internal/core/domain
git commit -m "feat(core): add Quantity, Price and Fee decimal types"
```

---

### Task 3: Symbol, Email, เวลา executed_at และชื่อ/คำอธิบายพอร์ต

**Files:**
- Create: `internal/core/domain/symbol.go`, `email.go`, `time.go`, `portfolio.go`, `text.go`
- Test: `internal/core/domain/text_test.go`
- Modify: `internal/core/domain/echo_test.go` (เพิ่ม parsers)

**Interfaces:**
- Consumes: `invalidInput` (Task 1); test helpers (Task 1); `echoMarker` และ `TestErrorsNeverEchoInput` (Task 2)
- Produces:
  - `type domain.Symbol`, `func domain.ParseSymbol(s string) (Symbol, error)`, `String()`, `IsZero()`
  - `type domain.Email`, `func domain.ParseEmail(s string) (Email, error)`, `String()`, `IsZero()`
  - `func domain.ParseExecutedAt(s string, now time.Time) (time.Time, error)` — คืนเวลา UTC
  - `type domain.PortfolioName`, `func domain.ParsePortfolioName(s string) (PortfolioName, error)`, `String()`, `IsZero()`
  - `type domain.PortfolioDescription`, `func domain.ParsePortfolioDescription(s string) (PortfolioDescription, error)`, `String()`
  - unexported `hasControl(s string) bool`, `isASCII(s string) bool` ใน `text.go`

- [ ] **Step 1: เขียน failing tests**

`internal/core/domain/text_test.go`:

```go
package domain_test

import (
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestParseSymbol(t *testing.T) {
	for in, want := range map[string]string{
		"AAPL": "AAPL", "aapl": "AAPL", " msft ": "MSFT", "brk.b": "BRK.B", "BF-B": "BF-B",
		"A": "A", "ABCDEFGHIJKLMNO": "ABCDEFGHIJKLMNO", // 15 characters
	} {
		s, err := domain.ParseSymbol(in)
		if err != nil || s.String() != want {
			t.Errorf("ParseSymbol(%q) = %q, %v; want %q", in, s, err, want)
		}
	}
	for _, in := range []string{
		"", "   ", "1ABC", ".ABC", "-ABC", "AB CD", "AB_C", "ABCDEFGHIJKLMNOP", // 16 characters
		"ıbm",  // dotless i upper-cases to ASCII "I"
		"ſpy",  // long s upper-cases to ASCII "S"
		"ＡＡＰＬ", // full-width letters
	} {
		_, err := domain.ParseSymbol(in)
		requireInvalidInput(t, err, "symbol")
	}
	var zero domain.Symbol
	if !zero.IsZero() {
		t.Error("zero Symbol.IsZero() = false")
	}
}

func TestParseEmail(t *testing.T) {
	for in, want := range map[string]string{
		"User@Example.COM":                        "user@example.com",
		"  a.b+tag@example.com  ":                 "a.b+tag@example.com", // dots and +suffix are kept
		"ÄLICE@EXAMPLE.COM":                       "älice@example.com",
		strings.Repeat("a", 242) + "@example.com": strings.Repeat("a", 242) + "@example.com", // 254
	} {
		e, err := domain.ParseEmail(in)
		if err != nil || e.String() != want {
			t.Errorf("ParseEmail(%q) = %q, %v; want %q", in, e, err, want)
		}
	}
	for _, in := range []string{
		"", "   ", "user", "@example.com", "user@", "a@b@c", "us er@example.com", "user@exa\tmple.com",
		"user\x00@example.com", "\xff@example.com",
		strings.Repeat("a", 243) + "@example.com", // 255
	} {
		_, err := domain.ParseEmail(in)
		requireInvalidInput(t, err, "email")
	}
}

func TestParseExecutedAt(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	for in, want := range map[string]time.Time{
		"2026-10-01T14:30:00Z":         time.Date(2026, 10, 1, 14, 30, 0, 0, time.UTC),
		"2026-10-01T21:30:00+07:00":    time.Date(2026, 10, 1, 14, 30, 0, 0, time.UTC),
		"2026-10-01T14:30:00.123456Z":  time.Date(2026, 10, 1, 14, 30, 0, 123456000, time.UTC),
		"2026-10-01T14:30:00.1234560Z": time.Date(2026, 10, 1, 14, 30, 0, 123456000, time.UTC), // trailing zero loses nothing
		"1970-01-01T00:00:00Z":         time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		"2026-10-16T12:00:00Z":         now, // exactly now is allowed
	} {
		got, err := domain.ParseExecutedAt(in, now)
		if err != nil || !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("ParseExecutedAt(%q) = %v, %v; want %v in UTC", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "2026-10-01", "2026-10-01T14:30:00", "2026-10-01 14:30:00Z", "1696170600",
		"2026-10-01T14:30:00.1234567Z", // sub-microsecond
		"1969-12-31T23:59:59.999999Z",  // before 1970
		"1970-01-01T06:59:59+07:00",    // before 1970 once converted to UTC
		"2026-10-16T12:00:00.000001Z",  // one microsecond in the future
		"2026-10-16T19:00:01+07:00",    // future once converted to UTC
	} {
		_, err := domain.ParseExecutedAt(in, now)
		requireInvalidInput(t, err, "executed_at")
	}
}

func TestParsePortfolioName(t *testing.T) {
	thai100 := strings.Repeat("ก", 100) // 300 bytes, 100 characters
	for in, want := range map[string]string{
		"Long term": "Long term", "  ระยะยาว  ": "ระยะยาว", thai100: thai100, "a": "a",
		"\tname\n": "name", "name\u0085": "name", // surrounding whitespace, including line breaks, is trimmed
	} {
		n, err := domain.ParsePortfolioName(in)
		if err != nil || n.String() != want {
			t.Errorf("ParsePortfolioName(%q) = %q, %v; want %q", in, n, err, want)
		}
	}
	for _, in := range []string{"", "   ", "\n\t", thai100 + "ก", "line\nbreak", "tab\there", "nul\x00", "c1\u0085x", "\xff"} {
		_, err := domain.ParsePortfolioName(in)
		requireInvalidInput(t, err, "name")
	}
	var zero domain.PortfolioName
	if !zero.IsZero() {
		t.Error("zero PortfolioName.IsZero() = false")
	}
}

func TestParsePortfolioDescription(t *testing.T) {
	thai1000 := strings.Repeat("ข", 1000)
	for _, in := range []string{"", "  kept as written  ", thai1000} {
		d, err := domain.ParsePortfolioDescription(in)
		if err != nil || d.String() != in {
			t.Errorf("ParsePortfolioDescription(%q) = %q, %v; want unchanged", in, d, err)
		}
	}
	for _, in := range []string{thai1000 + "ข", "line\nbreak", "bell\a", "\xff"} {
		_, err := domain.ParsePortfolioDescription(in)
		requireInvalidInput(t, err, "description")
	}
}
```

ใน `internal/core/domain/echo_test.go` เพิ่มรายการต่อไปนี้ใน `parsers` ต่อจากบรรทัด `"ParseFee": ...` และเพิ่ม `"time"` ใน import:

```go
		"ParseSymbol":   func(s string) error { _, err := domain.ParseSymbol("1" + s); return err },
		"ParseEmail":    func(s string) error { _, err := domain.ParseEmail(s + " x"); return err },
		"ParseExecutedAt": func(s string) error {
			_, err := domain.ParseExecutedAt(s, time.Now())
			return err
		},
		"ParsePortfolioName":        func(s string) error { _, err := domain.ParsePortfolioName(s + "\x01x"); return err },
		"ParsePortfolioDescription": func(s string) error { _, err := domain.ParsePortfolioDescription(s + "\x01x"); return err },
```

ไฟล์ทั้งไฟล์หลังแก้ต้องเป็นดังนี้:

`internal/core/domain/echo_test.go`:

```go
package domain_test

import (
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// echoMarker never appears in a fixed error message, so finding it means the input leaked.
const echoMarker = "ZQXJ"

// TestErrorsNeverEchoInput feeds every parser an invalid value carrying echoMarker and checks
// the message does not contain it: messages reach clients and logs.
func TestErrorsNeverEchoInput(t *testing.T) {
	parsers := map[string]func(string) error{
		"ParseID":       func(s string) error { _, err := domain.ParseID("id", s); return err },
		"ParsePage":     func(s string) error { _, err := domain.ParsePage(s, ""); return err },
		"ParseQuantity": func(s string) error { _, err := domain.ParseQuantity(s); return err },
		"ParsePrice":    func(s string) error { _, err := domain.ParsePrice(s); return err },
		"ParseFee":      func(s string) error { _, err := domain.ParseFee(s); return err },
		"ParseSymbol":   func(s string) error { _, err := domain.ParseSymbol("1" + s); return err },
		"ParseEmail":    func(s string) error { _, err := domain.ParseEmail(s + " x"); return err },
		"ParseExecutedAt": func(s string) error {
			_, err := domain.ParseExecutedAt(s, time.Now())
			return err
		},
		"ParsePortfolioName":        func(s string) error { _, err := domain.ParsePortfolioName(s + "\x01x"); return err },
		"ParsePortfolioDescription": func(s string) error { _, err := domain.ParsePortfolioDescription(s + "\x01x"); return err },
	}
	for name, parse := range parsers {
		for _, in := range []string{echoMarker, "1" + echoMarker, echoMarker + "@", strings.Repeat(echoMarker, 300)} {
			err := parse(in)
			if err == nil {
				t.Fatalf("%s(%q) accepted an invalid value", name, in)
			}
			if strings.Contains(strings.ToUpper(err.Error()), echoMarker) {
				t.Errorf("%s error %q echoes the input", name, err)
			}
		}
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้มเหลว**

Run: `go test ./internal/core/domain/`
Expected: FAIL ด้วย `undefined: domain.ParseSymbol`, `undefined: domain.ParseEmail` ฯลฯ

- [ ] **Step 3: เขียนโค้ด**

`internal/core/domain/text.go`:

```go
package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// hasControl reports whether s contains a C0 control character, DEL or a C1 control character.
func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
```

`internal/core/domain/symbol.go`:

```go
package domain

import (
	"regexp"
	"strings"
)

var symbolPattern = regexp.MustCompile(`^[A-Z][A-Z0-9.-]{0,14}$`)

// Symbol is a US stock ticker in upper case, such as "AAPL" or "BRK.B".
type Symbol struct{ value string }

// ParseSymbol trims and upper-cases s, then requires ^[A-Z][A-Z0-9.-]{0,14}$. Non-ASCII input is
// rejected before upper-casing, because characters such as "ı" upper-case to ASCII letters.
func ParseSymbol(s string) (Symbol, error) {
	v := strings.TrimSpace(s)
	if !isASCII(v) {
		return Symbol{}, invalidInput("symbol", "must contain only ASCII letters, digits, '.' or '-'")
	}
	v = strings.ToUpper(v)
	if !symbolPattern.MatchString(v) {
		return Symbol{}, invalidInput("symbol", "must be 1-15 characters: a letter, then letters, digits, '.' or '-'")
	}
	return Symbol{value: v}, nil
}

func (s Symbol) String() string { return s.value }

// IsZero reports whether s is the zero value (no symbol).
func (s Symbol) IsZero() bool { return s.value == "" }
```

`internal/core/domain/email.go`:

```go
package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxEmailRunes = 254

// Email is a trimmed, lower-cased address. Dots and +suffixes are kept as typed.
type Email struct{ value string }

// ParseEmail checks only the shape (one "@" with text on both sides): ownership is proven by
// the verification OTP, not by syntax.
func ParseEmail(s string) (Email, error) {
	v := strings.TrimSpace(s)
	if !utf8.ValidString(v) {
		return Email{}, invalidInput("email", "must be valid UTF-8")
	}
	v = strings.ToLower(v)
	n := utf8.RuneCountInString(v)
	if n == 0 {
		return Email{}, invalidInput("email", "is required")
	}
	if n > maxEmailRunes {
		return Email{}, invalidInput("email", "must be at most 254 characters")
	}
	if strings.IndexFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return Email{}, invalidInput("email", "must not contain spaces or control characters")
	}
	local, domainPart, ok := strings.Cut(v, "@")
	if !ok || local == "" || domainPart == "" || strings.Contains(domainPart, "@") {
		return Email{}, invalidInput("email", "must be an address such as name@example.com")
	}
	return Email{value: v}, nil
}

func (e Email) String() string { return e.value }

// IsZero reports whether e is the zero value (no email).
func (e Email) IsZero() bool { return e.value == "" }
```

`internal/core/domain/time.go`:

```go
package domain

import "time"

var minExecutedAt = time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)

// ParseExecutedAt parses the time a trade was executed. It requires RFC 3339 with an explicit
// offset, rejects sub-microsecond precision instead of rounding (PostgreSQL keeps microseconds),
// and accepts 1970-01-01T00:00:00Z up to and including now. The result is in UTC.
func ParseExecutedAt(s string, now time.Time) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, invalidInput("executed_at", "must be an RFC 3339 timestamp with a timezone, such as 2026-10-01T14:30:00Z")
	}
	if t.Nanosecond()%int(time.Microsecond) != 0 {
		return time.Time{}, invalidInput("executed_at", "must have at most microsecond precision")
	}
	t = t.UTC()
	if t.Before(minExecutedAt) {
		return time.Time{}, invalidInput("executed_at", "must not be before 1970-01-01T00:00:00Z")
	}
	if t.After(now) {
		return time.Time{}, invalidInput("executed_at", "must not be in the future")
	}
	return t, nil
}
```

`internal/core/domain/portfolio.go`:

```go
package domain

import (
	"strings"
	"unicode/utf8"
)

const (
	maxPortfolioNameRunes        = 100
	maxPortfolioDescriptionRunes = 1000
)

// PortfolioName is a trimmed, non-empty name of at most 100 characters.
type PortfolioName struct{ value string }

// ParsePortfolioName trims s and counts characters (runes), not bytes, to match varchar(100).
func ParsePortfolioName(s string) (PortfolioName, error) {
	v := strings.TrimSpace(s)
	if !utf8.ValidString(v) {
		return PortfolioName{}, invalidInput("name", "must be valid UTF-8")
	}
	if v == "" {
		return PortfolioName{}, invalidInput("name", "is required")
	}
	if utf8.RuneCountInString(v) > maxPortfolioNameRunes {
		return PortfolioName{}, invalidInput("name", "must be at most 100 characters")
	}
	if hasControl(v) {
		return PortfolioName{}, invalidInput("name", "must not contain control characters")
	}
	return PortfolioName{value: v}, nil
}

func (n PortfolioName) String() string { return n.value }

// IsZero reports whether n is the zero value, which is never a valid name.
func (n PortfolioName) IsZero() bool { return n.value == "" }

// PortfolioDescription is optional text of at most 1000 characters; the zero value is "".
type PortfolioDescription struct{ value string }

// ParsePortfolioDescription keeps s as written (no trimming) and rejects control characters,
// including line breaks.
func ParsePortfolioDescription(s string) (PortfolioDescription, error) {
	if !utf8.ValidString(s) {
		return PortfolioDescription{}, invalidInput("description", "must be valid UTF-8")
	}
	if utf8.RuneCountInString(s) > maxPortfolioDescriptionRunes {
		return PortfolioDescription{}, invalidInput("description", "must be at most 1000 characters")
	}
	if hasControl(s) {
		return PortfolioDescription{}, invalidInput("description", "must not contain control characters")
	}
	return PortfolioDescription{value: s}, nil
}

func (d PortfolioDescription) String() string { return d.value }
```

- [ ] **Step 4: รัน test และ lint ให้ผ่าน**

Run: `go test -race -count=1 ./internal/core/... && make lint`
Expected: `ok` และ `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/core/domain
git commit -m "feat(core): add symbol, email, executed_at and portfolio name types"
```

---

### Task 4: Entities ของ auth — User, Session, AuthProvider, OIDCFlow, OTPChallenge

**Files:**
- Create: `internal/core/domain/rules.go`, `user.go`, `otp.go`
- Test: `internal/core/domain/auth_entities_test.go`

**Interfaces:**
- Consumes: `ID`, `Email`, `hasControl`, `invalidInput`, `NewError`, `CodeInternal` (Tasks 1, 3)
- Produces:
  - `const domain.HashSize = 32`; unexported `invariant(field, rule string) *Error`, `parseEnum[T ~string](field, s string, allowed ...T) (T, error)`, `isOneOf[T comparable](v T, allowed ...T) bool`, `requireID(field string, id ID) error`, `requireHash(field string, h []byte) error`, `requireExpiry(createdAt, expiresAt time.Time) error`, `firstError(errs ...error) error`
  - `domain.User{ID, Email, EmailVerifiedAt *time.Time, CreatedAt, UpdatedAt}` — ไม่มี field password hash
  - `domain.RevokeReason` (`RevokeReasonLogout`, `RevokeReasonPasswordReset`), `ParseRevokeReason`
  - `domain.Session{ID, UserID, TokenHash []byte, CreatedAt, ExpiresAt, RevokedAt *time.Time, RevokeReason}`, `NewSession(Session) (Session, error)`
  - `domain.IdentityProviderName` (`IdentityProviderGoogle`), `ParseIdentityProviderName`
  - `domain.AuthProvider{ID, UserID, Provider, ProviderSubject string, CreatedAt}`, `NewAuthProvider(AuthProvider) (AuthProvider, error)`
  - `domain.OIDCPurpose` (`OIDCPurposeLogin`, `OIDCPurposeLink`), `ParseOIDCPurpose`
  - `domain.OIDCFlow{ID, Purpose, UserID, StateHash, NonceHash, BrowserBindingHash []byte, CreatedAt, ExpiresAt, ConsumedAt *time.Time}`, `NewOIDCFlow(OIDCFlow) (OIDCFlow, error)`
  - `domain.OTPPurpose` (`OTPPurposeEmailVerification`, `OTPPurposePasswordReset`), `ParseOTPPurpose`; `const MaxOTPFailedAttemptsCeiling = 20`
  - `domain.OTPChallenge{ID, UserID, Purpose, OTPHash []byte, CreatedAt, ExpiresAt, FailedAttempts int, ConsumedAt, InvalidatedAt *time.Time}`, `NewOTPChallenge(OTPChallenge) (OTPChallenge, error)`
  - test fixtures (แพ็กเกจ `domain_test`, ใช้ต่อใน Task 5–6): `t0 time.Time`, `hash32`, `hash31 []byte`, `revoked time.Time`

- [ ] **Step 1: เขียน failing tests**

`internal/core/domain/auth_entities_test.go`:

```go
package domain_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

var (
	t0      = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	hash32  = bytes.Repeat([]byte{7}, domain.HashSize)
	hash31  = bytes.Repeat([]byte{7}, domain.HashSize-1)
	revoked = t0.Add(time.Minute)
)

func TestEnumsMatchDBChecksExactly(t *testing.T) {
	ok := map[string]func(string) error{
		"logout":             func(s string) error { _, err := domain.ParseRevokeReason(s); return err },
		"password_reset":     func(s string) error { _, err := domain.ParseRevokeReason(s); return err },
		"google":             func(s string) error { _, err := domain.ParseIdentityProviderName(s); return err },
		"login":              func(s string) error { _, err := domain.ParseOIDCPurpose(s); return err },
		"link":               func(s string) error { _, err := domain.ParseOIDCPurpose(s); return err },
		"email_verification": func(s string) error { _, err := domain.ParseOTPPurpose(s); return err },
	}
	for value, parse := range ok {
		if err := parse(value); err != nil {
			t.Errorf("parse(%q) error = %v", value, err)
		}
		if err := parse(strings.ToUpper(value)); err == nil {
			t.Errorf("parse(%q) accepted a different case", strings.ToUpper(value))
		}
		if err := parse(" " + value); err == nil {
			t.Errorf("parse(%q) accepted surrounding space", " "+value)
		}
	}
	_, err := domain.ParseOTPPurpose("login")
	requireInvalidInput(t, err, "purpose")
	if !strings.Contains(err.Error(), "email_verification, password_reset") {
		t.Errorf("enum error %q does not list the allowed values", err)
	}
}

func validSession(t *testing.T) domain.Session {
	return domain.Session{ID: mustID(t), UserID: mustID(t), TokenHash: hash32, CreatedAt: t0, ExpiresAt: t0.Add(7 * 24 * time.Hour)}
}

func TestNewSession(t *testing.T) {
	if _, err := domain.NewSession(validSession(t)); err != nil {
		t.Fatalf("NewSession(valid) error = %v", err)
	}
	bad := map[string]func(*domain.Session){
		"no id":            func(s *domain.Session) { s.ID = domain.ID{} },
		"no user":          func(s *domain.Session) { s.UserID = domain.ID{} },
		"short hash":       func(s *domain.Session) { s.TokenHash = hash31 },
		"nil hash":         func(s *domain.Session) { s.TokenHash = nil },
		"no created_at":    func(s *domain.Session) { s.CreatedAt = time.Time{} },
		"expires at start": func(s *domain.Session) { s.ExpiresAt = s.CreatedAt },
		"already revoked":  func(s *domain.Session) { s.RevokedAt = &revoked },
		"has reason":       func(s *domain.Session) { s.RevokeReason = domain.RevokeReasonLogout },
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			s := validSession(t)
			mutate(&s)
			_, err := domain.NewSession(s)
			requireCode(t, err, domain.CodeInternal)
		})
	}
}

func TestNewAuthProvider(t *testing.T) {
	valid := func() domain.AuthProvider {
		return domain.AuthProvider{ID: mustID(t), UserID: mustID(t), Provider: domain.IdentityProviderGoogle, ProviderSubject: "1098765432101234567890", CreatedAt: t0}
	}
	if _, err := domain.NewAuthProvider(valid()); err != nil {
		t.Fatalf("NewAuthProvider(valid) error = %v", err)
	}
	p := valid()
	p.ProviderSubject = strings.Repeat("9", 255)
	if _, err := domain.NewAuthProvider(p); err != nil {
		t.Errorf("255-character subject rejected: %v", err)
	}
	for name, mutate := range map[string]func(*domain.AuthProvider){
		"no user":       func(p *domain.AuthProvider) { p.UserID = domain.ID{} },
		"bad provider":  func(p *domain.AuthProvider) { p.Provider = "github" },
		"empty subject": func(p *domain.AuthProvider) { p.ProviderSubject = "" },
		"long subject":  func(p *domain.AuthProvider) { p.ProviderSubject = strings.Repeat("9", 256) },
		"control":       func(p *domain.AuthProvider) { p.ProviderSubject = "12\n34" },
		"no created_at": func(p *domain.AuthProvider) { p.CreatedAt = time.Time{} },
	} {
		p := valid()
		mutate(&p)
		if _, err := domain.NewAuthProvider(p); err == nil {
			t.Errorf("%s: NewAuthProvider accepted an invalid link", name)
		}
	}
}

func TestNewOIDCFlowUserRule(t *testing.T) {
	flow := func(purpose domain.OIDCPurpose, user domain.ID) domain.OIDCFlow {
		return domain.OIDCFlow{ID: mustID(t), Purpose: purpose, UserID: user, StateHash: hash32, NonceHash: hash32,
			BrowserBindingHash: hash32, CreatedAt: t0, ExpiresAt: t0.Add(10 * time.Minute)}
	}
	if _, err := domain.NewOIDCFlow(flow(domain.OIDCPurposeLogin, domain.ID{})); err != nil {
		t.Errorf("login flow without user rejected: %v", err)
	}
	if _, err := domain.NewOIDCFlow(flow(domain.OIDCPurposeLink, mustID(t))); err != nil {
		t.Errorf("link flow with user rejected: %v", err)
	}
	for name, f := range map[string]domain.OIDCFlow{
		"login with user":   flow(domain.OIDCPurposeLogin, mustID(t)),
		"link without user": flow(domain.OIDCPurposeLink, domain.ID{}),
		"unknown purpose":   flow("signup", domain.ID{}),
		"short nonce hash": func() domain.OIDCFlow {
			f := flow(domain.OIDCPurposeLogin, domain.ID{})
			f.NonceHash = hash31
			return f
		}(),
		"already consumed": func() domain.OIDCFlow {
			f := flow(domain.OIDCPurposeLogin, domain.ID{})
			f.ConsumedAt = &revoked
			return f
		}(),
		"expires before now": func() domain.OIDCFlow {
			f := flow(domain.OIDCPurposeLogin, domain.ID{})
			f.ExpiresAt = t0.Add(-time.Second)
			return f
		}(),
	} {
		_, err := domain.NewOIDCFlow(f)
		if err == nil {
			t.Errorf("%s: NewOIDCFlow accepted an invalid flow", name)
		}
	}
}

func TestNewOTPChallenge(t *testing.T) {
	valid := func() domain.OTPChallenge {
		return domain.OTPChallenge{ID: mustID(t), UserID: mustID(t), Purpose: domain.OTPPurposePasswordReset, OTPHash: hash32, CreatedAt: t0, ExpiresAt: t0.Add(10 * time.Minute)}
	}
	if _, err := domain.NewOTPChallenge(valid()); err != nil {
		t.Fatalf("NewOTPChallenge(valid) error = %v", err)
	}
	for name, mutate := range map[string]func(*domain.OTPChallenge){
		"unknown purpose":  func(c *domain.OTPChallenge) { c.Purpose = "login" },
		"failed attempts":  func(c *domain.OTPChallenge) { c.FailedAttempts = 1 },
		"consumed":         func(c *domain.OTPChallenge) { c.ConsumedAt = &revoked },
		"invalidated":      func(c *domain.OTPChallenge) { c.InvalidatedAt = &revoked },
		"short hash":       func(c *domain.OTPChallenge) { c.OTPHash = hash31 },
		"expires at start": func(c *domain.OTPChallenge) { c.ExpiresAt = c.CreatedAt },
	} {
		c := valid()
		mutate(&c)
		if _, err := domain.NewOTPChallenge(c); err == nil {
			t.Errorf("%s: NewOTPChallenge accepted an invalid challenge", name)
		}
	}
	if domain.MaxOTPFailedAttemptsCeiling != 20 {
		t.Errorf("MaxOTPFailedAttemptsCeiling = %d, want the DB CHECK ceiling 20", domain.MaxOTPFailedAttemptsCeiling)
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้มเหลว**

Run: `go test ./internal/core/domain/`
Expected: FAIL ด้วย `undefined: domain.ParseRevokeReason`, `undefined: domain.Session`, `undefined: domain.HashSize` ฯลฯ

- [ ] **Step 3: เขียนโค้ด**

`internal/core/domain/rules.go`:

```go
package domain

import (
	"strings"
	"time"
)

// HashSize is the length of every stored token, OTP, state, nonce and key hash (SHA-256/HMAC-SHA-256).
const HashSize = 32

// invariant reports a rule a server-built value broke: a bug, not bad input.
func invariant(field, rule string) *Error {
	return NewError(CodeInternal, field+" "+rule)
}

// parseEnum returns s as T when it is exactly one of allowed (case-sensitive, matching the DB CHECK).
func parseEnum[T ~string](field, s string, allowed ...T) (T, error) {
	for _, a := range allowed {
		if string(a) == s {
			return a, nil
		}
	}
	names := make([]string, len(allowed))
	for i, a := range allowed {
		names[i] = string(a)
	}
	var zero T
	return zero, invalidInput(field, "must be one of "+strings.Join(names, ", "))
}

// isOneOf reports whether v is one of allowed; constructors use it to check server-built enums.
func isOneOf[T comparable](v T, allowed ...T) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

func requireID(field string, id ID) error {
	if id.IsZero() {
		return invariant(field, "is required")
	}
	return nil
}

func requireHash(field string, h []byte) error {
	if len(h) != HashSize {
		return invariant(field, "must be 32 bytes")
	}
	return nil
}

func requireExpiry(createdAt, expiresAt time.Time) error {
	if createdAt.IsZero() {
		return invariant("created_at", "is required")
	}
	if !expiresAt.After(createdAt) {
		return invariant("expires_at", "must be after created_at")
	}
	return nil
}

// firstError returns the first non-nil error, so constructors read as a list of rules.
func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
```

`internal/core/domain/user.go`:

```go
package domain

import (
	"time"
	"unicode/utf8"
)

const maxProviderSubjectRunes = 255

// User is an account. It deliberately has no password hash field: only the login, register and
// reset queries read or write users.password_hash, so the hash cannot leak through a User.
type User struct {
	ID              ID
	Email           Email
	EmailVerifiedAt *time.Time // nil until the email is verified; never cleared afterwards
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// RevokeReason records why a session was revoked (sessions.revoke_reason CHECK).
type RevokeReason string

const (
	RevokeReasonLogout        RevokeReason = "logout"
	RevokeReasonPasswordReset RevokeReason = "password_reset"
)

// ParseRevokeReason accepts exactly "logout" or "password_reset".
func ParseRevokeReason(s string) (RevokeReason, error) {
	return parseEnum("revoke_reason", s, RevokeReasonLogout, RevokeReasonPasswordReset)
}

// Session is a server-side login session. Only the SHA-256 hash of its token is stored.
type Session struct {
	ID           ID
	UserID       ID
	TokenHash    []byte
	CreatedAt    time.Time
	ExpiresAt    time.Time
	RevokedAt    *time.Time
	RevokeReason RevokeReason // "" while not revoked
}

// NewSession checks a session the service is about to create: not yet revoked.
func NewSession(s Session) (Session, error) {
	err := firstError(
		requireID("session.id", s.ID),
		requireID("session.user_id", s.UserID),
		requireHash("session.token_hash", s.TokenHash),
		requireExpiry(s.CreatedAt, s.ExpiresAt),
	)
	if err == nil && (s.RevokedAt != nil || s.RevokeReason != "") {
		err = invariant("session", "must not be revoked when created")
	}
	if err != nil {
		return Session{}, err
	}
	return s, nil
}

// IdentityProviderName is an external identity provider (auth_providers.provider CHECK).
type IdentityProviderName string

const IdentityProviderGoogle IdentityProviderName = "google"

// ParseIdentityProviderName accepts exactly "google".
func ParseIdentityProviderName(s string) (IdentityProviderName, error) {
	return parseEnum("provider", s, IdentityProviderGoogle)
}

// AuthProvider links a user to an external identity. ProviderSubject is the provider's stable
// subject (Google "sub"), never the email.
type AuthProvider struct {
	ID              ID
	UserID          ID
	Provider        IdentityProviderName
	ProviderSubject string
	CreatedAt       time.Time
}

// NewAuthProvider checks a link the service is about to store.
func NewAuthProvider(p AuthProvider) (AuthProvider, error) {
	err := firstError(
		requireID("auth_provider.id", p.ID),
		requireID("auth_provider.user_id", p.UserID),
	)
	switch {
	case err != nil:
	case !isOneOf(p.Provider, IdentityProviderGoogle):
		err = invariant("auth_provider.provider", "must be google")
	case p.ProviderSubject == "" || !utf8.ValidString(p.ProviderSubject) ||
		utf8.RuneCountInString(p.ProviderSubject) > maxProviderSubjectRunes || hasControl(p.ProviderSubject):
		err = invariant("auth_provider.provider_subject", "must be 1-255 printable characters")
	case p.CreatedAt.IsZero():
		err = invariant("auth_provider.created_at", "is required")
	}
	if err != nil {
		return AuthProvider{}, err
	}
	return p, nil
}

// OIDCPurpose says what an OpenID Connect flow is for (oauth_flows.purpose CHECK).
type OIDCPurpose string

const (
	OIDCPurposeLogin OIDCPurpose = "login"
	OIDCPurposeLink  OIDCPurpose = "link"
)

// ParseOIDCPurpose accepts exactly "login" or "link".
func ParseOIDCPurpose(s string) (OIDCPurpose, error) {
	return parseEnum("purpose", s, OIDCPurposeLogin, OIDCPurposeLink)
}

// OIDCFlow is a pending Google sign-in or account link. A link flow belongs to the signed-in
// user; a login flow has no user (UserID is the zero ID).
type OIDCFlow struct {
	ID                 ID
	Purpose            OIDCPurpose
	UserID             ID
	StateHash          []byte
	NonceHash          []byte
	BrowserBindingHash []byte
	CreatedAt          time.Time
	ExpiresAt          time.Time
	ConsumedAt         *time.Time
}

// NewOIDCFlow checks a flow the service is about to store: unconsumed, with the user rule above.
func NewOIDCFlow(f OIDCFlow) (OIDCFlow, error) {
	err := firstError(
		requireID("oidc_flow.id", f.ID),
		requireHash("oidc_flow.state_hash", f.StateHash),
		requireHash("oidc_flow.nonce_hash", f.NonceHash),
		requireHash("oidc_flow.browser_binding_hash", f.BrowserBindingHash),
		requireExpiry(f.CreatedAt, f.ExpiresAt),
	)
	switch {
	case err != nil:
	case f.Purpose == OIDCPurposeLink && f.UserID.IsZero():
		err = invariant("oidc_flow.user_id", "is required for a link flow")
	case f.Purpose == OIDCPurposeLogin && !f.UserID.IsZero():
		err = invariant("oidc_flow.user_id", "must be empty for a login flow")
	case !isOneOf(f.Purpose, OIDCPurposeLogin, OIDCPurposeLink):
		err = invariant("oidc_flow.purpose", "must be login or link")
	case f.ConsumedAt != nil:
		err = invariant("oidc_flow", "must not be consumed when created")
	}
	if err != nil {
		return OIDCFlow{}, err
	}
	return f, nil
}
```

`internal/core/domain/otp.go`:

```go
package domain

import "time"

// MaxOTPFailedAttemptsCeiling is the DB CHECK ceiling on otp_challenges.failed_attempts; the
// allowed number of attempts comes from config and must stay within 1..this value.
const MaxOTPFailedAttemptsCeiling = 20

// OTPPurpose separates the two OTP flows (otp_challenges.purpose CHECK). It is part of the OTP
// HMAC so a code issued for one flow never verifies in the other.
type OTPPurpose string

const (
	OTPPurposeEmailVerification OTPPurpose = "email_verification"
	OTPPurposePasswordReset     OTPPurpose = "password_reset"
)

// ParseOTPPurpose accepts exactly "email_verification" or "password_reset".
func ParseOTPPurpose(s string) (OTPPurpose, error) {
	return parseEnum("purpose", s, OTPPurposeEmailVerification, OTPPurposePasswordReset)
}

// OTPChallenge is one issued one-time code. Only its HMAC is stored.
type OTPChallenge struct {
	ID             ID
	UserID         ID
	Purpose        OTPPurpose
	OTPHash        []byte
	CreatedAt      time.Time
	ExpiresAt      time.Time
	FailedAttempts int
	ConsumedAt     *time.Time
	InvalidatedAt  *time.Time
}

// NewOTPChallenge checks a challenge the service is about to store: fresh and open.
func NewOTPChallenge(c OTPChallenge) (OTPChallenge, error) {
	err := firstError(
		requireID("otp_challenge.id", c.ID),
		requireID("otp_challenge.user_id", c.UserID),
		requireHash("otp_challenge.otp_hash", c.OTPHash),
		requireExpiry(c.CreatedAt, c.ExpiresAt),
	)
	switch {
	case err != nil:
	case !isOneOf(c.Purpose, OTPPurposeEmailVerification, OTPPurposePasswordReset):
		err = invariant("otp_challenge.purpose", "must be email_verification or password_reset")
	case c.FailedAttempts != 0 || c.ConsumedAt != nil || c.InvalidatedAt != nil:
		err = invariant("otp_challenge", "must be open with no failed attempts when created")
	}
	if err != nil {
		return OTPChallenge{}, err
	}
	return c, nil
}
```

- [ ] **Step 4: รัน test และ lint ให้ผ่าน**

Run: `go test -race -count=1 ./internal/core/... && make lint`
Expected: `ok` และ `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/core/domain
git commit -m "feat(core): add user, session, identity, OIDC flow and OTP entities"
```

---

### Task 5: Entities ของพอร์ต — Portfolio, Instrument, Transaction

**Files:**
- Create: `internal/core/domain/instrument.go`, `transaction.go`
- Modify: `internal/core/domain/portfolio.go` (import + entity), `internal/core/domain/text.go` (เพิ่ม `isText`)
- Test: `internal/core/domain/portfolio_entities_test.go`

**Interfaces:**
- Consumes: `ID`, `Version`, `Page`, `Symbol`, `Quantity`, `Price`, `Fee`, `PortfolioName`, `PortfolioDescription` (Tasks 1–3); `parseEnum`, `isOneOf`, `invariant`, `requireID`, `hasControl` (Tasks 3–4); fixtures `t0`, `mustID` (Tasks 1, 4)
- Produces:
  - `domain.Currency` (`CurrencyUSD`), `ParseCurrency`
  - `domain.Portfolio{ID, UserID, Name PortfolioName, Description PortfolioDescription, Currency, Version, CreatedAt, UpdatedAt}`, `domain.PortfolioPage{Items []Portfolio, NextCursor string}`
  - `domain.AssetClass` (`AssetClassUSEquity`), `domain.InstrumentStatus` (`InstrumentStatusActive`, `InstrumentStatusInactive`), `domain.InstrumentSource` (`InstrumentSourceAlpaca`, `InstrumentSourceDemo`) พร้อม `Parse*`
  - `domain.Instrument{ID, Symbol, ProviderAssetID, Name, Exchange string, AssetClass, Status, Source, VerifiedAt, CreatedAt, UpdatedAt}`, `NewInstrument(Instrument) (Instrument, error)`
  - `domain.Side` (`SideBuy`, `SideSell`), `ParseSide`
  - `domain.Transaction{ID, PortfolioID, InstrumentID, Symbol, Sequence int64, Side, Quantity, Price, Fee, ExecutedAt, Version, CreatedAt, UpdatedAt}`
  - `domain.TransactionFilter{Page, Symbol *Symbol, Side *Side, From, To *time.Time}`, `NewTransactionFilter(page Page, symbol *Symbol, side *Side, from, to *time.Time) (TransactionFilter, error)`, `domain.TransactionPage{Items []Transaction, NextCursor string}`
  - unexported `isText(s string, minRunes, maxRunes int) bool`

- [ ] **Step 1: เขียน failing tests**

`internal/core/domain/portfolio_entities_test.go`:

```go
package domain_test

import (
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestPortfolioEnums(t *testing.T) {
	cases := map[string]func(string) error{
		"USD":       func(s string) error { _, err := domain.ParseCurrency(s); return err },
		"us_equity": func(s string) error { _, err := domain.ParseAssetClass(s); return err },
		"active":    func(s string) error { _, err := domain.ParseInstrumentStatus(s); return err },
		"inactive":  func(s string) error { _, err := domain.ParseInstrumentStatus(s); return err },
		"alpaca":    func(s string) error { _, err := domain.ParseInstrumentSource(s); return err },
		"demo":      func(s string) error { _, err := domain.ParseInstrumentSource(s); return err },
		"buy":       func(s string) error { _, err := domain.ParseSide(s); return err },
		"sell":      func(s string) error { _, err := domain.ParseSide(s); return err },
	}
	for value, parse := range cases {
		if err := parse(value); err != nil {
			t.Errorf("parse(%q) error = %v", value, err)
		}
		if err := parse(strings.ToUpper(value) + "x"); err == nil {
			t.Errorf("parse accepted %q", strings.ToUpper(value)+"x")
		}
	}
	_, err := domain.ParseSide("BUY")
	requireInvalidInput(t, err, "side")
	_, err = domain.ParseCurrency("usd")
	requireInvalidInput(t, err, "currency")
}

func validInstrument(t *testing.T) domain.Instrument {
	sym, err := domain.ParseSymbol("AAPL")
	if err != nil {
		t.Fatal(err)
	}
	return domain.Instrument{
		ID: mustID(t), Symbol: sym, ProviderAssetID: "b0b6dd9d-8b9b-48a9-ba46-b9d54906e415", Name: "Apple Inc. Common Stock",
		Exchange: "NASDAQ", AssetClass: domain.AssetClassUSEquity, Status: domain.InstrumentStatusActive,
		Source: domain.InstrumentSourceAlpaca, VerifiedAt: t0, CreatedAt: t0, UpdatedAt: t0,
	}
}

func TestNewInstrument(t *testing.T) {
	if _, err := domain.NewInstrument(validInstrument(t)); err != nil {
		t.Fatalf("NewInstrument(valid) error = %v", err)
	}
	demo := validInstrument(t)
	demo.Source, demo.ProviderAssetID = domain.InstrumentSourceDemo, ""
	if _, err := domain.NewInstrument(demo); err != nil {
		t.Errorf("demo instrument without provider asset id rejected: %v", err)
	}
	for name, mutate := range map[string]func(*domain.Instrument){
		"no id":                  func(i *domain.Instrument) { i.ID = domain.ID{} },
		"no symbol":              func(i *domain.Instrument) { i.Symbol = domain.Symbol{} },
		"empty name":             func(i *domain.Instrument) { i.Name = "" },
		"long name":              func(i *domain.Instrument) { i.Name = strings.Repeat("n", 201) },
		"control in name":        func(i *domain.Instrument) { i.Name = "Apple\nInc" },
		"long exchange":          func(i *domain.Instrument) { i.Exchange = strings.Repeat("x", 21) },
		"crypto":                 func(i *domain.Instrument) { i.AssetClass = "crypto" },
		"unknown status":         func(i *domain.Instrument) { i.Status = "halted" },
		"unknown source":         func(i *domain.Instrument) { i.Source = "yahoo" },
		"alpaca without asset":   func(i *domain.Instrument) { i.ProviderAssetID = "" },
		"long provider asset id": func(i *domain.Instrument) { i.ProviderAssetID = strings.Repeat("a", 65) },
		"never verified":         func(i *domain.Instrument) { i.VerifiedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			i := validInstrument(t)
			mutate(&i)
			_, err := domain.NewInstrument(i)
			requireCode(t, err, domain.CodeInternal)
		})
	}
}

func TestNewTransactionFilter(t *testing.T) {
	page, err := domain.ParsePage("", "")
	if err != nil {
		t.Fatal(err)
	}
	from, to := t0.Add(-time.Hour), t0
	f, err := domain.NewTransactionFilter(page, nil, nil, &from, &to)
	if err != nil || f.Page.Limit() != domain.DefaultPageLimit || !f.From.Equal(from) {
		t.Fatalf("NewTransactionFilter(valid) = %+v, %v", f, err)
	}
	if _, err := domain.NewTransactionFilter(page, nil, nil, &to, &to); err != nil {
		t.Errorf("from == to rejected: %v", err)
	}
	if _, err := domain.NewTransactionFilter(page, nil, nil, &from, nil); err != nil {
		t.Errorf("open-ended range rejected: %v", err)
	}
	_, err = domain.NewTransactionFilter(page, nil, nil, &to, &from)
	requireInvalidInput(t, err, "from")
	_, err = domain.NewTransactionFilter(domain.Page{}, nil, nil, nil, nil)
	requireCode(t, err, domain.CodeInternal)
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้มเหลว**

Run: `go test ./internal/core/domain/`
Expected: FAIL ด้วย `undefined: domain.ParseCurrency`, `undefined: domain.Instrument`, `undefined: domain.NewTransactionFilter` ฯลฯ

- [ ] **Step 3: แก้ `portfolio.go` และ `text.go`**

ใน `internal/core/domain/portfolio.go` แทน import block เดิมด้วย:

```go
import (
	"strings"
	"time"
	"unicode/utf8"
)
```

แล้วต่อท้ายไฟล์ด้วย:

```go
// Currency is the portfolio currency. Phase 1 supports only USD (portfolios.currency CHECK).
type Currency string

const CurrencyUSD Currency = "USD"

// ParseCurrency accepts exactly "USD".
func ParseCurrency(s string) (Currency, error) {
	return parseEnum("currency", s, CurrencyUSD)
}

// Portfolio belongs to one user. Its Version covers name and description only.
type Portfolio struct {
	ID          ID
	UserID      ID
	Name        PortfolioName
	Description PortfolioDescription
	Currency    Currency
	Version     Version
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PortfolioPage is one page of a user's portfolios, newest first. NextCursor is "" on the last page.
type PortfolioPage struct {
	Items      []Portfolio
	NextCursor string
}
```

ต่อท้าย `internal/core/domain/text.go` ด้วย:

```go
// isText reports whether s is valid UTF-8 of min..max runes without control characters.
func isText(s string, minRunes, maxRunes int) bool {
	n := utf8.RuneCountInString(s)
	return utf8.ValidString(s) && n >= minRunes && n <= maxRunes && !hasControl(s)
}
```

- [ ] **Step 4: เขียน `instrument.go` และ `transaction.go`**

`internal/core/domain/instrument.go`:

```go
package domain

import "time"

const (
	maxInstrumentNameRunes     = 200
	maxInstrumentExchangeRunes = 20
	maxProviderAssetIDRunes    = 64
)

// AssetClass is the instrument asset class (instruments.asset_class CHECK). Phase 1: US equities.
type AssetClass string

const AssetClassUSEquity AssetClass = "us_equity"

// ParseAssetClass accepts exactly "us_equity".
func ParseAssetClass(s string) (AssetClass, error) {
	return parseEnum("asset_class", s, AssetClassUSEquity)
}

// InstrumentStatus says whether the provider still lists the instrument as tradable.
type InstrumentStatus string

const (
	InstrumentStatusActive   InstrumentStatus = "active"
	InstrumentStatusInactive InstrumentStatus = "inactive"
)

// ParseInstrumentStatus accepts exactly "active" or "inactive".
func ParseInstrumentStatus(s string) (InstrumentStatus, error) {
	return parseEnum("status", s, InstrumentStatusActive, InstrumentStatusInactive)
}

// InstrumentSource is where the instrument was verified. demo rows exist only in dev and test.
type InstrumentSource string

const (
	InstrumentSourceAlpaca InstrumentSource = "alpaca"
	InstrumentSourceDemo   InstrumentSource = "demo"
)

// ParseInstrumentSource accepts exactly "alpaca" or "demo".
func ParseInstrumentSource(s string) (InstrumentSource, error) {
	return parseEnum("source", s, InstrumentSourceAlpaca, InstrumentSourceDemo)
}

// Instrument is a verified stock shared by all users. Transactions reference its ID, not its
// symbol, because symbols change (FB -> META) and can be reused.
type Instrument struct {
	ID              ID
	Symbol          Symbol
	ProviderAssetID string // required when Source is alpaca; "" otherwise allowed
	Name            string
	Exchange        string
	AssetClass      AssetClass
	Status          InstrumentStatus
	Source          InstrumentSource
	VerifiedAt      time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// NewInstrument checks an instrument built from a provider lookup before it is stored.
func NewInstrument(i Instrument) (Instrument, error) {
	err := requireID("instrument.id", i.ID)
	switch {
	case err != nil:
	case i.Symbol.IsZero():
		err = invariant("instrument.symbol", "is required")
	case !isText(i.Name, 1, maxInstrumentNameRunes):
		err = invariant("instrument.name", "must be 1-200 printable characters")
	case !isText(i.Exchange, 1, maxInstrumentExchangeRunes):
		err = invariant("instrument.exchange", "must be 1-20 printable characters")
	case !isOneOf(i.AssetClass, AssetClassUSEquity):
		err = invariant("instrument.asset_class", "must be us_equity")
	case !isOneOf(i.Status, InstrumentStatusActive, InstrumentStatusInactive):
		err = invariant("instrument.status", "must be active or inactive")
	case !isOneOf(i.Source, InstrumentSourceAlpaca, InstrumentSourceDemo):
		err = invariant("instrument.source", "must be alpaca or demo")
	case i.Source == InstrumentSourceAlpaca && i.ProviderAssetID == "":
		err = invariant("instrument.provider_asset_id", "is required for alpaca instruments")
	case i.ProviderAssetID != "" && !isText(i.ProviderAssetID, 1, maxProviderAssetIDRunes):
		err = invariant("instrument.provider_asset_id", "must be 1-64 printable characters")
	case i.VerifiedAt.IsZero():
		err = invariant("instrument.verified_at", "is required")
	}
	if err != nil {
		return Instrument{}, err
	}
	return i, nil
}
```

`internal/core/domain/transaction.go`:

```go
package domain

import "time"

// Side is the direction of a trade (transactions.side CHECK).
type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

// ParseSide accepts exactly "buy" or "sell".
func ParseSide(s string) (Side, error) {
	return parseEnum("side", s, SideBuy, SideSell)
}

// Transaction is one recorded trade. Sequence is assigned by the database, never changes and
// breaks ties when ExecutedAt is equal: replay orders by (ExecutedAt, Sequence).
type Transaction struct {
	ID           ID
	PortfolioID  ID
	InstrumentID ID
	Symbol       Symbol // the instrument's current symbol, for responses
	Sequence     int64
	Side         Side
	Quantity     Quantity
	Price        Price
	Fee          Fee
	ExecutedAt   time.Time
	Version      Version
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// TransactionFilter narrows a transaction list. Nil fields do not filter.
type TransactionFilter struct {
	Page   Page
	Symbol *Symbol
	Side   *Side
	From   *time.Time
	To     *time.Time
}

// NewTransactionFilter checks the filter a client asked for: From must not be after To.
func NewTransactionFilter(page Page, symbol *Symbol, side *Side, from, to *time.Time) (TransactionFilter, error) {
	if page.IsZero() {
		return TransactionFilter{}, invariant("filter.page", "is required")
	}
	if from != nil && to != nil && from.After(*to) {
		return TransactionFilter{}, invalidInput("from", "must not be after to")
	}
	return TransactionFilter{Page: page, Symbol: symbol, Side: side, From: from, To: to}, nil
}

// TransactionPage is one page of a portfolio's transactions, newest first.
type TransactionPage struct {
	Items      []Transaction
	NextCursor string
}
```

- [ ] **Step 5: รัน test และ lint ให้ผ่าน**

Run: `go test -race -count=1 ./internal/core/... && make lint`
Expected: `ok` และ `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add internal/core/domain
git commit -m "feat(core): add portfolio, instrument and transaction entities"
```

---

### Task 6: Idempotency, audit และ rate limit

**Files:**
- Create: `internal/core/domain/idempotency.go`, `audit.go`, `ratelimit.go`
- Test: `internal/core/domain/records_test.go`

**Interfaces:**
- Consumes: `ID`, `invalidInput`, `invariant`, `parseEnum`, `isOneOf`, `requireID`, `requireHash`, `requireExpiry`, `firstError`, `isText` (Tasks 1, 4, 5); fixtures `t0`, `hash32`, `hash31`, `mustID`
- Produces:
  - `domain.IdempotencyOperation` (`IdempotencyOperationCreateTransaction` = `"transactions.create"`), `ParseIdempotencyOperation`
  - `domain.IdempotencyScope{UserID, PortfolioID ID, Operation}`
  - `func domain.ParseIdempotencyKey(s string) (string, error)` — 1–128 ตัว visible ASCII; คืน string เพราะ port รับ `string` ตาม spec
  - `domain.IdempotencyRecord{Scope, Key string, RequestHash []byte, Result json.RawMessage, CreatedAt, ExpiresAt}`, `NewIdempotencyRecord(IdempotencyRecord) (IdempotencyRecord, error)`, `const MaxIdempotencyResultBytes = 16384`
  - `domain.AuditOutcome` (`AuditOutcomeSuccess`, `AuditOutcomeFailure`, `AuditOutcomeDenied`), `domain.EventType` + ค่าคงที่ `Event*` 21 ตัว, `func domain.EventTypes() []EventType`
  - `domain.AuditEvent{ID, OccurredAt, EventType, ActorUserID, RequestID, SessionID ID, TargetType, TargetID string, Outcome, ReasonCode string, Metadata json.RawMessage}`, `NewAuditEvent(AuditEvent) (AuditEvent, error)` (nil metadata → `{}`), `const MaxAuditMetadataBytes = 8192`
  - `domain.RateLimitScope` + ค่าคงที่ `RateLimit*` 11 ตัว, `func domain.RateLimitScopes() []RateLimitScope`
  - `domain.RateLimitRule{Scope, KeyHash []byte, Limit int, Window time.Duration}`, `NewRateLimitRule(RateLimitRule) (RateLimitRule, error)`, `domain.RateLimitDecision{Allowed bool, RetryAfter time.Duration, RecordDenialAudit bool}`

- [ ] **Step 1: เขียน failing tests**

`internal/core/domain/records_test.go`:

```go
package domain_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestParseIdempotencyKey(t *testing.T) {
	for _, k := range []string{"a", "0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d", strings.Repeat("k", 128), "!~"} {
		if got, err := domain.ParseIdempotencyKey(k); err != nil || got != k {
			t.Errorf("ParseIdempotencyKey(%q) = %q, %v", k, got, err)
		}
	}
	for _, k := range []string{"", strings.Repeat("k", 129), "has space", "tab\t", "ключ", "nul\x00"} {
		_, err := domain.ParseIdempotencyKey(k)
		requireInvalidInput(t, err, "Idempotency-Key")
	}
}

func validRecord(t *testing.T) domain.IdempotencyRecord {
	return domain.IdempotencyRecord{
		Scope:       domain.IdempotencyScope{UserID: mustID(t), PortfolioID: mustID(t), Operation: domain.IdempotencyOperationCreateTransaction},
		Key:         "retry-1",
		RequestHash: hash32,
		Result:      json.RawMessage(`{"id":"x"}`),
		CreatedAt:   t0,
		ExpiresAt:   t0.Add(24 * time.Hour),
	}
}

func TestNewIdempotencyRecord(t *testing.T) {
	if _, err := domain.NewIdempotencyRecord(validRecord(t)); err != nil {
		t.Fatalf("NewIdempotencyRecord(valid) error = %v", err)
	}
	big := validRecord(t)
	big.Result = json.RawMessage(`"` + strings.Repeat("a", domain.MaxIdempotencyResultBytes-2) + `"`)
	if _, err := domain.NewIdempotencyRecord(big); err != nil {
		t.Errorf("16 KiB result rejected: %v", err)
	}
	for name, mutate := range map[string]func(*domain.IdempotencyRecord){
		"no portfolio":  func(r *domain.IdempotencyRecord) { r.Scope.PortfolioID = domain.ID{} },
		"bad operation": func(r *domain.IdempotencyRecord) { r.Scope.Operation = "portfolios.create" },
		"empty key":     func(r *domain.IdempotencyRecord) { r.Key = "" },
		"short hash":    func(r *domain.IdempotencyRecord) { r.RequestHash = hash31 },
		"invalid json":  func(r *domain.IdempotencyRecord) { r.Result = json.RawMessage(`{"id":`) },
		"result too big": func(r *domain.IdempotencyRecord) {
			r.Result = json.RawMessage(`"` + strings.Repeat("a", domain.MaxIdempotencyResultBytes) + `"`)
		},
		"no expiry": func(r *domain.IdempotencyRecord) { r.ExpiresAt = r.CreatedAt },
	} {
		t.Run(name, func(t *testing.T) {
			r := validRecord(t)
			mutate(&r)
			_, err := domain.NewIdempotencyRecord(r)
			requireCode(t, err, domain.CodeInternal)
		})
	}
}

func validAudit(t *testing.T) domain.AuditEvent {
	return domain.AuditEvent{
		ID: mustID(t), OccurredAt: t0, EventType: domain.EventPortfolioCreated, ActorUserID: mustID(t),
		RequestID: mustID(t), TargetType: "portfolio", TargetID: "0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d",
		Outcome: domain.AuditOutcomeSuccess, Metadata: json.RawMessage(`{"name":"Long term"}`),
	}
}

func TestNewAuditEvent(t *testing.T) {
	e, err := domain.NewAuditEvent(validAudit(t))
	if err != nil {
		t.Fatalf("NewAuditEvent(valid) error = %v", err)
	}
	if string(e.Metadata) != `{"name":"Long term"}` {
		t.Errorf("metadata changed: %s", e.Metadata)
	}

	anon := validAudit(t)
	anon.EventType, anon.ActorUserID, anon.Outcome, anon.ReasonCode, anon.Metadata =
		domain.EventAuthLoginFailed, domain.ID{}, domain.AuditOutcomeFailure, "invalid_credentials", nil
	e, err = domain.NewAuditEvent(anon)
	if err != nil {
		t.Fatalf("failed login without actor rejected: %v", err)
	}
	if string(e.Metadata) != "{}" {
		t.Errorf("nil metadata = %s, want {}", e.Metadata)
	}

	big := validAudit(t)
	big.Metadata = json.RawMessage(`{"a":"` + strings.Repeat("x", domain.MaxAuditMetadataBytes-8) + `"}`)
	if _, err := domain.NewAuditEvent(big); err != nil {
		t.Errorf("8 KiB metadata rejected: %v", err)
	}

	for name, mutate := range map[string]func(*domain.AuditEvent){
		"no request id":     func(e *domain.AuditEvent) { e.RequestID = domain.ID{} },
		"no occurred_at":    func(e *domain.AuditEvent) { e.OccurredAt = time.Time{} },
		"unknown event":     func(e *domain.AuditEvent) { e.EventType = "portfolio.viewed" },
		"unknown outcome":   func(e *domain.AuditEvent) { e.Outcome = "ok" },
		"empty target type": func(e *domain.AuditEvent) { e.TargetType = "" },
		"long target type":  func(e *domain.AuditEvent) { e.TargetType = strings.Repeat("t", 41) },
		"long reason code":  func(e *domain.AuditEvent) { e.ReasonCode = strings.Repeat("r", 81) },
		"metadata array":    func(e *domain.AuditEvent) { e.Metadata = json.RawMessage(`["a"]`) },
		"metadata invalid":  func(e *domain.AuditEvent) { e.Metadata = json.RawMessage(`{"a":`) },
		"metadata too big": func(e *domain.AuditEvent) {
			e.Metadata = json.RawMessage(`{"a":"` + strings.Repeat("x", domain.MaxAuditMetadataBytes) + `"}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := validAudit(t)
			mutate(&e)
			_, err := domain.NewAuditEvent(e)
			requireCode(t, err, domain.CodeInternal)
		})
	}
}

func TestAuditEventTypesMatchAgentsList(t *testing.T) {
	types := domain.EventTypes()
	if len(types) != 21 {
		t.Fatalf("EventTypes() has %d types, AGENTS.md lists 21", len(types))
	}
	seen := map[domain.EventType]bool{}
	for _, et := range types {
		if seen[et] {
			t.Errorf("duplicate event type %q", et)
		}
		seen[et] = true
		if len(et) > 80 {
			t.Errorf("event type %q is longer than varchar(80)", et)
		}
	}
}

func TestNewRateLimitRule(t *testing.T) {
	valid := func() domain.RateLimitRule {
		return domain.RateLimitRule{Scope: domain.RateLimitLoginEmail, KeyHash: hash32, Limit: 5, Window: time.Hour}
	}
	if _, err := domain.NewRateLimitRule(valid()); err != nil {
		t.Fatalf("NewRateLimitRule(valid) error = %v", err)
	}
	if n := len(domain.RateLimitScopes()); n != 11 {
		t.Errorf("RateLimitScopes() has %d scopes, spec section 7 lists 11", n)
	}
	for name, mutate := range map[string]func(*domain.RateLimitRule){
		"unknown scope": func(r *domain.RateLimitRule) { r.Scope = "login" },
		"short hash":    func(r *domain.RateLimitRule) { r.KeyHash = hash31 },
		"zero limit":    func(r *domain.RateLimitRule) { r.Limit = 0 },
		"zero window":   func(r *domain.RateLimitRule) { r.Window = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			r := valid()
			mutate(&r)
			_, err := domain.NewRateLimitRule(r)
			requireCode(t, err, domain.CodeInternal)
		})
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้มเหลว**

Run: `go test ./internal/core/domain/`
Expected: FAIL ด้วย `undefined: domain.ParseIdempotencyKey`, `undefined: domain.AuditEvent`, `undefined: domain.RateLimitRule` ฯลฯ

- [ ] **Step 3: เขียนโค้ด**

`internal/core/domain/idempotency.go`:

```go
package domain

import (
	"encoding/json"
	"time"
	"unicode/utf8"
)

const (
	maxIdempotencyKeyBytes    = 128
	MaxIdempotencyResultBytes = 16 * 1024
)

// IdempotencyOperation names the operation a key protects (idempotency_keys.operation CHECK).
type IdempotencyOperation string

const IdempotencyOperationCreateTransaction IdempotencyOperation = "transactions.create"

// ParseIdempotencyOperation accepts exactly "transactions.create".
func ParseIdempotencyOperation(s string) (IdempotencyOperation, error) {
	return parseEnum("operation", s, IdempotencyOperationCreateTransaction)
}

// IdempotencyScope binds a key to one user, portfolio and operation.
type IdempotencyScope struct {
	UserID      ID
	PortfolioID ID
	Operation   IdempotencyOperation
}

// ParseIdempotencyKey checks the Idempotency-Key header: 1-128 visible ASCII characters.
// It returns the key unchanged because repositories take it as a string (spec section 8).
func ParseIdempotencyKey(s string) (string, error) {
	if !isVisibleASCII(s, maxIdempotencyKeyBytes) {
		return "", invalidInput("Idempotency-Key", "must be 1-128 visible ASCII characters")
	}
	return s, nil
}

// IdempotencyRecord stores the result of the first request made with a key.
type IdempotencyRecord struct {
	Scope       IdempotencyScope
	Key         string
	RequestHash []byte
	Result      json.RawMessage
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// NewIdempotencyRecord checks a record the service is about to save.
func NewIdempotencyRecord(r IdempotencyRecord) (IdempotencyRecord, error) {
	err := firstError(
		requireID("idempotency.user_id", r.Scope.UserID),
		requireID("idempotency.portfolio_id", r.Scope.PortfolioID),
		requireHash("idempotency.request_hash", r.RequestHash),
		requireExpiry(r.CreatedAt, r.ExpiresAt),
	)
	switch {
	case err != nil:
	case !isOneOf(r.Scope.Operation, IdempotencyOperationCreateTransaction):
		err = invariant("idempotency.operation", "must be transactions.create")
	case !isVisibleASCII(r.Key, maxIdempotencyKeyBytes):
		err = invariant("idempotency.key", "must be 1-128 visible ASCII characters")
	case len(r.Result) > MaxIdempotencyResultBytes || !json.Valid(r.Result):
		err = invariant("idempotency.result", "must be valid JSON of at most 16 KiB")
	}
	if err != nil {
		return IdempotencyRecord{}, err
	}
	return r, nil
}

// isVisibleASCII reports whether s is 1..maxBytes characters in '!'..'~'.
func isVisibleASCII(s string, maxBytes int) bool {
	if s == "" || len(s) > maxBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '!' || s[i] > '~' {
			return false
		}
	}
	return utf8.ValidString(s)
}
```

`internal/core/domain/audit.go`:

```go
package domain

import (
	"bytes"
	"encoding/json"
	"time"
)

const (
	MaxAuditMetadataBytes   = 8 * 1024
	maxAuditTargetTypeRunes = 40
	maxAuditReasonCodeRunes = 80
)

// AuditOutcome is the result an audit event records (audit_events.outcome CHECK).
type AuditOutcome string

const (
	AuditOutcomeSuccess AuditOutcome = "success"
	AuditOutcomeFailure AuditOutcome = "failure"
	AuditOutcomeDenied  AuditOutcome = "denied"
)

// EventType is one of the phase 1 audit events listed in AGENTS.md.
type EventType string

const (
	EventAuthRegistered                 EventType = "auth.registered"
	EventAuthLoginSucceeded             EventType = "auth.login_succeeded"
	EventAuthLoginFailed                EventType = "auth.login_failed"
	EventAuthEmailVerificationRequested EventType = "auth.email_verification_requested"
	EventAuthEmailVerified              EventType = "auth.email_verified"
	EventAuthEmailVerificationFailed    EventType = "auth.email_verification_failed"
	EventAuthLogout                     EventType = "auth.logout"
	EventAuthSessionsRevoked            EventType = "auth.sessions_revoked"
	EventAuthPasswordResetRequested     EventType = "auth.password_reset_requested"
	EventAuthOTPVerificationFailed      EventType = "auth.otp_verification_failed"
	EventAuthPasswordResetSucceeded     EventType = "auth.password_reset_succeeded"
	EventAuthIdentityLinked             EventType = "auth.identity_linked"
	EventAuthIdentityLinkFailed         EventType = "auth.identity_link_failed"
	EventPortfolioCreated               EventType = "portfolio.created"
	EventPortfolioUpdated               EventType = "portfolio.updated"
	EventPortfolioDeleted               EventType = "portfolio.deleted"
	EventTransactionCreated             EventType = "transaction.created"
	EventTransactionUpdated             EventType = "transaction.updated"
	EventTransactionDeleted             EventType = "transaction.deleted"
	EventSecurityAccessDenied           EventType = "security.access_denied"
	EventSecurityRateLimited            EventType = "security.rate_limited"
)

// EventTypes lists every audit event type, for tests and the DB enum check in milestone 3.
func EventTypes() []EventType {
	return []EventType{
		EventAuthRegistered, EventAuthLoginSucceeded, EventAuthLoginFailed,
		EventAuthEmailVerificationRequested, EventAuthEmailVerified, EventAuthEmailVerificationFailed,
		EventAuthLogout, EventAuthSessionsRevoked,
		EventAuthPasswordResetRequested, EventAuthOTPVerificationFailed, EventAuthPasswordResetSucceeded,
		EventAuthIdentityLinked, EventAuthIdentityLinkFailed,
		EventPortfolioCreated, EventPortfolioUpdated, EventPortfolioDeleted,
		EventTransactionCreated, EventTransactionUpdated, EventTransactionDeleted,
		EventSecurityAccessDenied, EventSecurityRateLimited,
	}
}

// AuditEvent is an append-only record of something a use case did. Optional IDs use the zero
// ID; optional strings use "". Metadata must be a JSON object; nil means "{}".
type AuditEvent struct {
	ID          ID
	OccurredAt  time.Time
	EventType   EventType
	ActorUserID ID // zero when the actor is not authenticated, e.g. a failed login
	RequestID   ID
	SessionID   ID // internal session ID, never the session token or its hash
	TargetType  string
	TargetID    string
	Outcome     AuditOutcome
	ReasonCode  string
	Metadata    json.RawMessage
}

// NewAuditEvent checks an event before it is appended and replaces nil metadata with "{}".
func NewAuditEvent(e AuditEvent) (AuditEvent, error) {
	if e.Metadata == nil {
		e.Metadata = json.RawMessage("{}")
	}
	err := firstError(
		requireID("audit.id", e.ID),
		requireID("audit.request_id", e.RequestID),
	)
	switch {
	case err != nil:
	case e.OccurredAt.IsZero():
		err = invariant("audit.occurred_at", "is required")
	case !isOneOf(e.EventType, EventTypes()...):
		err = invariant("audit.event_type", "must be a phase 1 event type")
	case !isOneOf(e.Outcome, AuditOutcomeSuccess, AuditOutcomeFailure, AuditOutcomeDenied):
		err = invariant("audit.outcome", "must be success, failure or denied")
	case !isText(e.TargetType, 1, maxAuditTargetTypeRunes):
		err = invariant("audit.target_type", "must be 1-40 printable characters")
	case e.ReasonCode != "" && !isText(e.ReasonCode, 1, maxAuditReasonCodeRunes):
		err = invariant("audit.reason_code", "must be at most 80 printable characters")
	case len(e.Metadata) > MaxAuditMetadataBytes || !isJSONObject(e.Metadata):
		err = invariant("audit.metadata", "must be a JSON object of at most 8 KiB")
	}
	if err != nil {
		return AuditEvent{}, err
	}
	return e, nil
}

func isJSONObject(b json.RawMessage) bool {
	t := bytes.TrimSpace(b)
	return len(t) > 0 && t[0] == '{' && json.Valid(t)
}
```

`internal/core/domain/ratelimit.go`:

```go
package domain

import "time"

// RateLimitScope is what a rate-limit key counts (rate_limit_keys.scope CHECK).
type RateLimitScope string

const (
	RateLimitLoginEmail                    RateLimitScope = "login_email"
	RateLimitLoginIP                       RateLimitScope = "login_ip"
	RateLimitSignupIP                      RateLimitScope = "signup_ip"
	RateLimitGoogleStartIP                 RateLimitScope = "google_start_ip"
	RateLimitEmailVerificationRequestEmail RateLimitScope = "email_verification_request_email"
	RateLimitEmailVerificationRequestIP    RateLimitScope = "email_verification_request_ip"
	RateLimitEmailVerificationConfirmIP    RateLimitScope = "email_verification_confirm_ip"
	RateLimitPasswordResetRequestEmail     RateLimitScope = "password_reset_request_email"
	RateLimitPasswordResetRequestIP        RateLimitScope = "password_reset_request_ip"
	RateLimitPasswordResetConfirmIP        RateLimitScope = "password_reset_confirm_ip"
	RateLimitInstrumentLookupUser          RateLimitScope = "instrument_lookup_user"
)

// RateLimitScopes lists every scope, for tests and the DB CHECK in milestone 3.
func RateLimitScopes() []RateLimitScope {
	return []RateLimitScope{
		RateLimitLoginEmail, RateLimitLoginIP, RateLimitSignupIP, RateLimitGoogleStartIP,
		RateLimitEmailVerificationRequestEmail, RateLimitEmailVerificationRequestIP, RateLimitEmailVerificationConfirmIP,
		RateLimitPasswordResetRequestEmail, RateLimitPasswordResetRequestIP, RateLimitPasswordResetConfirmIP,
		RateLimitInstrumentLookupUser,
	}
}

// RateLimitRule allows at most Limit events per Window for one hashed key.
type RateLimitRule struct {
	Scope   RateLimitScope
	KeyHash []byte
	Limit   int
	Window  time.Duration
}

// NewRateLimitRule checks a rule built from config.
func NewRateLimitRule(r RateLimitRule) (RateLimitRule, error) {
	err := requireHash("rate_limit.key_hash", r.KeyHash)
	switch {
	case err != nil:
	case !isOneOf(r.Scope, RateLimitScopes()...):
		err = invariant("rate_limit.scope", "must be a known scope")
	case r.Limit < 1:
		err = invariant("rate_limit.limit", "must be at least 1")
	case r.Window <= 0:
		err = invariant("rate_limit.window", "must be positive")
	}
	if err != nil {
		return RateLimitRule{}, err
	}
	return r, nil
}

// RateLimitDecision is the result of admitting one request against a set of rules.
type RateLimitDecision struct {
	Allowed           bool
	RetryAfter        time.Duration
	RecordDenialAudit bool // true at most once per key per window, so attacks cannot flood audit
}
```

- [ ] **Step 4: รัน test และ lint ให้ผ่าน**

Run: `go test -race -count=1 ./internal/core/... && make lint`
Expected: `ok` และ `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/core/domain
git commit -m "feat(core): add idempotency, audit event and rate limit types"
```

---

### Task 7: Ports และ test บังคับขอบเขต dependency ของ core

**Files:**
- Create: `internal/core/port/clock.go`, `uow.go`, `auth.go`, `portfolio.go`, `audit.go`
- Test: `internal/core/boundary_test.go`

**Interfaces:**
- Consumes: ชนิดทั้งหมดจาก Tasks 1–6
- Produces (signature ตาม phase 1 spec หัวข้อ 8 ยกเว้น 4 จุดที่ใช้ชนิดที่ตรวจแล้ว):
  - `port.Clock{ Now() time.Time }`
  - `port.UnitOfWork{ WithinTransaction(ctx, fn func(TxRepositories) error) error }`
  - `port.TxRepositories{ Users(); Sessions(); Identities(); OTPChallenges(); Portfolios(); Transactions(); Idempotency(); Instruments(); Audit() }`
  - `port.UserRepository` (`ByEmail(ctx, domain.Email)`), `port.SessionRepository` (`Revoke`/`RevokeAll` รับ `domain.RevokeReason`), `port.IdentityRepository` (`BySubject(ctx, domain.IdentityProviderName, string)`), `port.OIDCFlowRepository`, `port.OTPChallengeRepository`
  - `port.PortfolioRepository`, `port.TransactionRepository`, `port.InstrumentRepository` (`BySymbol(ctx, domain.Symbol)`), `port.IdempotencyRepository`
  - `port.AuditRepository`, `port.RateLimitRepository`
  - ไม่สร้าง service ports และ provider ports (`AuthService`, `PriceProvider`, `Mailer` ฯลฯ) ใน milestone นี้

Ports เป็น interface ล้วนจึงไม่มี behavior ให้ test โดยตรง: compiler ตรวจว่า signature อ้างชนิดที่มีอยู่จริง และ boundary test ตรวจว่า core ไม่ import อะไรนอก allowlist ซึ่ง test นี้ผ่านทันทีที่เขียน (core ปัจจุบันสะอาด) จึงต้องพิสูจน์ด้วยไฟล์ละเมิดชั่วคราวใน Step 3

- [ ] **Step 1: เขียน boundary test**

`internal/core/boundary_test.go`:

```go
package core_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// allowedThirdParty are the only non-standard-library imports the core may use (AGENTS.md:
// the core knows nothing of Fiber, pgx, sqlc, Alpaca or Supabase).
var allowedThirdParty = map[string]bool{
	"github.com/shopspring/decimal": true,
	"github.com/google/uuid":        true,
}

const corePrefix = "go-backend-stock/internal/core/"

// TestCoreImportsOnlyAllowedPackages parses every .go file under internal/core, tests included,
// and fails on any import outside the standard library, the allowlist and the core itself.
func TestCoreImportsOnlyAllowedPackages(t *testing.T) {
	fset := token.NewFileSet()
	files := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		files++
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if !allowed(p) {
				t.Errorf("%s imports %q: the core may import only the standard library, %v and %s*", path, p, keys(allowedThirdParty), corePrefix)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/core: %v", err)
	}
	if files < 10 {
		t.Fatalf("checked only %d files; is the test running from internal/core?", files)
	}
}

func allowed(p string) bool {
	if allowedThirdParty[p] || strings.HasPrefix(p, corePrefix) {
		return true
	}
	// Standard library paths have no dot in their first element (e.g. "net/http", not "github.com/x").
	first, _, _ := strings.Cut(p, "/")
	return !strings.Contains(first, ".")
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
```

- [ ] **Step 2: รัน test ให้ผ่านกับ core ปัจจุบัน**

Run: `go test -count=1 ./internal/core/`
Expected: `ok  	go-backend-stock/internal/core`

- [ ] **Step 3: พิสูจน์ว่า test จับการละเมิดได้ (RED)**

```bash
mkdir -p internal/core/port
printf 'package port\n\nimport _ "github.com/gofiber/fiber/v3"\n' > internal/core/port/zz_violation.go
go test -count=1 -run TestCoreImportsOnlyAllowedPackages ./internal/core/
```

Expected: FAIL ด้วย `port/zz_violation.go imports "github.com/gofiber/fiber/v3": the core may import only ...`

```bash
rm internal/core/port/zz_violation.go
```

- [ ] **Step 4: เขียน ports**

`internal/core/port/clock.go`:

```go
// Package port declares the boundaries the core owns. Adapters implement these interfaces;
// the core never imports an adapter. Service and provider ports are added in the milestone
// that first implements them (spec 2026-10-16-milestone-2-domain-ports-design.md section 5).
package port

import "time"

// Clock is the core's only source of the current time, so tests can fix it.
type Clock interface {
	Now() time.Time
}
```

`internal/core/port/uow.go`:

```go
package port

import "context"

// UnitOfWork runs fn inside one database transaction. The transaction commits only if fn
// returns nil, so a business change and its audit event commit or roll back together.
// fn must not start a nested transaction.
type UnitOfWork interface {
	WithinTransaction(ctx context.Context, fn func(TxRepositories) error) error
}

// TxRepositories are the repositories bound to the current transaction. Lock* methods are only
// valid on these instances.
type TxRepositories interface {
	Users() UserRepository
	Sessions() SessionRepository
	Identities() IdentityRepository
	OTPChallenges() OTPChallengeRepository // otp_challenges for both purposes
	Portfolios() PortfolioRepository
	Transactions() TransactionRepository
	Idempotency() IdempotencyRepository
	Instruments() InstrumentRepository
	Audit() AuditRepository
}
```

`internal/core/port/auth.go`:

```go
package port

import (
	"context"
	"time"

	"go-backend-stock/internal/core/domain"
)

// UserRepository stores accounts. Not-found results return domain.ErrNotFound.
type UserRepository interface {
	ByID(ctx context.Context, id domain.ID) (domain.User, error)
	ByEmail(ctx context.Context, email domain.Email) (domain.User, error)
	// Lock reads the user with a row lock; transaction-bound instances only.
	Lock(ctx context.Context, id domain.ID) (domain.User, error)
	Insert(ctx context.Context, user domain.User) error
	SetPassword(ctx context.Context, id domain.ID, passwordHash string, at time.Time) error
	// MarkEmailVerified is a no-op when the email is already verified.
	MarkEmailVerified(ctx context.Context, id domain.ID, at time.Time) error
}

// SessionRepository stores server-side sessions by token hash.
type SessionRepository interface {
	ByTokenHash(ctx context.Context, tokenHash []byte) (domain.Session, error)
	Insert(ctx context.Context, session domain.Session) error
	// Revoke reports false when the session was already revoked, so logout audits only once.
	Revoke(ctx context.Context, id domain.ID, at time.Time, reason domain.RevokeReason) (bool, error)
	// RevokeAll revokes every active session of userID and returns how many it revoked.
	RevokeAll(ctx context.Context, userID domain.ID, at time.Time, reason domain.RevokeReason) (int64, error)
}

// IdentityRepository stores links to external identities, unique per (provider, subject).
type IdentityRepository interface {
	BySubject(ctx context.Context, provider domain.IdentityProviderName, subject string) (domain.AuthProvider, error)
	Insert(ctx context.Context, link domain.AuthProvider) error
}

// OIDCFlowRepository stores pending OpenID Connect flows.
type OIDCFlowRepository interface {
	Insert(ctx context.Context, flow domain.OIDCFlow) error
	// Consume atomically marks the flow matching both hashes as consumed if it is unexpired and
	// unconsumed at `at`; a second call for the same flow returns domain.ErrNotFound.
	Consume(ctx context.Context, stateHash, browserBindingHash []byte, at time.Time) (domain.OIDCFlow, error)
}

// OTPChallengeRepository stores OTP challenges. Every method takes the purpose so a challenge
// ID from one flow can never be used in the other.
type OTPChallengeRepository interface {
	ByID(ctx context.Context, purpose domain.OTPPurpose, id domain.ID) (domain.OTPChallenge, error)
	Lock(ctx context.Context, purpose domain.OTPPurpose, id domain.ID) (domain.OTPChallenge, error)
	// InvalidateOpen invalidates userID's open challenges of this purpose, expired ones included.
	InvalidateOpen(ctx context.Context, userID domain.ID, purpose domain.OTPPurpose, at time.Time) error
	Insert(ctx context.Context, challenge domain.OTPChallenge) error
	RecordFailure(ctx context.Context, purpose domain.OTPPurpose, id domain.ID) error
	Consume(ctx context.Context, purpose domain.OTPPurpose, id domain.ID, at time.Time) error
}
```

`internal/core/port/portfolio.go`:

```go
package port

import (
	"context"
	"time"

	"go-backend-stock/internal/core/domain"
)

// ID arguments are ordered ownerID, portfolioID, transactionID. Every query filters by the
// owner, so another user's portfolio reads as domain.ErrNotFound.

// PortfolioRepository stores portfolios scoped to their owner.
type PortfolioRepository interface {
	Insert(ctx context.Context, portfolio domain.Portfolio) error
	GetOwned(ctx context.Context, ownerID, portfolioID domain.ID) (domain.Portfolio, error)
	// LockOwned takes the portfolio row lock that serializes every change inside the portfolio.
	LockOwned(ctx context.Context, ownerID, portfolioID domain.ID) (domain.Portfolio, error)
	ListOwned(ctx context.Context, ownerID domain.ID, page domain.Page) (domain.PortfolioPage, error)
	// UpdateOwned writes name and description when portfolio.Version still matches and returns
	// the row with version + 1; a mismatch returns domain.ErrVersionConflict.
	UpdateOwned(ctx context.Context, portfolio domain.Portfolio, ownerID domain.ID) (domain.Portfolio, error)
	// DeleteOwnedEmpty returns domain.ErrPortfolioNotEmpty while transactions remain.
	DeleteOwnedEmpty(ctx context.Context, ownerID, portfolioID domain.ID) error
}

// TransactionRepository stores trades scoped to their owner and portfolio.
type TransactionRepository interface {
	// History returns the whole portfolio history ordered by (executed_at, sequence) for replay.
	History(ctx context.Context, ownerID, portfolioID domain.ID) ([]domain.Transaction, error)
	List(ctx context.Context, ownerID, portfolioID domain.ID, filter domain.TransactionFilter) (domain.TransactionPage, error)
	Get(ctx context.Context, ownerID, portfolioID, transactionID domain.ID) (domain.Transaction, error)
	// Insert returns the stored row, including the database-assigned sequence.
	Insert(ctx context.Context, ownerID domain.ID, transaction domain.Transaction) (domain.Transaction, error)
	// Update returns the row with version + 1; a version mismatch returns domain.ErrVersionConflict.
	Update(ctx context.Context, ownerID domain.ID, transaction domain.Transaction) (domain.Transaction, error)
	Delete(ctx context.Context, ownerID, portfolioID, transactionID domain.ID) error
}

// InstrumentRepository stores verified instruments shared by all users.
type InstrumentRepository interface {
	BySymbol(ctx context.Context, symbol domain.Symbol) (domain.Instrument, error)
	// Upsert returns the stored row; when a concurrent request inserted the symbol first it
	// returns that existing row.
	Upsert(ctx context.Context, instrument domain.Instrument) (domain.Instrument, error)
}

// IdempotencyRepository stores the first result of each idempotent request.
type IdempotencyRepository interface {
	Get(ctx context.Context, scope domain.IdempotencyScope, key string) (domain.IdempotencyRecord, error)
	DeleteExpired(ctx context.Context, scope domain.IdempotencyScope, key string, now time.Time) error
	Save(ctx context.Context, record domain.IdempotencyRecord) error
	DeleteForPortfolio(ctx context.Context, ownerID, portfolioID domain.ID) error
}
```

`internal/core/port/audit.go`:

```go
package port

import (
	"context"
	"time"

	"go-backend-stock/internal/core/domain"
)

// AuditRepository appends audit events. There is no update or delete: audit is append-only for
// the runtime role.
type AuditRepository interface {
	Append(ctx context.Context, event domain.AuditEvent) error
}

// RateLimitRepository counts attempts in PostgreSQL so a restart cannot reset quotas.
type RateLimitRepository interface {
	// Admit atomically records one attempt against every rule and reports whether all allow it.
	Admit(ctx context.Context, rules []domain.RateLimitRule, now time.Time) (domain.RateLimitDecision, error)
}
```

- [ ] **Step 5: รัน test ทั้งหมด, vet และ lint ให้ผ่าน**

Run: `ls internal/core/port && go test -race -count=1 ./internal/core/... && go vet ./... && make lint`
Expected: มีไฟล์ `audit.go auth.go clock.go portfolio.go uow.go` เท่านั้น (ไม่มี `zz_violation.go`), `ok` ทั้ง `internal/core` และ `internal/core/domain`, `?   	go-backend-stock/internal/core/port	[no test files]` และ `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add internal/core
git commit -m "feat(core): add repository ports, unit of work, clock and import boundary test"
```

---

### Task 8: เอกสาร — ADR-0013, phase 1 spec หัวข้อ 8 และ roadmap

**Files:**
- Create: `docs/adr/0013-domain-value-types.md`
- Modify: `docs/adr/README.md`, `docs/superpowers/specs/2026-10-05-phase-1-design.md` (หัวข้อ 8), `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md` (milestone 2), `docs/superpowers/specs/2026-10-16-milestone-2-domain-ports-design.md` (สถานะ)

**Interfaces:**
- Consumes: ชื่อชนิดและ signatures จาก Tasks 1–7
- Produces: เอกสารที่ตรงกับโค้ด (AGENTS.md: spec และ roadmap ต้องอัปเดตในชุดงานเดียวกัน)

- [ ] **Step 1: เขียน `docs/adr/0013-domain-value-types.md`**

```markdown
# ADR-0013: Domain value types แบบ parse, don't validate และชนิดตัวเลขแยกตามความหมาย

- **สถานะ:** Accepted
- **วันที่:** 2026-10-16
- **เกี่ยวข้อง:** [milestone 2 design](../superpowers/specs/2026-10-16-milestone-2-domain-ports-design.md), [ADR-0003](0003-decimal-and-event-replay.md), phase 1 spec หัวข้อ 8

## บริบท
core ต้องรับ quantity/price/fee, symbol, email และเวลาที่ผู้ใช้ส่งมา และส่งต่อให้ service/repository หลาย milestone ถ้าตรวจค่าแบบกระจาย จะมีจุดที่ลืมตรวจ และ quantity กับ price ที่เป็น decimal เหมือนกันสลับตำแหน่งกันได้โดย compiler ไม่เตือน

## ทางเลือกที่พิจารณา
1. struct ใช้ชนิดพื้นฐาน + เมธอด `Validate()`: เขียนง่าย แต่ลืมเรียกได้ และค่าที่ตรวจแล้วกับยังไม่ตรวจหน้าตาเหมือนกัน
2. ตรวจใน service อย่างเดียว: domain เป็นแค่ข้อมูล กติกากระจายทุก service
3. parse, don't validate: ค่าดิบแปลงเป็นชนิดที่ตรวจแล้วผ่าน `Parse*` เท่านั้น และตัวเลขแยกชนิด `Quantity`/`Price`/`Fee`

## การตัดสินใจ
ใช้ทางเลือก 3: ชนิดเก็บค่าใน field ที่ไม่ export; `Parse*` รับค่าจากภายนอกและคืน `invalid_input`; `New*` ตรวจค่าที่ server สร้างและคืน `internal`; port signatures ใช้ชนิดที่ตรวจแล้ว (`Email`, `Symbol`, `RevokeReason`, `IdentityProviderName`) แทน `string`

## ผลที่ตามมา
- service และ repository ไม่ต้องตรวจซ้ำ และ compiler กันการสลับ quantity กับ price
- Go ยังสร้าง zero value ได้ (`Quantity{}`) จึงต้องมี `IsZero()` ให้ปฏิเสธ; zero value ของ `Fee` คือค่าธรรมเนียม 0 ที่ถูกต้อง
- adapter ต้อง map ค่าจาก DB ผ่าน `Parse*` อีกครั้ง ค่าที่ผิดกติกาจาก DB ถือเป็น `internal`
- ค่าที่คำนวณ (cost basis, P&L) ใน milestone 7–8 ใช้ `decimal.Decimal` ตรง ไม่สร้างชนิดเพิ่มจนกว่าจะมีเหตุผล
```

- [ ] **Step 2: เพิ่มแถวใน `docs/adr/README.md`** ต่อจากแถว 0012:

```markdown
| [0013](0013-domain-value-types.md) | Domain value types แบบ parse, don't validate และชนิดตัวเลขแยกตามความหมาย | Accepted |
```

- [ ] **Step 3: แก้ phase 1 spec หัวข้อ 8** (`docs/superpowers/specs/2026-10-05-phase-1-design.md`)

แทนประโยคนี้ (บรรทัดแรกใต้หัวข้อ `## 8. Ports และชนิดข้อมูล`):

```text
Signatures นี้เป็นข้อเสนอ interface ใน core ไม่ใช่ไฟล์โค้ดที่สร้างแล้ว ทุกชนิด `domain.*` เป็น plain Go value ไม่มี Fiber/pgx/sqlc/SDK types
```

ด้วย:

```text
ทุกชนิด `domain.*` เป็น plain Go value ไม่มี Fiber/pgx/sqlc/SDK types. **Milestone 2 (2026-10-16):** สร้าง repository ports, `UnitOfWork`/`TxRepositories` และ `Clock` แล้วใน `internal/core/port` ตาม [milestone 2 design](2026-10-16-milestone-2-domain-ports-design.md) ส่วน service ports และ provider ports ด้านล่างยังเป็นข้อเสนอ และสร้างใน milestone ที่ใช้จริง; 4 พารามิเตอร์เปลี่ยนเป็นชนิดที่ตรวจแล้วตาม [ADR-0013](../../adr/0013-domain-value-types.md)
```

แล้วแก้ 5 บรรทัดใน code block ของหัวข้อเดียวกัน (บรรทัดละคู่ เดิม → ใหม่):

```text
    BySymbol(context.Context, string) (domain.Instrument, error)
    BySymbol(context.Context, domain.Symbol) (domain.Instrument, error)

    ByEmail(context.Context, string) (domain.User, error)
    ByEmail(context.Context, domain.Email) (domain.User, error)

    Revoke(context.Context, domain.ID, time.Time, string) (bool, error)
    Revoke(context.Context, domain.ID, time.Time, domain.RevokeReason) (bool, error)

    RevokeAll(context.Context, domain.ID, time.Time, string) (int64, error)
    RevokeAll(context.Context, domain.ID, time.Time, domain.RevokeReason) (int64, error)

    BySubject(context.Context, string, string) (domain.AuthProvider, error)
    BySubject(context.Context, domain.IdentityProviderName, string) (domain.AuthProvider, error)
```

Run: `grep -nE 'BySymbol|ByEmail|Revoke\(|RevokeAll|BySubject' docs/superpowers/specs/2026-10-05-phase-1-design.md`
Expected: 5 บรรทัดที่แสดงชนิดใหม่ทั้งหมด และไม่มีบรรทัดใดที่ยังรับ `string` ในตำแหน่งที่เปลี่ยน

- [ ] **Step 4: แก้ roadmap milestone 2** (`docs/superpowers/plans/2026-10-05-phase-1-roadmap.md`)

1. ใต้หัวข้อ `### 2. Domain + ports + กติกาข้อมูลพื้นฐาน` เพิ่มบรรทัดนี้เหนือบรรทัด `**ไฟล์หลัก:**`:

   ```text
   **แผนลงมือ:** [2026-10-16-milestone-2-domain-ports.md](2026-10-16-milestone-2-domain-ports.md)
   ```

2. แทนบรรทัด `**ไฟล์หลัก:**` ของ milestone 2 ทั้งบรรทัดด้วย:

   ```text
   **ไฟล์หลัก:** `internal/core/domain/` (value types, entities, errors), `internal/core/port/` (repository ports, UnitOfWork, Clock), `internal/core/boundary_test.go`
   ```

3. tick ทั้ง 3 รายการของ milestone 2 เป็น `- [x]` และต่อท้ายรายการที่สอง (ขึ้นต้นด้วย `สร้าง service/repository/provider ports`) ด้วย:

   ```text
    (สร้างเฉพาะ repository ports, UnitOfWork และ Clock ที่ milestone 3 ใช้; service/provider ports สร้างใน milestone ที่ใช้จริง ตามที่ผู้ใช้ยืนยัน 2026-10-16)
   ```

4. ในบรรทัด `**Status:**` แทนข้อความ `ขั้นต่อไปคือแตกแผนย่อยของ milestone 2 ยังไม่เริ่ม` ด้วย `milestone 2 ลงมือครบทุก task แล้ว (2026-10-16) รอผู้ใช้ตรวจรับ`

- [ ] **Step 5: แก้สถานะใน design ของ milestone 2** (`docs/superpowers/specs/2026-10-16-milestone-2-domain-ports-design.md`)

แทนสองบรรทัดนี้:

```text
- **สถานะ:** รอผู้ใช้รีวิว (design ทั้ง 4 ส่วนได้รับการยืนยันในบทสนทนา 2026-10-16)
- **ADR ที่จะเพิ่ม:** ADR-0013 domain value types แบบ parse, don't validate
```

ด้วย:

```text
- **สถานะ:** ผู้ใช้ยืนยันแล้ว 2026-10-16; implement ตาม [แผน](../plans/2026-10-16-milestone-2-domain-ports.md)
- **ADR:** [ADR-0013](../../adr/0013-domain-value-types.md)
```

- [ ] **Step 6: ตรวจทั้ง repo**

Run: `make check`
Expected: ทุกขั้นผ่าน — `go mod tidy -diff` ไม่มี output, `go vet` ไม่มี output, `0 issues.`, swagger valid, tests `ok` ทุกแพ็กเกจ (integration tests SKIP) และ `git diff --exit-code -- docs/api` ไม่มี diff

- [ ] **Step 7: Commit**

```bash
git add docs
git commit -m "docs: record milestone 2 domain value types and ports"
```
