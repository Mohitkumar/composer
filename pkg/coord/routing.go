package coord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/util"
	"github.com/rs/zerolog"
)

// router applies routing decisions for one run inside one transaction that
// holds the run's lock. It turns the ExecutionState's pure decisions into task
// rows, dispatches and parked waits.
type router struct {
	c           *Coordinator
	ctx         context.Context
	t           *txn
	logger      zerolog.Logger
	run         *runRow
	es          *model.ExecutionState
	scheduledAt time.Time
}

func (c *Coordinator) newRouter(ctx context.Context, t *txn, logger zerolog.Logger, run *runRow,
	dag *model.WorkflowDAG, rc *model.RunContext, states model.NodeStates) *router {

	r := &router{c: c, ctx: ctx, t: t, logger: logger, run: run, scheduledAt: time.Now()}
	r.es = model.NewExecutionState(dag, rc, c.Template, states, r.loadOutputs)
	return r
}

func (r *router) loadOutputs(nodeIDs []string) (map[string]json.RawMessage, error) {
	return r.c.repo.LoadOutputs(r.ctx, r.t, r.run.ID, nodeIDs)
}

// begin activates node unconditionally — the start node, or the node a retry
// restarts from — and finishes the run if nothing ends up in flight.
func (r *router) begin(node *model.Node) error {
	routing, err := r.es.Begin(node)
	if err != nil {
		var loadErr *model.OutputLoadError
		if errors.As(err, &loadErr) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := r.persist(routing); err != nil {
		return err
	}
	return r.c.finalizeIfDone(r.ctx, r.t, r.logger, r.run)
}

// advance routes from a node that completed with output and finishes the run
// if nothing is left in flight.
func (r *router) advance(nodeID string, output json.RawMessage) error {
	if err := r.step(nodeID, output); err != nil {
		return err
	}
	return r.c.finalizeIfDone(r.ctx, r.t, r.logger, r.run)
}

// step routes from one completed node and persists the result, recursing into
// waits that resolve immediately. It never finalizes the run: siblings may
// still be scheduled by the caller.
func (r *router) step(nodeID string, output json.RawMessage) error {
	if r.run.Status.IsTerminal() {
		return nil
	}
	r.es.SetOutput(nodeID, output)
	routing, err := r.es.Advance(nodeID)
	if err != nil {
		var loadErr *model.OutputLoadError
		if errors.As(err, &loadErr) {
			return err // transient: abort the transaction, the report is retried
		}
		// Permanent (a bad SWITCH condition, a node missing from the
		// definition): fail the run instead of letting the worker retry forever.
		r.logger.Error().Err(err).Msg("routing failed, failing workflow")
		return r.c.finishRun(r.ctx, r.t, r.logger, r.run, model.WorkflowInstanceStatusFailed,
			fmt.Sprintf("routing after node %s failed: %v", nodeID, err))
	}
	return r.persist(routing)
}

// persist records a routing step: control nodes complete inline, skipped nodes
// are recorded, worker nodes are dispatched, and wait nodes are parked.
func (r *router) persist(routing *model.RoutingResult) error {
	for _, cn := range routing.ControlNodes {
		if err := r.insert(newTask{Node: cn.Node, State: model.TaskInstanceStatusCompleted,
			Output: cn.Output, Branches: cn.Branches}); err != nil {
			return err
		}
	}
	for _, n := range routing.SkippedNodes {
		if err := r.insert(newTask{Node: n, State: model.TaskInstanceStatusSkipped}); err != nil {
			return err
		}
	}
	for _, n := range routing.ExecutableNodes {
		if err := r.schedule(n); err != nil {
			return err
		}
	}
	for _, n := range routing.WaitingNodes {
		resolved, output, err := r.park(n)
		if err != nil {
			return err
		}
		if resolved {
			if err := r.step(n.NodeID, output); err != nil {
				return err
			}
		}
	}

	r.logger.Debug().
		Int("scheduled", len(routing.ExecutableNodes)).
		Int("control", len(routing.ControlNodes)).
		Int("skipped", len(routing.SkippedNodes)).
		Int("waiting", len(routing.WaitingNodes)).
		Msg("routing applied")
	return nil
}

// schedule creates a worker node's task and its outbox dispatch. A node whose
// input fails to resolve is recorded as FAILED instead.
func (r *router) schedule(n *model.Node) error {
	input, err := r.resolveInput(n)
	if err != nil {
		var loadErr *model.OutputLoadError
		if errors.As(err, &loadErr) {
			return err
		}
		return r.insert(newTask{Node: n, State: model.TaskInstanceStatusFailed, Error: err.Error()})
	}

	task := newTask{ID: newTaskID(), Node: n, State: model.TaskInstanceStatusPending, Input: input}
	inserted, err := r.insertTask(task)
	if err != nil || !inserted {
		return err
	}
	return r.c.repo.EnqueueDispatch(r.ctx, r.t, &taskDispatch{
		TaskID:          task.ID,
		RunID:           r.run.ID,
		WorkflowID:      r.run.WorkflowID,
		WorkflowVersion: r.run.WorkflowVersion,
		NodeID:          n.NodeID,
		NodeType:        n.GetNodeTypeForRouting(),
		Queue:           n.Queue(),
		Input:           input,
	}, time.Now())
}

// resolveInput resolves the node's templates, logging resolution failures.
func (r *router) resolveInput(n *model.Node) (json.RawMessage, error) {
	input, err := r.es.ResolveInput(n)
	if err != nil {
		r.logger.Error().Err(err).Str("node_id", n.NodeID).Msg("input resolution failed")
	}
	return input, err
}

func (r *router) insert(t newTask) error {
	_, err := r.insertTask(t)
	return err
}

// insertTask fills in the run-level fields and inserts the task.
func (r *router) insertTask(t newTask) (bool, error) {
	if t.ID == "" {
		t.ID = newTaskID()
	}
	t.RunID, t.ScheduledAt = r.run.ID, r.scheduledAt
	inserted, err := r.c.repo.InsertTask(r.ctx, r.t, t)
	if err != nil || !inserted {
		return inserted, err
	}
	nodeType, runID, nodeID, taskID := t.Node.GetNodeTypeForRouting(), r.run.ID, t.Node.NodeID, t.ID
	if t.State != model.TaskInstanceStatusSkipped {
		r.t.onCommit(func() { util.RecordTaskCreated(r.ctx, taskID, runID, nodeID, nodeType) })
	}
	return true, nil
}
