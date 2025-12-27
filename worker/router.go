package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mohitkumar/composer/worker/executors"
	"github.com/rs/zerolog"
)

// Helper function to get map keys for logging
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// Helper function to truncate string for logging
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// Router routes tasks to appropriate executors
type Router struct {
	executors     map[string]executors.Executor
	langClientMgr *LangClientManager // Multi-client manager for language runtimes
	logger        zerolog.Logger
}

// NewRouter creates a new task router
func NewRouter(langClientMgr *LangClientManager, logger zerolog.Logger) *Router {
	r := &Router{
		executors:     make(map[string]executors.Executor),
		langClientMgr: langClientMgr,
		logger:        logger,
	}

	// Register built-in executors
	r.RegisterExecutor(executors.NewHTTPExecutor())

	return r
}

// RegisterExecutor registers an executor for a specific node type
func (r *Router) RegisterExecutor(exec executors.Executor) {
	r.executors[exec.Type()] = exec
}

// Route routes a task to the appropriate executor based on node type
// nodeType can be:
// - For CODE nodes: runtime (python, js, node) - routes to language executor
// - For other nodes: type (HTTP) - routes to built-in executor
// - Control flow nodes (SWITCH, PARALLEL/FORK, JOIN) are handled by coordinator, not worker
func (r *Router) Route(ctx context.Context, nodeType string, input json.RawMessage) (json.RawMessage, error) {
	logger := r.logger.With().Str("node_type", nodeType).Logger()

	// Check if it's a language runtime (python, js, node, etc.)
	// These come from CODE type nodes where runtime is specified
	if isLanguageRuntime(nodeType) {
		if r.langClientMgr == nil {
			return nil, fmt.Errorf("language client manager not configured")
		}
		langClient, err := r.langClientMgr.GetClientForRuntime(nodeType)
		if err != nil {
			return nil, fmt.Errorf("language client not configured for runtime: %s: %w", nodeType, err)
		}
		return r.executeLanguageTaskWithClient(ctx, nodeType, input, langClient, logger)
	}

	// Check for built-in executor (HTTP, etc.)
	exec, exists := r.executors[nodeType]
	if !exists {
		return nil, fmt.Errorf("no executor found for node type: %s", nodeType)
	}

	logger.Debug().Msg("routing to built-in executor")
	return exec.Execute(ctx, input)
}

// executeLanguageTaskWithClient executes a task using a language worker via gRPC with a specific client
func (r *Router) executeLanguageTaskWithClient(ctx context.Context, runtime string, input json.RawMessage, langClient *LangClient, logger zerolog.Logger) (json.RawMessage, error) {
	logger.Debug().Str("runtime", runtime).Msg("routing to language executor")

	// Parse input to extract code and other parameters
	var payload map[string]interface{}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse language task input: %w", err)
	}

	code, codeOk := payload["code"].(string)
	filename, _ := payload["filename"].(string)

	// Log code extraction for debugging
	if !codeOk || code == "" {
		logger.Error().
			Interface("payload_keys", getMapKeys(payload)).
			Bool("code_ok", codeOk).
			Int("code_length", len(code)).
			Msg("code field missing or empty in payload")
		return nil, fmt.Errorf("code field is required and must be a non-empty string")
	}

	logger.Debug().
		Int("code_length", len(code)).
		Str("code_preview", truncateString(code, 200)).
		Str("filename", filename).
		Msg("extracted code for language executor")

	// Extract input - it can be a string (JSON) or an object (after template resolution)
	var inputJSON string
	if inputVal, ok := payload["input"]; ok {
		if inputStr, ok := inputVal.(string); ok {
			// Already a string (JSON)
			inputJSON = inputStr
		} else {
			// It's an object/map after template resolution, marshal it to JSON
			inputBytes, err := json.Marshal(inputVal)
			if err != nil {
				logger.Error().Err(err).Msg("failed to marshal resolved input to JSON")
				return nil, fmt.Errorf("failed to marshal input to JSON: %w", err)
			}
			inputJSON = string(inputBytes)
		}
	} else {
		// No input field, use empty object
		inputJSON = "{}"
	}

	logger.Debug().
		Str("input_json", inputJSON).
		Int("input_length", len(inputJSON)).
		Msg("extracted input for language executor")

	timeout := 30
	if v, ok := payload["timeout"].(float64); ok {
		timeout = int(v)
	}

	// Use a placeholder task ID - in real implementation, this should come from the task message
	taskID := "task-id"
	if v, ok := payload["task_id"].(string); ok {
		taskID = v
	}

	resp, err := langClient.ExecuteInlineCode(ctx, taskID, runtime, code, filename, inputJSON, timeout)
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
