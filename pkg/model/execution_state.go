package model

import (
	"encoding/json"
	"fmt"
	"slices"
)

// TemplateResolver evaluates CEL expressions against a RunContext.
// Implemented by pkg/template.Resolver; declared here as an interface to avoid
// a circular import between pkg/model and pkg/template.
type TemplateResolver interface {
	ResolveTemplates(spec interface{}, ctx *RunContext) (interface{}, error)
}

// NodeStates maps node ID → task state for every node of a run that has a
// task instance. Nodes without an entry have not been reached yet.
type NodeStates map[string]TaskInstanceStatus

// ControlNode is a SWITCH, FORK or JOIN that was resolved inline during routing.
// These nodes are instantly completed by the coordinator (no worker execution).
type ControlNode struct {
	Node   *Node
	Output json.RawMessage // output to persist; predecessor pass-through for SWITCH
}

// RoutingResult is everything a routing step decided; the executor persists it.
type RoutingResult struct {
	// ExecutableNodes are worker tasks to create and enqueue.
	ExecutableNodes []*Node
	// ControlNodes are control-flow nodes to persist as instantly completed.
	ControlNodes []ControlNode
	// SkippedNodes are nodes on branches that can no longer be taken.
	SkippedNodes []*Node
}

// IsEmpty reports whether the routing step produced nothing to persist.
func (r *RoutingResult) IsEmpty() bool {
	return len(r.ExecutableNodes) == 0 && len(r.ControlNodes) == 0 && len(r.SkippedNodes) == 0
}

// ExecutionState is the workflow state machine for a single run.
// It combines the cached, immutable WorkflowDAG with the run's mutable
// RunContext and the current state of each node. All routing logic lives here;
// the executor only persists the returned RoutingResult.
//
// Scheduling rule: a node becomes ready once every incoming edge is resolved.
// An edge is active when its source completed (and, for a SWITCH, selected
// this target); it is inactive when the source was skipped or the SWITCH
// selected another branch. A ready node runs if any incoming edge is active
// and is skipped otherwise, which propagates skips down untaken branches.
// This gives fork/join, diamonds, and SWITCH re-convergence one uniform
// semantics, regardless of branch length.
//
// The caller must serialize ExecutionState use per run (the coordinator holds
// the workflow_runs row lock); it is not safe for concurrent use.
type ExecutionState struct {
	dag    *WorkflowDAG
	ctx    *RunContext
	tmpl   TemplateResolver
	states NodeStates
}

// NewExecutionState creates an ExecutionState for one run. states holds the
// persisted state of every node that already has a task instance.
func NewExecutionState(dag *WorkflowDAG, ctx *RunContext, tmpl TemplateResolver, states NodeStates) *ExecutionState {
	if states == nil {
		states = NodeStates{}
	}
	if ctx.Tasks == nil {
		ctx.Tasks = map[string]*StepResult{}
	}
	return &ExecutionState{dag: dag, ctx: ctx, tmpl: tmpl, states: states}
}

// RunContext returns the current run context, including SWITCH pass-through
// entries added during routing that the executor must persist.
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

// Begin activates node unconditionally (the run's start node, or the node a
// retry restarts from) and resolves everything that becomes ready as a result.
func (e *ExecutionState) Begin(node *Node) (*RoutingResult, error) {
	result := &RoutingResult{}
	next, err := e.activate(node, result)
	if err != nil {
		return nil, err
	}
	if err := e.propagate(next, result); err != nil {
		return nil, err
	}
	return result, nil
}

// Advance records that completedNodeID finished successfully and resolves
// every downstream node that became ready.
func (e *ExecutionState) Advance(completedNodeID string) (*RoutingResult, error) {
	if e.dag.GetNode(completedNodeID) == nil {
		return nil, fmt.Errorf("node %s not found in workflow definition", completedNodeID)
	}
	e.states[completedNodeID] = TaskInstanceStatusCompleted

	result := &RoutingResult{}
	if err := e.propagate(e.dag.Successors(completedNodeID), result); err != nil {
		return nil, err
	}
	return result, nil
}

// propagate walks forward from candidates, running or skipping each node whose
// incoming edges are all resolved. Nodes that are still waiting are left alone;
// a later completion re-examines them.
func (e *ExecutionState) propagate(candidates []*Node, result *RoutingResult) error {
	queue := slices.Clone(candidates)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if _, reached := e.states[n.NodeID]; reached {
			continue
		}

		switch e.readiness(n) {
		case nodeWaiting:
			continue
		case nodeSkip:
			e.states[n.NodeID] = TaskInstanceStatusSkipped
			result.SkippedNodes = append(result.SkippedNodes, n)
			queue = append(queue, e.dag.Successors(n.NodeID)...)
		case nodeRun:
			next, err := e.activate(n, result)
			if err != nil {
				return err
			}
			queue = append(queue, next...)
		}
	}
	return nil
}

// activate starts a node. Worker nodes are queued for dispatch; control-flow
// nodes complete inline and return their successors for further propagation.
func (e *ExecutionState) activate(n *Node, result *RoutingResult) ([]*Node, error) {
	switch n.Type {
	case NodeTypeSwitch:
		output := e.dag.PredecessorOutput(n.NodeID, e.ctx.Tasks)
		if output == nil {
			output = json.RawMessage("{}")
		}
		branches, err := e.selectBranches(n)
		if err != nil {
			return nil, fmt.Errorf("switch %s: %w", n.NodeID, err)
		}
		// Record the pass-through output and the decision so downstream
		// conditions and later join checks see them.
		e.ctx.Tasks[n.NodeID] = &StepResult{
			Status:   TaskInstanceStatusCompleted,
			Output:   output,
			Branches: branches,
		}
		e.states[n.NodeID] = TaskInstanceStatusCompleted
		result.ControlNodes = append(result.ControlNodes, ControlNode{Node: n, Output: output})
		return e.dag.Successors(n.NodeID), nil

	case NodeTypeFork, NodeTypeJoin:
		e.states[n.NodeID] = TaskInstanceStatusCompleted
		result.ControlNodes = append(result.ControlNodes, ControlNode{Node: n, Output: json.RawMessage("{}")})
		return e.dag.Successors(n.NodeID), nil

	default:
		e.states[n.NodeID] = TaskInstanceStatusPending
		result.ExecutableNodes = append(result.ExecutableNodes, n)
		return nil, nil
	}
}

type readiness int

const (
	nodeWaiting readiness = iota // some incoming edge is unresolved
	nodeRun                      // all edges resolved, at least one active
	nodeSkip                     // all edges resolved, none active
)

func (e *ExecutionState) readiness(n *Node) readiness {
	anyActive := false
	for _, edge := range e.dag.IncomingEdges(n.NodeID) {
		active, resolved := e.edgeState(edge)
		if !resolved {
			return nodeWaiting
		}
		anyActive = anyActive || active
	}
	if anyActive {
		return nodeRun
	}
	return nodeSkip
}

// edgeState reports whether an edge is resolved and, if so, whether it is active.
func (e *ExecutionState) edgeState(edge *Edge) (active, resolved bool) {
	state, reached := e.states[edge.From]
	switch {
	case !reached:
		return false, false
	case state == TaskInstanceStatusSkipped:
		return false, true
	case !state.IsCompleted():
		return false, false // pending, running or failed
	}

	from := e.dag.GetNode(edge.From)
	if from == nil || from.Type != NodeTypeSwitch {
		return true, true
	}
	return slices.Contains(e.switchBranches(from), edge.To), true
}

// switchBranches returns the successors a completed SWITCH selected. Runs
// persisted before decisions were recorded fall back to re-evaluation.
func (e *ExecutionState) switchBranches(n *Node) []string {
	if res := e.ctx.Tasks[n.NodeID]; res != nil && res.Branches != nil {
		return res.Branches
	}
	branches, err := e.selectBranches(n)
	if err != nil {
		return nil
	}
	return branches
}

// selectBranches evaluates a SWITCH's edge conditions. Unconditional edges are
// always selected. The result is non-nil so an empty decision is still recorded.
func (e *ExecutionState) selectBranches(n *Node) ([]string, error) {
	branches := []string{}
	for _, edge := range e.dag.OutgoingEdges(n.NodeID) {
		if edge.Condition != "" {
			ok, err := e.evalCondition(edge.Condition)
			if err != nil {
				return nil, fmt.Errorf("condition eval for edge %s→%s: %w", edge.From, edge.To, err)
			}
			if !ok {
				continue
			}
		}
		branches = append(branches, edge.To)
	}
	return branches, nil
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
		return nil, fmt.Errorf("marshal resolved input for node %s: %w", node.NodeID, err)
	}
	return out, nil
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
		return true, nil
	}
}
