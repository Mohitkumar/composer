package statemachine

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mohitkumar/composer/pkg/model"
)

var (
	// ErrInvalidRequest: the event cannot apply to this run or definition
	// (unknown node, missing signal name, unresolvable start node, …).
	ErrInvalidRequest = errors.New("invalid request")
	// ErrRunFinished: the run is already terminal and cannot take the event.
	ErrRunFinished = errors.New("workflow run is not active")
	// ErrRunActive: a retry targets a run that still has tasks in flight.
	ErrRunActive = errors.New("workflow run still has active tasks")
)

// StoreError wraps a failure of the Store. Unlike the machine's own errors it
// is transient: the caller should retry the event rather than give up.
type StoreError struct{ Err error }

func (e *StoreError) Error() string { return "statemachine store: " + e.Err.Error() }
func (e *StoreError) Unwrap() error { return e.Err }

// TemplateResolver evaluates the CEL templates in node specs and conditions.
type TemplateResolver interface {
	ResolveTemplates(spec any, ctx *model.RunContext) (any, error)
}

// Store gives the machine read access to run data it only loads on demand.
type Store interface {
	// Outputs returns the outputs of the given completed nodes.
	Outputs(nodeIDs []string) (map[string]json.RawMessage, error)
	// PendingSignal returns the oldest buffered signal with that name, if any.
	PendingSignal(name string) (*Signal, error)
}

// Deps are the machine's collaborators.
type Deps struct {
	Templates TemplateResolver
	Store     Store
	Now       func() time.Time
	NewID     func() string
}

// Machine decides how one event moves one run forward. Create a Machine per
// event; it is not safe for concurrent use, and the caller must serialize
// events per run (the coordinator holds the run's row lock).
type Machine struct {
	def  *model.WorkflowDAG
	run  *Run
	deps Deps

	tr     *Transition
	now    time.Time
	loaded map[string]bool // nodes whose output is in run.Context.Tasks
}

// New returns a machine for run, governed by its workflow definition.
func New(def *model.WorkflowDAG, run *Run, deps Deps) *Machine {
	if run.Context.Tasks == nil {
		run.Context.Tasks = map[string]*model.StepResult{}
	}
	loaded := make(map[string]bool, len(run.Context.Tasks))
	for id := range run.Context.Tasks {
		loaded[id] = true
	}
	return &Machine{def: def, run: run, deps: deps, loaded: loaded}
}

// Apply consumes one event and returns the resulting transition. Errors leave
// nothing to persist: ErrInvalidRequest, ErrRunFinished and ErrRunActive
// reject the event; a *StoreError is transient.
func (m *Machine) Apply(ev Event) (*Transition, error) {
	m.tr, m.now = &Transition{}, m.deps.Now()

	var err error
	switch ev := ev.(type) {
	case RunStarted:
		err = m.onRunStarted(ev)
	case NodeCompleted:
		err = m.onNodeCompleted(ev)
	case NodeFailed:
		err = m.onNodeFailed(ev)
	case TimerFired:
		err = m.onTimerFired(ev)
	case SignalReceived:
		err = m.onSignalReceived(ev)
	case RunRetried:
		err = m.onRunRetried(ev)
	case RunCancelled:
		err = m.onRunCancelled(ev)
	default:
		err = fmt.Errorf("%w: unknown event %T", ErrInvalidRequest, ev)
	}
	if err != nil {
		return nil, err
	}
	if m.tr.Ignored == "" {
		m.finishIfDone()
	}
	return m.tr, nil
}

// emit records an action and applies it to the snapshot.
func (m *Machine) emit(a Action) {
	m.tr.Actions = append(m.tr.Actions, a)
	a.apply(m.run)
}

func (m *Machine) ignore(format string, args ...any) error {
	m.tr.Ignored = fmt.Sprintf(format, args...)
	return nil
}

func (m *Machine) terminal() bool { return m.run.Status.IsTerminal() }

// ── Event handlers ───────────────────────────────────────────────────────────

func (m *Machine) onRunStarted(ev RunStarted) error {
	start, err := m.def.StartNode(ev.StartNodeID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return m.begin(start)
}

func (m *Machine) onNodeCompleted(ev NodeCompleted) error {
	n := m.run.Nodes[ev.NodeID]
	if !n.matches(ev.TaskID, ev.Attempt) || !n.isTaskActive() {
		return m.ignore("stale or duplicate completion of %s attempt %d", ev.NodeID, ev.Attempt)
	}
	m.emit(CompleteTask{TaskID: ev.TaskID, NodeID: ev.NodeID, Attempt: ev.Attempt, Output: ev.Output})
	if m.terminal() {
		return nil // e.g. cancelled while running: keep the output, schedule nothing
	}
	return m.advanceFrom(ev.NodeID, ev.Output)
}

func (m *Machine) onNodeFailed(ev NodeFailed) error {
	n := m.run.Nodes[ev.NodeID]
	if !n.matches(ev.TaskID, ev.Attempt) || !n.isTaskActive() {
		return m.ignore("stale or duplicate failure of %s attempt %d", ev.NodeID, ev.Attempt)
	}
	if node := m.def.GetNode(ev.NodeID); node != nil && !m.terminal() && ev.Attempt+1 < node.MaxAttempts() {
		m.emit(RetryTask{TaskID: ev.TaskID, NodeID: ev.NodeID, Attempt: ev.Attempt, Reason: ev.Error,
			NotBefore: m.now.Add(node.RetryDelay(ev.Attempt + 1))})
		return nil
	}
	m.emit(FailTask{TaskID: ev.TaskID, NodeID: ev.NodeID, Attempt: ev.Attempt, Reason: ev.Error})
	return nil
}

func (m *Machine) onTimerFired(ev TimerFired) error {
	n := m.run.Nodes[ev.NodeID]
	if n == nil || n.TaskID != ev.TaskID || n.Status != model.TaskInstanceStatusWaiting || m.terminal() {
		return m.ignore("timer for %s no longer pending", ev.NodeID)
	}
	if node := m.def.GetNode(ev.NodeID); node != nil && node.Type == model.NodeTypeWaitForEvent {
		m.emit(ResolveWait{TaskID: ev.TaskID, NodeID: ev.NodeID, Status: model.TaskInstanceStatusFailed,
			Reason: fmt.Sprintf("timed out waiting for event %q", n.AwaitedSignal)})
		return nil
	}
	output := wokeAt(m.now)
	m.emit(ResolveWait{TaskID: ev.TaskID, NodeID: ev.NodeID, Status: model.TaskInstanceStatusCompleted, Output: output})
	return m.advanceFrom(ev.NodeID, output)
}

func (m *Machine) onSignalReceived(ev SignalReceived) error {
	if ev.Name == "" {
		return fmt.Errorf("%w: event name is required", ErrInvalidRequest)
	}
	if len(ev.Payload) > 0 && !json.Valid(ev.Payload) {
		return fmt.Errorf("%w: payload must be valid JSON", ErrInvalidRequest)
	}
	if m.terminal() {
		return fmt.Errorf("%w: run is %s", ErrRunFinished, m.run.Status)
	}

	nodeID, n := m.waiterFor(ev.Name)
	if n == nil {
		m.emit(BufferSignal{Name: ev.Name, Payload: ev.Payload})
		return nil
	}
	output := signalOutput(ev.Payload)
	m.emit(ResolveWait{TaskID: n.TaskID, NodeID: nodeID, Status: model.TaskInstanceStatusCompleted, Output: output})
	m.tr.SignalDelivered = true
	return m.advanceFrom(nodeID, output)
}

// waiterFor returns the oldest node waiting for the signal (task IDs are time-ordered).
func (m *Machine) waiterFor(name string) (string, *NodeState) {
	var bestID string
	var best *NodeState
	for id, n := range m.run.Nodes {
		if n.Status == model.TaskInstanceStatusWaiting && n.AwaitedSignal == name &&
			(best == nil || n.TaskID < best.TaskID) {
			bestID, best = id, n
		}
	}
	return bestID, best
}

func (m *Machine) onRunRetried(ev RunRetried) error {
	if active, _ := m.run.counts(); active > 0 {
		return fmt.Errorf("%w: %d task(s) in flight; cancel the run before retrying", ErrRunActive, active)
	}
	restart := m.def.GetNode(ev.FromNodeID)
	if restart == nil {
		return fmt.Errorf("%w: node %s not in workflow definition", ErrInvalidRequest, ev.FromNodeID)
	}

	ctx := m.run.Context.Persisted()
	if len(ev.Input) > 0 {
		ctx.Input = ev.Input
	}
	if len(ev.Config) > 0 {
		ctx.Config = ev.Config
	}
	reset := append(m.def.Descendants(ev.FromNodeID), ev.FromNodeID)
	for _, id := range reset {
		delete(m.loaded, id)
	}
	m.emit(ReopenRun{Version: ev.Version, Context: ctx, ResetNodes: reset})
	return m.begin(restart)
}

func (m *Machine) onRunCancelled(ev RunCancelled) error {
	if m.terminal() {
		return fmt.Errorf("%w: run is %s", ErrRunFinished, m.run.Status)
	}
	reason := ev.Reason
	if reason == "" {
		reason = "cancelled"
	}
	m.emit(CancelTasks{Reason: reason})
	m.emit(FinishRun{Status: model.WorkflowInstanceStatusFailed, Reason: reason})
	return nil
}

// finishIfDone finishes the run once no task is active: FAILED if any failed,
// SUCCEEDED otherwise.
func (m *Machine) finishIfDone() {
	if m.terminal() {
		return
	}
	active, failed := m.run.counts()
	switch {
	case active > 0:
	case failed > 0:
		m.emit(FinishRun{Status: model.WorkflowInstanceStatusFailed,
			Reason: fmt.Sprintf("workflow has %d failed task(s)", failed)})
	default:
		m.emit(FinishRun{Status: model.WorkflowInstanceStatusSucceeded})
	}
}

// ── Advancing ────────────────────────────────────────────────────────────────

// begin activates node unconditionally — the start node, or the node a retry
// restarts from — and everything that becomes ready as a result.
func (m *Machine) begin(node *model.Node) error {
	next, err := m.activate(node)
	if err == nil {
		err = m.propagate(next)
	}
	if err != nil && !isStoreError(err) {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return err
}

// advanceFrom routes past a node that completed with output. Routing errors
// are permanent (a bad SWITCH condition, a node missing from the definition),
// so they fail the run instead of being returned for endless retries.
func (m *Machine) advanceFrom(nodeID string, output json.RawMessage) error {
	m.setOutput(nodeID, output)
	if m.def.GetNode(nodeID) == nil {
		return m.failRun(fmt.Sprintf("node %s not found in workflow definition", nodeID))
	}
	if err := m.propagate(m.def.Successors(nodeID)); err != nil {
		if isStoreError(err) {
			return err
		}
		return m.failRun(fmt.Sprintf("routing after node %s failed: %v", nodeID, err))
	}
	return nil
}

func (m *Machine) failRun(reason string) error {
	m.emit(FinishRun{Status: model.WorkflowInstanceStatusFailed, Reason: reason})
	return nil
}

func isStoreError(err error) bool {
	var se *StoreError
	return errors.As(err, &se)
}

// setOutput caches a node's output for templates that reference it.
func (m *Machine) setOutput(nodeID string, output json.RawMessage) {
	m.run.Context.Tasks[nodeID] = &model.StepResult{Status: model.TaskInstanceStatusCompleted, Output: output}
	m.loaded[nodeID] = true
}

// ensureOutputs loads the outputs node's templates may read, if not loaded yet.
func (m *Machine) ensureOutputs(node *model.Node) error {
	refs, ok := m.def.TaskRefs(node.NodeID)
	if !ok {
		refs = make([]string, 0, len(m.run.Nodes))
		for id := range m.run.Nodes {
			refs = append(refs, id)
		}
	}
	var missing []string
	for _, id := range refs {
		n := m.run.Nodes[id]
		if !m.loaded[id] && n != nil && n.Status.IsCompleted() && !slices.Contains(missing, id) {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	outputs, err := m.deps.Store.Outputs(missing)
	if err != nil {
		return &StoreError{Err: err}
	}
	for _, id := range missing {
		m.setOutput(id, outputs[id])
	}
	return nil
}
