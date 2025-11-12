package model

import "time"

type WorkflowNodeState string

const (
	NodePending   WorkflowNodeState = "PENDING"
	NodeRunning   WorkflowNodeState = "RUNNING"
	NodeCompleted WorkflowNodeState = "COMPLETED"
	NodeFailed    WorkflowNodeState = "FAILED"
)

type WorkflowInstanceNode struct {
	ID                 string            `db:"id"` // uuid per instance-node
	WorkflowInstanceID string            `db:"workflow_instance_id"`
	NodeID             string            `db:"node_id"` // reference to definition node
	State              WorkflowNodeState `db:"state"`
	OutputPayload      []byte            `db:"output_payload"` // raw json/from worker
	ErrorMessage       string            `db:"error_message"`
	RetryCount         int               `db:"retry_count"`
	CreatedAt          time.Time         `db:"created_at"`
	UpdatedAt          time.Time         `db:"updated_at"`
}
