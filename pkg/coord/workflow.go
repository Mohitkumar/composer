package coord

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/util"
)

// RegisterWorkflow registers a new workflow definition or creates a new version.
func (c *Coordinator) RegisterWorkflow(ctx context.Context, p *model.RegisterWorkflowRequest) error {
	ctx, span := util.StartSpan(ctx, "workflow.register",
		util.Attribute("workflow.id", p.WorkflowID),
		util.AttributeInt("workflow.version", int64(p.Version)),
	)
	defer util.EndSpan(span, nil)

	logger := c.Logger.With().
		Str("workflow_id", p.WorkflowID).
		Str("name", p.Name).
		Int("version", p.Version).
		Str("created_by", p.CreatedBy).
		Logger()

	logger.Info().Msg("registering workflow definition")

	tx, err := c.DB.Begin(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("failed to begin transaction")
		util.RecordError(span, err)
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `INSERT INTO workflow_definitions (id, name, version, spec_json, created_by, created_at)
        VALUES ($1,$2,$3,$4::jsonb,$5,NOW()) ON CONFLICT (id, version) DO NOTHING`,
		p.WorkflowID, p.Name, p.Version, p.Spec, sql.NullString{String: p.CreatedBy, Valid: p.CreatedBy != ""})
	if err != nil {
		logger.Error().Err(err).Msg("failed to insert workflow definition")
		util.RecordError(span, err)
		return err
	}

	logger.Info().Msg("workflow definition registered successfully")
	return tx.Commit(ctx)
}

// GetWorkflow retrieves a workflow definition by ID and version.
// If version is 0, returns the latest version.
func (c *Coordinator) GetWorkflow(ctx context.Context, workflowID string, version int) (*model.WorkflowDefinitionRecord, error) {
	var wf model.WorkflowDefinitionRecord
	var webhookURL sql.NullString

	query := `SELECT id, name, version, spec_json, created_by, enabled, webhook_url, created_at 
	          FROM workflow_definitions WHERE id = $1`
	args := []interface{}{workflowID}

	if version > 0 {
		query += ` AND version = $2`
		args = append(args, version)
	} else {
		query += ` ORDER BY version DESC LIMIT 1`
	}

	err := c.DB.QueryRow(ctx, query, args...).Scan(
		&wf.ID, &wf.Name, &wf.Version, &wf.Spec, &wf.CreatedBy, &wf.Enabled, &webhookURL, &wf.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("workflow not found")
		}
		return nil, err
	}

	if webhookURL.Valid {
		wf.WebhookURL = webhookURL
	}

	return &wf, nil
}

// ListWorkflows lists all workflow definitions, optionally filtered by enabled status.
func (c *Coordinator) ListWorkflows(ctx context.Context, enabledOnly bool) ([]model.WorkflowDefinitionRecord, error) {
	query := `SELECT id, name, version, spec_json, created_by, enabled, webhook_url, created_at 
	          FROM workflow_definitions`
	args := []interface{}{}

	if enabledOnly {
		query += ` WHERE enabled = true`
	}

	query += ` ORDER BY id, version DESC`

	rows, err := c.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workflows []model.WorkflowDefinitionRecord
	for rows.Next() {
		var wf model.WorkflowDefinitionRecord
		var webhookURL sql.NullString

		if err := rows.Scan(&wf.ID, &wf.Name, &wf.Version, &wf.Spec, &wf.CreatedBy,
			&wf.Enabled, &webhookURL, &wf.CreatedAt); err != nil {
			return nil, err
		}

		if webhookURL.Valid {
			wf.WebhookURL = webhookURL
		}

		workflows = append(workflows, wf)
	}

	return workflows, rows.Err()
}

// UpdateWorkflow updates a workflow definition's metadata (name, enabled, webhook_url).
// The spec cannot be updated; create a new version instead.
func (c *Coordinator) UpdateWorkflow(ctx context.Context, workflowID string, version int,
	name *string, enabled *bool, webhookURL *string) error {
	tx, err := c.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	updates := []string{}
	args := []interface{}{}
	argPos := 1

	if name != nil {
		updates = append(updates, "name = $"+strconv.Itoa(argPos))
		args = append(args, *name)
		argPos++
	}

	if enabled != nil {
		updates = append(updates, "enabled = $"+strconv.Itoa(argPos))
		args = append(args, *enabled)
		argPos++
	}

	if webhookURL != nil {
		updates = append(updates, "webhook_url = $"+strconv.Itoa(argPos))
		args = append(args, sql.NullString{String: *webhookURL, Valid: *webhookURL != ""})
		argPos++
	}

	if len(updates) == 0 {
		return errors.New("no fields to update")
	}

	// Build the query with proper parameter placeholders
	query := `UPDATE workflow_definitions SET ` + updates[0]
	for i := 1; i < len(updates); i++ {
		query += `, ` + updates[i]
	}
	query += fmt.Sprintf(` WHERE id = $%d AND version = $%d`, argPos, argPos+1)

	args = append(args, workflowID, version)

	result, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return err
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return errors.New("workflow not found")
	}

	return tx.Commit(ctx)
}

// DeleteWorkflow disables a workflow definition (soft delete).
// Set hardDelete to true to actually delete the workflow.
func (c *Coordinator) DeleteWorkflow(ctx context.Context, workflowID string, version int, hardDelete bool) error {
	tx, err := c.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if hardDelete {
		_, err = tx.Exec(ctx, `DELETE FROM workflow_definitions WHERE id = $1 AND version = $2`,
			workflowID, version)
	} else {
		_, err = tx.Exec(ctx, `UPDATE workflow_definitions SET enabled = false WHERE id = $1 AND version = $2`,
			workflowID, version)
	}

	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// GetWorkflowInstance retrieves a workflow instance (run) by ID.
func (c *Coordinator) GetWorkflowInstance(ctx context.Context, instanceID string) (*model.WorkflowInstanceRecord, error) {
	var inst model.WorkflowInstanceRecord
	var completedAt sql.NullTime

	err := c.DB.QueryRow(ctx,
		`SELECT id, workflow_definition_id, workflow_version, status, input_json, created_at, updated_at, completed_at 
		 FROM workflow_runs WHERE id = $1`,
		instanceID).Scan(
		&inst.ID, &inst.WorkflowDefinitionID, &inst.WorkflowVersion, &inst.Status,
		&inst.Input, &inst.CreatedAt, &inst.UpdatedAt, &completedAt)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("workflow instance not found")
		}
		return nil, err
	}

	if completedAt.Valid {
		inst.CompletedAt = &completedAt.Time
	}

	return &inst, nil
}

// GetExecutionDetails retrieves comprehensive execution details including context and task instances
func (c *Coordinator) GetExecutionDetails(ctx context.Context, instanceID string) (*model.ExecutionDetailResponse, error) {
	// Get execution
	execution, err := c.GetWorkflowInstance(ctx, instanceID)
	if err != nil {
		return nil, err
	}

	// Get context
	var contextJSON json.RawMessage
	err = c.DB.QueryRow(ctx,
		`SELECT context_json FROM workflow_runs WHERE id = $1`,
		instanceID).Scan(&contextJSON)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed to load context: %w", err)
	}

	var runContext *model.RunContext
	if len(contextJSON) > 0 {
		runContext = &model.RunContext{}
		if err := json.Unmarshal(contextJSON, runContext); err != nil {
			// If context is invalid JSON, just skip it
			runContext = nil
		}
	}

	// Get task instances
	rows, err := c.DB.Query(ctx,
		`SELECT id, run_id, node_id, node_type, state, attempt, input_json, output_json, last_error,
		        scheduled_at, started_at, finished_at, completed_at, created_at, updated_at,
		        lease_owner, lease_expires_at
		 FROM task_instances WHERE run_id = $1 ORDER BY created_at ASC`,
		instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to query task instances: %w", err)
	}
	defer rows.Close()

	var taskInstances []model.TaskInstanceRecord
	for rows.Next() {
		var task model.TaskInstanceRecord
		var scheduledAt, startedAt, finishedAt, completedAt, leaseExpiresAt sql.NullTime
		var leaseOwner sql.NullString

		err := rows.Scan(
			&task.ID, &task.RunID, &task.NodeID, &task.NodeType, &task.State, &task.Attempt,
			&task.Input, &task.Output, &task.LastError,
			&scheduledAt, &startedAt, &finishedAt, &completedAt,
			&task.CreatedAt, &task.UpdatedAt,
			&leaseOwner, &leaseExpiresAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan task instance: %w", err)
		}

		if scheduledAt.Valid {
			task.ScheduledAt = &scheduledAt.Time
		}
		if startedAt.Valid {
			task.StartedAt = &startedAt.Time
		}
		if finishedAt.Valid {
			task.FinishedAt = &finishedAt.Time
		}
		if completedAt.Valid {
			task.CompletedAt = &completedAt.Time
		}
		if leaseOwner.Valid {
			task.LeaseOwner = &leaseOwner.String
		}
		if leaseExpiresAt.Valid {
			task.LeaseExpiresAt = &leaseExpiresAt.Time
		}

		taskInstances = append(taskInstances, task)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating task instances: %w", err)
	}

	return &model.ExecutionDetailResponse{
		Execution:     execution,
		Context:       runContext,
		TaskInstances: taskInstances,
	}, nil
}

// ListWorkflowInstances lists workflow instances, optionally filtered by workflow ID and status.
func (c *Coordinator) ListWorkflowInstances(ctx context.Context, workflowID *string,
	status *model.WorkflowInstanceStatus, limit, offset int) ([]model.WorkflowInstanceRecord, error) {
	query := `SELECT id, workflow_definition_id, workflow_version, status, input_json, created_at, updated_at, completed_at 
	          FROM workflow_runs WHERE 1=1`
	args := []interface{}{}
	argPos := 1

	if workflowID != nil {
		query += fmt.Sprintf(` AND workflow_definition_id = $%d`, argPos)
		args = append(args, *workflowID)
		argPos++
	}

	if status != nil {
		query += fmt.Sprintf(` AND status = $%d`, argPos)
		args = append(args, string(*status))
		argPos++
	}

	query += ` ORDER BY created_at DESC`

	if limit > 0 {
		query += fmt.Sprintf(` LIMIT $%d`, argPos)
		args = append(args, limit)
		argPos++
		if offset > 0 {
			query += fmt.Sprintf(` OFFSET $%d`, argPos)
			args = append(args, offset)
		}
	}

	rows, err := c.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var instances []model.WorkflowInstanceRecord
	for rows.Next() {
		var inst model.WorkflowInstanceRecord
		var completedAt sql.NullTime
		if err := rows.Scan(&inst.ID, &inst.WorkflowDefinitionID, &inst.WorkflowVersion,
			&inst.Status, &inst.Input, &inst.CreatedAt, &inst.UpdatedAt, &completedAt); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			inst.CompletedAt = &completedAt.Time
		}
		instances = append(instances, inst)
	}

	return instances, rows.Err()
}

// GetWorkflowInstanceStatus retrieves detailed status information for a workflow instance.
func (c *Coordinator) GetWorkflowInstanceStatus(ctx context.Context, instanceID string) (*model.WorkflowInstanceStatusResponse, error) {
	var status model.WorkflowInstanceStatusResponse

	// Get instance basic info
	err := c.DB.QueryRow(ctx,
		`SELECT id, status, created_at, updated_at FROM workflow_runs WHERE id = $1`,
		instanceID).Scan(&status.ID, &status.Status, &status.CreatedAt, &status.UpdatedAt)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("workflow instance not found")
		}
		return nil, err
	}

	// Get task counts
	err = c.DB.QueryRow(ctx,
		`SELECT 
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE state IN ('PENDING', 'QUEUED')) as pending,
			COUNT(*) FILTER (WHERE state = 'RUNNING') as running,
			COUNT(*) FILTER (WHERE state = 'SUCCEEDED' OR state = 'COMPLETED') as completed,
			COUNT(*) FILTER (WHERE state = 'FAILED') as failed
		 FROM task_instances WHERE run_id = $1`,
		instanceID).Scan(
		&status.TotalTasks, &status.PendingTasks, &status.RunningTasks,
		&status.CompletedTasks, &status.FailedTasks)

	if err != nil {
		return nil, err
	}

	return &status, nil
}

// CancelWorkflowInstance cancels a running workflow instance by setting its status to FAILED.
func (c *Coordinator) CancelWorkflowInstance(ctx context.Context, instanceID string, reason string) error {
	tx, err := c.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Update workflow run status
	result, err := tx.Exec(ctx,
		`UPDATE workflow_runs SET status = 'FAILED', updated_at = NOW() 
		 WHERE id = $1 AND status IN ('RUNNING', 'PENDING')`,
		instanceID)

	if err != nil {
		return err
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return errors.New("workflow instance not found or not in cancellable state")
	}

	// Cancel all pending/running tasks
	_, err = tx.Exec(ctx,
		`UPDATE task_instances SET state = 'FAILED', updated_at = NOW() 
		 WHERE run_id = $1 AND state IN ('PENDING', 'QUEUED', 'RUNNING')`,
		instanceID)

	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}
