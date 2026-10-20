package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	workflowpb "github.com/mohitkumar/composer/pkg/api/proto/gen/go/workflow"
	"github.com/mohitkumar/composer/pkg/stream"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
)

// ── Fakes ────────────────────────────────────────────────────────────────────

// memStream is an in-memory stream.Consumer.
type memStream struct {
	mu      sync.Mutex
	pending []stream.Message
	acked   []string
}

func (s *memStream) push(values map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, stream.Message{ID: fmt.Sprint(len(s.pending) + len(s.acked)), Values: values})
}

func (s *memStream) ReadNew(ctx context.Context, max int64) ([]stream.Message, error) {
	s.mu.Lock()
	n := min(int(max), len(s.pending))
	msgs := s.pending[:n]
	s.pending = s.pending[n:]
	s.mu.Unlock()
	if n == 0 {
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
		}
	}
	return msgs, nil
}

func (s *memStream) ReclaimOrphaned(context.Context, time.Duration) ([]stream.Message, error) {
	return nil, nil
}

func (s *memStream) Ack(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acked = append(s.acked, id)
	return nil
}

func (s *memStream) ackedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.acked...)
}

func (s *memStream) Setup(context.Context) error { return nil }
func (s *memStream) Close() error                { return nil }

// fakeCoordinator records lease and report calls.
type fakeCoordinator struct {
	workflowpb.UnimplementedWorkflowServiceServer
	mu         sync.Mutex
	grantLease bool
	failReport bool
	leases     []*workflowpb.AcquireTaskLeaseRequest
	reports    []*workflowpb.ReportTaskResultRequest
}

func (f *fakeCoordinator) AcquireTaskLease(_ context.Context, req *workflowpb.AcquireTaskLeaseRequest) (*workflowpb.AcquireTaskLeaseResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leases = append(f.leases, req)
	return &workflowpb.AcquireTaskLeaseResponse{Acquired: f.grantLease}, nil
}

func (f *fakeCoordinator) RenewTaskLease(context.Context, *workflowpb.AcquireTaskLeaseRequest) (*workflowpb.AcquireTaskLeaseResponse, error) {
	return &workflowpb.AcquireTaskLeaseResponse{Acquired: true}, nil
}

func (f *fakeCoordinator) ReportTaskResult(_ context.Context, req *workflowpb.ReportTaskResultRequest) (*workflowpb.ReportTaskResultResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failReport {
		return nil, errors.New("coordinator unavailable")
	}
	f.reports = append(f.reports, req)
	return &workflowpb.ReportTaskResultResponse{Acknowledged: true}, nil
}

func (f *fakeCoordinator) snapshot() (leases int, reports []*workflowpb.ReportTaskResultRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.leases), append([]*workflowpb.ReportTaskResultRequest(nil), f.reports...)
}

// ── Harness ──────────────────────────────────────────────────────────────────

type fixture struct {
	stream *memStream
	coord  *fakeCoordinator
	http   *httptest.Server
}

func startWorker(t *testing.T, coord *fakeCoordinator) *fixture {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	workflowpb.RegisterWorkflowServiceServer(srv, coord)
	go srv.Serve(lis) //nolint:errcheck
	t.Cleanup(srv.Stop)

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"ok": true}`)
	}))
	t.Cleanup(api.Close)

	st := &memStream{}
	w, err := NewWorker(WorkerConfig{
		Streams:         []stream.Consumer{st},
		CoordinatorAddr: lis.Addr().String(),
		ConsumerName:    "test-worker",
		LeaseTTLSeconds: 30,
		Concurrency:     2,
		Logger:          zerolog.Nop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; w.Close() })
	return &fixture{stream: st, coord: coord, http: api}
}

func (f *fixture) task(id, path string) map[string]any {
	input, _ := json.Marshal(map[string]string{"url": f.http.URL + path, "method": "GET"})
	return map[string]any{"task_id": id, "run_id": "run-1", "node_id": "n-" + id,
		"node_type": "HTTP", "attempt": "2", "input": string(input)}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

// ── Tests ────────────────────────────────────────────────────────────────────

func TestWorkerLeasesExecutesReportsAndAcks(t *testing.T) {
	f := startWorker(t, &fakeCoordinator{grantLease: true})
	f.stream.push(f.task("t1", "/ok"))
	f.stream.push(f.task("t2", "/fail"))

	eventually(t, "both messages acked", func() bool { return len(f.stream.ackedIDs()) == 2 })
	_, reports := f.coord.snapshot()
	byTask := map[string]*workflowpb.ReportTaskResultRequest{}
	for _, r := range reports {
		byTask[r.TaskId] = r
	}
	if ok := byTask["t1"]; ok == nil || ok.Status != "completed" || string(ok.Output) != `{"ok": true}` ||
		ok.RunId != "run-1" || ok.Attempt != 2 {
		t.Fatalf("success report: %+v", ok)
	}
	if bad := byTask["t2"]; bad == nil || bad.Status != "failed" || bad.Error == "" {
		t.Fatalf("failure report: %+v", bad)
	}
}

func TestWorkerDropsMessagesItCannotLease(t *testing.T) {
	f := startWorker(t, &fakeCoordinator{grantLease: false})
	f.stream.push(f.task("t1", "/ok"))

	eventually(t, "message acked", func() bool { return len(f.stream.ackedIDs()) == 1 })
	if leases, reports := f.coord.snapshot(); leases != 1 || len(reports) != 0 {
		t.Fatalf("a task leased elsewhere must not run: leases=%d reports=%d", leases, len(reports))
	}
}

func TestWorkerKeepsMessageWhenReportFails(t *testing.T) {
	f := startWorker(t, &fakeCoordinator{grantLease: true, failReport: true})
	f.stream.push(f.task("t1", "/ok"))

	eventually(t, "lease attempted", func() bool { leases, _ := f.coord.snapshot(); return leases == 1 })
	time.Sleep(300 * time.Millisecond)
	if acked := f.stream.ackedIDs(); len(acked) != 0 {
		t.Fatalf("an unreported result must stay pending for redelivery, acked %v", acked)
	}
}

func TestParseTaskMessage(t *testing.T) {
	msg, err := parseTaskMessage(map[string]any{
		"task_id": "t", "run_id": "r", "node_id": "n", "workflow_id": "w",
		"workflow_version": "3", "attempt": "1", "input": `{"a":1}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if msg.TaskID != "t" || msg.WorkflowVersion != 3 || msg.Attempt != 1 || msg.NodeType != "http" ||
		string(msg.Input) != `{"a":1}` {
		t.Fatalf("parsed: %+v", msg)
	}
	if _, err := parseTaskMessage(map[string]any{"run_id": "r"}); err == nil {
		t.Fatal("a message without task_id must be rejected")
	}
}
