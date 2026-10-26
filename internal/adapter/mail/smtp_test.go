package mail

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

type fakeSMTPOptions struct {
	tlsConfig   *tls.Config // offers STARTTLS, or wraps every connection when implicitTLS
	implicitTLS bool
	offerAuth   bool
	rejectRcpt  bool
	stall       bool // accept connections and never send the greeting
	failQuit    bool // answer QUIT with an error after accepting the message
}

type received struct {
	From, Auth string
	To         []string
	Data       string
	TLS        bool
}

// fakeSMTP is a minimal SMTP server: enough of RFC 5321 for net/smtp, recording what it receives.
type fakeSMTP struct {
	opts  fakeSMTPOptions
	ln    net.Listener
	done  chan struct{}
	wg    sync.WaitGroup
	mu    sync.Mutex
	conns []net.Conn
	msgs  []received
}

func startFakeSMTP(t *testing.T, opts fakeSMTPOptions) *fakeSMTP {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if opts.implicitTLS {
		ln = tls.NewListener(ln, opts.tlsConfig)
	}
	s := &fakeSMTP{opts: opts, ln: ln, done: make(chan struct{})}
	t.Cleanup(func() {
		close(s.done)
		_ = ln.Close()
		s.mu.Lock()
		for _, c := range s.conns {
			_ = c.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
	})
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns = append(s.conns, conn)
			s.mu.Unlock()
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.serve(conn)
			}()
		}
	}()
	return s
}

func (s *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	if s.opts.stall {
		<-s.done
		return
	}
	reader := bufio.NewReader(conn)
	tlsActive := s.opts.implicitTLS
	say := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	var cur received
	say("220 fake ESMTP ready")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			say("250-fake.local")
			if s.opts.tlsConfig != nil && !s.opts.implicitTLS && !tlsActive {
				say("250-STARTTLS")
			}
			if s.opts.offerAuth {
				say("250-AUTH PLAIN")
			}
			say("250 SIZE 1048576")
		case upper == "STARTTLS":
			say("220 ready to start TLS")
			tlsConn := tls.Server(conn, s.opts.tlsConfig)
			conn = tlsConn
			reader = bufio.NewReader(tlsConn)
			tlsActive = true
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			cur.Auth = line
			say("235 authenticated")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			cur = received{From: strings.TrimSpace(line[len("MAIL FROM:"):]), Auth: cur.Auth, TLS: tlsActive}
			say("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			if s.opts.rejectRcpt {
				say("550 mailbox unavailable")
				continue
			}
			cur.To = append(cur.To, strings.TrimSpace(line[len("RCPT TO:"):]))
			say("250 ok")
		case upper == "DATA":
			say("354 end data with <CR><LF>.<CR><LF>")
			var data strings.Builder
			for {
				l, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				data.WriteString(strings.TrimPrefix(l, ".")) // undo dot-stuffing
			}
			cur.Data = data.String()
			s.mu.Lock()
			s.msgs = append(s.msgs, cur)
			s.mu.Unlock()
			say("250 queued")
		case upper == "QUIT":
			if s.opts.failQuit {
				say("554 transaction failed")
				return
			}
			say("221 bye")
			return
		default:
			say("502 command not implemented")
		}
	}
}

func (s *fakeSMTP) messages() []received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]received(nil), s.msgs...)
}

// testTLS returns a server TLS config with a self-signed certificate for 127.0.0.1 and a pool
// that trusts it.
func testTLS(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake smtp"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}, pool
}

func newTestMailer(t *testing.T, srv *fakeSMTP, mutate func(*SMTPConfig)) *SMTPMailer {
	t.Helper()
	host, portText, err := net.SplitHostPort(srv.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	cfg := SMTPConfig{Host: host, Port: port, From: "no-reply@localhost", TLS: TLSNone, Timeout: 5 * time.Second}
	if mutate != nil {
		mutate(&cfg)
	}
	m, err := NewSMTPMailer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC) }
	return m
}

func testOTPMail(t *testing.T) domain.OTPMail {
	t.Helper()
	to, err := domain.ParseEmail("user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	otp, err := domain.ParseOTP("042731")
	if err != nil {
		t.Fatal(err)
	}
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return domain.OTPMail{To: to, OTP: otp, ChallengeID: id, ExpiresAt: time.Date(2026, 10, 22, 9, 10, 0, 0, time.UTC)}
}

func TestSMTPMailerSendsTheVerificationEmail(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{})
	mailer := newTestMailer(t, srv, nil)
	mail := testOTPMail(t)

	if err := mailer.SendEmailVerification(t.Context(), mail); err != nil {
		t.Fatalf("SendEmailVerification() error = %v", err)
	}
	got := srv.messages()
	if len(got) != 1 {
		t.Fatalf("server received %d messages, want 1", len(got))
	}
	m := got[0]
	if m.From != "<no-reply@localhost>" || len(m.To) != 1 || m.To[0] != "<user@example.com>" {
		t.Errorf("envelope = from %q to %q", m.From, m.To)
	}
	for _, want := range []string{
		"Date: Thu, 22 Oct 2026 09:00:00 +0000\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n", "Content-Transfer-Encoding: quoted-printable\r\n",
		"Subject: =?UTF-8?",
	} {
		if !strings.Contains(m.Data, want) {
			t.Errorf("message lacks %q:\n%s", want, m.Data)
		}
	}
	got2 := parseReceivedMail(t, m.Data)
	if got2.subject != wantVerificationSubject {
		t.Errorf("decoded subject = %q, want %q", got2.subject, wantVerificationSubject)
	}
	for _, want := range []string{"042731", mail.ChallengeID.String(), "น. (เวลาประเทศไทย)"} {
		if !strings.Contains(got2.body, want) {
			t.Errorf("decoded body lacks %q:\n%s", want, got2.body)
		}
	}
}

func TestSMTPMailerSendsTheRegistrationAttemptWithoutACode(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{})
	mailer := newTestMailer(t, srv, nil)
	to, err := domain.ParseEmail("user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := mailer.SendRegistrationAttempt(t.Context(), to); err != nil {
		t.Fatalf("SendRegistrationAttempt() error = %v", err)
	}
	got := srv.messages()
	if len(got) != 1 {
		t.Fatalf("server received %d messages, want 1", len(got))
	}
	parsed := parseReceivedMail(t, got[0].Data)
	if parsed.subject != wantRegistrationAttemptSubject {
		t.Errorf("decoded subject = %q, want %q", parsed.subject, wantRegistrationAttemptSubject)
	}
	if !strings.Contains(parsed.body, "มีบัญชีอยู่แล้ว") || strings.Contains(parsed.body, "รหัสยืนยัน") {
		t.Errorf("decoded body = %q", parsed.body)
	}
	if sixDigitCode.MatchString(parsed.body) || sixDigitCode.MatchString(parsed.subject) {
		t.Errorf("the registration attempt email must carry no 6-digit code:\n%s\n%s", parsed.subject, parsed.body)
	}
}

func TestSMTPMailerAuthenticatesWithPlain(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{offerAuth: true})
	mailer := newTestMailer(t, srv, func(c *SMTPConfig) { c.Username, c.Password = "mailer", "mail-pass" })

	if err := mailer.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatal(err)
	}
	want := "AUTH PLAIN " + base64.StdEncoding.EncodeToString([]byte("\x00mailer\x00mail-pass"))
	if got := srv.messages(); len(got) != 1 || got[0].Auth != want {
		t.Errorf("auth line = %+v, want %q", got, want)
	}
}

func TestSMTPMailerStartTLS(t *testing.T) {
	serverTLS, pool := testTLS(t)
	srv := startFakeSMTP(t, fakeSMTPOptions{tlsConfig: serverTLS})
	mailer := newTestMailer(t, srv, func(c *SMTPConfig) { c.TLS = TLSStartTLS })
	mailer.tlsConfig.RootCAs = pool

	if err := mailer.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatalf("SendEmailVerification() error = %v", err)
	}
	if got := srv.messages(); len(got) != 1 || !got[0].TLS {
		t.Errorf("messages = %+v, want one sent over TLS", got)
	}
}

func TestSMTPMailerImplicitTLS(t *testing.T) {
	serverTLS, pool := testTLS(t)
	srv := startFakeSMTP(t, fakeSMTPOptions{tlsConfig: serverTLS, implicitTLS: true})
	mailer := newTestMailer(t, srv, func(c *SMTPConfig) { c.TLS = TLSImplicit })
	mailer.tlsConfig.RootCAs = pool

	if err := mailer.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatalf("SendEmailVerification() error = %v", err)
	}
	if got := srv.messages(); len(got) != 1 || !got[0].TLS {
		t.Errorf("messages = %+v, want one sent over TLS", got)
	}
}

func TestSMTPMailerRefusesAnUntrustedCertificate(t *testing.T) {
	serverTLS, _ := testTLS(t) // the mailer does not trust this certificate
	srv := startFakeSMTP(t, fakeSMTPOptions{tlsConfig: serverTLS, implicitTLS: true})
	mailer := newTestMailer(t, srv, func(c *SMTPConfig) { c.TLS = TLSImplicit })

	if err := mailer.SendEmailVerification(t.Context(), testOTPMail(t)); err == nil {
		t.Fatal("SendEmailVerification() accepted a certificate nobody vouches for")
	}
	if got := srv.messages(); len(got) != 0 {
		t.Errorf("a message was delivered over an unverified connection: %+v", got)
	}
}

func TestSMTPMailerReportsServerRejection(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{rejectRcpt: true})
	mailer := newTestMailer(t, srv, nil)

	err := mailer.SendEmailVerification(t.Context(), testOTPMail(t))
	var tpErr *textproto.Error
	if !errors.As(err, &tpErr) || tpErr.Code != 550 {
		t.Errorf("error = %v, want an SMTP 550 error", err)
	}
}

func TestSMTPMailerTimesOut(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{stall: true})
	mailer := newTestMailer(t, srv, func(c *SMTPConfig) { c.Timeout = 100 * time.Millisecond })

	start := time.Now()
	err := mailer.SendEmailVerification(t.Context(), testOTPMail(t))
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Errorf("error = %v, want a timeout", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v, the 100ms timeout was not applied", time.Since(start))
	}
}

func TestSMTPMailerStopsAtTheContextDeadline(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{stall: true})
	mailer := newTestMailer(t, srv, nil) // 5s timeout: only the context can end this quickly

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := mailer.SendEmailVerification(ctx, testOTPMail(t)); err == nil {
		t.Fatal("SendEmailVerification() succeeded against a server that never answers")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v after the context ended", time.Since(start))
	}
}

func TestSMTPMailerStopsWhenTheContextIsCancelled(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{stall: true})
	mailer := newTestMailer(t, srv, nil) // 5s timeout and no context deadline: only cancel() can end this quickly

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	err := mailer.SendEmailVerification(ctx, testOTPMail(t))
	if err == nil {
		t.Fatal("SendEmailVerification() succeeded against a server that never answers")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %v after cancel, the connection was not closed", elapsed)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Errorf("error = %v, want a cancellation (closed connection), not the 5s send timeout", err)
	}
}

func TestSMTPMailerFailsWhenNothingListens(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{})
	mailer := newTestMailer(t, srv, nil)
	_ = srv.ln.Close()

	err := mailer.SendEmailVerification(t.Context(), testOTPMail(t))
	if err == nil {
		t.Fatal("SendEmailVerification() succeeded without a server")
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) || opErr.Op != "dial" {
		t.Errorf("error = %v, want a dial error", err)
	}
}

func TestNewSMTPMailerValidatesConfig(t *testing.T) {
	good := SMTPConfig{Host: "mailpit", Port: 1025, From: "no-reply@localhost", TLS: TLSNone, Timeout: time.Second}
	if _, err := NewSMTPMailer(good); err != nil {
		t.Fatalf("NewSMTPMailer(good) error = %v", err)
	}
	for name, mutate := range map[string]func(*SMTPConfig){
		"no host":        func(c *SMTPConfig) { c.Host = "" },
		"port zero":      func(c *SMTPConfig) { c.Port = 0 },
		"port too large": func(c *SMTPConfig) { c.Port = 65536 },
		"unknown tls":    func(c *SMTPConfig) { c.TLS = "ssl" },
		"no timeout":     func(c *SMTPConfig) { c.Timeout = 0 },
		"unsafe sender":  func(c *SMTPConfig) { c.From = "a@b.com\r\nBcc: x@y.com" },
		"username only":  func(c *SMTPConfig) { c.Username = "mailer" },
		"password only":  func(c *SMTPConfig) { c.Password = "pw" },
	} {
		cfg := good
		mutate(&cfg)
		if _, err := NewSMTPMailer(cfg); err == nil {
			t.Errorf("%s: NewSMTPMailer() accepted the config", name)
		}
	}
}

func TestSMTPMailerNeverPrintsItsPassword(t *testing.T) {
	m, err := NewSMTPMailer(SMTPConfig{
		Host: "smtp.example.com", Port: 587, Username: "mailer", Password: "S3cretMailPass",
		From: "no-reply@example.com", TLS: TLSStartTLS, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{
		fmt.Sprint(m), fmt.Sprintf("%v", m), fmt.Sprintf("%+v", m), fmt.Sprintf("%#v", m), fmt.Sprintf("%+v", *m),
		fmt.Sprintf("%+v", m.cfg), fmt.Sprintf("%#v", m.cfg),
	} {
		if strings.Contains(out, "S3cretMailPass") {
			t.Errorf("output %q leaks the SMTP password", out)
		}
	}
}

func TestSMTPConfigAndMailerStayOutOfJSONLogsAndMarshal(t *testing.T) {
	cfg := SMTPConfig{
		Host: "smtp.example.com", Port: 587, Username: "mailer-user-77", Password: "S3cretMailPass",
		From: "no-reply@example.com", TLS: TLSStartTLS, Timeout: time.Second,
	}
	m, err := NewSMTPMailer(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("x", slog.Any("smtp", cfg), slog.Any("smtpPtr", &cfg),
		slog.Any("mailer", m), slog.Any("mailerValue", *m), slog.Any("mailerPtr", &m))
	outputs := map[string]string{"slog": buf.String()}
	for name, v := range map[string]any{"cfg": cfg, "cfgPtr": &cfg, "mailer": m, "mailerValue": *m} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("json.Marshal(%s): %v", name, err)
		}
		outputs["json "+name] = string(raw)
	}
	for name, out := range outputs {
		for _, secret := range []string{"S3cretMailPass", "mailer-user-77"} {
			if strings.Contains(out, secret) {
				t.Errorf("%s output %q leaks %q", name, out, secret)
			}
		}
	}
	if !strings.Contains(outputs["slog"], "smtp.example.com") {
		t.Errorf("slog output %q should still name the host", outputs["slog"])
	}
	if !strings.Contains(outputs["json cfg"], "smtp.example.com") {
		t.Errorf("json output %q should still name the host", outputs["json cfg"])
	}
}

func TestSMTPMailerTreatsAFailingQuitAfterDataAsDelivered(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{failQuit: true})
	mailer := newTestMailer(t, srv, nil)
	if err := mailer.SendEmailVerification(t.Context(), testOTPMail(t)); err != nil {
		t.Fatalf("SendEmailVerification() error = %v, but the server had already accepted the message", err)
	}
	if got := srv.messages(); len(got) != 1 {
		t.Errorf("server received %d messages, want 1", len(got))
	}
}

func TestSMTPMailerReportsCancellationAsTheContextError(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{stall: true})
	mailer := newTestMailer(t, srv, nil)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)

	err := mailer.SendEmailVerification(ctx, testOTPMail(t))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want one that wraps context.Canceled so the dispatcher logs it as canceled", err)
	}
}
