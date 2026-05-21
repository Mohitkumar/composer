package coord

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/util"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/trace"
)

// StartWorkflow creates a workflow_run and schedules the initial task from the start node.
func (c *Coordinator) StartWorkflow(ctx context.Context, p *model.StartWorkflowRequest) error {
	if p.RunID == "" {
		p.RunID = uuid.New().String()
	}

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

	dag, err := c.loadDAG(ctx, p.WorkflowID, p.WorkflowVersion)
	if err != nil {
		logger.Error().Err(err).Msg("failed to load workflow definition")
		util.RecordError(span, err)
		return err
	}

	runContext := c.newRunContext(p.Input, p.ExecutionOptions)
	execState := model.NewExecutionState(dag, runContext, c.Template)

	err = c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		if err := c.Repository.InsertWorkflowRun(ctx, tx, p, logger, span); err != nil {
			return err
		}

		startNode, err := execState.StartNode(p.StartNodeID)
		if err != nil {
			logger.Error().Err(err).Str("start_node_id", p.StartNodeID).Msg("failed to find start node")
			util.RecordError(span, err)
			return err
		}

		taskID := uuid.New().String()
		scheduledAt := time.Now()
		if p.StartedAtUnixMs > 0 {
			scheduledAt = time.UnixMilli(p.StartedAtUnixMs)
		}

		taskInput, err := execState.ResolveInput(startNode)
		if err != nil {
			return err
		}

		taskLogger := logger.With().
			Str("task_id", taskID).
			Str("node_id", startNode.NodeID).
			Str("node_type", startNode.GetNodeTypeForRouting()).
			Logger()

		if err := c.Repository.InsertTaskInstance(ctx, tx, taskID, p.RunID, startNode, taskInput, scheduledAt, taskLogger, span); err != nil {
			return err
		}
		c.recordTaskCreated(ctx, taskLogger, span, taskID, p.RunID, startNode)

		return c.Repository.InsertTaskOutbox(ctx, tx, taskID, p.RunID, p.WorkflowID, p.WorkflowVersion, startNode, taskInput, taskLogger, span)
	})
	if err != nil {
		return err
	}

	duration := time.Since(startTime)
	util.RecordWorkflowStarted(ctx, p.WorkflowID, p.RunID)
	util.SetSpanAttributes(span, util.AttributeInt("workflow.duration_ms", duration.Milliseconds()))
	return nil
}

// HandleNodeCompleted is the hot path. Structure:
//
//  1. Txn A  — record completion, load updated RunContext
//  2. Build ExecutionState (no DB)
//  3. If branch node: Txn B (FOR UPDATE) — count completed siblings
//  4. NextNodes — pure state machine, no DB
//  5. Txn C  — persist RoutingResult (control nodes + executable tasks)
func (c *Coordinator) HandleNodeCompleted(ctx context.Context, taskID, runID, nodeID, workflowID string, workflowVersion int, output json.RawMessage) error {
	ctx, span := util.StartTaskSpan(ctx, "completed", "", runID, nodeID)
	defer util.EndSpan(span, nil)

	logger := c.Logger.With().
		Str("workflow_id", workflowID).
		Str("run_id", runID).
		Str("node_id", nodeID).
		Logger()

	logger.Info().Msg("handling node completion")
	startTime := time.Now()

	// ── Txn A: record completion ────────────────────────────────────────────────
	dag, err := c.loadDAG(ctx, workflowID, workflowVersion)
	if err != nil {
		return err
	}

	var runContext *model.RunContext
	var alreadyCompleted bool
	var completedTaskID, completedNodeType string
	var completedStartedAt time.Time
	err = c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		var updated bool
		var err error
		updated, completedTaskID, completedNodeType, completedStartedAt, err = c.Repository.MarkTaskCompleted(ctx, tx, runID, nodeID, output, logger, span)
		if err != nil {
			return err
		}
		if !updated {
			alreadyCompleted = true
			return nil
		}
		if err := c.Repository.ClearTaskLease(ctx, tx, runID, nodeID, logger, span); err != nil {
			return err
		}
		runContext, err = c.Repository.LoadAndUpdateRunContext(ctx, tx, runID, nodeID, output, logger, span)
		return err
	})
	if err != nil {
		logger.Error().Err(err).Msg("failed to record node completion")
		return fmt.Errorf("failed to record node completion: %w", err)
	}
	if alreadyCompleted {
		logger.Debug().Msg("node already completed (duplicate delivery), skipping")
		return nil
	}

	// Always record task completion counter. Duration is only meaningful when the
	// task was leased by a worker (started_at set), so pass 0 when it's missing and
	// RecordTaskCompleted will skip the histogram observation for zero durations.
	taskDuration := time.Duration(0)
	if !completedStartedAt.IsZero() {
		taskDuration = time.Since(completedStartedAt)
	}
	util.RecordTaskCompleted(ctx, completedTaskID, runID, nodeID, completedNodeType, taskDuration)
	util.SetSpanAttributes(span,
		util.Attribute("task.id", completedTaskID),
		util.Attribute("task.node_type", completedNodeType),
	)

	// ── Build state machine ─────────────────────────────────────────────────────
	execState := model.NewExecutionState(dag, runContext, c.Template)

	completedNode := execState.GetNode(nodeID)
	if completedNode == nil {
		logger.Error().Str("node_id", nodeID).Msg("completed node not found in workflow spec")
		return fmt.Errorf("node %s not found in workflow spec", nodeID)
	}

	// ── Txn B (branch nodes only): count completed siblings ─────────────────────
	completedBranchCount := 0
	if execState.IsBranchNode(nodeID) {
		siblings := execState.BranchSiblingIDs(nodeID)
		err = c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
			completedBranchCount, err = c.Repository.CountCompletedBranches(ctx, tx, runID, siblings)
			return err
		})
		if err != nil {
			logger.Error().Err(err).Msg("failed to count completed branches")
			return fmt.Errorf("failed to count completed branches: %w", err)
		}
	}

	// ── Route (pure state machine, no DB) ───────────────────────────────────────
	routing, err := execState.NextNodes(nodeID, completedBranchCount)
	if err != nil {
		logger.Error().Err(err).Str("node_type", string(completedNode.Type)).Msg("routing failed")
		return fmt.Errorf("routing failed for node %s: %w", nodeID, err)
	}

	logger.Debug().
		Str("node_type", string(completedNode.Type)).
		Int("executable_nodes", len(routing.ExecutableNodes)).
		Int("control_nodes", len(routing.ControlNodes)).
		Msg("routing resolved")

	// ── Persist result ──────────────────────────────────────────────────────────
	if routing.IsEmpty() {
		err = c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
			done, pending, err := c.finalizeWorkflowIfComplete(ctx, tx, runID, workflowID, span, logger)
			if err != nil {
				return err
			}
			if !done {
				logger.Debug().Int("pending_tasks", pending).Msg("workflow still has pending tasks")
			}
			return nil
		})
		if err != nil {
			logger.Error().Err(err).Msg("failed to finalize workflow completion check")
		}
	} else {
		celFailed, totalExec, persistErr := c.persistRoutingResult(ctx, span, logger, runID, workflowID, workflowVersion, execState, routing)
		if persistErr != nil {
			failedNodeID := "unknown"
			if len(routing.ExecutableNodes) > 0 {
				failedNodeID = routing.ExecutableNodes[0].NodeID
			}
			logger.Error().Err(persistErr).
				Str("failed_node_id", failedNodeID).
				Str("error_type", "TASK_INSTANCE_CREATION_ERROR").
				Msg("failed to persist routing result")
			markErr := c.markWorkflowFailedWithReason(ctx, runID, workflowID,
				fmt.Sprintf("failed to create task instance for node %s: %v", failedNodeID, persistErr), logger, span)
			if markErr != nil {
				logger.Error().Err(markErr).Msg("failed to mark workflow as failed")
			}
		} else if totalExec > 0 && celFailed == totalExec {
			logger.Warn().Int("failed_nodes", celFailed).Msg("all next nodes failed CEL resolution, marking workflow as failed")
			markErr := c.markWorkflowFailedWithReason(ctx, runID, workflowID,
				fmt.Sprintf("all next nodes (%d) failed CEL resolution", celFailed), logger, span)
			if markErr != nil {
				logger.Error().Err(markErr).Msg("failed to mark workflow as failed")
			}
		}
	}

	duration := time.Since(startTime)
	logger.Info().
		Dur("duration_ms", duration).
		Int("next_tasks_count", len(routing.ExecutableNodes)).
		Msg("node completion handled")
	return nil
}

// persistRoutingResult persists a RoutingResult in a single transaction (Txn C).
// Returns (celFailedCount, totalExecutableCount, error).
//
// Control nodes (SWITCH/FORK) are created as instantly-completed task instances.
// The updated RunContext (which reflects SWITCH pass-through outputs) is written.
// Executable nodes get task instances and outbox entries; CEL failures become FAILED instances.
func (c *Coordinator) persistRoutingResult(
	ctx context.Context,
	span trace.Span,
	logger zerolog.Logger,
	runID, workflowID string,
	workflowVersion int,
	execState *model.ExecutionState,
	routing *model.RoutingResult,
) (celFailed, totalExec int, err error) {
	err = c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		// ── 1. Persist control nodes as instantly-completed ──────────────────────
		for _, cn := range routing.ControlNodes {
			exists, existingTaskID, existingState, err := c.Repository.TaskInstanceExists(ctx, tx, runID, cn.Node.NodeID)
			if err != nil {
				return err
			}
			if exists && existingState != "FAILED" {
				continue // idempotent — already persisted
			}

			taskID := uuid.New().String()
			if exists && existingState == "FAILED" {
				taskID = existingTaskID
			}

			if err := c.Repository.InsertTaskInstance(ctx, tx, taskID, runID, cn.Node, json.RawMessage("{}"), time.Now(), logger, span); err != nil {
				logger.Error().Err(err).Str("node_id", cn.Node.NodeID).Msg("failed to create control node task instance")
				return fmt.Errorf("failed to create task instance for control node %s: %w", cn.Node.NodeID, err)
			}
			if _, _, _, _, err := c.Repository.MarkTaskCompleted(ctx, tx, runID, cn.Node.NodeID, cn.Output, logger, span); err != nil {
				logger.Error().Err(err).Str("node_id", cn.Node.NodeID).Msg("failed to mark control node as completed")
				return fmt.Errorf("failed to mark control node %s as completed: %w", cn.Node.NodeID, err)
			}
			c.recordTaskCreated(ctx, logger, span, taskID, runID, cn.Node)
		}

		// ── 2. Persist the updated run context ──────────────────────────────────
		// ExecutionState updated in-memory ctx as SWITCH nodes were traversed.
		if len(routing.ControlNodes) > 0 {
			if err := c.Repository.UpdateRunContext(ctx, tx, runID, execState.RunContext(), logger, span); err != nil {
				return err
			}
		}

		// ── 3. Create task instances for executable (worker) nodes ───────────────
		totalExec = len(routing.ExecutableNodes)
		var celErrors []string
		for _, node := range routing.ExecutableNodes {
			exists, existingTaskID, existingState, err := c.Repository.TaskInstanceExists(ctx, tx, runID, node.NodeID)
			if err != nil {
				return err
			}
			if exists && existingState != "FAILED" {
				logger.Debug().
					Str("node_id", node.NodeID).
					Str("existing_task_id", existingTaskID).
					Str("existing_state", existingState).
					Msg("task already exists and is not failed, skipping")
				totalExec-- // don't count pre-existing tasks towards the CEL-fail check
				continue
			}

			nextTaskID := uuid.New().String()
			if exists && existingState == "FAILED" {
				logger.Debug().Str("node_id", node.NodeID).Msg("recreating task instance for failed node")
				nextTaskID = existingTaskID
			}

			taskLogger := logger.With().
				Str("next_task_id", nextTaskID).
				Str("next_node_id", node.NodeID).
				Str("next_node_type", node.GetNodeTypeForRouting()).
				Logger()

			taskInput, err := execState.ResolveInput(node)
			if err != nil {
				// CEL resolution failure: persist as FAILED task so the run is visible
				celError := fmt.Sprintf("failed to resolve CEL expressions: %v", err)
				taskLogger.Error().Err(err).Str("error_type", "CEL_RESOLUTION_ERROR").
					Msg("CEL resolution failed — creating task instance as FAILED")
				if insertErr := c.Repository.InsertTaskInstanceWithState(ctx, tx, nextTaskID, runID, node,
					json.RawMessage("{}"), time.Now(), "FAILED", celError, taskLogger, span); insertErr != nil {
					taskLogger.Error().Err(insertErr).Msg("failed to insert FAILED task instance")
					return fmt.Errorf("failed to create FAILED task instance for node %s: %w", node.NodeID, insertErr)
				}
				celErrors = append(celErrors, fmt.Sprintf("node %s: %v", node.NodeID, err))
				celFailed++
				c.recordTaskCreated(ctx, taskLogger, span, nextTaskID, runID, node)
				continue
			}

			if err := c.Repository.InsertTaskInstance(ctx, tx, nextTaskID, runID, node, taskInput, time.Now(), taskLogger, span); err != nil {
				taskLogger.Error().Err(err).Str("error_type", "TASK_INSTANCE_CREATION_ERROR").
					Msg("failed to insert task instance")
				return fmt.Errorf("failed to create task instance for node %s: %w", node.NodeID, err)
			}
			c.recordTaskCreated(ctx, taskLogger, span, nextTaskID, runID, node)

			if err := c.Repository.InsertTaskOutbox(ctx, tx, nextTaskID, runID, workflowID, workflowVersion, node, taskInput, taskLogger, span); err != nil {
				taskLogger.Error().Err(err).Str("error_type", "OUTBOX_CREATION_ERROR").
					Msg("failed to create outbox entry")
				return fmt.Errorf("failed to create outbox entry for node %s: %w", node.NodeID, err)
			}
		}

		if len(celErrors) > 0 {
			logger.Warn().
				Strs("cel_resolution_errors", celErrors).
				Int("failed_nodes_count", len(celErrors)).
				Msg("some nodes failed CEL resolution and were marked as FAILED")
		}

		logger.Info().
			Int("control_nodes_persisted", len(routing.ControlNodes)).
			Int("tasks_created", totalExec-celFailed).
			Int("failed_tasks_created", celFailed).
			Msg("routing result persisted")
		return nil
	})
	return celFailed, totalExec, err
}

// HandleNodeFailed marks a node as failed and propagates workflow failure if no tasks remain.
func (c *Coordinator) HandleNodeFailed(ctx context.Context, runID, nodeID, errorMsg, workflowID string) error {
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
		taskID, nodeType, taskStartTime, err := c.Repository.GetTaskInstanceInfo(ctx, tx, runID, nodeID)
		if err != nil {
			logger.Warn().Err(err).Msg("failed to get task instance info, continuing with failure handling")
		}

		updated, err := c.Repository.MarkTaskFailed(ctx, tx, runID, nodeID, errorMsg, logger, span)
		if err != nil {
			util.RecordError(span, err)
			return fmt.Errorf("failed to update task instance: %w", err)
		}
		if !updated {
			logger.Debug().Msg("task already failed (idempotent)")
			return nil
		}

		if err := c.Repository.ClearTaskLease(ctx, tx, runID, nodeID, logger, span); err != nil {
			return err
		}

		taskDuration := time.Since(taskStartTime)
		if taskID != "" {
			logger.Error().Str("task_id", taskID).Dur("task_duration_ms", taskDuration).Msg("task marked as failed")
			util.RecordTaskFailed(ctx, taskID, runID, nodeID, nodeType, taskDuration, errorMsg)
			util.SetSpanAttributes(span,
				util.Attribute("task.id", taskID),
				util.Attribute("task.node_type", nodeType),
				util.Attribute("error.message", errorMsg),
			)
		}

		done, pending, err := c.Repository.CheckPendingTasks(ctx, tx, runID)
		if err != nil {
			util.RecordError(span, err)
			return fmt.Errorf("failed to check pending tasks: %w", err)
		}
		if done {
			if err := c.Repository.MarkWorkflowFailed(ctx, tx, runID, workflowID, errorMsg, logger, span); err != nil {
				return err
			}
			logger.Error().Msg("workflow marked as failed")
		} else {
			logger.Debug().Int("pending_tasks", pending).Msg("workflow still has pending tasks, not marking as failed yet")
		}
		return nil
	})
	if err != nil {
		return err
	}

	logger.Info().Dur("duration_ms", time.Since(startTime)).Msg("node failure handled")
	return nil
}

// RetryWorkflowInstance restarts a workflow run from a given node,
// optionally replacing the run's input and execution options.
func (c *Coordinator) RetryWorkflowInstance(ctx context.Context, p *model.RetryWorkflowRequest) error {
	ctx, span := util.StartWorkflowSpan(ctx, "retry", "", p.RunID)
	defer util.EndSpan(span, nil)

	logger := c.Logger.With().
		Str("run_id", p.RunID).
		Str("node_id", p.NodeID).
		Int("workflow_version", p.WorkflowVersion).
		Logger()

	logger.Info().Msg("retrying workflow instance")
	startTime := time.Now()

	workflowID, _, _, err := c.Repository.GetWorkflowRunForRetry(ctx, p.RunID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("workflow instance not found: %s", p.RunID)
		}
		util.RecordError(span, err)
		return fmt.Errorf("failed to get workflow instance: %w", err)
	}
	logger = logger.With().Str("workflow_id", workflowID).Logger()

	workflowVersion := p.WorkflowVersion
	if workflowVersion == 0 {
		deployedWorkflow, err := c.GetWorkflow(ctx, workflowID, 0)
		if err != nil {
			util.RecordError(span, err)
			return fmt.Errorf("failed to get deployed workflow version: %w", err)
		}
		if !deployedWorkflow.Deployed {
			return fmt.Errorf("workflow has no deployed version")
		}
		workflowVersion = deployedWorkflow.Version
		logger = logger.With().Int("workflow_version", workflowVersion).Logger()
	}

	dag, err := c.loadDAG(ctx, workflowID, workflowVersion)
	if err != nil {
		util.RecordError(span, err)
		return err
	}

	// ExecutionState is constructed inside the transaction so it has the loaded RunContext.
	err = c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		runContext, err := c.Repository.LoadRunContext(ctx, tx, p.RunID, logger, span)
		if err != nil {
			return err
		}

		var workflowInput json.RawMessage
		if err := tx.QueryRow(ctx, `SELECT input_json FROM workflow_runs WHERE id=$1`, p.RunID).Scan(&workflowInput); err != nil {
			util.RecordError(span, err)
			return fmt.Errorf("failed to load workflow input: %w", err)
		}

		if len(p.Input) > 0 {
			runContext.Input = p.Input
			workflowInput = p.Input
		}
		if len(p.ExecutionOptions) > 0 {
			runContext.Config = p.ExecutionOptions
		}

		execState := model.NewExecutionState(dag, runContext, c.Template)

		restartNode := execState.GetNode(p.NodeID)
		if restartNode == nil {
			return fmt.Errorf("node %s not found in workflow definition", p.NodeID)
		}

		// Clean up task results for the restart node and all descendants
		for _, id := range execState.Descendants(p.NodeID) {
			delete(runContext.Tasks, id)
		}
		delete(runContext.Tasks, p.NodeID)

		if err := c.Repository.UpdateWorkflowRunStatus(ctx, tx, p.RunID, "RUNNING", workflowVersion); err != nil {
			return err
		}
		if err := c.Repository.UpdateRunContext(ctx, tx, p.RunID, runContext, logger, span); err != nil {
			return err
		}
		if len(p.Input) > 0 {
			if err := c.Repository.UpdateWorkflowInput(ctx, tx, p.RunID, p.Input); err != nil {
				return err
			}
		}

		nodeIDsToDelete := append(execState.Descendants(p.NodeID), p.NodeID)
		if err := c.Repository.DeleteTaskInstances(ctx, tx, p.RunID, nodeIDsToDelete, logger, span); err != nil {
			return err
		}

		taskID := uuid.New().String()
		taskInput, err := execState.ResolveInput(restartNode)
		if err != nil {
			return err
		}

		taskLogger := logger.With().
			Str("task_id", taskID).
			Str("node_id", restartNode.NodeID).
			Str("node_type", restartNode.GetNodeTypeForRouting()).
			Logger()

		if err := c.Repository.InsertTaskInstance(ctx, tx, taskID, p.RunID, restartNode, taskInput, time.Now(), taskLogger, span); err != nil {
			return err
		}
		c.recordTaskCreated(ctx, taskLogger, span, taskID, p.RunID, restartNode)
		return c.Repository.InsertTaskOutbox(ctx, tx, taskID, p.RunID, workflowID, workflowVersion, restartNode, taskInput, taskLogger, span)
	})
	if err != nil {
		return err
	}

	duration := time.Since(startTime)
	logger.Info().Dur("duration_ms", duration).Msg("workflow retry started successfully")
	util.SetSpanAttributes(span,
		util.AttributeInt("workflow.duration_ms", duration.Milliseconds()),
		util.Attribute("workflow.retry_node_id", p.NodeID),
	)
	return nil
}

// loadDAG returns a WorkflowDAG using the in-process TTL cache.
func (c *Coordinator) loadDAG(ctx context.Context, workflowID string, workflowVersion int) (*model.WorkflowDAG, error) {
	if dag, ok := c.wfCache.get(workflowID, workflowVersion); ok {
		return dag, nil
	}
	wfDef, err := c.GetWorkflow(ctx, workflowID, workflowVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to load workflow definition: %w", err)
	}
	var spec model.WorkflowDefinitionSpec
	if err := json.Unmarshal(wfDef.Spec, &spec); err != nil {
		return nil, fmt.Errorf("failed to parse workflow spec: %w", err)
	}
	dag := model.BuildDAG(&spec)
	c.wfCache.set(workflowID, workflowVersion, dag)
	return dag, nil
}

func (c *Coordinator) newRunContext(input json.RawMessage, config json.RawMessage) *model.RunContext {
	return &model.RunContext{
		Input:  input,
		Config: config,
		Tasks:  map[string]*model.StepResult{},
	}
}

func (c *Coordinator) recordTaskCreated(ctx context.Context, logger zerolog.Logger, span trace.Span, taskID, runID string, node *model.Node) {
	nodeTypeForRouting := node.GetNodeTypeForRouting()
	util.RecordTaskCreated(ctx, taskID, runID, node.NodeID, nodeTypeForRouting)
	util.SetSpanAttributes(span,
		util.Attribute("task.id", taskID),
		util.Attribute("task.node_id", node.NodeID),
		util.Attribute("task.node_type", nodeTypeForRouting),
	)
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

func (c *Coordinator) finalizeWorkflowIfComplete(ctx context.Context, tx pgx.Tx, runID, workflowID string, span trace.Span, logger zerolog.Logger) (bool, int, error) {
	done, pending, err := c.Repository.CheckPendingTasks(ctx, tx, runID)
	if err != nil {
		return false, 0, err
	}
	if !done {
		return false, pending, nil
	}

	failedCount, err := c.Repository.CheckFailedTasks(ctx, tx, runID)
	if err != nil {
		return false, 0, err
	}
	if failedCount > 0 {
		reason := fmt.Sprintf("workflow has %d failed task(s)", failedCount)
		if err := c.Repository.MarkWorkflowFailed(ctx, tx, runID, workflowID, reason, logger, span); err != nil {
			return false, 0, err
		}
		logger.Error().Int("failed_tasks", failedCount).Msg("workflow marked as failed due to failed tasks")
		return true, 0, nil
	}

	if err := c.Repository.MarkWorkflowCompleted(ctx, tx, runID, workflowID, span, logger); err != nil {
		return false, 0, err
	}
	logger.Info().Msg("workflow completed successfully")
	return true, 0, nil
}

func (c *Coordinator) markWorkflowFailedWithReason(ctx context.Context, runID, workflowID, reason string, logger zerolog.Logger, span trace.Span) error {
	return c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		currentStatus, err := c.Repository.GetWorkflowRunStatus(ctx, tx, runID)
		if err != nil {
			return fmt.Errorf("failed to check workflow status: %w", err)
		}
		if currentStatus != "FAILED" && currentStatus != "COMPLETED" && currentStatus != "SUCCEEDED" {
			if err := c.Repository.MarkWorkflowFailed(ctx, tx, runID, workflowID, reason, logger, span); err != nil {
				return err
			}
			logger.Error().Str("failure_reason", reason).Msg("workflow marked as failed")
		} else {
			logger.Debug().Str("current_status", currentStatus).Msg("workflow already in terminal state, not marking as failed")
		}
		return nil
	})
}

// ExecuteWithTransaction runs fn inside a database transaction, committing on success.
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
