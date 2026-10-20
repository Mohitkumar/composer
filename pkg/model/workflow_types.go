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

// IsTerminal reports whether no further nodes should be scheduled for the run.
func (s WorkflowInstanceStatus) IsTerminal() bool {
	switch s {
	case WorkflowInstanceStatusSucceeded, WorkflowInstanceStatusCompleted, WorkflowInstanceStatusFailed:
		return true
	}
	return false
}

// TaskInstanceStatus represents the status of a task instance
type TaskInstanceStatus string

const (
	TaskInstanceStatusPending   TaskInstanceStatus = "PENDING"
	TaskInstanceStatusQueued    TaskInstanceStatus = "QUEUED"
	TaskInstanceStatusRunning   TaskInstanceStatus = "RUNNING"
	TaskInstanceStatusSucceeded TaskInstanceStatus = "SUCCEEDED"
	TaskInstanceStatusCompleted TaskInstanceStatus = "COMPLETED"
	TaskInstanceStatusFailed    TaskInstanceStatus = "FAILED"
	// TaskInstanceStatusSkipped marks a node on a branch that was not taken
	// (an unselected SWITCH edge, or downstream of one).
	TaskInstanceStatusSkipped TaskInstanceStatus = "SKIPPED"
)

// IsCompleted reports whether the task finished successfully.
func (s TaskInstanceStatus) IsCompleted() bool {
	return s == TaskInstanceStatusCompleted || s == TaskInstanceStatusSucceeded
}

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
	// JOIN - Explicit join point; any node with several incoming edges already
	// waits for all of them, so JOIN is a no-op pass-through.
	NodeTypeJoin NodeType = "JOIN"
	// WAIT - Wait for external event
	NodeTypeWait NodeType = "WAIT"
)

// IsControlFlow reports whether the coordinator resolves this node inline
// instead of dispatching it to a worker.
func (t NodeType) IsControlFlow() bool {
	return t == NodeTypeSwitch || t == NodeTypeFork || t == NodeTypeJoin
}
