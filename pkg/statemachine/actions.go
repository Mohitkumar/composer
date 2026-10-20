package statemachine

import (
	"encoding/json"
	"time"

	"github.com/mohitkumar/composer/pkg/model"
)

// Action is a state change the machine decided on. The shell persists actions
// in order; apply evolves the in-memory snapshot the same way.
type Action interface{ apply(*Run) }

// Transition is the outcome of applying one event.
type Transition struct {
	Actions []Action
	// Ignored explains why the event changed nothing, e.g. a duplicate or a
	// stale report from a superseded task attempt.
	Ignored string
	// SignalDelivered reports, for SignalReceived, whether a waiting node
	// consumed the signal (false: it was buffered).
	SignalDelivered bool
}

// ── Creating tasks ───────────────────────────────────────────────────────────

// CreateTask records a reached node as a task in the given state: PENDING
// (followed by DispatchTask), WAITING, or already resolved (COMPLETED for
// control nodes, SKIPPED, FAILED when its input cannot be resolved).
type CreateTask struct {
	TaskID        string
	Node          *model.Node
	Status        model.TaskInstanceStatus
	Input         json.RawMessage
	Output        json.RawMessage
	Error         string
	Branches      []string   // SWITCH decision
	WakeAt        *time.Time // WAITING: timer or timeout
	AwaitedSignal string     // WAITING on WAIT_FOR_EVENT
}

// DispatchTask sends a PENDING task attempt to a worker, no earlier than NotBefore.
type DispatchTask struct {
	TaskID    string
	Node      *model.Node
	Attempt   int
	Input     json.RawMessage
	NotBefore time.Time
}

// ── Resolving tasks ──────────────────────────────────────────────────────────

// CompleteTask: an active task attempt finished successfully.
type CompleteTask struct {
	TaskID  string
	NodeID  string
	Attempt int
	Output  json.RawMessage
}

// FailTask: an active task attempt failed for good.
type FailTask struct {
	TaskID  string
	NodeID  string
	Attempt int
	Reason  string
}

// RetryTask: a failed attempt is retried; the task returns to PENDING with
// Attempt+1 and is re-dispatched (with its stored input) at NotBefore.
type RetryTask struct {
	TaskID    string
	NodeID    string
	Attempt   int // the attempt that failed
	Reason    string
	NotBefore time.Time
}

// ResolveWait: a WAITING node completed (timer fired, signal arrived) or
// failed (event timeout).
type ResolveWait struct {
	TaskID string
	NodeID string
	Status model.TaskInstanceStatus
	Output json.RawMessage
	Reason string
}

// ── Signals ──────────────────────────────────────────────────────────────────

// BufferSignal keeps a signal no node is waiting for yet.
type BufferSignal struct {
	Name    string
	Payload json.RawMessage
}

// ConsumeSignal marks a buffered signal as used by a WAIT_FOR_EVENT node.
type ConsumeSignal struct {
	SignalID string
}

// ── Run ──────────────────────────────────────────────────────────────────────

// ReopenRun puts a finished run back to RUNNING for a retry: the reset nodes'
// tasks are deleted and the run moves to Version with the given context.
type ReopenRun struct {
	Version    int
	Context    *model.RunContext
	ResetNodes []string
}

// CancelTasks fails every task that is still active.
type CancelTasks struct {
	Reason string
}

// FinishRun moves the run to a terminal status.
type FinishRun struct {
	Status model.WorkflowInstanceStatus
	Reason string
}

// ── Evolving the snapshot ────────────────────────────────────────────────────

func (a CreateTask) apply(r *Run) {
	r.Nodes[a.Node.NodeID] = &NodeState{
		TaskID: a.TaskID, Status: a.Status, Branches: a.Branches, AwaitedSignal: a.AwaitedSignal,
	}
}

func (DispatchTask) apply(*Run) {}

func (a CompleteTask) apply(r *Run) { r.Nodes[a.NodeID].Status = model.TaskInstanceStatusCompleted }

func (a FailTask) apply(r *Run) { r.Nodes[a.NodeID].Status = model.TaskInstanceStatusFailed }

func (a RetryTask) apply(r *Run) {
	n := r.Nodes[a.NodeID]
	n.Status, n.Attempt = model.TaskInstanceStatusPending, a.Attempt+1
}

func (a ResolveWait) apply(r *Run) {
	n := r.Nodes[a.NodeID]
	n.Status, n.AwaitedSignal = a.Status, ""
}

func (BufferSignal) apply(*Run) {}

func (ConsumeSignal) apply(*Run) {}

func (a ReopenRun) apply(r *Run) {
	tasks := r.Context.Tasks
	for _, id := range a.ResetNodes {
		delete(r.Nodes, id)
		delete(tasks, id)
	}
	r.Context = a.Context
	r.Context.Tasks = tasks
	r.WorkflowVersion = a.Version
	r.Status = model.WorkflowInstanceStatusRunning
}

func (CancelTasks) apply(r *Run) {
	for _, n := range r.Nodes {
		if n.isActive() {
			n.Status = model.TaskInstanceStatusFailed
		}
	}
}

func (a FinishRun) apply(r *Run) { r.Status = a.Status }
