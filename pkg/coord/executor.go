package coord

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/util"
)

// StartWorkflow creates a workflow_run and creates initial task instance from start node
func (c *Coordinator) StartWorkflow(ctx context.Context, p *model.StartWorkflowRequest) error {
	// Start tracing span
	ctx, span := util.StartWorkflowSpan(ctx, "start", p.WorkflowID, p.RunID)
	defer util.EndSpan(span, nil)

	logger := c.Log.With().
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

	logger.Debug().
		Int("node_count", len(wfSpec.Nodes)).
		Int("edge_count", len(wfSpec.Edges)).
		Msg("parsed workflow spec")

	// Now start transaction
	tx, err := c.DB.Begin(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("failed to begin transaction")
		util.RecordError(span, err)
		return err
	}
	defer tx.Rollback(ctx)

	// Create workflow run
	status := "RUNNING"
	_, err = tx.Exec(ctx, `INSERT INTO workflow_runs (id, workflow_definition_id, workflow_version, status, input_json, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5::jsonb,NOW(),NOW()) ON CONFLICT (id) DO NOTHING`,
		p.RunID, p.WorkflowID, p.WorkflowVersion, status, p.Input)
	if err != nil {
		logger.Error().Err(err).Msg("failed to create workflow run")
		util.RecordError(span, err)
		return err
	}

	logger.Debug().Msg("created workflow run record")

	// Find start node using utility method
	startNode, err := wfSpec.FindStartNode(p.StartNodeID)
	if err != nil {
		logger.Error().Err(err).Str("start_node_id", p.StartNodeID).Msg("failed to find start node")
		util.RecordError(span, err)
		return err
	}

	logger.Debug().
		Str("start_node_id", startNode.NodeID).
		Str("start_node_type", startNode.Runtime).
		Msg("found start node")

	// Create task instance for start node
	taskID := uuid.New().String()
	scheduledAt := time.Now()
	if p.StartedAtUnixMs > 0 {
		scheduledAt = time.UnixMilli(p.StartedAtUnixMs)
	}

	// Use workflow input as initial task input
	taskInput := p.Input
	if len(taskInput) == 0 {
		taskInput = json.RawMessage("{}")
	}

	taskLogger := logger.With().
		Str("task_id", taskID).
		Str("node_id", startNode.NodeID).
		Str("node_type", startNode.Runtime).
		Logger()

	_, err = tx.Exec(ctx, `INSERT INTO task_instances (id, run_id, node_id, node_type, state, attempt, input_json, scheduled_at, created_at, updated_at)
        VALUES ($1,$2,$3,$4,'PENDING',0,$5::jsonb,$6,NOW(),NOW()) ON CONFLICT (id) DO NOTHING`,
		taskID, p.RunID, startNode.NodeID, startNode.Runtime, taskInput, scheduledAt)
	if err != nil {
		taskLogger.Error().Err(err).Msg("failed to create task instance")
		util.RecordError(span, err)
		return fmt.Errorf("failed to create task instance: %w", err)
	}

	taskLogger.Info().Msg("created initial task instance")
	util.RecordTaskCreated(ctx, taskID, p.RunID, startNode.NodeID, startNode.Runtime)
	util.SetSpanAttributes(span,
		util.Attribute("task.id", taskID),
		util.Attribute("task.node_id", startNode.NodeID),
		util.Attribute("task.node_type", startNode.Runtime),
	)

	// Create outbox entry for the task
	outboxID := uuid.New().String()
	outboxPayload := map[string]interface{}{
		"task_id":   taskID,
		"run_id":    p.RunID,
		"node_id":   startNode.NodeID,
		"node_type": startNode.Runtime,
		"input":     string(taskInput),
	}
	outboxPayloadBytes, err := json.Marshal(outboxPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal outbox payload: %w", err)
	}

	_, err = tx.Exec(ctx, `INSERT INTO outbox (id, stream, payload, type, status, next_attempt_at, attempt_count, max_retries, created_at, updated_at)
        VALUES ($1,'workflow.tasks',$2::jsonb,'TASK_EXECUTION','PENDING',NOW(),0,5,NOW(),NOW())`,
		outboxID, outboxPayloadBytes)
	if err != nil {
		taskLogger.Error().Err(err).Msg("failed to create outbox entry")
		util.RecordError(span, err)
		return fmt.Errorf("failed to create outbox entry: %w", err)
	}

	taskLogger.Debug().Str("outbox_id", outboxID).Msg("created outbox entry for task")

	if err := tx.Commit(ctx); err != nil {
		logger.Error().Err(err).Msg("failed to commit transaction")
		util.RecordError(span, err)
		return err
	}

	duration := time.Since(startTime)
	logger.Info().
		Dur("duration_ms", duration).
		Str("task_id", taskID).
		Msg("workflow started successfully")

	util.RecordWorkflowStarted(ctx, p.WorkflowID, p.RunID)
	util.SetSpanAttributes(span,
		util.AttributeInt("workflow.duration_ms", duration.Milliseconds()),
	)

	return nil
}

// HandleNodeCompleted marks a node as completed and dispatches next tasks from the workflow DAG
func (c *Coordinator) HandleNodeCompleted(ctx context.Context, runID string, nodeID string, output json.RawMessage) error {
	// Get workflow run info to load workflow definition (before transaction)
	var workflowID string
	var workflowVersion int
	err := c.DB.QueryRow(ctx, `SELECT workflow_definition_id, workflow_version FROM workflow_runs WHERE id=$1`, runID).
		Scan(&workflowID, &workflowVersion)
	if err != nil {
		c.Log.Error().Err(err).Str("run_id", runID).Msg("failed to get workflow run")
		return fmt.Errorf("failed to get workflow run: %w", err)
	}

	// Start tracing span
	ctx, span := util.StartTaskSpan(ctx, "completed", "", runID, nodeID)
	defer util.EndSpan(span, nil)

	logger := c.Log.With().
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

	// Now start transaction
	tx, err := c.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Mark task instance as completed
	result, err := tx.Exec(ctx,
		`UPDATE task_instances SET state='COMPLETED', output_json=$1::jsonb, completed_at=now(), updated_at=now() 
         WHERE run_id=$2 AND node_id=$3 AND state IN ('RUNNING', 'PENDING')`,
		output, runID, nodeID)
	if err != nil {
		return fmt.Errorf("failed to update task instance: %w", err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		// Task already completed or not found, return without error (idempotent)
		return tx.Commit(ctx)
	}

	// Find next nodes using utility method
	nextNodes := wfSpec.FindNextNodes(nodeID)

	logger.Debug().Int("next_nodes_count", len(nextNodes)).Msg("found next nodes")

	// If no next nodes, check if workflow is complete
	if len(nextNodes) == 0 {
		var pending int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(1) FROM task_instances WHERE run_id=$1 AND state IN ('PENDING','RUNNING','QUEUED')`,
			runID).Scan(&pending); err != nil {
			logger.Error().Err(err).Msg("failed to check pending tasks")
			util.RecordError(span, err)
			return fmt.Errorf("failed to check pending tasks: %w", err)
		}

		if pending == 0 {
			// Get workflow start time for duration calculation
			var workflowStartTime time.Time
			_ = c.DB.QueryRow(ctx, `SELECT created_at FROM workflow_runs WHERE id=$1`, runID).Scan(&workflowStartTime)
			workflowDuration := time.Since(workflowStartTime)

			_, err = tx.Exec(ctx,
				`UPDATE workflow_runs SET status='SUCCEEDED', completed_at=now(), updated_at=now() WHERE id=$1`,
				runID)
			if err != nil {
				logger.Error().Err(err).Msg("failed to mark workflow as succeeded")
				util.RecordError(span, err)
				return fmt.Errorf("failed to mark workflow as succeeded: %w", err)
			}

			logger.Info().
				Dur("workflow_duration_ms", workflowDuration).
				Msg("workflow completed successfully")

			util.RecordWorkflowCompleted(ctx, workflowID, runID, workflowDuration)
			util.SetSpanAttributes(span,
				util.Attribute("workflow.status", "succeeded"),
				util.AttributeInt("workflow.duration_ms", workflowDuration.Milliseconds()),
			)

			return tx.Commit(ctx)
		}
		// Still have pending tasks, just commit
		logger.Debug().Int("pending_tasks", pending).Msg("workflow still has pending tasks")
		return tx.Commit(ctx)
	}

	// Create task instances and outbox entries for next nodes
	for _, nextNode := range nextNodes {
		// Check if task instance already exists (idempotency)
		var existingTaskID string
		err = tx.QueryRow(ctx,
			`SELECT id FROM task_instances WHERE run_id=$1 AND node_id=$2`,
			runID, nextNode.NodeID).Scan(&existingTaskID)

		if err == nil && existingTaskID != "" {
			// Task already exists, skip
			logger.Debug().
				Str("next_node_id", nextNode.NodeID).
				Str("existing_task_id", existingTaskID).
				Msg("next task already exists, skipping")
			continue
		}

		// Create new task instance
		nextTaskID := uuid.New().String()

		// Use output from completed node as input for next node (can be enhanced with data transformation)
		nextInput := output
		if len(nextInput) == 0 {
			nextInput = json.RawMessage("{}")
		}

		nextTaskLogger := logger.With().
			Str("next_task_id", nextTaskID).
			Str("next_node_id", nextNode.NodeID).
			Str("next_node_type", nextNode.Runtime).
			Logger()

		_, err = tx.Exec(ctx, `INSERT INTO task_instances (id, run_id, node_id, node_type, state, attempt, input_json, scheduled_at, created_at, updated_at)
            VALUES ($1,$2,$3,$4,'PENDING',0,$5::jsonb,NOW(),NOW(),NOW()) ON CONFLICT (id) DO NOTHING`,
			nextTaskID, runID, nextNode.NodeID, nextNode.Runtime, nextInput)
		if err != nil {
			nextTaskLogger.Error().Err(err).Msg("failed to create task instance for next node")
			util.RecordError(span, err)
			return fmt.Errorf("failed to create task instance for node %s: %w", nextNode.NodeID, err)
		}

		nextTaskLogger.Info().Msg("created task instance for next node")
		util.RecordTaskCreated(ctx, nextTaskID, runID, nextNode.NodeID, nextNode.Runtime)

		// Create outbox entry
		outboxID := uuid.New().String()
		outboxPayload := map[string]interface{}{
			"task_id":   nextTaskID,
			"run_id":    runID,
			"node_id":   nextNode.NodeID,
			"node_type": nextNode.Runtime,
			"input":     string(nextInput),
		}
		outboxPayloadBytes, err := json.Marshal(outboxPayload)
		if err != nil {
			nextTaskLogger.Error().Err(err).Msg("failed to marshal outbox payload")
			util.RecordError(span, err)
			return fmt.Errorf("failed to marshal outbox payload: %w", err)
		}

		_, err = tx.Exec(ctx, `INSERT INTO outbox (id, stream, payload, type, status, next_attempt_at, attempt_count, max_retries, created_at, updated_at)
            VALUES ($1,'workflow.tasks',$2::jsonb,'TASK_EXECUTION','PENDING',NOW(),0,5,NOW(),NOW())`,
			outboxID, outboxPayloadBytes)
		if err != nil {
			nextTaskLogger.Error().Err(err).Msg("failed to create outbox entry")
			util.RecordError(span, err)
			return fmt.Errorf("failed to create outbox entry: %w", err)
		}

		nextTaskLogger.Debug().Str("outbox_id", outboxID).Msg("created outbox entry for next task")
	}

	duration := time.Since(startTime)
	logger.Info().
		Dur("duration_ms", duration).
		Int("next_tasks_created", len(nextNodes)).
		Msg("node completion handled successfully")

	return tx.Commit(ctx)
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

	logger := c.Log.With().
		Str("workflow_id", workflowID).
		Str("run_id", runID).
		Str("node_id", nodeID).
		Str("error", errorMsg).
		Logger()

	logger.Error().Msg("handling node failure")

	startTime := time.Now()

	tx, err := c.DB.Begin(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("failed to begin transaction")
		util.RecordError(span, err)
		return err
	}
	defer tx.Rollback(ctx)

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
		`UPDATE task_instances SET state='FAILED', last_error=$1::jsonb, finished_at=now(), updated_at=now() 
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
		return tx.Commit(ctx)
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

	duration := time.Since(startTime)
	logger.Info().
		Dur("duration_ms", duration).
		Msg("node failure handled")

	return tx.Commit(ctx)
}
