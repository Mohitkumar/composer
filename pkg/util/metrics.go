package util

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	MeterName = "github.com/mohitkumar/composer/coordinator"
)

var (
	// Workflow metrics
	workflowStartedCounter    metric.Int64Counter
	workflowCompletedCounter  metric.Int64Counter
	workflowFailedCounter     metric.Int64Counter
	workflowDurationHistogram metric.Float64Histogram

	// Task metrics
	taskCreatedCounter    metric.Int64Counter
	taskStartedCounter    metric.Int64Counter
	taskCompletedCounter  metric.Int64Counter
	taskFailedCounter     metric.Int64Counter
	taskDurationHistogram metric.Float64Histogram

	// System metrics
	outboxProcessedCounter metric.Int64Counter
	outboxFailedCounter    metric.Int64Counter
	outboxDLQCounter       metric.Int64Counter
	outboxInFlightGauge    metric.Int64UpDownCounter

	// Depth gauges — set by the coordinator's background sampler
	RedisStreamDepthGauge metric.Int64Gauge
	OutboxBacklogGauge    metric.Int64Gauge
)

// InitMetrics initializes all metrics. Should be called once at startup.
func InitMetrics() error {
	meter := otel.Meter(MeterName)

	var err error

	// Workflow metrics
	workflowStartedCounter, err = meter.Int64Counter(
		"workflow.started",
		metric.WithDescription("Total number of workflows started"),
	)
	if err != nil {
		return err
	}

	workflowCompletedCounter, err = meter.Int64Counter(
		"workflow.completed",
		metric.WithDescription("Total number of workflows completed successfully"),
	)
	if err != nil {
		return err
	}

	workflowFailedCounter, err = meter.Int64Counter(
		"workflow.failed",
		metric.WithDescription("Total number of workflows that failed"),
	)
	if err != nil {
		return err
	}

	workflowDurationHistogram, err = meter.Float64Histogram(
		"workflow.duration",
		metric.WithDescription("End-to-end duration of workflow execution in seconds"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600, 1800, 3600),
	)
	if err != nil {
		return err
	}

	// Task metrics
	taskCreatedCounter, err = meter.Int64Counter(
		"task.created",
		metric.WithDescription("Total number of tasks created"),
	)
	if err != nil {
		return err
	}

	taskStartedCounter, err = meter.Int64Counter(
		"task.started",
		metric.WithDescription("Total number of tasks started"),
	)
	if err != nil {
		return err
	}

	taskCompletedCounter, err = meter.Int64Counter(
		"task.completed",
		metric.WithDescription("Total number of tasks completed successfully"),
	)
	if err != nil {
		return err
	}

	taskFailedCounter, err = meter.Int64Counter(
		"task.failed",
		metric.WithDescription("Total number of tasks that failed"),
	)
	if err != nil {
		return err
	}

	taskDurationHistogram, err = meter.Float64Histogram(
		"task.duration",
		metric.WithDescription("Duration of individual task execution in seconds (started_at to completed_at)"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60),
	)
	if err != nil {
		return err
	}

	// Outbox metrics
	outboxProcessedCounter, err = meter.Int64Counter(
		"outbox.processed",
		metric.WithDescription("Total number of outbox messages processed"),
	)
	if err != nil {
		return err
	}

	outboxFailedCounter, err = meter.Int64Counter(
		"outbox.failed",
		metric.WithDescription("Total number of outbox messages that failed to process"),
	)
	if err != nil {
		return err
	}

	outboxDLQCounter, err = meter.Int64Counter(
		"outbox.dlq",
		metric.WithDescription("Total number of outbox messages moved to dead-letter queue"),
	)
	if err != nil {
		return err
	}

	outboxInFlightGauge, err = meter.Int64UpDownCounter(
		"outbox.in_flight",
		metric.WithDescription("Current number of outbox messages being processed"),
	)
	if err != nil {
		return err
	}

	RedisStreamDepthGauge, err = meter.Int64Gauge(
		"redis.stream.depth",
		metric.WithDescription("Number of messages in the Redis task stream"),
	)
	if err != nil {
		return err
	}

	OutboxBacklogGauge, err = meter.Int64Gauge(
		"outbox.backlog",
		metric.WithDescription("Number of outbox rows waiting to be published (PENDING + RETRY)"),
	)
	if err != nil {
		return err
	}

	return nil
}

// RecordWorkflowStarted records that a workflow was started
func RecordWorkflowStarted(ctx context.Context, workflowID, runID string) {
	if workflowStartedCounter != nil {
		workflowStartedCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("workflow.id", workflowID),
				attribute.String("workflow.run_id", runID),
			),
		)
	}
}

// RecordWorkflowCompleted records that a workflow completed successfully
func RecordWorkflowCompleted(ctx context.Context, workflowID, runID string, duration time.Duration) {
	if workflowCompletedCounter != nil {
		workflowCompletedCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("workflow.id", workflowID),
				attribute.String("workflow.run_id", runID),
			),
		)
	}
	if workflowDurationHistogram != nil {
		workflowDurationHistogram.Record(ctx, duration.Seconds(),
			metric.WithAttributes(
				attribute.String("workflow.id", workflowID),
				attribute.String("workflow.run_id", runID),
			),
		)
	}
}

// RecordWorkflowFailed records that a workflow failed
func RecordWorkflowFailed(ctx context.Context, workflowID, runID string, duration time.Duration, reason string) {
	if workflowFailedCounter != nil {
		workflowFailedCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("workflow.id", workflowID),
				attribute.String("workflow.run_id", runID),
				attribute.String("error.reason", reason),
			),
		)
	}
	if workflowDurationHistogram != nil {
		workflowDurationHistogram.Record(ctx, duration.Seconds(),
			metric.WithAttributes(
				attribute.String("workflow.id", workflowID),
				attribute.String("workflow.run_id", runID),
				attribute.String("workflow.status", "failed"),
			),
		)
	}
}

// RecordTaskCreated records that a task was created
func RecordTaskCreated(ctx context.Context, taskID, runID, nodeID, nodeType string) {
	if taskCreatedCounter != nil {
		taskCreatedCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("task.id", taskID),
				attribute.String("workflow.run_id", runID),
				attribute.String("task.node_id", nodeID),
				attribute.String("task.node_type", nodeType),
			),
		)
	}
}

// RecordTaskStarted records that a task was started
func RecordTaskStarted(ctx context.Context, taskID, runID, nodeID, nodeType string) {
	if taskStartedCounter != nil {
		taskStartedCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("task.id", taskID),
				attribute.String("workflow.run_id", runID),
				attribute.String("task.node_id", nodeID),
				attribute.String("task.node_type", nodeType),
			),
		)
	}
}

// RecordTaskCompleted records that a task completed successfully
func RecordTaskCompleted(ctx context.Context, taskID, runID, nodeID, nodeType string, duration time.Duration) {
	if taskCompletedCounter != nil {
		taskCompletedCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("task.id", taskID),
				attribute.String("workflow.run_id", runID),
				attribute.String("task.node_id", nodeID),
				attribute.String("task.node_type", nodeType),
			),
		)
	}
	if taskDurationHistogram != nil {
		taskDurationHistogram.Record(ctx, duration.Seconds(),
			metric.WithAttributes(
				attribute.String("task.id", taskID),
				attribute.String("workflow.run_id", runID),
				attribute.String("task.node_id", nodeID),
				attribute.String("task.node_type", nodeType),
			),
		)
	}
}

// RecordTaskFailed records that a task failed
func RecordTaskFailed(ctx context.Context, taskID, runID, nodeID, nodeType string, duration time.Duration, reason string) {
	if taskFailedCounter != nil {
		taskFailedCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("task.id", taskID),
				attribute.String("workflow.run_id", runID),
				attribute.String("task.node_id", nodeID),
				attribute.String("task.node_type", nodeType),
				attribute.String("error.reason", reason),
			),
		)
	}
	if taskDurationHistogram != nil {
		taskDurationHistogram.Record(ctx, duration.Seconds(),
			metric.WithAttributes(
				attribute.String("task.id", taskID),
				attribute.String("workflow.run_id", runID),
				attribute.String("task.node_id", nodeID),
				attribute.String("task.node_type", nodeType),
				attribute.String("task.status", "failed"),
			),
		)
	}
}

// RecordOutboxProcessed records that an outbox message was processed
func RecordOutboxProcessed(ctx context.Context, stream string) {
	if outboxProcessedCounter != nil {
		outboxProcessedCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("outbox.stream", stream),
			),
		)
	}
}

// RecordOutboxFailed records that an outbox message failed to process
func RecordOutboxFailed(ctx context.Context, stream string, reason string) {
	if outboxFailedCounter != nil {
		outboxFailedCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("outbox.stream", stream),
				attribute.String("error.reason", reason),
			),
		)
	}
}

// RecordOutboxDLQ records that an outbox message was moved to DLQ
func RecordOutboxDLQ(ctx context.Context, stream string) {
	if outboxDLQCounter != nil {
		outboxDLQCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("outbox.stream", stream),
			),
		)
	}
}

// RecordOutboxInFlight increments or decrements the in-flight counter
func RecordOutboxInFlight(ctx context.Context, stream string, delta int64) {
	if outboxInFlightGauge != nil {
		outboxInFlightGauge.Add(ctx, delta,
			metric.WithAttributes(
				attribute.String("outbox.stream", stream),
			),
		)
	}
}
