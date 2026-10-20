package coord

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/util"
)

// definitionColumns are the workflow_definitions columns read by scanDefinition.
const definitionColumns = `id, name, version, spec_json, created_by, enabled, webhook_url, deployed, deployed_at, created_at`

func scanDefinition(row pgx.Row) (*model.WorkflowDefinitionRecord, error) {
	var wf model.WorkflowDefinitionRecord
	err := row.Scan(&wf.ID, &wf.Name, &wf.Version, &wf.Spec, &wf.CreatedBy,
		&wf.Enabled, &wf.WebhookURL, &wf.Deployed, &wf.DeployedAt, &wf.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: workflow", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("scan workflow definition: %w", err)
	}
	return &wf, nil
}

// RegisterWorkflow stores a workflow definition version. An undeployed version
// is updated in place; registering over a deployed version creates the next
// version instead, so deployed definitions (and the runs pinned to them) never
// change.
func (c *Coordinator) RegisterWorkflow(ctx context.Context, p *model.RegisterWorkflowRequest) (err error) {
	ctx, span := util.StartSpan(ctx, "workflow.register",
		util.Attribute("workflow.id", p.WorkflowID),
		util.AttributeInt("workflow.version", int64(p.Version)),
	)
	defer func() { util.EndSpan(span, err) }()
	createdBy := nullString(p.CreatedBy)

	err = c.inTx(ctx, func(t *txn) error {
		tag, err := t.Exec(ctx, `
			UPDATE workflow_definitions
			   SET name = $1, spec_json = $2::jsonb, created_by = $3, created_at = NOW()
			 WHERE id = $4 AND version = $5 AND deleted_at IS NULL AND deployed = false`,
			p.Name, p.Spec, createdBy, p.WorkflowID, p.Version)
		if err != nil {
			return fmt.Errorf("update workflow definition: %w", err)
		}
		if tag.RowsAffected() > 0 {
			// The cached DAG of this version is stale now. Other coordinators
			// serve theirs until the cache TTL; undeployed versions are drafts.
			t.onCommit(func() { c.wfCache.evict(p.WorkflowID, p.Version) })
			return nil
		}

		version := p.Version
		var deployed bool
		err = t.QueryRow(ctx,
			`SELECT deployed FROM workflow_definitions WHERE id = $1 AND version = $2 AND deleted_at IS NULL`,
			p.WorkflowID, p.Version).Scan(&deployed)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// New version: insert as requested.
		case err != nil:
			return fmt.Errorf("check workflow version: %w", err)
		case deployed:
			if err := t.QueryRow(ctx,
				`SELECT COALESCE(MAX(version), 0) + 1 FROM workflow_definitions WHERE id = $1`,
				p.WorkflowID).Scan(&version); err != nil {
				return fmt.Errorf("next workflow version: %w", err)
			}
		}

		_, err = t.Exec(ctx, `
			INSERT INTO workflow_definitions (id, name, version, spec_json, created_by, deployed)
			VALUES ($1, $2, $3, $4::jsonb, $5, false)
			ON CONFLICT (id, version) DO NOTHING`,
			p.WorkflowID, p.Name, version, p.Spec, createdBy)
		if err != nil {
			return fmt.Errorf("insert workflow definition: %w", err)
		}
		c.Logger.Info().Str("workflow_id", p.WorkflowID).Int("version", version).Msg("workflow version registered")
		return nil
	})
	return err
}

// GetWorkflow returns a workflow definition version. Version 0 returns the
// deployed version, or the latest one if none is deployed.
func (c *Coordinator) GetWorkflow(ctx context.Context, workflowID string, version int) (*model.WorkflowDefinitionRecord, error) {
	if version > 0 {
		return scanDefinition(c.DB.QueryRow(ctx,
			`SELECT `+definitionColumns+` FROM workflow_definitions
			  WHERE id = $1 AND version = $2 AND deleted_at IS NULL`,
			workflowID, version))
	}
	return scanDefinition(c.DB.QueryRow(ctx,
		`SELECT `+definitionColumns+` FROM workflow_definitions
		  WHERE id = $1 AND deleted_at IS NULL
		  ORDER BY deployed DESC, version DESC LIMIT 1`,
		workflowID))
}

// GetLatestWorkflow returns the highest version of a workflow, deployed or not.
func (c *Coordinator) GetLatestWorkflow(ctx context.Context, workflowID string) (*model.WorkflowDefinitionRecord, error) {
	return scanDefinition(c.DB.QueryRow(ctx,
		`SELECT `+definitionColumns+` FROM workflow_definitions
		  WHERE id = $1 AND deleted_at IS NULL
		  ORDER BY version DESC LIMIT 1`,
		workflowID))
}

// ListWorkflowsResponse represents a paginated list of workflows
type ListWorkflowsResponse struct {
	Workflows  []model.WorkflowDefinitionRecord `json:"workflows"`
	Total      int                              `json:"total"`
	Page       int                              `json:"page"`
	PageSize   int                              `json:"page_size"`
	TotalPages int                              `json:"total_pages"`
}

// ListWorkflows lists one row per workflow — its deployed version, or the
// latest if none is deployed — with search and pagination. Deleted versions
// are excluded.
func (c *Coordinator) ListWorkflows(ctx context.Context, enabledOnly bool, search string, pageNum, pageSize int) (*ListWorkflowsResponse, error) {
	pg := newPage(pageNum, pageSize, 10)
	var f filter
	f.add("deleted_at IS NULL")
	if enabledOnly {
		f.add("enabled = true")
	}
	if search != "" {
		f.add("(name ILIKE ? OR id ILIKE ?)", "%"+search+"%", "%"+search+"%")
	}

	var total int
	if err := c.DB.QueryRow(ctx, `SELECT COUNT(DISTINCT id) FROM workflow_definitions`+f.where(), f.args...).
		Scan(&total); err != nil {
		return nil, fmt.Errorf("count workflows: %w", err)
	}

	// DISTINCT ON picks each workflow's deployed-or-latest version.
	query := `SELECT * FROM (
	              SELECT DISTINCT ON (id) ` + definitionColumns + `
	                FROM workflow_definitions` + f.where() + `
	               ORDER BY id, deployed DESC, version DESC
	          ) AS current_versions
	          ORDER BY id` + f.limit(pg)
	rows, err := c.DB.Query(ctx, query, f.args...)
	if err != nil {
		return nil, fmt.Errorf("query workflows: %w", err)
	}
	workflows, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.WorkflowDefinitionRecord, error) {
		wf, err := scanDefinition(row)
		if err != nil {
			return model.WorkflowDefinitionRecord{}, err
		}
		return *wf, nil
	})
	if err != nil {
		return nil, err
	}

	return &ListWorkflowsResponse{
		Workflows:  workflows,
		Total:      total,
		Page:       pg.Number,
		PageSize:   pg.Size,
		TotalPages: pg.totalPages(total),
	}, nil
}

// WorkflowWithStats extends WorkflowDefinitionRecord with statistics
type WorkflowWithStats struct {
	model.WorkflowDefinitionRecord
	// SuccessRate is the percentage of finished runs that succeeded, or -1
	// when the workflow has no finished runs.
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

// ListWorkflowsWithStats is ListWorkflows plus each workflow's success rate
// and latest/deployed versions. Statistics are best-effort: if they cannot be
// computed the list is still returned.
func (c *Coordinator) ListWorkflowsWithStats(ctx context.Context, enabledOnly bool, search string, pageNum, pageSize int) (*ListWorkflowsWithStatsResponse, error) {
	result, err := c.ListWorkflows(ctx, enabledOnly, search, pageNum, pageSize)
	if err != nil {
		return nil, err
	}

	ids := make([]string, len(result.Workflows))
	for i, wf := range result.Workflows {
		ids[i] = wf.ID
	}
	rates, err := c.successRates(ctx, ids)
	if err != nil {
		c.Logger.Warn().Err(err).Msg("failed to calculate success rates")
	}
	versions, err := c.versionSummaries(ctx, ids)
	if err != nil {
		c.Logger.Warn().Err(err).Msg("failed to load workflow versions")
	}

	out := make([]WorkflowWithStats, len(result.Workflows))
	for i, wf := range result.Workflows {
		rate, ok := rates[wf.ID]
		if !ok {
			rate = -1
		}
		v := versions[wf.ID]
		if v.latest == 0 {
			v.latest = wf.Version
		}
		if v.deployed == 0 && wf.Deployed {
			v.deployed = wf.Version
		}
		out[i] = WorkflowWithStats{WorkflowDefinitionRecord: wf, SuccessRate: rate,
			LatestVersion: v.latest, DeployedVersion: v.deployed}
	}

	return &ListWorkflowsWithStatsResponse{
		Workflows:  out,
		Total:      result.Total,
		Page:       result.Page,
		PageSize:   result.PageSize,
		TotalPages: result.TotalPages,
	}, nil
}

type versionSummary struct{ latest, deployed int }

// versionSummaries returns the latest and highest deployed version per workflow.
func (c *Coordinator) versionSummaries(ctx context.Context, ids []string) (map[string]versionSummary, error) {
	out := map[string]versionSummary{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := c.DB.Query(ctx, `
		SELECT id, MAX(version), COALESCE(MAX(version) FILTER (WHERE deployed), 0)
		  FROM workflow_definitions
		 WHERE id = ANY($1) AND deleted_at IS NULL
		 GROUP BY id`, ids)
	if err != nil {
		return out, fmt.Errorf("query workflow versions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var v versionSummary
		if err := rows.Scan(&id, &v.latest, &v.deployed); err != nil {
			return out, fmt.Errorf("scan workflow versions: %w", err)
		}
		out[id] = v
	}
	return out, rows.Err()
}

// successRates returns, per workflow with finished runs, the percentage that succeeded.
func (c *Coordinator) successRates(ctx context.Context, ids []string) (map[string]float64, error) {
	out := map[string]float64{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := c.DB.Query(ctx, `
		SELECT workflow_definition_id,
		       COUNT(*),
		       COUNT(*) FILTER (WHERE status IN ('COMPLETED', 'SUCCEEDED'))
		  FROM workflow_runs
		 WHERE workflow_definition_id = ANY($1)
		   AND deleted_at IS NULL
		   AND status IN ('COMPLETED', 'SUCCEEDED', 'FAILED')
		 GROUP BY workflow_definition_id`, ids)
	if err != nil {
		return out, fmt.Errorf("query success rates: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var total, succeeded int
		if err := rows.Scan(&id, &total, &succeeded); err != nil {
			return out, fmt.Errorf("scan success rate: %w", err)
		}
		if total > 0 {
			out[id] = float64(succeeded) / float64(total) * 100
		}
	}
	return out, rows.Err()
}

// UpdateWorkflow updates a definition version's metadata (name, enabled,
// webhook_url); nil fields are left unchanged. The spec is immutable —
// register a new version instead.
func (c *Coordinator) UpdateWorkflow(ctx context.Context, workflowID string, version int,
	name *string, enabled *bool, webhookURL *string) error {

	var f filter
	var sets []string
	if name != nil {
		sets = append(sets, "name = "+f.arg(*name))
	}
	if enabled != nil {
		sets = append(sets, "enabled = "+f.arg(*enabled))
	}
	if webhookURL != nil {
		sets = append(sets, "webhook_url = "+f.arg(nullString(*webhookURL)))
	}
	if len(sets) == 0 {
		return fmt.Errorf("%w: no fields to update", ErrInvalid)
	}
	f.add("id = ?", workflowID)
	f.add("version = ?", version)

	tag, err := c.DB.Exec(ctx,
		`UPDATE workflow_definitions SET `+strings.Join(sets, ", ")+f.where(), f.args...)
	if err != nil {
		return fmt.Errorf("update workflow: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: workflow %s v%d", ErrNotFound, workflowID, version)
	}
	return nil
}

// DeleteWorkflow deletes one version of a workflow, or all versions when
// version is 0, together with their runs. A soft delete marks rows deleted; a
// hard delete removes them.
func (c *Coordinator) DeleteWorkflow(ctx context.Context, workflowID string, version int, hardDelete bool) error {
	var defs, runs filter
	defs.add("id = ?", workflowID)
	runs.add("workflow_definition_id = ?", workflowID)
	if version > 0 {
		defs.add("version = ?", version)
		runs.add("workflow_version = ?", version)
	}

	return c.inTx(ctx, func(t *txn) error {
		if hardDelete {
			// Task instances and events go with their runs (ON DELETE CASCADE).
			if _, err := t.Exec(ctx, `DELETE FROM workflow_runs`+runs.where(), runs.args...); err != nil {
				return fmt.Errorf("delete workflow runs: %w", err)
			}
			if _, err := t.Exec(ctx, `DELETE FROM workflow_definitions`+defs.where(), defs.args...); err != nil {
				return fmt.Errorf("delete workflow definitions: %w", err)
			}
			return nil
		}

		defs.add("deleted_at IS NULL")
		tag, err := t.Exec(ctx,
			`UPDATE workflow_definitions SET deleted_at = NOW(), enabled = false`+defs.where(), defs.args...)
		if err != nil {
			return fmt.Errorf("soft delete workflow definitions: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: workflow %s (or already deleted)", ErrNotFound, workflowID)
		}
		runs.add("deleted_at IS NULL")
		if _, err := t.Exec(ctx, `UPDATE workflow_runs SET deleted_at = NOW()`+runs.where(), runs.args...); err != nil {
			return fmt.Errorf("soft delete workflow runs: %w", err)
		}
		return nil
	})
}

// DeployWorkflow makes version the workflow's deployed version.
func (c *Coordinator) DeployWorkflow(ctx context.Context, workflowID string, version int) error {
	return c.activateVersion(ctx, "workflow.deploy", workflowID, version, false)
}

// RollbackWorkflow redeploys a version that was deployed before.
func (c *Coordinator) RollbackWorkflow(ctx context.Context, workflowID string, targetVersion int) error {
	return c.activateVersion(ctx, "workflow.rollback", workflowID, targetVersion, true)
}

// activateVersion marks version deployed and every other version undeployed.
// deployed_at is kept on undeployed versions as rollback history; with
// mustHaveBeenDeployed only such versions are eligible.
func (c *Coordinator) activateVersion(ctx context.Context, op, workflowID string, version int, mustHaveBeenDeployed bool) (err error) {
	ctx, span := util.StartSpan(ctx, op,
		util.Attribute("workflow.id", workflowID),
		util.AttributeInt("workflow.version", int64(version)),
	)
	defer func() { util.EndSpan(span, err) }()

	err = c.inTx(ctx, func(t *txn) error {
		var eligible bool
		if err := t.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM workflow_definitions
			                WHERE id = $1 AND version = $2 AND deleted_at IS NULL
			                  AND ($3 = false OR deployed_at IS NOT NULL))`,
			workflowID, version, mustHaveBeenDeployed).Scan(&eligible); err != nil {
			return fmt.Errorf("check workflow version: %w", err)
		}
		if !eligible {
			if mustHaveBeenDeployed {
				return fmt.Errorf("%w: workflow %s v%d was never deployed", ErrNotFound, workflowID, version)
			}
			return fmt.Errorf("%w: workflow %s v%d", ErrNotFound, workflowID, version)
		}
		if _, err := t.Exec(ctx, `
			UPDATE workflow_definitions
			   SET deployed = (version = $2),
			       deployed_at = CASE WHEN version = $2 THEN NOW() ELSE deployed_at END
			 WHERE id = $1 AND deleted_at IS NULL AND (deployed OR version = $2)`,
			workflowID, version); err != nil {
			return fmt.Errorf("deploy workflow version: %w", err)
		}
		return nil
	})
	if err == nil {
		c.Logger.Info().Str("workflow_id", workflowID).Int("version", version).Str("op", op).Msg("workflow version deployed")
	}
	return err
}

// GetPreviousDeployedVersions returns versions below currentVersion that were
// deployed at some point, newest first: the candidates for a rollback.
func (c *Coordinator) GetPreviousDeployedVersions(ctx context.Context, workflowID string, currentVersion int) ([]int, error) {
	rows, err := c.DB.Query(ctx, `
		SELECT version FROM workflow_definitions
		 WHERE id = $1 AND version < $2 AND deployed_at IS NOT NULL AND deleted_at IS NULL
		 ORDER BY version DESC`,
		workflowID, currentVersion)
	if err != nil {
		return nil, fmt.Errorf("query previous deployed versions: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[int])
}

// nullString maps "" to SQL NULL.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
