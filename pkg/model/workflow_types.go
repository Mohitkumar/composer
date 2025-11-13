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
	NodeTypeTask     NodeType = "TASK"
	NodeTypeSwitch   NodeType = "SWITCH"
	NodeTypeParallel NodeType = "PARALLEL"
	NodeTypeWait     NodeType = "WAIT"
)
