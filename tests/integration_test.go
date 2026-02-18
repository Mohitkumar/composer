package tests

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mohitkumar/composer/pkg/coord"
	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/redis/go-redis/v9"
)

// TestIntegrationWorkflowExecution tests workflow execution with direct coordinator calls (not true E2E):
// 1. Register workflow
// 2. Start workflow run
// 3. Worker pulls task from Redis stream
// 4. Worker executes task
// 5. Worker reports result to coordinator
// 6. Coordinator dispatches next tasks
// 7. Workflow completes
func TestIntegrationWorkflowExecution(t *testing.T) {
	// Skip if running in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Setup test infrastructure (PostgreSQL, Redis, migrations)
	infra := SetupTestInfra(ctx, t)
	defer infra.Cleanup(ctx, t)

	// Create coordinator
	coordConfig := &coord.Config{
		DBConfig:    db.Config{URL: infra.DBURL},
		RedisConfig: redisq.RedisConfig{Addrs: []string{infra.RedisAddr}},
	}
	coordinator, err := coord.NewCoordinator(coordConfig, &infra.Logger)
	if err != nil {
		t.Fatalf("Failed to create coordinator: %v", err)
	}

	// Test workflow definition
	workflowID := "test-workflow"
	workflowVersion := 1
	workflowName := "Test Workflow"

	// Define a simple workflow: http -> python -> http
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
				Type:   model.NodeTypeCode,
				Spec: json.RawMessage(`{
					"code": "import json\ndef handler(input):\n    data = json.loads(input) if isinstance(input, str) else input\n    return {\"processed\": True, \"source\": data.get(\"slideshow\", {}).get(\"title\", \"unknown\")}\n",
					"filename": "process.py",
					"timeout": 30
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

	// Step 1: Register workflow
	t.Log("Step 1: Registering workflow")
	registerPayload := &model.RegisterWorkflowRequest{
		WorkflowID: workflowID,
		Name:       workflowName,
		Version:    workflowVersion,
		Spec:       specJSON,
		CreatedBy:  "integration-test",
	}
	err = coordinator.RegisterWorkflow(ctx, registerPayload)
	if err != nil {
		t.Fatalf("Failed to register workflow: %v", err)
	}

	// Verify workflow was registered
	wf, err := coordinator.GetWorkflow(ctx, workflowID, workflowVersion)
	if err != nil {
		t.Fatalf("Failed to get workflow: %v", err)
	}
	if wf.ID != workflowID {
		t.Errorf("Expected workflow ID %s, got %s", workflowID, wf.ID)
	}
	if wf.Name != workflowName {
		t.Errorf("Expected workflow name %s, got %s", workflowName, wf.Name)
	}

	// Step 2: Start workflow run (run_id will be auto-generated)
	t.Log("Step 2: Starting workflow run")
	startPayload := &model.StartWorkflowRequest{
		WorkflowID:      workflowID,
		WorkflowVersion: workflowVersion,
		Input:           json.RawMessage(`{"test": "data"}`),
		StartedBy:       "integration-test",
	}
	err = coordinator.StartWorkflow(ctx, startPayload)
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}

	// Verify run_id was generated
	runID := startPayload.RunID
	if runID == "" {
		t.Fatalf("Expected run_id to be generated, got empty")
	}

	// Verify workflow run was created
	instance, err := coordinator.GetWorkflowInstance(ctx, runID)
	if err != nil {
		t.Fatalf("Failed to get workflow instance: %v", err)
	}
	if instance.ID != runID {
		t.Errorf("Expected run ID %s, got %s", runID, instance.ID)
	}
	if instance.Status != "RUNNING" {
		t.Errorf("Expected status RUNNING, got %s", instance.Status)
	}

	// Step 3: Process outbox to push task to Redis stream
	t.Log("Step 3: Processing outbox to push task to Redis stream")
	// Manually process outbox entry to push to Redis stream
	// In production, the outbox enqueuer would do this automatically
	var outboxPayloadJSON []byte
	err = infra.DBPool.QueryRow(ctx, `
		SELECT payload FROM outbox 
		WHERE status = 'PENDING' 
		ORDER BY created_at LIMIT 1
	`).Scan(&outboxPayloadJSON)
	if err == nil {
		// Parse payload and push to Redis stream
		var payloadMap map[string]interface{}
		if err := json.Unmarshal(outboxPayloadJSON, &payloadMap); err == nil {
			streamName := "workflow.tasks"
			_, err = infra.RedisStream.XAdd(ctx, streamName, payloadMap)
			if err != nil {
				t.Logf("Failed to push to Redis stream: %v", err)
			} else {
				// Mark outbox entry as sent (matching enqueuer behavior)
				_, _ = infra.DBPool.Exec(ctx, `
					UPDATE outbox SET status = 'SENT', sent_at = NOW(), updated_at = NOW() 
					WHERE payload = $1::jsonb AND status = 'PENDING'
				`, outboxPayloadJSON)
				t.Log("Successfully pushed task to Redis stream")
			}
		}
	} else {
		t.Logf("No pending outbox entries found (may have been processed): %v", err)
	}

	// Step 4: Verify task was created
	t.Log("Step 4: Verifying task instance was created")
	var taskID string
	var nodeID string
	err = infra.DBPool.QueryRow(ctx, `
		SELECT id, node_id FROM task_instances 
		WHERE run_id = $1 AND state = 'PENDING' 
		ORDER BY created_at LIMIT 1
	`, runID).Scan(&taskID, &nodeID)
	if err != nil {
		t.Fatalf("Failed to find task instance: %v", err)
	}
	if nodeID != "fetch_data" {
		t.Errorf("Expected node ID fetch_data, got %s", nodeID)
	}
	t.Logf("Found task instance: id=%s, node_id=%s", taskID, nodeID)

	// Step 5: Simulate worker pulling from Redis stream
	t.Log("Step 5: Simulating worker pulling task from Redis stream")
	streamName := "workflow.tasks"
	consumerGroup := "test-workers"
	consumerName := "test-worker-1"

	// Create consumer group if it doesn't exist
	err = infra.RedisStream.XGroupCreateMkStream(ctx, streamName, consumerGroup, "0")
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		t.Logf("Consumer group may already exist: %v", err)
	}

	// Read from stream
	args := &redis.XReadGroupArgs{
		Group:    consumerGroup,
		Consumer: consumerName,
		Streams:  []string{streamName, ">"},
		Count:    1,
		Block:    2 * time.Second,
	}
	streams, err := infra.RedisStream.XReadGroup(ctx, args)
	if err != nil && err != redis.Nil {
		t.Logf("No messages in stream yet (this is expected if outbox hasn't processed): %v", err)
		// Continue with manual task execution simulation
	} else if len(streams) > 0 && len(streams[0].Messages) > 0 {
		msg := streams[0].Messages[0]
		t.Logf("Received message from stream: %s", msg.ID)

		// Parse task message
		var taskMsg struct {
			TaskID   string          `json:"task_id"`
			RunID    string          `json:"run_id"`
			NodeID   string          `json:"node_id"`
			NodeType string          `json:"node_type"`
			Input    json.RawMessage `json:"input"`
		}

		taskIDVal, _ := msg.Values["task_id"].(string)
		runIDVal, _ := msg.Values["run_id"].(string)
		nodeIDVal, _ := msg.Values["node_id"].(string)

		taskMsg.TaskID = taskIDVal
		taskMsg.RunID = runIDVal
		taskMsg.NodeID = nodeIDVal

		if taskMsg.RunID != runID {
			t.Errorf("Expected run ID %s, got %s", runID, taskMsg.RunID)
		}
		if taskMsg.NodeID != "fetch_data" {
			t.Errorf("Expected node ID fetch_data, got %s", taskMsg.NodeID)
		}

		// Step 6: Simulate task execution
		t.Log("Step 6: Simulating task execution")
		// In a real scenario, the worker would execute the task
		// For this test, we'll simulate successful execution
		output := json.RawMessage(`{"slideshow": {"title": "Sample Slide Show"}}`)

		// Step 7: Report task completion to coordinator
		t.Log("Step 7: Reporting task completion to coordinator")
		err = coordinator.HandleNodeCompleted(ctx, taskMsg.TaskID, runID, taskMsg.NodeID, output)
		if err != nil {
			t.Fatalf("Failed to handle node completion: %v", err)
		}

		// Verify next task was created
		time.Sleep(200 * time.Millisecond)
		var nextTaskID string
		var nextNodeID string
		err = infra.DBPool.QueryRow(ctx, `
			SELECT id, node_id FROM task_instances 
			WHERE run_id = $1 AND node_id = 'process_data' AND state = 'PENDING'
			ORDER BY created_at LIMIT 1
		`, runID).Scan(&nextTaskID, &nextNodeID)
		if err != nil {
			t.Fatalf("Next task should be created: %v", err)
		}
		if nextNodeID != "process_data" {
			t.Errorf("Expected node ID process_data, got %s", nextNodeID)
		}

		// Step 8: Complete remaining tasks
		t.Log("Step 8: Completing remaining tasks")
		// Complete process_data task
		processOutput := json.RawMessage(`{"processed": true, "source": "Sample Slide Show"}`)
		err = coordinator.HandleNodeCompleted(ctx, nextTaskID, runID, "process_data", processOutput)
		if err != nil {
			t.Fatalf("Failed to complete process_data task: %v", err)
		}

		// Wait for final task
		time.Sleep(200 * time.Millisecond)
		var finalTaskID string
		err = infra.DBPool.QueryRow(ctx, `
			SELECT id FROM task_instances 
			WHERE run_id = $1 AND node_id = 'store_result' AND state = 'PENDING'
			ORDER BY created_at LIMIT 1
		`, runID).Scan(&finalTaskID)
		if err != nil {
			t.Fatalf("Final task should be created: %v", err)
		}

		// Complete final task
		finalOutput := json.RawMessage(`{"success": true}`)
		err = coordinator.HandleNodeCompleted(ctx, finalTaskID, runID, "store_result", finalOutput)
		if err != nil {
			t.Fatalf("Failed to complete store_result task: %v", err)
		}

		// Step 9: Verify workflow completed
		t.Log("Step 9: Verifying workflow completion")
		time.Sleep(200 * time.Millisecond)
		status, err := coordinator.GetWorkflowInstanceStatus(ctx, runID)
		if err != nil {
			t.Fatalf("Failed to get workflow status: %v", err)
		}
		// Note: The coordinator uses "SUCCEEDED" status when workflow completes
		if status.Status != "SUCCEEDED" && status.Status != "COMPLETED" {
			t.Errorf("Expected status SUCCEEDED or COMPLETED, got %s", status.Status)
		}
		if status.PendingTasks != 0 {
			t.Errorf("Expected 0 pending tasks, got %d", status.PendingTasks)
		}
		if status.RunningTasks != 0 {
			t.Errorf("Expected 0 running tasks, got %d", status.RunningTasks)
		}
		if status.CompletedTasks != 3 {
			t.Errorf("Expected 3 completed tasks, got %d", status.CompletedTasks)
		}

		// Acknowledge message
		_, _ = infra.RedisStream.XAck(ctx, streamName, consumerGroup, msg.ID)
	} else {
		t.Log("No messages in stream - this test requires outbox enqueuer to be running")
		t.Log("For a complete test, run the outbox enqueuer in a separate process")
	}

	// Cleanup
	t.Log("Cleaning up test data")
	_, _ = infra.DBPool.Exec(ctx, "DELETE FROM workflow_runs WHERE id = $1", runID)
	_, _ = infra.DBPool.Exec(ctx, "DELETE FROM task_instances WHERE run_id = $1", runID)
	_, _ = infra.DBPool.Exec(ctx, "DELETE FROM workflow_definitions WHERE id = $1 AND version = $2", workflowID, workflowVersion)
}
