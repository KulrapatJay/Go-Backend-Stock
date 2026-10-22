package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

// testArgon2Params keeps unit tests fast; DefaultArgon2Params is exercised once below.
var testArgon2Params = Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1}

func requireCode(t *testing.T, err error, want domain.ErrorCode) *domain.Error {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != want {
		t.Fatalf("error = %v, want *domain.Error with code %s", err, want)
	}
	return de
}

func mustPassword(t *testing.T, s string) domain.Password {
	t.Helper()
	p, err := domain.ParsePassword(s, domain.Email{})
	if err != nil {
		t.Fatalf("ParsePassword(%q) error = %v", s, err)
	}
	return p
}

func newHasher(t *testing.T, params Argon2Params, concurrency int, queueTimeout time.Duration) (*PasswordHasher, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	h, err := NewPasswordHasher(PasswordHasherConfig{
		Params: params, Concurrency: concurrency, QueueTimeout: queueTimeout,
		Logger: slog.New(slog.NewJSONHandler(logs, nil)),
	})
	if err != nil {
		t.Fatalf("NewPasswordHasher() error = %v", err)
	}
	return h, logs
}

// phc builds a well-formed hash string with all-zero salt and key.
func phc(params string, saltLen, keyLen int) domain.PasswordHash {
	enc := base64.RawStdEncoding
	return domain.PasswordHash("$argon2id$v=19$" + params + "$" +
		enc.EncodeToString(make([]byte, saltLen)) + "$" + enc.EncodeToString(make([]byte, keyLen)))
}

func TestHashAndVerify(t *testing.T) {
	h, _ := newHasher(t, testArgon2Params, 2, 0)
	ctx := t.Context()

	hash, err := h.Hash(ctx, mustPassword(t, "Zq9!kVx1"))
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := h.Verify(ctx, hash, "Zq9!kVx1"); err != nil || !ok {
		t.Errorf("Verify(right password) = %v, %v; want true, nil", ok, err)
	}
	if ok, err := h.Verify(ctx, hash, "Zq9!kVx2"); err != nil || ok {
		t.Errorf("Verify(wrong password) = %v, %v; want false, nil", ok, err)
	}
	if ok, err := h.Verify(ctx, hash, ""); err != nil || ok {
		t.Errorf("Verify(empty password) = %v, %v; want false, nil", ok, err)
	}

	again, err := h.Hash(ctx, mustPassword(t, "Zq9!kVx1"))
	if err != nil {
		t.Fatal(err)
	}
	if again == hash {
		t.Error("two hashes of one password must differ (random salt)")
	}
}

func TestHashRejectsZeroPassword(t *testing.T) {
	h, _ := newHasher(t, testArgon2Params, 1, 0)
	_, err := h.Hash(t.Context(), domain.Password{})
	requireCode(t, err, domain.CodeInternal)
}

func TestHashUsesTheConfirmedFormat(t *testing.T) {
	h, _ := newHasher(t, DefaultArgon2Params, 1, 0)
	hash, err := h.Hash(t.Context(), mustPassword(t, "Zq9!kVx1"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(hash)
	if !strings.HasPrefix(s, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash %q does not start with the confirmed parameters", s)
	}
	parts := strings.Split(s, "$") // "", argon2id, v=19, params, salt, key
	if len(parts) != 6 || strings.Contains(parts[4]+parts[5], "=") {
		t.Fatalf("hash %q must have six parts and no base64 padding", s)
	}
	salt, err1 := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	key, err2 := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(salt) != 16 || len(key) != 32 {
		t.Errorf("salt %d bytes (%v), key %d bytes (%v); want 16 and 32", len(salt), err1, len(key), err2)
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	h, _ := newHasher(t, testArgon2Params, 1, 0)
	for name, hash := range map[string]domain.PasswordHash{
		"empty":             "",
		"plain text":        "not a hash",
		"wrong algorithm":   "$argon2i$v=19$m=64,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"wrong version":     domain.PasswordHash(strings.Replace(string(phc("m=64,t=1,p=1", 16, 32)), "v=19", "v=18", 1)),
		"two parameters":    phc("m=64,t=1", 16, 32),
		"parameters order":  phc("t=1,m=64,p=1", 16, 32),
		"text parameter":    phc("m=abc,t=1,p=1", 16, 32),
		"zero iterations":   phc("m=64,t=0,p=1", 16, 32),
		"zero parallelism":  phc("m=64,t=1,p=0", 16, 32),
		"memory below 8p":   phc("m=15,t=1,p=2", 16, 32),
		"bad salt encoding": domain.PasswordHash("$argon2id$v=19$m=64,t=1,p=1$%%%$" + strings.Repeat("A", 43)),
		"padded key":        domain.PasswordHash(string(phc("m=64,t=1,p=1", 16, 32)) + "="),
	} {
		ok, err := h.Verify(t.Context(), hash, "Zq9!kVx1")
		if ok {
			t.Errorf("%s: Verify() = true for a malformed hash", name)
		}
		requireCode(t, err, domain.CodeInternal)
		if _, err := h.NeedsRehash(hash); err == nil {
			t.Errorf("%s: NeedsRehash() accepted a malformed hash", name)
		}
	}
}

// A corrupt or hostile stored hash must not make Verify allocate gigabytes: the parameters are
// bounded before any hashing starts.
func TestVerifyRejectsOversizedParameters(t *testing.T) {
	h, _ := newHasher(t, testArgon2Params, 1, 0)
	for name, hash := range map[string]domain.PasswordHash{
		"memory":      phc("m=262145,t=1,p=1", 16, 32),
		"huge memory": phc("m=4194304,t=2,p=1", 16, 32),
		"iterations":  phc("m=64,t=11,p=1", 16, 32),
		"parallelism": phc("m=64,t=1,p=9", 16, 32),
		"short salt":  phc("m=64,t=1,p=1", 7, 32),
		"long salt":   phc("m=64,t=1,p=1", 65, 32),
		"short key":   phc("m=64,t=1,p=1", 16, 15),
		"long key":    phc("m=64,t=1,p=1", 16, 65),
	} {
		start := time.Now()
		_, err := h.Verify(t.Context(), hash, "Zq9!kVx1")
		requireCode(t, err, domain.CodeInternal)
		if time.Since(start) > time.Second {
			t.Errorf("%s: rejecting took %v, it must not start hashing", name, time.Since(start))
		}
	}

	// The documented upper bounds themselves are accepted by the parser (not hashed here).
	if _, _, _, err := decodePHC(string(phc("m=262144,t=10,p=8", 64, 64))); err != nil {
		t.Errorf("decodePHC() rejected parameters at the bound: %v", err)
	}
}

func TestVerifyWellFormedButWrongHashIsFalseNotError(t *testing.T) {
	h, _ := newHasher(t, testArgon2Params, 1, 0)
	ok, err := h.Verify(t.Context(), phc("m=64,t=1,p=1", 16, 32), "Zq9!kVx1")
	if err != nil || ok {
		t.Errorf("Verify() = %v, %v; want false, nil", ok, err)
	}
}

func TestNeedsRehash(t *testing.T) {
	current, _ := newHasher(t, testArgon2Params, 1, 0)
	older, _ := newHasher(t, Argon2Params{MemoryKiB: 32, Iterations: 1, Parallelism: 1}, 1, 0)

	hash, err := current.Hash(t.Context(), mustPassword(t, "Zq9!kVx1"))
	if err != nil {
		t.Fatal(err)
	}
	if need, err := current.NeedsRehash(hash); err != nil || need {
		t.Errorf("NeedsRehash(current) = %v, %v; want false, nil", need, err)
	}
	if need, err := older.NeedsRehash(hash); err != nil || !need {
		t.Errorf("NeedsRehash(other parameters) = %v, %v; want true, nil", need, err)
	}
}

// hold takes a slot the way an in-flight hash would, so the next caller has to queue.
func hold(t *testing.T, h *PasswordHasher) (release func()) {
	t.Helper()
	release, err := h.acquire(t.Context())
	if err != nil {
		t.Fatalf("acquire() error = %v", err)
	}
	return release
}

func TestQueueTimeoutReturnsServerBusy(t *testing.T) {
	h, logs := newHasher(t, testArgon2Params, 1, 30*time.Millisecond)
	release := hold(t, h)

	start := time.Now()
	_, err := h.Hash(t.Context(), mustPassword(t, "Zq9!kVx1"))
	requireCode(t, err, domain.CodeServerBusy)
	if elapsed := time.Since(start); elapsed < 25*time.Millisecond || elapsed > time.Second {
		t.Errorf("waited %v, want about the 30ms queue timeout", elapsed)
	}
	if !strings.Contains(logs.String(), `"event":"auth.hash_busy"`) || !strings.Contains(logs.String(), `"level":"WARN"`) {
		t.Errorf("log %q should hold a warn auth.hash_busy record", logs.String())
	}

	release()
	if _, err := h.Hash(t.Context(), mustPassword(t, "Zq9!kVx1")); err != nil {
		t.Errorf("Hash() after the slot was released error = %v", err)
	}
}

func TestCancelledContextWhileWaitingReturnsServerBusy(t *testing.T) {
	h, _ := newHasher(t, testArgon2Params, 1, time.Minute)
	release := hold(t, h)
	defer release()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := h.Hash(ctx, mustPassword(t, "Zq9!kVx1"))
	requireCode(t, err, domain.CodeServerBusy)
	if time.Since(start) > time.Second {
		t.Errorf("a cancelled request kept waiting for %v", time.Since(start))
	}
}

func TestVerifyDummyCostsAsMuchAsVerifyAndUsesTheQueue(t *testing.T) {
	h, _ := newHasher(t, testArgon2Params, 1, 20*time.Millisecond)
	if err := h.VerifyDummy(t.Context(), "anything at all"); err != nil {
		t.Fatalf("VerifyDummy() error = %v", err)
	}

	release := hold(t, h)
	defer release()
	requireCode(t, h.VerifyDummy(t.Context(), "anything at all"), domain.CodeServerBusy)
}

func TestNewPasswordHasherValidatesConfig(t *testing.T) {
	discard := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	for name, cfg := range map[string]PasswordHasherConfig{
		"no iterations":   {Params: Argon2Params{MemoryKiB: 64, Iterations: 0, Parallelism: 1}, Concurrency: 1, Logger: discard},
		"no parallelism":  {Params: Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 0}, Concurrency: 1, Logger: discard},
		"memory below 8p": {Params: Argon2Params{MemoryKiB: 15, Iterations: 1, Parallelism: 2}, Concurrency: 1, Logger: discard},
		"no concurrency":  {Params: testArgon2Params, Concurrency: 0, Logger: discard},
		"no logger":       {Params: testArgon2Params, Concurrency: 1},
	} {
		if _, err := NewPasswordHasher(cfg); err == nil {
			t.Errorf("%s: NewPasswordHasher() accepted the config", name)
		}
	}
}
