package coord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/util"
	"github.com/rs/zerolog"
)

// TaskResult is a worker's report for one task attempt.
type TaskResult struct {
	TaskID  string
	RunID   string
	NodeID  string
	Attempt int
	Output  json.RawMessage
	Error   string // non-empty for failures
}

func (r TaskResult) ref() taskRef {
	return taskRef{TaskID: r.TaskID, RunID: r.RunID, NodeID: r.NodeID, Attempt: r.Attempt}
}

// ErrRunActive is returned when a retry targets a run that still has tasks in flight.
var ErrRunActive = errors.New("workflow run still has active tasks")

// StartWorkflow creates a workflow run and schedules everything reachable from
// the start node, in a single transaction.
func (c *Coordinator) StartWorkflow(ctx context.Context, p *model.StartWorkflowRequest) (err error) {
	if p.RunID == "" {
		p.RunID = uuid.NewString()
	}
	ctx, span := util.StartWorkflowSpan(ctx, "start", p.WorkflowID, p.RunID)
	defer func() { util.EndSpan(span, err) }()

	logger := c.Logger.With().
		Str("workflow_id", p.WorkflowID).
		Int("workflow_version", p.WorkflowVersion).
		Str("run_id", p.RunID).
		Logger()

	dag, err := c.loadDAG(ctx, p.WorkflowID, p.WorkflowVersion)
	if err != nil {
		return err
	}
	start, err := dag.StartNode(p.StartNodeID)
	if err != nil {
		return err
	}
	scheduledAt := time.Now()
	if p.StartedAtUnixMs > 0 {
		scheduledAt = time.UnixMilli(p.StartedAtUnixMs)
	}

	rc := model.NewRunContext(p.Input, p.ExecutionOptions)
	err = c.inTx(ctx, func(t *txn) error {
		if err := c.repo.InsertRun(ctx, t, p, rc); err != nil {
			return err
		}
		run := &runRow{ID: p.RunID, WorkflowID: p.WorkflowID, WorkflowVersion: p.WorkflowVersion,
			Status: model.WorkflowInstanceStatusRunning}

		es := model.NewExecutionState(dag, rc, c.Template, nil)
		routing, err := es.Begin(start)
		if err != nil {
			return err
		}
		return c.applyRouting(ctx, t, logger, run, es, routing, scheduledAt)
	})
	if err != nil {
		logger.Error().Err(err).Msg("failed to start workflow")
		return err
	}

	util.RecordWorkflowStarted(ctx, p.WorkflowID, p.RunID)
	logger.Info().Str("start_node_id", start.NodeID).Msg("workflow started")
	return nil
}

// HandleNodeCompleted records a successful task attempt and schedules every
// node that became ready, atomically. Stale or duplicate reports are ignored.
func (c *Coordinator) HandleNodeCompleted(ctx context.Context, res TaskResult) (err error) {
	ctx, span := util.StartTaskSpan(ctx, "completed", res.TaskID, res.RunID, res.NodeID)
	defer func() { util.EndSpan(span, err) }()
	logger := c.taskLogger(res)

	err = c.withRun(ctx, res.RunID, func(t *txn, run *runRow) error {
		task, ok, err := c.repo.CompleteTask(ctx, t, res.ref(), res.Output)
		if err != nil {
			return err
		}
		if !ok {
			logger.Debug().Msg("ignoring stale or duplicate completion")
			return nil
		}
		t.onCommit(func() {
			util.RecordTaskCompleted(ctx, res.TaskID, res.RunID, res.NodeID, task.NodeType, sinceOrZero(task.StartedAt))
		})

		rc, err := c.repo.MergeTaskOutput(ctx, t, res.RunID, res.NodeID, res.Output)
		if err != nil {
			return err
		}
		if run.Status.IsTerminal() {
			return nil // e.g. cancelled: keep the output, schedule nothing
		}
		return c.advance(ctx, t, logger, run, rc, res.NodeID)
	})
	if err != nil {
		logger.Error().Err(err).Msg("failed to handle node completion")
	}
	return err
}

// HandleNodeFailed records a failed task attempt. If the node's retry policy
// allows another attempt the task is re-dispatched after its backoff;
// otherwise it is marked FAILED and the run fails once nothing is in flight.
func (c *Coordinator) HandleNodeFailed(ctx context.Context, res TaskResult) (err error) {
	ctx, span := util.StartTaskSpan(ctx, "failed", res.TaskID, res.RunID, res.NodeID)
	defer func() { util.EndSpan(span, err) }()
	logger := c.taskLogger(res).With().Str("error", res.Error).Logger()

	err = c.withRun(ctx, res.RunID, func(t *txn, run *runRow) error {
		if !run.Status.IsTerminal() {
			retried, err := c.retryTask(ctx, t, logger, run, res)
			if err != nil || retried {
				return err
			}
		}

		task, ok, err := c.repo.FailTask(ctx, t, res.ref(), res.Error)
		if err != nil {
			return err
		}
		if !ok {
			logger.Debug().Msg("ignoring stale or duplicate failure")
			return nil
		}
		t.onCommit(func() {
			util.RecordTaskFailed(ctx, res.TaskID, res.RunID, res.NodeID, task.NodeType, sinceOrZero(task.StartedAt), res.Error)
		})
		logger.Warn().Msg("task failed")

		if run.Status.IsTerminal() {
			return nil
		}
		return c.finalizeIfDone(ctx, t, logger, run)
	})
	if err != nil {
		logger.Error().Err(err).Msg("failed to handle node failure")
	}
	return err
}

// retryTask re-dispatches the task when its node's retry policy has attempts
// left. It reports whether a retry was scheduled.
func (c *Coordinator) retryTask(ctx context.Context, t *txn, logger zerolog.Logger, run *runRow, res TaskResult) (bool, error) {
	dag, err := c.loadDAG(ctx, run.WorkflowID, run.WorkflowVersion)
	if err != nil {
		return false, err
	}
	node := dag.GetNode(res.NodeID)
	if node == nil || res.Attempt+1 >= node.MaxAttempts() {
		return false, nil
	}

	dispatch, ok, err := c.repo.RequeueTask(ctx, t, res.ref(), res.Error)
	if err != nil || !ok {
		return ok, err
	}
	dispatch.WorkflowID, dispatch.WorkflowVersion = run.WorkflowID, run.WorkflowVersion
	delay := node.RetryDelay(dispatch.Attempt)
	if err := c.repo.EnqueueDispatch(ctx, t, dispatch, time.Now().Add(delay)); err != nil {
		return false, err
	}
	logger.Warn().
		Int("next_attempt", dispatch.Attempt).
		Int("max_attempts", node.MaxAttempts()).
		Dur("backoff", delay).
		Msg("task failed, retry scheduled")
	return true, nil
}

// RetryWorkflowInstance restarts a run from a node: the node and all its
// descendants are reset and the node is scheduled again, optionally on a
// different workflow version, input or execution options.
func (c *Coordinator) RetryWorkflowInstance(ctx context.Context, p *model.RetryWorkflowRequest) (err error) {
	ctx, span := util.StartWorkflowSpan(ctx, "retry", "", p.RunID)
	defer func() { util.EndSpan(span, err) }()
	logger := c.Logger.With().Str("run_id", p.RunID).Str("node_id", p.NodeID).Logger()

	err = c.withRun(ctx, p.RunID, func(t *txn, run *runRow) error {
		active, _, err := c.repo.CountTasks(ctx, t, p.RunID)
		if err != nil {
			return err
		}
		if active > 0 {
			return fmt.Errorf("%w: %d task(s) in flight; cancel the run before retrying", ErrRunActive, active)
		}

		version, err := c.resolveRetryVersion(ctx, run.WorkflowID, p.WorkflowVersion)
		if err != nil {
			return err
		}
		dag, err := c.loadDAG(ctx, run.WorkflowID, version)
		if err != nil {
			return err
		}
		restart := dag.GetNode(p.NodeID)
		if restart == nil {
			return fmt.Errorf("node %s not found in workflow definition", p.NodeID)
		}

		rc, err := c.repo.LoadRunContext(ctx, t, p.RunID)
		if err != nil {
			return err
		}
		if len(p.Input) > 0 {
			rc.Input = p.Input
		}
		if len(p.ExecutionOptions) > 0 {
			rc.Config = p.ExecutionOptions
		}
		reset := append(dag.Descendants(p.NodeID), p.NodeID)
		for _, id := range reset {
			delete(rc.Tasks, id)
		}

		if err := c.repo.ResetRunForRetry(ctx, t, p.RunID, version, p.Input); err != nil {
			return err
		}
		if err := c.repo.DeleteTasks(ctx, t, p.RunID, reset); err != nil {
			return err
		}
		if err := c.repo.SaveRunContext(ctx, t, p.RunID, rc); err != nil {
			return err
		}
		run.WorkflowVersion, run.Status = version, model.WorkflowInstanceStatusRunning

		states, err := c.repo.LoadNodeStates(ctx, t, p.RunID)
		if err != nil {
			return err
		}
		es := model.NewExecutionState(dag, rc, c.Template, states)
		routing, err := es.Begin(restart)
		if err != nil {
			return err
		}
		logger = logger.With().Str("workflow_id", run.WorkflowID).Int("workflow_version", version).Logger()
		return c.applyRouting(ctx, t, logger, run, es, routing, time.Now())
	})
	if err != nil {
		logger.Error().Err(err).Msg("failed to retry workflow run")
		return err
	}
	logger.Info().Msg("workflow retry started")
	return nil
}

func (c *Coordinator) resolveRetryVersion(ctx context.Context, workflowID string, requested int) (int, error) {
	if requested > 0 {
		return requested, nil
	}
	wf, err := c.GetWorkflow(ctx, workflowID, 0)
	if err != nil {
		return 0, fmt.Errorf("get deployed workflow version: %w", err)
	}
	if !wf.Deployed {
		return 0, fmt.Errorf("workflow has no deployed version")
	}
	return wf.Version, nil
}

// advance routes from a completed node. Routing errors are permanent (a bad
// SWITCH condition, or a node missing from the definition), so they fail the
// run instead of being returned for the worker to retry forever.
func (c *Coordinator) advance(ctx context.Context, t *txn, logger zerolog.Logger, run *runRow, rc *model.RunContext, nodeID string) error {
	dag, err := c.loadDAG(ctx, run.WorkflowID, run.WorkflowVersion)
	if err != nil {
		return err
	}
	states, err := c.repo.LoadNodeStates(ctx, t, run.ID)
	if err != nil {
		return err
	}

	es := model.NewExecutionState(dag, rc, c.Template, states)
	routing, err := es.Advance(nodeID)
	if err != nil {
		logger.Error().Err(err).Msg("routing failed, failing workflow")
		return c.finishRun(ctx, t, logger, run, model.WorkflowInstanceStatusFailed,
			fmt.Sprintf("routing after node %s failed: %v", nodeID, err))
	}
	return c.applyRouting(ctx, t, logger, run, es, routing, time.Now())
}

// applyRouting persists a routing step: control nodes complete inline, skipped
// nodes are recorded, and worker nodes get a task instance plus an outbox
// dispatch. A node whose input fails to resolve is recorded as FAILED. Finally
// the run is finished if nothing is left in flight.
func (c *Coordinator) applyRouting(ctx context.Context, t *txn, logger zerolog.Logger, run *runRow,
	es *model.ExecutionState, routing *model.RoutingResult, scheduledAt time.Time) error {

	for _, cn := range routing.ControlNodes {
		if _, err := c.repo.InsertTask(ctx, t, newTask{ID: uuid.NewString(), RunID: run.ID, Node: cn.Node,
			State: model.TaskInstanceStatusCompleted, Output: cn.Output, ScheduledAt: scheduledAt}); err != nil {
			return err
		}
	}
	for _, n := range routing.SkippedNodes {
		if _, err := c.repo.InsertTask(ctx, t, newTask{ID: uuid.NewString(), RunID: run.ID, Node: n,
			State: model.TaskInstanceStatusSkipped, ScheduledAt: scheduledAt}); err != nil {
			return err
		}
	}
	if len(routing.ControlNodes) > 0 {
		// SWITCH nodes added pass-through outputs and branch decisions.
		if err := c.repo.SaveRunContext(ctx, t, run.ID, es.RunContext()); err != nil {
			return err
		}
	}

	for _, n := range routing.ExecutableNodes {
		if err := c.scheduleNode(ctx, t, logger, run, es, n, scheduledAt); err != nil {
			return err
		}
	}

	logger.Debug().
		Int("scheduled", len(routing.ExecutableNodes)).
		Int("control", len(routing.ControlNodes)).
		Int("skipped", len(routing.SkippedNodes)).
		Msg("routing applied")
	return c.finalizeIfDone(ctx, t, logger, run)
}

func (c *Coordinator) scheduleNode(ctx context.Context, t *txn, logger zerolog.Logger, run *runRow,
	es *model.ExecutionState, n *model.Node, scheduledAt time.Time) error {

	task := newTask{ID: uuid.NewString(), RunID: run.ID, Node: n,
		State: model.TaskInstanceStatusPending, ScheduledAt: scheduledAt}
	input, resolveErr := es.ResolveInput(n)
	if resolveErr != nil {
		logger.Error().Err(resolveErr).Str("node_id", n.NodeID).Msg("input resolution failed, recording node as FAILED")
		task.State, task.Error = model.TaskInstanceStatusFailed, resolveErr.Error()
	} else {
		task.Input = input
	}

	inserted, err := c.repo.InsertTask(ctx, t, task)
	if err != nil || !inserted {
		return err
	}
	nodeType := n.GetNodeTypeForRouting()
	t.onCommit(func() { util.RecordTaskCreated(ctx, task.ID, run.ID, n.NodeID, nodeType) })
	if task.State != model.TaskInstanceStatusPending {
		return nil
	}

	return c.repo.EnqueueDispatch(ctx, t, &taskDispatch{
		TaskID:          task.ID,
		RunID:           run.ID,
		WorkflowID:      run.WorkflowID,
		WorkflowVersion: run.WorkflowVersion,
		NodeID:          n.NodeID,
		NodeType:        nodeType,
		Input:           input,
	}, time.Now())
}

// finalizeIfDone finishes the run once no task is in flight: FAILED if any
// task failed, SUCCEEDED otherwise.
func (c *Coordinator) finalizeIfDone(ctx context.Context, t *txn, logger zerolog.Logger, run *runRow) error {
	active, failed, err := c.repo.CountTasks(ctx, t, run.ID)
	if err != nil || active > 0 {
		return err
	}
	if failed > 0 {
		return c.finishRun(ctx, t, logger, run, model.WorkflowInstanceStatusFailed,
			fmt.Sprintf("workflow has %d failed task(s)", failed))
	}
	return c.finishRun(ctx, t, logger, run, model.WorkflowInstanceStatusSucceeded, "")
}

func (c *Coordinator) finishRun(ctx context.Context, t *txn, logger zerolog.Logger, run *runRow,
	status model.WorkflowInstanceStatus, reason string) error {

	duration, changed, err := c.repo.FinishRun(ctx, t, run.ID, status, reason)
	if err != nil || !changed {
		return err
	}
	run.Status = status
	t.onCommit(func() {
		if status == model.WorkflowInstanceStatusFailed {
			util.RecordWorkflowFailed(ctx, run.WorkflowID, run.ID, duration, reason)
			logger.Error().Str("reason", reason).Msg("workflow failed")
		} else {
			util.RecordWorkflowCompleted(ctx, run.WorkflowID, run.ID, duration)
			logger.Info().Dur("duration", duration).Msg("workflow succeeded")
		}
	})
	return nil
}

// loadDAG returns the definition a run is pinned to, using the in-process
// cache. Deleted definitions still load so in-flight runs can finish.
func (c *Coordinator) loadDAG(ctx context.Context, workflowID string, version int) (*model.WorkflowDAG, error) {
	if dag, ok := c.wfCache.get(workflowID, version); ok {
		return dag, nil
	}
	var specJSON json.RawMessage
	err := c.DB.QueryRow(ctx,
		`SELECT spec_json FROM workflow_definitions WHERE id = $1 AND version = $2`,
		workflowID, version).Scan(&specJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("workflow definition not found: %s v%d", workflowID, version)
	}
	if err != nil {
		return nil, fmt.Errorf("load workflow definition: %w", err)
	}
	var spec model.WorkflowDefinitionSpec
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return nil, fmt.Errorf("parse workflow spec: %w", err)
	}
	dag := model.BuildDAG(&spec)
	c.wfCache.set(workflowID, version, dag)
	return dag, nil
}

func (c *Coordinator) taskLogger(res TaskResult) zerolog.Logger {
	return c.Logger.With().
		Str("run_id", res.RunID).
		Str("node_id", res.NodeID).
		Str("task_id", res.TaskID).
		Int("attempt", res.Attempt).
		Logger()
}

func sinceOrZero(t time.Time) time.Duration {
	if t.IsZero() {
		return 0
	}
	return time.Since(t)
}

// ── Transactions ─────────────────────────────────────────────────────────────

// txn is a transaction plus side effects (metrics, logs) that must only
// happen if it commits.
type txn struct {
	pgx.Tx
	afterCommit []func()
}

func (t *txn) onCommit(fn func()) {
	t.afterCommit = append(t.afterCommit, fn)
}

// inTx runs fn in a transaction and commits it if fn succeeds.
func (c *Coordinator) inTx(ctx context.Context, fn func(t *txn) error) error {
	tx, err := c.DB.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	t := &txn{Tx: tx}
	if err := fn(t); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	for _, f := range t.afterCommit {
		f()
	}
	return nil
}

// withRun runs fn in a transaction holding the run's row lock, so state
// transitions of one run never interleave.
func (c *Coordinator) withRun(ctx context.Context, runID string, fn func(t *txn, run *runRow) error) error {
	return c.inTx(ctx, func(t *txn) error {
		run, err := c.repo.LockRun(ctx, t, runID)
		if err != nil {
			return err
		}
		return fn(t, run)
	})
}
