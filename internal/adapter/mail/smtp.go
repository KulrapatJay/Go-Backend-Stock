package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// TLSMode says how the connection to the mail server is protected.
type TLSMode string

const (
	TLSNone     TLSMode = "none"     // plain text; for a local Mailpit only
	TLSStartTLS TLSMode = "starttls" // upgrade after EHLO
	TLSImplicit TLSMode = "tls"      // TLS from the first byte
)

// SMTPConfig configures NewSMTPMailer.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string // empty = no authentication
	Password string
	From     string // plain address, no display name
	TLS      TLSMode
	Timeout  time.Duration // the whole send: connect, handshake, and delivery
}

// SMTPMailer sends email synchronously over SMTP with net/smtp (frozen, but enough for
// STARTTLS, implicit TLS and AUTH PLAIN). Certificates are always verified.
type SMTPMailer struct {
	cfg       SMTPConfig
	tlsConfig *tls.Config
	now       func() time.Time
}

var _ port.Mailer = (*SMTPMailer)(nil)

// NewSMTPMailer validates cfg.
func NewSMTPMailer(cfg SMTPConfig) (*SMTPMailer, error) {
	switch {
	case cfg.Host == "":
		return nil, errors.New("smtp host is required")
	case cfg.Port < 1 || cfg.Port > 65535:
		return nil, errors.New("smtp port must be between 1 and 65535")
	case cfg.TLS != TLSNone && cfg.TLS != TLSStartTLS && cfg.TLS != TLSImplicit:
		return nil, errors.New("smtp tls mode must be none, starttls or tls")
	case cfg.Timeout <= 0:
		return nil, errors.New("smtp timeout must be positive")
	case (cfg.Username == "") != (cfg.Password == ""):
		return nil, errors.New("smtp username and password must be set together")
	}
	if err := checkAddress(cfg.From); err != nil {
		return nil, fmt.Errorf("smtp sender: %w", err)
	}
	return &SMTPMailer{
		cfg:       cfg,
		tlsConfig: &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12},
		now:       time.Now,
	}, nil
}

// String names the server only, so `%v` and `%+v` of a mailer cannot print the password.
func (c SMTPConfig) String() string {
	return "SMTPConfig{" + net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) + "}"
}

// GoString makes %#v print the same safe form.
func (c SMTPConfig) GoString() string { return c.String() }

// String names the server only (value receiver, so a copy of the mailer is safe to print too).
func (m SMTPMailer) String() string { return "SMTPMailer{" + m.cfg.String() + "}" }

// GoString makes %#v print the same safe form.
func (m SMTPMailer) GoString() string { return m.String() }

// SendEmailVerification implements port.Mailer.
func (m *SMTPMailer) SendEmailVerification(ctx context.Context, mail domain.OTPMail) error {
	return m.send(ctx, mail.To.String(), verificationSubject, verificationBody(mail))
}

// SendRegistrationAttempt implements port.Mailer.
func (m *SMTPMailer) SendRegistrationAttempt(ctx context.Context, to domain.Email) error {
	return m.send(ctx, to.String(), registrationAttemptSubject, registrationAttemptBody())
}

func (m *SMTPMailer) send(ctx context.Context, to, subject, body string) error {
	id, err := newMessageID(m.cfg.From)
	if err != nil {
		return fmt.Errorf("create message id: %w", err)
	}
	raw, err := message{from: m.cfg.From, to: to, subject: subject, body: body, date: m.now(), id: id}.bytes()
	if err != nil {
		return err
	}
	return m.deliver(ctx, to, raw)
}

// deliver runs one SMTP conversation. The deadline covers all of it; cancelling ctx closes the
// connection so a stalled server cannot hold a shutdown.
func (m *SMTPMailer) deliver(ctx context.Context, to string, raw []byte) error {
	deadline := time.Now().Add(m.cfg.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	dialCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(dialCtx, "tcp", net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port)))
	if err != nil {
		return err
	}
	// The caller's context (not dialCtx) closes the connection, so a send that runs out of time
	// reports the connection's own timeout error while a cancelled one is cut off at once.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if m.cfg.TLS == TLSImplicit {
		tlsConn := tls.Client(conn, m.tlsConfig)
		if err := tlsConn.HandshakeContext(dialCtx); err != nil {
			_ = conn.Close()
			return err
		}
		conn = tlsConn
	}
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer client.Close()

	if m.cfg.TLS == TLSStartTLS {
		if err := client.StartTLS(m.tlsConfig); err != nil {
			return err
		}
	}
	if m.cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); err != nil {
			return err
		}
	}
	if err := client.Mail(m.cfg.From); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
