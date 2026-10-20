# Milestone 3b — Repositories ฝั่ง auth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** implement repository ports ฝั่ง auth ทั้ง 6 ตัว (`UserRepository`, `SessionRepository`, `IdentityRepository`, `OIDCFlowRepository`, `OTPChallengeRepository`, `RateLimitRepository`) บน PostgreSQL จริงผ่าน role `app_runtime` พร้อม tests ภายใต้คำขอพร้อมกัน — แผนที่สองจาก 3 แผนของ milestone 3

**Architecture:** แต่ละ repository เป็น struct ใน `internal/adapter/postgres` ที่ถือ `*sqlc.Queries` (บน pool หรือบน transaction ของ `withinTx`) และ implement port ของ core; `Store` สร้าง instance บน pool ส่วน `txRepositories` สร้าง instance ที่ผูกกับ transaction; การเขียนที่อาจชน unique ใช้ `ON CONFLICT DO NOTHING` + ตรวจจำนวนแถว เพื่อไม่ให้ transaction ถูก abort; การ consume ใช้ UPDATE ประโยคเดียวเพื่อให้สำเร็จครั้งเดียวภายใต้คำขอพร้อมกัน; `RateLimitRepository.Admit` ทำใน transaction ของตัวเองและ lock แถว key เรียงตาม `key_hash`

**Tech Stack:** Go 1.27 (go.mod `go 1.27.1`), pgx v5.11.0, sqlc v1.31.1 (cgo), PostgreSQL 17.11, golangci-lint v2.14.0

**Spec:** [docs/superpowers/specs/2026-10-17-milestone-3-persistence-design.md](../specs/2026-10-17-milestone-3-persistence-design.md) หัวข้อ 5 (แผน 3b) ซึ่งอ้าง [phase 1 spec](../specs/2026-10-05-phase-1-design.md) หัวข้อ 7–9 และ [AGENTS.md](../../../AGENTS.md)

## Global Constraints

- ทุก query ระบุ `app.` หน้าชื่อตาราง; ห้ามแก้ไฟล์ใน `internal/adapter/postgres/sqlc/` ด้วยมือ ให้แก้ query แล้ว `make sqlc`
- email เทียบตรงตามค่าที่ Go normalize แล้ว: ห้ามใช้ `lower()` กับ email ใน SQL
- เฉพาะ `GetUserCredentialsByEmail` เท่านั้นที่ SELECT `password_hash`
- `Lock*` บน repository ที่สร้างจาก pool ต้องคืน `internal` โดยไม่ส่ง query
- `now`/`at` มาจาก parameter (Clock ของ service) ไม่ใช้ `now()` ของ DB
- ค่าจาก DB ที่ parse ไม่ผ่านเป็น `internal` ผ่าน `fromStored` ไม่ใช่ `invalid_input`
- ไม่เพิ่ม dependency ใหม่; core ไม่ import adapter
- integration tests ต่อเป็น `app_runtime` และข้อมูลสุ่มต่อ test ไม่ล้างตาราง; ในเครื่องต้องเปิด PostgreSQL ของ compose ก่อน (`docker compose up -d postgres migrate`) และใช้ `make test-integration`
- ต้องเห็น test ล้มก่อนแล้วจึงเขียนโค้ด และตรวจผล `make test-integration` ทุกครั้งว่าไม่มี `SKIP`
- commit message ไม่มี `Co-Authored-By` หรือชื่อ Claude; commit ในเครื่องเท่านั้น ห้าม push
- รันทุกคำสั่งที่ root ของ repo บน branch ที่ผู้ใช้ใช้อยู่ (`feature/creat_login_and_register`); Go ในเครื่องคือ `/usr/local/go/bin/go` (1.27.2)

## Review Focus

1. **conflict ทำให้ transaction ใช้ต่อไม่ได้** — สมัครด้วยอีเมลซ้ำหรือเชื่อม Google ซ้ำแล้ว service เขียน audit ต่อไม่ได้ (SQLSTATE 25P02) — Task 1 `TestUserInsertDuplicateEmailKeepsTransactionUsable`, Task 2 `TestIdentityConflictsKeepTransactionUsable`
2. **ใช้ซ้ำได้ภายใต้คำขอพร้อมกัน** — OIDC callback หรือ OTP เดียวกันผ่านสองครั้ง — Task 3 `TestOIDCFlowConcurrentConsumeSucceedsOnce`, `TestOTPChallengeConcurrentConsumeSucceedsOnce`
3. **OTP ผิดขั้นตอน** — challenge ของ flow อื่น หมดอายุ ถูก invalidate หรือขอใหม่พร้อมกันแล้วชน one-open index — Task 3 `TestOTPChallengeInsertAndReadByPurpose`, `TestOTPChallengeConsumeOnlyWhileOpenAndUnexpired`, `TestOTPChallengeConcurrentReissueUnderUserLock`
4. **logout ซ้ำถูกนับเป็น revoke สำเร็จใหม่** — Task 2 `TestSessionRevokeReportsOnlyTheFirstRevoke`; `RevokeAll` ไม่แตะ session ของผู้อื่นหรือที่ revoke ไปแล้ว — `TestSessionRevokeAllRevokesOnlyTheUsersActiveSessions`
5. **rate limit เกินโควตาเมื่อคำขอพร้อมกัน หรือล็อกผู้ใช้จริงนานขึ้นเพราะคำขอที่ถูกปฏิเสธ** — Task 4 `TestRateLimitConcurrentAdmissionsRespectTheLimit`, `TestRateLimitDeniedAttemptsAreNotCounted`

---

## File Structure

| ไฟล์ | หน้าที่ | Task |
| --- | --- | --- |
| `internal/adapter/postgres/queries/users.sql`, `users.go`, `users_test.go`, `fixtures_test.go` | `UserRepository` และ helper ของ tests | 1 |
| `internal/adapter/postgres/queries/sessions.sql`, `identities.sql`, `sessions.go`, `identities.go` (+tests) | `SessionRepository`, `IdentityRepository` | 2 |
| `internal/adapter/postgres/queries/oidc.sql`, `otp.sql`, `oidc.go`, `otp.go` (+tests) | `OIDCFlowRepository`, `OTPChallengeRepository` | 3 |
| `internal/adapter/postgres/queries/ratelimit.sql`, `ratelimit.go` (+test) | `RateLimitRepository.Admit` | 4 |
| `internal/adapter/postgres/store.go` | accessor ของ repositories บน `Store` และ `txRepositories` | 1–4 |
| `internal/core/port/auth.go` | อัปเดต comment ของ contract เท่านั้น | 1–3 |
| `internal/adapter/postgres/sqlc/` (generated) | โค้ดที่ `make sqlc` สร้าง | 1–4 |
| spec/roadmap | เอกสาร | 5 |

---

### Task 1: `UserRepository` และ accessor บน `Store`

**Files:**
- Create: `internal/adapter/postgres/queries/users.sql`, `internal/adapter/postgres/users.go`
- Test: `internal/adapter/postgres/fixtures_test.go`, `internal/adapter/postgres/users_test.go`
- Modify: `internal/adapter/postgres/store.go`, `internal/core/port/auth.go`
- Generated by `make sqlc`: `internal/adapter/postgres/sqlc/users.sql.go`

**Interfaces:**
- Consumes: `Store`, `withinTx`, `txRepositories`, `newRuntimeStore(t)`, `newID(t)`, `auditEvent`, `auditExists` (แผน 3a); ports ใน `internal/core/port/auth.go`; `mapError`, mapper ใน `mapping.go`
- Produces: `Store.Users() port.UserRepository`, `(*txRepositories).Users() port.UserRepository` (instance ใน transaction ตั้ง `inTx` ให้ `Lock` ใช้ได้); `(*txRepositories).Audit()` คืน `port.AuditRepository`; `errLockOutsideTx`; test helpers `now()`, `uniqueEmail(t)`, `insertUser(t, store)`

- [ ] **Step 1: เขียน failing tests**

`internal/adapter/postgres/fixtures_test.go`:

```go
package postgres

import (
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// now returns the current time at microsecond precision, which is what PostgreSQL stores, so
// values read back compare equal.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

// uniqueEmail returns an address no other test uses, already normalized.
func uniqueEmail(t *testing.T) domain.Email {
	t.Helper()
	e, err := domain.ParseEmail("user-" + newID(t).String() + "@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// insertUser stores a fresh unverified account through the repository and returns it.
func insertUser(t *testing.T, store *Store) domain.User {
	t.Helper()
	at := now()
	u := domain.User{ID: newID(t), Email: uniqueEmail(t), CreatedAt: at, UpdatedAt: at}
	if err := store.Users().Insert(t.Context(), u); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return u
}
```

`internal/adapter/postgres/users_test.go`:

```go
package postgres

import (
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestUserInsertAndRead(t *testing.T) {
	store := newRuntimeStore(t)
	u := insertUser(t, store)

	byID, err := store.Users().ByID(t.Context(), u.ID)
	if err != nil || byID.ID != u.ID || byID.Email != u.Email || byID.EmailVerifiedAt != nil ||
		!byID.CreatedAt.Equal(u.CreatedAt) || !byID.UpdatedAt.Equal(u.UpdatedAt) {
		t.Fatalf("ByID = %+v, %v; want %+v", byID, err, u)
	}
	byEmail, err := store.Users().ByEmail(t.Context(), u.Email)
	if err != nil || byEmail.ID != u.ID {
		t.Fatalf("ByEmail = %+v, %v", byEmail, err)
	}
	if _, err := store.Users().ByID(t.Context(), newID(t)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ByID(unknown) error = %v, want not_found", err)
	}
	if _, err := store.Users().ByEmail(t.Context(), uniqueEmail(t)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ByEmail(unknown) error = %v, want not_found", err)
	}
}

// TestUserEmailIsComparedExactly: the stored value is Go's normalized email, compared without
// lower() in SQL, so non-ASCII addresses behave the same in Go and in the database.
func TestUserEmailIsComparedExactly(t *testing.T) {
	store := newRuntimeStore(t)
	e, err := domain.ParseEmail("ÄLICE-" + newID(t).String() + "@EXAMPLE.COM")
	if err != nil {
		t.Fatal(err)
	}
	at := now()
	u := domain.User{ID: newID(t), Email: e, CreatedAt: at, UpdatedAt: at}
	if err := store.Users().Insert(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	got, err := store.Users().ByEmail(t.Context(), e)
	if err != nil || got.ID != u.ID || got.Email.String() != e.String() {
		t.Fatalf("ByEmail(%q) = %+v, %v", e, got, err)
	}
}

// TestUserInsertDuplicateEmailKeepsTransactionUsable is the review finding of plan 3a: a conflict
// must not abort the transaction, so the caller can still write in the same callback.
func TestUserInsertDuplicateEmailKeepsTransactionUsable(t *testing.T) {
	store := newRuntimeStore(t)
	first := insertUser(t, store)
	second := domain.User{ID: newID(t), Email: uniqueEmail(t), CreatedAt: now(), UpdatedAt: now()}

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		dup := domain.User{ID: newID(t), Email: first.Email, CreatedAt: now(), UpdatedAt: now()}
		if err := r.Users().Insert(t.Context(), dup); !errors.Is(err, domain.ErrIdentityConflict) {
			t.Errorf("duplicate email error = %v, want identity_conflict", err)
		}
		return r.Users().Insert(t.Context(), second) // must still work in the same transaction
	})

	if err != nil {
		t.Fatalf("transaction after a conflict: %v", err)
	}
	if _, err := store.Users().ByID(t.Context(), second.ID); err != nil {
		t.Fatalf("write after the conflict was not committed: %v", err)
	}
}

func TestUserCredentialsAndPassword(t *testing.T) {
	store := newRuntimeStore(t)
	u := insertUser(t, store)

	creds, err := store.Users().CredentialsByEmail(t.Context(), u.Email)
	if err != nil || creds.User.ID != u.ID || creds.HasPassword() {
		t.Fatalf("credentials before SetPassword = %+v, %v; want no password (Google-only shape)", creds.User, err)
	}

	const hash = domain.PasswordHash("$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA")
	at := now().Add(time.Second)
	if err := store.Users().SetPassword(t.Context(), u.ID, hash, at); err != nil {
		t.Fatal(err)
	}
	creds, err = store.Users().CredentialsByEmail(t.Context(), u.Email)
	if err != nil || creds.PasswordHash != hash || !creds.User.UpdatedAt.Equal(at) {
		t.Fatalf("credentials after SetPassword: hash matches=%v updated=%v err=%v", creds.PasswordHash == hash, creds.User.UpdatedAt, err)
	}

	if err := store.Users().SetPassword(t.Context(), newID(t), hash, at); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("SetPassword(unknown) error = %v, want not_found", err)
	}
	if err := store.Users().SetPassword(t.Context(), u.ID, "", at); !errors.Is(err, domain.ErrInternal) {
		t.Errorf("SetPassword(empty hash) error = %v, want internal", err)
	}
}

func TestUserMarkEmailVerifiedKeepsFirstTime(t *testing.T) {
	store := newRuntimeStore(t)
	u := insertUser(t, store)
	first := now().Add(time.Minute)

	if err := store.Users().MarkEmailVerified(t.Context(), u.ID, first); err != nil {
		t.Fatal(err)
	}
	if err := store.Users().MarkEmailVerified(t.Context(), u.ID, first.Add(time.Hour)); err != nil {
		t.Fatalf("second MarkEmailVerified must be a no-op, got %v", err)
	}
	got, err := store.Users().ByID(t.Context(), u.ID)
	if err != nil || got.EmailVerifiedAt == nil || !got.EmailVerifiedAt.Equal(first) || !got.UpdatedAt.Equal(first) {
		t.Fatalf("verified_at = %v updated_at = %v err %v; want both %v", got.EmailVerifiedAt, got.UpdatedAt, err, first)
	}
	if err := store.Users().MarkEmailVerified(t.Context(), newID(t), first); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("MarkEmailVerified(unknown) error = %v, want not_found", err)
	}
}

func TestUserLockRequiresTransaction(t *testing.T) {
	store := newRuntimeStore(t)
	u := insertUser(t, store)

	if _, err := store.Users().Lock(t.Context(), u.ID); !errors.Is(err, domain.ErrInternal) {
		t.Errorf("Lock outside a transaction error = %v, want internal", err)
	}
	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		got, err := r.Users().Lock(t.Context(), u.ID)
		if err != nil || got.ID != u.ID {
			t.Errorf("Lock in transaction = %+v, %v", got, err)
		}
		if _, err := r.Users().Lock(t.Context(), newID(t)); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Lock(unknown) error = %v, want not_found", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: รันให้เห็นว่าล้มเหลว**

Run: `make test-integration`
Expected: FAIL ด้วย `[build failed]` และ `store.Users undefined` (ยังไม่มี accessor)

- [ ] **Step 3: เขียน queries แล้ว generate**

`internal/adapter/postgres/queries/users.sql`:

```sql
-- Every query except CredentialsByEmail leaves password_hash out (phase 1 spec section 7).

-- name: GetUserByID :one
SELECT id, email, email_verified_at, created_at, updated_at
FROM app.users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT id, email, email_verified_at, created_at, updated_at
FROM app.users WHERE email = $1;

-- name: GetUserCredentialsByEmail :one
SELECT id, email, email_verified_at, created_at, updated_at, password_hash
FROM app.users WHERE email = $1;

-- name: LockUser :one
SELECT id, email, email_verified_at, created_at, updated_at
FROM app.users WHERE id = $1
FOR UPDATE;

-- ON CONFLICT DO NOTHING keeps the transaction usable when the email is taken: zero rows
-- inserted means the email is already registered.
-- name: InsertUser :execrows
INSERT INTO app.users (id, email, email_verified_at, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (email) DO NOTHING;

-- name: SetUserPassword :execrows
UPDATE app.users SET password_hash = $2, updated_at = $3 WHERE id = $1;

-- Sets email_verified_at only the first time; a verified user keeps the original time.
-- name: MarkUserEmailVerified :execrows
UPDATE app.users
SET email_verified_at = COALESCE(email_verified_at, @verified_at::timestamptz),
    updated_at = CASE WHEN email_verified_at IS NULL THEN @verified_at::timestamptz ELSE updated_at END
WHERE id = @id;
```

Run: `make sqlc && ls internal/adapter/postgres/sqlc`
Expected: ไม่มี error และมีไฟล์ `users.sql.go` เพิ่ม

- [ ] **Step 4: เขียน repository และ accessor**

`internal/adapter/postgres/users.go`:

```go
package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// UserRepository stores accounts. Only CredentialsByEmail reads password_hash.
type UserRepository struct {
	q    *sqlc.Queries
	inTx bool
}

var _ port.UserRepository = (*UserRepository)(nil)

// ByID returns the user, or domain.ErrNotFound.
func (r *UserRepository) ByID(ctx context.Context, id domain.ID) (domain.User, error) {
	row, err := r.q.GetUserByID(ctx, pgUUID(id))
	if err != nil {
		return domain.User{}, mapError(err)
	}
	return userFromColumns(row.ID, row.Email, row.EmailVerifiedAt, row.CreatedAt, row.UpdatedAt)
}

// ByEmail returns the user with exactly this (already normalized) email, or domain.ErrNotFound.
func (r *UserRepository) ByEmail(ctx context.Context, email domain.Email) (domain.User, error) {
	row, err := r.q.GetUserByEmail(ctx, email.String())
	if err != nil {
		return domain.User{}, mapError(err)
	}
	return userFromColumns(row.ID, row.Email, row.EmailVerifiedAt, row.CreatedAt, row.UpdatedAt)
}

// CredentialsByEmail returns the user and the password hash; the hash is "" for Google-only accounts.
func (r *UserRepository) CredentialsByEmail(ctx context.Context, email domain.Email) (domain.UserCredentials, error) {
	row, err := r.q.GetUserCredentialsByEmail(ctx, email.String())
	if err != nil {
		return domain.UserCredentials{}, mapError(err)
	}
	user, err := userFromColumns(row.ID, row.Email, row.EmailVerifiedAt, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return domain.UserCredentials{}, err
	}
	return domain.UserCredentials{User: user, PasswordHash: domain.PasswordHash(row.PasswordHash.String)}, nil
}

// Lock reads the user with FOR UPDATE; it only works inside a transaction.
func (r *UserRepository) Lock(ctx context.Context, id domain.ID) (domain.User, error) {
	if !r.inTx {
		return domain.User{}, errLockOutsideTx
	}
	row, err := r.q.LockUser(ctx, pgUUID(id))
	if err != nil {
		return domain.User{}, mapError(err)
	}
	return userFromColumns(row.ID, row.Email, row.EmailVerifiedAt, row.CreatedAt, row.UpdatedAt)
}

// Insert stores a new account without a password (SetPassword adds it in the same transaction).
// A taken email returns domain.ErrIdentityConflict and leaves the transaction usable.
func (r *UserRepository) Insert(ctx context.Context, u domain.User) error {
	n, err := r.q.InsertUser(ctx, sqlc.InsertUserParams{
		ID:              pgUUID(u.ID),
		Email:           u.Email.String(),
		EmailVerifiedAt: pgOptionalTime(u.EmailVerifiedAt),
		CreatedAt:       pgTime(u.CreatedAt),
		UpdatedAt:       pgTime(u.UpdatedAt),
	})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrIdentityConflict
	}
	return nil
}

// SetPassword stores a new password hash. An empty hash is a bug: Google-only accounts never get
// a password through this method.
func (r *UserRepository) SetPassword(ctx context.Context, id domain.ID, hash domain.PasswordHash, at time.Time) error {
	if hash == "" {
		return domain.NewError(domain.CodeInternal, "password hash is empty")
	}
	n, err := r.q.SetUserPassword(ctx, sqlc.SetUserPasswordParams{
		ID:           pgUUID(id),
		PasswordHash: pgtype.Text{String: string(hash), Valid: true},
		UpdatedAt:    pgTime(at),
	})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// MarkEmailVerified sets email_verified_at the first time; later calls keep the original time.
func (r *UserRepository) MarkEmailVerified(ctx context.Context, id domain.ID, at time.Time) error {
	n, err := r.q.MarkUserEmailVerified(ctx, sqlc.MarkUserEmailVerifiedParams{ID: pgUUID(id), VerifiedAt: pgTime(at)})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func userFromColumns(id pgtype.UUID, email string, verifiedAt, createdAt, updatedAt pgtype.Timestamptz) (domain.User, error) {
	userID, err := idFromPG("user.id", id)
	if err != nil {
		return domain.User{}, err
	}
	parsedEmail, err := domain.ParseEmail(email)
	if err != nil {
		return domain.User{}, fromStored(err)
	}
	return domain.User{
		ID:              userID,
		Email:           parsedEmail,
		EmailVerifiedAt: optionalTimeFromPG(verifiedAt),
		CreatedAt:       timeFromPG(createdAt),
		UpdatedAt:       timeFromPG(updatedAt),
	}, nil
}
```

แก้ `internal/adapter/postgres/store.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
	"go-backend-stock/internal/core/domain"
)
```

   ด้วย

```go
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)
```

2. แทน

```go
// txRepositories are the repositories bound to one database transaction. Later plans add one
// accessor per repository until it satisfies port.TxRepositories.
type txRepositories struct {
	q *sqlc.Queries
}

// Audit returns the audit repository bound to this transaction.
func (r *txRepositories) Audit() *AuditRepository {
```

   ด้วย

```go
// errLockOutsideTx is returned by Lock* on a repository that is not bound to a transaction: a row
// lock outside a transaction is released at once and protects nothing (phase 1 spec section 8).
var errLockOutsideTx = domain.NewError(domain.CodeInternal, "row lock requires a transaction")

// Repositories that read outside a transaction. They use the pool, so each call is its own
// statement; Lock* methods refuse to run on them.

// Users returns the user repository over the pool.
func (s *Store) Users() port.UserRepository { return &UserRepository{q: sqlc.New(s.pool)} }

// txRepositories are the repositories bound to one database transaction. Plan 3c adds the
// portfolio-side accessors until it satisfies port.TxRepositories.
type txRepositories struct {
	q *sqlc.Queries
}

// Users returns the user repository bound to this transaction.
func (r *txRepositories) Users() port.UserRepository { return &UserRepository{q: r.q, inTx: true} }

// Audit returns the audit repository bound to this transaction.
func (r *txRepositories) Audit() port.AuditRepository {
```

แก้ `internal/core/port/auth.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
	SetPassword(ctx context.Context, id domain.ID, passwordHash domain.PasswordHash, at time.Time) error
```

   ด้วย

```go
	// SetPassword returns an internal error for an empty hash: Google-only accounts never gain a
	// password this way.
	SetPassword(ctx context.Context, id domain.ID, passwordHash domain.PasswordHash, at time.Time) error
```

- [ ] **Step 5: รัน tests และ lint ให้ผ่าน**

Run: `gofmt -l internal && make test-integration && make lint`
Expected: `gofmt` ไม่พิมพ์อะไร, `ok  	go-backend-stock/internal/adapter/postgres` โดยไม่มี `SKIP` (ตรวจด้วย `go test -v` ว่า PASS 28 รายการ รวม tests เดิมของ 3a) และ `0 issues.`

- [ ] **Step 6: พิสูจน์ว่า test จับการเขียนผิดได้**

```bash
git add -A internal
```

ใน `internal/adapter/postgres/queries/users.sql` แทนชั่วคราว

```sql
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (email) DO NOTHING;
```

ด้วย

```sql
VALUES ($1, $2, $3, $4, $5);
```

Run: `make sqlc && go test -count=1 -run TestUserInsertDuplicateEmailKeepsTransactionUsable ./internal/adapter/postgres/`
Expected: FAIL ด้วย `transaction after a conflict: ... current transaction is aborted ... (SQLSTATE 25P02)`

จากนั้นคืนไฟล์: `git checkout -- internal/adapter/postgres/queries/users.sql && make sqlc && git status --short` ต้องไม่แสดงไฟล์ที่ยังไม่ได้ stage (ไม่มี diff เทียบกับ Step 4)

- [ ] **Step 7: Commit**

```bash
git add -A internal
git commit -m "feat(db): add user repository over sqlc"
```

---

### Task 2: `SessionRepository` และ `IdentityRepository`

**Files:**
- Create: `internal/adapter/postgres/queries/sessions.sql`, `internal/adapter/postgres/queries/identities.sql`, `internal/adapter/postgres/sessions.go`, `internal/adapter/postgres/identities.go`
- Test: `internal/adapter/postgres/sessions_test.go`, `internal/adapter/postgres/identities_test.go`
- Modify: `internal/adapter/postgres/store.go`, `internal/core/port/auth.go`, `internal/adapter/postgres/fixtures_test.go`
- Generated by `make sqlc`: `internal/adapter/postgres/sqlc/identities.sql.go`, `internal/adapter/postgres/sqlc/sessions.sql.go`

**Interfaces:**
- Consumes: `insertUser`, `now` (Task 1); `withinTx`, `auditEvent`, `auditExists` (3a)
- Produces: `Store.Sessions()`, `Store.Identities()` และ accessor บน `txRepositories`; test helpers `randomHash(t)`, `insertSession(t, store, userID)`, `googleLink(t, userID, subject)`

- [ ] **Step 1: เขียน failing tests**

`internal/adapter/postgres/sessions_test.go`:

```go
package postgres

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func insertSession(t *testing.T, store *Store, userID domain.ID) domain.Session {
	t.Helper()
	at := now()
	s, err := domain.NewSession(domain.Session{ID: newID(t), UserID: userID, TokenHash: randomHash(t), CreatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Sessions().Insert(t.Context(), s); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return s
}

func TestSessionInsertAndRead(t *testing.T) {
	store := newRuntimeStore(t)
	s := insertSession(t, store, insertUser(t, store).ID)

	got, err := store.Sessions().ByTokenHash(t.Context(), s.TokenHash)
	if err != nil || got.ID != s.ID || got.UserID != s.UserID || !bytes.Equal(got.TokenHash, s.TokenHash) ||
		!got.CreatedAt.Equal(s.CreatedAt) || !got.ExpiresAt.Equal(s.ExpiresAt) || got.RevokedAt != nil || got.RevokeReason != "" {
		t.Fatalf("ByTokenHash = %+v, %v; want %+v", got, err, s)
	}
	if _, err := store.Sessions().ByTokenHash(t.Context(), randomHash(t)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ByTokenHash(unknown) error = %v, want not_found", err)
	}
}

func TestSessionRevokeReportsOnlyTheFirstRevoke(t *testing.T) {
	store := newRuntimeStore(t)
	s := insertSession(t, store, insertUser(t, store).ID)
	at := now()

	first, err := store.Sessions().Revoke(t.Context(), s.ID, at, domain.RevokeReasonLogout)
	if err != nil || !first {
		t.Fatalf("first Revoke = %v, %v; want true", first, err)
	}
	second, err := store.Sessions().Revoke(t.Context(), s.ID, at.Add(time.Minute), domain.RevokeReasonLogout)
	if err != nil || second {
		t.Fatalf("second Revoke = %v, %v; want false so logout audits once", second, err)
	}
	unknown, err := store.Sessions().Revoke(t.Context(), newID(t), at, domain.RevokeReasonLogout)
	if err != nil || unknown {
		t.Errorf("Revoke(unknown) = %v, %v; want false", unknown, err)
	}

	got, err := store.Sessions().ByTokenHash(t.Context(), s.TokenHash)
	if err != nil || got.RevokedAt == nil || !got.RevokedAt.Equal(at) || got.RevokeReason != domain.RevokeReasonLogout {
		t.Fatalf("revoked session = %+v, %v; want revoked at the first time with reason logout", got, err)
	}
}

func TestSessionRevokeAllRevokesOnlyTheUsersActiveSessions(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	other := insertUser(t, store)
	a := insertSession(t, store, user.ID)
	b := insertSession(t, store, user.ID)
	alreadyRevoked := insertSession(t, store, user.ID)
	othersSession := insertSession(t, store, other.ID)
	if _, err := store.Sessions().Revoke(t.Context(), alreadyRevoked.ID, now(), domain.RevokeReasonLogout); err != nil {
		t.Fatal(err)
	}

	n, err := store.Sessions().RevokeAll(t.Context(), user.ID, now(), domain.RevokeReasonPasswordReset)
	if err != nil || n != 2 {
		t.Fatalf("RevokeAll = %d, %v; want 2", n, err)
	}
	for _, s := range []domain.Session{a, b} {
		got, _ := store.Sessions().ByTokenHash(t.Context(), s.TokenHash)
		if got.RevokeReason != domain.RevokeReasonPasswordReset {
			t.Errorf("session %s reason = %q, want password_reset", s.ID, got.RevokeReason)
		}
	}
	if got, _ := store.Sessions().ByTokenHash(t.Context(), alreadyRevoked.TokenHash); got.RevokeReason != domain.RevokeReasonLogout {
		t.Errorf("already revoked session changed reason to %q", got.RevokeReason)
	}
	if got, _ := store.Sessions().ByTokenHash(t.Context(), othersSession.TokenHash); got.RevokedAt != nil {
		t.Error("RevokeAll revoked another user's session")
	}
}
```

`internal/adapter/postgres/identities_test.go`:

```go
package postgres

import (
	"errors"
	"testing"

	"go-backend-stock/internal/core/domain"
)

func googleLink(t *testing.T, userID domain.ID, subject string) domain.AuthProvider {
	t.Helper()
	link, err := domain.NewAuthProvider(domain.AuthProvider{
		ID: newID(t), UserID: userID, Provider: domain.IdentityProviderGoogle, ProviderSubject: subject, CreatedAt: now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return link
}

func TestIdentityInsertAndRead(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	link := googleLink(t, user.ID, "sub-"+newID(t).String())
	if err := store.Identities().Insert(t.Context(), link); err != nil {
		t.Fatal(err)
	}

	got, err := store.Identities().BySubject(t.Context(), domain.IdentityProviderGoogle, link.ProviderSubject)
	if err != nil || got.ID != link.ID || got.UserID != user.ID || got.Provider != domain.IdentityProviderGoogle ||
		got.ProviderSubject != link.ProviderSubject || !got.CreatedAt.Equal(link.CreatedAt) {
		t.Fatalf("BySubject = %+v, %v; want %+v", got, err, link)
	}
	if _, err := store.Identities().BySubject(t.Context(), domain.IdentityProviderGoogle, "sub-unknown-"+newID(t).String()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("BySubject(unknown) error = %v, want not_found", err)
	}
}

// TestIdentityConflictsKeepTransactionUsable: both unique rules return identity_conflict without
// aborting the transaction, so the service can write auth.identity_link_failed in the same callback.
func TestIdentityConflictsKeepTransactionUsable(t *testing.T) {
	store := newRuntimeStore(t)
	owner := insertUser(t, store)
	subject := "sub-" + newID(t).String()
	if err := store.Identities().Insert(t.Context(), googleLink(t, owner.ID, subject)); err != nil {
		t.Fatal(err)
	}
	intruder := insertUser(t, store)
	e := auditEvent(t, `{}`)

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		// The same Google account cannot link to a second user.
		if err := r.Identities().Insert(t.Context(), googleLink(t, intruder.ID, subject)); !errors.Is(err, domain.ErrIdentityConflict) {
			t.Errorf("same subject error = %v, want identity_conflict", err)
		}
		// A user cannot link a second Google account.
		if err := r.Identities().Insert(t.Context(), googleLink(t, owner.ID, "sub-"+newID(t).String())); !errors.Is(err, domain.ErrIdentityConflict) {
			t.Errorf("second Google account error = %v, want identity_conflict", err)
		}
		return r.Audit().Append(t.Context(), e)
	})

	if err != nil {
		t.Fatalf("transaction after identity conflicts: %v", err)
	}
	if !auditExists(t, e.ID) {
		t.Fatal("audit written after the conflicts was not committed")
	}
}
```

แก้ `internal/adapter/postgres/fixtures_test.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
import (
	"testing"
```

   ด้วย

```go
import (
	"crypto/rand"
	"testing"
```

2. แทน

```go
return time.Now().UTC().Truncate(time.Microsecond)
}
```

   ด้วย

```go
return time.Now().UTC().Truncate(time.Microsecond)
}

func randomHash(t *testing.T) []byte {
	t.Helper()
	h := make([]byte, domain.HashSize)
	if _, err := rand.Read(h); err != nil {
		t.Fatal(err)
	}
	return h
}
```

- [ ] **Step 2: รันให้เห็นว่าล้มเหลว**

Run: `make test-integration`
Expected: FAIL ด้วย `[build failed]` และ `store.Sessions undefined`, `store.Identities undefined` (ยังไม่มี accessor)

- [ ] **Step 3: เขียน queries แล้ว generate**

`internal/adapter/postgres/queries/sessions.sql`:

```sql
-- name: GetSessionByTokenHash :one
SELECT id, user_id, token_hash, created_at, expires_at, revoked_at, revoke_reason
FROM app.sessions WHERE token_hash = $1;

-- name: InsertSession :exec
INSERT INTO app.sessions (id, user_id, token_hash, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: RevokeSession :execrows
UPDATE app.sessions SET revoked_at = $2, revoke_reason = $3
WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeUserSessions :execrows
UPDATE app.sessions SET revoked_at = $2, revoke_reason = $3
WHERE user_id = $1 AND revoked_at IS NULL;
```

`internal/adapter/postgres/queries/identities.sql`:

```sql
-- name: GetIdentityBySubject :one
SELECT id, user_id, provider, provider_subject, created_at
FROM app.auth_providers WHERE provider = $1 AND provider_subject = $2;

-- Zero rows inserted means the subject, or this user's link to the provider, already exists.
-- name: InsertIdentity :execrows
INSERT INTO app.auth_providers (id, user_id, provider, provider_subject, created_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT DO NOTHING;
```

Run: `make sqlc && ls internal/adapter/postgres/sqlc`
Expected: ไม่มี error และมีไฟล์ `identities.sql.go`, `sessions.sql.go` เพิ่ม

- [ ] **Step 4: เขียน repository และ accessor**

`internal/adapter/postgres/sessions.go`:

```go
package postgres

import (
	"context"
	"time"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// SessionRepository stores server-side sessions by the SHA-256 hash of their token.
type SessionRepository struct {
	q *sqlc.Queries
}

var _ port.SessionRepository = (*SessionRepository)(nil)

// ByTokenHash returns the session as stored, expired or revoked included, or domain.ErrNotFound.
func (r *SessionRepository) ByTokenHash(ctx context.Context, tokenHash []byte) (domain.Session, error) {
	row, err := r.q.GetSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		return domain.Session{}, mapError(err)
	}
	return sessionFromRow(row)
}

// Insert stores a new session; build it with domain.NewSession.
func (r *SessionRepository) Insert(ctx context.Context, s domain.Session) error {
	return mapError(r.q.InsertSession(ctx, sqlc.InsertSessionParams{
		ID:        pgUUID(s.ID),
		UserID:    pgUUID(s.UserID),
		TokenHash: s.TokenHash,
		CreatedAt: pgTime(s.CreatedAt),
		ExpiresAt: pgTime(s.ExpiresAt),
	}))
}

// Revoke reports true only when this call revoked the session; an already revoked or unknown
// session reports false, so logout writes its audit event once.
func (r *SessionRepository) Revoke(ctx context.Context, id domain.ID, at time.Time, reason domain.RevokeReason) (bool, error) {
	n, err := r.q.RevokeSession(ctx, sqlc.RevokeSessionParams{
		ID: pgUUID(id), RevokedAt: pgTime(at), RevokeReason: pgText(string(reason)),
	})
	if err != nil {
		return false, mapError(err)
	}
	return n == 1, nil
}

// RevokeAll revokes every session of userID that is not revoked yet and returns how many.
func (r *SessionRepository) RevokeAll(ctx context.Context, userID domain.ID, at time.Time, reason domain.RevokeReason) (int64, error) {
	n, err := r.q.RevokeUserSessions(ctx, sqlc.RevokeUserSessionsParams{
		UserID: pgUUID(userID), RevokedAt: pgTime(at), RevokeReason: pgText(string(reason)),
	})
	if err != nil {
		return 0, mapError(err)
	}
	return n, nil
}

func sessionFromRow(row sqlc.AppSession) (domain.Session, error) {
	id, err := idFromPG("session.id", row.ID)
	if err != nil {
		return domain.Session{}, err
	}
	userID, err := idFromPG("session.user_id", row.UserID)
	if err != nil {
		return domain.Session{}, err
	}
	var reason domain.RevokeReason
	if row.RevokeReason.Valid {
		if reason, err = domain.ParseRevokeReason(row.RevokeReason.String); err != nil {
			return domain.Session{}, fromStored(err)
		}
	}
	return domain.Session{
		ID:           id,
		UserID:       userID,
		TokenHash:    row.TokenHash,
		CreatedAt:    timeFromPG(row.CreatedAt),
		ExpiresAt:    timeFromPG(row.ExpiresAt),
		RevokedAt:    optionalTimeFromPG(row.RevokedAt),
		RevokeReason: reason,
	}, nil
}
```

`internal/adapter/postgres/identities.go`:

```go
package postgres

import (
	"context"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// IdentityRepository stores links between users and external identities (Google).
type IdentityRepository struct {
	q *sqlc.Queries
}

var _ port.IdentityRepository = (*IdentityRepository)(nil)

// BySubject returns the link for the provider's stable subject, or domain.ErrNotFound.
func (r *IdentityRepository) BySubject(ctx context.Context, provider domain.IdentityProviderName, subject string) (domain.AuthProvider, error) {
	row, err := r.q.GetIdentityBySubject(ctx, sqlc.GetIdentityBySubjectParams{
		Provider: string(provider), ProviderSubject: subject,
	})
	if err != nil {
		return domain.AuthProvider{}, mapError(err)
	}
	return identityFromRow(row)
}

// Insert stores a link; build it with domain.NewAuthProvider. An existing subject, or an existing
// link of this user to the provider, returns domain.ErrIdentityConflict and leaves the transaction
// usable, so the caller can still write an auth.identity_link_failed audit event.
func (r *IdentityRepository) Insert(ctx context.Context, link domain.AuthProvider) error {
	n, err := r.q.InsertIdentity(ctx, sqlc.InsertIdentityParams{
		ID:              pgUUID(link.ID),
		UserID:          pgUUID(link.UserID),
		Provider:        string(link.Provider),
		ProviderSubject: link.ProviderSubject,
		CreatedAt:       pgTime(link.CreatedAt),
	})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrIdentityConflict
	}
	return nil
}

func identityFromRow(row sqlc.AppAuthProvider) (domain.AuthProvider, error) {
	id, err := idFromPG("auth_provider.id", row.ID)
	if err != nil {
		return domain.AuthProvider{}, err
	}
	userID, err := idFromPG("auth_provider.user_id", row.UserID)
	if err != nil {
		return domain.AuthProvider{}, err
	}
	provider, err := domain.ParseIdentityProviderName(row.Provider)
	if err != nil {
		return domain.AuthProvider{}, fromStored(err)
	}
	return domain.AuthProvider{
		ID: id, UserID: userID, Provider: provider, ProviderSubject: row.ProviderSubject, CreatedAt: timeFromPG(row.CreatedAt),
	}, nil
}
```

แก้ `internal/adapter/postgres/store.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
func (s *Store) Users() port.UserRepository { return &UserRepository{q: sqlc.New(s.pool)} }
```

   ด้วย

```go
func (s *Store) Users() port.UserRepository { return &UserRepository{q: sqlc.New(s.pool)} }

// Sessions returns the session repository over the pool.
func (s *Store) Sessions() port.SessionRepository { return &SessionRepository{q: sqlc.New(s.pool)} }

// Identities returns the identity repository over the pool.
func (s *Store) Identities() port.IdentityRepository {
	return &IdentityRepository{q: sqlc.New(s.pool)}
}
```

2. แทน

```go
func (r *txRepositories) Users() port.UserRepository { return &UserRepository{q: r.q, inTx: true} }
```

   ด้วย

```go
func (r *txRepositories) Users() port.UserRepository { return &UserRepository{q: r.q, inTx: true} }

// Sessions returns the session repository bound to this transaction.
func (r *txRepositories) Sessions() port.SessionRepository { return &SessionRepository{q: r.q} }

// Identities returns the identity repository bound to this transaction.
func (r *txRepositories) Identities() port.IdentityRepository { return &IdentityRepository{q: r.q} }
```

แก้ `internal/core/port/auth.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
	// Revoke reports false when the session was already revoked, so logout audits only once.
```

   ด้วย

```go
	// Revoke reports false when the session was already revoked or does not exist, so logout
	// audits only once.
```

- [ ] **Step 5: รัน tests และ lint ให้ผ่าน**

Run: `gofmt -l internal && make test-integration && make lint`
Expected: `gofmt` ไม่พิมพ์อะไร, `ok  	go-backend-stock/internal/adapter/postgres` โดยไม่มี `SKIP` (ตรวจด้วย `go test -v` ว่า PASS 33 รายการ รวม tests เดิมของ 3a) และ `0 issues.`

- [ ] **Step 6: พิสูจน์ว่า test จับการเขียนผิดได้**

```bash
git add -A internal
```

ใน `internal/adapter/postgres/queries/sessions.sql` แทนชั่วคราว

```sql
WHERE id = $1 AND revoked_at IS NULL;
```

ด้วย

```sql
WHERE id = $1;
```

Run: `make sqlc && go test -count=1 -run TestSessionRevokeReportsOnlyTheFirstRevoke ./internal/adapter/postgres/`
Expected: FAIL ด้วย `second Revoke = true, <nil>; want false so logout audits once`

จากนั้นคืนไฟล์: `git checkout -- internal/adapter/postgres/queries/sessions.sql && make sqlc && git status --short` ต้องไม่แสดงไฟล์ที่ยังไม่ได้ stage (ไม่มี diff เทียบกับ Step 4)

- [ ] **Step 7: Commit**

```bash
git add -A internal
git commit -m "feat(db): add session and identity repositories"
```

---

### Task 3: `OIDCFlowRepository` และ `OTPChallengeRepository`

**Files:**
- Create: `internal/adapter/postgres/queries/oidc.sql`, `internal/adapter/postgres/queries/otp.sql`, `internal/adapter/postgres/oidc.go`, `internal/adapter/postgres/otp.go`
- Test: `internal/adapter/postgres/oidc_test.go`, `internal/adapter/postgres/otp_test.go`
- Modify: `internal/adapter/postgres/store.go`, `internal/core/port/auth.go`, `internal/adapter/postgres/fixtures_test.go`
- Generated by `make sqlc`: `internal/adapter/postgres/sqlc/oidc.sql.go`, `internal/adapter/postgres/sqlc/otp.sql.go`

**Interfaces:**
- Consumes: `insertUser`, `now`, `randomHash` (Tasks 1–2)
- Produces: `Store.OIDCFlows()`, `Store.OTPChallenges()`, `(*txRepositories).OTPChallenges()`; test helpers `runConcurrently(n, call)`, `insertOIDCFlow`, `newChallenge`, `insertChallenge`

- [ ] **Step 1: เขียน failing tests**

`internal/adapter/postgres/oidc_test.go`:

```go
package postgres

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func insertOIDCFlow(t *testing.T, store *Store, purpose domain.OIDCPurpose, userID domain.ID, createdAt time.Time) domain.OIDCFlow {
	t.Helper()
	f, err := domain.NewOIDCFlow(domain.OIDCFlow{
		ID: newID(t), Purpose: purpose, UserID: userID, StateHash: randomHash(t), NonceHash: randomHash(t),
		BrowserBindingHash: randomHash(t), CreatedAt: createdAt, ExpiresAt: createdAt.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.OIDCFlows().Insert(t.Context(), f); err != nil {
		t.Fatalf("insert flow: %v", err)
	}
	return f
}

func TestOIDCFlowConsumeOnce(t *testing.T) {
	store := newRuntimeStore(t)
	f := insertOIDCFlow(t, store, domain.OIDCPurposeLogin, domain.ID{}, now())
	at := now()

	got, err := store.OIDCFlows().Consume(t.Context(), f.StateHash, f.BrowserBindingHash, at)
	if err != nil || got.ID != f.ID || got.Purpose != domain.OIDCPurposeLogin || !got.UserID.IsZero() ||
		!bytes.Equal(got.NonceHash, f.NonceHash) || got.ConsumedAt == nil || !got.ConsumedAt.Equal(at) {
		t.Fatalf("Consume = %+v, %v", got, err)
	}
	if _, err := store.OIDCFlows().Consume(t.Context(), f.StateHash, f.BrowserBindingHash, at); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second Consume error = %v, want not_found (replayed callback)", err)
	}
}

func TestOIDCFlowConsumeRejectsWrongBindingAndExpiry(t *testing.T) {
	store := newRuntimeStore(t)
	f := insertOIDCFlow(t, store, domain.OIDCPurposeLogin, domain.ID{}, now())

	if _, err := store.OIDCFlows().Consume(t.Context(), f.StateHash, randomHash(t), now()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("other browser binding error = %v, want not_found", err)
	}
	if _, err := store.OIDCFlows().Consume(t.Context(), f.StateHash, f.BrowserBindingHash, f.ExpiresAt); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("consume at expiry error = %v, want not_found", err)
	}
	// The failed attempts did not consume it.
	if _, err := store.OIDCFlows().Consume(t.Context(), f.StateHash, f.BrowserBindingHash, now()); err != nil {
		t.Errorf("valid consume after failed attempts: %v", err)
	}
}

func TestOIDCFlowLinkKeepsUser(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	f := insertOIDCFlow(t, store, domain.OIDCPurposeLink, user.ID, now())

	got, err := store.OIDCFlows().Consume(t.Context(), f.StateHash, f.BrowserBindingHash, now())
	if err != nil || got.Purpose != domain.OIDCPurposeLink || got.UserID != user.ID {
		t.Fatalf("Consume(link) = %+v, %v", got, err)
	}
}

func TestOIDCFlowConcurrentConsumeSucceedsOnce(t *testing.T) {
	store := newRuntimeStore(t)
	f := insertOIDCFlow(t, store, domain.OIDCPurposeLogin, domain.ID{}, now())

	succeeded, errs := runConcurrently(10, func() error {
		_, err := store.OIDCFlows().Consume(t.Context(), f.StateHash, f.BrowserBindingHash, now())
		return err
	})

	if succeeded != 1 {
		t.Fatalf("%d concurrent consumes succeeded, want exactly 1", succeeded)
	}
	for _, err := range errs {
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("losing consume error = %v, want not_found", err)
		}
	}
}
```

`internal/adapter/postgres/otp_test.go`:

```go
package postgres

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func newChallenge(t *testing.T, userID domain.ID, purpose domain.OTPPurpose, createdAt time.Time) domain.OTPChallenge {
	t.Helper()
	c, err := domain.NewOTPChallenge(domain.OTPChallenge{
		ID: newID(t), UserID: userID, Purpose: purpose, OTPHash: randomHash(t), CreatedAt: createdAt, ExpiresAt: createdAt.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func insertChallenge(t *testing.T, store *Store, userID domain.ID, purpose domain.OTPPurpose) domain.OTPChallenge {
	t.Helper()
	c := newChallenge(t, userID, purpose, now())
	if err := store.OTPChallenges().Insert(t.Context(), c); err != nil {
		t.Fatalf("insert challenge: %v", err)
	}
	return c
}

func TestOTPChallengeInsertAndReadByPurpose(t *testing.T) {
	store := newRuntimeStore(t)
	c := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposePasswordReset)

	got, err := store.OTPChallenges().ByID(t.Context(), domain.OTPPurposePasswordReset, c.ID)
	if err != nil || got.ID != c.ID || got.UserID != c.UserID || got.Purpose != c.Purpose || !bytes.Equal(got.OTPHash, c.OTPHash) ||
		!got.ExpiresAt.Equal(c.ExpiresAt) || got.FailedAttempts != 0 || got.ConsumedAt != nil || got.InvalidatedAt != nil {
		t.Fatalf("ByID = %+v, %v; want %+v", got, err, c)
	}
	// The same ID never works for the other flow.
	if _, err := store.OTPChallenges().ByID(t.Context(), domain.OTPPurposeEmailVerification, c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ByID(other purpose) error = %v, want not_found", err)
	}
	if err := store.OTPChallenges().Consume(t.Context(), domain.OTPPurposeEmailVerification, c.ID, now()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Consume(other purpose) error = %v, want not_found", err)
	}
}

func TestOTPChallengeLockRequiresTransaction(t *testing.T) {
	store := newRuntimeStore(t)
	c := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposeEmailVerification)

	if _, err := store.OTPChallenges().Lock(t.Context(), c.Purpose, c.ID); !errors.Is(err, domain.ErrInternal) {
		t.Errorf("Lock outside a transaction error = %v, want internal", err)
	}
	if err := store.withinTx(t.Context(), func(r *txRepositories) error {
		got, err := r.OTPChallenges().Lock(t.Context(), c.Purpose, c.ID)
		if err != nil || got.ID != c.ID {
			t.Errorf("Lock in transaction = %+v, %v", got, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestOTPChallengeInvalidateOpenAllowsANewChallenge: the one-open-challenge index forbids a second
// open challenge, so issuing a new code is InvalidateOpen then Insert (expired ones included).
func TestOTPChallengeInvalidateOpenAllowsANewChallenge(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	expired := newChallenge(t, user.ID, domain.OTPPurposeEmailVerification, now().Add(-time.Hour))
	if err := store.OTPChallenges().Insert(t.Context(), expired); err != nil {
		t.Fatal(err)
	}
	otherFlow := insertChallenge(t, store, user.ID, domain.OTPPurposePasswordReset)
	at := now()

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		if _, err := r.Users().Lock(t.Context(), user.ID); err != nil {
			return err
		}
		if err := r.OTPChallenges().InvalidateOpen(t.Context(), user.ID, domain.OTPPurposeEmailVerification, at); err != nil {
			return err
		}
		return r.OTPChallenges().Insert(t.Context(), newChallenge(t, user.ID, domain.OTPPurposeEmailVerification, at))
	})
	if err != nil {
		t.Fatalf("invalidate then insert: %v", err)
	}

	old, _ := store.OTPChallenges().ByID(t.Context(), domain.OTPPurposeEmailVerification, expired.ID)
	if old.InvalidatedAt == nil || !old.InvalidatedAt.Equal(at) {
		t.Errorf("expired challenge invalidated_at = %v, want %v", old.InvalidatedAt, at)
	}
	untouched, _ := store.OTPChallenges().ByID(t.Context(), domain.OTPPurposePasswordReset, otherFlow.ID)
	if untouched.InvalidatedAt != nil {
		t.Error("InvalidateOpen closed a challenge of the other flow")
	}
}

func TestOTPChallengeRecordFailure(t *testing.T) {
	store := newRuntimeStore(t)
	c := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposePasswordReset)

	for range 3 {
		if err := store.OTPChallenges().RecordFailure(t.Context(), c.Purpose, c.ID); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := store.OTPChallenges().ByID(t.Context(), c.Purpose, c.ID)
	if got.FailedAttempts != 3 {
		t.Errorf("failed_attempts = %d, want 3", got.FailedAttempts)
	}
	if err := store.OTPChallenges().RecordFailure(t.Context(), c.Purpose, newID(t)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("RecordFailure(unknown) error = %v, want not_found", err)
	}
}

func TestOTPChallengeConsumeOnlyWhileOpenAndUnexpired(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	c := insertChallenge(t, store, user.ID, domain.OTPPurposePasswordReset)

	if err := store.OTPChallenges().Consume(t.Context(), c.Purpose, c.ID, c.ExpiresAt); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("consume at expiry error = %v, want not_found", err)
	}
	at := now()
	if err := store.OTPChallenges().Consume(t.Context(), c.Purpose, c.ID, at); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if err := store.OTPChallenges().Consume(t.Context(), c.Purpose, c.ID, at); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second Consume error = %v, want not_found", err)
	}
	got, _ := store.OTPChallenges().ByID(t.Context(), c.Purpose, c.ID)
	if got.ConsumedAt == nil || !got.ConsumedAt.Equal(at) {
		t.Errorf("consumed_at = %v, want %v", got.ConsumedAt, at)
	}

	invalidated := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposePasswordReset)
	if err := store.OTPChallenges().InvalidateOpen(t.Context(), invalidated.UserID, invalidated.Purpose, now()); err != nil {
		t.Fatal(err)
	}
	if err := store.OTPChallenges().Consume(t.Context(), invalidated.Purpose, invalidated.ID, now()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Consume(invalidated) error = %v, want not_found", err)
	}
}

// TestOTPChallengeConcurrentConsumeSucceedsOnce: AGENTS.md "concurrent OTP reset" — one code
// resets the password once even when several requests race.
func TestOTPChallengeConcurrentConsumeSucceedsOnce(t *testing.T) {
	store := newRuntimeStore(t)
	c := insertChallenge(t, store, insertUser(t, store).ID, domain.OTPPurposePasswordReset)

	succeeded, errs := runConcurrently(10, func() error {
		return store.OTPChallenges().Consume(t.Context(), c.Purpose, c.ID, now())
	})

	if succeeded != 1 {
		t.Fatalf("%d concurrent consumes succeeded, want exactly 1", succeeded)
	}
	for _, err := range errs {
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("losing consume error = %v, want not_found", err)
		}
	}
}

// TestOTPChallengeConcurrentReissueUnderUserLock: resends that race must not trip the
// one-open-challenge index. Locking the user first serializes them, so every resend succeeds and
// exactly one challenge stays open (the lock order milestone 5 services follow).
func TestOTPChallengeConcurrentReissueUnderUserLock(t *testing.T) {
	store := newRuntimeStore(t)
	user := insertUser(t, store)
	issued := make(chan domain.ID, 10)

	succeeded, errs := runConcurrently(10, func() error {
		return store.withinTx(t.Context(), func(r *txRepositories) error {
			if _, err := r.Users().Lock(t.Context(), user.ID); err != nil {
				return err
			}
			at := now()
			if err := r.OTPChallenges().InvalidateOpen(t.Context(), user.ID, domain.OTPPurposeEmailVerification, at); err != nil {
				return err
			}
			c := newChallenge(t, user.ID, domain.OTPPurposeEmailVerification, at)
			if err := r.OTPChallenges().Insert(t.Context(), c); err != nil {
				return err
			}
			issued <- c.ID
			return nil
		})
	})

	if succeeded != 10 {
		t.Fatalf("%d of 10 concurrent resends succeeded, want all; errors: %v", succeeded, errs)
	}
	close(issued)
	open := 0
	for id := range issued {
		c, err := store.OTPChallenges().ByID(t.Context(), domain.OTPPurposeEmailVerification, id)
		if err != nil {
			t.Fatal(err)
		}
		if c.InvalidatedAt == nil && c.ConsumedAt == nil {
			open++
		}
	}
	if open != 1 {
		t.Errorf("%d challenges left open, want exactly 1", open)
	}
}
```

แก้ `internal/adapter/postgres/fixtures_test.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
	return u
}
```

   ด้วย

```go
	return u
}

// runConcurrently starts n goroutines at once and returns how many calls succeeded.
func runConcurrently(n int, call func() error) (succeeded int, errs []error) {
	start := make(chan struct{})
	results := make(chan error, n)
	for range n {
		go func() {
			<-start
			results <- call()
		}()
	}
	close(start)
	for range n {
		if err := <-results; err != nil {
			errs = append(errs, err)
		} else {
			succeeded++
		}
	}
	return succeeded, errs
}
```

- [ ] **Step 2: รันให้เห็นว่าล้มเหลว**

Run: `make test-integration`
Expected: FAIL ด้วย `[build failed]` และ `store.OIDCFlows undefined`, `store.OTPChallenges undefined` (ยังไม่มี accessor)

- [ ] **Step 3: เขียน queries แล้ว generate**

`internal/adapter/postgres/queries/oidc.sql`:

```sql
-- name: InsertOIDCFlow :exec
INSERT INTO app.oauth_flows (id, purpose, user_id, state_hash, nonce_hash, browser_binding_hash, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- One statement consumes the flow, so two concurrent callbacks cannot both succeed.
-- name: ConsumeOIDCFlow :one
UPDATE app.oauth_flows SET consumed_at = @at
WHERE state_hash = @state_hash AND browser_binding_hash = @browser_binding_hash
  AND consumed_at IS NULL AND expires_at > @at
RETURNING id, purpose, user_id, state_hash, nonce_hash, browser_binding_hash, created_at, expires_at, consumed_at;
```

`internal/adapter/postgres/queries/otp.sql`:

```sql
-- Every query filters by purpose, so a challenge ID from one flow never works in the other.

-- name: GetOTPChallenge :one
SELECT id, user_id, purpose, otp_hash, created_at, expires_at, failed_attempts, consumed_at, invalidated_at
FROM app.otp_challenges WHERE id = $1 AND purpose = $2;

-- name: LockOTPChallenge :one
SELECT id, user_id, purpose, otp_hash, created_at, expires_at, failed_attempts, consumed_at, invalidated_at
FROM app.otp_challenges WHERE id = $1 AND purpose = $2
FOR UPDATE;

-- Closes every open challenge of the flow, expired ones included, before a new one is issued.
-- name: InvalidateOpenOTPChallenges :exec
UPDATE app.otp_challenges SET invalidated_at = $3
WHERE user_id = $1 AND purpose = $2 AND consumed_at IS NULL AND invalidated_at IS NULL;

-- name: InsertOTPChallenge :exec
INSERT INTO app.otp_challenges (id, user_id, purpose, otp_hash, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: RecordOTPFailure :execrows
UPDATE app.otp_challenges SET failed_attempts = failed_attempts + 1
WHERE id = $1 AND purpose = $2;

-- A challenge can be consumed once, and only while it is open and unexpired.
-- name: ConsumeOTPChallenge :execrows
UPDATE app.otp_challenges SET consumed_at = @at
WHERE id = @id AND purpose = @purpose AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at > @at;
```

Run: `make sqlc && ls internal/adapter/postgres/sqlc`
Expected: ไม่มี error และมีไฟล์ `oidc.sql.go`, `otp.sql.go` เพิ่ม

- [ ] **Step 4: เขียน repository และ accessor**

`internal/adapter/postgres/oidc.go`:

```go
package postgres

import (
	"context"
	"time"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// OIDCFlowRepository stores pending Google sign-in and account-link flows.
type OIDCFlowRepository struct {
	q *sqlc.Queries
}

var _ port.OIDCFlowRepository = (*OIDCFlowRepository)(nil)

// Insert stores a new flow; build it with domain.NewOIDCFlow.
func (r *OIDCFlowRepository) Insert(ctx context.Context, f domain.OIDCFlow) error {
	return mapError(r.q.InsertOIDCFlow(ctx, sqlc.InsertOIDCFlowParams{
		ID:                 pgUUID(f.ID),
		Purpose:            string(f.Purpose),
		UserID:             pgUUID(f.UserID),
		StateHash:          f.StateHash,
		NonceHash:          f.NonceHash,
		BrowserBindingHash: f.BrowserBindingHash,
		CreatedAt:          pgTime(f.CreatedAt),
		ExpiresAt:          pgTime(f.ExpiresAt),
	}))
}

// Consume marks the flow matching both hashes as consumed in one statement, so only one of two
// concurrent callbacks succeeds. An unknown, expired or already consumed flow, or a different
// browser binding, returns domain.ErrNotFound.
func (r *OIDCFlowRepository) Consume(ctx context.Context, stateHash, browserBindingHash []byte, at time.Time) (domain.OIDCFlow, error) {
	row, err := r.q.ConsumeOIDCFlow(ctx, sqlc.ConsumeOIDCFlowParams{
		At: pgTime(at), StateHash: stateHash, BrowserBindingHash: browserBindingHash,
	})
	if err != nil {
		return domain.OIDCFlow{}, mapError(err)
	}
	return oidcFlowFromRow(row)
}

func oidcFlowFromRow(row sqlc.AppOauthFlow) (domain.OIDCFlow, error) {
	id, err := idFromPG("oidc_flow.id", row.ID)
	if err != nil {
		return domain.OIDCFlow{}, err
	}
	userID, err := optionalIDFromPG("oidc_flow.user_id", row.UserID)
	if err != nil {
		return domain.OIDCFlow{}, err
	}
	purpose, err := domain.ParseOIDCPurpose(row.Purpose)
	if err != nil {
		return domain.OIDCFlow{}, fromStored(err)
	}
	return domain.OIDCFlow{
		ID:                 id,
		Purpose:            purpose,
		UserID:             userID,
		StateHash:          row.StateHash,
		NonceHash:          row.NonceHash,
		BrowserBindingHash: row.BrowserBindingHash,
		CreatedAt:          timeFromPG(row.CreatedAt),
		ExpiresAt:          timeFromPG(row.ExpiresAt),
		ConsumedAt:         optionalTimeFromPG(row.ConsumedAt),
	}, nil
}
```

`internal/adapter/postgres/otp.go`:

```go
package postgres

import (
	"context"
	"time"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// OTPChallengeRepository stores email-verification and password-reset challenges. Every query
// filters by purpose.
type OTPChallengeRepository struct {
	q    *sqlc.Queries
	inTx bool
}

var _ port.OTPChallengeRepository = (*OTPChallengeRepository)(nil)

// ByID returns the challenge of this purpose, or domain.ErrNotFound.
func (r *OTPChallengeRepository) ByID(ctx context.Context, purpose domain.OTPPurpose, id domain.ID) (domain.OTPChallenge, error) {
	row, err := r.q.GetOTPChallenge(ctx, sqlc.GetOTPChallengeParams{ID: pgUUID(id), Purpose: string(purpose)})
	if err != nil {
		return domain.OTPChallenge{}, mapError(err)
	}
	return otpChallengeFromRow(row)
}

// Lock reads the challenge with FOR UPDATE; it only works inside a transaction.
func (r *OTPChallengeRepository) Lock(ctx context.Context, purpose domain.OTPPurpose, id domain.ID) (domain.OTPChallenge, error) {
	if !r.inTx {
		return domain.OTPChallenge{}, errLockOutsideTx
	}
	row, err := r.q.LockOTPChallenge(ctx, sqlc.LockOTPChallengeParams{ID: pgUUID(id), Purpose: string(purpose)})
	if err != nil {
		return domain.OTPChallenge{}, mapError(err)
	}
	return otpChallengeFromRow(row)
}

// InvalidateOpen closes userID's open challenges of this purpose, expired ones included. The
// caller holds the user's row lock, so two concurrent requests cannot both pass this step and
// then collide on the one-open-challenge index.
func (r *OTPChallengeRepository) InvalidateOpen(ctx context.Context, userID domain.ID, purpose domain.OTPPurpose, at time.Time) error {
	return mapError(r.q.InvalidateOpenOTPChallenges(ctx, sqlc.InvalidateOpenOTPChallengesParams{
		UserID: pgUUID(userID), Purpose: string(purpose), InvalidatedAt: pgTime(at),
	}))
}

// Insert stores a new challenge; build it with domain.NewOTPChallenge after InvalidateOpen.
func (r *OTPChallengeRepository) Insert(ctx context.Context, c domain.OTPChallenge) error {
	return mapError(r.q.InsertOTPChallenge(ctx, sqlc.InsertOTPChallengeParams{
		ID:        pgUUID(c.ID),
		UserID:    pgUUID(c.UserID),
		Purpose:   string(c.Purpose),
		OtpHash:   c.OTPHash,
		CreatedAt: pgTime(c.CreatedAt),
		ExpiresAt: pgTime(c.ExpiresAt),
	}))
}

// RecordFailure adds one failed attempt atomically, or returns domain.ErrNotFound.
func (r *OTPChallengeRepository) RecordFailure(ctx context.Context, purpose domain.OTPPurpose, id domain.ID) error {
	n, err := r.q.RecordOTPFailure(ctx, sqlc.RecordOTPFailureParams{ID: pgUUID(id), Purpose: string(purpose)})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Consume marks the challenge used. It succeeds once, and only while the challenge is open and
// unexpired at `at`; otherwise it returns domain.ErrNotFound.
func (r *OTPChallengeRepository) Consume(ctx context.Context, purpose domain.OTPPurpose, id domain.ID, at time.Time) error {
	n, err := r.q.ConsumeOTPChallenge(ctx, sqlc.ConsumeOTPChallengeParams{ID: pgUUID(id), Purpose: string(purpose), At: pgTime(at)})
	if err != nil {
		return mapError(err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func otpChallengeFromRow(row sqlc.AppOtpChallenge) (domain.OTPChallenge, error) {
	id, err := idFromPG("otp_challenge.id", row.ID)
	if err != nil {
		return domain.OTPChallenge{}, err
	}
	userID, err := idFromPG("otp_challenge.user_id", row.UserID)
	if err != nil {
		return domain.OTPChallenge{}, err
	}
	purpose, err := domain.ParseOTPPurpose(row.Purpose)
	if err != nil {
		return domain.OTPChallenge{}, fromStored(err)
	}
	return domain.OTPChallenge{
		ID:             id,
		UserID:         userID,
		Purpose:        purpose,
		OTPHash:        row.OtpHash,
		CreatedAt:      timeFromPG(row.CreatedAt),
		ExpiresAt:      timeFromPG(row.ExpiresAt),
		FailedAttempts: int(row.FailedAttempts),
		ConsumedAt:     optionalTimeFromPG(row.ConsumedAt),
		InvalidatedAt:  optionalTimeFromPG(row.InvalidatedAt),
	}, nil
}
```

แก้ `internal/adapter/postgres/store.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
func (s *Store) Identities() port.IdentityRepository {
	return &IdentityRepository{q: sqlc.New(s.pool)}
}
```

   ด้วย

```go
func (s *Store) Identities() port.IdentityRepository {
	return &IdentityRepository{q: sqlc.New(s.pool)}
}

// OTPChallenges returns the OTP challenge repository over the pool.
func (s *Store) OTPChallenges() port.OTPChallengeRepository {
	return &OTPChallengeRepository{q: sqlc.New(s.pool)}
}

// OIDCFlows returns the OIDC flow repository. Its Consume is a single statement, so it needs no
// surrounding transaction.
func (s *Store) OIDCFlows() port.OIDCFlowRepository {
	return &OIDCFlowRepository{q: sqlc.New(s.pool)}
}
```

2. แทน

```go
func (r *txRepositories) Identities() port.IdentityRepository { return &IdentityRepository{q: r.q} }
```

   ด้วย

```go
func (r *txRepositories) Identities() port.IdentityRepository { return &IdentityRepository{q: r.q} }

// OTPChallenges returns the OTP challenge repository bound to this transaction.
func (r *txRepositories) OTPChallenges() port.OTPChallengeRepository {
	return &OTPChallengeRepository{q: r.q, inTx: true}
}
```

แก้ `internal/core/port/auth.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
// domain.ErrNotFound for an unknown or other-purpose challenge; Consume also does so for a
// challenge that is no longer open.
```

   ด้วย

```go
// domain.ErrNotFound for an unknown or other-purpose challenge; Consume also does so for a
// challenge that is consumed, invalidated or expired at `at`.
```

- [ ] **Step 5: รัน tests และ lint ให้ผ่าน**

Run: `gofmt -l internal && make test-integration && make lint`
Expected: `gofmt` ไม่พิมพ์อะไร, `ok  	go-backend-stock/internal/adapter/postgres` โดยไม่มี `SKIP` (ตรวจด้วย `go test -v` ว่า PASS 44 รายการ รวม tests เดิมของ 3a) และ `0 issues.`

- [ ] **Step 6: พิสูจน์ว่า test จับการเขียนผิดได้**

```bash
git add -A internal
```

ใน `internal/adapter/postgres/queries/otp.sql` แทนชั่วคราว

```sql
WHERE id = @id AND purpose = @purpose AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at > @at;
```

ด้วย

```sql
WHERE id = @id AND purpose = @purpose AND invalidated_at IS NULL AND expires_at > @at;
```

Run: `make sqlc && go test -count=1 -run TestOTPChallengeConcurrentConsumeSucceedsOnce ./internal/adapter/postgres/`
Expected: FAIL ด้วย `10 concurrent consumes succeeded, want exactly 1`

จากนั้นคืนไฟล์: `git checkout -- internal/adapter/postgres/queries/otp.sql && make sqlc && git status --short` ต้องไม่แสดงไฟล์ที่ยังไม่ได้ stage (ไม่มี diff เทียบกับ Step 4)

- [ ] **Step 7: Commit**

```bash
git add -A internal
git commit -m "feat(db): add OIDC flow and OTP challenge repositories"
```

---

### Task 4: `RateLimitRepository.Admit`

**Files:**
- Create: `internal/adapter/postgres/queries/ratelimit.sql`, `internal/adapter/postgres/ratelimit.go`
- Test: `internal/adapter/postgres/ratelimit_test.go`
- Modify: `internal/adapter/postgres/store.go`
- Generated by `make sqlc`: `internal/adapter/postgres/sqlc/ratelimit.sql.go`

**Interfaces:**
- Consumes: `now`, `randomHash`, `runConcurrently` (Tasks 1–3); `domain.NewRateLimitRule`, `domain.RateLimitDecision` (milestone 2)
- Produces: `Store.RateLimits()` — `Admit` ไม่อยู่ใน `txRepositories` เพราะทำ transaction ของตัวเอง (phase 1 spec หัวข้อ 8)

- [ ] **Step 1: เขียน failing tests**

`internal/adapter/postgres/ratelimit_test.go`:

```go
package postgres

import (
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func rule(t *testing.T, scope domain.RateLimitScope, key []byte, limit int, window time.Duration) domain.RateLimitRule {
	t.Helper()
	r, err := domain.NewRateLimitRule(domain.RateLimitRule{Scope: scope, KeyHash: key, Limit: limit, Window: window})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func admit(t *testing.T, store *Store, at time.Time, rules ...domain.RateLimitRule) domain.RateLimitDecision {
	t.Helper()
	d, err := store.RateLimits().Admit(t.Context(), rules, at)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	return d
}

func TestRateLimitAllowsUpToLimitThenDenies(t *testing.T) {
	store := newRuntimeStore(t)
	r := rule(t, domain.RateLimitLoginIP, randomHash(t), 3, time.Hour)
	start := now()

	for i := range 3 {
		if d := admit(t, store, start.Add(time.Duration(i)*time.Minute), r); !d.Allowed {
			t.Fatalf("attempt %d denied, want allowed", i+1)
		}
	}
	at := start.Add(10 * time.Minute)
	d := admit(t, store, at, r)
	if d.Allowed {
		t.Fatal("4th attempt within the hour allowed, want denied")
	}
	// The oldest attempt (at start) leaves the window at start+1h.
	if want := start.Add(time.Hour).Sub(at); d.RetryAfter != want {
		t.Errorf("RetryAfter = %v, want %v", d.RetryAfter, want)
	}
	// Once the oldest attempt leaves the window, one more attempt fits.
	if d := admit(t, store, start.Add(time.Hour), r); !d.Allowed {
		t.Error("attempt after the oldest left the window denied, want allowed")
	}
}

// TestRateLimitDeniedAttemptsAreNotCounted: an attacker hammering a denied key does not push the
// window forward, and a user who waits is let back in on time.
func TestRateLimitDeniedAttemptsAreNotCounted(t *testing.T) {
	store := newRuntimeStore(t)
	r := rule(t, domain.RateLimitLoginEmail, randomHash(t), 1, time.Minute)
	start := now()

	admit(t, store, start, r)
	for i := 1; i <= 5; i++ {
		if admit(t, store, start.Add(time.Duration(i)*time.Second), r).Allowed {
			t.Fatal("denied key allowed within the window")
		}
	}
	if d := admit(t, store, start.Add(time.Minute), r); !d.Allowed {
		t.Fatalf("attempt after the window denied (RetryAfter %v): denied attempts were counted", d.RetryAfter)
	}
}

// TestRateLimitCooldownAndHourlyQuotaShareOneKey: *_request_email has a 60 s cooldown and 5 per
// hour on the same key (phase 1 spec section 7).
func TestRateLimitCooldownAndHourlyQuotaShareOneKey(t *testing.T) {
	store := newRuntimeStore(t)
	key := randomHash(t)
	cooldown := rule(t, domain.RateLimitPasswordResetRequestEmail, key, 1, time.Minute)
	hourly := rule(t, domain.RateLimitPasswordResetRequestEmail, key, 5, time.Hour)
	start := now()

	if !admit(t, store, start, cooldown, hourly).Allowed {
		t.Fatal("first request denied")
	}
	d := admit(t, store, start.Add(10*time.Second), cooldown, hourly)
	if d.Allowed || d.RetryAfter != 50*time.Second {
		t.Fatalf("request inside the cooldown = %+v, want denied with RetryAfter 50s", d)
	}
	for i := 1; i <= 4; i++ {
		if !admit(t, store, start.Add(time.Duration(i)*time.Minute), cooldown, hourly).Allowed {
			t.Fatalf("request %d after each cooldown denied", i+1)
		}
	}
	d = admit(t, store, start.Add(5*time.Minute), cooldown, hourly)
	if d.Allowed || d.RetryAfter != 55*time.Minute {
		t.Fatalf("6th request in the hour = %+v, want denied until the first leaves the hour (55m)", d)
	}
}

func TestRateLimitRecordsDenialAuditOncePerMinute(t *testing.T) {
	store := newRuntimeStore(t)
	r := rule(t, domain.RateLimitSignupIP, randomHash(t), 1, time.Hour)
	start := now()
	admit(t, store, start, r)

	if d := admit(t, store, start.Add(time.Second), r); d.Allowed || !d.RecordDenialAudit {
		t.Fatalf("first denial = %+v, want RecordDenialAudit", d)
	}
	if d := admit(t, store, start.Add(30*time.Second), r); d.Allowed || d.RecordDenialAudit {
		t.Fatalf("second denial within a minute = %+v, want no audit", d)
	}
	if d := admit(t, store, start.Add(time.Second+time.Minute), r); d.Allowed || !d.RecordDenialAudit {
		t.Fatalf("denial a minute later = %+v, want RecordDenialAudit again", d)
	}
}

func TestRateLimitKeysAreIndependent(t *testing.T) {
	store := newRuntimeStore(t)
	a := rule(t, domain.RateLimitLoginIP, randomHash(t), 1, time.Hour)
	b := rule(t, domain.RateLimitLoginIP, randomHash(t), 1, time.Hour)
	start := now()
	admit(t, store, start, a)

	if !admit(t, store, start, b).Allowed {
		t.Fatal("a full key blocked a different key")
	}
	// A request checked against both keys is denied when either is full, and records nothing.
	if admit(t, store, start.Add(time.Second), a, rule(t, domain.RateLimitLoginEmail, randomHash(t), 5, time.Hour)).Allowed {
		t.Fatal("request allowed although one of its keys is full")
	}
}

// TestRateLimitConcurrentAdmissionsRespectTheLimit: 20 simultaneous requests against a limit of 5
// admit exactly 5, because each admission locks the key row.
func TestRateLimitConcurrentAdmissionsRespectTheLimit(t *testing.T) {
	store := newRuntimeStore(t)
	r := rule(t, domain.RateLimitPasswordResetConfirmIP, randomHash(t), 5, time.Hour)
	at := now()

	allowed, errs := runConcurrently(20, func() error {
		d, err := store.RateLimits().Admit(t.Context(), []domain.RateLimitRule{r}, at)
		if err != nil {
			return err
		}
		if !d.Allowed {
			return errors.New("denied")
		}
		return nil
	})

	if allowed != 5 {
		t.Fatalf("%d of 20 concurrent requests allowed, want exactly 5", allowed)
	}
	for _, err := range errs {
		if err.Error() != "denied" {
			t.Errorf("unexpected error: %v", err)
		}
	}
}

func TestRateLimitRejectsInvalidRuleSets(t *testing.T) {
	store := newRuntimeStore(t)
	if _, err := store.RateLimits().Admit(t.Context(), nil, now()); !errors.Is(err, domain.ErrInternal) {
		t.Errorf("Admit(no rules) error = %v, want internal", err)
	}
	key := randomHash(t)
	twoScopes := []domain.RateLimitRule{
		rule(t, domain.RateLimitLoginIP, key, 5, time.Hour),
		rule(t, domain.RateLimitSignupIP, key, 5, time.Hour),
	}
	if _, err := store.RateLimits().Admit(t.Context(), twoScopes, now()); !errors.Is(err, domain.ErrInternal) {
		t.Errorf("Admit(one key, two scopes) error = %v, want internal", err)
	}
}
```

- [ ] **Step 2: รันให้เห็นว่าล้มเหลว**

Run: `make test-integration`
Expected: FAIL ด้วย `[build failed]` และ `store.RateLimits undefined` (ยังไม่มี accessor)

- [ ] **Step 3: เขียน queries แล้ว generate**

`internal/adapter/postgres/queries/ratelimit.sql`:

```sql
-- Upserting the key takes its row lock, which serializes concurrent admissions for that key.
-- name: UpsertRateLimitKey :exec
INSERT INTO app.rate_limit_keys (key_hash, scope, expires_at)
VALUES ($1, $2, $3)
ON CONFLICT (key_hash) DO UPDATE SET expires_at = GREATEST(app.rate_limit_keys.expires_at, EXCLUDED.expires_at);

-- name: CountRateLimitEvents :one
SELECT count(*) FROM app.rate_limit_events WHERE key_hash = $1 AND occurred_at > $2;

-- The event that must leave the window before one more attempt fits: the (n+1)-th oldest.
-- name: NthRateLimitEventTime :one
SELECT occurred_at FROM app.rate_limit_events
WHERE key_hash = $1 AND occurred_at > $2
ORDER BY occurred_at, id
OFFSET $3 LIMIT 1;

-- name: InsertRateLimitEvent :exec
INSERT INTO app.rate_limit_events (key_hash, occurred_at) VALUES ($1, $2);

-- Returns a row only for the first denial in a minute per key, so attacks cannot flood audit.
-- name: ClaimRateLimitDenialAudit :execrows
UPDATE app.rate_limit_keys SET last_denial_audit_at = @now
WHERE key_hash = @key_hash AND (last_denial_audit_at IS NULL OR last_denial_audit_at <= @audit_before);
```

Run: `make sqlc && ls internal/adapter/postgres/sqlc`
Expected: ไม่มี error และมีไฟล์ `ratelimit.sql.go` เพิ่ม

- [ ] **Step 4: เขียน repository และ accessor**

`internal/adapter/postgres/ratelimit.go`:

```go
package postgres

import (
	"bytes"
	"context"
	"slices"
	"time"

	"go-backend-stock/internal/adapter/postgres/sqlc"
	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// denialAuditInterval is how often a key may report a denial for a security.rate_limited audit
// event (phase 1 spec section 9: one event per key per minute).
const denialAuditInterval = time.Minute

// RateLimitRepository admits requests against rolling-window limits stored in PostgreSQL, so a
// restart cannot reset a quota.
type RateLimitRepository struct {
	store *Store
}

var _ port.RateLimitRepository = (*RateLimitRepository)(nil)

type rateLimitKey struct {
	hash      []byte
	scope     domain.RateLimitScope
	maxWindow time.Duration
}

// Admit checks every rule in one transaction. When all allow the request it records one event per
// key; when any denies it records nothing (admission limit: denied attempts do not extend the
// wait), reports when the request would fit, and sets RecordDenialAudit for the first denial of a
// key in a minute. now comes from the service's Clock, never from the database.
func (r *RateLimitRepository) Admit(ctx context.Context, rules []domain.RateLimitRule, now time.Time) (domain.RateLimitDecision, error) {
	keys, err := rateLimitKeys(rules)
	if err != nil {
		return domain.RateLimitDecision{}, err
	}
	var decision domain.RateLimitDecision
	err = r.store.withinTx(ctx, func(tx *txRepositories) error {
		// Upserting each key takes its row lock; keys are always locked in key_hash order so two
		// requests that share several keys cannot deadlock.
		for _, k := range keys {
			if err := tx.q.UpsertRateLimitKey(ctx, sqlc.UpsertRateLimitKeyParams{
				KeyHash: k.hash, Scope: string(k.scope), ExpiresAt: pgTime(now.Add(k.maxWindow)),
			}); err != nil {
				return mapError(err)
			}
		}

		var denied [][]byte
		var retryAfter time.Duration
		for _, rule := range rules {
			cutoff := pgTime(now.Add(-rule.Window))
			count, err := tx.q.CountRateLimitEvents(ctx, sqlc.CountRateLimitEventsParams{KeyHash: rule.KeyHash, OccurredAt: cutoff})
			if err != nil {
				return mapError(err)
			}
			if count < int64(rule.Limit) {
				continue
			}
			// One more attempt fits once the (count-limit)-th oldest event leaves the window.
			oldest, err := tx.q.NthRateLimitEventTime(ctx, sqlc.NthRateLimitEventTimeParams{
				KeyHash: rule.KeyHash, OccurredAt: cutoff, Offset: int32(count - int64(rule.Limit)), //nolint:gosec // count is a row count of one key's window, far below MaxInt32.
			})
			if err != nil {
				return mapError(err)
			}
			retryAfter = max(retryAfter, timeFromPG(oldest).Add(rule.Window).Sub(now))
			if !slices.ContainsFunc(denied, func(h []byte) bool { return bytes.Equal(h, rule.KeyHash) }) {
				denied = append(denied, rule.KeyHash)
			}
		}

		if len(denied) == 0 {
			for _, k := range keys {
				if err := tx.q.InsertRateLimitEvent(ctx, sqlc.InsertRateLimitEventParams{KeyHash: k.hash, OccurredAt: pgTime(now)}); err != nil {
					return mapError(err)
				}
			}
			decision = domain.RateLimitDecision{Allowed: true}
			return nil
		}

		decision = domain.RateLimitDecision{RetryAfter: retryAfter}
		for _, hash := range denied {
			claimed, err := tx.q.ClaimRateLimitDenialAudit(ctx, sqlc.ClaimRateLimitDenialAuditParams{
				KeyHash: hash, Now: pgTime(now), AuditBefore: pgTime(now.Add(-denialAuditInterval)),
			})
			if err != nil {
				return mapError(err)
			}
			if claimed > 0 {
				decision.RecordDenialAudit = true
			}
		}
		return nil
	})
	if err != nil {
		return domain.RateLimitDecision{}, err
	}
	return decision, nil
}

// rateLimitKeys returns each distinct key once, sorted by hash, with the longest window of its
// rules. Rules come from domain.NewRateLimitRule; a key used with two scopes is a bug.
func rateLimitKeys(rules []domain.RateLimitRule) ([]rateLimitKey, error) {
	if len(rules) == 0 {
		return nil, domain.NewError(domain.CodeInternal, "rate limit needs at least one rule")
	}
	var keys []rateLimitKey
	for _, rule := range rules {
		i := slices.IndexFunc(keys, func(k rateLimitKey) bool { return bytes.Equal(k.hash, rule.KeyHash) })
		switch {
		case i < 0:
			keys = append(keys, rateLimitKey{hash: rule.KeyHash, scope: rule.Scope, maxWindow: rule.Window})
		case keys[i].scope != rule.Scope:
			return nil, domain.NewError(domain.CodeInternal, "rate limit key is used with two scopes")
		default:
			keys[i].maxWindow = max(keys[i].maxWindow, rule.Window)
		}
	}
	slices.SortFunc(keys, func(a, b rateLimitKey) int { return bytes.Compare(a.hash, b.hash) })
	return keys, nil
}
```

แก้ `internal/adapter/postgres/store.go` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```go
func (s *Store) OIDCFlows() port.OIDCFlowRepository {
	return &OIDCFlowRepository{q: sqlc.New(s.pool)}
}
```

   ด้วย

```go
func (s *Store) OIDCFlows() port.OIDCFlowRepository {
	return &OIDCFlowRepository{q: sqlc.New(s.pool)}
}

// RateLimits returns the rate-limit repository. Admit runs its own transaction.
func (s *Store) RateLimits() port.RateLimitRepository { return &RateLimitRepository{store: s} }
```

- [ ] **Step 5: รัน tests และ lint ให้ผ่าน**

Run: `gofmt -l internal && make test-integration && make lint`
Expected: `gofmt` ไม่พิมพ์อะไร, `ok  	go-backend-stock/internal/adapter/postgres` โดยไม่มี `SKIP` (ตรวจด้วย `go test -v` ว่า PASS 51 รายการ รวม tests เดิมของ 3a) และ `0 issues.`

- [ ] **Step 6: พิสูจน์ว่า test จับการเขียนผิดได้**

```bash
git add -A internal
```

ใน `internal/adapter/postgres/queries/ratelimit.sql` แทนชั่วคราว

```sql
ON CONFLICT (key_hash) DO UPDATE SET expires_at = GREATEST(app.rate_limit_keys.expires_at, EXCLUDED.expires_at);
```

ด้วย

```sql
ON CONFLICT (key_hash) DO NOTHING;
```

Run: `make sqlc && go test -count=1 -run TestRateLimitConcurrentAdmissionsRespectTheLimit ./internal/adapter/postgres/`
Expected: FAIL ด้วย `... of 20 concurrent requests allowed, want exactly 5`

จากนั้นคืนไฟล์: `git checkout -- internal/adapter/postgres/queries/ratelimit.sql && make sqlc && git status --short` ต้องไม่แสดงไฟล์ที่ยังไม่ได้ stage (ไม่มี diff เทียบกับ Step 4)

- [ ] **Step 7: Commit**

```bash
git add -A internal
git commit -m "feat(db): add rate-limit repository with admission limit"
```

---

### Task 5: เอกสาร — spec, roadmap

**Files:**
- Modify: `docs/superpowers/specs/2026-10-17-milestone-3-persistence-design.md`, `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md`

**Interfaces:**
- Consumes: พฤติกรรมและไฟล์จาก Tasks 1–4
- Produces: เอกสารที่ตรงกับโค้ด (AGENTS.md)

- [ ] **Step 1: แก้ spec ของ milestone 3**

แก้ `docs/superpowers/specs/2026-10-17-milestone-3-persistence-design.md` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```text
แผน 3a ผ่านการตรวจรับ 2026-10-20: [2026-10-17-milestone-3a-schema-sqlc-audit.md](../plans/2026-10-17-milestone-3a-schema-sqlc-audit.md)
```

   ด้วย

```text
แผน 3a ผ่านการตรวจรับ 2026-10-20: [2026-10-17-milestone-3a-schema-sqlc-audit.md](../plans/2026-10-17-milestone-3a-schema-sqlc-audit.md); แผน 3b: [2026-10-20-milestone-3b-auth-repositories.md](../plans/2026-10-20-milestone-3b-auth-repositories.md)
```

2. แทน

```text
| `RateLimitRepository` | `Admit` ดูหัวข้อ 5.1 |
```

   ด้วย

```text
| `RateLimitRepository` | `Admit` ดูหัวข้อ 5.1 |

รายละเอียดที่ตัดสินในแผน 3b (2026-10-20):

- **conflict ไม่ทำให้ transaction จบ** (ข้อส่งต่อจากรีวิว 3a): `UserRepository.Insert` และ `IdentityRepository.Insert` ใช้ `INSERT ... ON CONFLICT DO NOTHING` แล้วตรวจจำนวนแถว ศูนย์แถว → `ErrIdentityConflict` โดย transaction ยังเขียนต่อได้ (service บันทึก audit ใน callback เดิมได้); การแปลง `users_email_key`/`auth_providers_*` ใน `mapError` คงไว้เป็นทางสำรอง
- **OTP `Consume` ต้องยังไม่หมดอายุ** (`expires_at > at`) เหมือน OIDC `Consume`; comment ของ port อัปเดตให้ตรง
- `Revoke` ของ session ที่ไม่มีอยู่คืน `false`; `SetPassword` ที่ hash ว่าง → `internal`; `Admit` ที่ไม่มี rule หรือ key เดียวใช้กับสอง scope → `internal`
- `Lock*` บน repository จาก pool คืน `internal` (`errLockOutsideTx`) ก่อนส่ง query
- **ลำดับ lock ตอนออก OTP ใหม่:** service ต้อง `Users().Lock` ก่อน `InvalidateOpen` + `Insert` ในทุก flow (ข้อส่งต่อจากรีวิว 3a); ไม่เช่นนั้นคำขอพร้อมกันชน `otp_challenges_one_open_idx`
```

3. แทน

```text
2. นับ `rate_limit_events` ในช่วง window ของแต่ละ rule;
```

   ด้วย

```text
2. นับ `rate_limit_events` ที่ `occurred_at > now − window` ของแต่ละ rule;
```

4. แทน

```text
4. ไม่ผ่าน → ไม่ insert event; `RetryAfter` = เวลาที่เร็วที่สุดที่ทุก rule ที่ปฏิเสธจะผ่าน;
```

   ด้วย

```text
4. ไม่ผ่าน → ไม่ insert event; `RetryAfter` = เวลาที่เร็วที่สุดที่ทุก rule ที่ปฏิเสธจะผ่าน (ของแต่ละ rule คือเวลาที่ event ลำดับที่ count − limit + 1 นับจากเก่าสุดออกจาก window);
```

5. แทน

```text
- `SessionRepository.Revoke` สองครั้ง → `true` แล้ว `false`
```

   ด้วย

```text
- `SessionRepository.Revoke` สองครั้ง → `true` แล้ว `false`
- ออก OTP ใหม่ของผู้ใช้เดียวกันจาก 10 goroutine โดย `Users().Lock` ก่อน `InvalidateOpen` + `Insert` → สำเร็จทั้ง 10 และเหลือ challenge ที่เปิดอยู่หนึ่งรายการ
```

- [ ] **Step 2: แก้ roadmap**

แก้ `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md` ตามคู่ต่อไปนี้ (ข้อความเดิมต้องเจอครั้งเดียวพอดี):

1. แทน

```text
ขั้นต่อไปคือแผน 3b
```

   ด้วย

```text
แผน 3b ลงมือครบแล้ว รอผู้ใช้ตรวจรับ จากนั้นแผน 3c
```

2. แทน

```text
3b (repositories ฝั่ง auth)
```

   ด้วย

```text
[3b](2026-10-20-milestone-3b-auth-repositories.md) (repositories ฝั่ง auth)
```

3. แทน

```text
(3a ทำแล้ว: sqlc + mapper + `AuditRepository`; ยังเหลือ repositories ฝั่ง auth ใน 3b และฝั่งพอร์ตใน 3c)
```

   ด้วย

```text
(3a ทำแล้ว: sqlc + mapper + `AuditRepository`; 3b ทำแล้ว: repositories ฝั่ง auth ทั้ง 6 ตัว; ยังเหลือฝั่งพอร์ตใน 3c)
```

4. แทน

```text
และ service ที่ต้องบันทึก audit หลัง conflict ต้องทำใน transaction ใหม่
```

   ด้วย

```text
และ service ที่ต้องบันทึก audit หลัง conflict ต้องทำใน transaction ใหม่ — **3b:** users/auth_providers ใช้ `ON CONFLICT DO NOTHING` แล้ว; 3c ยังต้องตัดสินกับ portfolios และ idempotency_keys
```

5. แทน

```text
ให้ lock แถว user ก่อน `InvalidateOpen` + `Insert` ตาม phase 1 spec หัวข้อ 9
```

   ด้วย

```text
ให้ lock แถว user ก่อน `InvalidateOpen` + `Insert` ตาม phase 1 spec หัวข้อ 9 — **3b:** `TestOTPChallengeConcurrentReissueUnderUserLock` พิสูจน์ลำดับ lock นี้ที่ระดับ repository แล้ว; service ของ milestone 5 ต้องทำตาม
```

- [ ] **Step 3: ตรวจทั้ง repo**

Run: `make check && make test-integration`
Expected: ผ่านทั้งคู่ (`0 issues.`, swagger valid, `ok` ทุกแพ็กเกจ, ไม่มี diff ของ `internal/adapter/postgres/sqlc` และไม่มี `SKIP`)

- [ ] **Step 4: Commit**

```bash
git add docs
git commit -m "docs: record milestone 3b auth repositories"
```
