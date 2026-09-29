package logger

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// captureOutput redirects defaultLogger to a buffer and returns captured output.
func captureOutput(t *testing.T, env string, level string, fn func()) string {
	t.Helper()
	var buf bytes.Buffer

	lvl := parseLevel(level)
	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	if env == "production" {
		handler = slog.NewJSONHandler(&buf, opts)
	} else {
		handler = slog.NewTextHandler(&buf, opts)
	}

	old := defaultLogger
	defaultLogger = slog.New(handler).With("component", "[ENGINE]")
	defer func() { defaultLogger = old }()

	fn()
	return buf.String()
}

func TestInit(t *testing.T) {
	// Init should not panic for various levels and envs.
	levels := []string{"debug", "info", "warn", "error", "unknown"}
	envs := []string{"production", "development", ""}
	for _, lvl := range levels {
		for _, env := range envs {
			Init(lvl, env)
		}
	}
	// Restore a safe default.
	Init("info", "development")
}

func TestInfoOutput(t *testing.T) {
	out := captureOutput(t, "production", "info", func() {
		Info("hello world", "key", "value")
	})
	if !strings.Contains(out, "hello world") {
		t.Errorf("expected 'hello world' in output, got: %s", out)
	}
	if !strings.Contains(out, `"key"`) || !strings.Contains(out, `"value"`) {
		t.Errorf("expected key/value in output, got: %s", out)
	}
	if !strings.Contains(out, "[ENGINE]") {
		t.Errorf("expected [ENGINE] component in output, got: %s", out)
	}
}

func TestWarnOutput(t *testing.T) {
	out := captureOutput(t, "development", "info", func() {
		Warn("something fishy", "code", 42)
	})
	if !strings.Contains(out, "something fishy") {
		t.Errorf("expected warn message in output, got: %s", out)
	}
}

func TestErrorOutput(t *testing.T) {
	out := captureOutput(t, "production", "info", func() {
		Error("failure", "err", "timeout")
	})
	if !strings.Contains(out, "failure") {
		t.Errorf("expected error message in output, got: %s", out)
	}
	if !strings.Contains(out, "ERROR") {
		t.Errorf("expected ERROR level in output, got: %s", out)
	}
}

func TestDebugNotShownAtInfoLevel(t *testing.T) {
	out := captureOutput(t, "development", "info", func() {
		Debug("should not appear")
	})
	if strings.Contains(out, "should not appear") {
		t.Errorf("debug message should not appear at info level, got: %s", out)
	}
}

func TestDebugShownAtDebugLevel(t *testing.T) {
	out := captureOutput(t, "development", "debug", func() {
		Debug("should appear")
	})
	if !strings.Contains(out, "should appear") {
		t.Errorf("debug message should appear at debug level, got: %s", out)
	}
}

func TestWithRequestIDAndFromContext(t *testing.T) {
	ctx := context.Background()
	rid := "req-abc-123"
	ctx = WithRequestID(ctx, rid)

	// Verify we can extract the logger with request_id.
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	old := defaultLogger
	defaultLogger = slog.New(handler).With("component", "[ENGINE]")
	defer func() { defaultLogger = old }()

	l := FromContext(ctx)
	l.Info("test with rid")
	output := buf.String()

	if !strings.Contains(output, rid) {
		t.Errorf("expected request_id %q in output, got: %s", rid, output)
	}
	if !strings.Contains(output, "request_id") {
		t.Errorf("expected 'request_id' key in output, got: %s", output)
	}
}

func TestFromContextWithoutRequestID(t *testing.T) {
	ctx := context.Background()
	l := FromContext(ctx)
	if l == nil {
		t.Error("FromContext should never return nil")
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"unknown", slog.LevelInfo},
		{"", slog.LevelInfo},
	}
	for _, tt := range tests {
		got := parseLevel(tt.input)
		if got != tt.expected {
			t.Errorf("parseLevel(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

// Ensure the package compiles with slog from stdlib.
func TestStdlibCompatibility(t *testing.T) {
	_ = os.Stdout // reference os to ensure import
	Init("info", "production")
	Info("stdlib compat check")
}
