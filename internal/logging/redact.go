package logging

import (
	"context"
	"log/slog"
	"strings"
)

// RedactingHandler wraps a slog.Handler and replaces known sensitive strings
// with "[REDACTED]" in log messages and string attribute values.
// This prevents accidental leakage of secrets (webhook secrets, tokens)
// into stored run logs.
type RedactingHandler struct {
	inner   slog.Handler
	secrets []string
}

// NewRedactingHandler creates a handler that redacts the given sensitive values
// before forwarding records to inner. Empty strings in the secrets slice are ignored.
func NewRedactingHandler(inner slog.Handler, secrets []string) *RedactingHandler {
	// Filter out empty strings to avoid replacing empty substrings everywhere.
	filtered := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if s != "" {
			filtered = append(filtered, s)
		}
	}
	return &RedactingHandler{inner: inner, secrets: filtered}
}

// Enabled reports whether the wrapped handler handles records at the given level.
func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle redacts known secret values from the record before forwarding to inner.
func (h *RedactingHandler) Handle(ctx context.Context, r slog.Record) error {
	if len(h.secrets) == 0 {
		return h.inner.Handle(ctx, r)
	}
	// Redact the log message.
	redacted := slog.NewRecord(r.Time, r.Level, h.redactString(r.Message), r.PC)
	// Copy and redact attributes.
	r.Attrs(func(a slog.Attr) bool {
		redacted.AddAttrs(h.redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, redacted)
}

// WithAttrs returns a new RedactingHandler with the given attributes added to
// the wrapped handler. Attrs are redacted before being forwarded so that secrets
// added via logger.With(...) do not bypass redaction.
func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 || len(h.secrets) == 0 {
		return &RedactingHandler{inner: h.inner.WithAttrs(attrs), secrets: h.secrets}
	}
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = h.redactAttr(a)
	}
	return &RedactingHandler{inner: h.inner.WithAttrs(redacted), secrets: h.secrets}
}

// WithGroup returns a new RedactingHandler with the given group added to the
// wrapped handler.
func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{inner: h.inner.WithGroup(name), secrets: h.secrets}
}

// redactString replaces all occurrences of known secrets with "[REDACTED]".
func (h *RedactingHandler) redactString(s string) string {
	for _, secret := range h.secrets {
		s = strings.ReplaceAll(s, secret, "[REDACTED]")
	}
	return s
}

// redactAttr redacts string values within an attribute, including nested groups.
func (h *RedactingHandler) redactAttr(a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, h.redactString(a.Value.String()))
	case slog.KindGroup:
		group := a.Value.Group()
		redacted := make([]any, len(group))
		for i, sub := range group {
			redacted[i] = h.redactAttr(sub)
		}
		return slog.Group(a.Key, redacted...)
	default:
		return a
	}
}
