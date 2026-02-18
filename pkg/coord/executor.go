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
	wfSpec, err := c.loadAndParseWorkflowSpec(ctx, p.WorkflowID, p.WorkflowVersion)
	if err != nil {
		logger.Error().Err(err).Msg("failed to load workflow definition")
		util.RecordError(span, err)
		return err
	}

	err = c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		if err := c.Repository.InsertWorkflowRun(ctx, tx, p, logger, span); err != nil {
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
	logger.Info().
		Dur("duration_ms", duration).
		Msg("workflow started successfully")

	util.RecordWorkflowStarted(ctx, p.WorkflowID, p.RunID)
	util.SetSpanAttributes(span,
		util.AttributeInt("workflow.duration_ms", duration.Milliseconds()),
	)

	return nil
}

func (c *Coordinator) HandleNodeCompleted(ctx context.Context, taskID string, runID string, nodeID string, output json.RawMessage) error {
	// Get workflow run info to load workflow definition (before transaction)
	workflowID, workflowVersion, err := c.Repository.GetWorkflowRunInfo(ctx, runID)
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
	wfSpec, err := c.loadAndParseWorkflowSpec(ctx, workflowID, workflowVersion)
	if err != nil {
		return err
	}

	// STEP 1: Record the output from the previous node successfully
	// This transaction should always succeed to preserve the node completion state
	var runContext *model.RunContext
	var alreadyCompleted bool
	err = c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		updated, err := c.Repository.MarkTaskCompleted(ctx, tx, runID, nodeID, output, logger, span)
		if err != nil {
			return err
		}
		if !updated {
			// Task was already completed — this is a duplicate delivery (idempotent)
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

	// If task was already completed, this is a duplicate delivery — return early (idempotent)
	if alreadyCompleted {
		logger.Debug().Msg("node already completed (duplicate delivery), skipping next-node scheduling")
		return nil
	}

	// Get the completed node to check its type
	completedNode := wfSpec.GetNodeByID(nodeID)
	if completedNode == nil {
		logger.Error().Str("node_id", nodeID).Msg("completed node not found in workflow spec")
		return fmt.Errorf("node %s not found in workflow spec", nodeID)
	}

	// This is a separate transaction so that if it fails, we can retry without losing the completion record
	var nextNodes []*model.Node
	var nextNodesErr error

	// Handle different node types
	switch completedNode.Type {
	case model.NodeTypeSwitch:
		// For SWITCH nodes, evaluate conditions on outgoing edges
		nextNodes, nextNodesErr = c.handleSwitchNode(ctx, wfSpec, nodeID, runContext, logger)
		if nextNodesErr != nil {
			logger.Error().Err(nextNodesErr).Msg("failed to handle switch node")
			return fmt.Errorf("failed to handle switch node: %w", nextNodesErr)
		}
	case model.NodeTypeFork:
		// For FORK nodes, fork into all outgoing branches immediately
		// The join logic will be handled when branch nodes complete
		nextNodes = wfSpec.FindNextNodes(nodeID)
		logger.Info().
			Int("forked_branches", len(nextNodes)).
			Msg("parallel node forking into branches")
	default:
		// For CODE, HTTP, WAIT, and other executable nodes, use standard next node logic
		// Control flow nodes (SWITCH, FORK) are handled above

		// Check if this node is part of a parallel execution (a branch node)
		// Branch nodes have incoming edges from a FORK node
		var parallelNodeID string
		for _, edge := range wfSpec.Edges {
			if edge.To == nodeID {
				fromNode := wfSpec.GetNodeByID(edge.From)
				if fromNode != nil && fromNode.Type == model.NodeTypeFork {
					parallelNodeID = edge.From
					break
				}
			}
		}

		if parallelNodeID != "" {
			// This is a branch node - only process join nodes when all branches complete
			// Don't process regular next nodes from individual branches
			joinNodes, joinErr := c.checkParallelJoin(ctx, wfSpec, nodeID, runID, logger, span)
			if joinErr != nil {
				logger.Warn().Err(joinErr).Msg("failed to check parallel join, continuing with normal flow")
				// On error, don't process any next nodes to avoid premature execution
				nextNodes = nil
			} else if len(joinNodes) > 0 {
				// All parallel branches are complete, process join nodes only
				nextNodes = joinNodes
				logger.Info().
					Int("join_nodes", len(joinNodes)).
					Msg("parallel branches complete, adding join nodes")
			} else {
				// Not all branches complete yet, don't process any next nodes from this branch
				// (they will be processed when all branches complete)
				nextNodes = nil
				logger.Debug().
					Str("branch_node_id", nodeID).
					Str("parallel_node_id", parallelNodeID).
					Msg("parallel branch completed, waiting for other branches before processing next nodes")
			}
		} else {
			// Not a branch node, use standard next node logic
			nextNodes = wfSpec.FindNextNodes(nodeID)
		}
	}

	logger.Debug().
		Str("node_type", string(completedNode.Type)).
		Int("next_nodes_count", len(nextNodes)).
		Msg("found next nodes")

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
		}
	} else {
		// Start next nodes in a separate transaction
		err = c.startNextNodes(ctx, span, logger, runID, workflowID, workflowVersion, nextNodes, runContext)
		if err != nil {
			// Only fail workflow if there's a database/system error
			// CEL resolution errors are now handled by creating FAILED task instances
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

			// Mark workflow as FAILED only for non-CEL errors (database issues, etc.)
			markErr := c.markWorkflowFailedWithReason(ctx, runID, workflowID, fmt.Sprintf("failed to create task instance for node %s: %v", failedNodeID, err), logger, span)
			if markErr != nil {
				logger.Error().Err(markErr).Msg("failed to mark workflow as failed after task instance creation error")
			}
		} else {
			// Check if all next nodes failed CEL resolution - if so, mark workflow as failed
			// This check happens after successful transaction, so we can query the database
			if len(nextNodes) > 0 {
				nodeIDs := make([]string, len(nextNodes))
				for i, node := range nextNodes {
					nodeIDs[i] = node.NodeID
				}

				var failedCount int
				var totalCount int
				// Use pgx.Array to properly pass array to PostgreSQL
				err = c.DB.QueryRow(ctx,
					`SELECT 
						COUNT(*) FILTER (WHERE state = 'FAILED') as failed_count,
						COUNT(*) as total_count
					FROM task_instances 
					WHERE run_id = $1 AND node_id = ANY($2)`,
					runID, nodeIDs).Scan(&failedCount, &totalCount)

				if err == nil && failedCount > 0 && failedCount == totalCount {
					// All next nodes failed CEL resolution - mark workflow as failed
					logger.Warn().
						Int("failed_nodes", failedCount).
						Msg("all next nodes failed CEL resolution, marking workflow as failed")
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

// loadAndParseWorkflowSpec loads a workflow definition and parses its spec
func (c *Coordinator) loadAndParseWorkflowSpec(ctx context.Context, workflowID string, workflowVersion int) (*model.WorkflowDefinitionSpec, error) {
	wfDef, err := c.GetWorkflow(ctx, workflowID, workflowVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to load workflow definition: %w", err)
	}

	var wfSpec model.WorkflowDefinitionSpec
	if err := json.Unmarshal(wfDef.Spec, &wfSpec); err != nil {
		return nil, fmt.Errorf("failed to parse workflow spec: %w", err)
	}

	return &wfSpec, nil
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
			// fall back to treating raw bytes as string
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

// finalizeWorkflowIfComplete checks if workflow is complete and marks it as succeeded or failed.
// If no tasks are pending but some have failed, the workflow is marked FAILED (not SUCCEEDED).
// Returns (isComplete, pendingCount, error)
func (c *Coordinator) finalizeWorkflowIfComplete(ctx context.Context, tx pgx.Tx, runID, workflowID string, span trace.Span, logger zerolog.Logger) (bool, int, error) {
	done, pending, err := c.Repository.CheckPendingTasks(ctx, tx, runID)
	if err != nil {
		return false, 0, err
	}

	if !done {
		return false, pending, nil
	}

	// No pending tasks — check if any tasks failed before declaring success
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

// startNextNodes starts the next nodes in a separate transaction
// This allows retrying if it fails without losing the completion record of the previous node
func (c *Coordinator) startNextNodes(ctx context.Context, span trace.Span, logger zerolog.Logger, runID, workflowID string, workflowVersion int, nextNodes []*model.Node, runContext *model.RunContext) error {
	return c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		// Load workflow spec once for edge lookups (predecessor resolution, fork targets, etc.)
		wfSpec, err := c.loadAndParseWorkflowSpec(ctx, workflowID, workflowVersion)
		if err != nil {
			return fmt.Errorf("failed to load workflow spec: %w", err)
		}

		// Create task instances and outbox entries for next nodes
		var celResolutionErrors []string
		var nodesToProcess []*model.Node

		// First pass: handle control flow nodes (SWITCH, FORK) immediately
		for _, nextNode := range nextNodes {
			if nextNode.IsControlFlowNode() {
				// Control flow nodes are handled by coordinator, not worker
				if nextNode.Type == model.NodeTypeSwitch {
					// For SWITCH nodes:
					// 1. Create a task instance and mark it as completed immediately (SWITCH nodes pass through input as output)
					// 2. Update run context with SWITCH node output
					// 3. Evaluate conditions and process resulting nodes

					// Check if task instance already exists
					exists, existingTaskID, existingState, err := c.Repository.TaskInstanceExists(ctx, tx, runID, nextNode.NodeID)
					if err != nil {
						return err
					}

					var switchOutput json.RawMessage
					if !exists || existingState == "FAILED" {
						// Create task instance for SWITCH node
						switchTaskID := uuid.New().String()
						if exists && existingState == "FAILED" {
							switchTaskID = existingTaskID
						}

						// SWITCH nodes pass through the output from their predecessor node.
						// Find the predecessor by looking at incoming edges to this switch node.
						if runContext.Tasks != nil {
							for _, edge := range wfSpec.Edges {
								if edge.To == nextNode.NodeID {
									if predResult, ok := runContext.Tasks[edge.From]; ok && predResult != nil &&
										predResult.Status == model.TaskInstanceStatusCompleted && len(predResult.Output) > 0 {
										switchOutput = predResult.Output
										break
									}
								}
							}
						}

						// If no output found, use empty object
						if switchOutput == nil {
							switchOutput = json.RawMessage("{}")
						}

						// Create task instance and mark as completed immediately
						if err := c.Repository.InsertTaskInstance(ctx, tx, switchTaskID, runID, nextNode, json.RawMessage("{}"), time.Now(), logger, span); err != nil {
							logger.Error().Err(err).Str("node_id", nextNode.NodeID).Msg("failed to create task instance for switch node")
							return fmt.Errorf("failed to create task instance for switch node: %w", err)
						}

						// Mark as completed with pass-through output
						if _, err := c.Repository.MarkTaskCompleted(ctx, tx, runID, nextNode.NodeID, switchOutput, logger, span); err != nil {
							logger.Error().Err(err).Str("node_id", nextNode.NodeID).Msg("failed to mark switch node as completed")
							return fmt.Errorf("failed to mark switch node as completed: %w", err)
						}

						// Update run context in database with SWITCH node output
						updatedRunContext, err := c.Repository.LoadAndUpdateRunContext(ctx, tx, runID, nextNode.NodeID, switchOutput, logger, span)
						if err != nil {
							logger.Error().Err(err).Str("node_id", nextNode.NodeID).Msg("failed to update run context for switch node")
							return fmt.Errorf("failed to update run context for switch node: %w", err)
						}

						// Update local run context for condition evaluation
						runContext = updatedRunContext

						c.recordTaskCreated(ctx, logger, span, switchTaskID, runID, nextNode)
						logger.Debug().
							Str("switch_node_id", nextNode.NodeID).
							Str("task_id", switchTaskID).
							Msg("switch node task instance created and marked as completed")
					} else {
						// Task instance already exists, load its output for run context
						var existingOutput json.RawMessage
						err := tx.QueryRow(ctx, `SELECT output_json FROM task_instances WHERE run_id=$1 AND node_id=$2`, runID, nextNode.NodeID).
							Scan(&existingOutput)
						if err == nil && existingOutput != nil {
							switchOutput = existingOutput
							// Update local run context for condition evaluation
							if runContext.Tasks == nil {
								runContext.Tasks = make(map[string]*model.StepResult)
							}
							runContext.Tasks[nextNode.NodeID] = &model.StepResult{
								Status: model.TaskInstanceStatusCompleted,
								Output: switchOutput,
							}
						}
					}

					// Now evaluate conditions and get matched nodes
					switchNodes, err := c.handleSwitchNode(ctx, wfSpec, nextNode.NodeID, runContext, logger)
					if err != nil {
						logger.Error().Err(err).Str("node_id", nextNode.NodeID).Msg("failed to handle switch node")
						return fmt.Errorf("failed to handle switch node: %w", err)
					}

					// Recursively process the nodes that matched the switch conditions
					nodesToProcess = append(nodesToProcess, switchNodes...)
					logger.Debug().
						Str("switch_node_id", nextNode.NodeID).
						Int("matched_nodes", len(switchNodes)).
						Msg("switch node processed, adding matched nodes to processing queue")
				} else if nextNode.Type == model.NodeTypeFork {
					// For FORK nodes, get all outgoing nodes and process them
					forkNodes := wfSpec.FindNextNodes(nextNode.NodeID)
					nodesToProcess = append(nodesToProcess, forkNodes...)
					logger.Debug().
						Str("fork_node_id", nextNode.NodeID).
						Int("forked_nodes", len(forkNodes)).
						Msg("fork node processed, adding forked nodes to processing queue")
				}
				// JOIN nodes are handled automatically when all parallel branches complete
				continue
			}
			// Regular nodes go to the processing queue
			nodesToProcess = append(nodesToProcess, nextNode)
		}

		// Second pass: create task instances for executable nodes
		for _, nextNode := range nodesToProcess {
			// Check if task instance already exists (idempotency)
			// Allow recreating if the existing task is in FAILED state (for retry scenarios)
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

			// If task exists but is FAILED, we'll recreate it with a new ID
			// This allows retrying failed nodes within the same workflow run
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
				// CEL resolution failed - create task instance as FAILED instead of failing the workflow
				celError := fmt.Sprintf("failed to resolve CEL expressions: %v", err)
				nextTaskLogger.Error().
					Err(err).
					Str("error_type", "CEL_RESOLUTION_ERROR").
					Msg("failed to resolve CEL expressions for next node - creating task instance as FAILED")

				// Create task instance with FAILED state
				if insertErr := c.Repository.InsertTaskInstanceWithState(ctx, tx, nextTaskID, runID, nextNode, json.RawMessage("{}"), time.Now(), "FAILED", celError, nextTaskLogger, span); insertErr != nil {
					nextTaskLogger.Error().
						Err(insertErr).
						Str("error_type", "TASK_INSTANCE_CREATION_ERROR").
						Msg("failed to insert failed task instance into database")
					return fmt.Errorf("failed to create failed task instance for node %s: %w", nextNode.NodeID, insertErr)
				}

				celResolutionErrors = append(celResolutionErrors, fmt.Sprintf("node %s: %v", nextNode.NodeID, err))
				c.recordTaskCreated(ctx, nextTaskLogger, span, nextTaskID, runID, nextNode)
				// Don't create outbox entry for failed tasks - they can't be executed
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

		// Log CEL resolution errors but don't fail the transaction
		// The workflow can continue with other nodes that succeeded
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

// handleSwitchNode handles completion of a SWITCH node by evaluating conditions on outgoing edges
// and returning only the nodes that match the conditions
func (c *Coordinator) handleSwitchNode(ctx context.Context, wfSpec *model.WorkflowDefinitionSpec, nodeID string, runContext *model.RunContext, logger zerolog.Logger) ([]*model.Node, error) {
	logger.Debug().Str("node_id", nodeID).Msg("handling switch node completion")

	// Create a condition evaluator using CEL
	evaluateCondition := func(condition string) (bool, error) {
		// Use the template resolver to evaluate the condition
		val, err := c.Template.ResolveTemplates(condition, runContext)
		if err != nil {
			return false, fmt.Errorf("failed to evaluate condition: %w", err)
		}

		// Convert to boolean
		switch v := val.(type) {
		case bool:
			return v, nil
		case string:
			// Treat non-empty strings as true
			return v != "", nil
		case float64:
			// Treat non-zero numbers as true
			return v != 0, nil
		case nil:
			return false, nil
		default:
			// For other types, check if they're truthy
			return v != nil, nil
		}
	}

	// Find next nodes by evaluating conditions
	nextNodes, err := wfSpec.FindNextNodesWithConditions(nodeID, evaluateCondition)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate switch conditions: %w", err)
	}

	logger.Info().
		Int("matched_branches", len(nextNodes)).
		Msg("switch node evaluated conditions")

	return nextNodes, nil
}

// checkParallelJoin checks if a completed node is part of a parallel execution and if all branches are complete
// Returns join nodes if all branches are done, otherwise returns empty slice
// This is workflow state transition logic - it uses repository for database queries
func (c *Coordinator) checkParallelJoin(ctx context.Context, wfSpec *model.WorkflowDefinitionSpec, completedNodeID string, runID string, logger zerolog.Logger, span trace.Span) ([]*model.Node, error) {
	// Find if this node has an incoming edge from a FORK node
	var parallelNodeID string
	for _, edge := range wfSpec.Edges {
		if edge.To == completedNodeID {
			node := wfSpec.GetNodeByID(edge.From)
			if node != nil && node.Type == model.NodeTypeFork {
				parallelNodeID = edge.From
				break
			}
		}
	}

	// If not part of a parallel execution, return empty
	if parallelNodeID == "" {
		return nil, nil
	}

	logger.Debug().
		Str("parallel_node_id", parallelNodeID).
		Str("branch_node_id", completedNodeID).
		Msg("checking parallel join condition")

	// Find all branches from the parallel node
	outgoingEdges := wfSpec.FindOutgoingEdges(parallelNodeID)
	if len(outgoingEdges) == 0 {
		return nil, nil
	}

	// Get all branch target nodes
	branchNodeIDs := make([]string, 0, len(outgoingEdges))
	for _, edge := range outgoingEdges {
		branchNodeIDs = append(branchNodeIDs, edge.To)
	}

	// Check if all branches are complete using repository
	var joinNodes []*model.Node
	err := c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		completedCount, totalCount, err := c.Repository.CheckParallelBranchesComplete(ctx, tx, runID, branchNodeIDs)
		if err != nil {
			return err
		}

		// If not all branches are complete, return empty (no join yet)
		if completedCount < totalCount {
			logger.Debug().
				Int("completed_branches", completedCount).
				Int("total_branches", totalCount).
				Msg("not all parallel branches complete yet")
			joinNodes = nil
			return nil
		}

		// All branches are complete, find join nodes
		joinNodes, err = c.findJoinNodes(wfSpec, branchNodeIDs, logger)
		return err
	})

	return joinNodes, err
}

// findJoinNodes finds nodes that should be executed after all parallel branches complete
func (c *Coordinator) findJoinNodes(wfSpec *model.WorkflowDefinitionSpec, branchNodeIDs []string, logger zerolog.Logger) ([]*model.Node, error) {
	// Find all nodes that come after the branch nodes
	joinCandidates := make(map[string]int) // nodeID -> incoming branch count
	nodeMap := wfSpec.GetNodeMap()

	for _, branchNodeID := range branchNodeIDs {
		nextNodes := wfSpec.FindNextNodes(branchNodeID)
		for _, nextNode := range nextNodes {
			joinCandidates[nextNode.NodeID]++
		}
	}

	// Find nodes that have incoming edges from all branches (join nodes)
	joinNodes := make([]*model.Node, 0)
	for candidateID, incomingCount := range joinCandidates {
		// Check if this candidate has incoming edges from all branches
		hasAllBranches := true
		for _, branchNodeID := range branchNodeIDs {
			hasIncoming := false
			for _, edge := range wfSpec.Edges {
				if edge.From == branchNodeID && edge.To == candidateID {
					hasIncoming = true
					break
				}
			}
			if !hasIncoming {
				hasAllBranches = false
				break
			}
		}

		if hasAllBranches && incomingCount == len(branchNodeIDs) {
			if node := nodeMap[candidateID]; node != nil {
				joinNodes = append(joinNodes, node)
			}
		}
	}

	// If no explicit join node found, return nodes that come after any branch
	// (this handles cases where branches merge naturally)
	if len(joinNodes) == 0 {
		allNextNodes := make(map[string]*model.Node)
		for _, branchNodeID := range branchNodeIDs {
			nextNodes := wfSpec.FindNextNodes(branchNodeID)
			for _, nextNode := range nextNodes {
				allNextNodes[nextNode.NodeID] = nextNode
			}
		}
		for _, node := range allNextNodes {
			joinNodes = append(joinNodes, node)
		}
	}

	return joinNodes, nil
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
		taskID, nodeType, taskStartTime, err := c.Repository.GetTaskInstanceInfo(ctx, tx, runID, nodeID)
		if err != nil {
			logger.Warn().Err(err).Msg("failed to get task instance info, continuing with failure handling")
		}

		// Mark task instance as failed
		updated, err := c.Repository.MarkTaskFailed(ctx, tx, runID, nodeID, errorMsg, logger, span)
		if err != nil {
			logger.Error().Err(err).Msg("failed to update task instance")
			util.RecordError(span, err)
			return fmt.Errorf("failed to update task instance: %w", err)
		}

		if !updated {
			// Task already failed or not found, return without error (idempotent)
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

		// Check if workflow should be marked as failed
		// For now, we mark workflow as failed if any critical node fails
		// This can be enhanced with retry logic or conditional failure handling
		done, pending, err := c.Repository.CheckPendingTasks(ctx, tx, runID)
		if err != nil {
			logger.Error().Err(err).Msg("failed to check pending tasks")
			util.RecordError(span, err)
			return fmt.Errorf("failed to check pending tasks: %w", err)
		}

		// If no pending tasks and we have a failure, mark workflow as failed
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
	logger.Info().
		Dur("duration_ms", duration).
		Msg("node failure handled")

	return nil
}

// markWorkflowFailedWithReason marks a workflow as failed with a specific error reason
func (c *Coordinator) markWorkflowFailedWithReason(ctx context.Context, runID, workflowID, reason string, logger zerolog.Logger, span trace.Span) error {
	return c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		// Check current status - only mark as failed if still running/pending
		currentStatus, err := c.Repository.GetWorkflowRunStatus(ctx, tx, runID)
		if err != nil {
			return fmt.Errorf("failed to check workflow status: %w", err)
		}

		// Only update if workflow is still in a non-terminal state
		if currentStatus != "FAILED" && currentStatus != "COMPLETED" && currentStatus != "SUCCEEDED" {
			if err := c.Repository.MarkWorkflowFailed(ctx, tx, runID, workflowID, reason, logger, span); err != nil {
				return err
			}
			logger.Error().
				Str("failure_reason", reason).
				Msg("workflow marked as failed: could not create task instance")
		} else {
			logger.Debug().
				Str("current_status", currentStatus).
				Msg("workflow already in terminal state, not marking as failed")
		}

		return nil
	})
}

// RetryWorkflowInstance retries a workflow instance from a specific node
// It accepts optional config and input to replace the saved context, or uses the saved context if not provided
// If workflowVersion is 0, it uses the deployed version
func (c *Coordinator) RetryWorkflowInstance(ctx context.Context, p *model.RetryWorkflowRequest) error {
	// Start tracing span
	ctx, span := util.StartWorkflowSpan(ctx, "retry", "", p.RunID)
	defer util.EndSpan(span, nil)

	logger := c.Logger.With().
		Str("run_id", p.RunID).
		Str("node_id", p.NodeID).
		Int("workflow_version", p.WorkflowVersion).
		Logger()

	logger.Info().Msg("retrying workflow instance")

	startTime := time.Now()

	// Get workflow instance to retrieve workflow ID and current version
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

	// Determine workflow version to use
	workflowVersion := p.WorkflowVersion
	if workflowVersion == 0 {
		// Get deployed version
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

	// Load workflow definition to get nodes and edges (before transaction)
	wfSpec, err := c.loadAndParseWorkflowSpec(ctx, workflowID, workflowVersion)
	if err != nil {
		logger.Error().Err(err).Msg("failed to load workflow definition")
		util.RecordError(span, err)
		return err
	}

	// Find the restart node
	restartNode := wfSpec.GetNodeByID(p.NodeID)
	if restartNode == nil {
		return fmt.Errorf("node %s not found in workflow definition", p.NodeID)
	}

	// Find all descendant nodes to clean up
	descendantNodeIDs := wfSpec.FindAllDescendantNodes(p.NodeID)

	err = c.ExecuteWithTransaction(ctx, span, logger, func(ctx context.Context, tx pgx.Tx) error {
		// Load current run context
		runContext, err := c.Repository.LoadRunContext(ctx, tx, p.RunID, logger, span)
		if err != nil {
			return err
		}

		// Get workflow input for updating
		var workflowInput json.RawMessage
		err = tx.QueryRow(ctx, `SELECT input_json FROM workflow_runs WHERE id=$1`, p.RunID).
			Scan(&workflowInput)
		if err != nil {
			logger.Error().Err(err).Msg("failed to load workflow input")
			util.RecordError(span, err)
			return fmt.Errorf("failed to load workflow input: %w", err)
		}

		// Update input and config if provided
		if len(p.Input) > 0 {
			runContext.Input = p.Input
			workflowInput = p.Input
		}
		if len(p.ExecutionOptions) > 0 {
			runContext.Config = p.ExecutionOptions
		}

		// Remove task results for descendant nodes (they will be re-executed)
		for _, nodeID := range descendantNodeIDs {
			delete(runContext.Tasks, nodeID)
		}
		// Also remove the restart node itself if it has results
		delete(runContext.Tasks, p.NodeID)

		// Update workflow run: reset status to RUNNING, update context and input, update version if changed
		if err := c.Repository.UpdateWorkflowRunStatus(ctx, tx, p.RunID, "RUNNING", workflowVersion); err != nil {
			return err
		}

		// Update run context and input
		if err := c.Repository.UpdateRunContext(ctx, tx, p.RunID, runContext, logger, span); err != nil {
			return err
		}

		// Update input if changed
		if len(p.Input) > 0 {
			if err := c.Repository.UpdateWorkflowInput(ctx, tx, p.RunID, p.Input); err != nil {
				return err
			}
		}

		// Delete task instances for restart node and all descendant nodes
		nodeIDsToDelete := append(descendantNodeIDs, p.NodeID)
		if err := c.Repository.DeleteTaskInstances(ctx, tx, p.RunID, nodeIDsToDelete, logger, span); err != nil {
			return err
		}

		// Create new task instance for restart node
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
	logger.Info().
		Dur("duration_ms", duration).
		Msg("workflow retry started successfully")

	util.RecordWorkflowStarted(ctx, workflowID, p.RunID)
	util.SetSpanAttributes(span,
		util.AttributeInt("workflow.duration_ms", duration.Milliseconds()),
		util.Attribute("workflow.retry_node_id", p.NodeID),
	)

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
