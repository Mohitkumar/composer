package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	// Note: This import path assumes proto files are generated
	// Update this path based on actual generated code location
	workflowpb "github.com/mohitkumar/composer/pkg/api/proto/gen/go/workflow"
	"github.com/rs/zerolog"
)

// CoordinatorClient wraps a gRPC client connection to the coordinator
type CoordinatorClient struct {
	conn   *grpc.ClientConn
	client workflowpb.WorkflowServiceClient
	logger zerolog.Logger
}

// NewCoordinatorClient creates a new coordinator gRPC client
func NewCoordinatorClient(addr string, logger zerolog.Logger) (*CoordinatorClient, error) {
	if addr == "" {
		return nil, fmt.Errorf("coordinator address is required")
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to coordinator: %w", err)
	}

	client := workflowpb.NewWorkflowServiceClient(conn)

	return &CoordinatorClient{
		conn:   conn,
		client: client,
		logger: logger,
	}, nil
}

// Close closes the gRPC connection
func (c *CoordinatorClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// AcquireTaskLease asks the coordinator to claim a task for owner.
func (c *CoordinatorClient) AcquireTaskLease(ctx context.Context, taskID, owner string, ttlSeconds int64) (bool, error) {
	return c.lease(ctx, c.client.AcquireTaskLease, taskID, owner, ttlSeconds)
}

// RenewTaskLease extends a lease owner holds; false means the lease was lost.
func (c *CoordinatorClient) RenewTaskLease(ctx context.Context, taskID, owner string, ttlSeconds int64) (bool, error) {
	return c.lease(ctx, c.client.RenewTaskLease, taskID, owner, ttlSeconds)
}

type leaseRPC func(context.Context, *workflowpb.AcquireTaskLeaseRequest, ...grpc.CallOption) (*workflowpb.AcquireTaskLeaseResponse, error)

func (c *CoordinatorClient) lease(ctx context.Context, rpc leaseRPC, taskID, owner string, ttlSeconds int64) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := rpc(ctx, &workflowpb.AcquireTaskLeaseRequest{
		TaskId:     taskID,
		Owner:      owner,
		TtlSeconds: ttlSeconds,
	})
	if err != nil {
		return false, fmt.Errorf("task lease rpc: %w", err)
	}
	return resp.GetAcquired(), nil
}

// ReportTaskResult reports the outcome of one task attempt. errMsg is nil on success.
func (c *CoordinatorClient) ReportTaskResult(ctx context.Context, msg *TaskMessage, output json.RawMessage, errMsg *string) error {
	req := &workflowpb.ReportTaskResultRequest{
		TaskId:  msg.TaskID,
		RunId:   msg.RunID,
		NodeId:  msg.NodeID,
		Attempt: int32(msg.Attempt),
		Status:  "completed",
		Output:  output,
	}
	if errMsg != nil {
		req.Status = "failed"
		req.Error = *errMsg
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := c.client.ReportTaskResult(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to report task result: %w", err)
	}
	if !resp.GetAcknowledged() {
		return fmt.Errorf("coordinator did not acknowledge task result")
	}
	return nil
}
