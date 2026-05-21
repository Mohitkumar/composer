package model

import (
	"encoding/json"
	"fmt"
)

// WorkflowDAG is an indexed, read-only view of a WorkflowDefinitionSpec.
// Pre-built adjacency maps make all graph queries O(1) or O(degree) instead of O(nodes+edges).
type WorkflowDAG struct {
	nodes      map[string]*Node
	outgoing   map[string][]*Edge // nodeID → outgoing edges
	incoming   map[string][]*Edge // nodeID → incoming edges
	forkParent map[string]string  // branchNodeID → forkNodeID, only set for direct FORK children
	entryNode  *Node              // node with no incoming edges (preserves slice order for determinism)
}

// BuildDAG constructs a WorkflowDAG from a spec, pre-building all index structures.
func BuildDAG(spec *WorkflowDefinitionSpec) *WorkflowDAG {
	dag := &WorkflowDAG{
		nodes:      make(map[string]*Node, len(spec.Nodes)),
		outgoing:   make(map[string][]*Edge, len(spec.Nodes)),
		incoming:   make(map[string][]*Edge, len(spec.Nodes)),
		forkParent: make(map[string]string),
	}

	for i := range spec.Nodes {
		dag.nodes[spec.Nodes[i].NodeID] = &spec.Nodes[i]
	}

	for i := range spec.Edges {
		e := &spec.Edges[i]
		dag.outgoing[e.From] = append(dag.outgoing[e.From], e)
		dag.incoming[e.To] = append(dag.incoming[e.To], e)
	}

	// Pre-compute fork parents for O(1) branch detection
	for id, node := range dag.nodes {
		if node.Type == NodeTypeFork {
			for _, e := range dag.outgoing[id] {
				dag.forkParent[e.To] = id
			}
		}
	}

	// Find entry node in original slice order for determinism
	for i := range spec.Nodes {
		if len(dag.incoming[spec.Nodes[i].NodeID]) == 0 {
			dag.entryNode = &spec.Nodes[i]
			break
		}
	}

	return dag
}

// GetNode returns the node with the given ID, or nil if not found.
func (d *WorkflowDAG) GetNode(id string) *Node {
	return d.nodes[id]
}

// StartNode returns the workflow entry node.
// If hint is non-empty, returns that specific node; otherwise returns the node with no incoming edges.
func (d *WorkflowDAG) StartNode(hint string) (*Node, error) {
	if hint != "" {
		n := d.nodes[hint]
		if n == nil {
			return nil, fmt.Errorf("start node %s not found in workflow definition", hint)
		}
		return n, nil
	}
	if d.entryNode != nil {
		return d.entryNode, nil
	}
	return nil, fmt.Errorf("no start node found: ensure workflow has a node with no incoming edges")
}

// Successors returns all nodes directly reachable from nodeID.
func (d *WorkflowDAG) Successors(nodeID string) []*Node {
	edges := d.outgoing[nodeID]
	nodes := make([]*Node, 0, len(edges))
	for _, e := range edges {
		if n := d.nodes[e.To]; n != nil {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

// SuccessorsConditional evaluates edge conditions and returns only matching successors.
// Edges without a condition are always included.
func (d *WorkflowDAG) SuccessorsConditional(nodeID string, evalFn func(string) (bool, error)) ([]*Node, error) {
	var nodes []*Node
	for _, e := range d.outgoing[nodeID] {
		if e.Condition == "" {
			if n := d.nodes[e.To]; n != nil {
				nodes = append(nodes, n)
			}
			continue
		}
		ok, err := evalFn(e.Condition)
		if err != nil {
			return nil, fmt.Errorf("condition eval for edge %s→%s: %w", e.From, e.To, err)
		}
		if ok {
			if n := d.nodes[e.To]; n != nil {
				nodes = append(nodes, n)
			}
		}
	}
	return nodes, nil
}

// ForkParent returns the FORK node that is the direct predecessor of nodeID,
// or nil if nodeID is not a direct FORK branch.
func (d *WorkflowDAG) ForkParent(nodeID string) *Node {
	if forkID, ok := d.forkParent[nodeID]; ok {
		return d.nodes[forkID]
	}
	return nil
}

// BranchesOf returns the IDs of the direct branch children of a FORK node.
func (d *WorkflowDAG) BranchesOf(forkNodeID string) []string {
	edges := d.outgoing[forkNodeID]
	ids := make([]string, 0, len(edges))
	for _, e := range edges {
		ids = append(ids, e.To)
	}
	return ids
}

// JoinNodes returns nodes that have incoming edges from ALL of the given branch node IDs.
// Falls back to any node reachable from any branch if no strict join point is found.
func (d *WorkflowDAG) JoinNodes(branchNodeIDs []string) []*Node {
	// Count how many branch nodes feed into each candidate successor
	incomingCount := make(map[string]int)
	for _, branchID := range branchNodeIDs {
		for _, e := range d.outgoing[branchID] {
			incomingCount[e.To]++
		}
	}

	var join []*Node
	for candidateID, count := range incomingCount {
		if count == len(branchNodeIDs) {
			if n := d.nodes[candidateID]; n != nil {
				join = append(join, n)
			}
		}
	}

	// Fallback: collect any node reachable from at least one branch
	if len(join) == 0 {
		for candidateID := range incomingCount {
			if n := d.nodes[candidateID]; n != nil {
				join = append(join, n)
			}
		}
	}

	return join
}

// PredecessorOutput returns the output of the first completed predecessor of nodeID.
// Used by SWITCH nodes which pass through their predecessor's output.
func (d *WorkflowDAG) PredecessorOutput(nodeID string, tasks map[string]*StepResult) json.RawMessage {
	for _, e := range d.incoming[nodeID] {
		if result, ok := tasks[e.From]; ok && result != nil &&
			result.Status == TaskInstanceStatusCompleted && len(result.Output) > 0 {
			return result.Output
		}
	}
	return nil
}

// Descendants returns all node IDs reachable from nodeID via BFS (nodeID itself is excluded).
func (d *WorkflowDAG) Descendants(nodeID string) []string {
	visited := map[string]bool{nodeID: true}
	queue := []string{nodeID}
	var result []string
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range d.outgoing[cur] {
			if !visited[e.To] {
				visited[e.To] = true
				queue = append(queue, e.To)
				result = append(result, e.To)
			}
		}
	}
	return result
}
