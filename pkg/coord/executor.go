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
// If RunID is not provided it is generated.
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

	err = c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		if err := c.Repository.InsertWorkflowRun(ctx, tx, p, logger, span); err != nil {
			return err
		}

		startNode, err := dag.StartNode(p.StartNodeID)
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

		runContext := c.newRunContext(p.Input, p.ExecutionOptions)
		taskInput, err := c.resolveNodeInputFromSpecRaw(startNode.Spec, runContext, logger)
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

		if err := c.Repository.InsertTaskOutbox(ctx, tx, taskID, p.RunID, startNode, taskInput, taskLogger, span); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return err
	}

	duration := time.Since(startTime)
	logger.Info().Dur("duration_ms", duration).Msg("workflow started successfully")

	util.RecordWorkflowStarted(ctx, p.WorkflowID, p.RunID)
	util.SetSpanAttributes(span, util.AttributeInt("workflow.duration_ms", duration.Milliseconds()))

	return nil
}

func (c *Coordinator) HandleNodeCompleted(ctx context.Context, taskID string, runID string, nodeID string, output json.RawMessage) error {
	workflowID, workflowVersion, err := c.Repository.GetWorkflowRunInfo(ctx, runID)
	if err != nil {
		c.Logger.Error().Err(err).Str("run_id", runID).Msg("failed to get workflow run")
		return fmt.Errorf("failed to get workflow run: %w", err)
	}

	ctx, span := util.StartTaskSpan(ctx, "completed", "", runID, nodeID)
	defer util.EndSpan(span, nil)

	logger := c.Logger.With().
		Str("workflow_id", workflowID).
		Str("run_id", runID).
		Str("node_id", nodeID).
		Logger()

	logger.Info().Msg("handling node completion")

	startTime := time.Now()

	dag, err := c.loadDAG(ctx, workflowID, workflowVersion)
	if err != nil {
		return err
	}

	var runContext *model.RunContext
	var alreadyCompleted bool
	err = c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		updated, err := c.Repository.MarkTaskCompleted(ctx, tx, runID, nodeID, output, logger, span)
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

	if alreadyCompleted {
		logger.Debug().Msg("node already completed (duplicate delivery), skipping next-node scheduling")
		return nil
	}

	completedNode := dag.GetNode(nodeID)
	if completedNode == nil {
		logger.Error().Str("node_id", nodeID).Msg("completed node not found in workflow spec")
		return fmt.Errorf("node %s not found in workflow spec", nodeID)
	}

	var nextNodes []*model.Node
	var nextNodesErr error

	switch completedNode.Type {
	case model.NodeTypeSwitch:
		nextNodes, nextNodesErr = c.handleSwitchNode(ctx, dag, nodeID, runContext, logger)
		if nextNodesErr != nil {
			logger.Error().Err(nextNodesErr).Msg("failed to handle switch node")
			return fmt.Errorf("failed to handle switch node: %w", nextNodesErr)
		}
	case model.NodeTypeFork:
		nextNodes = dag.Successors(nodeID)
		logger.Info().Int("forked_branches", len(nextNodes)).Msg("parallel node forking into branches")
	default:
		forkParent := dag.ForkParent(nodeID)
		if forkParent != nil {
			joinNodes, joinErr := c.checkParallelJoin(ctx, dag, nodeID, runID, logger, span)
			if joinErr != nil {
				logger.Warn().Err(joinErr).Msg("failed to check parallel join, continuing with normal flow")
				nextNodes = nil
			} else if len(joinNodes) > 0 {
				nextNodes = joinNodes
				logger.Info().Int("join_nodes", len(joinNodes)).Msg("parallel branches complete, adding join nodes")
			} else {
				nextNodes = nil
				logger.Debug().
					Str("branch_node_id", nodeID).
					Str("parallel_node_id", forkParent.NodeID).
					Msg("parallel branch completed, waiting for other branches before processing next nodes")
			}
		} else {
			nextNodes = dag.Successors(nodeID)
		}
	}

	logger.Debug().
		Str("node_type", string(completedNode.Type)).
		Int("next_nodes_count", len(nextNodes)).
		Msg("found next nodes")

	if len(nextNodes) == 0 {
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
		}
	} else {
		err = c.startNextNodes(ctx, span, logger, runID, workflowID, dag, nextNodes, runContext)
		if err != nil {
			failedNodeID := "unknown"
			if len(nextNodes) > 0 {
				failedNodeID = nextNodes[0].NodeID
			}
			logger.Error().
				Err(err).
				Str("failed_node_id", failedNodeID).
				Int("next_nodes_count", len(nextNodes)).
				Str("error_type", "TASK_INSTANCE_CREATION_ERROR").
				Msg("failed to start next nodes: database or system error (not CEL resolution)")

			markErr := c.markWorkflowFailedWithReason(ctx, runID, workflowID,
				fmt.Sprintf("failed to create task instance for node %s: %v", failedNodeID, err), logger, span)
			if markErr != nil {
				logger.Error().Err(markErr).Msg("failed to mark workflow as failed after task instance creation error")
			}
		} else {
			if len(nextNodes) > 0 {
				nodeIDs := make([]string, len(nextNodes))
				for i, node := range nextNodes {
					nodeIDs[i] = node.NodeID
				}

				var failedCount int
				var totalCount int
				err = c.DB.QueryRow(ctx,
					`SELECT
						COUNT(*) FILTER (WHERE state = 'FAILED') as failed_count,
						COUNT(*) as total_count
					FROM task_instances
					WHERE run_id = $1 AND node_id = ANY($2)`,
					runID, nodeIDs).Scan(&failedCount, &totalCount)

				if err == nil && failedCount > 0 && failedCount == totalCount {
					logger.Warn().Int("failed_nodes", failedCount).Msg("all next nodes failed CEL resolution, marking workflow as failed")
					markErr := c.markWorkflowFailedWithReason(ctx, runID, workflowID,
						fmt.Sprintf("all next nodes (%d) failed CEL resolution", failedCount), logger, span)
					if markErr != nil {
						logger.Error().Err(markErr).Msg("failed to mark workflow as failed after all nodes failed CEL resolution")
					}
				}
			}
		}
	}

	duration := time.Since(startTime)
	logger.Info().
		Dur("duration_ms", duration).
		Int("next_tasks_count", len(nextNodes)).
		Msg("node completion handled")

	return nil
}

// loadDAG returns a WorkflowDAG for the given workflow, using the in-process TTL cache.
// On a cache miss it loads from the database and builds the DAG index structures.
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

func (c *Coordinator) resolveNodeInputFromSpecRaw(specRaw json.RawMessage, runContext *model.RunContext, logger zerolog.Logger) (json.RawMessage, error) {
	var spec interface{}
	if len(specRaw) > 0 {
		if err := json.Unmarshal(specRaw, &spec); err != nil {
			logger.Error().Err(err).Msg("failed to unmarshal node spec for templating; using raw spec")
			spec = string(specRaw)
		}
	}

	resolvedParameters, err := c.Template.ResolveTemplates(spec, runContext)
	if err != nil {
		logger.Error().
			Err(err).
			Str("error_type", "CEL_RESOLUTION_ERROR").
			Msg("failed to resolve CEL expressions in node spec - check CEL syntax and context variables")
		return nil, fmt.Errorf("failed to resolve CEL expressions/templates: %w", err)
	}

	taskInput, err := json.Marshal(resolvedParameters)
	if err != nil {
		logger.Error().Err(err).Msg("failed to marshal task input")
		return json.RawMessage("{}"), nil
	}

	return taskInput, nil
}

func (c *Coordinator) recordTaskCreated(ctx context.Context, logger zerolog.Logger, span trace.Span, taskID, runID string, node *model.Node) {
	logger.Info().Msg("created task instance")
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

// finalizeWorkflowIfComplete checks if all tasks are done and marks the workflow SUCCEEDED or FAILED.
// Returns (isComplete, pendingCount, error).
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

// startNextNodes creates task instances and outbox entries for the next nodes in a new transaction.
// Separating this from the completion transaction means a retry won't re-process the completion.
func (c *Coordinator) startNextNodes(ctx context.Context, span trace.Span, logger zerolog.Logger, runID, workflowID string, dag *model.WorkflowDAG, nextNodes []*model.Node, runContext *model.RunContext) error {
	return c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		var celResolutionErrors []string
		var nodesToProcess []*model.Node

		// First pass: expand control-flow nodes (SWITCH, FORK) inline — they don't become worker tasks
		for _, nextNode := range nextNodes {
			if !nextNode.IsControlFlowNode() {
				nodesToProcess = append(nodesToProcess, nextNode)
				continue
			}

			switch nextNode.Type {
			case model.NodeTypeSwitch:
				exists, existingTaskID, existingState, err := c.Repository.TaskInstanceExists(ctx, tx, runID, nextNode.NodeID)
				if err != nil {
					return err
				}

				var switchOutput json.RawMessage
				if !exists || existingState == "FAILED" {
					switchTaskID := uuid.New().String()
					if exists && existingState == "FAILED" {
						switchTaskID = existingTaskID
					}

					// SWITCH passes through the output of its predecessor
					if runContext.Tasks != nil {
						switchOutput = dag.PredecessorOutput(nextNode.NodeID, runContext.Tasks)
					}
					if switchOutput == nil {
						switchOutput = json.RawMessage("{}")
					}

					if err := c.Repository.InsertTaskInstance(ctx, tx, switchTaskID, runID, nextNode, json.RawMessage("{}"), time.Now(), logger, span); err != nil {
						logger.Error().Err(err).Str("node_id", nextNode.NodeID).Msg("failed to create task instance for switch node")
						return fmt.Errorf("failed to create task instance for switch node: %w", err)
					}

					if _, err := c.Repository.MarkTaskCompleted(ctx, tx, runID, nextNode.NodeID, switchOutput, logger, span); err != nil {
						logger.Error().Err(err).Str("node_id", nextNode.NodeID).Msg("failed to mark switch node as completed")
						return fmt.Errorf("failed to mark switch node as completed: %w", err)
					}

					updatedRunContext, err := c.Repository.LoadAndUpdateRunContext(ctx, tx, runID, nextNode.NodeID, switchOutput, logger, span)
					if err != nil {
						logger.Error().Err(err).Str("node_id", nextNode.NodeID).Msg("failed to update run context for switch node")
						return fmt.Errorf("failed to update run context for switch node: %w", err)
					}
					runContext = updatedRunContext

					c.recordTaskCreated(ctx, logger, span, switchTaskID, runID, nextNode)
					logger.Debug().
						Str("switch_node_id", nextNode.NodeID).
						Str("task_id", switchTaskID).
						Msg("switch node task instance created and marked as completed")
				} else {
					// Already exists — reload its output into the local run context for condition evaluation
					var existingOutput json.RawMessage
					_ = tx.QueryRow(ctx, `SELECT output_json FROM task_instances WHERE run_id=$1 AND node_id=$2`, runID, nextNode.NodeID).
						Scan(&existingOutput)
					if existingOutput != nil {
						switchOutput = existingOutput
						if runContext.Tasks == nil {
							runContext.Tasks = make(map[string]*model.StepResult)
						}
						runContext.Tasks[nextNode.NodeID] = &model.StepResult{
							Status: model.TaskInstanceStatusCompleted,
							Output: switchOutput,
						}
					}
				}

				switchNodes, err := c.handleSwitchNode(ctx, dag, nextNode.NodeID, runContext, logger)
				if err != nil {
					logger.Error().Err(err).Str("node_id", nextNode.NodeID).Msg("failed to handle switch node")
					return fmt.Errorf("failed to handle switch node: %w", err)
				}
				nodesToProcess = append(nodesToProcess, switchNodes...)
				logger.Debug().
					Str("switch_node_id", nextNode.NodeID).
					Int("matched_nodes", len(switchNodes)).
					Msg("switch node processed, adding matched nodes to processing queue")

			case model.NodeTypeFork:
				forkNodes := dag.Successors(nextNode.NodeID)
				nodesToProcess = append(nodesToProcess, forkNodes...)
				logger.Debug().
					Str("fork_node_id", nextNode.NodeID).
					Int("forked_nodes", len(forkNodes)).
					Msg("fork node processed, adding forked nodes to processing queue")
			}
			// JOIN nodes are handled automatically when all parallel branches complete
		}

		// Second pass: create task instances for executable nodes
		for _, nextNode := range nodesToProcess {
			exists, existingTaskID, existingState, err := c.Repository.TaskInstanceExists(ctx, tx, runID, nextNode.NodeID)
			if err != nil {
				return err
			}
			if exists && existingState != "FAILED" {
				logger.Debug().
					Str("next_node_id", nextNode.NodeID).
					Str("existing_task_id", existingTaskID).
					Str("existing_state", existingState).
					Msg("next task already exists and is not failed, skipping")
				continue
			}

			nextTaskID := uuid.New().String()
			if exists && existingState == "FAILED" {
				logger.Debug().
					Str("next_node_id", nextNode.NodeID).
					Str("existing_task_id", existingTaskID).
					Msg("recreating task instance for failed node")
			}

			nextTaskLogger := logger.With().
				Str("next_task_id", nextTaskID).
				Str("next_node_id", nextNode.NodeID).
				Str("next_node_type", nextNode.GetNodeTypeForRouting()).
				Logger()

			taskInput, err := c.resolveNodeInputFromSpecRaw(nextNode.Spec, runContext, nextTaskLogger)
			if err != nil {
				celError := fmt.Sprintf("failed to resolve CEL expressions: %v", err)
				nextTaskLogger.Error().
					Err(err).
					Str("error_type", "CEL_RESOLUTION_ERROR").
					Msg("failed to resolve CEL expressions for next node - creating task instance as FAILED")

				if insertErr := c.Repository.InsertTaskInstanceWithState(ctx, tx, nextTaskID, runID, nextNode, json.RawMessage("{}"), time.Now(), "FAILED", celError, nextTaskLogger, span); insertErr != nil {
					nextTaskLogger.Error().
						Err(insertErr).
						Str("error_type", "TASK_INSTANCE_CREATION_ERROR").
						Msg("failed to insert failed task instance into database")
					return fmt.Errorf("failed to create failed task instance for node %s: %w", nextNode.NodeID, insertErr)
				}
				celResolutionErrors = append(celResolutionErrors, fmt.Sprintf("node %s: %v", nextNode.NodeID, err))
				c.recordTaskCreated(ctx, nextTaskLogger, span, nextTaskID, runID, nextNode)
				continue
			}

			if err := c.Repository.InsertTaskInstance(ctx, tx, nextTaskID, runID, nextNode, taskInput, time.Now(), nextTaskLogger, span); err != nil {
				nextTaskLogger.Error().
					Err(err).
					Str("error_type", "TASK_INSTANCE_CREATION_ERROR").
					Msg("failed to insert task instance into database")
				return fmt.Errorf("failed to create task instance for node %s: %w", nextNode.NodeID, err)
			}
			c.recordTaskCreated(ctx, nextTaskLogger, span, nextTaskID, runID, nextNode)

			if err := c.Repository.InsertTaskOutbox(ctx, tx, nextTaskID, runID, nextNode, taskInput, nextTaskLogger, span); err != nil {
				nextTaskLogger.Error().
					Err(err).
					Str("error_type", "OUTBOX_CREATION_ERROR").
					Msg("failed to create outbox entry for task")
				return fmt.Errorf("failed to create outbox entry for node %s: %w", nextNode.NodeID, err)
			}
		}

		if len(celResolutionErrors) > 0 {
			logger.Warn().
				Strs("cel_resolution_errors", celResolutionErrors).
				Int("failed_nodes_count", len(celResolutionErrors)).
				Msg("some nodes failed CEL resolution and were marked as FAILED")
		}

		logger.Info().
			Int("next_tasks_created", len(nodesToProcess)-len(celResolutionErrors)).
			Int("failed_tasks_created", len(celResolutionErrors)).
			Msg("next nodes processing completed")

		return nil
	})
}

// handleSwitchNode evaluates outgoing edge conditions and returns the matching successor nodes.
func (c *Coordinator) handleSwitchNode(ctx context.Context, dag *model.WorkflowDAG, nodeID string, runContext *model.RunContext, logger zerolog.Logger) ([]*model.Node, error) {
	logger.Debug().Str("node_id", nodeID).Msg("handling switch node completion")

	evaluateCondition := func(condition string) (bool, error) {
		val, err := c.Template.ResolveTemplates(condition, runContext)
		if err != nil {
			return false, fmt.Errorf("failed to evaluate condition: %w", err)
		}
		switch v := val.(type) {
		case bool:
			return v, nil
		case string:
			return v != "", nil
		case float64:
			return v != 0, nil
		case nil:
			return false, nil
		default:
			return v != nil, nil
		}
	}

	nextNodes, err := dag.SuccessorsConditional(nodeID, evaluateCondition)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate switch conditions: %w", err)
	}

	logger.Info().Int("matched_branches", len(nextNodes)).Msg("switch node evaluated conditions")
	return nextNodes, nil
}

// checkParallelJoin checks whether all branches of a fork are complete.
// Returns the join nodes when all branches are done, or nil when waiting for more branches.
func (c *Coordinator) checkParallelJoin(ctx context.Context, dag *model.WorkflowDAG, completedNodeID string, runID string, logger zerolog.Logger, span trace.Span) ([]*model.Node, error) {
	forkNode := dag.ForkParent(completedNodeID)
	if forkNode == nil {
		return nil, nil
	}

	logger.Debug().
		Str("parallel_node_id", forkNode.NodeID).
		Str("branch_node_id", completedNodeID).
		Msg("checking parallel join condition")

	branchNodeIDs := dag.BranchesOf(forkNode.NodeID)
	if len(branchNodeIDs) == 0 {
		return nil, nil
	}

	var joinNodes []*model.Node
	err := c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		completedCount, totalCount, err := c.Repository.CheckParallelBranchesComplete(ctx, tx, runID, branchNodeIDs)
		if err != nil {
			return err
		}

		if completedCount < totalCount {
			logger.Debug().
				Int("completed_branches", completedCount).
				Int("total_branches", totalCount).
				Msg("not all parallel branches complete yet")
			return nil
		}

		joinNodes = dag.JoinNodes(branchNodeIDs)
		return nil
	})

	return joinNodes, err
}

// HandleNodeFailed marks a node as failed and handles workflow failure logic.
func (c *Coordinator) HandleNodeFailed(ctx context.Context, runID string, nodeID string, errorMsg string) error {
	var workflowID string
	_ = c.DB.QueryRow(ctx, `SELECT workflow_definition_id FROM workflow_runs WHERE id=$1`, runID).
		Scan(&workflowID)

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
			logger.Error().Err(err).Msg("failed to update task instance")
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

		done, pending, err := c.Repository.CheckPendingTasks(ctx, tx, runID)
		if err != nil {
			logger.Error().Err(err).Msg("failed to check pending tasks")
			util.RecordError(span, err)
			return fmt.Errorf("failed to check pending tasks: %w", err)
		}

		if done {
			if err := c.Repository.MarkWorkflowFailed(ctx, tx, runID, workflowID, errorMsg, logger, span); err != nil {
				logger.Error().Err(err).Msg("failed to mark workflow as failed")
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

	duration := time.Since(startTime)
	logger.Info().Dur("duration_ms", duration).Msg("node failure handled")

	return nil
}

// markWorkflowFailedWithReason marks a workflow as failed only if it is still in a non-terminal state.
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
			logger.Error().Str("failure_reason", reason).Msg("workflow marked as failed: could not create task instance")
		} else {
			logger.Debug().Str("current_status", currentStatus).Msg("workflow already in terminal state, not marking as failed")
		}

		return nil
	})
}

// RetryWorkflowInstance retries a workflow instance from a specific node.
// It accepts optional config and input to replace the saved context, or uses the saved context if not provided.
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
		logger.Error().Err(err).Msg("failed to get workflow instance")
		util.RecordError(span, err)
		return fmt.Errorf("failed to get workflow instance: %w", err)
	}

	logger = logger.With().Str("workflow_id", workflowID).Logger()

	workflowVersion := p.WorkflowVersion
	if workflowVersion == 0 {
		deployedWorkflow, err := c.GetWorkflow(ctx, workflowID, 0)
		if err != nil {
			logger.Error().Err(err).Msg("failed to get deployed workflow version")
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
		logger.Error().Err(err).Msg("failed to load workflow definition")
		util.RecordError(span, err)
		return err
	}

	restartNode := dag.GetNode(p.NodeID)
	if restartNode == nil {
		return fmt.Errorf("node %s not found in workflow definition", p.NodeID)
	}

	descendantNodeIDs := dag.Descendants(p.NodeID)

	err = c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		runContext, err := c.Repository.LoadRunContext(ctx, tx, p.RunID, logger, span)
		if err != nil {
			return err
		}

		var workflowInput json.RawMessage
		err = tx.QueryRow(ctx, `SELECT input_json FROM workflow_runs WHERE id=$1`, p.RunID).
			Scan(&workflowInput)
		if err != nil {
			logger.Error().Err(err).Msg("failed to load workflow input")
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

		for _, nodeID := range descendantNodeIDs {
			delete(runContext.Tasks, nodeID)
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

		nodeIDsToDelete := append(descendantNodeIDs, p.NodeID)
		if err := c.Repository.DeleteTaskInstances(ctx, tx, p.RunID, nodeIDsToDelete, logger, span); err != nil {
			return err
		}

		taskID := uuid.New().String()
		scheduledAt := time.Now()

		taskInput, err := c.resolveNodeInputFromSpecRaw(restartNode.Spec, runContext, logger)
		if err != nil {
			return err
		}

		taskLogger := logger.With().
			Str("task_id", taskID).
			Str("node_id", restartNode.NodeID).
			Str("node_type", restartNode.GetNodeTypeForRouting()).
			Logger()

		if err := c.Repository.InsertTaskInstance(ctx, tx, taskID, p.RunID, restartNode, taskInput, scheduledAt, taskLogger, span); err != nil {
			return err
		}
		c.recordTaskCreated(ctx, taskLogger, span, taskID, p.RunID, restartNode)

		if err := c.Repository.InsertTaskOutbox(ctx, tx, taskID, p.RunID, restartNode, taskInput, taskLogger, span); err != nil {
			return err
		}

		logger.Debug().Msg("workflow retry initialized successfully")
		return nil
	})

	if err != nil {
		return err
	}

	duration := time.Since(startTime)
	logger.Info().Dur("duration_ms", duration).Msg("workflow retry started successfully")

	util.RecordWorkflowStarted(ctx, workflowID, p.RunID)
	util.SetSpanAttributes(span,
		util.AttributeInt("workflow.duration_ms", duration.Milliseconds()),
		util.Attribute("workflow.retry_node_id", p.NodeID),
	)

	return nil
}

// ExecuteWithTransaction executes fn inside a database transaction, committing on success.
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
