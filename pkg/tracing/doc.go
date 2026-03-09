// Package tracing provides OpenTelemetry tracing setup and helpers for BEME.
// It initialises a global tracer provider backed by a stdout exporter (for development),
// and exposes convenience wrappers for starting spans and extracting trace/span IDs.
package tracing
