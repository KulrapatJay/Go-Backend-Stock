package config

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
)

const redactedValue = "[REDACTED]"

// Secret is a configuration value that must never be printed or logged. It formats, marshals and
// logs as [REDACTED]; Reveal and Bytes return the real value for the code that needs it.
type Secret string

func (Secret) String() string   { return redactedValue }
func (Secret) GoString() string { return redactedValue }

// Format makes every fmt verb (%d, %x, %q…) print [REDACTED].
func (Secret) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, redactedValue) }

// MarshalJSON keeps the value out of JSON.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redactedValue + `"`), nil }

// LogValue keeps the value out of slog records.
func (Secret) LogValue() slog.Value { return slog.StringValue(redactedValue) }

// Reveal returns the value.
func (s Secret) Reveal() string { return string(s) }

// Bytes returns the value as bytes, for HMAC keys.
func (s Secret) Bytes() []byte { return []byte(s) }

// String summarises the configuration for humans. It leaves out everything that can hold a
// credential: the database URL, the secrets and the SMTP credentials.
func (c Config) String() string {
	return fmt.Sprintf("config{env=%s addr=%s origin=%s docs=%t try_it_out=%t smtp=%s:%d smtp_tls=%s google=%t}",
		c.Env, c.HTTPAddr, c.PublicOrigin, c.DocsEnabled, c.DocsTryItOutEnabled, c.SMTP.Host, c.SMTP.Port, c.SMTP.TLS,
		c.Google.Enabled())
}

// GoString makes %#v print the same safe summary.
func (c Config) GoString() string { return c.String() }

// MarshalJSON marshals the same safe summary, so encoding a Config cannot expose the database URL.
func (c Config) MarshalJSON() ([]byte, error) { return json.Marshal(c.String()) }

// LogValue makes slog log the same safe summary as a group.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("env", string(c.Env)),
		slog.String("http_addr", c.HTTPAddr),
		slog.String("public_origin", c.PublicOrigin),
		slog.Bool("docs_enabled", c.DocsEnabled),
		slog.Bool("docs_try_it_out_enabled", c.DocsTryItOutEnabled),
		slog.String("smtp_host", c.SMTP.Host),
		slog.Int("smtp_port", c.SMTP.Port),
		slog.String("smtp_tls", c.SMTP.TLS),
		slog.Bool("google_enabled", c.Google.Enabled()),
	)
}
