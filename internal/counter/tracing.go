package counter

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

// tracer is named after this package's import path, the OpenTelemetry
// convention for library-level instrumentation.
var tracer = otel.Tracer("homepage/internal/counter")

// withSpan runs fn inside a span named "counter.<op>", recording fn's error
// onto the span before returning it. Flush has no request to be a child
// span of — it runs off Run's own ticker — so this is a root span each
// time, not nested under anything; that's still worth seeing in a trace
// query even without an HTTP parent.
func withSpan(ctx context.Context, op string, fn func(ctx context.Context) error) error {
	ctx, span := tracer.Start(ctx, "counter."+op)
	defer span.End()
	err := fn(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}
