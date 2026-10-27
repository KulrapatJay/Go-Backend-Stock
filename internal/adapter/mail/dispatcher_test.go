package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// fakeSender records what it is asked to send. If block is set, a send waits for it (or for its
// context to end); started receives a value each time a send begins.
type fakeSender struct {
	mu        sync.Mutex
	sent      []domain.OTPMail
	attempts  []domain.Email
	started   chan struct{}
	block     chan struct{}
	err       error
	cancelled atomic.Bool
	panics    atomic.Int32 // sends that should panic before doing anything
}

func (f *fakeSender) wait(ctx context.Context) error {
	if f.panics.Add(-1) >= 0 {
		panic("sender exploded")
	}
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.block == nil {
		return nil
	}
	select {
	case <-f.block:
		return nil
	case <-ctx.Done():
		f.cancelled.Store(true)
		return ctx.Err()
	}
}

func (f *fakeSender) SendEmailVerification(ctx context.Context, m domain.OTPMail) error {
	if err := f.wait(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return f.err
}

func (f *fakeSender) SendRegistrationAttempt(ctx context.Context, to domain.Email) error {
	if err := f.wait(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts = append(f.attempts, to)
	return f.err
}

func (f *fakeSender) counts() (verifications, attempts int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent), len(f.attempts)
}

func newTestDispatcher(t *testing.T, sender *fakeSender, mutate func(*DispatcherConfig)) (*Dispatcher, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	cfg := DispatcherConfig{
		Sender: sender, Logger: slog.New(slog.NewJSONHandler(logs, nil)),
		QueueSize: 10, Workers: 1, SendTimeout: 5 * time.Second,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	d, err := NewDispatcher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = d.Close(ctx)
	})
	return d, logs
}

func TestDispatcherReturnsAtOnceAndDeliversInTheBackground(t *testing.T) {
	sender := &fakeSender{block: make(chan struct{}), started: make(chan struct{}, 4)}
	d, _ := newTestDispatcher(t, sender, nil)

	start := time.Now()
	if err := d.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Errorf("SendEmailVerification() took %v while the server was blocked", time.Since(start))
	}
	<-sender.started
	if v, _ := sender.counts(); v != 0 {
		t.Fatal("the send finished although the sender was blocked")
	}

	close(sender.block)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if v, _ := sender.counts(); v != 1 {
		t.Errorf("delivered %d verification emails, want 1", v)
	}
}

func TestDispatcherDeliversEverythingQueuedBeforeClose(t *testing.T) {
	sender := &fakeSender{}
	d, _ := newTestDispatcher(t, sender, func(c *DispatcherConfig) { c.Workers = 2; c.QueueSize = 50 })
	to, err := domain.ParseEmail("user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if err := d.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
			t.Fatal(err)
		}
		if err := d.SendRegistrationAttempt(t.Context(), to); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if v, a := sender.counts(); v != 20 || a != 20 {
		t.Errorf("delivered %d verification and %d attempt emails, want 20 and 20", v, a)
	}
}

func TestDispatcherQueueFull(t *testing.T) {
	sender := &fakeSender{block: make(chan struct{}), started: make(chan struct{}, 4)}
	d, logs := newTestDispatcher(t, sender, func(c *DispatcherConfig) { c.QueueSize = 1 })
	mail := testOTPMail(t)

	if err := d.SendEmailVerification(t.Context(), mail); err != nil {
		t.Fatal(err)
	}
	<-sender.started // the worker took the first one, so the queue is empty again
	if err := d.SendEmailVerification(t.Context(), mail); err != nil {
		t.Fatalf("second send error = %v, want it queued", err)
	}
	err := d.SendEmailVerification(t.Context(), mail)
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("third send error = %v, want ErrQueueFull", err)
	}
	if !strings.Contains(logs.String(), `"event":"mail.queue_full"`) || !strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("log %q should hold an error mail.queue_full record", logs.String())
	}
	close(sender.block)
}

func TestClassifyError(t *testing.T) {
	for name, tt := range map[string]struct {
		err  error
		want string
	}{
		"smtp rejection":    {&textproto.Error{Code: 550, Msg: "user@example.com does not exist"}, "smtp_550"},
		"wrapped rejection": {errors.Join(errors.New("x"), &textproto.Error{Code: 421, Msg: "later"}), "smtp_421"},
		"deadline":          {context.DeadlineExceeded, "timeout"},
		"cancelled":         {context.Canceled, "canceled"},
		"network timeout":   {&net.OpError{Op: "read", Err: timeoutError{}}, "timeout"},
		"network":           {&net.OpError{Op: "dial", Err: errors.New("connection refused")}, "network"},
		"certificate":       {&tls.CertificateVerificationError{Err: errors.New("unknown authority")}, "tls"},
		"anything else":     {errors.New("550 user@example.com mailbox full"), "other"},
	} {
		if got := classifyError(tt.err); got != tt.want {
			t.Errorf("%s: classifyError() = %q, want %q", name, got, tt.want)
		}
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestDispatcherLogsNeverContainAddressOrOTP(t *testing.T) {
	sender := &fakeSender{err: &textproto.Error{Code: 550, Msg: "user@example.com 042731 mailbox unavailable"}}
	d, logs := newTestDispatcher(t, sender, nil)
	if err := d.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatal(err)
	}

	out := logs.String()
	for _, want := range []string{`"event":"mail.send_failed"`, `"kind":"email_verification"`, `"error_class":"smtp_550"`, `"level":"ERROR"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q lacks %s", out, want)
		}
	}
	for _, secret := range []string{"user@example.com", "example.com", "042731", "mailbox unavailable"} {
		if strings.Contains(out, secret) {
			t.Errorf("log %q leaks %q", out, secret)
		}
	}
}

func TestDispatcherAppliesTheSendTimeout(t *testing.T) {
	sender := &fakeSender{block: make(chan struct{})}
	d, logs := newTestDispatcher(t, sender, func(c *DispatcherConfig) { c.SendTimeout = 50 * time.Millisecond })
	if err := d.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(logs.String(), `"error_class":"timeout"`) {
		if time.Now().After(deadline) {
			t.Fatalf("no timeout was logged, log = %q", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDispatcherRefusesAfterClose(t *testing.T) {
	sender := &fakeSender{}
	d, logs := newTestDispatcher(t, sender, nil)
	if err := d.Close(t.Context()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := d.Close(t.Context()); err != nil {
		t.Errorf("second Close() error = %v, want it to be harmless", err)
	}
	if err := d.SendEmailVerification(t.Context(), testOTPMail(t)); !errors.Is(err, ErrDispatcherClosed) {
		t.Errorf("send after Close error = %v, want ErrDispatcherClosed", err)
	}
	if !strings.Contains(logs.String(), `"event":"mail.closed"`) {
		t.Errorf("log %q should record the dropped email", logs.String())
	}
}

func TestDispatcherCloseGivesUpAtItsDeadlineAndCancelsInFlightSends(t *testing.T) {
	sender := &fakeSender{block: make(chan struct{}), started: make(chan struct{}, 1)}
	d, _ := newTestDispatcher(t, sender, nil)
	if err := d.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatal(err)
	}
	<-sender.started

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := d.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close() error = %v, want context.DeadlineExceeded", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !sender.cancelled.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the in-flight send was not cancelled when Close gave up")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDispatcherSendsAndCloseMayRace(t *testing.T) {
	sender := &fakeSender{}
	d, _ := newTestDispatcher(t, sender, func(c *DispatcherConfig) { c.QueueSize = 5; c.Workers = 2 })
	mail := testOTPMail(t)

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := d.SendEmailVerification(context.Background(), mail)
			if err != nil && !errors.Is(err, ErrQueueFull) && !errors.Is(err, ErrDispatcherClosed) {
				t.Errorf("send error = %v", err)
			}
		}()
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	wg.Wait()
}

func TestNewDispatcherValidatesConfig(t *testing.T) {
	good := DispatcherConfig{Sender: &fakeSender{}, Logger: slog.New(slog.NewJSONHandler(&syncBuffer{}, nil)), SendTimeout: time.Second}
	d, err := NewDispatcher(good)
	if err != nil {
		t.Fatalf("NewDispatcher(good) error = %v", err)
	}
	_ = d.Close(context.Background())

	for name, mutate := range map[string]func(*DispatcherConfig){
		"no sender":        func(c *DispatcherConfig) { c.Sender = nil },
		"no logger":        func(c *DispatcherConfig) { c.Logger = nil },
		"no timeout":       func(c *DispatcherConfig) { c.SendTimeout = 0 },
		"negative queue":   func(c *DispatcherConfig) { c.QueueSize = -1 },
		"negative workers": func(c *DispatcherConfig) { c.Workers = -1 },
	} {
		cfg := good
		mutate(&cfg)
		if _, err := NewDispatcher(cfg); err == nil {
			t.Errorf("%s: NewDispatcher() accepted the config", name)
		}
	}
}

func TestDispatcherSurvivesAPanickingSender(t *testing.T) {
	sender := &fakeSender{}
	sender.panics.Store(1)
	d, logs := newTestDispatcher(t, sender, nil)
	if err := d.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatal(err)
	}
	if err := d.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v: a panic must not stop the worker", err)
	}
	if v, _ := sender.counts(); v != 1 {
		t.Errorf("delivered %d emails after the panic, want 1", v)
	}
	if !strings.Contains(logs.String(), `"event":"mail.panic"`) || strings.Contains(logs.String(), "exploded") {
		t.Errorf("log %q should record mail.panic without the panic text", logs.String())
	}
}
