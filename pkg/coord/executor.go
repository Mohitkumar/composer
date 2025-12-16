package coord

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/util"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/trace"
)

// StartWorkflow creates a workflow_run and creates initial task instance from start node
// If RunID is not provided, it will be generated using UUID
func (c *Coordinator) StartWorkflow(ctx context.Context, p *model.StartWorkflowRequest) error {
	// Generate run_id if not provided
	if p.RunID == "" {
		p.RunID = uuid.New().String()
	}

	// Start tracing span
	ctx, span := util.StartWorkflowSpan(ctx, "start", p.WorkflowID, p.RunID)
	defer util.EndSpan(span, nil)

	logger := c.Logger.With().
		Str("workflow_id", p.WorkflowID).
		Str("run_id", p.RunID).
		Int("workflow_version", p.WorkflowVersion).
		Str("started_by", p.StartedBy).
		Logger()

	logger.Info().Msg("starting workflow execution")

	startTime := time.Now()

	// Load workflow definition to get nodes and edges (before transaction)
	wfDef, err := c.GetWorkflow(ctx, p.WorkflowID, p.WorkflowVersion)
	if err != nil {
		logger.Error().Err(err).Msg("failed to load workflow definition")
		util.RecordError(span, err)
		return fmt.Errorf("failed to load workflow definition: %w", err)
	}

	// Parse workflow spec to get nodes and edges
	var wfSpec model.WorkflowDefinitionSpec
	if err := json.Unmarshal(wfDef.Spec, &wfSpec); err != nil {
		logger.Error().Err(err).Msg("failed to parse workflow spec")
		util.RecordError(span, err)
		return fmt.Errorf("failed to parse workflow spec: %w", err)
	}

	err = c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		if err := c.insertWorkflowRun(ctx, tx, p, logger, span); err != nil {
			return err
		}

		// Find start node using utility method
		startNode, err := wfSpec.FindStartNode(p.StartNodeID)
		if err != nil {
			logger.Error().Err(err).Str("start_node_id", p.StartNodeID).Msg("failed to find start node")
			util.RecordError(span, err)
			return err
		}

		// Create task instance for start node
		taskID := uuid.New().String()
		scheduledAt := time.Now()
		if p.StartedAtUnixMs > 0 {
			scheduledAt = time.UnixMilli(p.StartedAtUnixMs)
		}

		runContext := c.newRunContext(p.Input, p.ExecutionOptions)
		taskInput, err := c.resolveNodeInputFromSpecRaw(startNode.Spec, runContext, logger)
		if err != nil {
			return err
		}

		taskLogger := logger.With().
			Str("task_id", taskID).
			Str("node_id", startNode.NodeID).
			Str("node_type", startNode.Runtime).
			Logger()

		if err := c.insertTaskInstance(ctx, tx, taskID, p.RunID, startNode, taskInput, scheduledAt, taskLogger, span); err != nil {
			return err
		}
		c.recordTaskCreated(ctx, taskLogger, span, taskID, p.RunID, startNode)

		if err := c.insertTaskOutbox(ctx, tx, taskID, p.RunID, startNode, taskInput, taskLogger, span); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return err
	}

	duration := time.Since(startTime)
	logger.Info().
		Dur("duration_ms", duration).
		Msg("workflow started successfully")

	util.RecordWorkflowStarted(ctx, p.WorkflowID, p.RunID)
	util.SetSpanAttributes(span,
		util.AttributeInt("workflow.duration_ms", duration.Milliseconds()),
	)

	return nil
}

// HandleNodeCompleted marks a node as completed and dispatches next tasks from the workflow DAG
// This is split into two transactions:
// 1. First transaction: Record the output from the previous node (mark completed, clear lease, update run context)
// 2. Second transaction: Start the next nodes (resolve parameters, create task instances)
// This ensures that node completion is always recorded even if starting next nodes fails.
func (c *Coordinator) HandleNodeCompleted(ctx context.Context, taskID string, runID string, nodeID string, output json.RawMessage) error {
	// Get workflow run info to load workflow definition (before transaction)
	var workflowID string
	var workflowVersion int
	err := c.DB.QueryRow(ctx, `SELECT workflow_definition_id, workflow_version FROM workflow_runs WHERE id=$1`, runID).
		Scan(&workflowID, &workflowVersion)
	if err != nil {
		c.Logger.Error().Err(err).Str("run_id", runID).Msg("failed to get workflow run")
		return fmt.Errorf("failed to get workflow run: %w", err)
	}

	// Start tracing span
	ctx, span := util.StartTaskSpan(ctx, "completed", "", runID, nodeID)
	defer util.EndSpan(span, nil)

	logger := c.Logger.With().
		Str("workflow_id", workflowID).
		Str("run_id", runID).
		Str("node_id", nodeID).
		Logger()

	logger.Info().Msg("handling node completion")

	startTime := time.Now()

	// Load workflow definition to get DAG structure (before transaction)
	wfDef, err := c.GetWorkflow(ctx, workflowID, workflowVersion)
	if err != nil {
		return fmt.Errorf("failed to load workflow definition: %w", err)
	}

	// Parse workflow spec
	var wfSpec model.WorkflowDefinitionSpec
	if err := json.Unmarshal(wfDef.Spec, &wfSpec); err != nil {
		return fmt.Errorf("failed to parse workflow spec: %w", err)
	}

	// STEP 1: Record the output from the previous node successfully
	// This transaction should always succeed to preserve the node completion state
	var runContext *model.RunContext
	err = c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		updated, err := c.markTaskCompleted(ctx, tx, runID, nodeID, output, logger, span)
		if err != nil {
			return err
		}
		if !updated {
			// Task was already completed, load run context for potential next node processing
			runContext, err = c.loadRunContext(ctx, tx, runID, logger, span)
			return err
		}

		if err := c.clearTaskLease(ctx, tx, runID, nodeID, logger, span); err != nil {
			return err
		}

		runContext, err = c.loadAndUpdateRunContext(ctx, tx, runID, nodeID, output, logger, span)
		if err != nil {
			return err
		}

		logger.Debug().Msg("node completion recorded successfully")
		return nil
	})

	if err != nil {
		logger.Error().Err(err).Msg("failed to record node completion")
		return fmt.Errorf("failed to record node completion: %w", err)
	}

	// STEP 2: Start the next nodes
	// This is a separate transaction so that if it fails, we can retry without losing the completion record
	nextNodes := wfSpec.FindNextNodes(nodeID)
	logger.Debug().Int("next_nodes_count", len(nextNodes)).Msg("found next nodes")

	if len(nextNodes) == 0 {
		// No next nodes, check if workflow is complete
		err = c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
			done, pending, err := c.finalizeWorkflowIfComplete(ctx, tx, runID, workflowID, span, logger)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			logger.Debug().Int("pending_tasks", pending).Msg("workflow still has pending tasks")
			return nil
		})
		if err != nil {
			logger.Error().Err(err).Msg("failed to finalize workflow completion check")
			// Don't return error here - the node is already marked as completed
		}
	} else {
		// Start next nodes in a separate transaction
		err = c.startNextNodes(ctx, span, logger, runID, workflowID, nextNodes, runContext)
		if err != nil {
			logger.Error().Err(err).Msg("failed to start next nodes, but node completion was recorded")
			// Don't return error here - the node is already marked as completed
			// The next nodes can be started later via a retry mechanism or manual intervention
		}
	}

	duration := time.Since(startTime)
	logger.Info().
		Dur("duration_ms", duration).
		Int("next_tasks_count", len(nextNodes)).
		Msg("node completion handled")

	return nil
}

func (c *Coordinator) newRunContext(input json.RawMessage, config json.RawMessage) *model.RunContext {
	return &model.RunContext{
		Input:  input,
		Config: config,
		Tasks:  map[string]*model.StepResult{},
	}
}

func (c *Coordinator) insertWorkflowRun(ctx context.Context, tx pgx.Tx, p *model.StartWorkflowRequest, logger zerolog.Logger, span trace.Span) error {
	status := "RUNNING"
	runContext := c.newRunContext(p.Input, p.ExecutionOptions)
	runContextJSON, err := json.Marshal(runContext)
	if err != nil {
		logger.Error().Err(err).Msg("failed to marshal run context")
		util.RecordError(span, err)
		return err
	}

	_, err = tx.Exec(ctx, `INSERT INTO workflow_runs (id, workflow_definition_id, workflow_version, status, input_json, context_json, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5::jsonb,$6::jsonb,NOW(),NOW()) ON CONFLICT (id) DO NOTHING`,
		p.RunID, p.WorkflowID, p.WorkflowVersion, status, p.Input, runContextJSON)
	if err != nil {
		logger.Error().Err(err).Msg("failed to create workflow run")
		util.RecordError(span, err)
		return err
	}

	logger.Debug().Msg("created workflow run record")
	return nil
}

func (c *Coordinator) resolveNodeInputFromSpecRaw(specRaw json.RawMessage, runContext *model.RunContext, logger zerolog.Logger) (json.RawMessage, error) {
	var spec interface{}
	if len(specRaw) > 0 {
		if err := json.Unmarshal(specRaw, &spec); err != nil {
			logger.Error().Err(err).Msg("failed to unmarshal node spec for templating; using raw spec")
			// fall back to treating raw bytes as string
			spec = string(specRaw)
		}
	}

	resolvedParameters, err := c.Template.ResolveTemplates(spec, runContext)
	if err != nil {
		logger.Error().Err(err).Msg("failed to resolve templates")
		return nil, fmt.Errorf("failed to resolve templates: %w", err)
	}

	taskInput, err := json.Marshal(resolvedParameters)
	if err != nil {
		logger.Error().Err(err).Msg("failed to marshal task input")
		return json.RawMessage("{}"), nil
	}

	return taskInput, nil
}

func (c *Coordinator) insertTaskInstance(ctx context.Context, tx pgx.Tx, taskID, runID string, node *model.Node, taskInput json.RawMessage, scheduledAt time.Time, logger zerolog.Logger, span trace.Span) error {
	_, err := tx.Exec(ctx, `INSERT INTO task_instances (id, run_id, node_id, node_type, state, attempt, input_json, scheduled_at, created_at, updated_at)
        VALUES ($1,$2,$3,$4,'PENDING',0,$5::jsonb,$6,NOW(),NOW()) ON CONFLICT (id) DO NOTHING`,
		taskID, runID, node.NodeID, node.Runtime, taskInput, scheduledAt)
	if err != nil {
		logger.Error().Err(err).Msg("failed to create task instance")
		util.RecordError(span, err)
		return fmt.Errorf("failed to create task instance: %w", err)
	}

	return nil
}

func (c *Coordinator) recordTaskCreated(ctx context.Context, logger zerolog.Logger, span trace.Span, taskID, runID string, node *model.Node) {
	logger.Info().Msg("created task instance")
	util.RecordTaskCreated(ctx, taskID, runID, node.NodeID, node.Runtime)
	util.SetSpanAttributes(span,
		util.Attribute("task.id", taskID),
		util.Attribute("task.node_id", node.NodeID),
		util.Attribute("task.node_type", node.Runtime),
	)
}

func (c *Coordinator) insertTaskOutbox(ctx context.Context, tx pgx.Tx, taskID, runID string, node *model.Node, taskInput json.RawMessage, logger zerolog.Logger, span trace.Span) error {
	outboxID := uuid.New().String()
	outboxPayload := map[string]interface{}{
		"task_id":   taskID,
		"run_id":    runID,
		"node_id":   node.NodeID,
		"node_type": node.Runtime,
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

func (c *Coordinator) markTaskCompleted(ctx context.Context, tx pgx.Tx, runID, nodeID string, output json.RawMessage, logger zerolog.Logger, span trace.Span) (bool, error) {
	result, err := tx.Exec(ctx,
		`UPDATE task_instances 
             SET state='COMPLETED',
                 output_json=$1::jsonb,
                 completed_at=now(),
                 updated_at=now() 
         WHERE run_id=$2 AND node_id=$3 AND state IN ('RUNNING', 'PENDING')`,
		output, runID, nodeID)
	if err != nil {
		return false, fmt.Errorf("failed to update task instance: %w", err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return false, nil
	}
	logger.Debug().Int64("rows_updated", rowsAffected).Msg("marked task completed")
	return true, nil
}

// loadRunContext loads the run context without updating it (for read-only operations)
func (c *Coordinator) loadRunContext(ctx context.Context, tx pgx.Tx, runID string, logger zerolog.Logger, span trace.Span) (*model.RunContext, error) {
	var workflowInput json.RawMessage
	var runContextJSON json.RawMessage
	if err := tx.QueryRow(ctx, `SELECT input_json, context_json FROM workflow_runs WHERE id=$1`, runID).
		Scan(&workflowInput, &runContextJSON); err != nil {
		logger.Error().Err(err).Msg("failed to load workflow run context")
		util.RecordError(span, err)
		return nil, fmt.Errorf("failed to load workflow run context: %w", err)
	}

	runContext, err := c.mergeRunContext(workflowInput, runContextJSON)
	if err != nil {
		logger.Error().Err(err).Msg("failed to merge run context")
		util.RecordError(span, err)
		return nil, err
	}

	return runContext, nil
}

func (c *Coordinator) loadAndUpdateRunContext(ctx context.Context, tx pgx.Tx, runID, nodeID string, output json.RawMessage, logger zerolog.Logger, span trace.Span) (*model.RunContext, error) {
	var workflowInput json.RawMessage
	var runContextJSON json.RawMessage
	if err := tx.QueryRow(ctx, `SELECT input_json, context_json FROM workflow_runs WHERE id=$1 FOR UPDATE`, runID).
		Scan(&workflowInput, &runContextJSON); err != nil {
		logger.Error().Err(err).Msg("failed to load workflow run context")
		util.RecordError(span, err)
		return nil, fmt.Errorf("failed to load workflow run context: %w", err)
	}

	runContext, err := c.mergeRunContext(workflowInput, runContextJSON)
	if err != nil {
		logger.Error().Err(err).Msg("failed to merge run context")
		util.RecordError(span, err)
		return nil, err
	}

	var outputObj interface{}
	if len(output) > 0 {
		if err := json.Unmarshal(output, &outputObj); err != nil {
			outputObj = string(output)
		}
	}

	runContext.Tasks[nodeID] = &model.StepResult{
		Status: model.TaskInstanceStatusCompleted,
		Output: outputObj,
	}

	runContextBytes, err := json.Marshal(runContext)
	if err != nil {
		logger.Error().Err(err).Msg("failed to marshal run context")
		util.RecordError(span, err)
		return nil, fmt.Errorf("failed to marshal run context: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE workflow_runs SET context_json=$1, updated_at=now() WHERE id=$2`, runContextBytes, runID); err != nil {
		logger.Error().Err(err).Msg("failed to update workflow run context")
		util.RecordError(span, err)
		return nil, fmt.Errorf("failed to update workflow run context: %w", err)
	}

	return runContext, nil
}

func (c *Coordinator) mergeRunContext(workflowInput json.RawMessage, runContextJSON json.RawMessage) (*model.RunContext, error) {
	runContext := &model.RunContext{
		Input: workflowInput,
		Tasks: map[string]*model.StepResult{},
	}

	if len(runContextJSON) > 0 {
		if err := json.Unmarshal(runContextJSON, runContext); err != nil {
			return nil, fmt.Errorf("failed to unmarshal run context: %w", err)
		}
	}
	if runContext.Tasks == nil {
		runContext.Tasks = map[string]*model.StepResult{}
	}
	if len(runContext.Input) == 0 {
		runContext.Input = workflowInput
	}

	return runContext, nil
}

func (c *Coordinator) clearTaskLease(ctx context.Context, tx pgx.Tx, runID, nodeID string, logger zerolog.Logger, span trace.Span) error {
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

func (c *Coordinator) finalizeWorkflowIfComplete(ctx context.Context, tx pgx.Tx, runID, workflowID string, span trace.Span, logger zerolog.Logger) (bool, int, error) {
	var pending int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(1) FROM task_instances WHERE run_id=$1 AND state IN ('PENDING','RUNNING','QUEUED')`,
		runID).Scan(&pending); err != nil {
		logger.Error().Err(err).Msg("failed to check pending tasks")
		util.RecordError(span, err)
		return false, 0, fmt.Errorf("failed to check pending tasks: %w", err)
	}

	if pending == 0 {
		var workflowStartTime time.Time
		_ = tx.QueryRow(ctx, `SELECT created_at FROM workflow_runs WHERE id=$1`, runID).Scan(&workflowStartTime)
		workflowDuration := time.Since(workflowStartTime)

		if _, err := tx.Exec(ctx,
			`UPDATE workflow_runs SET status='SUCCEEDED', completed_at=now(), updated_at=now() WHERE id=$1`,
			runID); err != nil {
			logger.Error().Err(err).Msg("failed to mark workflow as succeeded")
			util.RecordError(span, err)
			return false, 0, fmt.Errorf("failed to mark workflow as succeeded: %w", err)
		}

		logger.Info().
			Dur("workflow_duration_ms", workflowDuration).
			Msg("workflow completed successfully")

		util.RecordWorkflowCompleted(ctx, workflowID, runID, workflowDuration)
		util.SetSpanAttributes(span,
			util.Attribute("workflow.status", "succeeded"),
			util.AttributeInt("workflow.duration_ms", workflowDuration.Milliseconds()),
		)

		return true, 0, nil
	}

	return false, pending, nil
}

func (c *Coordinator) taskInstanceExists(ctx context.Context, tx pgx.Tx, runID, nodeID string) (bool, string, error) {
	var existingTaskID string
	err := tx.QueryRow(ctx,
		`SELECT id FROM task_instances WHERE run_id=$1 AND node_id=$2`,
		runID, nodeID).Scan(&existingTaskID)

	if err != nil {
		if err == pgx.ErrNoRows {
			return false, "", nil
		}
		return false, "", fmt.Errorf("failed to check existing task instance: %w", err)
	}

	if existingTaskID != "" {
		return true, existingTaskID, nil
	}
	return false, "", nil
}

// startNextNodes starts the next nodes in a separate transaction
// This allows retrying if it fails without losing the completion record of the previous node
func (c *Coordinator) startNextNodes(ctx context.Context, span trace.Span, logger zerolog.Logger, runID, workflowID string, nextNodes []*model.Node, runContext *model.RunContext) error {
	return c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		// Create task instances and outbox entries for next nodes
		for _, nextNode := range nextNodes {
			// Check if task instance already exists (idempotency)
			exists, existingTaskID, err := c.taskInstanceExists(ctx, tx, runID, nextNode.NodeID)
			if err != nil {
				return err
			}
			if exists {
				logger.Debug().
					Str("next_node_id", nextNode.NodeID).
					Str("existing_task_id", existingTaskID).
					Msg("next task already exists, skipping")
				continue
			}

			// Create new task instance
			nextTaskID := uuid.New().String()

			taskInput, err := c.resolveNodeInputFromSpecRaw(nextNode.Spec, runContext, logger)
			if err != nil {
				return err
			}
			nextTaskLogger := logger.With().
				Str("next_task_id", nextTaskID).
				Str("next_node_id", nextNode.NodeID).
				Str("next_node_type", nextNode.Runtime).
				Logger()

			if err := c.insertTaskInstance(ctx, tx, nextTaskID, runID, nextNode, taskInput, time.Now(), nextTaskLogger, span); err != nil {
				return err
			}
			c.recordTaskCreated(ctx, nextTaskLogger, span, nextTaskID, runID, nextNode)

			if err := c.insertTaskOutbox(ctx, tx, nextTaskID, runID, nextNode, taskInput, nextTaskLogger, span); err != nil {
				return err
			}
		}

		logger.Info().
			Int("next_tasks_created", len(nextNodes)).
			Msg("next nodes started successfully")

		return nil
	})
}

// HandleNodeFailed marks a node as failed and handles workflow failure logic
func (c *Coordinator) HandleNodeFailed(ctx context.Context, runID string, nodeID string, errorMsg string) error {
	// Get workflow info for logging
	var workflowID string
	var workflowVersion int
	_ = c.DB.QueryRow(ctx, `SELECT workflow_definition_id, workflow_version FROM workflow_runs WHERE id=$1`, runID).
		Scan(&workflowID, &workflowVersion)

	// Start tracing span
	ctx, span := util.StartTaskSpan(ctx, "failed", "", runID, nodeID)
	defer util.EndSpan(span, nil)

	logger := c.Logger.With().
		Str("workflow_id", workflowID).
		Str("run_id", runID).
		Str("node_id", nodeID).
		Str("error", errorMsg).
		Logger()

	logger.Error().Msg("handling node failure")

	startTime := time.Now()

	err := c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		// Get task info for metrics
		var taskID string
		var nodeType string
		var taskStartTime time.Time
		_ = tx.QueryRow(ctx,
			`SELECT id, node_type, started_at FROM task_instances WHERE run_id=$1 AND node_id=$2`,
			runID, nodeID).Scan(&taskID, &nodeType, &taskStartTime)

		// Mark task instance as failed
		errorJSON, _ := json.Marshal(map[string]string{"error": errorMsg})
		result, err := tx.Exec(ctx,
			`UPDATE task_instances 
             SET state='FAILED',
                 last_error=$1::jsonb,
                 finished_at=now(),
                 updated_at=now() 
         WHERE run_id=$2 AND node_id=$3 AND state IN ('RUNNING', 'PENDING')`,
			errorJSON, runID, nodeID)
		if err != nil {
			logger.Error().Err(err).Msg("failed to update task instance")
			util.RecordError(span, err)
			return fmt.Errorf("failed to update task instance: %w", err)
		}

		rowsAffected := result.RowsAffected()
		if rowsAffected == 0 {
			// Task already failed or not found, return without error (idempotent)
			logger.Debug().Msg("task already failed (idempotent)")
			return nil
		}

		if err := c.clearTaskLease(ctx, tx, runID, nodeID, logger, span); err != nil {
			return err
		}

		taskDuration := time.Since(taskStartTime)
		if taskID != "" {
			logger.Error().
				Str("task_id", taskID).
				Dur("task_duration_ms", taskDuration).
				Msg("task marked as failed")
			util.RecordTaskFailed(ctx, taskID, runID, nodeID, nodeType, taskDuration, errorMsg)
			util.SetSpanAttributes(span,
				util.Attribute("task.id", taskID),
				util.Attribute("task.node_type", nodeType),
				util.Attribute("error.message", errorMsg),
			)
		}

		// Check if workflow should be marked as failed
		// For now, we mark workflow as failed if any critical node fails
		// This can be enhanced with retry logic or conditional failure handling
		var pending int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(1) FROM task_instances WHERE run_id=$1 AND state IN ('PENDING','RUNNING','QUEUED')`,
			runID).Scan(&pending); err != nil {
			logger.Error().Err(err).Msg("failed to check pending tasks")
			util.RecordError(span, err)
			return fmt.Errorf("failed to check pending tasks: %w", err)
		}

		// If no pending tasks and we have a failure, mark workflow as failed
		if pending == 0 {
			// Get workflow start time for duration calculation
			var workflowStartTime time.Time
			_ = c.DB.QueryRow(ctx, `SELECT created_at FROM workflow_runs WHERE id=$1`, runID).Scan(&workflowStartTime)
			workflowDuration := time.Since(workflowStartTime)

			_, err = tx.Exec(ctx,
				`UPDATE workflow_runs SET status='FAILED', updated_at=now() WHERE id=$1`,
				runID)
			if err != nil {
				logger.Error().Err(err).Msg("failed to mark workflow as failed")
				util.RecordError(span, err)
				return fmt.Errorf("failed to mark workflow as failed: %w", err)
			}

			logger.Error().
				Dur("workflow_duration_ms", workflowDuration).
				Msg("workflow marked as failed")

			util.RecordWorkflowFailed(ctx, workflowID, runID, workflowDuration, errorMsg)
			util.SetSpanAttributes(span,
				util.Attribute("workflow.status", "failed"),
				util.AttributeInt("workflow.duration_ms", workflowDuration.Milliseconds()),
			)
		} else {
			logger.Debug().Int("pending_tasks", pending).Msg("workflow still has pending tasks, not marking as failed yet")
		}

		return nil
	})

	if err != nil {
		return err
	}

	duration := time.Since(startTime)
	logger.Info().
		Dur("duration_ms", duration).
		Msg("node failure handled")

	return nil
}

// ExecuteWithTransaction executes a function within a transaction
func (c *Coordinator) ExecuteWithTransaction(ctx context.Context, span trace.Span, logger zerolog.Logger, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := c.DB.Begin(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("failed to begin transaction")
		util.RecordError(span, err)
		return err
	}
	defer tx.Rollback(ctx)

	if err := fn(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		logger.Error().Err(err).Msg("failed to commit transaction")
		util.RecordError(span, err)
		return err
	}

	return nil
}
