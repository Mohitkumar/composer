package model_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/mohitkumar/composer/pkg/model"
)

func strPtr(s string) *string { return &s }

func TestNodeQueueAndRoutingKey(t *testing.T) {
	cases := []struct {
		node        model.Node
		routing     string
		wantedQueue string
	}{
		{model.Node{Type: model.NodeTypeHTTP}, "HTTP", "http"},
		{model.Node{Type: model.NodeTypeCode, Runtime: strPtr("python")}, "python", "python"},
		{model.Node{Type: model.NodeTypeCode, Runtime: strPtr("py")}, "py", "python"},
		{model.Node{Type: model.NodeTypeCode, Runtime: strPtr("javascript")}, "javascript", "js"},
		{model.Node{Type: model.NodeTypeCode, Runtime: strPtr("node")}, "node", "js"},
		{model.Node{Type: model.NodeTypeCode}, "python", "python"}, // runtime defaults to python
		{model.Node{Type: model.NodeTypeHTTP, TaskQueue: "GPU"}, "HTTP", "gpu"},
	}
	for _, c := range cases {
		if got := c.node.GetNodeTypeForRouting(); got != c.routing {
			t.Errorf("%+v routing key: got %s, want %s", c.node, got, c.routing)
		}
		if got := c.node.Queue(); got != c.wantedQueue {
			t.Errorf("%+v queue: got %s, want %s", c.node, got, c.wantedQueue)
		}
	}
}

func TestStatusPredicates(t *testing.T) {
	for s, terminal := range map[model.WorkflowInstanceStatus]bool{
		model.WorkflowInstanceStatusRunning: false, model.WorkflowInstanceStatusPending: false,
		model.WorkflowInstanceStatusSucceeded: true, model.WorkflowInstanceStatusFailed: true,
		model.WorkflowInstanceStatusCompleted: true,
	} {
		if s.IsTerminal() != terminal {
			t.Errorf("%s.IsTerminal() = %v", s, !terminal)
		}
	}
	if !model.TaskInstanceStatusSucceeded.IsCompleted() || model.TaskInstanceStatusSkipped.IsCompleted() {
		t.Error("IsCompleted must accept COMPLETED/SUCCEEDED only")
	}
	if !model.NodeTypeJoin.IsControlFlow() || model.NodeTypeWait.IsControlFlow() {
		t.Error("IsControlFlow: SWITCH/FORK/JOIN only")
	}
	if !model.NodeTypeWaitForEvent.IsWait() || model.NodeTypeHTTP.IsWait() {
		t.Error("IsWait: WAIT/WAIT_FOR_EVENT only")
	}
}

func TestRunContextPersistsOnlyInputAndConfig(t *testing.T) {
	rc := model.NewRunContext(json.RawMessage(`{"a":1}`), json.RawMessage(`{"c":2}`))
	rc.Tasks["n"] = &model.StepResult{Output: json.RawMessage(`{}`)}

	stored, _ := json.Marshal(rc.Persisted())
	if string(stored) != `{"input":{"a":1},"config":{"c":2}}` {
		t.Fatalf("persisted context: %s", stored)
	}

	// Older rows carried task outputs; they must be dropped on decode, and a
	// missing input falls back to the run's input column.
	decoded, err := model.DecodeRunContext(json.RawMessage(`{"x":1}`),
		json.RawMessage(`{"config":{"c":2},"tasks":{"old":{"status":"COMPLETED"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded.Input) != `{"x":1}` || len(decoded.Tasks) != 0 || string(decoded.Config) != `{"c":2}` {
		t.Fatalf("decoded: %+v", decoded)
	}
	if _, err := model.DecodeRunContext(nil, json.RawMessage(`{bad`)); err == nil {
		t.Fatal("malformed context must error")
	}
}

func TestDAGNavigation(t *testing.T) {
	dag := model.BuildDAG(&model.WorkflowDefinitionSpec{
		Nodes: []model.Node{{NodeID: "a"}, {NodeID: "b"}, {NodeID: "c"}, {NodeID: "d"}},
		Edges: []model.Edge{{From: "a", To: "b"}, {From: "a", To: "c"}, {From: "b", To: "d"}, {From: "c", To: "d"},
			{From: "a", To: "ghost"}}, // edges to unknown nodes are ignored
	})

	if n, err := dag.StartNode(""); err != nil || n.NodeID != "a" {
		t.Fatalf("entry node: %v %v", n, err)
	}
	if n, err := dag.StartNode("c"); err != nil || n.NodeID != "c" {
		t.Fatalf("start hint: %v %v", n, err)
	}
	if _, err := dag.StartNode("nope"); err == nil {
		t.Fatal("unknown start hint must error")
	}

	var succ []string
	for _, n := range dag.Successors("a") {
		succ = append(succ, n.NodeID)
	}
	if !slices.Equal(succ, []string{"b", "c"}) {
		t.Fatalf("successors: %v", succ)
	}
	desc := dag.Descendants("a")
	slices.Sort(desc)
	if !slices.Equal(desc, []string{"b", "c", "d", "ghost"}) {
		t.Fatalf("descendants: %v", desc)
	}

	tasks := map[string]*model.StepResult{
		"b": {Status: model.TaskInstanceStatusFailed, Output: json.RawMessage(`{"b":1}`)},
		"c": {Status: model.TaskInstanceStatusCompleted, Output: json.RawMessage(`{"c":1}`)},
	}
	if out := dag.PredecessorOutput("d", tasks); string(out) != `{"c":1}` {
		t.Fatalf("predecessor output must come from a completed predecessor: %s", out)
	}

	cyclic := model.BuildDAG(&model.WorkflowDefinitionSpec{
		Nodes: []model.Node{{NodeID: "x"}, {NodeID: "y"}},
		Edges: []model.Edge{{From: "x", To: "y"}, {From: "y", To: "x"}},
	})
	if _, err := cyclic.StartNode(""); err == nil {
		t.Fatal("a graph without an entry node must error")
	}
}
