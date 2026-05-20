package model

import (
	"encoding/json"
	"fmt"
)

// TemplateResolver evaluates CEL expressions against a RunContext.
// Implemented by pkg/template.Resolver; declared here as an interface to avoid
// a circular import between pkg/model and pkg/template.
type TemplateResolver interface {
	ResolveTemplates(spec interface{}, ctx *RunContext) (interface{}, error)
}

// ControlNode is a SWITCH or FORK that was resolved inline during routing.
// These nodes are instantly completed by the coordinator (no worker execution).
type ControlNode struct {
	Node   *Node
	Output json.RawMessage // output to persist; predecessor pass-through for SWITCH
}

// RoutingResult is returned by ExecutionState.NextNodes.
// It separates what the executor needs to persist into two categories.
type RoutingResult struct {
	// ExecutableNodes are worker tasks to create and enqueue.
	ExecutableNodes []*Node
	// ControlNodes are SWITCH/FORK nodes to persist as instantly-completed.
	ControlNodes []ControlNode
}

// IsEmpty reports whether there are no nodes to process in either category.
func (r *RoutingResult) IsEmpty() bool {
	return len(r.ExecutableNodes) == 0 && len(r.ControlNodes) == 0
}

// ExecutionState is the workflow state machine for a single execution.
// It combines a cached, immutable WorkflowDAG with the mutable RunContext for
// one run. All routing logic lives here; the executor is responsible only for
// persisting the returned actions to the database.
//
// ExecutionState is not safe for concurrent use — create one per HandleNodeCompleted call.
type ExecutionState struct {
	dag  *WorkflowDAG
	ctx  *RunContext
	tmpl TemplateResolver
}

// NewExecutionState creates an ExecutionState for a single workflow execution.
// dag is the cached topology; ctx is the run-specific state loaded from the DB.
func NewExecutionState(dag *WorkflowDAG, ctx *RunContext, tmpl TemplateResolver) *ExecutionState {
	return &ExecutionState{dag: dag, ctx: ctx, tmpl: tmpl}
}

// RunContext returns the current run context.
// After NextNodes is called, the context may contain updated SWITCH pass-through
// entries that the executor must persist to the database.
func (e *ExecutionState) RunContext() *RunContext {
	return e.ctx
}

// GetNode returns the node with the given ID.
func (e *ExecutionState) GetNode(id string) *Node {
	return e.dag.GetNode(id)
}

// StartNode returns the workflow entry node.
func (e *ExecutionState) StartNode(hint string) (*Node, error) {
	return e.dag.StartNode(hint)
}

// Descendants returns all node IDs reachable from nodeID (BFS, excluding nodeID itself).
func (e *ExecutionState) Descendants(nodeID string) []string {
	return e.dag.Descendants(nodeID)
}

// IsBranchNode reports whether nodeID is a direct child of a FORK node.
// When true the caller must query the DB for the completed-branch count
// and pass it to NextNodes.
func (e *ExecutionState) IsBranchNode(nodeID string) bool {
	return e.dag.ForkParent(nodeID) != nil
}

// BranchSiblingIDs returns all sibling branch IDs for a FORK branch (including nodeID itself).
// Used by the executor to scope the completed-branch count query.
// Returns nil if nodeID is not a branch node.
func (e *ExecutionState) BranchSiblingIDs(nodeID string) []string {
	forkParent := e.dag.ForkParent(nodeID)
	if forkParent == nil {
		return nil
	}
	return e.dag.BranchesOf(forkParent.NodeID)
}

// NextNodes determines which nodes should execute after completedNodeID finishes.
//
// completedBranchCount is the number of branches (siblings of completedNodeID under
// the same FORK) that are in COMPLETED/SUCCEEDED state in the DB.
// This must be queried by the caller before calling NextNodes when IsBranchNode is true;
// pass 0 for non-branch nodes.
//
// The returned RoutingResult separates:
//   - ExecutableNodes: worker tasks to create and enqueue
//   - ControlNodes: SWITCH/FORK nodes to persist as instantly-completed
//
// NextNodes also updates the in-memory RunContext as SWITCH nodes are traversed,
// so RunContext() reflects the state after full routing resolution.
func (e *ExecutionState) NextNodes(completedNodeID string, completedBranchCount int) (*RoutingResult, error) {
	node := e.dag.GetNode(completedNodeID)
	if node == nil {
		return nil, fmt.Errorf("node %s not found in workflow definition", completedNodeID)
	}

	switch node.Type {
	case NodeTypeSwitch:
		branches, err := e.dag.SuccessorsConditional(completedNodeID, e.evalCondition)
		if err != nil {
			return nil, fmt.Errorf("switch %s: %w", completedNodeID, err)
		}
		return e.expandNodes(branches)

	case NodeTypeFork:
		return e.expandNodes(e.dag.Successors(completedNodeID))

	default:
		if forkParent := e.dag.ForkParent(completedNodeID); forkParent != nil {
			totalBranches := len(e.dag.BranchesOf(forkParent.NodeID))
			if completedBranchCount < totalBranches {
				// Other branches still running — nothing to do yet
				return &RoutingResult{}, nil
			}
			return e.expandNodes(e.dag.JoinNodes(e.dag.BranchesOf(forkParent.NodeID)))
		}
		return e.expandNodes(e.dag.Successors(completedNodeID))
	}
}

// ResolveInput resolves CEL template expressions in node.Spec against the current RunContext.
func (e *ExecutionState) ResolveInput(node *Node) (json.RawMessage, error) {
	var spec interface{}
	if len(node.Spec) > 0 {
		if err := json.Unmarshal(node.Spec, &spec); err != nil {
			// Fall back to treating the raw bytes as a plain string
			spec = string(node.Spec)
		}
	}

	resolved, err := e.tmpl.ResolveTemplates(spec, e.ctx)
	if err != nil {
		return nil, fmt.Errorf("CEL resolution for node %s: %w", node.NodeID, err)
	}

	out, err := json.Marshal(resolved)
	if err != nil {
		return json.RawMessage("{}"), nil
	}
	return out, nil
}

// expandNodes recursively resolves SWITCH/FORK control-flow nodes into their eventual
// executable descendants. It updates the in-memory RunContext for each SWITCH traversed
// so that subsequent condition evaluations see the correct pass-through output.
func (e *ExecutionState) expandNodes(nodes []*Node) (*RoutingResult, error) {
	result := &RoutingResult{}
	for _, n := range nodes {
		switch n.Type {
		case NodeTypeSwitch:
			// SWITCH passes through its predecessor's output
			output := e.dag.PredecessorOutput(n.NodeID, e.ctx.Tasks)
			if output == nil {
				output = json.RawMessage("{}")
			}
			// Update in-memory context so downstream condition evaluations are accurate
			if e.ctx.Tasks == nil {
				e.ctx.Tasks = make(map[string]*StepResult)
			}
			e.ctx.Tasks[n.NodeID] = &StepResult{
				Status: TaskInstanceStatusCompleted,
				Output: output,
			}
			result.ControlNodes = append(result.ControlNodes, ControlNode{Node: n, Output: output})

			branches, err := e.dag.SuccessorsConditional(n.NodeID, e.evalCondition)
			if err != nil {
				return nil, fmt.Errorf("switch %s: %w", n.NodeID, err)
			}
			sub, err := e.expandNodes(branches)
			if err != nil {
				return nil, err
			}
			result.ExecutableNodes = append(result.ExecutableNodes, sub.ExecutableNodes...)
			result.ControlNodes = append(result.ControlNodes, sub.ControlNodes...)

		case NodeTypeFork:
			result.ControlNodes = append(result.ControlNodes, ControlNode{Node: n, Output: json.RawMessage("{}")})
			sub, err := e.expandNodes(e.dag.Successors(n.NodeID))
			if err != nil {
				return nil, err
			}
			result.ExecutableNodes = append(result.ExecutableNodes, sub.ExecutableNodes...)
			result.ControlNodes = append(result.ControlNodes, sub.ControlNodes...)

		default:
			result.ExecutableNodes = append(result.ExecutableNodes, n)
		}
	}
	return result, nil
}

// evalCondition evaluates a CEL condition string against the current RunContext.
func (e *ExecutionState) evalCondition(condition string) (bool, error) {
	val, err := e.tmpl.ResolveTemplates(condition, e.ctx)
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
