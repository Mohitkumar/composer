package model

// WorkflowInstanceStatus represents the status of a workflow instance
type WorkflowInstanceStatus string

const (
	WorkflowInstanceStatusPending   WorkflowInstanceStatus = "PENDING"
	WorkflowInstanceStatusRunning   WorkflowInstanceStatus = "RUNNING"
	WorkflowInstanceStatusSucceeded WorkflowInstanceStatus = "SUCCEEDED"
	WorkflowInstanceStatusCompleted WorkflowInstanceStatus = "COMPLETED"
	WorkflowInstanceStatusFailed    WorkflowInstanceStatus = "FAILED"
)

// TaskInstanceStatus represents the status of a task instance
type TaskInstanceStatus string

const (
	TaskInstanceStatusPending   TaskInstanceStatus = "PENDING"
	TaskInstanceStatusQueued    TaskInstanceStatus = "QUEUED"
	TaskInstanceStatusRunning   TaskInstanceStatus = "RUNNING"
	TaskInstanceStatusSucceeded TaskInstanceStatus = "SUCCEEDED"
	TaskInstanceStatusCompleted TaskInstanceStatus = "COMPLETED"
	TaskInstanceStatusFailed    TaskInstanceStatus = "FAILED"
)

// NodeType represents the type of a workflow node
type NodeType string

const (
	// CODE - Execute code in a language runtime (requires runtime field)
	NodeTypeCode NodeType = "CODE"
	// HTTP - Execute HTTP request
	NodeTypeHTTP NodeType = "HTTP"
	// SWITCH - Conditional branching based on conditions
	NodeTypeSwitch NodeType = "SWITCH"
	// FORK - Fork execution into parallel branches
	NodeTypeFork NodeType = "FORK"
	// JOIN - Join parallel branches (handled automatically)
	NodeTypeJoin NodeType = "JOIN"
	// WAIT - Wait for external event
	NodeTypeWait NodeType = "WAIT"
)
