package model_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/mohitkumar/composer/pkg/model"
)

func TestTaskRefs(t *testing.T) {
	node := func(id string, typ model.NodeType, spec string) model.Node {
		return model.Node{NodeID: id, Type: typ, Spec: json.RawMessage(spec)}
	}
	dag := model.BuildDAG(&model.WorkflowDefinitionSpec{
		Nodes: []model.Node{
			node("p", model.NodeTypeHTTP, `{}`),
			node("s", model.NodeTypeSwitch, `{}`),
			node("dyn", model.NodeTypeHTTP, `{"v": "{{tasks[input.key].output}}"}`),
			node("q", model.NodeTypeHTTP, `{"v": "{{tasks[\"my-node\"].output.x + tasks.other.output.y}}", "url": "/api/tasks/1"}`),
		},
		Edges: []model.Edge{
			{From: "p", To: "s"},
			{From: "s", To: "dyn", Condition: "{{tasks.p.output.n > 1}}"},
		},
	})

	if refs, ok := dag.TaskRefs("s"); !ok || !slices.Equal(refs, []string{"p", "p"}) {
		t.Fatalf("switch reads its conditions and passes its predecessor through: %v %v", refs, ok)
	}
	if _, ok := dag.TaskRefs("dyn"); ok {
		t.Fatal("dynamic task access must fall back to loading everything")
	}
	if refs, ok := dag.TaskRefs("q"); !ok || !slices.Equal(refs, []string{"my-node", "other"}) {
		t.Fatalf("quoted refs: %v %v", refs, ok)
	}
}

func TestNodeRetryDelay(t *testing.T) {
	n := model.Node{Retry: &model.RetryPolicy{MaxAttempts: 3, BackoffSeconds: 2}}
	if n.MaxAttempts() != 3 {
		t.Fatalf("max attempts: %d", n.MaxAttempts())
	}
	if d := n.RetryDelay(1).Seconds(); d != 2 {
		t.Fatalf("retry 1 delay %v", d)
	}
	if d := n.RetryDelay(3).Seconds(); d != 8 {
		t.Fatalf("retry 3 delay %v", d)
	}
	if (&model.Node{}).MaxAttempts() != 1 {
		t.Fatal("default max attempts should be 1")
	}
}
