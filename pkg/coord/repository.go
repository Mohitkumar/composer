package coord

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/util"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/trace"
)

// WorkflowRepository handles all database operations for workflow runs and task instances
type WorkflowRepository interface {
	// Workflow Run operations
	InsertWorkflowRun(ctx context.Context, tx pgx.Tx, p *model.StartWorkflowRequest, logger zerolog.Logger, span trace.Span) error
	GetWorkflowRunInfo(ctx context.Context, runID string) (workflowID string, workflowVersion int, err error)
	LoadRunContext(ctx context.Context, tx pgx.Tx, runID string, logger zerolog.Logger, span trace.Span) (*model.RunContext, error)
	UpdateRunContext(ctx context.Context, tx pgx.Tx, runID string, runContext *model.RunContext, logger zerolog.Logger, span trace.Span) error
	LoadAndUpdateRunContext(ctx context.Context, tx pgx.Tx, runID, nodeID string, output json.RawMessage, logger zerolog.Logger, span trace.Span) (*model.RunContext, error)
	MarkWorkflowCompleted(ctx context.Context, tx pgx.Tx, runID, workflowID string, span trace.Span, logger zerolog.Logger) error
	MarkWorkflowFailed(ctx context.Context, tx pgx.Tx, runID, workflowID, reason string, logger zerolog.Logger, span trace.Span) error
	CheckPendingTasks(ctx context.Context, tx pgx.Tx, runID string) (bool, int, error)
	CheckFailedTasks(ctx context.Context, tx pgx.Tx, runID string) (int, error)

	// Task Instance operations
	InsertTaskInstance(ctx context.Context, tx pgx.Tx, taskID, runID string, node *model.Node, taskInput json.RawMessage, scheduledAt time.Time, logger zerolog.Logger, span trace.Span) error
	InsertTaskInstanceWithState(ctx context.Context, tx pgx.Tx, taskID, runID string, node *model.Node, taskInput json.RawMessage, scheduledAt time.Time, state string, errorMsg string, logger zerolog.Logger, span trace.Span) error
	MarkTaskCompleted(ctx context.Context, tx pgx.Tx, runID, nodeID string, output json.RawMessage, logger zerolog.Logger, span trace.Span) (updated bool, taskID string, nodeType string, startedAt time.Time, err error)
	MarkTaskFailed(ctx context.Context, tx pgx.Tx, runID, nodeID, errorMsg string, logger zerolog.Logger, span trace.Span) (bool, error)
	TaskInstanceExists(ctx context.Context, tx pgx.Tx, runID, nodeID string) (bool, string, string, error)
	GetTaskInstanceInfo(ctx context.Context, tx pgx.Tx, runID, nodeID string) (taskID string, nodeType string, taskStartTime time.Time, err error)
	ClearTaskLease(ctx context.Context, tx pgx.Tx, runID, nodeID string, logger zerolog.Logger, span trace.Span) error

	// Outbox operations
	InsertTaskOutbox(ctx context.Context, tx pgx.Tx, taskID, runID string, node *model.Node, taskInput json.RawMessage, logger zerolog.Logger, span trace.Span) error

	// Parallel execution operations
	CheckParallelBranchesComplete(ctx context.Context, tx pgx.Tx, runID string, branchNodeIDs []string) (completedCount, totalCount int, err error)
	// CountCompletedBranches counts completed branch nodes inside a transaction.
	// FOR UPDATE serializes concurrent join checks so only one goroutine proceeds.
	CountCompletedBranches(ctx context.Context, tx pgx.Tx, runID string, branchNodeIDs []string) (int, error)

	// Workflow run status operations
	GetWorkflowRunStatus(ctx context.Context, tx pgx.Tx, runID string) (status string, err error)
	GetWorkflowRunForRetry(ctx context.Context, runID string) (workflowID string, workflowVersion int, status string, err error)
	UpdateWorkflowRunStatus(ctx context.Context, tx pgx.Tx, runID string, status string, workflowVersion int) error
	UpdateWorkflowInput(ctx context.Context, tx pgx.Tx, runID string, input json.RawMessage) error
	DeleteTaskInstances(ctx context.Context, tx pgx.Tx, runID string, nodeIDs []string, logger zerolog.Logger, span trace.Span) error

	// RecoverOrphanedTasks is called on startup to re-queue tasks orphaned by a coordinator crash.
	// It resets RUNNING tasks with expired leases to PENDING, then creates missing outbox entries
	// for any PENDING task that has no active outbox row.
	RecoverOrphanedTasks(ctx context.Context, logger zerolog.Logger) error
}

// workflowRepository implements WorkflowRepository
type workflowRepository struct {
	db    db.DB
	coord *Coordinator // For access to mergeRunContext helper
}

// NewWorkflowRepository creates a new workflow repository
func NewWorkflowRepository(db db.DB, coord *Coordinator) WorkflowRepository {
	return &workflowRepository{
		db:    db,
		coord: coord,
	}
}

// InsertWorkflowRun inserts a new workflow run
func (r *workflowRepository) InsertWorkflowRun(ctx context.Context, tx pgx.Tx, p *model.StartWorkflowRequest, logger zerolog.Logger, span trace.Span) error {
	// Check if workflow is deleted before creating run
	var deletedAt sql.NullTime
	err := tx.QueryRow(ctx, `SELECT deleted_at FROM workflow_definitions WHERE id = $1 AND version = $2`,
		p.WorkflowID, p.WorkflowVersion).Scan(&deletedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("workflow definition not found: %s v%d", p.WorkflowID, p.WorkflowVersion)
		}
		return fmt.Errorf("failed to check workflow definition: %w", err)
	}
	if deletedAt.Valid {
		return fmt.Errorf("cannot start workflow: workflow %s v%d has been deleted", p.WorkflowID, p.WorkflowVersion)
	}

	runContext := r.coord.newRunContext(p.Input, p.ExecutionOptions)
	contextJSON, err := json.Marshal(runContext)
	if err != nil {
		logger.Error().Err(err).Msg("failed to marshal run context")
		util.RecordError(span, err)
		return fmt.Errorf("failed to marshal run context: %w", err)
	}

	status := "RUNNING"
	result, err := tx.Exec(ctx, `INSERT INTO workflow_runs (id, workflow_definition_id, workflow_version, status, input_json, context_json, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5::jsonb,$6::jsonb,NOW(),NOW()) ON CONFLICT (id) DO NOTHING`,
		p.RunID, p.WorkflowID, p.WorkflowVersion, status, p.Input, contextJSON)
	if err != nil {
		logger.Error().Err(err).Msg("failed to create workflow run")
		util.RecordError(span, err)
		return fmt.Errorf("failed to create workflow run: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("workflow run %s already exists (duplicate run_id)", p.RunID)
	}

	logger.Debug().Msg("created workflow run record")
	return nil
}

// GetWorkflowRunInfo gets workflow ID and version for a run
func (r *workflowRepository) GetWorkflowRunInfo(ctx context.Context, runID string) (workflowID string, workflowVersion int, err error) {
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	err = conn.QueryRow(ctx, `SELECT workflow_definition_id, workflow_version FROM workflow_runs WHERE id=$1`, runID).
		Scan(&workflowID, &workflowVersion)
	if err != nil {
		return "", 0, fmt.Errorf("failed to get workflow run info: %w", err)
	}
	return workflowID, workflowVersion, nil
}

// LoadRunContext loads the run context without updating it
func (r *workflowRepository) LoadRunContext(ctx context.Context, tx pgx.Tx, runID string, logger zerolog.Logger, span trace.Span) (*model.RunContext, error) {
	var workflowInput json.RawMessage
	var runContextJSON json.RawMessage
	if err := tx.QueryRow(ctx, `SELECT input_json, context_json FROM workflow_runs WHERE id=$1`, runID).
		Scan(&workflowInput, &runContextJSON); err != nil {
		logger.Error().Err(err).Msg("failed to load workflow run context")
		util.RecordError(span, err)
		return nil, fmt.Errorf("failed to load workflow run context: %w", err)
	}

	runContext, err := r.coord.mergeRunContext(workflowInput, runContextJSON)
	if err != nil {
		logger.Error().Err(err).Msg("failed to merge run context")
		util.RecordError(span, err)
		return nil, err
	}

	return runContext, nil
}

// UpdateRunContext updates the run context in the database
func (r *workflowRepository) UpdateRunContext(ctx context.Context, tx pgx.Tx, runID string, runContext *model.RunContext, logger zerolog.Logger, span trace.Span) error {
	runContextBytes, err := json.Marshal(runContext)
	if err != nil {
		logger.Error().Err(err).Msg("failed to marshal run context")
		util.RecordError(span, err)
		return fmt.Errorf("failed to marshal run context: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE workflow_runs SET context_json=$1, updated_at=now() WHERE id=$2`, runContextBytes, runID); err != nil {
		logger.Error().Err(err).Msg("failed to update workflow run context")
		util.RecordError(span, err)
		return fmt.Errorf("failed to update workflow run context: %w", err)
	}

	return nil
}

// LoadAndUpdateRunContext loads and updates the run context with a new node output
func (r *workflowRepository) LoadAndUpdateRunContext(ctx context.Context, tx pgx.Tx, runID, nodeID string, output json.RawMessage, logger zerolog.Logger, span trace.Span) (*model.RunContext, error) {
	var workflowInput json.RawMessage
	var runContextJSON json.RawMessage
	if err := tx.QueryRow(ctx, `SELECT input_json, context_json FROM workflow_runs WHERE id=$1 FOR UPDATE`, runID).
		Scan(&workflowInput, &runContextJSON); err != nil {
		logger.Error().Err(err).Msg("failed to load workflow run context")
		util.RecordError(span, err)
		return nil, fmt.Errorf("failed to load workflow run context: %w", err)
	}

	runContext, err := r.coord.mergeRunContext(workflowInput, runContextJSON)
	if err != nil {
		logger.Error().Err(err).Msg("failed to merge run context")
		util.RecordError(span, err)
		return nil, err
	}

	runContext.Tasks[nodeID] = &model.StepResult{
		Status: model.TaskInstanceStatusCompleted,
		Output: output,
	}

	return runContext, r.UpdateRunContext(ctx, tx, runID, runContext, logger, span)
}

// MarkWorkflowCompleted marks a workflow as completed
func (r *workflowRepository) MarkWorkflowCompleted(ctx context.Context, tx pgx.Tx, runID, workflowID string, span trace.Span, logger zerolog.Logger) error {
	// Get workflow start time for duration calculation
	var workflowStartTime time.Time
	_ = tx.QueryRow(ctx, `SELECT created_at FROM workflow_runs WHERE id=$1`, runID).Scan(&workflowStartTime)
	workflowDuration := time.Since(workflowStartTime)

	result, err := tx.Exec(ctx,
		`UPDATE workflow_runs 
             SET status='SUCCEEDED',
                 completed_at=now(),
                 updated_at=now() 
         WHERE id=$1 AND status='RUNNING'`,
		runID)
	if err != nil {
		util.RecordError(span, err)
		return fmt.Errorf("failed to mark workflow as completed: %w", err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return nil // Already completed or not found
	}

	util.RecordWorkflowCompleted(ctx, workflowID, runID, workflowDuration)
	return nil
}

// MarkWorkflowFailed marks a workflow as failed
func (r *workflowRepository) MarkWorkflowFailed(ctx context.Context, tx pgx.Tx, runID, workflowID, reason string, logger zerolog.Logger, span trace.Span) error {
	// Get workflow start time for duration calculation
	var workflowStartTime time.Time
	_ = tx.QueryRow(ctx, `SELECT created_at FROM workflow_runs WHERE id=$1`, runID).Scan(&workflowStartTime)
	workflowDuration := time.Since(workflowStartTime)

	errorJSON := json.RawMessage(fmt.Sprintf(`{"error": %q}`, reason))
	result, err := tx.Exec(ctx,
		`UPDATE workflow_runs 
             SET status='FAILED',
                 error_json=$1::jsonb,
                 completed_at=now(),
                 updated_at=now() 
         WHERE id=$2 AND status IN ('RUNNING', 'PENDING')`,
		errorJSON, runID)
	if err != nil {
		util.RecordError(span, err)
		return fmt.Errorf("failed to mark workflow as failed: %w", err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return nil // Already failed or not found
	}

	util.RecordWorkflowFailed(ctx, workflowID, runID, workflowDuration, reason)
	return nil
}

// CheckPendingTasks checks if there are pending tasks for a workflow run
func (r *workflowRepository) CheckPendingTasks(ctx context.Context, tx pgx.Tx, runID string) (bool, int, error) {
	var pendingCount int
	err := tx.QueryRow(ctx,
		`SELECT COUNT(1) FROM task_instances WHERE run_id=$1 AND state IN ('PENDING','RUNNING','QUEUED')`,
		runID).Scan(&pendingCount)
	if err != nil {
		return false, 0, fmt.Errorf("failed to check pending tasks: %w", err)
	}
	return pendingCount == 0, pendingCount, nil
}

// CheckFailedTasks returns the count of failed tasks for a workflow run
func (r *workflowRepository) CheckFailedTasks(ctx context.Context, tx pgx.Tx, runID string) (int, error) {
	var failedCount int
	err := tx.QueryRow(ctx,
		`SELECT COUNT(1) FROM task_instances WHERE run_id=$1 AND state = 'FAILED'`,
		runID).Scan(&failedCount)
	if err != nil {
		return 0, fmt.Errorf("failed to check failed tasks: %w", err)
	}
	return failedCount, nil
}

// InsertTaskInstance inserts a new task instance.
// Uses ON CONFLICT (run_id, node_id) to guarantee idempotency — duplicate inserts are silently skipped.
func (r *workflowRepository) InsertTaskInstance(ctx context.Context, tx pgx.Tx, taskID, runID string, node *model.Node, taskInput json.RawMessage, scheduledAt time.Time, logger zerolog.Logger, span trace.Span) error {
	nodeTypeForRouting := node.GetNodeTypeForRouting()
	result, err := tx.Exec(ctx, `INSERT INTO task_instances (id, run_id, node_id, node_type, state, attempt, input_json, scheduled_at, created_at, updated_at)
        VALUES ($1,$2,$3,$4,'PENDING',0,$5::jsonb,$6,NOW(),NOW()) ON CONFLICT (run_id, node_id) DO NOTHING`,
		taskID, runID, node.NodeID, nodeTypeForRouting, taskInput, scheduledAt)
	if err != nil {
		logger.Error().Err(err).Msg("failed to create task instance")
		util.RecordError(span, err)
		return fmt.Errorf("failed to create task instance: %w", err)
	}
	if result.RowsAffected() == 0 {
		logger.Debug().Str("run_id", runID).Str("node_id", node.NodeID).
			Msg("task instance already exists for this run/node, skipping (idempotent)")
	}
	return nil
}

// InsertTaskInstanceWithState inserts a task instance with a specific state
func (r *workflowRepository) InsertTaskInstanceWithState(ctx context.Context, tx pgx.Tx, taskID, runID string, node *model.Node, taskInput json.RawMessage, scheduledAt time.Time, state string, errorMsg string, logger zerolog.Logger, span trace.Span) error {
	var errorJSON json.RawMessage
	if errorMsg != "" {
		errorMap := map[string]string{"error": errorMsg}
		var err error
		errorJSON, err = json.Marshal(errorMap)
		if err != nil {
			logger.Error().Err(err).Msg("failed to marshal error message")
			errorJSON = json.RawMessage(fmt.Sprintf(`{"error": %q}`, errorMsg))
		}
	}

	finishedAt := sql.NullTime{}
	if state == "FAILED" || state == "COMPLETED" {
		finishedAt = sql.NullTime{Time: time.Now(), Valid: true}
	}

	nodeTypeForRouting := node.GetNodeTypeForRouting()
	_, err := tx.Exec(ctx, `INSERT INTO task_instances (id, run_id, node_id, node_type, state, attempt, input_json, last_error, scheduled_at, finished_at, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,0,$6::jsonb,$7::jsonb,$8,$9,NOW(),NOW()) ON CONFLICT (run_id, node_id) DO NOTHING`,
		taskID, runID, node.NodeID, nodeTypeForRouting, state, taskInput, errorJSON, scheduledAt, finishedAt)
	if err != nil {
		logger.Error().Err(err).Msg("failed to create task instance with state")
		util.RecordError(span, err)
		return fmt.Errorf("failed to create task instance with state: %w", err)
	}
	return nil
}

// MarkTaskCompleted marks a task instance as completed and returns its identity
// and start time for duration measurement. startedAt will be zero if the task
// was never started (e.g. control nodes persisted directly as COMPLETED).
func (r *workflowRepository) MarkTaskCompleted(ctx context.Context, tx pgx.Tx, runID, nodeID string, output json.RawMessage, logger zerolog.Logger, span trace.Span) (updated bool, taskID string, nodeType string, startedAt time.Time, err error) {
	var rawStartedAt sql.NullTime
	err = tx.QueryRow(ctx,
		`UPDATE task_instances
             SET state='COMPLETED',
                 output_json=$1::jsonb,
                 completed_at=now(),
                 updated_at=now()
         WHERE run_id=$2 AND node_id=$3 AND state IN ('RUNNING', 'PENDING')
         RETURNING id, node_type, started_at`,
		output, runID, nodeID).Scan(&taskID, &nodeType, &rawStartedAt)
	if err == pgx.ErrNoRows {
		return false, "", "", time.Time{}, nil
	}
	if err != nil {
		return false, "", "", time.Time{}, fmt.Errorf("failed to update task instance: %w", err)
	}
	logger.Debug().Int64("rows_updated", 1).Msg("marked task completed")
	if rawStartedAt.Valid {
		startedAt = rawStartedAt.Time
	}
	return true, taskID, nodeType, startedAt, nil
}

// MarkTaskFailed marks a task instance as failed
func (r *workflowRepository) MarkTaskFailed(ctx context.Context, tx pgx.Tx, runID, nodeID, errorMsg string, logger zerolog.Logger, span trace.Span) (bool, error) {
	errorJSON := json.RawMessage(fmt.Sprintf(`{"error": %q}`, errorMsg))
	result, err := tx.Exec(ctx,
		`UPDATE task_instances 
             SET state='FAILED',
                 last_error=$1::jsonb,
                 finished_at=now(),
                 updated_at=now() 
         WHERE run_id=$2 AND node_id=$3 AND state IN ('RUNNING', 'PENDING', 'QUEUED')`,
		errorJSON, runID, nodeID)
	if err != nil {
		return false, fmt.Errorf("failed to update task instance: %w", err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return false, nil
	}
	return true, nil
}

// TaskInstanceExists checks if a task instance exists and returns its state
func (r *workflowRepository) TaskInstanceExists(ctx context.Context, tx pgx.Tx, runID, nodeID string) (bool, string, string, error) {
	var existingTaskID string
	var existingState string
	err := tx.QueryRow(ctx,
		`SELECT id, state FROM task_instances WHERE run_id=$1 AND node_id=$2`,
		runID, nodeID).Scan(&existingTaskID, &existingState)
	if err == pgx.ErrNoRows {
		return false, "", "", nil
	}
	if err != nil {
		return false, "", "", fmt.Errorf("failed to check existing task instance: %w", err)
	}

	if existingTaskID != "" {
		return true, existingTaskID, existingState, nil
	}
	return false, "", "", nil
}

// GetTaskInstanceInfo gets task instance information
func (r *workflowRepository) GetTaskInstanceInfo(ctx context.Context, tx pgx.Tx, runID, nodeID string) (taskID string, nodeType string, taskStartTime time.Time, err error) {
	err = tx.QueryRow(ctx,
		`SELECT id, node_type, started_at FROM task_instances WHERE run_id=$1 AND node_id=$2`,
		runID, nodeID).Scan(&taskID, &nodeType, &taskStartTime)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("failed to get task instance info: %w", err)
	}
	return taskID, nodeType, taskStartTime, nil
}

// ClearTaskLease clears the lease for a task instance
func (r *workflowRepository) ClearTaskLease(ctx context.Context, tx pgx.Tx, runID, nodeID string, logger zerolog.Logger, span trace.Span) error {
	_, err := tx.Exec(ctx,
		`UPDATE task_instances 
         SET lease_owner=NULL,
             lease_expires_at=NULL,
             updated_at=now()
       WHERE run_id=$1 AND node_id=$2`,
		runID, nodeID)
	if err != nil {
		logger.Error().Err(err).Msg("failed to clear task lease")
		util.RecordError(span, err)
		return fmt.Errorf("failed to clear task lease: %w", err)
	}
	return nil
}

// InsertTaskOutbox inserts a task into the outbox
func (r *workflowRepository) InsertTaskOutbox(ctx context.Context, tx pgx.Tx, taskID, runID string, node *model.Node, taskInput json.RawMessage, logger zerolog.Logger, span trace.Span) error {
	outboxID := uuid.New().String()
	nodeTypeForRouting := node.GetNodeTypeForRouting()
	outboxPayload := map[string]interface{}{
		"task_id":   taskID,
		"run_id":    runID,
		"node_id":   node.NodeID,
		"node_type": nodeTypeForRouting,
		"input":     string(taskInput),
	}
	outboxPayloadBytes, err := json.Marshal(outboxPayload)
	if err != nil {
		logger.Error().Err(err).Msg("failed to marshal outbox payload")
		return fmt.Errorf("failed to marshal outbox payload: %w", err)
	}

	_, err = tx.Exec(ctx, `INSERT INTO outbox (id, stream, payload, type, status, next_attempt_at, attempt_count, max_retries, created_at, updated_at)
        VALUES ($1,'workflow.tasks',$2::jsonb,'TASK_EXECUTION','PENDING',NOW(),0,5,NOW(),NOW())`,
		outboxID, outboxPayloadBytes)
	if err != nil {
		logger.Error().Err(err).Msg("failed to create outbox entry")
		util.RecordError(span, err)
		return fmt.Errorf("failed to create outbox entry: %w", err)
	}

	logger.Debug().Str("outbox_id", outboxID).Msg("created outbox entry for task")
	return nil
}

// CheckParallelBranchesComplete checks if all parallel branches are complete.
// Uses FOR UPDATE to serialize concurrent join checks and prevent duplicate join node creation.
func (r *workflowRepository) CheckParallelBranchesComplete(ctx context.Context, tx pgx.Tx, runID string, branchNodeIDs []string) (completedCount, totalCount int, err error) {
	err = tx.QueryRow(ctx,
		`SELECT
			COUNT(*) FILTER (WHERE state IN ('COMPLETED', 'SUCCEEDED')) as completed_count,
			COUNT(*) as total_count
		FROM (
			SELECT id, state FROM task_instances
			WHERE run_id = $1 AND node_id = ANY($2)
			FOR UPDATE
		) locked`,
		runID, branchNodeIDs).Scan(&completedCount, &totalCount)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to check parallel branch status: %w", err)
	}
	return completedCount, totalCount, nil
}

// CountCompletedBranches counts how many of the given branch nodes are in a completed state.
// Uses FOR UPDATE to serialize concurrent join checks for the same run.
func (r *workflowRepository) CountCompletedBranches(ctx context.Context, tx pgx.Tx, runID string, branchNodeIDs []string) (int, error) {
	var count int
	err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FILTER (WHERE state IN ('COMPLETED', 'SUCCEEDED'))
		 FROM (
			SELECT id, state FROM task_instances
			WHERE run_id = $1 AND node_id = ANY($2)
			FOR UPDATE
		 ) locked`,
		runID, branchNodeIDs).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count completed branches: %w", err)
	}
	return count, nil
}

// GetWorkflowRunStatus gets the current status of a workflow run
func (r *workflowRepository) GetWorkflowRunStatus(ctx context.Context, tx pgx.Tx, runID string) (status string, err error) {
	err = tx.QueryRow(ctx, `SELECT status FROM workflow_runs WHERE id=$1`, runID).Scan(&status)
	if err != nil {
		return "", fmt.Errorf("failed to get workflow run status: %w", err)
	}
	return status, nil
}

// GetWorkflowRunForRetry gets workflow run information for retry operations
func (r *workflowRepository) GetWorkflowRunForRetry(ctx context.Context, runID string) (workflowID string, workflowVersion int, status string, err error) {
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return "", 0, "", fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	err = conn.QueryRow(ctx,
		`SELECT workflow_definition_id, workflow_version, status FROM workflow_runs WHERE id=$1 AND deleted_at IS NULL`,
		runID).Scan(&workflowID, &workflowVersion, &status)
	if err != nil {
		return "", 0, "", fmt.Errorf("failed to get workflow run for retry: %w", err)
	}
	return workflowID, workflowVersion, status, nil
}

// UpdateWorkflowRunStatus updates workflow run status and version
func (r *workflowRepository) UpdateWorkflowRunStatus(ctx context.Context, tx pgx.Tx, runID string, status string, workflowVersion int) error {
	_, err := tx.Exec(ctx,
		`UPDATE workflow_runs 
		 SET status=$1, 
		     workflow_version=$2,
		     completed_at=NULL,
		     updated_at=now() 
		 WHERE id=$3`,
		status, workflowVersion, runID)
	if err != nil {
		return fmt.Errorf("failed to update workflow run status: %w", err)
	}
	return nil
}

// UpdateWorkflowInput updates the input for a workflow run
func (r *workflowRepository) UpdateWorkflowInput(ctx context.Context, tx pgx.Tx, runID string, input json.RawMessage) error {
	_, err := tx.Exec(ctx, `UPDATE workflow_runs SET input_json=$1::jsonb WHERE id=$2`, input, runID)
	if err != nil {
		return fmt.Errorf("failed to update workflow input: %w", err)
	}
	return nil
}

// DeleteTaskInstances deletes task instances for given node IDs
func (r *workflowRepository) DeleteTaskInstances(ctx context.Context, tx pgx.Tx, runID string, nodeIDs []string, logger zerolog.Logger, span trace.Span) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx,
		`DELETE FROM task_instances WHERE run_id=$1 AND node_id = ANY($2)`,
		runID, nodeIDs)
	if err != nil {
		logger.Error().Err(err).Msg("failed to delete task instances")
		util.RecordError(span, err)
		return fmt.Errorf("failed to delete task instances: %w", err)
	}
	return nil
}

// RecoverOrphanedTasks resets expired RUNNING tasks to PENDING and creates outbox entries for
// any PENDING task that has no active outbox row. Called once on coordinator startup.
func (r *workflowRepository) RecoverOrphanedTasks(ctx context.Context, logger zerolog.Logger) error {
	// Step 1: Reset RUNNING tasks with expired leases back to PENDING.
	// These belong to workers that died or a previous coordinator crash.
	result, err := r.db.Exec(ctx,
		`UPDATE task_instances
		 SET state='PENDING', lease_owner=NULL, lease_expires_at=NULL, started_at=NULL, updated_at=NOW()
		 WHERE state='RUNNING' AND (lease_expires_at IS NULL OR lease_expires_at < NOW())`)
	if err != nil {
		return fmt.Errorf("failed to reset expired running tasks: %w", err)
	}
	if n := result.RowsAffected(); n > 0 {
		logger.Warn().Int64("count", n).Msg("reset expired RUNNING tasks to PENDING for recovery")
	}

	// Step 2: Find PENDING tasks whose outbox entry was never created or was already consumed
	// (coordinator crashed between task creation and outbox insert — they're in the same tx,
	// so this mainly covers reset tasks from step 1 above).
	rows, err := r.db.Query(ctx,
		`SELECT ti.id, ti.run_id, ti.node_id, ti.node_type, ti.input_json
		 FROM task_instances ti
		 WHERE ti.state = 'PENDING'
		 AND NOT EXISTS (
			 SELECT 1 FROM outbox o
			 WHERE o.payload->>'task_id' = ti.id
			   AND o.status IN ('PENDING','RETRY','IN_FLIGHT')
		 )`)
	if err != nil {
		return fmt.Errorf("failed to query orphaned pending tasks: %w", err)
	}
	defer rows.Close()

	type orphanRow struct {
		taskID   string
		runID    string
		nodeID   string
		nodeType string
		input    json.RawMessage
	}

	var orphans []orphanRow
	for rows.Next() {
		var o orphanRow
		if err := rows.Scan(&o.taskID, &o.runID, &o.nodeID, &o.nodeType, &o.input); err != nil {
			return fmt.Errorf("failed to scan orphaned task row: %w", err)
		}
		orphans = append(orphans, o)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating orphaned tasks: %w", err)
	}

	if len(orphans) == 0 {
		logger.Info().Msg("startup recovery: no orphaned tasks found")
		return nil
	}

	logger.Warn().Int("count", len(orphans)).Msg("startup recovery: re-queuing orphaned tasks")

	for _, o := range orphans {
		payload := map[string]interface{}{
			"task_id":   o.taskID,
			"run_id":    o.runID,
			"node_id":   o.nodeID,
			"node_type": o.nodeType,
			"input":     string(o.input),
		}
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			logger.Error().Err(err).Str("task_id", o.taskID).Msg("recovery: failed to marshal outbox payload")
			continue
		}
		outboxID := uuid.New().String()
		_, err = r.db.Exec(ctx,
			`INSERT INTO outbox (id, stream, payload, type, status, next_attempt_at, attempt_count, max_retries, created_at, updated_at)
			 VALUES ($1,'workflow.tasks',$2::jsonb,'TASK_EXECUTION','PENDING',NOW(),0,5,NOW(),NOW())`,
			outboxID, payloadBytes)
		if err != nil {
			logger.Error().Err(err).Str("task_id", o.taskID).Msg("recovery: failed to create outbox entry")
			continue
		}
		logger.Info().Str("task_id", o.taskID).Str("run_id", o.runID).Str("node_id", o.nodeID).Msg("recovery: re-queued orphaned task")
	}

	return nil
}
