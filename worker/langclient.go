package worker

import (
	"context"
	"fmt"
	"sync"
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

// LangClientManager manages multiple language clients, routing by runtime
type LangClientManager struct {
	clients map[string]*LangClient
	mu      sync.RWMutex
	logger  zerolog.Logger
}

// NewLangClientManager creates a new language client manager
func NewLangClientManager(addrs map[string]string, logger zerolog.Logger) (*LangClientManager, error) {
	manager := &LangClientManager{
		clients: make(map[string]*LangClient),
		logger:  logger,
	}

	// Create clients for each runtime
	for runtime, addr := range addrs {
		if addr == "" {
			logger.Warn().Str("runtime", runtime).Msg("empty address for runtime, skipping")
			continue
		}
		client, err := NewLangClient(addr, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create language client for runtime %s at %s: %w", runtime, addr, err)
		}
		manager.clients[runtime] = client
		logger.Info().Str("runtime", runtime).Str("addr", addr).Msg("language client created")
	}

	// Also support legacy single LangWorkerAddr for backward compatibility
	// This will be handled in GetClientForRuntime

	return manager, nil
}

// GetClientForRuntime returns the language client for the given runtime
func (m *LangClientManager) GetClientForRuntime(runtime string) (*LangClient, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Normalize runtime name
	normalized := normalizeRuntime(runtime)

	client, ok := m.clients[normalized]
	if !ok {
		return nil, fmt.Errorf("no language client configured for runtime: %s (normalized: %s)", runtime, normalized)
	}

	return client, nil
}

// normalizeRuntime normalizes runtime names to handle aliases
func normalizeRuntime(runtime string) string {
	normalized := map[string]string{
		"js":         "js",
		"javascript": "js",
		"node":       "js",
		"nodejs":     "js",
		"python":     "python",
		"py":         "python",
	}
	if norm, ok := normalized[runtime]; ok {
		return norm
	}
	return runtime
}

// Close closes all language clients
func (m *LangClientManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for runtime, client := range m.clients {
		if err := client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close client for %s: %w", runtime, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing language clients: %v", errs)
	}
	return nil
}
