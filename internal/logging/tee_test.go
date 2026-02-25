package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestTeeHandler(t *testing.T) {
	var buf1, buf2 bytes.Buffer

	handler1 := slog.NewTextHandler(&buf1, &slog.HandlerOptions{Level: slog.LevelInfo})
	handler2 := slog.NewTextHandler(&buf2, &slog.HandlerOptions{Level: slog.LevelInfo})

	teeHandler := NewTeeHandler(handler1, handler2)
	logger := slog.New(teeHandler)

	logger.Info("test message", "key", "value")

	// Both buffers should contain the log message
	output1 := buf1.String()
	output2 := buf2.String()

	if !strings.Contains(output1, "test message") {
		t.Errorf("handler1 output missing message: %s", output1)
	}
	if !strings.Contains(output2, "test message") {
		t.Errorf("handler2 output missing message: %s", output2)
	}
}

func TestNDJSONHandler(t *testing.T) {
	var lines [][]byte

	handler := NewNDJSONHandler(func(line []byte) error {
		lines = append(lines, append([]byte(nil), line...))
		return nil
	}, &NDJSONHandlerOptions{
		Level: slog.LevelInfo,
	})

	logger := slog.New(handler)

	// Log multiple messages
	logger.Info("first message", "count", 1)
	logger.Debug("debug message", "count", 2) // Should not appear (level too low)
	logger.Warn("warning message", "count", 3)

	// Should have 2 records (info and warn, not debug)
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines, got %d", len(lines))
	}

	// Parse first line
	var record1 map[string]interface{}
	if err := json.Unmarshal(lines[0], &record1); err != nil {
		t.Fatalf("failed to parse first log line: %v", err)
	}

	if record1["msg"] != "first message" {
		t.Errorf("msg = %q, want %q", record1["msg"], "first message")
	}
	if record1["level"] != "INFO" {
		t.Errorf("level = %q, want %q", record1["level"], "INFO")
	}
	if record1["count"] != float64(1) {
		t.Errorf("count = %v, want 1", record1["count"])
	}

	// Parse second line
	var record2 map[string]interface{}
	if err := json.Unmarshal(lines[1], &record2); err != nil {
		t.Fatalf("failed to parse second log line: %v", err)
	}

	if record2["msg"] != "warning message" {
		t.Errorf("msg = %q, want %q", record2["msg"], "warning message")
	}
	if record2["level"] != "WARN" {
		t.Errorf("level = %q, want %q", record2["level"], "WARN")
	}
}

func TestNDJSONHandler_WithAttrs(t *testing.T) {
	var lines [][]byte

	handler := NewNDJSONHandler(func(line []byte) error {
		lines = append(lines, append([]byte(nil), line...))
		return nil
	}, &NDJSONHandlerOptions{
		Level: slog.LevelInfo,
	})

	logger := slog.New(handler).With("run_id", "123")

	logger.Info("test message", "key", "value")

	if len(lines) != 1 {
		t.Fatalf("expected 1 log line, got %d", len(lines))
	}

	var record map[string]interface{}
	if err := json.Unmarshal(lines[0], &record); err != nil {
		t.Fatalf("failed to parse log line: %v", err)
	}

	if record["run_id"] != "123" {
		t.Errorf("run_id = %v, want 123", record["run_id"])
	}
	if record["key"] != "value" {
		t.Errorf("key = %v, want value", record["key"])
	}
}

func TestNDJSONHandler_WithGroup(t *testing.T) {
	var lines [][]byte

	handler := NewNDJSONHandler(func(line []byte) error {
		lines = append(lines, append([]byte(nil), line...))
		return nil
	}, &NDJSONHandlerOptions{
		Level: slog.LevelInfo,
	})

	logger := slog.New(handler).WithGroup("metadata")

	logger.Info("test message", "key", "value")

	if len(lines) != 1 {
		t.Fatalf("expected 1 log line, got %d", len(lines))
	}

	var record map[string]interface{}
	if err := json.Unmarshal(lines[0], &record); err != nil {
		t.Fatalf("failed to parse log line: %v", err)
	}

	metadata, ok := record["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("metadata group not found or wrong type: %v", record)
	}

	if metadata["key"] != "value" {
		t.Errorf("metadata.key = %v, want value", metadata["key"])
	}
}

func TestNDJSONHandler_TimeFormat(t *testing.T) {
	var lines [][]byte

	handler := NewNDJSONHandler(func(line []byte) error {
		lines = append(lines, append([]byte(nil), line...))
		return nil
	}, &NDJSONHandlerOptions{
		Level: slog.LevelInfo,
	})

	logger := slog.New(handler)
	logger.Info("test message")

	if len(lines) != 1 {
		t.Fatalf("expected 1 log line, got %d", len(lines))
	}

	var record map[string]interface{}
	if err := json.Unmarshal(lines[0], &record); err != nil {
		t.Fatalf("failed to parse log line: %v", err)
	}

	timeStr, ok := record["time"].(string)
	if !ok {
		t.Fatalf("time field not found or wrong type: %v", record)
	}

	// Verify time can be parsed in RFC3339Nano format
	_, err := time.Parse(time.RFC3339Nano, timeStr)
	if err != nil {
		t.Errorf("failed to parse time %q: %v", timeStr, err)
	}
}

func TestTeeHandler_Enabled(t *testing.T) {
	handler1 := NewNDJSONHandler(func([]byte) error { return nil }, &NDJSONHandlerOptions{Level: slog.LevelInfo})
	handler2 := NewNDJSONHandler(func([]byte) error { return nil }, &NDJSONHandlerOptions{Level: slog.LevelWarn})

	teeHandler := NewTeeHandler(handler1, handler2)

	ctx := context.Background()

	// Should be enabled for Info because handler1 accepts it
	if !teeHandler.Enabled(ctx, slog.LevelInfo) {
		t.Error("expected tee handler to be enabled for Info level")
	}

	// Should be enabled for Warn because both accept it
	if !teeHandler.Enabled(ctx, slog.LevelWarn) {
		t.Error("expected tee handler to be enabled for Warn level")
	}

	// Should not be enabled for Debug because neither accepts it
	if teeHandler.Enabled(ctx, slog.LevelDebug) {
		t.Error("expected tee handler to be disabled for Debug level")
	}
}
