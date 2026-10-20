package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mohitkumar/composer/pkg/api"
	grpcapi "github.com/mohitkumar/composer/pkg/api/grpc"
	"github.com/mohitkumar/composer/pkg/coord"
	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/redisq"
	redisstream "github.com/mohitkumar/composer/pkg/stream/redis"
	"github.com/mohitkumar/composer/worker"
	"github.com/rs/zerolog"
)

// engine is a coordinator, its gRPC API, and a stub HTTP service for HTTP
// nodes, all backed by real Postgres and Redis containers.
type engine struct {
	t        *testing.T
	ctx      context.Context
	infra    *TestInfra
	coord    *coord.Coordinator
	grpcAddr string
	svc      *stubService
}

func newEngine(t *testing.T) *engine {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping container-backed test in short mode")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	infra := SetupTestInfra(ctx, t)
	t.Cleanup(func() { infra.Cleanup(context.Background(), t) })

	// Log to stderr, not t: background loops may outlive an individual test.
	logger := zerolog.New(os.Stderr).Level(zerolog.WarnLevel)
	c, err := coord.NewCoordinator(&coord.Config{
		DBConfig:     db.Config{URL: infra.DBURL},
		RedisConfig:  redisq.RedisConfig{Addrs: []string{infra.RedisAddr}},
		OutboxConfig: coord.OutboxConfig{PollInterval: 20 * time.Millisecond, Concurrency: 2},
	}, &logger)
	if err != nil {
		t.Fatalf("create coordinator: %v", err)
	}
	if err := c.Run(ctx); err != nil {
		t.Fatalf("run coordinator: %v", err)
	}
	// Cleanups run last-in first-out: stop the coordinator before the containers.
	t.Cleanup(func() { cancel(); _ = c.Shutdown() })

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpcapi.NewServer(&api.APIConfig{}, c)
	go srv.GetServer().Serve(lis) //nolint:errcheck
	t.Cleanup(srv.Stop)

	svc := newStubService()
	t.Cleanup(svc.Close)

	return &engine{t: t, ctx: ctx, infra: infra, coord: c, grpcAddr: lis.Addr().String(), svc: svc}
}

// startWorker runs a worker until the test ends and returns its stop function.
func (e *engine) startWorker(name string, concurrency int, leaseTTL int64) context.CancelFunc {
	e.t.Helper()
	logger := zerolog.New(os.Stderr).Level(zerolog.WarnLevel)
	w, err := worker.NewWorker(worker.WorkerConfig{
		Stream: redisstream.NewConsumer(redisstream.ConsumerConfig{
			StreamName:    "workflow.tasks",
			ConsumerGroup: "engine-test",
			ConsumerName:  name,
			BlockDuration: 200 * time.Millisecond,
		}, e.infra.RedisStream, logger),
		CoordinatorAddr: e.grpcAddr,
		ConsumerName:    name,
		LeaseTTLSeconds: leaseTTL,
		Concurrency:     concurrency,
		Logger:          logger,
	})
	if err != nil {
		e.t.Fatalf("create worker: %v", err)
	}
	if err := w.EnsureConsumerGroup(e.ctx); err != nil {
		e.t.Fatalf("consumer group: %v", err)
	}
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	e.t.Cleanup(func() { cancel(); <-done; w.Close() })
	return cancel
}

// run registers spec as a new workflow and starts one run of it.
func (e *engine) run(spec model.WorkflowDefinitionSpec, input string) string {
	e.t.Helper()
	specJSON, _ := json.Marshal(spec)
	id := strings.ReplaceAll(e.t.Name(), "/", "_")
	if err := e.coord.RegisterWorkflow(e.ctx, &model.RegisterWorkflowRequest{
		WorkflowID: id, Name: id, Version: 1, Spec: specJSON, CreatedBy: "admin",
	}); err != nil {
		e.t.Fatalf("register workflow: %v", err)
	}
	req := &model.StartWorkflowRequest{WorkflowID: id, WorkflowVersion: 1, Input: json.RawMessage(input)}
	if err := e.coord.StartWorkflow(e.ctx, req); err != nil {
		e.t.Fatalf("start workflow: %v", err)
	}
	return req.RunID
}

// waitForRun waits until the run reaches a terminal status and returns it.
func (e *engine) waitForRun(runID string, timeout time.Duration) model.WorkflowInstanceStatus {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var status model.WorkflowInstanceStatus
		if err := e.infra.DBPool.QueryRow(e.ctx,
			`SELECT status FROM workflow_runs WHERE id = $1`, runID).Scan(&status); err != nil {
			e.t.Fatal(err)
		}
		if status.IsTerminal() {
			return status
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatalf("run %s did not finish within %v; tasks: %v", runID, timeout, e.nodeStates(runID))
	return ""
}

func (e *engine) nodeStates(runID string) map[string]string {
	rows, err := e.infra.DBPool.Query(e.ctx,
		`SELECT node_id, state || '#' || attempt FROM task_instances WHERE run_id = $1`, runID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var node, state string
		if err := rows.Scan(&node, &state); err != nil {
			e.t.Fatal(err)
		}
		out[node] = state
	}
	return out
}

func (e *engine) expectStates(runID string, want map[string]string) {
	e.t.Helper()
	got := e.nodeStates(runID)
	for node, state := range want {
		if got[node] != state {
			e.t.Errorf("node %s: got %q, want %q (all: %v)", node, got[node], state, got)
		}
	}
}

// ── Stub HTTP service ────────────────────────────────────────────────────────

// stubService answers /ok/<node> with {"node": ..., "n": 5}, and /flaky/<node>/<k>
// with a 500 for the first k calls. It counts calls per path.
type stubService struct {
	*httptest.Server
	mu    sync.Mutex
	calls map[string]int
}

func newStubService() *stubService {
	s := &stubService{calls: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls[r.URL.Path]++
		n := s.calls[r.URL.Path]
		s.mu.Unlock()

		// /flaky/<name>/<k>
		if parts := strings.Split(r.URL.Path, "/"); len(parts) == 4 && parts[1] == "flaky" && n <= atoi(parts[3]) {
			http.Error(w, "flaky", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"node": %q, "n": 5}`, r.URL.Path)
	}))
	return s
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func (s *stubService) callCount(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[path]
}

func (e *engine) httpNode(id string) model.Node {
	return model.Node{NodeID: id, Type: model.NodeTypeHTTP,
		Spec: json.RawMessage(fmt.Sprintf(`{"url": %q, "method": "GET"}`, e.svc.URL+"/ok/"+id))}
}

// ── Tests ────────────────────────────────────────────────────────────────────

// One engine is shared by all subtests: containers are the slow part.
func TestEngine(t *testing.T) {
	e := newEngine(t)
	e.startWorker("w1", 8, 10)
	e.startWorker("w2", 8, 10)

	t.Run("fork join waits for multi-node branches", func(t *testing.T) {
		e.t = t
		// fork → a1 → a2 ─┐
		//      → b1 ──────┴→ join → end
		spec := model.WorkflowDefinitionSpec{
			Nodes: []model.Node{
				{NodeID: "fork", Type: model.NodeTypeFork},
				e.httpNode("a1"), e.httpNode("a2"), e.httpNode("b1"),
				e.httpNode("join"), e.httpNode("end"),
			},
			Edges: []model.Edge{
				{From: "fork", To: "a1"}, {From: "a1", To: "a2"}, {From: "fork", To: "b1"},
				{From: "a2", To: "join"}, {From: "b1", To: "join"}, {From: "join", To: "end"},
			},
		}
		runID := e.run(spec, `{}`)
		if got := e.waitForRun(runID, 30*time.Second); got != model.WorkflowInstanceStatusSucceeded {
			t.Fatalf("status %s, tasks %v", got, e.nodeStates(runID))
		}
		var joinSawA2 bool
		err := e.infra.DBPool.QueryRow(e.ctx, `
			SELECT j.started_at >= a2.completed_at
			  FROM task_instances j JOIN task_instances a2 ON a2.run_id = j.run_id AND a2.node_id = 'a2'
			 WHERE j.run_id = $1 AND j.node_id = 'join'`, runID).Scan(&joinSawA2)
		if err != nil || !joinSawA2 {
			t.Fatalf("join must start after a2 completed (ok=%v, err=%v)", joinSawA2, err)
		}
	})

	t.Run("switch skips untaken branch and reconverges", func(t *testing.T) {
		e.t = t
		spec := model.WorkflowDefinitionSpec{
			Nodes: []model.Node{
				e.httpNode("p"), {NodeID: "s", Type: model.NodeTypeSwitch},
				e.httpNode("hi"), e.httpNode("lo"), e.httpNode("lo2"), e.httpNode("merge"),
			},
			Edges: []model.Edge{
				{From: "p", To: "s"},
				{From: "s", To: "hi", Condition: "{{tasks.p.output.n > 1}}"},
				{From: "s", To: "lo", Condition: "{{tasks.p.output.n <= 1}}"},
				{From: "lo", To: "lo2"}, {From: "hi", To: "merge"}, {From: "lo2", To: "merge"},
			},
		}
		runID := e.run(spec, `{}`)
		if got := e.waitForRun(runID, 30*time.Second); got != model.WorkflowInstanceStatusSucceeded {
			t.Fatalf("status %s, tasks %v", got, e.nodeStates(runID))
		}
		e.expectStates(runID, map[string]string{
			"s": "COMPLETED#0", "hi": "COMPLETED#0", "merge": "COMPLETED#0",
			"lo": "SKIPPED#0", "lo2": "SKIPPED#0",
		})
	})

	t.Run("wide fan-in runs the join exactly once", func(t *testing.T) {
		e.t = t
		spec := model.WorkflowDefinitionSpec{Nodes: []model.Node{{NodeID: "fork", Type: model.NodeTypeFork}, e.httpNode("join")}}
		for i := range 30 {
			id := fmt.Sprintf("b%02d", i)
			spec.Nodes = append(spec.Nodes, e.httpNode(id))
			spec.Edges = append(spec.Edges, model.Edge{From: "fork", To: id}, model.Edge{From: id, To: "join"})
		}
		before := e.svc.callCount("/ok/join")
		runID := e.run(spec, `{}`)
		if got := e.waitForRun(runID, 60*time.Second); got != model.WorkflowInstanceStatusSucceeded {
			t.Fatalf("status %s, tasks %v", got, e.nodeStates(runID))
		}
		if calls := e.svc.callCount("/ok/join") - before; calls != 1 {
			t.Fatalf("join executed %d times, want 1", calls)
		}
	})

	t.Run("retry policy re-runs a failing node", func(t *testing.T) {
		e.t = t
		flaky := model.Node{NodeID: "flaky", Type: model.NodeTypeHTTP,
			Retry: &model.RetryPolicy{MaxAttempts: 3, BackoffSeconds: 1},
			Spec:  json.RawMessage(fmt.Sprintf(`{"url": %q, "method": "GET"}`, e.svc.URL+"/flaky/retry/2"))}
		runID := e.run(model.WorkflowDefinitionSpec{Nodes: []model.Node{flaky}}, `{}`)
		if got := e.waitForRun(runID, 30*time.Second); got != model.WorkflowInstanceStatusSucceeded {
			t.Fatalf("status %s, tasks %v", got, e.nodeStates(runID))
		}
		e.expectStates(runID, map[string]string{"flaky": "COMPLETED#2"})
	})

	t.Run("exhausted retries fail the run", func(t *testing.T) {
		e.t = t
		flaky := model.Node{NodeID: "flaky", Type: model.NodeTypeHTTP,
			Retry: &model.RetryPolicy{MaxAttempts: 2},
			Spec:  json.RawMessage(fmt.Sprintf(`{"url": %q, "method": "GET"}`, e.svc.URL+"/flaky/exhaust/99"))}
		runID := e.run(model.WorkflowDefinitionSpec{Nodes: []model.Node{flaky, e.httpNode("never")},
			Edges: []model.Edge{{From: "flaky", To: "never"}}}, `{}`)
		if got := e.waitForRun(runID, 30*time.Second); got != model.WorkflowInstanceStatusFailed {
			t.Fatalf("status %s, tasks %v", got, e.nodeStates(runID))
		}
		e.expectStates(runID, map[string]string{"flaky": "FAILED#1"})
	})

	t.Run("stale attempt report is ignored", func(t *testing.T) {
		e.t = t
		slow := model.Node{NodeID: "only", Type: model.NodeTypeHTTP,
			Spec: json.RawMessage(fmt.Sprintf(`{"url": %q, "method": "GET"}`, e.svc.URL+"/ok/stale"))}
		runID := e.run(model.WorkflowDefinitionSpec{Nodes: []model.Node{slow}}, `{}`)
		e.waitForRun(runID, 30*time.Second)

		var taskID string
		_ = e.infra.DBPool.QueryRow(e.ctx, `SELECT id FROM task_instances WHERE run_id = $1`, runID).Scan(&taskID)
		err := e.coord.HandleNodeFailed(e.ctx, coord.TaskResult{
			TaskID: taskID, RunID: runID, NodeID: "only", Attempt: 7, Error: "late report"})
		if err != nil {
			t.Fatal(err)
		}
		e.expectStates(runID, map[string]string{"only": "COMPLETED#0"})
	})
}

// A worker that dies holding a lease must not strand its task: the sweeper
// re-dispatches it once the lease expires, and it executes exactly once more.
func TestExpiredLeaseIsRecovered(t *testing.T) {
	e := newEngine(t)
	runID := e.run(model.WorkflowDefinitionSpec{Nodes: []model.Node{e.httpNode("orphan")}}, `{}`)

	// Simulate a worker that leased the task and then died.
	var taskID string
	if err := e.infra.DBPool.QueryRow(e.ctx,
		`SELECT id FROM task_instances WHERE run_id = $1`, runID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.coord.LeaseManager.Acquire(e.ctx, taskID, "dead-worker", time.Second); err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}
	if ok, _ := e.coord.LeaseManager.Acquire(e.ctx, taskID, "dead-worker", time.Second); ok {
		t.Fatal("an owner must not re-acquire a lease it still holds")
	}

	time.Sleep(1500 * time.Millisecond)
	e.coord.RecoverTasks(e.ctx)
	e.startWorker("survivor", 2, 10)

	if got := e.waitForRun(runID, 30*time.Second); got != model.WorkflowInstanceStatusSucceeded {
		t.Fatalf("status %s, tasks %v", got, e.nodeStates(runID))
	}
	if calls := e.svc.callCount("/ok/orphan"); calls != 1 {
		t.Fatalf("orphaned task executed %d times, want 1", calls)
	}
}
