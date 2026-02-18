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
// If the specified version exists and is not deployed, it updates it in place.
// If the version is deployed or doesn't exist, it creates a new version.
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

	tx, err := c.DB.Begin(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("failed to begin transaction")
		util.RecordError(span, err)
		return err
	}
	defer tx.Rollback(ctx)

	// Try to update existing version if it exists and is not deployed
	result, err := tx.Exec(ctx, `UPDATE workflow_definitions 
		SET name = $1, spec_json = $2::jsonb, created_by = $3, created_at = NOW()
		WHERE id = $4 AND version = $5 AND deleted_at IS NULL AND deployed = false`,
		p.Name, p.Spec, sql.NullString{String: p.CreatedBy, Valid: p.CreatedBy != ""}, p.WorkflowID, p.Version)

	if err != nil {
		logger.Error().Err(err).Msg("failed to update workflow definition")
		util.RecordError(span, err)
		return err
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected > 0 {
		// Successfully updated existing undeployed version
		logger.Info().Int("version", p.Version).Msg("updated existing undeployed version")
	} else {
		// No rows updated - either version doesn't exist or it's deployed
		// Check if version exists and is deployed
		var deployed bool
		err = tx.QueryRow(ctx,
			`SELECT deployed FROM workflow_definitions WHERE id = $1 AND version = $2 AND deleted_at IS NULL`,
			p.WorkflowID, p.Version).Scan(&deployed)

		if err == nil {
			// Version exists and is deployed, create new version
			if deployed {
				var latestVersion int
				err = tx.QueryRow(ctx,
					`SELECT COALESCE(MAX(version), 0) FROM workflow_definitions WHERE id = $1 AND deleted_at IS NULL`,
					p.WorkflowID).Scan(&latestVersion)
				if err != nil {
					logger.Error().Err(err).Msg("failed to get latest version")
					util.RecordError(span, err)
					return err
				}
				nextVersion := latestVersion + 1
				logger.Info().
					Int("current_version", p.Version).
					Int("next_version", nextVersion).
					Msg("version is deployed, creating new version")

				_, err = tx.Exec(ctx, `INSERT INTO workflow_definitions (id, name, version, spec_json, created_by, created_at, deployed)
					VALUES ($1,$2,$3,$4::jsonb,$5,NOW(),false)`,
					p.WorkflowID, p.Name, nextVersion, p.Spec, sql.NullString{String: p.CreatedBy, Valid: p.CreatedBy != ""})
				if err != nil {
					logger.Error().Err(err).Msg("failed to insert new workflow version")
					util.RecordError(span, err)
					return err
				}
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			// Version doesn't exist, create it
			logger.Info().Int("version", p.Version).Msg("creating new workflow version")
			_, err = tx.Exec(ctx, `INSERT INTO workflow_definitions (id, name, version, spec_json, created_by, created_at, deployed)
				VALUES ($1,$2,$3,$4::jsonb,$5,NOW(),false)
				ON CONFLICT (id, version) DO NOTHING`,
				p.WorkflowID, p.Name, p.Version, p.Spec, sql.NullString{String: p.CreatedBy, Valid: p.CreatedBy != ""})
			if err != nil {
				logger.Error().Err(err).Msg("failed to insert workflow definition")
				util.RecordError(span, err)
				return err
			}
		} else {
			// Database error
			logger.Error().Err(err).Msg("failed to check existing workflow version")
			util.RecordError(span, err)
			return err
		}
	}

	logger.Info().Msg("workflow definition registered successfully")
	return tx.Commit(ctx)
}

// GetWorkflow retrieves a workflow definition by ID and version.
// If version is 0, returns the deployed version (or latest if no deployed version exists).
func (c *Coordinator) GetWorkflow(ctx context.Context, workflowID string, version int) (*model.WorkflowDefinitionRecord, error) {
	var wf model.WorkflowDefinitionRecord
	var webhookURL sql.NullString

	query := `SELECT id, name, version, spec_json, created_by, enabled, webhook_url, deployed, deployed_at, created_at 
	          FROM workflow_definitions WHERE id = $1 AND deleted_at IS NULL`
	args := []interface{}{workflowID}

	if version > 0 {
		query += ` AND version = $2`
		args = append(args, version)
	} else {
		// Return deployed version, or latest if no deployed version exists
		query += ` ORDER BY deployed DESC, version DESC LIMIT 1`
	}

	var deployedAt sql.NullTime
	err := c.DB.QueryRow(ctx, query, args...).Scan(
		&wf.ID, &wf.Name, &wf.Version, &wf.Spec, &wf.CreatedBy, &wf.Enabled, &webhookURL, &wf.Deployed, &deployedAt, &wf.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("workflow not found")
		}
		return nil, err
	}

	if webhookURL.Valid {
		wf.WebhookURL = webhookURL
	}
	if deployedAt.Valid {
		wf.DeployedAt = deployedAt
	}

	return &wf, nil
}

// GetLatestWorkflow retrieves the latest version of a workflow (regardless of deployment status)
func (c *Coordinator) GetLatestWorkflow(ctx context.Context, workflowID string) (*model.WorkflowDefinitionRecord, error) {
	var wf model.WorkflowDefinitionRecord
	var webhookURL sql.NullString

	query := `SELECT id, name, version, spec_json, created_by, enabled, webhook_url, deployed, deployed_at, created_at 
	          FROM workflow_definitions 
	          WHERE id = $1 AND deleted_at IS NULL
	          ORDER BY version DESC LIMIT 1`

	var deployedAt sql.NullTime
	err := c.DB.QueryRow(ctx, query, workflowID).Scan(
		&wf.ID, &wf.Name, &wf.Version, &wf.Spec, &wf.CreatedBy, &wf.Enabled, &webhookURL, &wf.Deployed, &deployedAt, &wf.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("workflow not found")
		}
		return nil, err
	}

	if webhookURL.Valid {
		wf.WebhookURL = webhookURL
	}
	if deployedAt.Valid {
		wf.DeployedAt = deployedAt
	}

	return &wf, nil
}

// ListWorkflowsResponse represents a paginated list of workflows
type ListWorkflowsResponse struct {
	Workflows  []model.WorkflowDefinitionRecord `json:"workflows"`
	Total      int                              `json:"total"`
	Page       int                              `json:"page"`
	PageSize   int                              `json:"page_size"`
	TotalPages int                              `json:"total_pages"`
}

// ListWorkflows lists workflow definitions with search, pagination, and optional enabled filter.
// Excludes soft-deleted workflows by default.
func (c *Coordinator) ListWorkflows(ctx context.Context, enabledOnly bool, search string, page, pageSize int) (*ListWorkflowsResponse, error) {
	// Build base query with filters
	baseQuery := `FROM workflow_definitions WHERE deleted_at IS NULL`
	args := []interface{}{}
	argPos := 1

	if enabledOnly {
		baseQuery += ` AND enabled = true`
	}

	// Add search filter (search in name and id)
	if search != "" {
		baseQuery += fmt.Sprintf(` AND (name ILIKE $%d OR id ILIKE $%d)`, argPos, argPos)
		args = append(args, "%"+search+"%")
		argPos++
	}

	// Get total count
	var total int
	countQuery := `SELECT COUNT(DISTINCT id) ` + baseQuery
	err := c.DB.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("failed to count workflows: %w", err)
	}

	// Calculate pagination
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	offset := (page - 1) * pageSize
	totalPages := (total + pageSize - 1) / pageSize

	// Build main query with pagination
	// Use DISTINCT ON to get only the deployed version of each workflow (or latest if no deployed version)
	// DISTINCT ON requires the ORDER BY to start with the DISTINCT ON columns
	// We need to wrap it in a subquery to apply LIMIT/OFFSET after DISTINCT ON
	query := `SELECT * FROM (
	          SELECT DISTINCT ON (id) id, name, version, spec_json, created_by, enabled, webhook_url, deployed, deployed_at, created_at 
	          ` + baseQuery + ` 
	          ORDER BY id, deployed DESC, version DESC
	          ) AS deployed_workflows
	          ORDER BY id
	          LIMIT $` + strconv.Itoa(argPos) + ` OFFSET $` + strconv.Itoa(argPos+1)
	args = append(args, pageSize, offset)

	rows, err := c.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query workflows: %w", err)
	}
	defer rows.Close()

	var workflows []model.WorkflowDefinitionRecord
	for rows.Next() {
		var wf model.WorkflowDefinitionRecord
		var webhookURL sql.NullString
		var deployedAt sql.NullTime

		if err := rows.Scan(&wf.ID, &wf.Name, &wf.Version, &wf.Spec, &wf.CreatedBy,
			&wf.Enabled, &webhookURL, &wf.Deployed, &deployedAt, &wf.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan workflow: %w", err)
		}

		if webhookURL.Valid {
			wf.WebhookURL = webhookURL
		}
		if deployedAt.Valid {
			wf.DeployedAt = deployedAt
		}

		workflows = append(workflows, wf)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating workflows: %w", err)
	}

	return &ListWorkflowsResponse{
		Workflows:  workflows,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}, nil
}

// WorkflowWithStats extends WorkflowDefinitionRecord with statistics
type WorkflowWithStats struct {
	model.WorkflowDefinitionRecord
	SuccessRate     float64 `json:"success_rate"`
	LatestVersion   int     `json:"latest_version"`
	DeployedVersion int     `json:"deployed_version,omitempty"`
}

// ListWorkflowsWithStatsResponse represents a paginated list of workflows with statistics
type ListWorkflowsWithStatsResponse struct {
	Workflows  []WorkflowWithStats `json:"workflows"`
	Total      int                 `json:"total"`
	Page       int                 `json:"page"`
	PageSize   int                 `json:"page_size"`
	TotalPages int                 `json:"total_pages"`
}

// ListWorkflowsWithStats lists workflow definitions with success rate statistics
func (c *Coordinator) ListWorkflowsWithStats(ctx context.Context, enabledOnly bool, search string, page, pageSize int) (*ListWorkflowsWithStatsResponse, error) {
	// First get workflows using existing logic
	result, err := c.ListWorkflows(ctx, enabledOnly, search, page, pageSize)
	if err != nil {
		return nil, err
	}

	// Extract workflow IDs
	workflowIDs := make([]string, 0, len(result.Workflows))
	for _, wf := range result.Workflows {
		workflowIDs = append(workflowIDs, wf.ID)
	}

	// Calculate success rates
	successRates, err := c.calculateWorkflowSuccessRates(ctx, workflowIDs)
	if err != nil {
		// Log error but don't fail the entire request
		c.Logger.Warn().Err(err).Msg("failed to calculate success rates")
		successRates = make(map[string]float64)
	}

	// Fetch latest and deployed versions in batch to avoid N+1 queries
	latestVersions, err := c.getWorkflowLatestVersions(ctx, workflowIDs)
	if err != nil {
		c.Logger.Warn().Err(err).Msg("failed to load latest workflow versions")
		latestVersions = make(map[string]int)
	}

	deployedVersions, err := c.getWorkflowDeployedVersions(ctx, workflowIDs)
	if err != nil {
		c.Logger.Warn().Err(err).Msg("failed to load deployed workflow versions")
		deployedVersions = make(map[string]int)
	}

	// Build workflows with stats
	workflowsWithStats := make([]WorkflowWithStats, 0, len(result.Workflows))
	for _, wf := range result.Workflows {
		rate := successRates[wf.ID]
		latestVersion := latestVersions[wf.ID]
		if latestVersion == 0 {
			latestVersion = wf.Version
		}
		deployedVersion := deployedVersions[wf.ID]
		if deployedVersion == 0 && wf.Deployed {
			deployedVersion = wf.Version
		}
		workflowsWithStats = append(workflowsWithStats, WorkflowWithStats{
			WorkflowDefinitionRecord: wf,
			SuccessRate:              rate,
			LatestVersion:            latestVersion,
			DeployedVersion:          deployedVersion,
		})
	}

	return &ListWorkflowsWithStatsResponse{
		Workflows:  workflowsWithStats,
		Total:      result.Total,
		Page:       result.Page,
		PageSize:   result.PageSize,
		TotalPages: result.TotalPages,
	}, nil
}

func (c *Coordinator) getWorkflowLatestVersions(ctx context.Context, workflowIDs []string) (map[string]int, error) {
	if len(workflowIDs) == 0 {
		return make(map[string]int), nil
	}

	query := `
		SELECT id, MAX(version) as latest_version
		FROM workflow_definitions
		WHERE id = ANY($1)
		  AND deleted_at IS NULL
		GROUP BY id
	`

	rows, err := c.DB.Query(ctx, query, workflowIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to query latest workflow versions: %w", err)
	}
	defer rows.Close()

	versions := make(map[string]int)
	for rows.Next() {
		var id string
		var version int
		if err := rows.Scan(&id, &version); err != nil {
			return nil, fmt.Errorf("failed to scan latest workflow version: %w", err)
		}
		versions[id] = version
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating latest workflow versions: %w", err)
	}

	return versions, nil
}

func (c *Coordinator) getWorkflowDeployedVersions(ctx context.Context, workflowIDs []string) (map[string]int, error) {
	if len(workflowIDs) == 0 {
		return make(map[string]int), nil
	}

	query := `
		SELECT id, MAX(version) as deployed_version
		FROM workflow_definitions
		WHERE id = ANY($1)
		  AND deleted_at IS NULL
		  AND deployed = true
		GROUP BY id
	`

	rows, err := c.DB.Query(ctx, query, workflowIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to query deployed workflow versions: %w", err)
	}
	defer rows.Close()

	versions := make(map[string]int)
	for rows.Next() {
		var id string
		var version int
		if err := rows.Scan(&id, &version); err != nil {
			return nil, fmt.Errorf("failed to scan deployed workflow version: %w", err)
		}
		versions[id] = version
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating deployed workflow versions: %w", err)
	}

	return versions, nil
}

// calculateWorkflowSuccessRates calculates success rates for multiple workflows in batch
func (c *Coordinator) calculateWorkflowSuccessRates(ctx context.Context, workflowIDs []string) (map[string]float64, error) {
	if len(workflowIDs) == 0 {
		return make(map[string]float64), nil
	}

	// Build query to get success rates for all workflows
	query := `
		SELECT 
			workflow_definition_id,
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status IN ('COMPLETED', 'SUCCEEDED')) as successful
		FROM workflow_runs
		WHERE workflow_definition_id = ANY($1)
		  AND deleted_at IS NULL
		  AND status IN ('COMPLETED', 'SUCCEEDED', 'FAILED')
		GROUP BY workflow_definition_id
	`

	rows, err := c.DB.Query(ctx, query, workflowIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to query success rates: %w", err)
	}
	defer rows.Close()

	rates := make(map[string]float64)
	for rows.Next() {
		var workflowID string
		var total, successful int
		if err := rows.Scan(&workflowID, &total, &successful); err != nil {
			return nil, fmt.Errorf("failed to scan success rate: %w", err)
		}
		if total > 0 {
			rates[workflowID] = float64(successful) / float64(total) * 100
		}
		// If total is 0, don't set a rate - we'll use -1 to indicate "no executions"
	}

	// Set -1 for workflows with no executions (to indicate N/A)
	for _, id := range workflowIDs {
		if _, exists := rates[id]; !exists {
			rates[id] = -1.0 // Use -1 to indicate no executions
		}
	}

	return rates, rows.Err()
}

// WorkflowIssueSummary represents a summary of issues for a workflow
type WorkflowIssueSummary struct {
	Summary string   `json:"summary"`
	Errors  []string `json:"errors"`
}

// GetWorkflowIssueSummary collects errors from failed executions and calls AI agent for summary
func (c *Coordinator) GetWorkflowIssueSummary(ctx context.Context, workflowID string) (*WorkflowIssueSummary, error) {
	// Get failed executions for this workflow
	query := `
		SELECT id, context_json
		FROM workflow_runs
		WHERE workflow_definition_id = $1
		  AND status = 'FAILED'
		  AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT 50
	`

	rows, err := c.DB.Query(ctx, query, workflowID)
	if err != nil {
		return nil, fmt.Errorf("failed to query failed executions: %w", err)
	}
	defer rows.Close()

	var errors []string
	var executionIDs []string

	for rows.Next() {
		var executionID string
		var contextJSON json.RawMessage

		if err := rows.Scan(&executionID, &contextJSON); err != nil {
			continue // Skip invalid rows
		}

		executionIDs = append(executionIDs, executionID)

		// Parse context to extract errors (errors might be in task results, but Err field is not serialized)
		// We'll rely on task instances for error extraction

		// Also get errors from task instances
		taskQuery := `
			SELECT last_error
			FROM task_instances
			WHERE run_id = $1 AND state = 'FAILED' AND last_error IS NOT NULL
		`
		taskRows, err := c.DB.Query(ctx, taskQuery, executionID)
		if err == nil {
			for taskRows.Next() {
				var taskError json.RawMessage
				if err := taskRows.Scan(&taskError); err == nil && len(taskError) > 0 {
					errors = append(errors, string(taskError))
				}
			}
			taskRows.Close()
		}
	}

	if len(errors) == 0 {
		return &WorkflowIssueSummary{
			Summary: "No errors found in recent failed executions.",
			Errors:  []string{},
		}, nil
	}

	// Try to get AI summary if configured, but always fall back to raw summary
	summary := ""
	if c.Intelligence != nil && c.Intelligence.IsConfigured() {
		aiSummary, err := c.Intelligence.SummarizeErrors(ctx, workflowID, errors)
		if err != nil {
			c.Logger.Warn().Err(err).Msg("failed to call AI agent, using raw summary")
			// Fall through to create raw summary
		} else {
			summary = aiSummary
		}
	}

	// If no summary from AI (either not configured or failed), create a raw summary
	if summary == "" {
		summary = fmt.Sprintf("Found %d error(s) across %d failed execution(s). Review the errors below for details.", len(errors), len(executionIDs))
	}

	return &WorkflowIssueSummary{
		Summary: summary,
		Errors:  errors,
	}, nil
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

// DeleteWorkflow performs soft delete on a workflow definition and all its instances.
// If version is 0, deletes all versions of the workflow.
// Set hardDelete to true to actually delete the workflow (cascade delete).
func (c *Coordinator) DeleteWorkflow(ctx context.Context, workflowID string, version int, hardDelete bool) error {
	tx, err := c.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	deleteAllVersions := version == 0

	if hardDelete {
		if deleteAllVersions {
			// Hard delete all versions: actually remove from database
			// First delete all workflow runs for all versions
			_, err = tx.Exec(ctx, `DELETE FROM workflow_runs WHERE workflow_definition_id = $1`,
				workflowID)
			if err != nil {
				return fmt.Errorf("failed to delete workflow runs: %w", err)
			}
			// Then delete all workflow definitions
			_, err = tx.Exec(ctx, `DELETE FROM workflow_definitions WHERE id = $1`,
				workflowID)
			if err != nil {
				return fmt.Errorf("failed to delete workflow definitions: %w", err)
			}
		} else {
			// Hard delete specific version: actually remove from database
			// First delete workflow runs (cascade)
			_, err = tx.Exec(ctx, `DELETE FROM workflow_runs WHERE workflow_definition_id = $1 AND workflow_version = $2`,
				workflowID, version)
			if err != nil {
				return fmt.Errorf("failed to delete workflow runs: %w", err)
			}
			// Then delete workflow definition
			_, err = tx.Exec(ctx, `DELETE FROM workflow_definitions WHERE id = $1 AND version = $2`,
				workflowID, version)
			if err != nil {
				return fmt.Errorf("failed to delete workflow definition: %w", err)
			}
		}
	} else {
		if deleteAllVersions {
			// Soft delete all versions: mark as deleted with timestamp
			// Mark all workflow definitions as deleted
			result, err := tx.Exec(ctx,
				`UPDATE workflow_definitions 
				 SET deleted_at = NOW(), enabled = false 
				 WHERE id = $1 AND deleted_at IS NULL`,
				workflowID)
			if err != nil {
				return fmt.Errorf("failed to soft delete workflow definitions: %w", err)
			}

			rowsAffected := result.RowsAffected()
			if rowsAffected == 0 {
				return errors.New("workflow not found or already deleted")
			}

			// Mark all workflow instances (runs) as deleted for all versions
			_, err = tx.Exec(ctx,
				`UPDATE workflow_runs 
				 SET deleted_at = NOW() 
				 WHERE workflow_definition_id = $1 AND deleted_at IS NULL`,
				workflowID)
			if err != nil {
				return fmt.Errorf("failed to soft delete workflow instances: %w", err)
			}
		} else {
			// Soft delete specific version: mark as deleted with timestamp
			// Mark workflow definition as deleted
			result, err := tx.Exec(ctx,
				`UPDATE workflow_definitions 
				 SET deleted_at = NOW(), enabled = false 
				 WHERE id = $1 AND version = $2 AND deleted_at IS NULL`,
				workflowID, version)
			if err != nil {
				return fmt.Errorf("failed to soft delete workflow definition: %w", err)
			}

			rowsAffected := result.RowsAffected()
			if rowsAffected == 0 {
				return errors.New("workflow not found or already deleted")
			}

			// Mark all workflow instances (runs) as deleted
			_, err = tx.Exec(ctx,
				`UPDATE workflow_runs 
				 SET deleted_at = NOW() 
				 WHERE workflow_definition_id = $1 AND workflow_version = $2 AND deleted_at IS NULL`,
				workflowID, version)
			if err != nil {
				return fmt.Errorf("failed to soft delete workflow instances: %w", err)
			}
		}
	}

	return tx.Commit(ctx)
}

// GetWorkflowInstance retrieves a workflow instance (run) by ID.
// Returns error if instance is soft-deleted.
func (c *Coordinator) GetWorkflowInstance(ctx context.Context, instanceID string) (*model.WorkflowInstanceRecord, error) {
	var inst model.WorkflowInstanceRecord
	var completedAt sql.NullTime

	err := c.DB.QueryRow(ctx,
		`SELECT id, workflow_definition_id, workflow_version, status, input_json, created_at, updated_at, completed_at 
		 FROM workflow_runs WHERE id = $1 AND deleted_at IS NULL`,
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
		`SELECT context_json FROM workflow_runs WHERE id = $1 AND deleted_at IS NULL`,
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

	// Get task instances (only for non-deleted runs)
	rows, err := c.DB.Query(ctx,
		`SELECT ti.id, ti.run_id, ti.node_id, ti.node_type, ti.state, ti.attempt, ti.input_json, ti.output_json, ti.last_error,
		        ti.scheduled_at, ti.started_at, ti.finished_at, ti.completed_at, ti.created_at, ti.updated_at,
		        ti.lease_owner, ti.lease_expires_at
		 FROM task_instances ti
		 INNER JOIN workflow_runs wr ON ti.run_id = wr.id
		 WHERE ti.run_id = $1 AND wr.deleted_at IS NULL
		 ORDER BY ti.created_at ASC`,
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

// ListWorkflowInstancesResponse represents a paginated list of workflow instances
type ListWorkflowInstancesResponse struct {
	Executions []model.WorkflowInstanceRecord `json:"executions"`
	Total      int                            `json:"total"`
	Page       int                            `json:"page"`
	PageSize   int                            `json:"page_size"`
	TotalPages int                            `json:"total_pages"`
}

// ListWorkflowInstances lists workflow instances with search, pagination, and optional filters.
// Excludes soft-deleted instances by default.
func (c *Coordinator) ListWorkflowInstances(ctx context.Context, workflowID *string,
	status *model.WorkflowInstanceStatus, search string, page, pageSize int) (*ListWorkflowInstancesResponse, error) {
	// Build base query with filters
	baseQuery := `FROM workflow_runs WHERE deleted_at IS NULL`
	args := []interface{}{}
	argPos := 1

	if workflowID != nil {
		baseQuery += fmt.Sprintf(` AND workflow_definition_id = $%d`, argPos)
		args = append(args, *workflowID)
		argPos++
	}

	if status != nil {
		baseQuery += fmt.Sprintf(` AND status = $%d`, argPos)
		args = append(args, string(*status))
		argPos++
	}

	// Add search filter (search in execution ID and workflow ID)
	if search != "" {
		baseQuery += fmt.Sprintf(` AND (id ILIKE $%d OR workflow_definition_id ILIKE $%d)`, argPos, argPos)
		args = append(args, "%"+search+"%")
		argPos++
	}

	// Get total count
	var total int
	countQuery := `SELECT COUNT(*) ` + baseQuery
	err := c.DB.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("failed to count executions: %w", err)
	}

	// Calculate pagination
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	totalPages := (total + pageSize - 1) / pageSize

	// Build main query with pagination
	query := `SELECT id, workflow_definition_id, workflow_version, status, input_json, created_at, updated_at, completed_at 
	          ` + baseQuery + ` 
	          ORDER BY created_at DESC
	          LIMIT $` + strconv.Itoa(argPos) + ` OFFSET $` + strconv.Itoa(argPos+1)
	args = append(args, pageSize, offset)

	rows, err := c.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query executions: %w", err)
	}
	defer rows.Close()

	var instances []model.WorkflowInstanceRecord
	for rows.Next() {
		var inst model.WorkflowInstanceRecord
		var completedAt sql.NullTime
		if err := rows.Scan(&inst.ID, &inst.WorkflowDefinitionID, &inst.WorkflowVersion,
			&inst.Status, &inst.Input, &inst.CreatedAt, &inst.UpdatedAt, &completedAt); err != nil {
			return nil, fmt.Errorf("failed to scan execution: %w", err)
		}
		if completedAt.Valid {
			inst.CompletedAt = &completedAt.Time
		}
		instances = append(instances, inst)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating executions: %w", err)
	}

	return &ListWorkflowInstancesResponse{
		Executions: instances,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}, nil
}

// GetWorkflowInstanceStatus retrieves detailed status information for a workflow instance.
func (c *Coordinator) GetWorkflowInstanceStatus(ctx context.Context, instanceID string) (*model.WorkflowInstanceStatusResponse, error) {
	var status model.WorkflowInstanceStatusResponse

	// Get instance basic info
	err := c.DB.QueryRow(ctx,
		`SELECT id, status, created_at, updated_at FROM workflow_runs WHERE id = $1 AND deleted_at IS NULL`,
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

// DeployWorkflow marks a workflow version as deployed
func (c *Coordinator) DeployWorkflow(ctx context.Context, workflowID string, version int) error {
	ctx, span := util.StartSpan(ctx, "workflow.deploy",
		util.Attribute("workflow.id", workflowID),
		util.AttributeInt("workflow.version", int64(version)),
	)
	defer util.EndSpan(span, nil)

	logger := c.Logger.With().
		Str("workflow_id", workflowID).
		Int("version", version).
		Logger()

	tx, err := c.DB.Begin(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("failed to begin transaction")
		util.RecordError(span, err)
		return err
	}
	defer tx.Rollback(ctx)

	// Check if workflow version exists
	var exists bool
	err = tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM workflow_definitions WHERE id = $1 AND version = $2 AND deleted_at IS NULL)`,
		workflowID, version).Scan(&exists)
	if err != nil {
		logger.Error().Err(err).Msg("failed to check workflow existence")
		util.RecordError(span, err)
		return err
	}
	if !exists {
		return errors.New("workflow version not found")
	}

	// Undeploy any currently deployed version for this workflow
	// Keep deployed_at timestamp for rollback history, only set deployed = false
	_, err = tx.Exec(ctx,
		`UPDATE workflow_definitions 
		 SET deployed = false
		 WHERE id = $1 AND deployed = true AND deleted_at IS NULL AND version != $2`,
		workflowID, version)
	if err != nil {
		logger.Error().Err(err).Msg("failed to undeploy current version")
		util.RecordError(span, err)
		return err
	}

	// Mark as deployed
	result, err := tx.Exec(ctx,
		`UPDATE workflow_definitions 
		 SET deployed = true, deployed_at = NOW() 
		 WHERE id = $1 AND version = $2 AND deleted_at IS NULL`,
		workflowID, version)
	if err != nil {
		logger.Error().Err(err).Msg("failed to deploy workflow")
		util.RecordError(span, err)
		return err
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return errors.New("workflow version not found or already deleted")
	}

	logger.Info().Msg("workflow version deployed successfully")
	return tx.Commit(ctx)
}

// RollbackWorkflow deploys a previous deployed version (makes it the active deployed version)
func (c *Coordinator) RollbackWorkflow(ctx context.Context, workflowID string, targetVersion int) error {
	ctx, span := util.StartSpan(ctx, "workflow.rollback",
		util.Attribute("workflow.id", workflowID),
		util.AttributeInt("target.version", int64(targetVersion)),
	)
	defer util.EndSpan(span, nil)

	logger := c.Logger.With().
		Str("workflow_id", workflowID).
		Int("target_version", targetVersion).
		Logger()

	tx, err := c.DB.Begin(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("failed to begin transaction")
		util.RecordError(span, err)
		return err
	}
	defer tx.Rollback(ctx)

	// Verify the target version exists and was deployed at some point (has deployed_at)
	var exists bool
	err = tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM workflow_definitions 
		 WHERE id = $1 AND version = $2 AND deleted_at IS NULL AND deployed_at IS NOT NULL)`,
		workflowID, targetVersion).Scan(&exists)
	if err != nil {
		logger.Error().Err(err).Msg("failed to check target version")
		util.RecordError(span, err)
		return err
	}
	if !exists {
		return errors.New("target workflow version not found or was never deployed")
	}

	// Undeploy the current deployed version (if any)
	// Keep deployed_at timestamp for rollback history, only set deployed = false
	_, err = tx.Exec(ctx,
		`UPDATE workflow_definitions 
		 SET deployed = false
		 WHERE id = $1 AND deployed = true AND deleted_at IS NULL`,
		workflowID)
	if err != nil {
		logger.Error().Err(err).Msg("failed to undeploy current version")
		util.RecordError(span, err)
		return err
	}

	// Deploy the target version
	result, err := tx.Exec(ctx,
		`UPDATE workflow_definitions 
		 SET deployed = true, deployed_at = NOW()
		 WHERE id = $1 AND version = $2 AND deleted_at IS NULL`,
		workflowID, targetVersion)
	if err != nil {
		logger.Error().Err(err).Msg("failed to deploy target version")
		util.RecordError(span, err)
		return err
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return errors.New("failed to deploy target version")
	}

	logger.Info().
		Int("rollback_to_version", targetVersion).
		Msg("workflow rolled back successfully")
	return tx.Commit(ctx)
}

// GetPreviousDeployedVersions returns a list of previous deployed versions for a workflow
// It looks for versions that have been deployed at some point (deployed_at IS NOT NULL),
// not just currently deployed versions, so rollback can work even after newer versions are deployed
func (c *Coordinator) GetPreviousDeployedVersions(ctx context.Context, workflowID string, currentVersion int) ([]int, error) {
	rows, err := c.DB.Query(ctx,
		`SELECT version FROM workflow_definitions 
		 WHERE id = $1 AND version < $2 AND deployed_at IS NOT NULL AND deleted_at IS NULL
		 ORDER BY version DESC`,
		workflowID, currentVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to query previous deployed versions: %w", err)
	}
	defer rows.Close()

	var versions []int
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("failed to scan version: %w", err)
		}
		versions = append(versions, version)
	}

	return versions, rows.Err()
}
