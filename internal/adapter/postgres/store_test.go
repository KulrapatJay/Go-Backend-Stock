package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// newRuntimeStore returns a Store connected as app_runtime, the role the API uses.
func newRuntimeStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(openPool(t, "TEST_RUNTIME_DATABASE_URL"))
}

func newID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func auditEvent(t *testing.T, metadata string) domain.AuditEvent {
	t.Helper()
	e, err := domain.NewAuditEvent(domain.AuditEvent{
		ID: newID(t), OccurredAt: time.Now().UTC().Truncate(time.Microsecond), EventType: domain.EventPortfolioCreated,
		ActorUserID: newID(t), RequestID: newID(t), TargetType: "portfolio", TargetID: newID(t).String(),
		Outcome: domain.AuditOutcomeSuccess, Metadata: json.RawMessage(metadata),
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// auditExists reads with the admin role: the runtime role cannot SELECT audit_events.
func auditExists(t *testing.T, id domain.ID) bool {
	t.Helper()
	var n int
	if err := openPool(t, "TEST_ADMIN_DATABASE_URL").QueryRow(t.Context(),
		`SELECT count(*) FROM app.audit_events WHERE id = $1`, pgUUID(id)).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n == 1
}

func TestAuditAppendStoresEvent(t *testing.T) {
	store := newRuntimeStore(t)
	e := auditEvent(t, `{"name":"Long term","quantity":"0.5"}`)
	e.SessionID, e.ReasonCode = newID(t), "created"

	if err := store.withinTx(t.Context(), func(r *txRepositories) error {
		return r.Audit().Append(t.Context(), e)
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	var (
		eventType, targetType, outcome, reason, targetID string
		actor, session                                   string
		metadata                                         map[string]string
		occurred                                         time.Time
	)
	err := openPool(t, "TEST_ADMIN_DATABASE_URL").QueryRow(t.Context(), `
		SELECT event_type, target_type, outcome, reason_code, target_id, actor_user_id::text, session_id::text, metadata, occurred_at
		FROM app.audit_events WHERE id = $1`, pgUUID(e.ID)).
		Scan(&eventType, &targetType, &outcome, &reason, &targetID, &actor, &session, &metadata, &occurred)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if eventType != string(e.EventType) || targetType != e.TargetType || outcome != string(e.Outcome) ||
		reason != e.ReasonCode || targetID != e.TargetID || actor != e.ActorUserID.String() ||
		session != e.SessionID.String() || metadata["quantity"] != "0.5" || !occurred.Equal(e.OccurredAt) {
		t.Errorf("stored row does not match the event: %s %s %s %s %v %v", eventType, outcome, actor, session, metadata, occurred)
	}
}

func TestAuditAppendWithoutActorOrSession(t *testing.T) {
	store := newRuntimeStore(t)
	e := auditEvent(t, `{}`)
	e.EventType, e.ActorUserID, e.Outcome, e.TargetID = domain.EventAuthLoginFailed, domain.ID{}, domain.AuditOutcomeFailure, ""

	if err := store.withinTx(t.Context(), func(r *txRepositories) error {
		return r.Audit().Append(t.Context(), e)
	}); err != nil {
		t.Fatalf("append failed-login event: %v", err)
	}
	var actorIsNull, targetIsNull bool
	if err := openPool(t, "TEST_ADMIN_DATABASE_URL").QueryRow(t.Context(),
		`SELECT actor_user_id IS NULL, target_id IS NULL FROM app.audit_events WHERE id = $1`, pgUUID(e.ID)).
		Scan(&actorIsNull, &targetIsNull); err != nil || !actorIsNull || !targetIsNull {
		t.Fatalf("actor/target NULL = %v/%v, err %v; want NULL for unauthenticated attempts", actorIsNull, targetIsNull, err)
	}
}

// TestWithinTxRollsBackEverythingWhenAuditFails is the failure injection of design section 4.5:
// a later write in the same transaction fails a database CHECK, and the earlier write must not
// survive. Milestone 3c repeats it with a portfolio as the earlier write.
func TestWithinTxRollsBackEverythingWhenAuditFails(t *testing.T) {
	store := newRuntimeStore(t)
	first := auditEvent(t, `{}`)
	bad := auditEvent(t, `{}`)
	bad.Metadata = json.RawMessage(`["not","an","object"]`) // bypasses NewAuditEvent; violates audit_events_metadata_object

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		if err := r.Audit().Append(t.Context(), first); err != nil {
			return err
		}
		return r.Audit().Append(t.Context(), bad)
	})

	if !errors.Is(err, domain.ErrInternal) {
		t.Fatalf("error = %v, want internal from the CHECK violation", err)
	}
	if auditExists(t, first.ID) || auditExists(t, bad.ID) {
		t.Fatal("a write from the failed transaction was committed")
	}
}

func TestWithinTxRollsBackOnCallbackError(t *testing.T) {
	store := newRuntimeStore(t)
	e := auditEvent(t, `{}`)
	sentinel := errors.New("business rule failed")

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		if err := r.Audit().Append(t.Context(), e); err != nil {
			return err
		}
		return sentinel
	})

	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want the callback's error", err)
	}
	if auditExists(t, e.ID) {
		t.Fatal("audit committed although the callback failed")
	}
}

func TestWithinTxRollsBackOnPanic(t *testing.T) {
	store := newRuntimeStore(t)
	e := auditEvent(t, `{}`)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic was swallowed")
			}
		}()
		_ = store.withinTx(t.Context(), func(r *txRepositories) error {
			if err := r.Audit().Append(t.Context(), e); err != nil {
				return err
			}
			panic("boom")
		})
	}()

	if auditExists(t, e.ID) {
		t.Fatal("audit committed although the callback panicked")
	}
}

// TestAuditSurvivesDeletionOfItsTarget: audit_events has no foreign keys, so deleting the user and
// portfolio an event mentions leaves the event in place (no cascade).
func TestAuditSurvivesDeletionOfItsTarget(t *testing.T) {
	admin := openPool(t, "TEST_ADMIN_DATABASE_URL")
	userID, portfolioID := newID(t), newID(t)
	now := time.Now().UTC()
	if _, err := admin.Exec(t.Context(),
		`INSERT INTO app.users (id, email, created_at, updated_at) VALUES ($1, $2, $3, $3)`,
		pgUUID(userID), "audit-target-"+userID.String()+"@example.com", now); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := admin.Exec(t.Context(),
		`INSERT INTO app.portfolios (id, user_id, name, currency, created_at, updated_at) VALUES ($1, $2, 'Doomed', 'USD', $3, $3)`,
		pgUUID(portfolioID), pgUUID(userID), now); err != nil {
		t.Fatalf("insert portfolio: %v", err)
	}
	e := auditEvent(t, `{}`)
	e.ActorUserID, e.TargetID = userID, portfolioID.String()
	if err := newRuntimeStore(t).withinTx(t.Context(), func(r *txRepositories) error {
		return r.Audit().Append(t.Context(), e)
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	if _, err := admin.Exec(t.Context(), `DELETE FROM app.portfolios WHERE id = $1`, pgUUID(portfolioID)); err != nil {
		t.Fatalf("delete portfolio: %v", err)
	}
	if _, err := admin.Exec(t.Context(), `DELETE FROM app.users WHERE id = $1`, pgUUID(userID)); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if !auditExists(t, e.ID) {
		t.Fatal("deleting the target removed its audit event")
	}
}

// TestAuditMetadataSizeIsCountedDifferentlyInGoAndDatabase documents why the database CHECK
// (octet_length(metadata::text) <= 9216) is only a backstop, not the same limit as Go's 8 KiB:
// jsonb::text adds a space after every ',' and ':', so some metadata Go accepts (<= 8192 raw bytes)
// is rejected by the database. Such an event rolls back as internal; metadata built by the
// server from an allowlist, with decimals as strings, stays far below both limits.
func TestAuditMetadataSizeIsCountedDifferentlyInGoAndDatabase(t *testing.T) {
	store := newRuntimeStore(t)
	// 900 members of about 9 bytes each: about 8.1 KB raw, within Go's 8 KiB, but jsonb::text
	// re-serializes each ":" and "," with a following space, about 9.9 KB in total.
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < 900; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"k%03d":1`, i)
	}
	b.WriteString("}")
	metadata := b.String()
	if len(metadata) > domain.MaxAuditMetadataBytes {
		t.Fatalf("test metadata is %d bytes, want <= %d so that Go accepts it", len(metadata), domain.MaxAuditMetadataBytes)
	}
	e := auditEvent(t, metadata) // NewAuditEvent accepts it: raw size is within 8 KiB

	err := store.withinTx(t.Context(), func(r *txRepositories) error {
		return r.Audit().Append(t.Context(), e)
	})

	if !errors.Is(err, domain.ErrInternal) {
		t.Fatalf("error = %v, want internal from audit_events_metadata_size", err)
	}
	if auditExists(t, e.ID) {
		t.Fatal("event was stored although the database CHECK rejects it")
	}
}
