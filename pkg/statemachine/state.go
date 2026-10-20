package statemachine

import (
	"encoding/json"

	"github.com/mohitkumar/composer/pkg/model"
)

// Run is the snapshot of one workflow run that the machine decides on. The
// machine updates it as it emits actions, so after Apply it reflects the
// state the persisted actions produce.
type Run struct {
	ID              string
	WorkflowID      string
	WorkflowVersion int
	Status          model.WorkflowInstanceStatus
	// Context holds the run's input and execution options. Its Tasks map is a
	// cache of outputs loaded on demand through the Store.
	Context *model.RunContext
	// Nodes holds every node that has been reached, i.e. that has a task.
	Nodes map[string]*NodeState
}

// NodeState is the state of a reached node: its task's current status.
type NodeState struct {
	TaskID  string
	Status  model.TaskInstanceStatus
	Attempt int
	// Branches lists the successors a completed SWITCH selected.
	Branches []string
	// AwaitedSignal is the signal a WAITING WAIT_FOR_EVENT node waits for.
	AwaitedSignal string
}

// Signal is an external event buffered for a run before any node waited for it.
type Signal struct {
	ID      string
	Payload json.RawMessage
}

// NewRun returns the snapshot of a run that has just been created.
func NewRun(id, workflowID string, version int, ctx *model.RunContext) *Run {
	return &Run{
		ID:              id,
		WorkflowID:      workflowID,
		WorkflowVersion: version,
		Status:          model.WorkflowInstanceStatusRunning,
		Context:         ctx,
		Nodes:           map[string]*NodeState{},
	}
}

// isTaskActive: the node's task is with (or on its way to) a worker.
func (n *NodeState) isTaskActive() bool {
	switch n.Status {
	case model.TaskInstanceStatusPending, model.TaskInstanceStatusQueued, model.TaskInstanceStatusRunning:
		return true
	}
	return false
}

// isActive: the node still has to finish before the run can.
func (n *NodeState) isActive() bool {
	return n.isTaskActive() || n.Status == model.TaskInstanceStatusWaiting
}

// matches reports whether a result is for this node's current task attempt.
func (n *NodeState) matches(taskID string, attempt int) bool {
	return n != nil && n.TaskID == taskID && n.Attempt == attempt
}

func (r *Run) counts() (active, failed int) {
	for _, n := range r.Nodes {
		switch {
		case n.isActive():
			active++
		case n.Status == model.TaskInstanceStatusFailed:
			failed++
		}
	}
	return active, failed
}
