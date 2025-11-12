package executors

import (
	"context"
	"encoding/json"
)

// Executor defines the interface for task executors
type Executor interface {
	// Execute runs a task with the given input and returns output or error
	Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)

	// Type returns the executor type name (e.g., "http", "s3", "csv")
	Type() string
}
