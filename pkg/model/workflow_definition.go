package model

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// WorkflowDefinitionSpec represents the workflow definition structure (the spec JSON)
// This is what gets stored in the spec_json column
// Note: workflow_id and name are stored in the workflow_definitions table, not in the spec
type WorkflowDefinitionSpec struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Node represents a node in the workflow DAG
type Node struct {
	NodeID  string          `json:"node_id"`
	Name    string          `json:"name"`
	Type    NodeType        `json:"type"`    // TASK, SWITCH, PARALLEL, WAIT, etc
	Runtime string          `json:"runtime"` // go/python/js/http/sql etc
	Spec    json.RawMessage `json:"spec"`    // task implementation definition (http spec, code reference etc)
}

// Edge represents an edge in the workflow DAG
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	// TODO: Condition expression or predicate for conditional edges
}

// WorkflowDefinitionRecord represents a workflow definition record in the database
type WorkflowDefinitionRecord struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Version    int             `json:"version"`
	Spec       json.RawMessage `json:"spec"` // Contains WorkflowDefinitionSpec as JSON
	CreatedBy  string          `json:"created_by,omitempty"`
	Enabled    bool            `json:"enabled"`
	WebhookURL sql.NullString  `json:"webhook_url,omitempty"`
	Deployed   bool            `json:"deployed"`
	DeployedAt sql.NullTime    `json:"deployed_at,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// RegisterWorkflowRequest represents a request to register a new workflow definition
type RegisterWorkflowRequest struct {
	WorkflowID string          `json:"workflow_id"`
	Name       string          `json:"name"`
	Version    int             `json:"version"`
	Spec       json.RawMessage `json:"spec"` // Should contain WorkflowDefinitionSpec as JSON
	CreatedBy  string          `json:"created_by,omitempty"`
}

// GetNodeByID returns a node by its ID, or nil if not found
func (wf *WorkflowDefinitionSpec) GetNodeByID(nodeID string) *Node {
	for i := range wf.Nodes {
		if wf.Nodes[i].NodeID == nodeID {
			return &wf.Nodes[i]
		}
	}
	return nil
}

// GetNodeMap returns a map of node ID to node pointer for efficient lookups
func (wf *WorkflowDefinitionSpec) GetNodeMap() map[string]*Node {
	nodeMap := make(map[string]*Node, len(wf.Nodes))
	for i := range wf.Nodes {
		nodeMap[wf.Nodes[i].NodeID] = &wf.Nodes[i]
	}
	return nodeMap
}

// FindStartNode finds the start node of the workflow.
// If startNodeID is provided, it returns that node.
// Otherwise, it finds nodes with no incoming edges (entry points).
// Returns an error if no start node is found.
func (wf *WorkflowDefinitionSpec) FindStartNode(startNodeID string) (*Node, error) {
	if startNodeID != "" {
		// Use provided start node ID
		node := wf.GetNodeByID(startNodeID)
		if node == nil {
			return nil, fmt.Errorf("start node %s not found in workflow definition", startNodeID)
		}
		return node, nil
	}

	// Find nodes with no incoming edges (entry points)
	hasIncoming := make(map[string]bool)
	for _, edge := range wf.Edges {
		hasIncoming[edge.To] = true
	}

	for i := range wf.Nodes {
		if !hasIncoming[wf.Nodes[i].NodeID] {
			return &wf.Nodes[i], nil
		}
	}

	return nil, errors.New("no start node found: either provide start_node_id or ensure workflow has entry nodes")
}

// FindNextNodes returns all nodes that come after the given nodeID in the workflow DAG.
// Returns an empty slice if there are no next nodes.
func (wf *WorkflowDefinitionSpec) FindNextNodes(nodeID string) []*Node {
	nextNodeIDs := make([]string, 0)
	for _, edge := range wf.Edges {
		if edge.From == nodeID {
			nextNodeIDs = append(nextNodeIDs, edge.To)
		}
	}

	if len(nextNodeIDs) == 0 {
		return nil
	}

	nextNodes := make([]*Node, 0, len(nextNodeIDs))
	nodeMap := wf.GetNodeMap()
	for _, nextNodeID := range nextNodeIDs {
		if node := nodeMap[nextNodeID]; node != nil {
			nextNodes = append(nextNodes, node)
		}
	}

	return nextNodes
}

// HasIncomingEdges checks if a node has any incoming edges
func (wf *WorkflowDefinitionSpec) HasIncomingEdges(nodeID string) bool {
	for _, edge := range wf.Edges {
		if edge.To == nodeID {
			return true
		}
	}
	return false
}

// HasOutgoingEdges checks if a node has any outgoing edges
func (wf *WorkflowDefinitionSpec) HasOutgoingEdges(nodeID string) bool {
	for _, edge := range wf.Edges {
		if edge.From == nodeID {
			return true
		}
	}
	return false
}

// FindAllDescendantNodes returns all nodes that are reachable from the given nodeID (transitive descendants only, not including the starting node).
// Uses BFS to traverse the DAG and collect all descendant node IDs.
func (wf *WorkflowDefinitionSpec) FindAllDescendantNodes(nodeID string) []string {
	visited := make(map[string]bool)
	queue := []string{nodeID}
	descendants := make([]string, 0)

	// Build adjacency list for efficient traversal
	adjacencyList := make(map[string][]string)
	for _, edge := range wf.Edges {
		adjacencyList[edge.From] = append(adjacencyList[edge.From], edge.To)
	}

	// Mark starting node as visited (but don't add it to descendants)
	visited[nodeID] = true

	// BFS traversal starting from children of the starting node
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		// Add all children to queue and descendants list
		for _, child := range adjacencyList[current] {
			if !visited[child] {
				visited[child] = true
				queue = append(queue, child)
				descendants = append(descendants, child)
			}
		}
	}

	return descendants
}
