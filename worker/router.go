package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mohitkumar/composer/worker/executors"
	"github.com/rs/zerolog"
)

// Router routes tasks to appropriate executors
type Router struct {
	executors  map[string]executors.Executor
	langClient *LangClient
	logger     zerolog.Logger
}

// NewRouter creates a new task router
func NewRouter(langClient *LangClient, logger zerolog.Logger) *Router {
	r := &Router{
		executors:  make(map[string]executors.Executor),
		langClient: langClient,
		logger:     logger,
	}

	// Register built-in executors
	r.RegisterExecutor(executors.NewHTTPExecutor())
	r.RegisterExecutor(executors.NewS3Executor())
	r.RegisterExecutor(executors.NewCSVExecutor())

	return r
}

// RegisterExecutor registers an executor for a specific node type
func (r *Router) RegisterExecutor(exec executors.Executor) {
	r.executors[exec.Type()] = exec
}

// Route routes a task to the appropriate executor based on node type
func (r *Router) Route(ctx context.Context, nodeType string, input json.RawMessage) (json.RawMessage, error) {
	logger := r.logger.With().Str("node_type", nodeType).Logger()

	// Check if it's a language executor (python, js, node, etc.)
	if isLanguageRuntime(nodeType) {
		if r.langClient == nil {
			return nil, fmt.Errorf("language client not configured for runtime: %s", nodeType)
		}
		return r.executeLanguageTask(ctx, nodeType, input, logger)
	}

	// Check for built-in executor
	exec, exists := r.executors[nodeType]
	if !exists {
		return nil, fmt.Errorf("no executor found for node type: %s", nodeType)
	}

	logger.Debug().Msg("routing to built-in executor")
	return exec.Execute(ctx, input)
}

// executeLanguageTask executes a task using a language worker via gRPC
func (r *Router) executeLanguageTask(ctx context.Context, runtime string, input json.RawMessage, logger zerolog.Logger) (json.RawMessage, error) {
	logger.Debug().Str("runtime", runtime).Msg("routing to language executor")

	// Parse input to extract code and other parameters
	var payload map[string]interface{}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse language task input: %w", err)
	}

	code, _ := payload["code"].(string)
	filename, _ := payload["filename"].(string)
	inputJSON, _ := payload["input"].(string)

	timeout := 30
	if v, ok := payload["timeout"].(float64); ok {
		timeout = int(v)
	}

	// Use a placeholder task ID - in real implementation, this should come from the task message
	taskID := "task-id"
	if v, ok := payload["task_id"].(string); ok {
		taskID = v
	}

	resp, err := r.langClient.ExecuteInlineCode(ctx, taskID, runtime, code, filename, inputJSON, timeout)
	if err != nil {
		return nil, fmt.Errorf("language executor failed: %w", err)
	}

	if !resp.Success {
		return nil, fmt.Errorf("language executor error: %s", resp.Error)
	}

	return json.RawMessage(resp.OutputJson), nil
}

// isLanguageRuntime checks if the node type is a language runtime
func isLanguageRuntime(nodeType string) bool {
	langRuntimes := map[string]bool{
		"python":     true,
		"js":         true,
		"node":       true,
		"javascript": true,
	}
	return langRuntimes[nodeType]
}
