package statemachine

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/mohitkumar/composer/pkg/model"
)

// propagate walks forward from candidates, running or skipping each node whose
// incoming edges are all resolved. Nodes still waiting on a predecessor are
// left alone; a later event re-examines them.
func (m *Machine) propagate(candidates []*model.Node) error {
	queue := slices.Clone(candidates)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if _, reached := m.run.Nodes[n.NodeID]; reached {
			continue
		}

		switch m.readiness(n) {
		case nodeWaiting:
			continue
		case nodeSkip:
			m.emit(CreateTask{TaskID: m.deps.NewID(), Node: n, Status: model.TaskInstanceStatusSkipped})
			queue = append(queue, m.def.Successors(n.NodeID)...)
		case nodeRun:
			next, err := m.activate(n)
			if err != nil {
				return err
			}
			queue = append(queue, next...)
		}
	}
	return nil
}

// activate starts a ready node and returns the nodes to consider next:
// control nodes and waits that are already satisfied complete inline and pass
// routing on to their successors; worker nodes and real waits stop it.
func (m *Machine) activate(n *model.Node) ([]*model.Node, error) {
	switch {
	case n.Type == model.NodeTypeSwitch:
		return m.activateSwitch(n)
	case n.Type.IsControlFlow(): // FORK, JOIN
		m.emit(CreateTask{TaskID: m.deps.NewID(), Node: n, Status: model.TaskInstanceStatusCompleted,
			Output: json.RawMessage("{}")})
		return m.def.Successors(n.NodeID), nil
	case n.Type.IsWait():
		return m.park(n)
	default:
		return nil, m.schedule(n)
	}
}

// activateSwitch passes its predecessor's output through and records which
// branches its conditions select.
func (m *Machine) activateSwitch(n *model.Node) ([]*model.Node, error) {
	if err := m.ensureOutputs(n); err != nil {
		return nil, err
	}
	output := m.def.PredecessorOutput(n.NodeID, m.run.Context.Tasks)
	if output == nil {
		output = json.RawMessage("{}")
	}
	branches, err := m.selectBranches(n)
	if err != nil {
		return nil, fmt.Errorf("switch %s: %w", n.NodeID, err)
	}
	m.emit(CreateTask{TaskID: m.deps.NewID(), Node: n, Status: model.TaskInstanceStatusCompleted,
		Output: output, Branches: branches})
	m.setOutput(n.NodeID, output)
	return m.def.Successors(n.NodeID), nil
}

// schedule creates a worker node's task and dispatches it. A node whose input
// cannot be resolved is recorded as FAILED instead.
func (m *Machine) schedule(n *model.Node) error {
	input, err := m.resolveInput(n)
	if err != nil {
		if isStoreError(err) {
			return err
		}
		m.emit(CreateTask{TaskID: m.deps.NewID(), Node: n, Status: model.TaskInstanceStatusFailed, Error: err.Error()})
		return nil
	}
	id := m.deps.NewID()
	m.emit(CreateTask{TaskID: id, Node: n, Status: model.TaskInstanceStatusPending, Input: input})
	m.emit(DispatchTask{TaskID: id, Node: n, Input: input, NotBefore: m.now})
	return nil
}

// ── Readiness ────────────────────────────────────────────────────────────────

type readiness int

const (
	nodeWaiting readiness = iota // some incoming edge is unresolved
	nodeRun                      // all edges resolved, at least one active
	nodeSkip                     // all edges resolved, none active
)

func (m *Machine) readiness(n *model.Node) readiness {
	anyActive := false
	for _, edge := range m.def.IncomingEdges(n.NodeID) {
		active, resolved := m.edgeState(edge)
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
func (m *Machine) edgeState(edge *model.Edge) (active, resolved bool) {
	from := m.run.Nodes[edge.From]
	switch {
	case from == nil:
		return false, false
	case from.Status == model.TaskInstanceStatusSkipped:
		return false, true
	case !from.Status.IsCompleted():
		return false, false // pending, running, waiting or failed
	}
	if node := m.def.GetNode(edge.From); node == nil || node.Type != model.NodeTypeSwitch {
		return true, true
	}
	return slices.Contains(m.switchBranches(edge.From, from), edge.To), true
}

// switchBranches returns the successors a completed SWITCH selected. Runs
// persisted before decisions were recorded fall back to re-evaluation.
func (m *Machine) switchBranches(nodeID string, s *NodeState) []string {
	if s.Branches != nil {
		return s.Branches
	}
	node := m.def.GetNode(nodeID)
	if err := m.ensureOutputs(node); err != nil {
		return nil
	}
	branches, err := m.selectBranches(node)
	if err != nil {
		return nil
	}
	return branches
}

// selectBranches evaluates a SWITCH's edge conditions. Unconditional edges are
// always selected. The result is non-nil so an empty decision is still recorded.
func (m *Machine) selectBranches(n *model.Node) ([]string, error) {
	branches := []string{}
	for _, edge := range m.def.OutgoingEdges(n.NodeID) {
		if edge.Condition != "" {
			ok, err := m.evalCondition(edge.Condition)
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

// ── Templates ────────────────────────────────────────────────────────────────

// resolveInput resolves the CEL templates in node.Spec. A *StoreError is
// transient; any other error means the node's templates cannot be resolved.
func (m *Machine) resolveInput(node *model.Node) (json.RawMessage, error) {
	if err := m.ensureOutputs(node); err != nil {
		return nil, err
	}
	var spec any
	if len(node.Spec) > 0 {
		if err := json.Unmarshal(node.Spec, &spec); err != nil {
			spec = string(node.Spec) // treat non-JSON specs as a plain string
		}
	}
	resolved, err := m.deps.Templates.ResolveTemplates(spec, m.run.Context)
	if err != nil {
		return nil, fmt.Errorf("CEL resolution for node %s: %w", node.NodeID, err)
	}
	out, err := json.Marshal(resolved)
	if err != nil {
		return nil, fmt.Errorf("marshal resolved input for node %s: %w", node.NodeID, err)
	}
	return out, nil
}

// evalCondition evaluates a CEL condition against the run context.
func (m *Machine) evalCondition(condition string) (bool, error) {
	val, err := m.deps.Templates.ResolveTemplates(condition, m.run.Context)
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
