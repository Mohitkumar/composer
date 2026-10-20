package coord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/mohitkumar/composer/pkg/model"
)

// WAIT and WAIT_FOR_EVENT nodes never reach a worker. Routing parks them as
// WAITING task rows; the coordinator's timer loop resolves due timers and event
// timeouts (FireDueWaits), and SignalWorkflow resolves event waits. Both go
// through the same run-locked transaction as task completions, so a resolved
// wait schedules its successors atomically.

// waitSpec is a wait node's resolved spec. Numbers may also be given as
// strings, so templated values like "{{input.delay}}" work.
type waitSpec struct {
	DurationSeconds json.RawMessage `json:"duration_seconds"`
	Until           string          `json:"until"` // RFC 3339
	Event           string          `json:"event"`
	TimeoutSeconds  json.RawMessage `json:"timeout_seconds"`
}

// timerBatch is how many due waits one FireDueWaits call resolves.
const timerBatch = 200

// park records a WAIT / WAIT_FOR_EVENT node as WAITING. If the wait is already
// satisfied — a zero duration, or a buffered event — the node completes now and
// park returns its output so routing can continue past it.
func (r *router) park(n *model.Node) (resolved bool, output json.RawMessage, err error) {
	input, err := r.resolveInput(n)
	if err != nil {
		var loadErr *model.OutputLoadError
		if errors.As(err, &loadErr) {
			return false, nil, err
		}
		return false, nil, r.insert(newTask{Node: n, State: model.TaskInstanceStatusFailed, Error: err.Error()})
	}
	var spec waitSpec
	if err := json.Unmarshal(input, &spec); err != nil {
		return false, nil, r.insert(newTask{Node: n, State: model.TaskInstanceStatusFailed, Input: input,
			Error: fmt.Sprintf("invalid wait spec: %v", err)})
	}

	now := time.Now()
	task := newTask{Node: n, Input: input, State: model.TaskInstanceStatusWaiting}
	switch n.Type {
	case model.NodeTypeWait:
		wakeAt, err := spec.wakeTime(now)
		if err != nil {
			task.State, task.Error = model.TaskInstanceStatusFailed, err.Error()
			return false, nil, r.insert(task)
		}
		if !wakeAt.After(now) {
			output = wokeAt(now)
			task.State, task.Output = model.TaskInstanceStatusCompleted, output
			return true, output, r.insert(task)
		}
		task.WakeAt = &wakeAt

	case model.NodeTypeWaitForEvent:
		if spec.Event == "" {
			task.State, task.Error = model.TaskInstanceStatusFailed, "WAIT_FOR_EVENT requires spec.event"
			return false, nil, r.insert(task)
		}
		payload, found, err := r.c.repo.ConsumeEvent(r.ctx, r.t, r.run.ID, spec.Event)
		if err != nil {
			return false, nil, err
		}
		if found {
			output = eventOutput(payload)
			task.State, task.Output = model.TaskInstanceStatusCompleted, output
			return true, output, r.insert(task)
		}
		task.EventName = spec.Event
		if timeout, err := parseSeconds(spec.TimeoutSeconds); err != nil {
			task.State, task.Error = model.TaskInstanceStatusFailed, fmt.Sprintf("timeout_seconds: %v", err)
			return false, nil, r.insert(task)
		} else if timeout > 0 {
			deadline := now.Add(timeout)
			task.WakeAt = &deadline
		}
	}

	r.logger.Debug().Str("node_id", n.NodeID).Str("event", task.EventName).Msg("node waiting")
	return false, nil, r.insert(task)
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

func eventOutput(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 || string(payload) == "null" {
		return json.RawMessage("{}")
	}
	return payload
}

// FireDueWaits resolves WAITING tasks whose time has come: WAIT nodes complete
// and their successors are scheduled; WAIT_FOR_EVENT nodes that timed out fail.
// It returns how many waits were resolved. Every coordinator runs it; the run
// lock makes concurrent calls safe.
func (c *Coordinator) FireDueWaits(ctx context.Context) (int, error) {
	due, err := c.repo.DueWaits(ctx, c.DB, timerBatch)
	if err != nil {
		return 0, err
	}
	fired := 0
	for _, w := range due {
		ok, err := c.fireWait(ctx, w)
		if err != nil {
			c.Logger.Error().Err(err).Str("run_id", w.RunID).Str("node_id", w.NodeID).Msg("failed to fire wait")
			continue
		}
		if ok {
			fired++
		}
	}
	return fired, nil
}

func (c *Coordinator) fireWait(ctx context.Context, w dueWait) (fired bool, err error) {
	logger := c.Logger.With().Str("run_id", w.RunID).Str("node_id", w.NodeID).Str("task_id", w.TaskID).Logger()
	err = c.withRun(ctx, w.RunID, func(t *txn, run *runRow) error {
		if run.Status.IsTerminal() {
			return nil
		}
		if w.NodeType == model.NodeTypeWait {
			output := wokeAt(time.Now())
			ok, err := c.repo.ResolveWait(ctx, t, w.RunID, w.TaskID, model.TaskInstanceStatusCompleted, output, "")
			if err != nil || !ok {
				return err
			}
			fired = true
			logger.Debug().Msg("wait timer fired")
			return c.advance(ctx, t, logger, run, w.NodeID, output)
		}

		ok, err := c.repo.ResolveWait(ctx, t, w.RunID, w.TaskID, model.TaskInstanceStatusFailed, nil,
			"timed out waiting for event")
		if err != nil || !ok {
			return err
		}
		fired = true
		logger.Warn().Msg("event wait timed out")
		return c.finalizeIfDone(ctx, t, logger, run)
	})
	return fired, err
}

// SignalWorkflow delivers a named event to a run. If a WAIT_FOR_EVENT node is
// waiting for it, the node completes with payload as its output and routing
// continues; otherwise the event is buffered until such a node is reached.
// It reports whether the event was delivered to a waiting node.
func (c *Coordinator) SignalWorkflow(ctx context.Context, runID, event string, payload json.RawMessage) (delivered bool, err error) {
	if event == "" {
		return false, fmt.Errorf("%w: event name is required", ErrInvalid)
	}
	if len(payload) > 0 && !json.Valid(payload) {
		return false, fmt.Errorf("%w: payload must be valid JSON", ErrInvalid)
	}
	logger := c.Logger.With().Str("run_id", runID).Str("event", event).Logger()

	err = c.withRun(ctx, runID, func(t *txn, run *runRow) error {
		if run.Status.IsTerminal() {
			return fmt.Errorf("%w: run is %s", ErrRunNotActive, run.Status)
		}
		taskID, nodeID, waiting, err := c.repo.FindEventWaiter(ctx, t, runID, event)
		if err != nil {
			return err
		}
		if !waiting {
			logger.Debug().Msg("no node waiting, buffering event")
			return c.repo.InsertEvent(ctx, t, runID, event, payload)
		}

		output := eventOutput(payload)
		if _, err := c.repo.ResolveWait(ctx, t, runID, taskID, model.TaskInstanceStatusCompleted, output, ""); err != nil {
			return err
		}
		delivered = true
		logger.Info().Str("node_id", nodeID).Msg("event delivered")
		return c.advance(ctx, t, logger, run, nodeID, output)
	})
	return delivered, err
}
