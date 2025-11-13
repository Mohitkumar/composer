package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/mohitkumar/composer/pkg/api"
	grpcapi "github.com/mohitkumar/composer/pkg/api/grpc"
	httpapi "github.com/mohitkumar/composer/pkg/api/http"
	"github.com/mohitkumar/composer/pkg/coord"
	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/outbox"
	"github.com/mohitkumar/composer/worker"
	"github.com/rs/zerolog"
)

// TestEndToEndWorkflowExecution tests the complete workflow lifecycle:
// 1. Start coordinator with HTTP and gRPC servers
// 2. Start worker that consumes from Redis
// 3. Register workflow via HTTP API
// 4. Start workflow execution via HTTP API
// 5. Worker pulls task from Redis stream
// 6. Worker executes task
// 7. Worker reports result to coordinator via gRPC
// 8. Coordinator dispatches next tasks
// 9. Workflow completes
func TestEndToEndWorkflowExecution(t *testing.T) {
	// Skip if running in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup test infrastructure (PostgreSQL, Redis, migrations)
	infra := SetupTestInfra(ctx, t)
	defer infra.Cleanup(ctx, t)

	// Setup coordinator
	coordLogger := zerolog.New(infra.Logger).With().Str("component", "coordinator").Logger()
	coordinator := coord.NewCoordinator(infra.DBPool, coordLogger)

	// Create outbox enqueuer
	outboxEnqueuer := outbox.NewOutboxEnqueuer(outbox.EnqueuerConfig{
		DB:           infra.DBPool,
		Redis:        infra.RedisStream,
		Logger:       coordLogger,
		BatchSize:    50,
		Concurrency:  2,
		PollInterval: 500 * time.Millisecond, // Faster for tests
		StreamPrefix: "workflow.",
	})

	// Start outbox enqueuer
	outboxCtx, outboxCancel := context.WithCancel(ctx)
	defer outboxCancel()
	go outboxEnqueuer.Run(outboxCtx)

	// Start HTTP server
	httpPort := 18080 // Use non-standard port for tests
	httpConfig := &api.APIConfig{
		HTTPPort: httpPort,
		GRPCPort: 15051,
	}
	httpServer := httpapi.NewServer(httpConfig, coordinator)
	httpAddr := fmt.Sprintf("http://localhost:%d", httpPort)

	httpCtx, httpCancel := context.WithCancel(ctx)
	defer httpCancel()
	go func() {
		if err := httpServer.Start(); err != nil && httpCtx.Err() == nil {
			t.Logf("HTTP server error: %v", err)
		}
	}()

	// Wait for HTTP server to start
	time.Sleep(100 * time.Millisecond)

	// Start gRPC server
	grpcServer := grpcapi.NewServer(httpConfig, coordinator)
	grpcAddr := fmt.Sprintf("localhost:%d", httpConfig.GRPCPort)

	grpcCtx, grpcCancel := context.WithCancel(ctx)
	defer grpcCancel()
	go func() {
		if err := grpcServer.Start(); err != nil && grpcCtx.Err() == nil {
			t.Logf("gRPC server error: %v", err)
		}
	}()

	// Wait for gRPC server to start
	time.Sleep(100 * time.Millisecond)

	// Setup worker
	workerLogger := zerolog.New(infra.Logger).With().Str("component", "worker").Logger()
	workerConfig := worker.WorkerConfig{
		Redis:           infra.RedisStream,
		CoordinatorAddr: grpcAddr,
		LangWorkerAddr:  "", // No language worker for this test
		StreamName:      "workflow.tasks",
		ConsumerGroup:   "test-workers",
		ConsumerName:    "test-worker-1",
		Logger:          workerLogger,
	}

	w, err := worker.NewWorker(workerConfig)
	if err != nil {
		t.Fatalf("Failed to create worker: %v", err)
	}
	defer w.Close()

	// Ensure consumer group exists
	if err := w.EnsureConsumerGroup(ctx); err != nil {
		t.Fatalf("Failed to ensure consumer group: %v", err)
	}

	// Start worker in background
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	go w.Run(workerCtx)

	// Wait for worker to start
	time.Sleep(100 * time.Millisecond)

	// Test workflow definition
	workflowID := "test-workflow"
	workflowVersion := 1
	workflowName := "Test Workflow"

	// Define a simple workflow: http -> http -> http
	workflowSpec := model.WorkflowDefinitionSpec{
		WorkflowID: workflowID,
		Name:       workflowName,
		Nodes: []model.Node{
			{
				NodeID:  "fetch_data",
				Name:    "Fetch Data",
				Type:    model.NodeTypeTask,
				Runtime: "http",
				Spec: json.RawMessage(`{
					"url": "https://httpbin.org/json",
					"method": "GET",
					"timeout_seconds": 10
				}`),
			},
			{
				NodeID:  "process_data",
				Name:    "Process Data",
				Type:    model.NodeTypeTask,
				Runtime: "http",
				Spec: json.RawMessage(`{
					"url": "https://httpbin.org/post",
					"method": "POST",
					"timeout_seconds": 10
				}`),
			},
			{
				NodeID:  "store_result",
				Name:    "Store Result",
				Type:    model.NodeTypeTask,
				Runtime: "http",
				Spec: json.RawMessage(`{
					"url": "https://httpbin.org/post",
					"method": "POST",
					"timeout_seconds": 10
				}`),
			},
		},
		Edges: []model.Edge{
			{From: "fetch_data", To: "process_data"},
			{From: "process_data", To: "store_result"},
		},
	}

	specJSON, err := json.Marshal(workflowSpec)
	if err != nil {
		t.Fatalf("Failed to marshal workflow spec: %v", err)
	}

	// Step 1: Register workflow via HTTP API
	t.Log("Step 1: Registering workflow via HTTP API")
	registerReq := map[string]interface{}{
		"workflow_id": workflowID,
		"name":        workflowName,
		"version":     workflowVersion,
		"spec":        json.RawMessage(specJSON),
		"created_by":  "integration-test",
	}
	registerBody, _ := json.Marshal(registerReq)
	resp, err := http.Post(httpAddr+"/api/v1/workflows", "application/json", bytes.NewReader(registerBody))
	if err != nil {
		t.Fatalf("Failed to register workflow: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Expected status 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Step 2: Start workflow run via HTTP API
	t.Log("Step 2: Starting workflow run via HTTP API")
	runID := fmt.Sprintf("test-run-%d", time.Now().UnixNano())
	executeReq := map[string]interface{}{
		"run_id":           runID,
		"workflow_id":      workflowID,
		"workflow_version": workflowVersion,
		"input":            json.RawMessage(`{"test": "data"}`),
		"started_by":       "integration-test",
	}
	executeBody, _ := json.Marshal(executeReq)
	resp, err = http.Post(httpAddr+"/api/v1/workflows/"+workflowID+"/execute", "application/json", bytes.NewReader(executeBody))
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected status 202, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Step 3: Wait for outbox to process and worker to pick up tasks
	t.Log("Step 3: Waiting for tasks to be processed...")
	time.Sleep(2 * time.Second) // Give time for outbox to process and worker to execute

	// Step 4: Poll workflow status until completion
	t.Log("Step 4: Polling workflow status until completion")
	maxWait := 30 * time.Second
	startTime := time.Now()
	for time.Since(startTime) < maxWait {
		// Use workflow status endpoint with run_id query parameter
		resp, err := http.Get(httpAddr + "/api/v1/workflows/" + workflowID + "/status?run_id=" + runID)
		if err != nil {
			t.Logf("Failed to get status: %v", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			var status struct {
				ID             string `json:"id"`
				Status         string `json:"status"`
				PendingTasks   int    `json:"pending_tasks"`
				RunningTasks   int    `json:"running_tasks"`
				CompletedTasks int    `json:"completed_tasks"`
				FailedTasks    int    `json:"failed_tasks"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&status); err == nil {
				resp.Body.Close()
				t.Logf("Workflow status: %s (pending: %d, running: %d, completed: %d, failed: %d)",
					status.Status, status.PendingTasks, status.RunningTasks, status.CompletedTasks, status.FailedTasks)

				if status.Status == "SUCCEEDED" || status.Status == "COMPLETED" {
					// Verify all tasks completed
					if status.CompletedTasks != 3 {
						t.Errorf("Expected 3 completed tasks, got %d", status.CompletedTasks)
					}
					if status.PendingTasks != 0 {
						t.Errorf("Expected 0 pending tasks, got %d", status.PendingTasks)
					}
					if status.RunningTasks != 0 {
						t.Errorf("Expected 0 running tasks, got %d", status.RunningTasks)
					}
					t.Log("Workflow completed successfully!")
					return
				}

				if status.Status == "FAILED" {
					t.Fatalf("Workflow failed")
				}
			} else {
				resp.Body.Close()
			}
		} else {
			resp.Body.Close()
		}

		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf("Workflow did not complete within %v", maxWait)
}
