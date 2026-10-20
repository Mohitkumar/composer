package coord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/model"
)

// repository holds the SQL for workflow runs, task instances, waits and the
// outbox. Every method takes the Querier to run on, so callers decide the
// transaction boundary; methods never log — they return errors for the caller.
//
// Run-scoped tables are partitioned by run ID, so every query here filters on
// run_id to touch a single partition.
type repository struct {
	// streamShards is the number of streams per task queue.
	streamShards int
}

// runRow is a locked workflow run.
type runRow struct {
	ID              string
	WorkflowID      string
	WorkflowVersion int
	Status          model.WorkflowInstanceStatus
}

// taskRef identifies one execution attempt of a task. Results are applied only
// when all fields match, which fences off reports from stale attempts.
type taskRef struct {
	TaskID  string
	RunID   string
	NodeID  string
	Attempt int
}

// finishedTask is what the coordinator needs about a task it just transitioned.
type finishedTask struct {
	NodeType  string
	StartedAt time.Time // zero when the task never started
}

// newTask is a task instance row to insert.
type newTask struct {
	ID          string
	RunID       string
	Node        *model.Node
	State       model.TaskInstanceStatus
	Input       json.RawMessage
	Output      json.RawMessage
	Error       string
	Branches    []string   // SWITCH decision
	WakeAt      *time.Time // WAITING: timer or timeout
	EventName   string     // WAITING on an event
	ScheduledAt time.Time
}

// ── Workflow runs ────────────────────────────────────────────────────────────

// InsertRun creates a RUNNING workflow run. It fails if the definition is
// missing or deleted, or if the run ID is already taken.
func (repository) InsertRun(ctx context.Context, q db.Querier, p *model.StartWorkflowRequest, rc *model.RunContext) error {
	var deletedAt *time.Time
	err := q.QueryRow(ctx,
		`SELECT deleted_at FROM workflow_definitions WHERE id = $1 AND version = $2`,
		p.WorkflowID, p.WorkflowVersion).Scan(&deletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: workflow definition %s v%d", ErrNotFound, p.WorkflowID, p.WorkflowVersion)
	}
	if err != nil {
		return fmt.Errorf("check workflow definition: %w", err)
	}
	if deletedAt != nil {
		return fmt.Errorf("%w: workflow %s v%d has been deleted", ErrInvalid, p.WorkflowID, p.WorkflowVersion)
	}

	contextJSON, err := json.Marshal(rc.Persisted())
	if err != nil {
		return fmt.Errorf("marshal run context: %w", err)
	}
	tag, err := q.Exec(ctx, `
		INSERT INTO workflow_runs (id, workflow_definition_id, workflow_version, status, input_json, context_json)
		VALUES ($1, $2, $3, 'RUNNING', $4::jsonb, $5::jsonb)
		ON CONFLICT (id) DO NOTHING`,
		p.RunID, p.WorkflowID, p.WorkflowVersion, nullJSON(p.Input), contextJSON)
	if err != nil {
		return fmt.Errorf("insert workflow run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: workflow run %s already exists", ErrConflict, p.RunID)
	}
	return nil
}

// LockRun takes the run's row lock for the rest of the transaction. Every
// state transition of a run happens under this lock, which serializes
// concurrent completions so join decisions always see each other's writes.
func (repository) LockRun(ctx context.Context, q db.Querier, runID string) (*runRow, error) {
	run := &runRow{ID: runID}
	err := q.QueryRow(ctx, `
		SELECT workflow_definition_id, workflow_version, status
		  FROM workflow_runs
		 WHERE id = $1 AND deleted_at IS NULL
		   FOR UPDATE`,
		runID).Scan(&run.WorkflowID, &run.WorkflowVersion, &run.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: workflow run %s", ErrNotFound, runID)
	}
	if err != nil {
		return nil, fmt.Errorf("lock workflow run: %w", err)
	}
	return run, nil
}

// LoadRunContext reads the run's input and execution options. Task outputs
// are not part of it; routing loads those on demand (LoadOutputs).
func (repository) LoadRunContext(ctx context.Context, q db.Querier, runID string) (*model.RunContext, error) {
	var input, contextJSON json.RawMessage
	if err := q.QueryRow(ctx,
		`SELECT input_json, context_json FROM workflow_runs WHERE id = $1`, runID).
		Scan(&input, &contextJSON); err != nil {
		return nil, fmt.Errorf("load run context: %w", err)
	}
	return model.DecodeRunContext(input, contextJSON)
}

// SaveRunContext stores the run's input and execution options.
func (repository) SaveRunContext(ctx context.Context, q db.Querier, runID string, rc *model.RunContext) error {
	contextJSON, err := json.Marshal(rc.Persisted())
	if err != nil {
		return fmt.Errorf("marshal run context: %w", err)
	}
	if _, err := q.Exec(ctx,
		`UPDATE workflow_runs SET context_json = $1, updated_at = NOW() WHERE id = $2`,
		contextJSON, runID); err != nil {
		return fmt.Errorf("save run context: %w", err)
	}
	return nil
}

// FinishRun moves a non-terminal run to SUCCEEDED or FAILED. It returns the
// run's duration and whether this call made the transition.
func (repository) FinishRun(ctx context.Context, q db.Querier, runID string, status model.WorkflowInstanceStatus, reason string) (time.Duration, bool, error) {
	var errorJSON []byte
	if reason != "" {
		errorJSON, _ = json.Marshal(map[string]string{"error": reason})
	}
	var createdAt time.Time
	err := q.QueryRow(ctx, `
		UPDATE workflow_runs
		   SET status = $1, error_json = $2::jsonb, completed_at = NOW(), updated_at = NOW()
		 WHERE id = $3 AND status IN ('RUNNING', 'PENDING')
		RETURNING created_at`,
		status, errorJSON, runID).Scan(&createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("finish workflow run: %w", err)
	}
	return time.Since(createdAt), true, nil
}

// ResetRunForRetry puts a run back into RUNNING on the given version,
// optionally replacing its input.
func (repository) ResetRunForRetry(ctx context.Context, q db.Querier, runID string, version int, input json.RawMessage) error {
	_, err := q.Exec(ctx, `
		UPDATE workflow_runs
		   SET status = 'RUNNING',
		       workflow_version = $1,
		       input_json = COALESCE($2::jsonb, input_json),
		       error_json = NULL,
		       completed_at = NULL,
		       updated_at = NOW()
		 WHERE id = $3`,
		version, nullJSON(input), runID)
	if err != nil {
		return fmt.Errorf("reset workflow run: %w", err)
	}
	return nil
}

// CancelRunTasks fails every task of the run that has not finished.
func (repository) CancelRunTasks(ctx context.Context, q db.Querier, runID, reason string) error {
	lastError, _ := json.Marshal(map[string]string{"error": reason})
	_, err := q.Exec(ctx, `
		UPDATE task_instances
		   SET state = 'FAILED', last_error = $1::jsonb, finished_at = NOW(), updated_at = NOW(),
		       lease_owner = NULL, lease_expires_at = NULL
		 WHERE run_id = $2 AND state IN ('PENDING', 'QUEUED', 'RUNNING', 'WAITING')`,
		lastError, runID)
	if err != nil {
		return fmt.Errorf("cancel run tasks: %w", err)
	}
	return nil
}

// ── Task instances ───────────────────────────────────────────────────────────

// LoadNodeStates returns the state (and SWITCH decision) of every task of the run.
func (repository) LoadNodeStates(ctx context.Context, q db.Querier, runID string) (model.NodeStates, error) {
	rows, err := q.Query(ctx,
		`SELECT node_id, state, branches FROM task_instances WHERE run_id = $1`, runID)
	if err != nil {
		return nil, fmt.Errorf("load node states: %w", err)
	}
	defer rows.Close()

	states := model.NodeStates{}
	for rows.Next() {
		var nodeID string
		var st model.NodeState
		if err := rows.Scan(&nodeID, &st.Status, &st.Branches); err != nil {
			return nil, fmt.Errorf("scan node state: %w", err)
		}
		states[nodeID] = st
	}
	return states, rows.Err()
}

// LoadOutputs returns the outputs of the given completed nodes of a run.
func (repository) LoadOutputs(ctx context.Context, q db.Querier, runID string, nodeIDs []string) (map[string]json.RawMessage, error) {
	rows, err := q.Query(ctx, `
		SELECT node_id, output_json FROM task_instances
		 WHERE run_id = $1 AND node_id = ANY($2) AND state IN ('COMPLETED', 'SUCCEEDED')`,
		runID, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("load task outputs: %w", err)
	}
	defer rows.Close()

	outputs := make(map[string]json.RawMessage, len(nodeIDs))
	for rows.Next() {
		var nodeID string
		var output json.RawMessage
		if err := rows.Scan(&nodeID, &output); err != nil {
			return nil, fmt.Errorf("scan task output: %w", err)
		}
		outputs[nodeID] = output
	}
	return outputs, rows.Err()
}

// CountTasks returns how many of the run's tasks are still active
// (pending, queued, running or waiting) and how many failed.
func (repository) CountTasks(ctx context.Context, q db.Querier, runID string) (active, failed int, err error) {
	err = q.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE state IN ('PENDING', 'QUEUED', 'RUNNING', 'WAITING')),
		       COUNT(*) FILTER (WHERE state = 'FAILED')
		  FROM task_instances
		 WHERE run_id = $1`,
		runID).Scan(&active, &failed)
	if err != nil {
		return 0, 0, fmt.Errorf("count tasks: %w", err)
	}
	return active, failed, nil
}

// InsertTask inserts a task instance. It reports false, without error, when a
// task for the same (run, node) already exists.
func (repository) InsertTask(ctx context.Context, q db.Querier, t newTask) (bool, error) {
	var lastError []byte
	if t.Error != "" {
		lastError, _ = json.Marshal(map[string]string{"error": t.Error})
	}
	var eventName *string
	if t.EventName != "" {
		eventName = &t.EventName
	}
	tag, err := q.Exec(ctx, `
		INSERT INTO task_instances
		       (id, run_id, node_id, node_type, queue, state, input_json, output_json, last_error,
		        branches, wake_at, event_name, scheduled_at, finished_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9::jsonb, $10, $11, $12, $13,
		        CASE WHEN $6 IN ('COMPLETED', 'FAILED', 'SKIPPED') THEN NOW() END,
		        CASE WHEN $6 = 'COMPLETED' THEN NOW() END)
		ON CONFLICT (run_id, node_id) DO NOTHING`,
		t.ID, t.RunID, t.Node.NodeID, t.Node.GetNodeTypeForRouting(), t.Node.Queue(), t.State,
		nullJSON(t.Input), nullJSON(t.Output), lastError,
		t.Branches, t.WakeAt, eventName, t.ScheduledAt)
	if err != nil {
		return false, fmt.Errorf("insert task instance for node %s: %w", t.Node.NodeID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// CompleteTask marks the referenced attempt COMPLETED and releases its lease.
// It reports false when the attempt is stale or the task already finished.
func (repository) CompleteTask(ctx context.Context, q db.Querier, ref taskRef, output json.RawMessage) (*finishedTask, bool, error) {
	return scanFinishedTask(q.QueryRow(ctx, `
		UPDATE task_instances
		   SET state = 'COMPLETED', output_json = $1::jsonb,
		       completed_at = NOW(), finished_at = NOW(), updated_at = NOW(),
		       lease_owner = NULL, lease_expires_at = NULL
		 WHERE run_id = $2 AND id = $3 AND node_id = $4 AND attempt = $5
		   AND state IN ('PENDING', 'QUEUED', 'RUNNING')
		RETURNING node_type, started_at`,
		nullJSON(output), ref.RunID, ref.TaskID, ref.NodeID, ref.Attempt))
}

// FailTask marks the referenced attempt FAILED and releases its lease.
func (repository) FailTask(ctx context.Context, q db.Querier, ref taskRef, reason string) (*finishedTask, bool, error) {
	lastError, _ := json.Marshal(map[string]string{"error": reason})
	return scanFinishedTask(q.QueryRow(ctx, `
		UPDATE task_instances
		   SET state = 'FAILED', last_error = $1::jsonb,
		       finished_at = NOW(), updated_at = NOW(),
		       lease_owner = NULL, lease_expires_at = NULL
		 WHERE run_id = $2 AND id = $3 AND node_id = $4 AND attempt = $5
		   AND state IN ('PENDING', 'QUEUED', 'RUNNING')
		RETURNING node_type, started_at`,
		lastError, ref.RunID, ref.TaskID, ref.NodeID, ref.Attempt))
}

// RequeueTask schedules the next attempt of a failed execution: the task goes
// back to PENDING with attempt+1. It returns the dispatch for the new attempt.
func (repository) RequeueTask(ctx context.Context, q db.Querier, ref taskRef, reason string) (*taskDispatch, bool, error) {
	lastError, _ := json.Marshal(map[string]string{"error": reason})
	d := &taskDispatch{TaskID: ref.TaskID, RunID: ref.RunID, NodeID: ref.NodeID}
	err := q.QueryRow(ctx, `
		UPDATE task_instances
		   SET state = 'PENDING', attempt = attempt + 1, last_error = $1::jsonb,
		       started_at = NULL, updated_at = NOW(),
		       lease_owner = NULL, lease_expires_at = NULL
		 WHERE run_id = $2 AND id = $3 AND node_id = $4 AND attempt = $5
		   AND state IN ('PENDING', 'QUEUED', 'RUNNING')
		RETURNING node_type, COALESCE(queue, lower(node_type)), attempt, input_json`,
		lastError, ref.RunID, ref.TaskID, ref.NodeID, ref.Attempt).
		Scan(&d.NodeType, &d.Queue, &d.Attempt, &d.Input)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("requeue task: %w", err)
	}
	return d, true, nil
}

// DeleteTasks removes the given nodes' task instances from a run.
func (repository) DeleteTasks(ctx context.Context, q db.Querier, runID string, nodeIDs []string) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	if _, err := q.Exec(ctx,
		`DELETE FROM task_instances WHERE run_id = $1 AND node_id = ANY($2)`,
		runID, nodeIDs); err != nil {
		return fmt.Errorf("delete task instances: %w", err)
	}
	return nil
}

func scanFinishedTask(row pgx.Row) (*finishedTask, bool, error) {
	var t finishedTask
	var startedAt *time.Time
	err := row.Scan(&t.NodeType, &startedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("update task instance: %w", err)
	}
	if startedAt != nil {
		t.StartedAt = *startedAt
	}
	return &t, true, nil
}

// nullJSON maps an empty payload to SQL NULL instead of invalid JSON.
func nullJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return []byte(b)
}
