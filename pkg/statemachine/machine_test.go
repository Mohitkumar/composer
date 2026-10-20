package statemachine_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/mohitkumar/composer/pkg/model"
	sm "github.com/mohitkumar/composer/pkg/statemachine"
	"github.com/mohitkumar/composer/pkg/template"
)

// harness plays the coordinator's role: it keeps the run snapshot between
// events, persists what actions write (outputs, buffered signals), and builds
// a fresh Machine per event with an empty output cache, so outputs are only
// reachable through the Store — exactly as after a reload from the database.
type harness struct {
	t       *testing.T
	def     *model.WorkflowDAG
	tmpl    *template.Resolver
	run     *sm.Run
	now     time.Time
	ids     int
	outputs map[string]json.RawMessage
	signals []bufferedSignal
	loads   [][]string
}

type bufferedSignal struct {
	sm.Signal
	name     string
	consumed bool
}

func newHarness(t *testing.T, nodes []model.Node, edges []model.Edge, input string) *harness {
	t.Helper()
	tmpl, err := template.NewResolver()
	if err != nil {
		t.Fatal(err)
	}
	return &harness{
		t:       t,
		def:     model.BuildDAG(&model.WorkflowDefinitionSpec{Nodes: nodes, Edges: edges}),
		tmpl:    tmpl,
		run:     sm.NewRun("run-1", "wf", 1, model.NewRunContext(json.RawMessage(input), nil)),
		now:     time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		outputs: map[string]json.RawMessage{},
	}
}

// Store implementation.
func (h *harness) Outputs(ids []string) (map[string]json.RawMessage, error) {
	h.loads = append(h.loads, ids)
	out := map[string]json.RawMessage{}
	for _, id := range ids {
		out[id] = h.outputs[id]
	}
	return out, nil
}

func (h *harness) PendingSignal(name string) (*sm.Signal, error) {
	for i := range h.signals {
		if s := &h.signals[i]; s.name == name && !s.consumed {
			return &s.Signal, nil
		}
	}
	return nil, nil
}

func (h *harness) try(ev sm.Event) (*sm.Transition, error) {
	h.run.Context.Tasks = nil // reloaded snapshot: no outputs cached
	m := sm.New(h.def, h.run, sm.Deps{
		Templates: h.tmpl,
		Store:     h,
		Now:       func() time.Time { return h.now },
		NewID:     func() string { h.ids++; return fmt.Sprintf("t%03d", h.ids) },
	})
	tr, err := m.Apply(ev)
	if err == nil {
		h.persist(tr)
	}
	return tr, err
}

func (h *harness) apply(ev sm.Event) *sm.Transition {
	h.t.Helper()
	tr, err := h.try(ev)
	if err != nil {
		h.t.Fatalf("apply %T: %v", ev, err)
	}
	return tr
}

func (h *harness) persist(tr *sm.Transition) {
	for _, a := range tr.Actions {
		switch a := a.(type) {
		case sm.CreateTask:
			if a.Output != nil {
				h.outputs[a.Node.NodeID] = a.Output
			}
		case sm.CompleteTask:
			h.outputs[a.NodeID] = a.Output
		case sm.ResolveWait:
			h.outputs[a.NodeID] = a.Output
		case sm.BufferSignal:
			h.signals = append(h.signals, bufferedSignal{Signal: sm.Signal{ID: fmt.Sprint(len(h.signals)), Payload: a.Payload}, name: a.Name})
		case sm.ConsumeSignal:
			for i := range h.signals {
				if h.signals[i].ID == a.SignalID {
					h.signals[i].consumed = true
				}
			}
		}
	}
}

func (h *harness) start() *sm.Transition { return h.apply(sm.RunStarted{}) }

func (h *harness) complete(nodeID, output string) *sm.Transition {
	h.t.Helper()
	n := h.run.Nodes[nodeID]
	return h.apply(sm.NodeCompleted{TaskID: n.TaskID, NodeID: nodeID, Attempt: n.Attempt, Output: json.RawMessage(output)})
}

func (h *harness) fail(nodeID, reason string) *sm.Transition {
	h.t.Helper()
	n := h.run.Nodes[nodeID]
	return h.apply(sm.NodeFailed{TaskID: n.TaskID, NodeID: nodeID, Attempt: n.Attempt, Error: reason})
}

func (h *harness) state(nodeID string) model.TaskInstanceStatus {
	if n := h.run.Nodes[nodeID]; n != nil {
		return n.Status
	}
	return ""
}

// ── Transition inspection ────────────────────────────────────────────────────

func dispatched(tr *sm.Transition) []string {
	var out []string
	for _, a := range tr.Actions {
		if d, ok := a.(sm.DispatchTask); ok {
			out = append(out, d.Node.NodeID)
		}
	}
	sort.Strings(out)
	return out
}

func created(tr *sm.Transition, status model.TaskInstanceStatus) []string {
	var out []string
	for _, a := range tr.Actions {
		if c, ok := a.(sm.CreateTask); ok && c.Status == status {
			out = append(out, c.Node.NodeID)
		}
	}
	sort.Strings(out)
	return out
}

func finished(tr *sm.Transition) model.WorkflowInstanceStatus {
	for _, a := range tr.Actions {
		if f, ok := a.(sm.FinishRun); ok {
			return f.Status
		}
	}
	return ""
}

func expect(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

func task(id string) model.Node { return model.Node{NodeID: id, Type: model.NodeTypeHTTP} }
func fork(id string) model.Node { return model.Node{NodeID: id, Type: model.NodeTypeFork} }
func sw(id string) model.Node   { return model.Node{NodeID: id, Type: model.NodeTypeSwitch} }
func wait(id, spec string) model.Node {
	return model.Node{NodeID: id, Type: model.NodeTypeWait, Spec: json.RawMessage(spec)}
}
func awaitEvent(id, spec string) model.Node {
	return model.Node{NodeID: id, Type: model.NodeTypeWaitForEvent, Spec: json.RawMessage(spec)}
}
func edge(from, to string) model.Edge { return model.Edge{From: from, To: to} }
func cond(from, to, c string) model.Edge {
	return model.Edge{From: from, To: to, Condition: c}
}

// ── Routing ──────────────────────────────────────────────────────────────────

func TestLinearChainRunsInOrderAndSucceeds(t *testing.T) {
	h := newHarness(t, []model.Node{task("a"), task("b"), task("c")},
		[]model.Edge{edge("a", "b"), edge("b", "c")}, `{}`)

	expect(t, "start", dispatched(h.start()), "a")
	expect(t, "after a", dispatched(h.complete("a", `{}`)), "b")
	expect(t, "after b", dispatched(h.complete("b", `{}`)), "c")
	if got := finished(h.complete("c", `{}`)); got != model.WorkflowInstanceStatusSucceeded {
		t.Fatalf("run should succeed after the last node, got %q", got)
	}
}

func TestForkJoinWaitsForMultiNodeBranches(t *testing.T) {
	// fork → a1 → a2 ─┐
	//      → b1 ──────┴→ join
	h := newHarness(t,
		[]model.Node{fork("f"), task("a1"), task("a2"), task("b1"), task("join")},
		[]model.Edge{edge("f", "a1"), edge("a1", "a2"), edge("f", "b1"), edge("a2", "join"), edge("b1", "join")},
		`{}`)

	tr := h.start()
	expect(t, "fork completes inline", created(tr, model.TaskInstanceStatusCompleted), "f")
	expect(t, "branches", dispatched(tr), "a1", "b1")
	expect(t, "after a1", dispatched(h.complete("a1", `{}`)), "a2")
	expect(t, "after b1 (join waits for a2)", dispatched(h.complete("b1", `{}`)))
	expect(t, "after a2", dispatched(h.complete("a2", `{}`)), "join")
}

func TestDiamondWaitsForAllPredecessors(t *testing.T) {
	h := newHarness(t, []model.Node{task("a"), task("b"), task("c"), task("d")},
		[]model.Edge{edge("a", "b"), edge("a", "c"), edge("b", "d"), edge("c", "d")}, `{}`)

	h.start()
	expect(t, "fan-out", dispatched(h.complete("a", `{}`)), "b", "c")
	expect(t, "after b", dispatched(h.complete("b", `{}`)))
	expect(t, "after c", dispatched(h.complete("c", `{}`)), "d")
}

func TestSwitchSkipsUntakenBranchAndReconverges(t *testing.T) {
	// p → s ─(n > 1)→ hi ─────────┐
	//       ─(n <= 1)→ lo → lo2 ──┴→ merge
	h := newHarness(t,
		[]model.Node{task("p"), sw("s"), task("hi"), task("lo"), task("lo2"), task("merge")},
		[]model.Edge{
			edge("p", "s"),
			cond("s", "hi", "{{tasks.p.output.n > 1}}"),
			cond("s", "lo", "{{tasks.p.output.n <= 1}}"),
			edge("lo", "lo2"), edge("hi", "merge"), edge("lo2", "merge"),
		}, `{}`)

	h.start()
	tr := h.complete("p", `{"n": 5}`)
	expect(t, "switch completes inline", created(tr, model.TaskInstanceStatusCompleted), "s")
	expect(t, "taken branch", dispatched(tr), "hi")
	expect(t, "skipped branch", created(tr, model.TaskInstanceStatusSkipped), "lo", "lo2")
	expect(t, "recorded decision", h.run.Nodes["s"].Branches, "hi")
	expect(t, "merge after taken branch", dispatched(h.complete("hi", `{}`)), "merge")
}

func TestSwitchWithNoMatchSkipsEverythingAndSucceeds(t *testing.T) {
	h := newHarness(t, []model.Node{sw("s"), task("x"), task("after")},
		[]model.Edge{cond("s", "x", "{{input.go}}"), edge("x", "after")}, `{"go": false}`)

	tr := h.start()
	expect(t, "skipped", created(tr, model.TaskInstanceStatusSkipped), "after", "x")
	if finished(tr) != model.WorkflowInstanceStatusSucceeded {
		t.Fatal("a run with nothing left to do succeeds")
	}
}

func TestJoinUsesRecordedSwitchDecision(t *testing.T) {
	h := newHarness(t,
		[]model.Node{fork("f"), sw("s"), task("slow"), task("x"), task("j")},
		[]model.Edge{edge("f", "s"), edge("f", "slow"), cond("s", "x", "{{input.flag}}"), edge("x", "j"), edge("slow", "j")},
		`{"flag": true}`)

	expect(t, "start", dispatched(h.start()), "slow", "x")
	h.run.Context.Input = json.RawMessage(`{"flag": false}`) // re-evaluation would now deselect x
	expect(t, "after slow", dispatched(h.complete("slow", `{}`)))
	expect(t, "after x", dispatched(h.complete("x", `{}`)), "j")
}

func TestBadSwitchConditionFailsTheRun(t *testing.T) {
	h := newHarness(t, []model.Node{task("a"), sw("s"), task("b")},
		[]model.Edge{edge("a", "s"), cond("s", "b", "{{tasks.a.output.missing.deeper > 1}}")}, `{}`)
	h.start()
	if got := finished(h.complete("a", `{}`)); got != model.WorkflowInstanceStatusFailed {
		t.Fatalf("routing failure must fail the run, got %q", got)
	}
}

func TestOutputsAreLoadedOnlyWhenReferenced(t *testing.T) {
	c := task("c")
	c.Spec = json.RawMessage(`{"url": "{{tasks.a.output.url}}"}`)
	h := newHarness(t, []model.Node{task("a"), task("b"), c},
		[]model.Edge{edge("a", "b"), edge("b", "c")}, `{}`)

	h.start()
	h.complete("a", `{"url": "http://x"}`)
	h.loads = nil
	tr := h.complete("b", `{"huge": true}`)

	if len(h.loads) != 1 || !slices.Equal(h.loads[0], []string{"a"}) {
		t.Fatalf("expected exactly one load of [a], got %v", h.loads)
	}
	for _, a := range tr.Actions {
		if d, ok := a.(sm.DispatchTask); ok && string(d.Input) != `{"url":"http://x"}` {
			t.Fatalf("c's input not resolved from a: %s", d.Input)
		}
	}
}

// ── Task results ─────────────────────────────────────────────────────────────

func TestStaleAndDuplicateReportsAreIgnored(t *testing.T) {
	h := newHarness(t, []model.Node{task("a"), task("b")}, []model.Edge{edge("a", "b")}, `{}`)
	h.start()
	taskID := h.run.Nodes["a"].TaskID

	if tr := h.apply(sm.NodeCompleted{TaskID: taskID, NodeID: "a", Attempt: 3}); tr.Ignored == "" {
		t.Fatal("a report for another attempt must be ignored")
	}
	if tr := h.apply(sm.NodeCompleted{TaskID: "other", NodeID: "a"}); tr.Ignored == "" {
		t.Fatal("a report for another task must be ignored")
	}
	h.complete("a", `{}`)
	if tr := h.apply(sm.NodeCompleted{TaskID: taskID, NodeID: "a"}); tr.Ignored == "" || len(tr.Actions) != 0 {
		t.Fatal("a duplicate completion must change nothing")
	}
}

func TestRetryPolicy(t *testing.T) {
	flaky := task("flaky")
	flaky.Retry = &model.RetryPolicy{MaxAttempts: 3, BackoffSeconds: 2}
	h := newHarness(t, []model.Node{flaky}, nil, `{}`)
	h.start()

	tr := h.fail("flaky", "boom")
	retry, ok := tr.Actions[0].(sm.RetryTask)
	if !ok || retry.NotBefore.Sub(h.now) != 2*time.Second {
		t.Fatalf("first failure should retry after 2s, got %#v", tr.Actions)
	}
	if h.state("flaky") != model.TaskInstanceStatusPending || h.run.Nodes["flaky"].Attempt != 1 {
		t.Fatal("a retried task is PENDING with the next attempt")
	}
	tr = h.fail("flaky", "boom")
	if retry := tr.Actions[0].(sm.RetryTask); retry.NotBefore.Sub(h.now) != 4*time.Second {
		t.Fatalf("second retry should back off 4s, got %v", retry.NotBefore.Sub(h.now))
	}
	if got := finished(h.fail("flaky", "boom")); got != model.WorkflowInstanceStatusFailed {
		t.Fatalf("exhausted retries must fail the run, got %q", got)
	}
}

// ── Waits and signals ────────────────────────────────────────────────────────

func TestWaitParksUntilTimerFires(t *testing.T) {
	h := newHarness(t, []model.Node{wait("w", `{"duration_seconds": "{{input.delay}}"}`), task("after")},
		[]model.Edge{edge("w", "after")}, `{"delay": 30}`)

	tr := h.start()
	parked := tr.Actions[0].(sm.CreateTask)
	if parked.Status != model.TaskInstanceStatusWaiting || parked.WakeAt.Sub(h.now) != 30*time.Second {
		t.Fatalf("wait should park for 30s, got %#v", parked)
	}
	expect(t, "dispatched while waiting", dispatched(tr))

	tr = h.apply(sm.TimerFired{TaskID: h.run.Nodes["w"].TaskID, NodeID: "w"})
	expect(t, "after timer", dispatched(tr), "after")
}

func TestZeroWaitCompletesImmediately(t *testing.T) {
	h := newHarness(t, []model.Node{wait("w", `{"duration_seconds": 0}`), task("after")},
		[]model.Edge{edge("w", "after")}, `{}`)
	expect(t, "start", dispatched(h.start()), "after")
}

func TestSignalDeliveredToWaitingNode(t *testing.T) {
	next := task("ship")
	next.Spec = json.RawMessage(`{"by": "{{tasks.approval.output.by}}"}`)
	h := newHarness(t, []model.Node{awaitEvent("approval", `{"event": "approved-{{input.order}}"}`), next},
		[]model.Edge{edge("approval", "ship")}, `{"order": 7}`)

	h.start()
	if h.run.Nodes["approval"].AwaitedSignal != "approved-7" {
		t.Fatalf("templated event name: %+v", h.run.Nodes["approval"])
	}
	tr := h.apply(sm.SignalReceived{Name: "approved-7", Payload: json.RawMessage(`{"by": "alice"}`)})
	if !tr.SignalDelivered {
		t.Fatal("the waiting node must consume the signal")
	}
	expect(t, "after signal", dispatched(tr), "ship")
}

func TestEarlySignalIsBufferedAndConsumed(t *testing.T) {
	h := newHarness(t, []model.Node{task("a"), awaitEvent("approval", `{"event": "go"}`), task("b")},
		[]model.Edge{edge("a", "approval"), edge("approval", "b")}, `{}`)
	h.start()

	if tr := h.apply(sm.SignalReceived{Name: "go", Payload: json.RawMessage(`{"early": true}`)}); tr.SignalDelivered {
		t.Fatal("no node waits yet: the signal must be buffered")
	}
	expect(t, "wait resolved by the buffered signal", dispatched(h.complete("a", `{}`)), "b")
	if string(h.outputs["approval"]) != `{"early": true}` || !h.signals[0].consumed {
		t.Fatal("the buffered payload becomes the output and the signal is consumed")
	}
}

func TestEventWaitTimesOut(t *testing.T) {
	h := newHarness(t, []model.Node{awaitEvent("approval", `{"event": "never", "timeout_seconds": 60}`)}, nil, `{}`)
	h.start()
	tr := h.apply(sm.TimerFired{TaskID: h.run.Nodes["approval"].TaskID, NodeID: "approval"})
	if h.state("approval") != model.TaskInstanceStatusFailed || finished(tr) != model.WorkflowInstanceStatusFailed {
		t.Fatal("an event wait that times out fails the node and the run")
	}
	if _, err := h.try(sm.SignalReceived{Name: "never"}); !errors.Is(err, sm.ErrRunFinished) {
		t.Fatalf("signal to a finished run: %v", err)
	}
}

// ── Run commands ─────────────────────────────────────────────────────────────

func TestCancelFailsActiveTasksAndTheRun(t *testing.T) {
	h := newHarness(t, []model.Node{fork("f"), task("a"), wait("w", `{"duration_seconds": 3600}`)},
		[]model.Edge{edge("f", "a"), edge("f", "w")}, `{}`)
	h.start()

	tr := h.apply(sm.RunCancelled{Reason: "user"})
	if h.state("a") != model.TaskInstanceStatusFailed || h.state("w") != model.TaskInstanceStatusFailed {
		t.Fatal("cancel fails running and waiting nodes")
	}
	if finished(tr) != model.WorkflowInstanceStatusFailed {
		t.Fatal("cancel finishes the run as FAILED")
	}
	if tr := h.complete("a", `{}`); tr.Ignored == "" {
		t.Fatal("a late result after cancel is ignored")
	}
}

func TestRetryRunRestartsFromNode(t *testing.T) {
	h := newHarness(t, []model.Node{task("a"), task("b"), task("c")},
		[]model.Edge{edge("a", "b"), edge("b", "c")}, `{}`)
	h.start()

	if _, err := h.try(sm.RunRetried{FromNodeID: "b", Version: 1}); !errors.Is(err, sm.ErrRunActive) {
		t.Fatalf("retrying a run with active tasks: %v", err)
	}
	h.complete("a", `{}`)
	h.fail("b", "boom")
	if h.run.Status != model.WorkflowInstanceStatusFailed {
		t.Fatal("run should have failed")
	}

	tr := h.apply(sm.RunRetried{FromNodeID: "b", Version: 2, Input: json.RawMessage(`{"fixed": true}`)})
	expect(t, "re-dispatched", dispatched(tr), "b")
	if h.run.Status != model.WorkflowInstanceStatusRunning || h.run.WorkflowVersion != 2 ||
		h.state("a") != model.TaskInstanceStatusCompleted {
		t.Fatalf("retry reopens the run on the new version and keeps upstream results: %+v", h.run)
	}
}
