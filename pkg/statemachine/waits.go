package statemachine

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/mohitkumar/composer/pkg/model"
)

// WAIT and WAIT_FOR_EVENT nodes never reach a worker: they park in WAITING
// and are resolved by TimerFired or SignalReceived.

// waitSpec is a wait node's resolved spec. Numbers may also be given as
// strings, so templated values like "{{input.delay}}" work.
type waitSpec struct {
	DurationSeconds json.RawMessage `json:"duration_seconds"`
	Until           string          `json:"until"` // RFC 3339
	Event           string          `json:"event"`
	TimeoutSeconds  json.RawMessage `json:"timeout_seconds"`
}

// park records a wait node as WAITING. A wait that is already satisfied — a
// zero duration, or a buffered signal — completes now and returns the node's
// successors so routing continues past it.
func (m *Machine) park(n *model.Node) ([]*model.Node, error) {
	input, err := m.resolveInput(n)
	if err != nil {
		if isStoreError(err) {
			return nil, err
		}
		return nil, m.failNode(n, nil, err.Error())
	}
	var spec waitSpec
	if err := json.Unmarshal(input, &spec); err != nil {
		return nil, m.failNode(n, input, fmt.Sprintf("invalid wait spec: %v", err))
	}

	task := CreateTask{TaskID: m.deps.NewID(), Node: n, Input: input, Status: model.TaskInstanceStatusWaiting}
	switch n.Type {
	case model.NodeTypeWait:
		wakeAt, err := spec.wakeTime(m.now)
		if err != nil {
			return nil, m.failNode(n, input, err.Error())
		}
		if !wakeAt.After(m.now) {
			return m.completeNow(task, wokeAt(m.now)), nil
		}
		task.WakeAt = &wakeAt

	case model.NodeTypeWaitForEvent:
		if spec.Event == "" {
			return nil, m.failNode(n, input, "WAIT_FOR_EVENT requires spec.event")
		}
		signal, err := m.deps.Store.PendingSignal(spec.Event)
		if err != nil {
			return nil, &StoreError{Err: err}
		}
		if signal != nil {
			m.emit(ConsumeSignal{SignalID: signal.ID})
			return m.completeNow(task, signalOutput(signal.Payload)), nil
		}
		timeout, err := parseSeconds(spec.TimeoutSeconds)
		if err != nil {
			return nil, m.failNode(n, input, fmt.Sprintf("timeout_seconds: %v", err))
		}
		if timeout > 0 {
			deadline := m.now.Add(timeout)
			task.WakeAt = &deadline
		}
		task.AwaitedSignal = spec.Event
	}

	m.emit(task)
	return nil, nil
}

// completeNow records a wait that is already satisfied and continues past it.
func (m *Machine) completeNow(task CreateTask, output json.RawMessage) []*model.Node {
	task.Status, task.Output = model.TaskInstanceStatusCompleted, output
	m.emit(task)
	m.setOutput(task.Node.NodeID, output)
	return m.def.Successors(task.Node.NodeID)
}

func (m *Machine) failNode(n *model.Node, input json.RawMessage, reason string) error {
	m.emit(CreateTask{TaskID: m.deps.NewID(), Node: n, Status: model.TaskInstanceStatusFailed, Input: input, Error: reason})
	return nil
}

// wakeTime returns when a WAIT node fires.
func (s waitSpec) wakeTime(now time.Time) (time.Time, error) {
	if s.Until != "" {
		t, err := time.Parse(time.RFC3339, s.Until)
		if err != nil {
			return time.Time{}, fmt.Errorf("until must be an RFC 3339 time: %w", err)
		}
		return t, nil
	}
	if len(s.DurationSeconds) == 0 {
		return time.Time{}, errors.New("WAIT requires spec.duration_seconds or spec.until")
	}
	d, err := parseSeconds(s.DurationSeconds)
	if err != nil {
		return time.Time{}, fmt.Errorf("duration_seconds: %w", err)
	}
	return now.Add(d), nil
}

// parseSeconds reads a JSON number or numeric string as a duration in seconds.
func parseSeconds(raw json.RawMessage) (time.Duration, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		raw = json.RawMessage(s)
	}
	f, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("must be a non-negative number, got %s", raw)
	}
	return time.Duration(f * float64(time.Second)), nil
}

func wokeAt(t time.Time) json.RawMessage {
	out, _ := json.Marshal(map[string]string{"woke_at": t.UTC().Format(time.RFC3339Nano)})
	return out
}

func signalOutput(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 || string(payload) == "null" {
		return json.RawMessage("{}")
	}
	return payload
}
