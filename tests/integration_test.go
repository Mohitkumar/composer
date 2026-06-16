package tests

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mohitkumar/composer/pkg/coord"
	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/model"
)

// TestIntegrationWorkflowExecution tests workflow execution via direct coordinator
// API calls, using the in-memory matching service (no Redis required).
func TestIntegrationWorkflowExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	infra := SetupTestInfra(ctx, t)
	defer infra.Cleanup(ctx, t)

	coordinator, err := coord.NewCoordinator(&coord.Config{DBConfig: db.Config{URL: infra.DBURL}}, &infra.Logger)
	if err != nil {
		t.Fatalf("Failed to create coordinator: %v", err)
	}
	if err := coordinator.Run(ctx); err != nil {
		t.Fatalf("Failed to start coordinator: %v", err)
	}

	workflowID := "test-workflow"
	workflowVersion := 1
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

	t.Log("Step 1: Registering workflow")
	err = coordinator.RegisterWorkflow(ctx, &model.RegisterWorkflowRequest{
		WorkflowID: workflowID,
		Name:       "Test Workflow",
		Version:    workflowVersion,
		Spec:       specJSON,
		CreatedBy:  "admin",
	})
	if err != nil {
		t.Fatalf("Failed to register workflow: %v", err)
	}

	wf, err := coordinator.GetWorkflow(ctx, workflowID, workflowVersion)
	if err != nil {
		t.Fatalf("Failed to get workflow: %v", err)
	}
	if wf.ID != workflowID {
		t.Errorf("Expected workflow ID %s, got %s", workflowID, wf.ID)
	}

	t.Log("Step 2: Starting workflow run")
	startPayload := &model.StartWorkflowRequest{
		WorkflowID:      workflowID,
		WorkflowVersion: workflowVersion,
		Input:           json.RawMessage(`{"test":"data"}`),
		StartedBy:       "integration-test",
	}
	if err := coordinator.StartWorkflow(ctx, startPayload); err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	runID := startPayload.RunID
	if runID == "" {
		t.Fatal("Expected run_id to be generated")
	}

	instance, err := coordinator.GetWorkflowInstance(ctx, runID)
	if err != nil {
		t.Fatalf("Failed to get workflow instance: %v", err)
	}
	if instance.Status != "RUNNING" {
		t.Errorf("Expected status RUNNING, got %s", instance.Status)
	}

	t.Log("Step 3: Verifying task dispatched to matching service")
	// Give the matching service a moment to receive the dispatched task.
	time.Sleep(50 * time.Millisecond)

	var taskID, nodeID string
	err = infra.DBPoolRaw.QueryRow(ctx, `
		SELECT id, node_id FROM task_instances
		WHERE run_id = $1 AND state = 'PENDING'
		ORDER BY created_at LIMIT 1
	`, runID).Scan(&taskID, &nodeID)
	if err != nil {
		t.Fatalf("Failed to find task instance: %v", err)
	}
	if nodeID != "fetch_data" {
		t.Errorf("Expected node_id fetch_data, got %s", nodeID)
	}
	t.Logf("Found task: id=%s node_id=%s", taskID, nodeID)

	t.Log("Step 4: Simulating task execution and reporting completion")
	fetchOutput := json.RawMessage(`{"slideshow":{"title":"Sample Slide Show"}}`)
	if err := coordinator.HandleNodeCompleted(ctx, taskID, runID, nodeID, workflowID, workflowVersion, fetchOutput); err != nil {
		t.Fatalf("Failed to handle node completion: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	var nextTaskID, nextNodeID string
	err = infra.DBPoolRaw.QueryRow(ctx, `
		SELECT id, node_id FROM task_instances
		WHERE run_id = $1 AND node_id = 'process_data' AND state = 'PENDING'
		ORDER BY created_at LIMIT 1
	`, runID).Scan(&nextTaskID, &nextNodeID)
	if err != nil {
		t.Fatalf("Expected process_data task to be created: %v", err)
	}

	if err := coordinator.HandleNodeCompleted(ctx, nextTaskID, runID, "process_data", workflowID, workflowVersion,
		json.RawMessage(`{"processed":true}`)); err != nil {
		t.Fatalf("Failed to complete process_data: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	var finalTaskID string
	err = infra.DBPoolRaw.QueryRow(ctx, `
		SELECT id FROM task_instances
		WHERE run_id = $1 AND node_id = 'store_result' AND state = 'PENDING'
		ORDER BY created_at LIMIT 1
	`, runID).Scan(&finalTaskID)
	if err != nil {
		t.Fatalf("Expected store_result task to be created: %v", err)
	}

	if err := coordinator.HandleNodeCompleted(ctx, finalTaskID, runID, "store_result", workflowID, workflowVersion,
		json.RawMessage(`{"success":true}`)); err != nil {
		t.Fatalf("Failed to complete store_result: %v", err)
	}

	t.Log("Step 5: Verifying workflow completed")
	time.Sleep(100 * time.Millisecond)
	status, err := coordinator.GetWorkflowInstanceStatus(ctx, runID)
	if err != nil {
		t.Fatalf("Failed to get workflow status: %v", err)
	}
	if status.Status != "SUCCEEDED" && status.Status != "COMPLETED" {
		t.Errorf("Expected SUCCEEDED or COMPLETED, got %s", status.Status)
	}
	if status.PendingTasks != 0 {
		t.Errorf("Expected 0 pending tasks, got %d", status.PendingTasks)
	}
	if status.CompletedTasks != 3 {
		t.Errorf("Expected 3 completed tasks, got %d", status.CompletedTasks)
	}
	t.Log("Workflow completed successfully!")
}
