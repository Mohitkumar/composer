package executors

import (
	"context"
	"encoding/json"
)

// Executor defines the interface for task executors
type Executor interface {
	// Execute runs a task with the given input and returns output or error
	Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)

	// Type returns the executor type name (e.g., "http")
	Type() string
}

type contextKey int

const taskIDKey contextKey = iota

// WithTaskID returns a context carrying the task instance ID.
// Executors read this to set Idempotency-Key headers or pass the ID to
// language servers, so all execution is traceable back to the originating
// task_instances row.
func WithTaskID(ctx context.Context, taskID string) context.Context {
	return context.WithValue(ctx, taskIDKey, taskID)
}

// TaskIDFromContext returns the task instance ID stored in ctx, or "" if none.
func TaskIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(taskIDKey).(string)
	return v
}
