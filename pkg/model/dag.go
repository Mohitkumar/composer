package model

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// WorkflowDAG is an indexed, read-only view of a WorkflowDefinitionSpec.
// Pre-built adjacency maps make all graph queries O(1) or O(degree) instead of O(nodes+edges).
type WorkflowDAG struct {
	nodes     map[string]*Node
	outgoing  map[string][]*Edge // nodeID → outgoing edges
	incoming  map[string][]*Edge // nodeID → incoming edges
	entryNode *Node              // first node (in spec order) with no incoming edges
	// taskRefs lists, per node, the task outputs its templates can read; a
	// missing entry means the references could not be determined statically.
	taskRefs map[string][]string
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

	dag.taskRefs = make(map[string][]string, len(spec.Nodes))
	for id, n := range dag.nodes {
		texts := jsonStrings(n.Spec)
		var implicit []string
		if n.Type == NodeTypeSwitch {
			// A SWITCH reads its conditions and passes a predecessor's output through.
			for _, e := range dag.outgoing[id] {
				texts = append(texts, e.Condition)
			}
			for _, e := range dag.incoming[id] {
				implicit = append(implicit, e.From)
			}
		}
		if refs, ok := templateTaskRefs(texts...); ok {
			dag.taskRefs[id] = append(refs, implicit...)
		}
	}

	return dag
}

// TaskRefs returns the node IDs whose outputs nodeID's templates may read.
// ok is false when they cannot be determined, meaning every output may be read.
func (d *WorkflowDAG) TaskRefs(nodeID string) (refs []string, ok bool) {
	refs, ok = d.taskRefs[nodeID]
	return refs, ok
}

var (
	templateExprRe = regexp.MustCompile(`{{\s*([^}]+)\s*}}`)
	tasksIdentRe   = regexp.MustCompile(`\btasks\b`)
	taskRefRe      = regexp.MustCompile(`\btasks\s*(?:\.\s*([A-Za-z_][A-Za-z0-9_]*)|\[\s*["']([^"']+)["']\s*\])`)
)

// templateTaskRefs extracts tasks.<id> and tasks["<id>"] references from the
// {{ }} expressions in texts. It reports false if any expression uses tasks in
// another way (e.g. tasks[someVar]), which could read any output.
func templateTaskRefs(texts ...string) ([]string, bool) {
	refs := []string{}
	for _, text := range texts {
		for _, expr := range templateExprRe.FindAllStringSubmatch(text, -1) {
			body := expr[1]
			matches := taskRefRe.FindAllStringSubmatch(body, -1)
			if len(matches) != len(tasksIdentRe.FindAllStringIndex(body, -1)) {
				return nil, false
			}
			for _, m := range matches {
				if m[1] != "" {
					refs = append(refs, m[1])
				} else {
					refs = append(refs, m[2])
				}
			}
		}
	}
	return refs, true
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

// jsonStrings returns every string value in a JSON document (so escaped quotes
// are decoded), or the raw text if it is not valid JSON.
func jsonStrings(raw json.RawMessage) []string {
	var v any
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return []string{string(raw)}
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			out = append(out, t)
		case map[string]any:
			for k, val := range t {
				out = append(out, k)
				walk(val)
			}
		case []any:
			for _, val := range t {
				walk(val)
			}
		}
	}
	walk(v)
	return out
}
