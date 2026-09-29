// Package logger provides structured logging for SupportFlast Engine.
// It wraps Go's log/slog with JSON output (production) or Text output (development),
// context-based request ID propagation, and an [ENGINE] prefix for compatibility.
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// contextKey is an unexported type used as key for context values to avoid collisions.
type contextKey struct{}

var requestIDKey = contextKey{}

// defaultLogger holds the package-level logger instance.
var defaultLogger *slog.Logger

func init() {
	// Initialize with a sensible default (text handler, info level) so the logger
	// is always usable even if Init() is never called.
	defaultLogger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})).With("component", "[ENGINE]")
}

// Init initializes the global logger.
//   - level: one of "debug", "info", "warn", "error" (case-insensitive).
//   - env: "production" uses JSON handler; anything else uses Text handler.
func Init(level string, env string) {
	lvl := parseLevel(level)
	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	if strings.ToLower(env) == "production" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	defaultLogger = slog.New(handler).With("component", "[ENGINE]")
	slog.SetDefault(defaultLogger)
}

// parseLevel converts a string level name to slog.Level.
func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Info logs a message at INFO level.
func Info(msg string, args ...any) {
	defaultLogger.Info(msg, args...)
}

// Warn logs a message at WARN level.
func Warn(msg string, args ...any) {
	defaultLogger.Warn(msg, args...)
}

// Error logs a message at ERROR level.
func Error(msg string, args ...any) {
	defaultLogger.Error(msg, args...)
}

// Debug logs a message at DEBUG level.
func Debug(msg string, args ...any) {
	defaultLogger.Debug(msg, args...)
}

// WithRequestID returns a new context that carries the given request ID.
// Use FromContext to retrieve a logger that automatically includes the request ID.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

// FromContext returns a logger enriched with the request ID stored in ctx (if any).
// If no request ID is present, it returns the default logger unchanged.
func FromContext(ctx context.Context) *slog.Logger {
	if rid, ok := ctx.Value(requestIDKey).(string); ok && rid != "" {
		return defaultLogger.With("request_id", rid)
	}
	return defaultLogger
}

// GetDefault returns the current default logger instance.
func GetDefault() *slog.Logger {
	return defaultLogger
}
