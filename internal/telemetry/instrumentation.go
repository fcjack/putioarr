package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// CARDINALITY BEST PRACTICES:
//
// High cardinality attributes (unique values per request) should NEVER be added to spans
// that contribute to metrics, as they create unbounded metric series and can cause:
// - Memory exhaustion
// - Query performance degradation
// - Storage cost explosion
//
// AVOID these as span attributes:
// - User IDs, session IDs, request IDs
// - File names, file paths, URLs with unique parameters
// - Timestamps, random values, UUIDs
// - Error messages with dynamic content
// - Transfer names, torrent names, download paths
//
// SAFE attributes (bounded cardinality):
// - Operation types (limited set: "download", "upload", "delete")
// - Status values (limited set: "success", "error", "timeout")
// - Client types (limited set: "putio")
// - Component names (limited set: "database", "download_client")
//
// For debugging, high-cardinality data should be:
// - Added to span status/events (not attributes)
// - Logged with correlation IDs
// - Stored in trace context for propagation

// InstrumentedFunc represents a function that can be instrumented.
type InstrumentedFunc func(ctx context.Context) error

// InstrumentOperation instruments a generic operation with telemetry.
func (t *Telemetry) InstrumentOperation(ctx context.Context, operationName, component string, fn InstrumentedFunc) error {
	if t == nil || t.tracer == nil {
		return fn(ctx)
	}

	start := time.Now()
	ctx, span := t.tracer.Start(ctx, operationName)

	defer span.End()

	span.SetAttributes(
		attribute.String("component", component),
		attribute.String("operation", operationName),
	)

	err := fn(ctx)
	duration := time.Since(start)

	status := "success"
	if err != nil {
		status = "error"

		span.SetAttributes(
			attribute.Bool("error", true),
			// Note: error.message is intentionally NOT added as attribute to prevent
			// high cardinality from unique error messages. Full error is in span status.
		)
		span.SetStatus(codes.Error, err.Error())
	}

	span.SetAttributes(
		attribute.String("status", status),
		attribute.Float64("duration_seconds", duration.Seconds()),
	)

	return err
}

// InstrumentDBOperation instruments database operations.
func (t *Telemetry) InstrumentDBOperation(ctx context.Context, operation string, fn InstrumentedFunc) error {
	if t == nil {
		return fn(ctx)
	}

	start := time.Now()
	err := t.InstrumentOperation(ctx, "db_"+operation, "database", fn)
	duration := time.Since(start)

	status := "success"
	if err != nil {
		status = "error"
	}

	t.RecordDBOperation(ctx, operation, status, duration)

	return err
}

// InstrumentClientOperation instruments download client operations.
func (t *Telemetry) InstrumentClientOperation(ctx context.Context, client, operation string, fn InstrumentedFunc) error {
	if t == nil {
		return fn(ctx)
	}

	err := t.InstrumentOperation(ctx, "client_"+operation, "download_client", func(ctx context.Context) error {
		ctx, span := t.tracer.Start(ctx, "client_"+operation)
		defer span.End()

		span.SetAttributes(
			attribute.String("client.type", client),
			attribute.String("client.operation", operation),
		)

		return fn(ctx)
	})

	status := "success"
	if err != nil {
		status = "error"
	}

	t.RecordClientOperation(ctx, client, operation, status)

	return err
}

// InstrumentDownload instruments download operations.
func (t *Telemetry) InstrumentDownload(ctx context.Context, transferID, transferName string, fn InstrumentedFunc) error {
	if t == nil {
		return fn(ctx)
	}

	start := time.Now()

	t.IncrementActiveDownloads(ctx)
	defer t.DecrementActiveDownloads(ctx)

	err := t.InstrumentOperation(ctx, "download", "downloader", func(ctx context.Context) error {
		ctx, span := t.tracer.Start(ctx, "download")
		defer span.End()

		// Note: transfer.id and transfer.name are intentionally NOT added as attributes
		// to prevent high cardinality issues. They are available in logs if needed.
		span.SetAttributes(
			attribute.String("download.type", "transfer"),
		)

		return fn(ctx)
	})

	duration := time.Since(start)

	status := "success"
	if err != nil {
		status = "error"
	}

	t.RecordDownload(ctx, status, duration)

	return err
}

// InstrumentTransfer instruments transfer operations.
func (t *Telemetry) InstrumentTransfer(ctx context.Context, operation string, fn InstrumentedFunc) error {
	if t == nil {
		return fn(ctx)
	}

	t.IncrementActiveTransfers(ctx)
	defer t.DecrementActiveTransfers(ctx)

	err := t.InstrumentOperation(ctx, "transfer_"+operation, "transfer", fn)

	status := "success"
	if err != nil {
		status = "error"
	}

	t.RecordTransfer(ctx, operation, status)

	return err
}
