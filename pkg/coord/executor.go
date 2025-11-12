package coord

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mohitkumar/composer/pkg/model"
)

// StartWorkflowPayload represents starting a workflow run.
type StartWorkflowPayload struct {
	RunID            string          `json:"run_id"`
	WorkflowID       string          `json:"workflow_id"`
	WorkflowVersion  int             `json:"workflow_version"`
	Input            json.RawMessage `json:"input,omitempty"`
	StartedBy        string          `json:"started_by,omitempty"`
	ExecutionOptions json.RawMessage `json:"execution_options,omitempty"`
	StartNodeID      string          `json:"start_node_id,omitempty"`
	StartedAtUnixMs  int64           `json:"started_at_unix_ms,omitempty"`
}

// NodeCompletedPayload represents a completed node/task in a run.
type NodeCompletedPayload struct {
	RunID         string          `json:"run_id"`
	TaskInstance  string          `json:"task_instance_id"`
	NodeID        string          `json:"node_id"`
	Attempt       int32           `json:"attempt"`
	Output        json.RawMessage `json:"output,omitempty"`
	Error         *string         `json:"error,omitempty"`
	CompletedAtMs int64           `json:"completed_at_unix_ms,omitempty"`
	// Optional children to schedule atomically
	Children []struct {
		TaskInstanceID string          `json:"task_instance_id"`
		NodeID         string          `json:"node_id"`
		Input          json.RawMessage `json:"input"`
		ScheduledAtMs  int64           `json:"scheduled_at_unix_ms,omitempty"`
	} `json:"children,omitempty"`
}

// StartWorkflow creates a workflow_run and creates initial task instance from start node
func (c *Coordinator) StartWorkflow(ctx context.Context, p *StartWorkflowPayload) error {
	// Load workflow definition to get nodes and edges (before transaction)
	wfDef, err := c.GetWorkflow(ctx, p.WorkflowID, p.WorkflowVersion)
	if err != nil {
		return fmt.Errorf("failed to load workflow definition: %w", err)
	}

	// Parse workflow spec to get nodes and edges
	var wfSpec model.WorkflowDefinition
	if err := json.Unmarshal(wfDef.Spec, &wfSpec); err != nil {
		return fmt.Errorf("failed to parse workflow spec: %w", err)
	}

	// Now start transaction
	tx, err := c.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Create workflow run
	status := "RUNNING"
	_, err = tx.Exec(ctx, `INSERT INTO workflow_runs (id, workflow_definition_id, workflow_version, status, input_json, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5::jsonb,NOW(),NOW()) ON CONFLICT (id) DO NOTHING`,
		p.RunID, p.WorkflowID, p.WorkflowVersion, status, p.Input)
	if err != nil {
		return err
	}

	// Find start node using utility method
	startNode, err := wfSpec.FindStartNode(p.StartNodeID)
	if err != nil {
		return err
	}

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

	_, err = tx.Exec(ctx, `INSERT INTO task_instances (id, run_id, node_id, node_type, state, attempt, input_json, scheduled_at, created_at, updated_at)
        VALUES ($1,$2,$3,$4,'PENDING',0,$5::jsonb,$6,NOW(),NOW()) ON CONFLICT (id) DO NOTHING`,
		taskID, p.RunID, startNode.NodeID, startNode.Runtime, taskInput, scheduledAt)
	if err != nil {
		return fmt.Errorf("failed to create task instance: %w", err)
	}

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
		return fmt.Errorf("failed to create outbox entry: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
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
		return fmt.Errorf("failed to get workflow run: %w", err)
	}

	// Load workflow definition to get DAG structure (before transaction)
	wfDef, err := c.GetWorkflow(ctx, workflowID, workflowVersion)
	if err != nil {
		return fmt.Errorf("failed to load workflow definition: %w", err)
	}

	// Parse workflow spec
	var wfSpec model.WorkflowDefinition
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

	// If no next nodes, check if workflow is complete
	if len(nextNodes) == 0 {
		var pending int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(1) FROM task_instances WHERE run_id=$1 AND state IN ('PENDING','RUNNING','QUEUED')`,
			runID).Scan(&pending); err != nil {
			return fmt.Errorf("failed to check pending tasks: %w", err)
		}

		if pending == 0 {
			_, err = tx.Exec(ctx,
				`UPDATE workflow_runs SET status='SUCCEEDED', completed_at=now(), updated_at=now() WHERE id=$1`,
				runID)
			if err != nil {
				return fmt.Errorf("failed to mark workflow as succeeded: %w", err)
			}
			return tx.Commit(ctx)
		}
		// Still have pending tasks, just commit
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
			continue
		}

		// Create new task instance
		taskID := uuid.New().String()

		// Use output from completed node as input for next node (can be enhanced with data transformation)
		nextInput := output
		if len(nextInput) == 0 {
			nextInput = json.RawMessage("{}")
		}

		_, err = tx.Exec(ctx, `INSERT INTO task_instances (id, run_id, node_id, node_type, state, attempt, input_json, scheduled_at, created_at, updated_at)
            VALUES ($1,$2,$3,$4,'PENDING',0,$5::jsonb,NOW(),NOW(),NOW()) ON CONFLICT (id) DO NOTHING`,
			taskID, runID, nextNode.NodeID, nextNode.Runtime, nextInput)
		if err != nil {
			return fmt.Errorf("failed to create task instance for node %s: %w", nextNode.NodeID, err)
		}

		// Create outbox entry
		outboxID := uuid.New().String()
		outboxPayload := map[string]interface{}{
			"task_id":   taskID,
			"run_id":    runID,
			"node_id":   nextNode.NodeID,
			"node_type": nextNode.Runtime,
			"input":     string(nextInput),
		}
		outboxPayloadBytes, err := json.Marshal(outboxPayload)
		if err != nil {
			return fmt.Errorf("failed to marshal outbox payload: %w", err)
		}

		_, err = tx.Exec(ctx, `INSERT INTO outbox (id, stream, payload, type, status, next_attempt_at, attempt_count, max_retries, created_at, updated_at)
            VALUES ($1,'workflow.tasks',$2::jsonb,'TASK_EXECUTION','PENDING',NOW(),0,5,NOW(),NOW())`,
			outboxID, outboxPayloadBytes)
		if err != nil {
			return fmt.Errorf("failed to create outbox entry: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// HandleNodeFailed marks a node as failed and handles workflow failure logic
func (c *Coordinator) HandleNodeFailed(ctx context.Context, runID string, nodeID string, errorMsg string) error {
	tx, err := c.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Mark task instance as failed
	errorJSON, _ := json.Marshal(map[string]string{"error": errorMsg})
	result, err := tx.Exec(ctx,
		`UPDATE task_instances SET state='FAILED', last_error=$1::jsonb, finished_at=now(), updated_at=now() 
         WHERE run_id=$2 AND node_id=$3 AND state IN ('RUNNING', 'PENDING')`,
		errorJSON, runID, nodeID)
	if err != nil {
		return fmt.Errorf("failed to update task instance: %w", err)
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		// Task already failed or not found, return without error (idempotent)
		return tx.Commit(ctx)
	}

	// Check if workflow should be marked as failed
	// For now, we mark workflow as failed if any critical node fails
	// This can be enhanced with retry logic or conditional failure handling
	var pending int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(1) FROM task_instances WHERE run_id=$1 AND state IN ('PENDING','RUNNING','QUEUED')`,
		runID).Scan(&pending); err != nil {
		return fmt.Errorf("failed to check pending tasks: %w", err)
	}

	// If no pending tasks and we have a failure, mark workflow as failed
	if pending == 0 {
		_, err = tx.Exec(ctx,
			`UPDATE workflow_runs SET status='FAILED', updated_at=now() WHERE id=$1`,
			runID)
		if err != nil {
			return fmt.Errorf("failed to mark workflow as failed: %w", err)
		}
	}

	return tx.Commit(ctx)
}
