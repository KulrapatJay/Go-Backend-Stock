package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/textproto"
	"strconv"
	"sync"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// ErrQueueFull is returned when the queue has no room: the email was dropped and logged.
var ErrQueueFull = errors.New("mail queue is full")

// ErrDispatcherClosed is returned after Close: the email was dropped and logged.
var ErrDispatcherClosed = errors.New("mail dispatcher is closed")

const (
	defaultQueueSize = 100
	defaultWorkers   = 2
)

// DispatcherConfig configures NewDispatcher.
type DispatcherConfig struct {
	Sender      port.Mailer // the mailer that really sends, normally an *SMTPMailer
	Logger      *slog.Logger
	QueueSize   int           // emails that may wait; 0 means 100
	Workers     int           // concurrent sends; 0 means 2
	SendTimeout time.Duration // limit for one send (MAIL_SEND_TIMEOUT)
}

// Dispatcher implements port.Mailer by queueing each email for background workers, so a
// response never waits for the mail server and its timing does not depend on it. The trade-off
// (ADR-0014): an email still queued when the process dies is lost; the user asks for a new code
// after the resend cooldown. It never logs a code, an address or the server's error text.
type Dispatcher struct {
	sender  port.Mailer
	log     *slog.Logger
	timeout time.Duration

	jobs chan job
	// baseCtx is the parent of every send; Close cancels it to abort sends still running at the
	// shutdown deadline.
	baseCtx context.Context
	cancel  context.CancelFunc

	mu     sync.Mutex // guards closed and sends on jobs against close(jobs)
	closed bool
	wg     sync.WaitGroup
}

var _ port.Mailer = (*Dispatcher)(nil)

type job struct {
	kind string
	run  func(ctx context.Context) error
}

// NewDispatcher validates cfg and starts the workers.
func NewDispatcher(cfg DispatcherConfig) (*Dispatcher, error) {
	switch {
	case cfg.Sender == nil:
		return nil, errors.New("dispatcher needs a sender")
	case cfg.Logger == nil:
		return nil, errors.New("dispatcher needs a logger")
	case cfg.SendTimeout <= 0:
		return nil, errors.New("dispatcher send timeout must be positive")
	case cfg.QueueSize < 0 || cfg.Workers < 0:
		return nil, errors.New("dispatcher queue size and workers must not be negative")
	}
	if cfg.QueueSize == 0 {
		cfg.QueueSize = defaultQueueSize
	}
	if cfg.Workers == 0 {
		cfg.Workers = defaultWorkers
	}
	d := &Dispatcher{sender: cfg.Sender, log: cfg.Logger, timeout: cfg.SendTimeout, jobs: make(chan job, cfg.QueueSize)}
	d.baseCtx, d.cancel = context.WithCancel(context.Background())
	for range cfg.Workers {
		d.wg.Add(1)
		go d.worker()
	}
	return d, nil
}

// SendEmailVerification queues the email. The request context is ignored on purpose: the email
// is sent after the request that caused it has finished.
func (d *Dispatcher) SendEmailVerification(_ context.Context, mail domain.OTPMail) error {
	return d.enqueue("email_verification", func(ctx context.Context) error {
		return d.sender.SendEmailVerification(ctx, mail)
	})
}

// SendRegistrationAttempt queues the email.
func (d *Dispatcher) SendRegistrationAttempt(_ context.Context, to domain.Email) error {
	return d.enqueue("registration_attempt", func(ctx context.Context) error {
		return d.sender.SendRegistrationAttempt(ctx, to)
	})
}

func (d *Dispatcher) enqueue(kind string, run func(ctx context.Context) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		d.log.Warn("mail dispatcher is closed, email dropped",
			slog.String("event", "mail.closed"), slog.String("kind", kind))
		return ErrDispatcherClosed
	}
	select {
	case d.jobs <- job{kind: kind, run: run}:
		return nil
	default:
		d.log.Error("mail queue is full, email dropped",
			slog.String("event", "mail.queue_full"), slog.String("kind", kind))
		return ErrQueueFull
	}
}

func (d *Dispatcher) worker() {
	defer d.wg.Done()
	for j := range d.jobs {
		ctx, cancel := context.WithTimeout(d.baseCtx, d.timeout)
		err := j.run(ctx)
		cancel()
		if err != nil {
			d.log.Error("sending email failed",
				slog.String("event", "mail.send_failed"),
				slog.String("kind", j.kind),
				slog.String("error_class", classifyError(err)))
		}
	}
}

// Close stops accepting emails and waits for the queued ones until ctx ends; then it cancels
// sends still running and returns ctx.Err(). Calling it again is harmless.
func (d *Dispatcher) Close(ctx context.Context) error {
	d.mu.Lock()
	if !d.closed {
		d.closed = true
		close(d.jobs)
	}
	d.mu.Unlock()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		d.cancel()
		return nil
	case <-ctx.Done():
		d.cancel()
		return ctx.Err()
	}
}

// classifyError names the kind of failure without any of its text: a server's error message can
// repeat the recipient's address.
func classifyError(err error) string {
	var smtpErr *textproto.Error
	var certErr *tls.CertificateVerificationError
	var netErr net.Error
	switch {
	case errors.As(err, &smtpErr):
		return "smtp_" + strconv.Itoa(smtpErr.Code)
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.As(err, &certErr):
		return "tls"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case errors.As(err, &netErr):
		return "network"
	}
	return "other"
}
