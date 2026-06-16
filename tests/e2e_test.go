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
	"github.com/mohitkumar/composer/worker"
	"github.com/rs/zerolog"
)

const testJWTSecret = "test-secret-key"

func generateTestToken(t *testing.T) string {
	t.Helper()
	jwtService := auth.NewJWTService(testJWTSecret, 24*time.Hour)
	token, err := jwtService.GenerateToken("admin", "admin")
	if err != nil {
		t.Fatalf("Failed to generate test JWT token: %v", err)
	}
	return token
}

func authPost(url, contentType string, body *bytes.Reader, token string) (*http.Response, error) {
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+token)
	return http.DefaultClient.Do(req)
}

func authGet(url, token string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return http.DefaultClient.Do(req)
}

// TestEndToEndWorkflowExecution tests the complete workflow lifecycle using the
// in-memory matching service (no Redis required).
func TestEndToEndWorkflowExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	infra := SetupTestInfra(ctx, t)
	defer infra.Cleanup(ctx, t)

	coordLogger := zerolog.New(infra.Logger).With().Str("component", "coordinator").Logger()
	coordConfig := &coord.Config{
		DBConfig: db.Config{URL: infra.DBURL},
	}
	coordinator, err := coord.NewCoordinator(coordConfig, &coordLogger)
	if err != nil {
		t.Fatalf("Failed to create coordinator: %v", err)
	}

	if err := coordinator.Run(ctx); err != nil {
		t.Fatalf("Failed to start coordinator: %v", err)
	}

	httpPort := 18080
	apiCfg := &api.APIConfig{
		HTTPPort:  httpPort,
		GRPCPort:  15051,
		JWTSecret: testJWTSecret,
	}
	httpServer := httpapi.NewServer(apiCfg, coordinator)
	httpAddr := fmt.Sprintf("http://localhost:%d", httpPort)
	token := generateTestToken(t)

	go func() {
		if err := httpServer.Start(); err != nil && ctx.Err() == nil {
			t.Logf("HTTP server error: %v", err)
		}
	}()
	time.Sleep(100 * time.Millisecond)

	grpcServer := grpcapi.NewServer(apiCfg, coordinator)
	grpcAddr := fmt.Sprintf("localhost:%d", apiCfg.GRPCPort)
	go func() {
		if err := grpcServer.Start(); err != nil && ctx.Err() == nil {
			t.Logf("gRPC server error: %v", err)
		}
	}()
	time.Sleep(100 * time.Millisecond)

	workerLogger := zerolog.New(infra.Logger).With().Str("component", "worker").Logger()
	w, err := worker.NewWorker(worker.WorkerConfig{
		CoordinatorAddr: grpcAddr,
		WorkerID:        "test-worker-1",
		TaskTypes:       []string{"http"},
		LeaseTTLSeconds: 60,
		Logger:          workerLogger,
	})
	if err != nil {
		t.Fatalf("Failed to create worker: %v", err)
	}
	defer w.Close()

	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	go w.Run(workerCtx)
	time.Sleep(100 * time.Millisecond)

	workflowID := "test-workflow"
	workflowSpec := model.WorkflowDefinitionSpec{
		Nodes: []model.Node{
			{NodeID: "fetch_data", Name: "Fetch Data", Type: model.NodeTypeHTTP,
				Spec: json.RawMessage(`{"url":"https://httpbin.org/json","method":"GET","timeout_seconds":10}`)},
			{NodeID: "process_data", Name: "Process Data", Type: model.NodeTypeHTTP,
				Spec: json.RawMessage(`{"url":"https://httpbin.org/post","method":"POST","timeout_seconds":10}`)},
			{NodeID: "store_result", Name: "Store Result", Type: model.NodeTypeHTTP,
				Spec: json.RawMessage(`{"url":"https://httpbin.org/post","method":"POST","timeout_seconds":10}`)},
		},
		Edges: []model.Edge{
			{From: "fetch_data", To: "process_data"},
			{From: "process_data", To: "store_result"},
		},
	}
	specJSON, _ := json.Marshal(workflowSpec)

	registerBody, _ := json.Marshal(map[string]interface{}{
		"workflow_id": workflowID, "name": "Test Workflow", "version": 1, "spec": string(specJSON),
	})
	resp, err := authPost(httpAddr+"/api/v1/workflows", "application/json", bytes.NewReader(registerBody), token)
	if err != nil {
		t.Fatalf("Failed to register workflow: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Expected status 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	executeBody, _ := json.Marshal(map[string]interface{}{
		"workflow_version": 1, "input": json.RawMessage(`{"test":"data"}`), "started_by": "integration-test",
	})
	resp, err = authPost(httpAddr+"/api/v1/workflows/"+workflowID+"/execute", "application/json", bytes.NewReader(executeBody), token)
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected status 202, got %d", resp.StatusCode)
	}
	var executeResp map[string]string
	json.NewDecoder(resp.Body).Decode(&executeResp)
	resp.Body.Close()
	runID := executeResp["run_id"]
	if runID == "" {
		t.Fatal("Expected run_id in response")
	}

	maxWait := 30 * time.Second
	startTime := time.Now()
	for time.Since(startTime) < maxWait {
		resp, err := authGet(httpAddr+"/api/v1/workflows/"+workflowID+"/status?run_id="+runID, token)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var s struct {
				Status         string `json:"status"`
				CompletedTasks int    `json:"completed_tasks"`
				PendingTasks   int    `json:"pending_tasks"`
				RunningTasks   int    `json:"running_tasks"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&s); err == nil {
				resp.Body.Close()
				t.Logf("status=%s completed=%d pending=%d running=%d", s.Status, s.CompletedTasks, s.PendingTasks, s.RunningTasks)
				if s.Status == "SUCCEEDED" || s.Status == "COMPLETED" {
					if s.CompletedTasks != 3 {
						t.Errorf("Expected 3 completed tasks, got %d", s.CompletedTasks)
					}
					t.Log("Workflow completed successfully!")
					return
				}
				if s.Status == "FAILED" {
					t.Fatal("Workflow failed")
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

// TestEndToEndParameterResolutionAndLease validates parameter resolution across
// nodes and lease-based task claiming via worker/coordinator.
func TestEndToEndParameterResolutionAndLease(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	infra := SetupTestInfra(ctx, t)
	defer infra.Cleanup(ctx, t)

	coordLogger := zerolog.New(infra.Logger).With().Str("component", "coordinator").Logger()
	coordinator, err := coord.NewCoordinator(&coord.Config{DBConfig: db.Config{URL: infra.DBURL}}, &coordLogger)
	if err != nil {
		t.Fatalf("Failed to create coordinator: %v", err)
	}
	if err := coordinator.Run(ctx); err != nil {
		t.Fatalf("Failed to start coordinator: %v", err)
	}

	apiCfg := &api.APIConfig{HTTPPort: 18081, GRPCPort: 15052, JWTSecret: testJWTSecret}
	httpServer := httpapi.NewServer(apiCfg, coordinator)
	httpAddr := fmt.Sprintf("http://localhost:%d", apiCfg.HTTPPort)
	token := generateTestToken(t)

	go func() {
		if err := httpServer.Start(); err != nil && ctx.Err() == nil {
			t.Logf("HTTP server error: %v", err)
		}
	}()
	time.Sleep(100 * time.Millisecond)

	grpcServer := grpcapi.NewServer(apiCfg, coordinator)
	grpcAddr := fmt.Sprintf("localhost:%d", apiCfg.GRPCPort)
	go func() {
		if err := grpcServer.Start(); err != nil && ctx.Err() == nil {
			t.Logf("gRPC server error: %v", err)
		}
	}()
	time.Sleep(100 * time.Millisecond)

	var receivedBody []byte
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/step1":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"value":"hello"}`))
		case "/step2":
			body, _ := io.ReadAll(r.Body)
			receivedBody = body
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer testServer.Close()

	workerLogger := zerolog.New(infra.Logger).With().Str("component", "worker").Logger()
	w, err := worker.NewWorker(worker.WorkerConfig{
		CoordinatorAddr: grpcAddr,
		WorkerID:        "lease-worker-1",
		TaskTypes:       []string{"http"},
		LeaseTTLSeconds: 60,
		Logger:          workerLogger,
	})
	if err != nil {
		t.Fatalf("Failed to create worker: %v", err)
	}
	defer w.Close()

	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	go w.Run(workerCtx)
	time.Sleep(100 * time.Millisecond)

	workflowID := "lease-param-workflow"
	workflowSpec := model.WorkflowDefinitionSpec{
		Nodes: []model.Node{
			{NodeID: "fetch_data", Name: "Fetch", Type: model.NodeTypeHTTP,
				Spec: json.RawMessage(fmt.Sprintf(`{"url":"%s/step1","method":"GET","timeout_seconds":5}`, testServer.URL))},
			{NodeID: "use_data", Name: "UseData", Type: model.NodeTypeHTTP,
				Spec: json.RawMessage(fmt.Sprintf(`{"url":"%s/step2","method":"POST","timeout_seconds":5,"body":{"fromPrev":"{{tasks.fetch_data.output.value}}","fromInput":"{{input.test}}"}}`, testServer.URL))},
		},
		Edges: []model.Edge{{From: "fetch_data", To: "use_data"}},
	}
	specJSON, _ := json.Marshal(workflowSpec)

	registerBody, _ := json.Marshal(map[string]interface{}{
		"workflow_id": workflowID, "name": "Lease Param Workflow", "version": 1, "spec": string(specJSON),
	})
	resp, err := authPost(httpAddr+"/api/v1/workflows", "application/json", bytes.NewReader(registerBody), token)
	if err != nil {
		t.Fatalf("Failed to register workflow: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Expected status 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	executeBody, _ := json.Marshal(map[string]interface{}{
		"workflow_version": 1, "input": json.RawMessage(`{"test":"world"}`), "started_by": "e2e-test",
	})
	resp, err = authPost(httpAddr+"/api/v1/workflows/"+workflowID+"/execute", "application/json", bytes.NewReader(executeBody), token)
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected status 202, got %d", resp.StatusCode)
	}
	var executeResp map[string]string
	json.NewDecoder(resp.Body).Decode(&executeResp)
	resp.Body.Close()
	runID := executeResp["run_id"]
	if runID == "" {
		t.Fatal("Expected run_id in response")
	}

	maxWait := 30 * time.Second
	startTime := time.Now()
	for time.Since(startTime) < maxWait {
		resp, err := authGet(httpAddr+"/api/v1/workflows/"+workflowID+"/status?run_id="+runID, token)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var s struct {
				Status         string `json:"status"`
				CompletedTasks int    `json:"completed_tasks"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&s); err == nil {
				resp.Body.Close()
				if s.Status == "SUCCEEDED" || s.Status == "COMPLETED" {
					if s.CompletedTasks != 2 {
						t.Fatalf("expected 2 completed tasks, got %d", s.CompletedTasks)
					}
					break
				}
				if s.Status == "FAILED" {
					t.Fatal("workflow failed")
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

	if len(receivedBody) == 0 {
		t.Fatal("second task did not hit test server")
	}
	var got map[string]interface{}
	if err := json.Unmarshal(receivedBody, &got); err != nil {
		t.Fatalf("failed to parse second task body: %v", err)
	}
	if got["fromPrev"] != "hello" || got["fromInput"] != "world" {
		t.Fatalf("unexpected body values: %+v", got)
	}
}
