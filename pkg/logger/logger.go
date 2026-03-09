package logger

import (
	"context"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// contextKey is an unexported type for context keys in this package.
type contextKey int

const (
	requestIDKey contextKey = iota
	traceIDKey
	spanIDKey
)

// New creates a zap production logger at the given log level.
// level must be one of: "debug", "info", "warn", "error", "dpanic", "panic", "fatal".
// Defaults to "info" if the level string is unrecognized.
func New(level string) (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()

	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		zapLevel = zapcore.InfoLevel
	}
	cfg.Level = zap.NewAtomicLevelAt(zapLevel)

	return cfg.Build()
}

// WithRequestID stores a request ID in the context.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

// WithTraceID stores a trace ID in the context.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// WithSpanID stores a span ID in the context.
func WithSpanID(ctx context.Context, spanID string) context.Context {
	return context.WithValue(ctx, spanIDKey, spanID)
}

// WithContext returns a child logger enriched with request_id, trace_id, and
// span_id fields extracted from ctx. Fields with empty values are omitted.
func WithContext(ctx context.Context, logger *zap.Logger) *zap.Logger {
	fields := make([]zap.Field, 0, 3)

	if v, ok := ctx.Value(requestIDKey).(string); ok && v != "" {
		fields = append(fields, zap.String("request_id", v))
	}
	if v, ok := ctx.Value(traceIDKey).(string); ok && v != "" {
		fields = append(fields, zap.String("trace_id", v))
	}
	if v, ok := ctx.Value(spanIDKey).(string); ok && v != "" {
		fields = append(fields, zap.String("span_id", v))
	}

	if len(fields) == 0 {
		return logger
	}
	return logger.With(fields...)
}
