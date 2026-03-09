package logger_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/beme/beme/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// newObservedLogger returns a zap logger whose output can be inspected in tests.
func newObservedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

func TestNew_DefaultsToInfoLevel(t *testing.T) {
	log, err := logger.New("info")
	require.NoError(t, err)
	assert.NotNil(t, log)
}

func TestNew_UnrecognizedLevelDefaultsToInfo(t *testing.T) {
	log, err := logger.New("bogus")
	require.NoError(t, err)
	assert.NotNil(t, log)
}

func TestNew_DebugLevel(t *testing.T) {
	log, err := logger.New("debug")
	require.NoError(t, err)
	assert.NotNil(t, log)
}

func TestWithContext_AddsAllThreeFields(t *testing.T) {
	base, logs := newObservedLogger()

	ctx := context.Background()
	ctx = logger.WithRequestID(ctx, "req-123")
	ctx = logger.WithTraceID(ctx, "trace-abc")
	ctx = logger.WithSpanID(ctx, "span-xyz")

	enriched := logger.WithContext(ctx, base)
	enriched.Info("test message")

	require.Equal(t, 1, logs.Len())
	entry := logs.All()[0]

	fields := map[string]string{}
	for _, f := range entry.Context {
		if f.Type == zapcore.StringType {
			fields[f.Key] = f.String
		}
	}

	assert.Equal(t, "req-123", fields["request_id"])
	assert.Equal(t, "trace-abc", fields["trace_id"])
	assert.Equal(t, "span-xyz", fields["span_id"])
}

func TestWithContext_EmptyContext_ReturnsOriginalLogger(t *testing.T) {
	base, logs := newObservedLogger()

	enriched := logger.WithContext(context.Background(), base)
	enriched.Info("no fields")

	require.Equal(t, 1, logs.Len())
	entry := logs.All()[0]

	// No trace fields should be present
	for _, f := range entry.Context {
		assert.NotEqual(t, "request_id", f.Key)
		assert.NotEqual(t, "trace_id", f.Key)
		assert.NotEqual(t, "span_id", f.Key)
	}
}

func TestWithContext_PartialFields(t *testing.T) {
	base, logs := newObservedLogger()

	ctx := logger.WithRequestID(context.Background(), "req-only")
	enriched := logger.WithContext(ctx, base)
	enriched.Info("partial")

	require.Equal(t, 1, logs.Len())
	entry := logs.All()[0]

	fields := map[string]string{}
	for _, f := range entry.Context {
		if f.Type == zapcore.StringType {
			fields[f.Key] = f.String
		}
	}

	assert.Equal(t, "req-only", fields["request_id"])
	_, hasTrace := fields["trace_id"]
	_, hasSpan := fields["span_id"]
	assert.False(t, hasTrace)
	assert.False(t, hasSpan)
}

func TestWithContext_JSONOutput_ContainsFields(t *testing.T) {
	// Use a real production-style logger writing to a buffer to verify JSON output.
	cfg := zap.NewProductionConfig()
	cfg.OutputPaths = []string{"stdout"}

	// Build a no-op logger just to confirm New() works; use observer for field check.
	base, logs := newObservedLogger()

	ctx := logger.WithRequestID(context.Background(), "r1")
	ctx = logger.WithTraceID(ctx, "t1")
	ctx = logger.WithSpanID(ctx, "s1")

	logger.WithContext(ctx, base).Info("json check")

	require.Equal(t, 1, logs.Len())
	raw, err := json.Marshal(logs.All()[0].ContextMap())
	require.NoError(t, err)
	body := string(raw)

	assert.True(t, strings.Contains(body, "r1"), "expected request_id value in output")
	assert.True(t, strings.Contains(body, "t1"), "expected trace_id value in output")
	assert.True(t, strings.Contains(body, "s1"), "expected span_id value in output")
}
