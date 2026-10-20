package model

import (
	"encoding/json"
	"fmt"
)

// WorkflowDAG is an indexed, read-only view of a WorkflowDefinitionSpec.
// Pre-built adjacency maps make all graph queries O(1) or O(degree) instead of O(nodes+edges).
type WorkflowDAG struct {
	nodes     map[string]*Node
	outgoing  map[string][]*Edge // nodeID → outgoing edges
	incoming  map[string][]*Edge // nodeID → incoming edges
	entryNode *Node              // first node (in spec order) with no incoming edges
}

// BuildDAG constructs a WorkflowDAG from a spec, pre-building all index structures.
func BuildDAG(spec *WorkflowDefinitionSpec) *WorkflowDAG {
	dag := &WorkflowDAG{
		nodes:    make(map[string]*Node, len(spec.Nodes)),
		outgoing: make(map[string][]*Edge, len(spec.Nodes)),
		incoming: make(map[string][]*Edge, len(spec.Nodes)),
	}

	for i := range spec.Nodes {
		dag.nodes[spec.Nodes[i].NodeID] = &spec.Nodes[i]
	}

	for i := range spec.Edges {
		e := &spec.Edges[i]
		dag.outgoing[e.From] = append(dag.outgoing[e.From], e)
		dag.incoming[e.To] = append(dag.incoming[e.To], e)
	}

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

// OutgoingEdges returns the edges leaving nodeID.
func (d *WorkflowDAG) OutgoingEdges(nodeID string) []*Edge {
	return d.outgoing[nodeID]
}

// IncomingEdges returns the edges entering nodeID.
func (d *WorkflowDAG) IncomingEdges(nodeID string) []*Edge {
	return d.incoming[nodeID]
}

// PredecessorOutput returns the output of the first completed predecessor of nodeID.
// Used by SWITCH nodes which pass through their predecessor's output.
func (d *WorkflowDAG) PredecessorOutput(nodeID string, tasks map[string]*StepResult) json.RawMessage {
	for _, e := range d.incoming[nodeID] {
		if result, ok := tasks[e.From]; ok && result != nil &&
			result.Status.IsCompleted() && len(result.Output) > 0 {
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
