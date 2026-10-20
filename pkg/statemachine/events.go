package statemachine

import "encoding/json"

// Event is something that happened to a workflow run. Machine.Apply consumes
// one event at a time.
type Event interface{ isEvent() }

// RunStarted: a new run was created. StartNodeID optionally overrides the
// definition's entry node.
type RunStarted struct {
	StartNodeID string
}

// NodeCompleted: a worker finished a task attempt successfully.
type NodeCompleted struct {
	TaskID  string
	NodeID  string
	Attempt int
	Output  json.RawMessage
}

// NodeFailed: a worker finished a task attempt with an error (or gave up on
// it, e.g. after too many deliveries).
type NodeFailed struct {
	TaskID  string
	NodeID  string
	Attempt int
	Error   string
}

// TimerFired: a waiting node's wake-up time passed — a WAIT node's timer, or
// a WAIT_FOR_EVENT node's timeout.
type TimerFired struct {
	TaskID string
	NodeID string
}

// SignalReceived: an external event (signal) was sent to the run.
type SignalReceived struct {
	Name    string
	Payload json.RawMessage
}

// RunRetried: the run is restarted from a node, on the definition version the
// machine was built with, optionally with new input or execution options.
type RunRetried struct {
	FromNodeID string
	Version    int
	Input      json.RawMessage
	Config     json.RawMessage
}

// RunCancelled: the run was cancelled by a user.
type RunCancelled struct {
	Reason string
}

func (RunStarted) isEvent()     {}
func (NodeCompleted) isEvent()  {}
func (NodeFailed) isEvent()     {}
func (TimerFired) isEvent()     {}
func (SignalReceived) isEvent() {}
func (RunRetried) isEvent()     {}
func (RunCancelled) isEvent()   {}
