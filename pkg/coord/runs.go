package coord

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/mohitkumar/composer/pkg/model"
)

// runColumns are the workflow_runs columns read by scanRun.
const runColumns = `id, workflow_definition_id, workflow_version, status, input_json, created_at, updated_at, completed_at`

func scanRun(row pgx.Row) (*model.WorkflowInstanceRecord, error) {
	return scanRunWith(row)
}

// GetWorkflowInstance returns a workflow run that is not deleted.
func (c *Coordinator) GetWorkflowInstance(ctx context.Context, runID string) (*model.WorkflowInstanceRecord, error) {
	return scanRun(c.DB.QueryRow(ctx,
		`SELECT `+runColumns+` FROM workflow_runs WHERE id = $1 AND deleted_at IS NULL`, runID))
}

// GetExecutionDetails returns a run with its context and every task instance.
// The context's Tasks are rebuilt from the completed tasks' outputs.
func (c *Coordinator) GetExecutionDetails(ctx context.Context, runID string) (*model.ExecutionDetailResponse, error) {
	var contextJSON []byte
	run, err := scanRunWith(c.DB.QueryRow(ctx,
		`SELECT `+runColumns+`, context_json FROM workflow_runs WHERE id = $1 AND deleted_at IS NULL`, runID),
		&contextJSON)
	if err != nil {
		return nil, err
	}
	rc, err := model.DecodeRunContext(run.Input, contextJSON)
	if err != nil {
		rc = nil // a malformed context should not hide the run
	}

	rows, err := c.DB.Query(ctx, `
		SELECT id, run_id, node_id, node_type, state, attempt, input_json, output_json, last_error,
		       scheduled_at, started_at, finished_at, completed_at, created_at, updated_at,
		       lease_owner, lease_expires_at
		  FROM task_instances
		 WHERE run_id = $1
		 ORDER BY created_at, id`, runID)
	if err != nil {
		return nil, fmt.Errorf("query task instances: %w", err)
	}
	tasks, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.TaskInstanceRecord, error) {
		var t model.TaskInstanceRecord
		err := row.Scan(&t.ID, &t.RunID, &t.NodeID, &t.NodeType, &t.State, &t.Attempt,
			&t.Input, &t.Output, &t.LastError,
			&t.ScheduledAt, &t.StartedAt, &t.FinishedAt, &t.CompletedAt, &t.CreatedAt, &t.UpdatedAt,
			&t.LeaseOwner, &t.LeaseExpiresAt)
		return t, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan task instances: %w", err)
	}

	if rc != nil {
		for _, t := range tasks {
			if t.State.IsCompleted() {
				rc.Tasks[t.NodeID] = &model.StepResult{Status: t.State, Output: t.Output}
			}
		}
	}
	return &model.ExecutionDetailResponse{Execution: run, Context: rc, TaskInstances: tasks}, nil
}

// scanRunWith scans runColumns followed by extra destinations.
func scanRunWith(row pgx.Row, extra ...any) (*model.WorkflowInstanceRecord, error) {
	var r model.WorkflowInstanceRecord
	dest := append([]any{&r.ID, &r.WorkflowDefinitionID, &r.WorkflowVersion, &r.Status,
		&r.Input, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: workflow run", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("scan workflow run: %w", err)
	}
	return &r, nil
}

// ListWorkflowInstancesResponse represents a paginated list of workflow instances
type ListWorkflowInstancesResponse struct {
	Executions []model.WorkflowInstanceRecord `json:"executions"`
	Total      int                            `json:"total"`
	Page       int                            `json:"page"`
	PageSize   int                            `json:"page_size"`
	TotalPages int                            `json:"total_pages"`
}

// ListWorkflowInstances lists runs, newest first, filtered by workflow,
// status and a search on run or workflow ID. Deleted runs are excluded.
func (c *Coordinator) ListWorkflowInstances(ctx context.Context, workflowID *string,
	status *model.WorkflowInstanceStatus, search string, pageNum, pageSize int) (*ListWorkflowInstancesResponse, error) {

	pg := newPage(pageNum, pageSize, 20)
	var f filter
	f.add("deleted_at IS NULL")
	if workflowID != nil {
		f.add("workflow_definition_id = ?", *workflowID)
	}
	if status != nil {
		f.add("status = ?", string(*status))
	}
	if search != "" {
		f.add("(id ILIKE ? OR workflow_definition_id ILIKE ?)", "%"+search+"%", "%"+search+"%")
	}

	var total int
	if err := c.DB.QueryRow(ctx, `SELECT COUNT(*) FROM workflow_runs`+f.where(), f.args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count workflow runs: %w", err)
	}

	rows, err := c.DB.Query(ctx,
		`SELECT `+runColumns+` FROM workflow_runs`+f.where()+` ORDER BY created_at DESC`+f.limit(pg), f.args...)
	if err != nil {
		return nil, fmt.Errorf("query workflow runs: %w", err)
	}
	runs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.WorkflowInstanceRecord, error) {
		r, err := scanRun(row)
		if err != nil {
			return model.WorkflowInstanceRecord{}, err
		}
		return *r, nil
	})
	if err != nil {
		return nil, err
	}

	return &ListWorkflowInstancesResponse{
		Executions: runs,
		Total:      total,
		Page:       pg.Number,
		PageSize:   pg.Size,
		TotalPages: pg.totalPages(total),
	}, nil
}

// GetWorkflowInstanceStatus returns a run's status with task counts per state.
func (c *Coordinator) GetWorkflowInstanceStatus(ctx context.Context, runID string) (*model.WorkflowInstanceStatusResponse, error) {
	var s model.WorkflowInstanceStatusResponse
	err := c.DB.QueryRow(ctx,
		`SELECT id, status, created_at, updated_at FROM workflow_runs WHERE id = $1 AND deleted_at IS NULL`,
		runID).Scan(&s.ID, &s.Status, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: workflow run %s", ErrNotFound, runID)
	}
	if err != nil {
		return nil, fmt.Errorf("query workflow run: %w", err)
	}

	err = c.DB.QueryRow(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE state IN ('PENDING', 'QUEUED')),
		       COUNT(*) FILTER (WHERE state = 'RUNNING'),
		       COUNT(*) FILTER (WHERE state IN ('COMPLETED', 'SUCCEEDED')),
		       COUNT(*) FILTER (WHERE state = 'FAILED'),
		       COUNT(*) FILTER (WHERE state = 'WAITING'),
		       COUNT(*) FILTER (WHERE state = 'SKIPPED')
		  FROM task_instances WHERE run_id = $1`,
		runID).Scan(&s.TotalTasks, &s.PendingTasks, &s.RunningTasks, &s.CompletedTasks,
		&s.FailedTasks, &s.WaitingTasks, &s.SkippedTasks)
	if err != nil {
		return nil, fmt.Errorf("count run tasks: %w", err)
	}
	return &s, nil
}
