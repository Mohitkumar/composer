package grpc

import (
	"context"

	workflowpb "github.com/mohitkumar/composer/pkg/api/proto/gen/go/workflow"
	"github.com/mohitkumar/composer/pkg/coord"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// WorkflowService implements the gRPC service for worker-coordinator communication
type WorkflowService struct {
	workflowpb.UnimplementedWorkflowServiceServer
	coordinator *coord.Coordinator
}

// NewWorkflowService creates a new workflow gRPC service
func NewWorkflowService(coordinator *coord.Coordinator) *WorkflowService {
	return &WorkflowService{
		coordinator: coordinator,
	}
}

// ReportTaskResult handles task result reports from workers
func (s *WorkflowService) ReportTaskResult(ctx context.Context, req *workflowpb.ReportTaskResultRequest) (*workflowpb.ReportTaskResultResponse, error) {
	// Extract metadata
	runID, ok := req.Metadata["run_id"]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "run_id is required in metadata")
	}

	nodeID, ok := req.Metadata["node_id"]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "node_id is required in metadata")
	}

	// Handle task completion or failure
	if req.Status == "completed" {
		err := s.coordinator.HandleNodeCompleted(ctx, runID, nodeID, req.Output)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	} else if req.Status == "failed" {
		errMsg := req.Error
		if errMsg == "" {
			errMsg = "task execution failed"
		}
		err := s.coordinator.HandleNodeFailed(ctx, runID, nodeID, errMsg)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	} else {
		return nil, status.Error(codes.InvalidArgument, "status must be 'completed' or 'failed'")
	}

	return &workflowpb.ReportTaskResultResponse{
		Acknowledged: true,
	}, nil
}

// ExecuteTask is not used in current architecture (workers pull from Redis)
func (s *WorkflowService) ExecuteTask(ctx context.Context, req *workflowpb.ExecuteTaskRequest) (*workflowpb.ExecuteTaskResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ExecuteTask is not implemented - workers pull tasks from Redis streams")
}

// GetTaskStatus retrieves task status
func (s *WorkflowService) GetTaskStatus(ctx context.Context, req *workflowpb.GetTaskStatusRequest) (*workflowpb.GetTaskStatusResponse, error) {
	// TODO: Implement task status retrieval
	return nil, status.Error(codes.Unimplemented, "GetTaskStatus is not yet implemented")
}

// Heartbeat handles worker heartbeats
func (s *WorkflowService) Heartbeat(ctx context.Context, req *workflowpb.HeartbeatRequest) (*workflowpb.HeartbeatResponse, error) {
	// TODO: Implement heartbeat tracking
	return &workflowpb.HeartbeatResponse{
		Acknowledged: true,
	}, nil
}
