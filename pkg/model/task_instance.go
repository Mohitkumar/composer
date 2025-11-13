package model

import (
	"encoding/json"
	"time"
)

// TaskInstanceRecord represents a task instance record in the database
type TaskInstanceRecord struct {
	ID             string             `json:"id"`
	RunID          string             `json:"run_id"`
	NodeID         string             `json:"node_id"`
	NodeType       string             `json:"node_type"` // Runtime type: http, python, js, etc.
	Status         TaskInstanceStatus `json:"status"`
	Attempt        int                `json:"attempt"`
	Input          json.RawMessage    `json:"input,omitempty"`
	Output         json.RawMessage    `json:"output,omitempty"`
	LastError      json.RawMessage    `json:"last_error,omitempty"`
	ScheduledAt    *time.Time         `json:"scheduled_at,omitempty"`
	StartedAt      *time.Time         `json:"started_at,omitempty"`
	FinishedAt     *time.Time         `json:"finished_at,omitempty"`
	CompletedAt    *time.Time         `json:"completed_at,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
	LeaseOwner     *string            `json:"lease_owner,omitempty"`
	LeaseExpiresAt *time.Time         `json:"lease_expires_at,omitempty"`
}

// NodeCompletedRequest represents a request to mark a node/task as completed
type NodeCompletedRequest struct {
	RunID         string          `json:"run_id"`
	TaskInstance  string          `json:"task_instance_id"`
	NodeID        string          `json:"node_id"`
	Attempt       int32           `json:"attempt"`
	Output        json.RawMessage `json:"output,omitempty"`
	Error         *string         `json:"error,omitempty"`
	CompletedAtMs int64           `json:"completed_at_unix_ms,omitempty"`
	// Optional children to schedule atomically
	Children []TaskChildRequest `json:"children,omitempty"`
}

// TaskChildRequest represents a child task to be scheduled
type TaskChildRequest struct {
	TaskInstanceID string          `json:"task_instance_id"`
	NodeID         string          `json:"node_id"`
	Input          json.RawMessage `json:"input"`
	ScheduledAtMs  int64           `json:"scheduled_at_unix_ms,omitempty"`
}
