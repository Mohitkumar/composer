package statemachine_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mohitkumar/composer/pkg/model"
	sm "github.com/mohitkumar/composer/pkg/statemachine"
)

func TestRejectedEvents(t *testing.T) {
	h := newHarness(t, []model.Node{task("a")}, nil, `{}`)
	if _, err := h.try(sm.RunStarted{StartNodeID: "nope"}); !errors.Is(err, sm.ErrInvalidRequest) {
		t.Fatalf("unknown start node: %v", err)
	}
	h.start()
	for name, ev := range map[string]sm.Event{
		"signal without name":   sm.SignalReceived{},
		"signal with bad JSON":  sm.SignalReceived{Name: "x", Payload: json.RawMessage(`{bad`)},
		"retry from ghost node": sm.RunRetried{FromNodeID: "ghost"},
	} {
		_, err := h.try(ev)
		if name == "retry from ghost node" {
			if !errors.Is(err, sm.ErrRunActive) { // the run is still active: rejected first
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if !errors.Is(err, sm.ErrInvalidRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	h.complete("a", `{}`)
	if _, err := h.try(sm.RunRetried{FromNodeID: "ghost"}); !errors.Is(err, sm.ErrInvalidRequest) {
		t.Fatalf("retry from an unknown node: %v", err)
	}
	if _, err := h.try(sm.RunCancelled{}); !errors.Is(err, sm.ErrRunFinished) {
		t.Fatalf("cancel a finished run: %v", err)
	}
}

// failingStore fails every load, like a dropped database connection.
type failingStore struct{}

func (failingStore) Outputs([]string) (map[string]json.RawMessage, error) {
	return nil, errors.New("connection reset")
}
func (failingStore) PendingSignal(string) (*sm.Signal, error) { return nil, errors.New("connection reset") }

func TestStoreErrorsAreTransient(t *testing.T) {
	// c reads a's output, which is not cached when z completes: routing must load it.
	c := task("c")
	c.Spec = json.RawMessage(`{"v": "{{tasks.a.output.x}}"}`)
	h := newHarness(t, []model.Node{task("a"), task("z"), c},
		[]model.Edge{edge("a", "z"), edge("z", "c")}, `{}`)
	h.start()
	h.complete("a", `{"x": 1}`)

	z := h.run.Nodes["z"]
	h.run.Context.Tasks = nil // reloaded snapshot
	m := sm.New(h.def, h.run, sm.Deps{Templates: h.tmpl, Store: failingStore{},
		Now: time.Now, NewID: func() string { return "id" }})
	tr, err := m.Apply(sm.NodeCompleted{TaskID: z.TaskID, NodeID: "z", Attempt: z.Attempt})

	var storeErr *sm.StoreError
	if !errors.As(err, &storeErr) || tr != nil {
		t.Fatalf("a store failure must abort with *StoreError and no transition, got %v %v", tr, err)
	}
}

func TestUnresolvableInputFailsTheNode(t *testing.T) {
	b := task("b")
	b.Spec = json.RawMessage(`{"v": "{{tasks.a.output.missing.deeper}}"}`)
	h := newHarness(t, []model.Node{task("a"), b}, []model.Edge{edge("a", "b")}, `{}`)
	h.start()
	tr := h.complete("a", `{}`)
	expect(t, "failed", created(tr, model.TaskInstanceStatusFailed), "b")
	if finished(tr) != model.WorkflowInstanceStatusFailed {
		t.Fatal("with nothing else active, the run fails")
	}
}

func TestWaitSpecs(t *testing.T) {
	h := newHarness(t, []model.Node{wait("until", `{"until": "2026-01-01T13:00:00Z"}`)}, nil, `{}`)
	tr := h.start()
	if c := tr.Actions[0].(sm.CreateTask); c.WakeAt == nil || c.WakeAt.Sub(h.now) != time.Hour {
		t.Fatalf("until: %+v", c)
	}

	for _, spec := range []string{`{}`, `{"until": "tomorrow"}`, `{"duration_seconds": -1}`} {
		h := newHarness(t, []model.Node{wait("w", spec)}, nil, `{}`)
		expect(t, "invalid wait "+spec, created(h.start(), model.TaskInstanceStatusFailed), "w")
	}
	h = newHarness(t, []model.Node{awaitEvent("e", `{"timeout_seconds": 5}`)}, nil, `{}`)
	expect(t, "event wait without event", created(h.start(), model.TaskInstanceStatusFailed), "e")
}

func TestStaleTimerIsIgnored(t *testing.T) {
	h := newHarness(t, []model.Node{wait("w", `{"duration_seconds": 10}`)}, nil, `{}`)
	h.start()
	if tr := h.apply(sm.TimerFired{TaskID: "other", NodeID: "w"}); tr.Ignored == "" {
		t.Fatal("a timer for another task must be ignored")
	}
}

func TestJoinNodeCompletesInline(t *testing.T) {
	h := newHarness(t,
		[]model.Node{task("a"), task("b"), {NodeID: "j", Type: model.NodeTypeJoin}, task("after")},
		[]model.Edge{edge("a", "j"), edge("b", "j"), edge("j", "after")}, `{}`)
	// Two entry points: start from a, b is reached by the start hint below.
	h.apply(sm.RunStarted{StartNodeID: "a"})
	h.apply(sm.RunStarted{StartNodeID: "b"})
	h.complete("a", `{}`)
	tr := h.complete("b", `{}`)
	expect(t, "join resolves inline", created(tr, model.TaskInstanceStatusCompleted), "j")
	expect(t, "after join", dispatched(tr), "after")
}
