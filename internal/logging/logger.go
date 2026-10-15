// Package logging builds the application logger injected from the composition root.
package logging

import (
	"io"
	"log/slog"
)

// New returns a JSON logger whose timestamps are UTC and whose records carry the service name.
func New(w io.Writer, level slog.Leveler, service string) *slog.Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey && a.Value.Kind() == slog.KindTime {
				a.Value = slog.TimeValue(a.Value.Time().UTC())
			}
			return a
		},
	})
	return slog.New(handler).With(slog.String("service", service))
}
