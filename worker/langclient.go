package worker

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	// Note: This will need to be updated once proto files are generated
	// For now using a placeholder - actual path depends on proto generation
	pb "github.com/mohitkumar/composer/pkg/api/proto/gen/go/workflow_exec"

	"github.com/rs/zerolog"
)

// LangClient wraps a gRPC client connection to a language worker.
type LangClient struct {
	addr   string
	conn   *grpc.ClientConn
	client pb.LanguageExecutorClient
	logger zerolog.Logger
}

// NewLangClient creates a client. If addr is "", returns nil (no language forwarding).
func NewLangClient(addr string, logger zerolog.Logger) (*LangClient, error) {
	if addr == "" {
		return nil, nil
	}
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	client := pb.NewLanguageExecutorClient(conn)
	return &LangClient{addr: addr, conn: conn, client: client, logger: logger}, nil
}

func (c *LangClient) Close() error {
	if c == nil {
		return nil
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// ExecuteInlineCode executes code on remote language worker with a timeout.
func (c *LangClient) ExecuteInlineCode(ctx context.Context, taskID, runtime, code, filename, inputJSON string, timeoutSec int) (*pb.ExecuteResponse, error) {
	if c == nil {
		return nil, ErrNoLangClient
	}
	req := &pb.ExecuteRequest{
		TaskId:         taskID,
		Runtime:        runtime,
		InputJson:      inputJSON,
		TimeoutSeconds: int32(timeoutSec),
		Metadata:       map[string]string{"from": "worker"},
		Payload: &pb.ExecuteRequest_InlineCode{
			InlineCode: &pb.InlineCode{Code: code, Filename: filename},
		},
	}
	ctx2, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec+5)*time.Second)
	defer cancel()
	return c.client.Execute(ctx2, req)
}

var ErrNoLangClient = fmt.Errorf("language client not configured")
