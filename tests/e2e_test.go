package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mohitkumar/composer/pkg/api"
	grpcapi "github.com/mohitkumar/composer/pkg/api/grpc"
	httpapi "github.com/mohitkumar/composer/pkg/api/http"
	"github.com/mohitkumar/composer/pkg/auth"
	"github.com/mohitkumar/composer/pkg/coord"
	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/outbox"
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/mohitkumar/composer/pkg/stream"
	redisstream "github.com/mohitkumar/composer/pkg/stream/redis"
	"github.com/mohitkumar/composer/worker"
	"github.com/rs/zerolog"
)

const testJWTSecret = "test-secret-key"

// generateTestToken creates a JWT token for the admin user for testing
func generateTestToken(t *testing.T) string {
	t.Helper()
	jwtService := auth.NewJWTService(testJWTSecret, 24*time.Hour)
	token, err := jwtService.GenerateToken("admin", "admin")
	if err != nil {
		t.Fatalf("Failed to generate test JWT token: %v", err)
	}
	return token
}

// authPost makes an authenticated POST request with Bearer token
func authPost(url, contentType string, body *bytes.Reader, token string) (*http.Response, error) {
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+token)
	return http.DefaultClient.Do(req)
}

// authGet makes an authenticated GET request with Bearer token
func authGet(url, token string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return http.DefaultClient.Do(req)
}

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
	coordConfig := &coord.Config{
		DBConfig:    db.Config{URL: infra.DBURL},
		RedisConfig: redisq.RedisConfig{Addrs: []string{infra.RedisAddr}},
	}
	coordinator, err := coord.NewCoordinator(coordConfig, &coordLogger)
	if err != nil {
		t.Fatalf("Failed to create coordinator: %v", err)
	}

	// Create outbox enqueuer
	outboxEnqueuer := outbox.NewOutboxEnqueuer(outbox.EnqueuerConfig{
		DB:           infra.DBPool,
		Publisher:    redisstream.NewPublisher(infra.RedisStream),
		Logger:       coordLogger,
		BatchSize:    50,
		Concurrency:  2,
		PollInterval: 500 * time.Millisecond, // Faster for tests
	})

	// Start outbox enqueuer
	outboxCtx, outboxCancel := context.WithCancel(ctx)
	defer outboxCancel()
	go outboxEnqueuer.Run(outboxCtx)

	// Start HTTP server
	httpPort := 18080 // Use non-standard port for tests
	httpConfig := &api.APIConfig{
		HTTPPort:  httpPort,
		GRPCPort:  15051,
		JWTSecret: testJWTSecret,
	}
	httpServer := httpapi.NewServer(httpConfig, coordinator)
	httpAddr := fmt.Sprintf("http://localhost:%d", httpPort)
	token := generateTestToken(t)

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
		Streams:         queueConsumers(infra, []string{"http"}, stream.DefaultShards, "test-workers", "test-worker-1"),
		CoordinatorAddr: grpcAddr,
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
		Nodes: []model.Node{
			{
				NodeID: "fetch_data",
				Name:   "Fetch Data",
				Type:   model.NodeTypeHTTP,
				Spec: json.RawMessage(`{
					"url": "https://httpbin.org/json",
					"method": "GET",
					"timeout_seconds": 10
				}`),
			},
			{
				NodeID: "process_data",
				Name:   "Process Data",
				Type:   model.NodeTypeHTTP,
				Spec: json.RawMessage(`{
					"url": "https://httpbin.org/post",
					"method": "POST",
					"timeout_seconds": 10
				}`),
			},
			{
				NodeID: "store_result",
				Name:   "Store Result",
				Type:   model.NodeTypeHTTP,
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
		"spec":        string(specJSON),
	}
	registerBody, _ := json.Marshal(registerReq)
	resp, err := authPost(httpAddr+"/api/v1/workflows", "application/json", bytes.NewReader(registerBody), token)
	if err != nil {
		t.Fatalf("Failed to register workflow: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Expected status 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Step 2: Start workflow run via HTTP API
	t.Log("Step 2: Starting workflow run via HTTP API")
	executeReq := map[string]interface{}{
		"workflow_version": workflowVersion,
		"input":            json.RawMessage(`{"test": "data"}`),
		"started_by":       "integration-test",
	}
	executeBody, _ := json.Marshal(executeReq)
	resp, err = authPost(httpAddr+"/api/v1/workflows/"+workflowID+"/execute", "application/json", bytes.NewReader(executeBody), token)
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected status 202, got %d", resp.StatusCode)
	}

	// Read run_id from response
	var executeResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&executeResp); err != nil {
		t.Fatalf("Failed to decode execute response: %v", err)
	}
	resp.Body.Close()
	runID := executeResp["run_id"]
	if runID == "" {
		t.Fatalf("Expected run_id in response, got empty")
	}

	// Step 3: Wait for outbox to process and worker to pick up tasks
	t.Log("Step 3: Waiting for tasks to be processed...")
	time.Sleep(2 * time.Second) // Give time for outbox to process and worker to execute

	// Step 4: Poll workflow status until completion
	t.Log("Step 4: Polling workflow status until completion")
	maxWait := 30 * time.Second
	startTime := time.Now()
	for time.Since(startTime) < maxWait {
		// Use workflow status endpoint with run_id query parameter
		resp, err := authGet(httpAddr+"/api/v1/workflows/"+workflowID+"/status?run_id="+runID, token)
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

// TestEndToEndParameterResolutionAndLease validates parameter resolution across nodes
// and lease-based task claiming via worker/coordinator.
func TestEndToEndParameterResolutionAndLease(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	infra := SetupTestInfra(ctx, t)
	defer infra.Cleanup(ctx, t)

	coordLogger := zerolog.New(infra.Logger).With().Str("component", "coordinator").Logger()
	coordConfig := &coord.Config{
		DBConfig:    db.Config{URL: infra.DBURL},
		RedisConfig: redisq.RedisConfig{Addrs: []string{infra.RedisAddr}},
	}
	coordinator, err := coord.NewCoordinator(coordConfig, &coordLogger)
	if err != nil {
		t.Fatalf("Failed to create coordinator: %v", err)
	}

	outboxEnqueuer := outbox.NewOutboxEnqueuer(outbox.EnqueuerConfig{
		DB:           infra.DBPool,
		Publisher:    redisstream.NewPublisher(infra.RedisStream),
		Logger:       coordLogger,
		BatchSize:    50,
		Concurrency:  2,
		PollInterval: 300 * time.Millisecond,
	})
	outboxCtx, outboxCancel := context.WithCancel(ctx)
	defer outboxCancel()
	go outboxEnqueuer.Run(outboxCtx)

	httpPort := 18081
	httpConfig := &api.APIConfig{
		HTTPPort:  httpPort,
		GRPCPort:  15052,
		JWTSecret: testJWTSecret,
	}
	httpServer := httpapi.NewServer(httpConfig, coordinator)
	httpAddr := fmt.Sprintf("http://localhost:%d", httpPort)
	token := generateTestToken(t)
	httpCtx, httpCancel := context.WithCancel(ctx)
	defer httpCancel()
	go func() {
		if err := httpServer.Start(); err != nil && httpCtx.Err() == nil {
			t.Logf("HTTP server error: %v", err)
		}
	}()
	time.Sleep(100 * time.Millisecond)

	grpcServer := grpcapi.NewServer(httpConfig, coordinator)
	grpcAddr := fmt.Sprintf("localhost:%d", httpConfig.GRPCPort)
	grpcCtx, grpcCancel := context.WithCancel(ctx)
	defer grpcCancel()
	go func() {
		if err := grpcServer.Start(); err != nil && grpcCtx.Err() == nil {
			t.Logf("gRPC server error: %v", err)
		}
	}()
	time.Sleep(100 * time.Millisecond)

	// Test HTTP backend used by tasks
	var receivedBody []byte
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/step1":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"value":"hello"}`))
		case "/step2":
			body, _ := io.ReadAll(r.Body)
			receivedBody = body
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer testServer.Close()

	workerLogger := zerolog.New(infra.Logger).With().Str("component", "worker").Logger()
	workerConfig := worker.WorkerConfig{
		Streams:         queueConsumers(infra, []string{"http"}, stream.DefaultShards, "lease-workers", "lease-worker-1"),
		CoordinatorAddr: grpcAddr,
		ConsumerName:    "lease-worker-1",
		LeaseTTLSeconds: 60,
		Logger:          workerLogger,
	}
	w, err := worker.NewWorker(workerConfig)
	if err != nil {
		t.Fatalf("Failed to create worker: %v", err)
	}
	defer w.Close()
	if err := w.EnsureConsumerGroup(ctx); err != nil {
		t.Fatalf("Failed to ensure consumer group: %v", err)
	}
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	go w.Run(workerCtx)
	time.Sleep(100 * time.Millisecond)

	workflowID := "lease-param-workflow"
	workflowVersion := 1
	workflowName := "Lease Param Workflow"

	workflowSpec := model.WorkflowDefinitionSpec{
		Nodes: []model.Node{
			{
				NodeID: "fetch_data",
				Name:   "Fetch",
				Type:   model.NodeTypeHTTP,
				Spec: json.RawMessage(fmt.Sprintf(`{
					"url": "%s/step1",
					"method": "GET",
					"timeout_seconds": 5
				}`, testServer.URL)),
			},
			{
				NodeID: "use_data",
				Name:   "UseData",
				Type:   model.NodeTypeHTTP,
				Spec: json.RawMessage(fmt.Sprintf(`{
					"url": "%s/step2",
					"method": "POST",
					"timeout_seconds": 5,
					"body": {
						"fromPrev": "{{tasks.fetch_data.output.value}}",
						"fromInput": "{{input.test}}"
					}
				}`, testServer.URL)),
			},
		},
		Edges: []model.Edge{
			{From: "fetch_data", To: "use_data"},
		},
	}

	specJSON, err := json.Marshal(workflowSpec)
	if err != nil {
		t.Fatalf("Failed to marshal workflow spec: %v", err)
	}

	// Register workflow
	registerReq := map[string]interface{}{
		"workflow_id": workflowID,
		"name":        workflowName,
		"version":     workflowVersion,
		"spec":        string(specJSON),
	}
	registerBody, _ := json.Marshal(registerReq)
	resp, err := authPost(httpAddr+"/api/v1/workflows", "application/json", bytes.NewReader(registerBody), token)
	if err != nil {
		t.Fatalf("Failed to register workflow: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Expected status 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Execute workflow
	executeReq := map[string]interface{}{
		"workflow_version": workflowVersion,
		"input":            json.RawMessage(`{"test":"world"}`),
		"started_by":       "e2e-test",
	}
	executeBody, _ := json.Marshal(executeReq)
	resp, err = authPost(httpAddr+"/api/v1/workflows/"+workflowID+"/execute", "application/json", bytes.NewReader(executeBody), token)
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected status 202, got %d", resp.StatusCode)
	}

	// Read run_id from response
	var executeResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&executeResp); err != nil {
		t.Fatalf("Failed to decode execute response: %v", err)
	}
	resp.Body.Close()
	runID := executeResp["run_id"]
	if runID == "" {
		t.Fatalf("Expected run_id in response, got empty")
	}

	// Wait for completion
	maxWait := 30 * time.Second
	startTime := time.Now()
	for time.Since(startTime) < maxWait {
		resp, err := authGet(httpAddr+"/api/v1/workflows/"+workflowID+"/status?run_id="+runID, token)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var status struct {
				Status         string `json:"status"`
				CompletedTasks int    `json:"completed_tasks"`
				PendingTasks   int    `json:"pending_tasks"`
				RunningTasks   int    `json:"running_tasks"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&status); err == nil {
				resp.Body.Close()
				if status.Status == "SUCCEEDED" || status.Status == "COMPLETED" {
					if status.CompletedTasks != 2 {
						t.Fatalf("expected 2 completed tasks, got %d", status.CompletedTasks)
					}
					break
				}
				if status.Status == "FAILED" {
					t.Fatalf("workflow failed")
				}
			} else {
				resp.Body.Close()
			}
		} else {
			resp.Body.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}

	if time.Since(startTime) >= maxWait {
		t.Fatalf("workflow did not complete in time")
	}

	// Validate parameter resolution reached second task
	if len(receivedBody) == 0 {
		t.Fatalf("second task did not hit test server")
	}
	var got map[string]interface{}
	if err := json.Unmarshal(receivedBody, &got); err != nil {
		t.Fatalf("failed to parse second task body: %v", err)
	}
	if got["fromPrev"] != "hello" || got["fromInput"] != "world" {
		t.Fatalf("unexpected body values: %+v", got)
	}
}
