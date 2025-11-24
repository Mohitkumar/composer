package util

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	TracerName = "github.com/mohitkumar/composer/coordinator"
)

// GetTracer returns a tracer for the given name
func GetTracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// Attribute is a helper to create attribute.KeyValue
func Attribute(key, value string) attribute.KeyValue {
	return attribute.String(key, value)
}

// AttributeInt is a helper to create an int attribute
func AttributeInt(key string, value int64) attribute.KeyValue {
	return attribute.Int64(key, value)
}

// StartSpan starts a new span with the given name and attributes
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	tracer := GetTracer(TracerName)
	opts := []trace.SpanStartOption{}
	if len(attrs) > 0 {
		opts = append(opts, trace.WithAttributes(attrs...))
	}
	return tracer.Start(ctx, name, opts...)
}

// StartWorkflowSpan starts a span for workflow operations
func StartWorkflowSpan(ctx context.Context, operation string, workflowID, runID string) (context.Context, trace.Span) {
	return StartSpan(ctx, fmt.Sprintf("workflow.%s", operation),
		attribute.String("workflow.id", workflowID),
		attribute.String("workflow.run_id", runID),
	)
}

// StartTaskSpan starts a span for task operations
func StartTaskSpan(ctx context.Context, operation string, taskID, runID, nodeID string) (context.Context, trace.Span) {
	return StartSpan(ctx, fmt.Sprintf("task.%s", operation),
		attribute.String("task.id", taskID),
		attribute.String("workflow.run_id", runID),
		attribute.String("task.node_id", nodeID),
	)
}

// RecordError records an error on a span
func RecordError(span trace.Span, err error) {
	if span != nil && err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

// SetSpanAttributes sets attributes on a span
func SetSpanAttributes(span trace.Span, attrs ...attribute.KeyValue) {
	if span != nil {
		span.SetAttributes(attrs...)
	}
}

// EndSpan ends a span and records any error
func EndSpan(span trace.Span, err error) {
	if span != nil {
		if err != nil {
			RecordError(span, err)
		}
		span.End()
	}
}
