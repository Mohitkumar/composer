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

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to dial coordinator: %w", err)
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

// ReportTaskResult reports task execution result to the coordinator
func (c *CoordinatorClient) ReportTaskResult(ctx context.Context, taskID, runID, nodeID string, output json.RawMessage, errMsg *string) error {
	status := "completed"
	if errMsg != nil {
		status = "failed"
	}

	req := &workflowpb.ReportTaskResultRequest{
		TaskId: taskID,
		Status: status,
		Output: output,
		Metadata: map[string]string{
			"run_id":  runID,
			"node_id": nodeID,
		},
	}

	if errMsg != nil {
		req.Error = *errMsg
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := c.client.ReportTaskResult(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to report task result: %w", err)
	}

	if !resp.Acknowledged {
		return fmt.Errorf("coordinator did not acknowledge task result")
	}

	return nil
}
