package grpc

import (
	"context"
	"time"

	workflowpb "github.com/mohitkumar/composer/pkg/api/proto/gen/go/workflow"
	"github.com/mohitkumar/composer/pkg/coord"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const defaultLeaseTTL = 90 * time.Second

// WorkflowService implements the gRPC service for worker-coordinator communication.
// RPCs not overridden here (ExecuteTask, GetTaskStatus) return Unimplemented
// via the embedded server: workers pull tasks from the stream instead.
type WorkflowService struct {
	workflowpb.UnimplementedWorkflowServiceServer
	coordinator *coord.Coordinator
}

// NewWorkflowService creates a new workflow gRPC service
func NewWorkflowService(coordinator *coord.Coordinator) *WorkflowService {
	return &WorkflowService{coordinator: coordinator}
}

// ReportTaskResult applies a worker's result for one task attempt.
func (s *WorkflowService) ReportTaskResult(ctx context.Context, req *workflowpb.ReportTaskResultRequest) (*workflowpb.ReportTaskResultResponse, error) {
	res := coord.TaskResult{
		TaskID:  req.GetTaskId(),
		RunID:   firstNonEmpty(req.GetRunId(), req.GetMetadata()["run_id"]),
		NodeID:  firstNonEmpty(req.GetNodeId(), req.GetMetadata()["node_id"]),
		Attempt: int(req.GetAttempt()),
		Output:  req.GetOutput(),
	}
	if res.TaskID == "" || res.RunID == "" || res.NodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "task_id, run_id and node_id are required")
	}

	var err error
	switch req.GetStatus() {
	case "completed":
		err = s.coordinator.HandleNodeCompleted(ctx, res)
	case "failed":
		res.Error = firstNonEmpty(req.GetError(), "task execution failed")
		err = s.coordinator.HandleNodeFailed(ctx, res)
	default:
		return nil, status.Error(codes.InvalidArgument, "status must be 'completed' or 'failed'")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &workflowpb.ReportTaskResultResponse{Acknowledged: true}, nil
}

// Heartbeat acknowledges worker heartbeats; liveness is tracked through task leases.
func (s *WorkflowService) Heartbeat(ctx context.Context, req *workflowpb.HeartbeatRequest) (*workflowpb.HeartbeatResponse, error) {
	return &workflowpb.HeartbeatResponse{Acknowledged: true}, nil
}

// AcquireTaskLease lets a worker claim a task before executing it.
func (s *WorkflowService) AcquireTaskLease(ctx context.Context, req *workflowpb.AcquireTaskLeaseRequest) (*workflowpb.AcquireTaskLeaseResponse, error) {
	return s.lease(ctx, req, s.coordinator.LeaseManager.Acquire)
}

// RenewTaskLease extends a lease the worker holds while the task runs.
func (s *WorkflowService) RenewTaskLease(ctx context.Context, req *workflowpb.AcquireTaskLeaseRequest) (*workflowpb.AcquireTaskLeaseResponse, error) {
	return s.lease(ctx, req, s.coordinator.LeaseManager.Renew)
}

type leaseFunc func(ctx context.Context, taskID, owner string, ttl time.Duration) (bool, error)

func (s *WorkflowService) lease(ctx context.Context, req *workflowpb.AcquireTaskLeaseRequest, fn leaseFunc) (*workflowpb.AcquireTaskLeaseResponse, error) {
	if req.GetTaskId() == "" || req.GetOwner() == "" {
		return nil, status.Error(codes.InvalidArgument, "task_id and owner are required")
	}
	ttl := time.Duration(req.GetTtlSeconds()) * time.Second
	if ttl <= 0 {
		ttl = defaultLeaseTTL
	}
	acquired, err := fn(ctx, req.GetTaskId(), req.GetOwner(), ttl)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &workflowpb.AcquireTaskLeaseResponse{Acquired: acquired}, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
