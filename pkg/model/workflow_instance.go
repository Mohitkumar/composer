package model

import (
	"time"
)

type WorkflowInstanceStatus string

const (
	WorkflowInstancePending   WorkflowInstanceStatus = "PENDING"
	WorkflowInstanceRunning   WorkflowInstanceStatus = "RUNNING"
	WorkflowInstanceCompleted WorkflowInstanceStatus = "COMPLETED"
	WorkflowInstanceFailed    WorkflowInstanceStatus = "FAILED"
)

type WorkflowInstance struct {
	ID                   string                 `db:"id"`
	WorkflowDefinitionID string                 `db:"workflow_definition_id"`
	Status               WorkflowInstanceStatus `db:"status"`
	CreatedAt            time.Time              `db:"created_at"`
	UpdatedAt            time.Time              `db:"updated_at"`
}
