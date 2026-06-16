package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	workflowpb "github.com/mohitkumar/composer/pkg/api/proto/gen/go/workflow"
	"github.com/rs/zerolog"
)

// CoordinatorClient wraps a gRPC client connection to the coordinator.
type CoordinatorClient struct {
	conn   *grpc.ClientConn
	client workflowpb.WorkflowServiceClient
	logger zerolog.Logger
}

// NewCoordinatorClient creates a new coordinator gRPC client.
func NewCoordinatorClient(addr string, logger zerolog.Logger) (*CoordinatorClient, error) {
	if addr == "" {
		return nil, fmt.Errorf("coordinator address is required")
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to coordinator: %w", err)
	}

	return &CoordinatorClient{
		conn:   conn,
		client: workflowpb.NewWorkflowServiceClient(conn),
		logger: logger,
	}, nil
}

// Close closes the gRPC connection.
func (c *CoordinatorClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// PollTaskQueue long-polls the coordinator for a task assignment. The call
// blocks on the server until a matching task is available or the 30-second
// deadline expires (DeadlineExceeded is the normal timeout signal; the caller
// should re-poll immediately).
func (c *CoordinatorClient) PollTaskQueue(ctx context.Context, workerID string, taskTypes []string) (*workflowpb.TaskAssignment, error) {
	pollCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return c.client.PollTaskQueue(pollCtx, &workflowpb.PollTaskQueueRequest{
		WorkerId:  workerID,
		TaskTypes: taskTypes,
	})
}

// AcquireTaskLease asks coordinator to claim a lease for this task.
func (c *CoordinatorClient) AcquireTaskLease(ctx context.Context, taskID, owner string, ttlSeconds int64) (bool, error) {
	leaseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := c.client.AcquireTaskLease(leaseCtx, &workflowpb.AcquireTaskLeaseRequest{
		TaskId:     taskID,
		Owner:      owner,
		TtlSeconds: ttlSeconds,
	})
	if err != nil {
		return false, fmt.Errorf("acquire lease failed: %w", err)
	}
	return resp.GetAcquired(), nil
}

// ReportTaskResult reports task execution result to the coordinator.
func (c *CoordinatorClient) ReportTaskResult(ctx context.Context, taskID, runID, nodeID, workflowID string, workflowVersion int, output json.RawMessage, errMsg *string) error {
	reqStatus := "completed"
	if errMsg != nil {
		reqStatus = "failed"
	}

	req := &workflowpb.ReportTaskResultRequest{
		TaskId: taskID,
		Status: reqStatus,
		Output: output,
		Metadata: map[string]string{
			"run_id":           runID,
			"node_id":          nodeID,
			"workflow_id":      workflowID,
			"workflow_version": strconv.Itoa(workflowVersion),
		},
	}
	if errMsg != nil {
		req.Error = *errMsg
	}

	reportCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := c.client.ReportTaskResult(reportCtx, req)
	if err != nil {
		return fmt.Errorf("failed to report task result: %w", err)
	}
	if !resp.Acknowledged {
		return fmt.Errorf("coordinator did not acknowledge task result")
	}
	return nil
}
