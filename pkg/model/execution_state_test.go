package model_test

import (
	"encoding/json"
	"slices"
	"sort"
	"testing"

	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/template"
)

// run simulates the coordinator: each step rebuilds ExecutionState from the
// persisted node states and run context, exactly as HandleNodeCompleted does.
type run struct {
	t      *testing.T
	dag    *model.WorkflowDAG
	tmpl   *template.Resolver
	ctx    *model.RunContext
	states model.NodeStates
	// outputs holds every completed output; load serves them like the DB does.
	outputs map[string]json.RawMessage
	loads   [][]string
}

func (r *run) load(ids []string) (map[string]json.RawMessage, error) {
	r.loads = append(r.loads, ids)
	out := map[string]json.RawMessage{}
	for _, id := range ids {
		out[id] = r.outputs[id]
	}
	return out, nil
}

func newRun(t *testing.T, nodes []model.Node, edges []model.Edge, input string) *run {
	t.Helper()
	tmpl, err := template.NewResolver()
	if err != nil {
		t.Fatal(err)
	}
	return &run{
		t:       t,
		dag:     model.BuildDAG(&model.WorkflowDefinitionSpec{Nodes: nodes, Edges: edges}),
		tmpl:    tmpl,
		ctx:     model.NewRunContext(json.RawMessage(input), nil),
		states:  model.NodeStates{},
		outputs: map[string]json.RawMessage{},
	}
}

func (r *run) state() *model.ExecutionState {
	return model.NewExecutionState(r.dag, r.ctx, r.tmpl, r.states, r.load)
}

func (r *run) start() *model.RoutingResult {
	r.t.Helper()
	es := r.state()
	start, err := es.StartNode("")
	if err != nil {
		r.t.Fatal(err)
	}
	res, err := es.Begin(start)
	if err != nil {
		r.t.Fatal(err)
	}
	r.persist(res)
	return res
}

func (r *run) complete(nodeID, output string) *model.RoutingResult {
	r.t.Helper()
	// Like the coordinator: a fresh context per step, outputs loaded on demand.
	r.ctx.Tasks = map[string]*model.StepResult{}
	r.outputs[nodeID] = json.RawMessage(output)
	es := r.state()
	es.SetOutput(nodeID, json.RawMessage(output))
	res, err := es.Advance(nodeID)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, n := range res.ExecutableNodes {
		if _, err := es.ResolveInput(n); err != nil {
			r.t.Fatal(err)
		}
	}
	r.states[nodeID] = model.NodeState{Status: model.TaskInstanceStatusCompleted}
	r.persist(res)
	return res
}

func (r *run) persist(res *model.RoutingResult) {
	for _, n := range res.ExecutableNodes {
		r.states[n.NodeID] = model.NodeState{Status: model.TaskInstanceStatusPending}
	}
	for _, c := range res.ControlNodes {
		r.states[c.Node.NodeID] = model.NodeState{Status: model.TaskInstanceStatusCompleted, Branches: c.Branches}
		r.outputs[c.Node.NodeID] = c.Output
	}
	for _, n := range res.SkippedNodes {
		r.states[n.NodeID] = model.NodeState{Status: model.TaskInstanceStatusSkipped}
	}
	for _, n := range res.WaitingNodes {
		r.states[n.NodeID] = model.NodeState{Status: model.TaskInstanceStatusWaiting}
	}
}

func ids(nodes []*model.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.NodeID)
	}
	sort.Strings(out)
	return out
}

func controlIDs(nodes []model.ControlNode) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Node.NodeID)
	}
	sort.Strings(out)
	return out
}

func expect(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

func task(id string) model.Node       { return model.Node{NodeID: id, Type: model.NodeTypeHTTP} }
func fork(id string) model.Node       { return model.Node{NodeID: id, Type: model.NodeTypeFork} }
func sw(id string) model.Node         { return model.Node{NodeID: id, Type: model.NodeTypeSwitch} }
func edge(from, to string) model.Edge { return model.Edge{From: from, To: to} }
func cond(from, to, c string) model.Edge {
	return model.Edge{From: from, To: to, Condition: c}
}

func TestLinearChain(t *testing.T) {
	r := newRun(t, []model.Node{task("a"), task("b"), task("c")},
		[]model.Edge{edge("a", "b"), edge("b", "c")}, `{}`)

	expect(t, "start", ids(r.start().ExecutableNodes), "a")
	expect(t, "after a", ids(r.complete("a", `{}`).ExecutableNodes), "b")
	expect(t, "after b", ids(r.complete("b", `{}`).ExecutableNodes), "c")
	if res := r.complete("c", `{}`); !res.IsEmpty() {
		t.Fatalf("expected nothing after the last node, got %+v", res)
	}
}

func TestForkJoinWithMultiNodeBranches(t *testing.T) {
	// fork → a1 → a2 ─┐
	//      → b1 ──────┴→ join
	r := newRun(t,
		[]model.Node{fork("f"), task("a1"), task("a2"), task("b1"), task("join")},
		[]model.Edge{edge("f", "a1"), edge("a1", "a2"), edge("f", "b1"), edge("a2", "join"), edge("b1", "join")},
		`{}`)

	res := r.start()
	expect(t, "fork control", controlIDs(res.ControlNodes), "f")
	expect(t, "fork branches", ids(res.ExecutableNodes), "a1", "b1")

	expect(t, "after a1", ids(r.complete("a1", `{}`).ExecutableNodes), "a2")
	expect(t, "after b1 (join must wait for a2)", ids(r.complete("b1", `{}`).ExecutableNodes))
	expect(t, "after a2", ids(r.complete("a2", `{}`).ExecutableNodes), "join")
}

func TestPlainDiamondWaitsForAllPredecessors(t *testing.T) {
	r := newRun(t, []model.Node{task("a"), task("b"), task("c"), task("d")},
		[]model.Edge{edge("a", "b"), edge("a", "c"), edge("b", "d"), edge("c", "d")}, `{}`)

	r.start()
	expect(t, "fan-out", ids(r.complete("a", `{}`).ExecutableNodes), "b", "c")
	expect(t, "after b", ids(r.complete("b", `{}`).ExecutableNodes))
	expect(t, "after c", ids(r.complete("c", `{}`).ExecutableNodes), "d")
}

func TestSwitchSkipsUntakenBranchAndReconverges(t *testing.T) {
	// p → s ─(n > 1)→ hi ─┐
	//       ─(n <= 1)→ lo → lo2 ─┴→ merge
	r := newRun(t,
		[]model.Node{task("p"), sw("s"), task("hi"), task("lo"), task("lo2"), task("merge")},
		[]model.Edge{
			edge("p", "s"),
			cond("s", "hi", "{{tasks.p.output.n > 1}}"),
			cond("s", "lo", "{{tasks.p.output.n <= 1}}"),
			edge("lo", "lo2"),
			edge("hi", "merge"),
			edge("lo2", "merge"),
		}, `{}`)

	r.start()
	res := r.complete("p", `{"n": 5}`)
	expect(t, "switch control", controlIDs(res.ControlNodes), "s")
	expect(t, "taken branch", ids(res.ExecutableNodes), "hi")
	expect(t, "skipped branch", ids(res.SkippedNodes), "lo", "lo2")

	if got := r.states["s"]; !slices.Equal(got.Branches, []string{"hi"}) {
		t.Fatalf("switch decision not recorded: %+v", got)
	}

	expect(t, "merge after taken branch", ids(r.complete("hi", `{}`).ExecutableNodes), "merge")
}

func TestSwitchWithNoMatchingBranchSkipsEverythingDownstream(t *testing.T) {
	r := newRun(t,
		[]model.Node{sw("s"), task("x"), task("after")},
		[]model.Edge{cond("s", "x", "{{input.go}}"), edge("x", "after")},
		`{"go": false}`)

	res := r.start()
	expect(t, "executable", ids(res.ExecutableNodes))
	expect(t, "skipped", ids(res.SkippedNodes), "after", "x")
}

func TestJoinUsesRecordedSwitchDecision(t *testing.T) {
	// The SWITCH decision must survive a reload: once recorded, a later
	// completion elsewhere in the graph must not re-evaluate the condition.
	r := newRun(t,
		[]model.Node{fork("f"), sw("s"), task("slow"), task("x"), task("j")},
		[]model.Edge{
			edge("f", "s"), edge("f", "slow"),
			cond("s", "x", "{{input.flag}}"),
			edge("x", "j"), edge("slow", "j"),
		}, `{"flag": true}`)

	res := r.start()
	expect(t, "start executable", ids(res.ExecutableNodes), "slow", "x")

	// Flip the input: re-evaluation would now deselect x.
	r.ctx.Input = json.RawMessage(`{"flag": false}`)
	expect(t, "after slow", ids(r.complete("slow", `{}`).ExecutableNodes))
	expect(t, "after x", ids(r.complete("x", `{}`).ExecutableNodes), "j")
}

func TestNodeRetryDelay(t *testing.T) {
	n := model.Node{Retry: &model.RetryPolicy{MaxAttempts: 3, BackoffSeconds: 2}}
	if n.MaxAttempts() != 3 {
		t.Fatalf("max attempts: %d", n.MaxAttempts())
	}
	if d := n.RetryDelay(1).Seconds(); d != 2 {
		t.Fatalf("attempt 1 delay %v", d)
	}
	if d := n.RetryDelay(3).Seconds(); d != 8 {
		t.Fatalf("attempt 3 delay %v", d)
	}
	if (&model.Node{}).MaxAttempts() != 1 {
		t.Fatal("default max attempts should be 1")
	}
}

func TestOutputsAreLoadedOnlyWhenReferenced(t *testing.T) {
	// c reads only a's output; b's large output must never be loaded.
	c := task("c")
	c.Spec = json.RawMessage(`{"url": "{{tasks.a.output.url}}"}`)
	r := newRun(t, []model.Node{task("a"), task("b"), c},
		[]model.Edge{edge("a", "b"), edge("b", "c")}, `{}`)

	r.start()
	r.complete("a", `{"url": "http://x"}`)
	r.loads = nil
	r.complete("b", `{"huge": true}`)

	if len(r.loads) != 1 || !slices.Equal(r.loads[0], []string{"a"}) {
		t.Fatalf("expected exactly one load of [a], got %v", r.loads)
	}
}

func TestTaskRefs(t *testing.T) {
	sw := sw("s")
	sw.Spec = json.RawMessage(`{}`)
	dynamic := task("dyn")
	dynamic.Spec = json.RawMessage(`{"v": "{{tasks[input.key].output}}"}`)
	quoted := task("q")
	quoted.Spec = json.RawMessage(`{"v": "{{tasks[\"my-node\"].output.x + tasks.other.output.y}}", "url": "/api/tasks/1"}`)

	dag := model.BuildDAG(&model.WorkflowDefinitionSpec{
		Nodes: []model.Node{task("p"), sw, dynamic, quoted},
		Edges: []model.Edge{edge("p", "s"), cond("s", "dyn", "{{tasks.p.output.n > 1}}")},
	})

	if refs, ok := dag.TaskRefs("s"); !ok || !slices.Equal(refs, []string{"p", "p"}) {
		t.Fatalf("switch refs: %v %v", refs, ok)
	}
	if _, ok := dag.TaskRefs("dyn"); ok {
		t.Fatal("dynamic task access must fall back to loading everything")
	}
	if refs, ok := dag.TaskRefs("q"); !ok || !slices.Equal(refs, []string{"my-node", "other"}) {
		t.Fatalf("quoted refs: %v %v", refs, ok)
	}
}

func TestWaitNodesParkTheBranch(t *testing.T) {
	r := newRun(t,
		[]model.Node{task("a"), {NodeID: "w", Type: model.NodeTypeWait}, {NodeID: "e", Type: model.NodeTypeWaitForEvent}, task("j")},
		[]model.Edge{edge("a", "w"), edge("a", "e"), edge("w", "j"), edge("e", "j")}, `{}`)

	r.start()
	res := r.complete("a", `{}`)
	expect(t, "waiting", ids(res.WaitingNodes), "e", "w")
	expect(t, "nothing dispatched", ids(res.ExecutableNodes))

	expect(t, "after timer", ids(r.complete("w", `{}`).ExecutableNodes))
	expect(t, "after event", ids(r.complete("e", `{"paid": true}`).ExecutableNodes), "j")
}
