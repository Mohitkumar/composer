package model

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// WorkflowDefinitionSpec represents the workflow definition structure (the spec JSON)
// This is what gets stored in the spec_json column
// Note: workflow_id and name are stored in the workflow_definitions table, not in the spec
type WorkflowDefinitionSpec struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Node represents a node in the workflow DAG
type Node struct {
	NodeID  string          `json:"node_id"`
	Name    string          `json:"name"`
	Type    NodeType        `json:"type"`              // CODE, HTTP, SWITCH, FORK, JOIN, etc
	Runtime *string         `json:"runtime,omitempty"` // Optional: python/js/node (only for CODE type nodes)
	Spec    json.RawMessage `json:"spec"`              // task implementation definition (http spec, code reference etc)
	Retry   *RetryPolicy    `json:"retry,omitempty"`   // Optional: retry failed executions of this node
	// TaskQueue optionally routes the node to a dedicated worker pool. By
	// default a node goes to the queue of its runtime ("http", "python", "js").
	TaskQueue string `json:"queue,omitempty"`
}

// Queue returns the task queue a worker node is dispatched on.
func (n *Node) Queue() string {
	if n.TaskQueue != "" {
		return strings.ToLower(n.TaskQueue)
	}
	key := strings.ToLower(n.GetNodeTypeForRouting())
	switch key {
	case "py":
		return "python"
	case "javascript", "node":
		return "js"
	}
	return key
}

// RetryPolicy controls how often a failed worker node is re-executed.
type RetryPolicy struct {
	// MaxAttempts is the total number of executions, including the first.
	MaxAttempts int `json:"max_attempts"`
	// BackoffSeconds is the base delay; attempt n waits BackoffSeconds * 2^(n-1).
	BackoffSeconds int `json:"backoff_seconds,omitempty"`
}

const maxRetryBackoff = 10 * time.Minute

// MaxAttempts returns how many times the node may execute (at least 1).
func (n *Node) MaxAttempts() int {
	if n.Retry == nil || n.Retry.MaxAttempts < 1 {
		return 1
	}
	return n.Retry.MaxAttempts
}

// RetryDelay returns the backoff before the given retry attempt (1-based).
func (n *Node) RetryDelay(attempt int) time.Duration {
	if n.Retry == nil || n.Retry.BackoffSeconds <= 0 || attempt < 1 {
		return 0
	}
	delay := time.Duration(n.Retry.BackoffSeconds) * time.Second << (attempt - 1)
	if delay <= 0 || delay > maxRetryBackoff {
		return maxRetryBackoff
	}
	return delay
}

// GetNodeTypeForRouting returns the node type string to use for routing to executors
// For CODE nodes, returns the runtime (python/js/node)
// For other nodes, returns the type string (HTTP/SWITCH/etc)
func (n *Node) GetNodeTypeForRouting() string {
	if n.Type == NodeTypeCode {
		// For CODE nodes, use runtime
		if n.Runtime != nil && *n.Runtime != "" {
			return *n.Runtime
		}
		// Fallback to "python" if runtime not specified
		return "python"
	}
	return string(n.Type)
}

// Edge represents an edge in the workflow DAG
type Edge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Condition string `json:"condition,omitempty"` // CEL expression for conditional edges (used in SWITCH nodes)
}

// WorkflowDefinitionRecord represents a workflow definition record in the database
type WorkflowDefinitionRecord struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Version    int             `json:"version"`
	Spec       json.RawMessage `json:"spec"` // Contains WorkflowDefinitionSpec as JSON
	CreatedBy  string          `json:"created_by,omitempty"`
	Enabled    bool            `json:"enabled"`
	WebhookURL sql.NullString  `json:"webhook_url,omitempty"`
	Deployed   bool            `json:"deployed"`
	DeployedAt sql.NullTime    `json:"deployed_at,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// RegisterWorkflowRequest represents a request to register a new workflow definition
type RegisterWorkflowRequest struct {
	WorkflowID string          `json:"workflow_id"`
	Name       string          `json:"name"`
	Version    int             `json:"version"`
	Spec       json.RawMessage `json:"spec"` // Should contain WorkflowDefinitionSpec as JSON
	CreatedBy  string          `json:"created_by,omitempty"`
}
