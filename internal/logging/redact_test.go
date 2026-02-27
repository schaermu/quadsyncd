package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// capturingHandler records log records for inspection in tests.
type capturingHandler struct {
	records []slog.Record
	level   slog.Level
}

func (c *capturingHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= c.level
}

func (c *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	c.records = append(c.records, r.Clone())
	return nil
}

func (c *capturingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return c
}

func (c *capturingHandler) WithGroup(_ string) slog.Handler {
	return c
}

func TestRedactingHandler_RedactsMessage(t *testing.T) {
	cap := &capturingHandler{}
	h := NewRedactingHandler(cap, []string{"super-secret"})

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "error: super-secret in url", 0)
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(cap.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(cap.records))
	}
	if strings.Contains(cap.records[0].Message, "super-secret") {
		t.Errorf("expected secret to be redacted from message, got: %q", cap.records[0].Message)
	}
	if !strings.Contains(cap.records[0].Message, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in message, got: %q", cap.records[0].Message)
	}
}

func TestRedactingHandler_RedactsStringAttr(t *testing.T) {
	cap := &capturingHandler{}
	h := NewRedactingHandler(cap, []string{"my-token"})

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "git auth", 0)
	r.AddAttrs(slog.String("token", "my-token"))
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(cap.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(cap.records))
	}

	var gotToken string
	cap.records[0].Attrs(func(a slog.Attr) bool {
		if a.Key == "token" {
			gotToken = a.Value.String()
		}
		return true
	})

	if strings.Contains(gotToken, "my-token") {
		t.Errorf("expected secret to be redacted from attr, got: %q", gotToken)
	}
	if gotToken != "[REDACTED]" {
		t.Errorf("expected [REDACTED] in attr, got: %q", gotToken)
	}
}

func TestRedactingHandler_PreservesNonSecretContent(t *testing.T) {
	cap := &capturingHandler{}
	h := NewRedactingHandler(cap, []string{"secret-value"})

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "sync completed", 0)
	r.AddAttrs(slog.String("repo", "https://github.com/org/repo"))
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(cap.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(cap.records))
	}
	if cap.records[0].Message != "sync completed" {
		t.Errorf("expected message unchanged, got: %q", cap.records[0].Message)
	}

	var gotRepo string
	cap.records[0].Attrs(func(a slog.Attr) bool {
		if a.Key == "repo" {
			gotRepo = a.Value.String()
		}
		return true
	})
	if gotRepo != "https://github.com/org/repo" {
		t.Errorf("expected repo attr unchanged, got: %q", gotRepo)
	}
}

func TestRedactingHandler_EmptySecretsPassThrough(t *testing.T) {
	cap := &capturingHandler{}
	h := NewRedactingHandler(cap, []string{"", ""})

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "hello world", 0)
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(cap.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(cap.records))
	}
	if cap.records[0].Message != "hello world" {
		t.Errorf("expected message unchanged, got: %q", cap.records[0].Message)
	}
}

func TestRedactingHandler_MultipleSecrets(t *testing.T) {
	cap := &capturingHandler{}
	h := NewRedactingHandler(cap, []string{"token-abc", "webhook-xyz"})

	r := slog.NewRecord(time.Now(), slog.LevelWarn, "using token-abc and webhook-xyz", 0)
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	msg := cap.records[0].Message
	if strings.Contains(msg, "token-abc") || strings.Contains(msg, "webhook-xyz") {
		t.Errorf("expected both secrets redacted, got: %q", msg)
	}
}

func TestRedactingHandler_RedactsGroupAttr(t *testing.T) {
	cap := &capturingHandler{}
	h := NewRedactingHandler(cap, []string{"s3cr3t"})

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "auth", 0)
	r.AddAttrs(slog.Group("auth",
		slog.String("token", "s3cr3t"),
		slog.String("user", "alice"),
	))
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(cap.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(cap.records))
	}

	var tokenVal, userVal string
	cap.records[0].Attrs(func(a slog.Attr) bool {
		if a.Key == "auth" {
			for _, sub := range a.Value.Group() {
				if sub.Key == "token" {
					tokenVal = sub.Value.String()
				}
				if sub.Key == "user" {
					userVal = sub.Value.String()
				}
			}
		}
		return true
	})

	if tokenVal != "[REDACTED]" {
		t.Errorf("expected token in group to be [REDACTED], got: %q", tokenVal)
	}
	if userVal != "alice" {
		t.Errorf("expected user in group to be unchanged, got: %q", userVal)
	}
}

func TestRedactingHandler_Enabled(t *testing.T) {
	cap := &capturingHandler{level: slog.LevelWarn}
	h := NewRedactingHandler(cap, []string{"secret"})

	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("expected Enabled(Info) = false when inner level is Warn")
	}
	if !h.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("expected Enabled(Warn) = true when inner level is Warn")
	}
}

func TestRedactingHandler_WithAttrsPreservesSecrets(t *testing.T) {
	cap := &capturingHandler{}
	h := NewRedactingHandler(cap, []string{"my-secret"})
	h2 := h.WithAttrs([]slog.Attr{slog.String("component", "sync")})

	rh, ok := h2.(*RedactingHandler)
	if !ok {
		t.Fatalf("expected *RedactingHandler, got %T", h2)
	}
	if len(rh.secrets) != 1 || rh.secrets[0] != "my-secret" {
		t.Errorf("expected secrets to be preserved after WithAttrs")
	}
}

func TestRedactingHandler_WithGroupPreservesSecrets(t *testing.T) {
	cap := &capturingHandler{}
	h := NewRedactingHandler(cap, []string{"my-secret"})
	h2 := h.WithGroup("g")

	rh, ok := h2.(*RedactingHandler)
	if !ok {
		t.Fatalf("expected *RedactingHandler, got %T", h2)
	}
	if len(rh.secrets) != 1 || rh.secrets[0] != "my-secret" {
		t.Errorf("expected secrets to be preserved after WithGroup")
	}
}

func TestRedactingHandler_IntegrationWithNDJSON(t *testing.T) {
	var buf bytes.Buffer
	ndjson := NewNDJSONHandler(func(line []byte) error {
		buf.Write(line)
		buf.WriteByte('\n')
		return nil
	}, nil)
	h := NewRedactingHandler(ndjson, []string{"webhook-secret-xyz"})
	logger := slog.New(h)

	logger.Info("received webhook", "token", "webhook-secret-xyz")

	out := buf.String()
	if strings.Contains(out, "webhook-secret-xyz") {
		t.Errorf("expected secret to be redacted in NDJSON output, got: %s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in NDJSON output, got: %s", out)
	}
}
