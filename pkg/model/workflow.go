package model

import (
	"encoding/json"
	"errors"
	"fmt"
)

type WorkflowDefinition struct {
	WorkflowID string `json:"workflow_id"`
	Name       string `json:"name"`
	Nodes      []Node `json:"nodes"`
	Edges      []Edge `json:"edges"`
}

type Node struct {
	NodeID  string          `json:"node_id"`
	Name    string          `json:"name"`
	Type    NodeType        `json:"type"`    // TASK, SWITCH, PARALLEL, WAIT, etc (later)
	Runtime string          `json:"runtime"` // go/python/js/http/sql etc
	Spec    json.RawMessage `json:"spec"`    // task impl definition (http spec, code reference etc)
}

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	// later: Condition expression or predicate
}

type NodeType string

const (
	NodeTypeTask     NodeType = "TASK"
	NodeTypeSwitch   NodeType = "SWITCH"
	NodeTypeParallel NodeType = "PARALLEL"
	NodeTypeWait     NodeType = "WAIT"
)

// GetNodeByID returns a node by its ID, or nil if not found
func (wf *WorkflowDefinition) GetNodeByID(nodeID string) *Node {
	for i := range wf.Nodes {
		if wf.Nodes[i].NodeID == nodeID {
			return &wf.Nodes[i]
		}
	}
	return nil
}

// GetNodeMap returns a map of node ID to node pointer for efficient lookups
func (wf *WorkflowDefinition) GetNodeMap() map[string]*Node {
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
func (wf *WorkflowDefinition) FindStartNode(startNodeID string) (*Node, error) {
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
func (wf *WorkflowDefinition) FindNextNodes(nodeID string) []*Node {
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
func (wf *WorkflowDefinition) HasIncomingEdges(nodeID string) bool {
	for _, edge := range wf.Edges {
		if edge.To == nodeID {
			return true
		}
	}
	return false
}

// HasOutgoingEdges checks if a node has any outgoing edges
func (wf *WorkflowDefinition) HasOutgoingEdges(nodeID string) bool {
	for _, edge := range wf.Edges {
		if edge.From == nodeID {
			return true
		}
	}
	return false
}
