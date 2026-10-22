package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"go-backend-stock/internal/core/domain"
)

// Argon2Params are the Argon2id cost parameters. They are stored inside every hash, so changing
// them later leaves old hashes verifiable; NeedsRehash reports which ones to upgrade at login.
type Argon2Params struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
}

// DefaultArgon2Params are the confirmed phase 1 parameters (ADR-0005): 19 MiB, 2 passes, 1 lane.
var DefaultArgon2Params = Argon2Params{MemoryKiB: 19456, Iterations: 2, Parallelism: 1}

const (
	argon2SaltBytes = 16
	argon2KeyBytes  = 32

	defaultHashQueueTimeout = 2 * time.Second

	// Limits for a hash read back from the database. The values we write are far below them; a
	// corrupt or hostile row must not be able to make Verify allocate gigabytes.
	maxStoredMemoryKiB   = 256 * 1024
	maxStoredIterations  = 10
	maxStoredParallelism = 8
	minStoredSaltBytes   = 8
	maxStoredSaltBytes   = 64
	minStoredKeyBytes    = 16
	maxStoredKeyBytes    = 64
)

// PasswordHasherConfig configures NewPasswordHasher.
type PasswordHasherConfig struct {
	Params       Argon2Params
	Concurrency  int           // how many hashes may run at once (PASSWORD_HASH_CONCURRENCY)
	QueueTimeout time.Duration // how long a caller may wait for a slot; 0 means 2 seconds
	Logger       *slog.Logger
}

// PasswordHasher hashes and verifies passwords with Argon2id. At most Concurrency hashes run at
// once because each needs MemoryKiB of RAM; a caller that cannot get a slot in time receives
// server_busy. Every Hash, Verify and VerifyDummy call goes through the same slots. Callers must
// not hold a database transaction while they wait for one. Safe for concurrent use.
type PasswordHasher struct {
	params       Argon2Params
	slots        chan struct{}
	queueTimeout time.Duration
	log          *slog.Logger
	dummy        domain.PasswordHash
}

// NewPasswordHasher validates cfg and creates the dummy hash that login verifies against when the
// account does not exist or has no password, so response time does not reveal which it is.
func NewPasswordHasher(cfg PasswordHasherConfig) (*PasswordHasher, error) {
	p := cfg.Params
	switch {
	case p.Iterations < 1 || p.Parallelism < 1 || p.MemoryKiB < 8*uint32(p.Parallelism):
		return nil, errors.New("invalid argon2 parameters")
	case cfg.Concurrency < 1:
		return nil, errors.New("password hash concurrency must be at least 1")
	case cfg.Logger == nil:
		return nil, errors.New("password hasher needs a logger")
	}
	h := &PasswordHasher{
		params:       p,
		slots:        make(chan struct{}, cfg.Concurrency),
		queueTimeout: cfg.QueueTimeout,
		log:          cfg.Logger,
	}
	if h.queueTimeout <= 0 {
		h.queueTimeout = defaultHashQueueTimeout
	}

	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate dummy password: %w", err)
	}
	dummy, err := h.hashString(base64.RawStdEncoding.EncodeToString(secret))
	if err != nil {
		return nil, fmt.Errorf("create dummy hash: %w", err)
	}
	h.dummy = dummy
	return h, nil
}

// Hash returns the PHC string for password, or server_busy when no slot frees up in time.
func (h *PasswordHasher) Hash(ctx context.Context, password domain.Password) (domain.PasswordHash, error) {
	if password.IsZero() {
		return "", domain.NewError(domain.CodeInternal, "cannot hash an empty password")
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	return h.hashString(password.Reveal())
}

// Verify reports whether password matches hash, comparing in constant time. A hash that cannot be
// parsed is an internal error, not a wrong password. It returns server_busy when no slot frees up.
func (h *PasswordHasher) Verify(ctx context.Context, hash domain.PasswordHash, password string) (bool, error) {
	params, salt, key, err := decodePHC(string(hash))
	if err != nil {
		return false, err
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	got := argon2.IDKey([]byte(password), salt, params.Iterations, params.MemoryKiB, params.Parallelism,
		uint32(len(key))) //nolint:gosec // decodePHC limits the key to 16..64 bytes.
	return subtle.ConstantTimeCompare(got, key) == 1, nil
}

// VerifyDummy spends the cost of one Verify against a hash nobody knows the password of and
// discards the result. Login calls it when there is no password to check.
func (h *PasswordHasher) VerifyDummy(ctx context.Context, password string) error {
	_, err := h.Verify(ctx, h.dummy, password)
	return err
}

// NeedsRehash reports whether hash was made with parameters other than the current ones, so login
// can upgrade it after a successful check.
func (h *PasswordHasher) NeedsRehash(hash domain.PasswordHash) (bool, error) {
	params, _, _, err := decodePHC(string(hash))
	if err != nil {
		return false, err
	}
	return params != h.params, nil
}

// acquire takes a slot, waiting up to the queue timeout or until ctx ends.
func (h *PasswordHasher) acquire(ctx context.Context) (release func(), err error) {
	select {
	case h.slots <- struct{}{}:
		return h.release, nil
	default:
	}
	timer := time.NewTimer(h.queueTimeout)
	defer timer.Stop()
	select {
	case h.slots <- struct{}{}:
		return h.release, nil
	case <-timer.C:
	case <-ctx.Done():
	}
	h.log.Warn("password hashing queue is full", slog.String("event", "auth.hash_busy"))
	return nil, domain.NewError(domain.CodeServerBusy, "server is busy, try again shortly")
}

func (h *PasswordHasher) release() { <-h.slots }

func (h *PasswordHasher) hashString(password string) (domain.PasswordHash, error) {
	salt := make([]byte, argon2SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, h.params.Iterations, h.params.MemoryKiB, h.params.Parallelism, argon2KeyBytes)
	enc := base64.RawStdEncoding
	return domain.PasswordHash(fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.params.MemoryKiB, h.params.Iterations, h.params.Parallelism,
		enc.EncodeToString(salt), enc.EncodeToString(key))), nil
}

func malformedHash() error {
	return domain.NewError(domain.CodeInternal, "stored password hash is malformed")
}

// decodePHC parses "$argon2id$v=19$m=…,t=…,p=…$<salt>$<key>" and enforces the limits above.
func decodePHC(hash string) (params Argon2Params, salt, key []byte, err error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return Argon2Params{}, nil, nil, malformedHash()
	}
	fields := strings.Split(parts[3], ",")
	if len(fields) != 3 {
		return Argon2Params{}, nil, nil, malformedHash()
	}
	m, okM := parseUintField(fields[0], "m=", 32)
	t, okT := parseUintField(fields[1], "t=", 32)
	p, okP := parseUintField(fields[2], "p=", 8)
	if !okM || !okT || !okP ||
		t < 1 || t > maxStoredIterations || p < 1 || p > maxStoredParallelism ||
		m < 8*p || m > maxStoredMemoryKiB {
		return Argon2Params{}, nil, nil, malformedHash()
	}
	enc := base64.RawStdEncoding.Strict()
	salt, errSalt := enc.DecodeString(parts[4])
	key, errKey := enc.DecodeString(parts[5])
	if errSalt != nil || errKey != nil ||
		len(salt) < minStoredSaltBytes || len(salt) > maxStoredSaltBytes ||
		len(key) < minStoredKeyBytes || len(key) > maxStoredKeyBytes {
		return Argon2Params{}, nil, nil, malformedHash()
	}
	//nolint:gosec // the values are bounded above (m <= 262144, t <= 10, p <= 8).
	return Argon2Params{MemoryKiB: uint32(m), Iterations: uint32(t), Parallelism: uint8(p)}, salt, key, nil
}

func parseUintField(field, prefix string, bits int) (uint64, bool) {
	v, ok := strings.CutPrefix(field, prefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(v, 10, bits)
	return n, err == nil
}
