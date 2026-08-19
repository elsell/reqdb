package observability

import (
	"context"
	"log/slog"

	"github.com/elsell/reqdb/internal/ports"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

type Fanout []ports.EventSink

func (sinks Fanout) Record(ctx context.Context, event ports.Event) {
	for _, sink := range sinks {
		sink.Record(ctx, event)
	}
}

type LogSink struct{ Logger *slog.Logger }

func (sink LogSink) Record(ctx context.Context, event ports.Event) {
	args := []any{"event", event.Name, "correlation_id", event.CorrelationID, "causation_id", event.CausationID}
	for key, value := range event.Fields {
		args = append(args, key, value)
	}
	sink.Logger.InfoContext(ctx, "domain event", args...)
}

type OTelSink struct{}

func (OTelSink) Record(ctx context.Context, event ports.Event) {
	_, span := otel.Tracer("reqdb").Start(ctx, event.Name)
	span.SetAttributes(attribute.String("correlation.id", event.CorrelationID), attribute.String("causation.id", event.CausationID))
	span.End()
}
