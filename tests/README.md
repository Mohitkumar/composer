# Integration Tests

This directory contains end-to-end integration tests for the Composer workflow engine.

## TestEndToEndWorkflowExecution

This integration test (`integration_test.go`) tests the complete end-to-end workflow execution lifecycle.

### What It Tests

The test verifies the complete workflow execution flow:

1. **Workflow Registration**: Registers a test workflow with 3 nodes (HTTP → Python → HTTP)
2. **Workflow Execution**: Starts a workflow run
3. **Task Creation**: Verifies initial task instance is created in the database
4. **Outbox Pattern**: Checks that task is added to outbox table
5. **Redis Stream**: Simulates worker pulling task from Redis stream
6. **Task Execution**: Simulates task execution (in real scenario, worker would execute)
7. **Result Reporting**: Reports task completion to coordinator via `HandleNodeCompleted`
8. **Next Task Dispatch**: Verifies coordinator creates next task in the workflow
9. **Workflow Completion**: Completes all tasks and verifies workflow reaches SUCCEEDED status

### Prerequisites

1. **Docker**: Required for Testcontainers-Go
   - The test automatically starts PostgreSQL and Redis containers using Testcontainers
   - No manual setup of PostgreSQL or Redis is needed
   - Docker must be running on your machine

2. **Database Schema**: Migrations are automatically applied
   - The test automatically runs all migration files from the `migrations/` directory
   - No manual migration step is required

### Running the Test

#### Option 1: Run with Go test

```bash
# Ensure Docker is running
docker ps

# Run the integration test
go test -v ./tests -run TestEndToEndWorkflowExecution

# Run without short mode (required for integration tests)
go test -v ./tests -run TestEndToEndWorkflowExecution -short=false
```

#### Option 2: Run with Makefile

```bash
# Run integration tests
make test-integration
```

**Note**: The test uses Testcontainers-Go to automatically:
- Start PostgreSQL 16 container
- Start Redis 7 container
- Run database migrations
- Clean up containers after the test completes

No manual setup of services is required!

### Test Workflow

The test uses a simple 3-node workflow:

1. **fetch_data** (HTTP): Fetches data from httpbin.org
2. **process_data** (Python): Processes the data with inline Python code
3. **store_result** (HTTP): Posts the result to httpbin.org

### Notes

- The test simulates task execution rather than actually executing HTTP requests or Python code
- For a complete test with actual execution, you would need:
  - Outbox enqueuer running to push tasks to Redis
  - Worker process running to consume from Redis and execute tasks
  - Language workers (Python/JS) running for language tasks
  - Coordinator gRPC server running to receive task results

- The test cleans up after itself by deleting test data

### Troubleshooting

1. **Docker not running**: Ensure Docker daemon is running (`docker ps` should work)
2. **Container startup timeout**: If containers fail to start, check Docker resources and network connectivity
3. **Migration errors**: Check that migration files exist in the `migrations/` directory relative to the test
4. **No messages in stream**: The test will log a message if outbox enqueuer isn't running - this is expected for unit testing
5. **Testcontainers issues**: Ensure you have sufficient Docker resources allocated and network access for pulling images

