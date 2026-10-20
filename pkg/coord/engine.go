package coord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/statemachine"
	"github.com/mohitkumar/composer/pkg/util"
	"github.com/rs/zerolog"
)

// The coordinator is the imperative shell around the workflow state machine
// (pkg/statemachine). Every input — a worker result, a fired timer, an API
// call — becomes a state-machine event, and is handled the same way:
//
//  1. begin a transaction and lock the run (LoadRun)
//  2. let the machine decide: Machine.Apply(event) → Transition
//  3. persist the transition's actions, including task dispatches through the
//     outbox (persist.go), and commit
//
// The lock serializes events per run, and because the decision and its
// effects commit together, the run never shows a half-applied transition.

// TaskResult is a worker's report for one task attempt.
type TaskResult struct {
	TaskID  string
	RunID   string
	NodeID  string
	Attempt int
	Output  json.RawMessage
	Error   string // non-empty for failures
}

// Client-supplied run IDs must be UUIDv7 close to the current time, so they
// land in an existing partition (run-scoped tables are partitioned by run ID).
const (
	runIDMaxAge  = 24 * time.Hour
	runIDMaxSkew = time.Hour
	// timerBatch is how many due waits one FireDueWaits call resolves.
	timerBatch = 200
)

// ── Inputs → events ──────────────────────────────────────────────────────────

// StartWorkflow creates a run and applies RunStarted, in one transaction.
func (c *Coordinator) StartWorkflow(ctx context.Context, p *model.StartWorkflowRequest) (err error) {
	if p.RunID, err = runIDFor(p.RunID); err != nil {
		return err
	}
	ctx, span := util.StartWorkflowSpan(ctx, "start", p.WorkflowID, p.RunID)
	defer func() { util.EndSpan(span, err) }()
	logger := c.Logger.With().Str("workflow_id", p.WorkflowID).Int("workflow_version", p.WorkflowVersion).
		Str("run_id", p.RunID).Logger()

	dag, err := c.loadDAG(ctx, p.WorkflowID, p.WorkflowVersion)
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
		run := statemachine.NewRun(p.RunID, p.WorkflowID, p.WorkflowVersion, rc)
		_, err := c.decide(ctx, t, logger, dag, run, statemachine.RunStarted{StartNodeID: p.StartNodeID}, scheduledAt)
		return err
	})
	if err != nil {
		logger.Error().Err(err).Msg("failed to start workflow")
		return err
	}
	util.RecordWorkflowStarted(ctx, p.WorkflowID, p.RunID)
	logger.Info().Msg("workflow started")
	return nil
}

// HandleNodeCompleted applies a worker's success report.
func (c *Coordinator) HandleNodeCompleted(ctx context.Context, res TaskResult) (err error) {
	ctx, span := util.StartTaskSpan(ctx, "completed", res.TaskID, res.RunID, res.NodeID)
	defer func() { util.EndSpan(span, err) }()
	_, err = c.handle(ctx, res.RunID, c.taskLogger(res), statemachine.NodeCompleted{
		TaskID: res.TaskID, NodeID: res.NodeID, Attempt: res.Attempt, Output: res.Output,
	})
	return err
}

// HandleNodeFailed applies a worker's failure report.
func (c *Coordinator) HandleNodeFailed(ctx context.Context, res TaskResult) (err error) {
	ctx, span := util.StartTaskSpan(ctx, "failed", res.TaskID, res.RunID, res.NodeID)
	defer func() { util.EndSpan(span, err) }()
	_, err = c.handle(ctx, res.RunID, c.taskLogger(res).With().Str("error", res.Error).Logger(), statemachine.NodeFailed{
		TaskID: res.TaskID, NodeID: res.NodeID, Attempt: res.Attempt, Error: res.Error,
	})
	return err
}

// FireDueWaits applies TimerFired to every WAITING task whose wake-up time has
// passed and returns how many timers fired. Every coordinator runs it; the run
// lock makes concurrent calls safe (the loser's event is ignored).
func (c *Coordinator) FireDueWaits(ctx context.Context) (int, error) {
	due, err := c.repo.DueWaits(ctx, c.DB, timerBatch)
	if err != nil {
		return 0, err
	}
	fired := 0
	for _, w := range due {
		logger := c.Logger.With().Str("run_id", w.RunID).Str("node_id", w.NodeID).Str("task_id", w.TaskID).Logger()
		tr, err := c.handle(ctx, w.RunID, logger, statemachine.TimerFired{TaskID: w.TaskID, NodeID: w.NodeID})
		switch {
		case err != nil:
			logger.Error().Err(err).Msg("failed to fire timer")
		case tr.Ignored == "":
			fired++
		}
	}
	return fired, nil
}

// SignalWorkflow applies SignalReceived: an external event for the run's
// WAIT_FOR_EVENT nodes. It reports whether a waiting node consumed it (false:
// it was buffered for a node the run has not reached yet).
func (c *Coordinator) SignalWorkflow(ctx context.Context, runID, event string, payload json.RawMessage) (bool, error) {
	logger := c.Logger.With().Str("run_id", runID).Str("event", event).Logger()
	tr, err := c.handle(ctx, runID, logger, statemachine.SignalReceived{Name: event, Payload: payload})
	if err != nil {
		return false, err
	}
	return tr.SignalDelivered, nil
}

// RetryWorkflowInstance applies RunRetried: the run restarts from a node,
// optionally on another definition version (0: the deployed one) and with new
// input or execution options.
func (c *Coordinator) RetryWorkflowInstance(ctx context.Context, p *model.RetryWorkflowRequest) (err error) {
	ctx, span := util.StartWorkflowSpan(ctx, "retry", "", p.RunID)
	defer func() { util.EndSpan(span, err) }()
	logger := c.Logger.With().Str("run_id", p.RunID).Str("node_id", p.NodeID).Logger()

	_, err = c.handleWith(ctx, p.RunID, logger, func(run *statemachine.Run) (*model.WorkflowDAG, statemachine.Event, error) {
		version, err := c.resolveRetryVersion(ctx, run.WorkflowID, p.WorkflowVersion)
		if err != nil {
			return nil, nil, err
		}
		dag, err := c.loadDAG(ctx, run.WorkflowID, version)
		return dag, statemachine.RunRetried{FromNodeID: p.NodeID, Version: version,
			Input: p.Input, Config: p.ExecutionOptions}, err
	})
	return err
}

// CancelWorkflowInstance applies RunCancelled.
func (c *Coordinator) CancelWorkflowInstance(ctx context.Context, runID, reason string) error {
	_, err := c.handle(ctx, runID, c.Logger.With().Str("run_id", runID).Logger(), statemachine.RunCancelled{Reason: reason})
	return err
}

// ── Event handling ───────────────────────────────────────────────────────────

// handle applies ev to a run on the definition version the run is pinned to.
func (c *Coordinator) handle(ctx context.Context, runID string, logger zerolog.Logger, ev statemachine.Event) (*statemachine.Transition, error) {
	return c.handleWith(ctx, runID, logger, func(run *statemachine.Run) (*model.WorkflowDAG, statemachine.Event, error) {
		dag, err := c.loadDAG(ctx, run.WorkflowID, run.WorkflowVersion)
		return dag, ev, err
	})
}

// handleWith locks and loads the run, lets prepare pick the definition and
// event, then decides and persists — all in one transaction.
func (c *Coordinator) handleWith(ctx context.Context, runID string, logger zerolog.Logger,
	prepare func(*statemachine.Run) (*model.WorkflowDAG, statemachine.Event, error)) (*statemachine.Transition, error) {

	var tr *statemachine.Transition
	err := c.inTx(ctx, func(t *txn) error {
		run, err := c.repo.LoadRun(ctx, t, runID)
		if err != nil {
			return err
		}
		dag, ev, err := prepare(run)
		if err != nil {
			return err
		}
		tr, err = c.decide(ctx, t, logger, dag, run, ev, time.Now())
		return err
	})
	if err != nil {
		logger.Error().Err(err).Msg("event not applied")
	}
	return tr, err
}

// decide runs the state machine for one event and persists its transition.
func (c *Coordinator) decide(ctx context.Context, t *txn, logger zerolog.Logger, dag *model.WorkflowDAG,
	run *statemachine.Run, ev statemachine.Event, scheduledAt time.Time) (*statemachine.Transition, error) {

	machine := statemachine.New(dag, run, statemachine.Deps{
		Templates: c.Template,
		Store:     runStore{ctx: ctx, q: t, repo: c.repo, runID: run.ID},
		Now:       time.Now,
		NewID:     newTaskID,
	})
	tr, err := machine.Apply(ev)
	if err != nil {
		return nil, err
	}
	logger.Debug().Str("event", fmt.Sprintf("%T", ev)).Int("actions", len(tr.Actions)).Str("ignored", tr.Ignored).
		Msg("event applied")
	if tr.Ignored != "" {
		return tr, nil
	}
	p := &persister{c: c, ctx: ctx, t: t, logger: logger, run: run, scheduledAt: scheduledAt}
	return tr, p.persist(tr.Actions)
}

// runStore is the machine's on-demand read access to the locked run.
type runStore struct {
	ctx   context.Context
	q     db.Querier
	repo  repository
	runID string
}

func (s runStore) Outputs(nodeIDs []string) (map[string]json.RawMessage, error) {
	return s.repo.LoadOutputs(s.ctx, s.q, s.runID, nodeIDs)
}

func (s runStore) PendingSignal(name string) (*statemachine.Signal, error) {
	return s.repo.PendingSignal(s.ctx, s.q, s.runID, name)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// runIDFor returns a new UUIDv7 run ID, or validates a client-supplied one.
func runIDFor(requested string) (string, error) {
	if requested == "" {
		id, err := uuid.NewV7()
		return id.String(), err
	}
	id, err := uuid.Parse(requested)
	if err != nil || id.Version() != 7 {
		return "", fmt.Errorf("%w: run_id must be a UUIDv7", ErrInvalid)
	}
	sec, nsec := id.Time().UnixTime()
	if age := time.Since(time.Unix(sec, nsec)); age > runIDMaxAge || age < -runIDMaxSkew {
		return "", fmt.Errorf("%w: run_id timestamp must be within the last %v", ErrInvalid, runIDMaxAge)
	}
	return id.String(), nil
}

func (c *Coordinator) resolveRetryVersion(ctx context.Context, workflowID string, requested int) (int, error) {
	if requested > 0 {
		return requested, nil
	}
	wf, err := c.GetWorkflow(ctx, workflowID, 0)
	if err != nil {
		return 0, err
	}
	if !wf.Deployed {
		return 0, fmt.Errorf("%w: workflow has no deployed version", ErrInvalid)
	}
	return wf.Version, nil
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
		return nil, fmt.Errorf("%w: workflow definition %s v%d", ErrNotFound, workflowID, version)
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

// newTaskID returns a time-ordered task ID, which keeps index inserts local.
func newTaskID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}
