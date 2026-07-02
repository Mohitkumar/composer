# Composer - Distributed Workflow Orchestration Engine

## Introduction

Composer is a high-performance, distributed workflow orchestration engine designed to execute complex workflows with strong execution guarantees. Built with Go, it leverages PostgreSQL for persistent state management and Redis Streams for efficient task distribution. The system supports multi-language task execution through dedicated language servers for Python and JavaScript, enabling flexible workflow definitions that can combine different runtime environments.

## Purpose

Composer provides a robust platform for:

- **Workflow Orchestration**: Define and execute complex, multi-step workflows with dependencies, conditional logic, and parallel execution
- **Distributed Task Execution**: Scale horizontally with multiple workers consuming tasks from Redis streams
- **Multi-Language Support**: Execute tasks written in Python, JavaScript, and Go within the same workflow
- **Execution Guarantees**: Ensure at-least-once execution semantics through database-backed leases
- **Reliability**: Maintain workflow state and task history in PostgreSQL with ACID guarantees
- **Observability**: Built-in tracing and metrics for monitoring workflow execution

## When to Use Composer

Composer is optimised for **high-throughput, short-to-medium duration workflows** where you want minimal infrastructure overhead and sub-10ms orchestration latency.

### Good fit

- **API orchestration** — fan-out to multiple services, aggregate results, conditional routing (SWITCH), parallel branches (FORK)
- **Data pipelines** — fetch → transform → load chains where each step is an HTTP call or a Python/JS compute node
- **Event-driven automation** — trigger a multi-step workflow in response to a webhook or queue message; complete in seconds
- **Multi-language processing** — mix Python and JavaScript compute nodes with HTTP calls in one workflow definition
- **High-concurrency workloads** — many short workflows running simultaneously; Redis stream dispatch keeps per-step overhead at ~1–5 ms

### Not a good fit (yet)

- **Long-running / durable flows** — workflows that need to `sleep` for hours or days, or wait indefinitely for an external signal (human approval, payment webhook with no SLA). Composer has no timer or signal-wait node type today; a step has a 90-second hard ceiling.
- **Execution semantics** — Composer provides at-least-once delivery guarded by a database lease. Duplicate execution is prevented in practice but is not structurally impossible the way Temporal's event-sourced history makes it.
- **In-flight workflow versioning** — changing a workflow definition while runs are in progress is undefined. Temporal has built-in versioning APIs for this.

For a detailed comparison with Temporal — including throughput numbers, reliability trade-offs, and the specific gaps to close — see [docs/comparison-temporal.md](docs/comparison-temporal.md).

## How to Build

### Prerequisites

- **Go 1.21+** - Core runtime and coordinator/worker services
- **Node.js 20+** - JavaScript language server
- **Python 3.11+ with UV** - Python language server
- **PostgreSQL 16+** - Persistent state storage
- **Redis 7+** - Task queue and stream processing
- **Docker & Docker Compose** (optional) - For containerized deployment
- **Protocol Buffers Compiler** (`protoc`) - For generating gRPC code

### Build Steps

1. **Install Dependencies**:
   ```bash
   make install-deps
   ```
   This installs Go modules, Node.js packages, and Python dependencies.

2. **Generate Protocol Buffer Files**:
   ```bash
   make proto
   ```
   Generates gRPC code for Go and Python. JavaScript uses runtime proto loading.

3. **Build Binaries**:
   ```bash
   make build
   ```
   Builds the coordinator and worker binaries, and prepares language server dependencies.

### Build Targets

- `make build` - Build all Go binaries (coordinator, worker)
- `make build-coordinator` - Build coordinator binary only
- `make build-worker` - Build Go worker binary only
- `make build-language-server-js` - Install JavaScript language server dependencies
- `make build-language-server-py` - Install Python language server dependencies
- `make proto` - Generate all protobuf files
- `make proto-go` - Generate Go protobuf files
- `make proto-py` - Generate Python protobuf files
- `make proto-js` - Verify JavaScript proto setup (runtime loading)
- `make install-deps` - Install all dependencies
- `make clean` - Clean build artifacts
- `make test` - Run unit tests
- `make test-integration` - Run integration tests
- `make test-e2e` - Run end-to-end tests

## Running Locally

### Option 1: Docker Compose (Recommended)

The easiest way to run Composer is using Docker Compose, which starts all services including PostgreSQL, Redis, Coordinator, Workers, Language Servers, AI Service, and Web UI.

```bash
# Start all services
make docker-up
# Or:
cd infra/docker && docker-compose up -d

# View logs
make docker-logs
# Or:
cd infra/docker && docker-compose logs -f

# Stop services
make docker-down
# Or:
cd infra/docker && docker-compose down
```

**Services Started:**
- **PostgreSQL** (port 5432) - Database for workflow state
- **Redis** (port 6379) - Task queue and streams
- **Coordinator** (HTTP: 8080, gRPC: 50051) - Workflow management
- **Worker** (2 replicas) - Task execution
- **JavaScript Language Server** (port 50052) - Executes JS/Node.js tasks
- **Python Language Server** (port 50053) - Executes Python tasks
- **Web UI** (port 3000) - Dashboard for workflow management
- **Jaeger** (port 16686) - Distributed tracing UI
- **OpenTelemetry Collector** - Metrics and trace collection

### Option 2: Local Development

For local development without Docker, use the bootstrap script:

```bash
# Run the local bootstrap script
bash infra/scripts/bootstrap_local.sh
```

The script will:
1. Check prerequisites (Go, Node.js, Python/UV, PostgreSQL, Redis)
2. Build binaries and generate protobuf files
3. Start all services in the background
4. Display service endpoints and log file locations

## Web UI
Composer includes a modern web-based user interface for managing workflows, viewing execution history, and monitoring system health.

### Accessing the Web UI

#### With Docker Compose

When using Docker Compose, the web UI is automatically started and accessible at:

```
http://localhost:3000
```

#### Local Development

If running locally, you can start the web UI separately:

```bash
cd web
npm install
npm run dev
```

The UI will be available at `http://localhost:3000`.

## Workflow YAML Specification

Workflows in Composer are defined using YAML files with a simple, declarative syntax. A workflow consists of **nodes** (tasks) and **edges** (dependencies between tasks).

### Basic Structure

```yaml
nodes:
  - name: Node Name
    node_id: unique_node_id
    type: NODE_TYPE
    runtime: runtime_name  # Optional, for CODE nodes
    spec:
      # Node-specific configuration
edges:
  - from: source_node_id
    to: target_node_id
    condition: "{{expression}}"  # Optional, for conditional edges
```

### Node Types

#### 1. CODE Node

Executes code in a specified runtime (Python or JavaScript).

**Specification:**
```yaml
nodes:
  - name: Process Data
    node_id: process_data
    type: CODE
    runtime: python  # or "js" / "javascript"
    spec:
      code: |
        def handler(input):
            # Your code here
            return {"result": "processed"}
      input: "{{input}}"  # Optional: template expression
      timeout: 30  # Optional: timeout in seconds
      filename: handler.py  # Optional: filename hint
```

**Fields:**
- `code` (required): The code to execute. Must contain a `handler` function that accepts input and returns output
- `input` (optional): Template expression for input data (defaults to `{{input}}`)
- `timeout` (optional): Execution timeout in seconds (default: 30)
- `filename` (optional): Filename hint for error messages

**Supported Runtimes:**
- `python` / `py`: Python 3.11+
- `js` / `javascript` / `node`: Node.js 20+

#### 2. HTTP Node

Makes HTTP requests to external APIs.

**Specification:**
```yaml
nodes:
  - name: Fetch Data
    node_id: fetch_data
    type: HTTP
    spec:
      url: "https://api.example.com/data"
      method: GET  # GET, POST, PUT, DELETE, PATCH
      headers:
        Authorization: "Bearer {{tasks.get_token.output.token}}"
        Content-Type: "application/json"
      body: "{{tasks.prepare_request.output}}"  # Optional, for POST/PUT
      timeout_seconds: 30  # Optional
```

**Fields:**
- `url` (required): Target URL (supports template expressions)
- `method` (required): HTTP method
- `headers` (optional): HTTP headers (supports template expressions)
- `body` (optional): Request body (for POST/PUT/PATCH)
- `timeout_seconds` (optional): Request timeout (default: 30)

#### 3. SWITCH Node

Conditional branching based on expressions.

**Specification:**
```yaml
nodes:
  - name: Check Condition
    node_id: check_condition
    type: SWITCH
    spec: {}  # Empty spec, conditions are in edges
edges:
  - from: check_condition
    to: process_high
    condition: "{{tasks.check_condition.output.value >= 100}}"
  - from: check_condition
    to: process_low
    condition: "{{tasks.check_condition.output.value < 100}}"
```

**Note:** SWITCH nodes require conditional edges. The first matching condition determines the execution path.

#### 4. FORK Node

Splits execution into parallel branches.

**Specification:**
```yaml
nodes:
  - name: Fork Parallel Tasks
    node_id: fork_parallel
    type: FORK
    spec: {}  # Empty spec
edges:
  - from: fork_parallel
    to: task_a
  - from: fork_parallel
    to: task_b
  - from: fork_parallel
    to: task_c
```

**Note:** All branches execute in parallel. Use a JOIN node (automatic) to wait for all branches to complete.

### Template Expressions

Workflow YAML supports template expressions for dynamic values:

- **Input Data**: `{{input.field_name}}` - Access workflow input
- **Task Outputs**: `{{tasks.node_id.output.field_name}}` - Access previous task output
- **Configurations**: `{{config.config_name.field_name}}` - Access stored configurations
- **Context**: `{{context.field_name}}` - Access workflow context

**Examples:**
```yaml
# Use input data
url: "https://api.example.com/users/{{input.user_id}}"

# Use previous task output
headers:
  Authorization: "Bearer {{tasks.get_token.output.access_token}}"

# Use configuration
url: "{{config.api_endpoint.base_url}}/users"

# Complex expressions (CEL)
condition: "{{tasks.check_value.output.count >= 5 && tasks.check_value.output.status == 'active'}}"
```

### Edges

Edges define the flow between nodes:

```yaml
edges:
  # Simple edge
  - from: node_a
    to: node_b
  
  # Conditional edge (for SWITCH nodes)
  - from: check_condition
    to: branch_true
    condition: "{{tasks.check_condition.output.result == true}}"
  
  # Multiple edges from one node (parallel execution)
  - from: fork_node
    to: task_1
  - from: fork_node
    to: task_2
```

## Workflow YAML Examples

### Example 1: Simple Sequential Workflow

A basic workflow that processes data through multiple steps:

```yaml
nodes:
  - name: Fetch User Data
    node_id: fetch_user
    type: HTTP
    spec:
      url: "https://api.example.com/users/{{input.user_id}}"
      method: GET
      timeout_seconds: 30
  
  - name: Process User Data
    node_id: process_user
    type: CODE
    runtime: python
    spec:
      code: |
        def handler(input):
            user = input.get('user', {})
            return {
                "user_id": user.get("id"),
                "full_name": f"{user.get('first_name')} {user.get('last_name')}",
                "email": user.get("email"),
                "processed_at": "{{context.timestamp}}"
            }
      input: "{{tasks.fetch_user.output}}"
      timeout: 30
  
  - name: Save Result
    node_id: save_result
    type: HTTP
    spec:
      url: "https://api.example.com/results"
      method: POST
      headers:
        Content-Type: "application/json"
      body: "{{tasks.process_user.output}}"
      timeout_seconds: 30

edges:
  - from: fetch_user
    to: process_user
  - from: process_user
    to: save_result
```

### Example 2: Conditional Branching (SWITCH)

A workflow with conditional logic:

```yaml
nodes:
  - name: Get Order Value
    node_id: get_order_value
    type: CODE
    runtime: python
    spec:
      code: |
        def handler(input):
            order = input.get('order', {})
            return {
                "order_id": order.get("id"),
                "value": order.get("total", 0),
                "status": order.get("status")
            }
      input: "{{input}}"
      timeout: 30
  
  - name: Route Order
    node_id: route_order
    type: SWITCH
    spec: {}
  
  - name: Process High Value Order
    node_id: process_high_value
    type: CODE
    runtime: python
    spec:
      code: |
        def handler(input):
            return {
                "priority": "high",
                "message": "High value order requires special handling"
            }
      input: "{{tasks.get_order_value.output}}"
      timeout: 30
  
  - name: Process Standard Order
    node_id: process_standard
    type: CODE
    runtime: python
    spec:
      code: |
        def handler(input):
            return {
                "priority": "standard",
                "message": "Standard order processing"
            }
      input: "{{tasks.get_order_value.output}}"
      timeout: 30

edges:
  - from: get_order_value
    to: route_order
  - from: route_order
    to: process_high_value
    condition: "{{tasks.get_order_value.output.value >= 1000}}"
  - from: route_order
    to: process_standard
    condition: "{{tasks.get_order_value.output.value < 1000}}"
```

### Example 3: Parallel Execution (FORK)

A workflow that executes multiple tasks in parallel:

```yaml
nodes:
  - name: Prepare Data
    node_id: prepare_data
    type: CODE
    runtime: python
    spec:
      code: |
        def handler(input):
            return {
                "value1": 10,
                "value2": 20,
                "value3": 30
            }
      input: "{{input}}"
      timeout: 30
  
  - name: Fork Parallel Tasks
    node_id: fork_parallel
    type: FORK
    spec: {}
  
  - name: Calculate Sum
    node_id: calc_sum
    type: CODE
    runtime: python
    spec:
      code: |
        def handler(input):
            data = input
            result = data.get("value1", 0) + data.get("value2", 0)
            return {"operation": "sum", "result": result}
      input: "{{tasks.prepare_data.output}}"
      timeout: 30
  
  - name: Calculate Product
    node_id: calc_product
    type: CODE
    runtime: python
    spec:
      code: |
        def handler(input):
            data = input
            result = data.get("value1", 0) * data.get("value2", 0)
            return {"operation": "product", "result": result}
      input: "{{tasks.prepare_data.output}}"
      timeout: 30
  
  - name: Aggregate Results
    node_id: aggregate_results
    type: CODE
    runtime: python
    spec:
      code: |
        def handler(input):
            # This receives aggregated context from all parallel branches
            return {
                "message": "All parallel tasks completed",
                "results": input
            }
      input: "{{context}}"
      timeout: 30

edges:
  - from: prepare_data
    to: fork_parallel
  - from: fork_parallel
    to: calc_sum
  - from: fork_parallel
    to: calc_product
  - from: calc_sum
    to: aggregate_results
  - from: calc_product
    to: aggregate_results
```

### Example 4: Multi-Language Workflow

A workflow combining Python and JavaScript nodes:

```yaml
nodes:
  - name: Process with Python
    node_id: process_python
    type: CODE
    runtime: python
    spec:
      code: |
        def handler(input):
            import json
            data = json.loads(input) if isinstance(input, str) else input
            return {
                "processed_by": "python",
                "result": data.get("value", 0) * 2
            }
      input: "{{input}}"
      timeout: 30
  
  - name: Transform with JavaScript
    node_id: transform_js
    type: CODE
    runtime: js
    spec:
      code: |
        function handler(input) {
            const data = typeof input === 'string' ? JSON.parse(input) : input;
            return {
                processed_by: "javascript",
                original: data.result,
                transformed: data.result * 10
            };
        }
      input: "{{tasks.process_python.output}}"
      timeout: 30
  
  - name: Final Processing
    node_id: final_process
    type: CODE
    runtime: python
    spec:
      code: |
        def handler(input):
            data = input if not isinstance(input, str) else json.loads(input)
            return {
                "final_result": data.get("transformed", 0),
                "pipeline": ["python", "javascript", "python"]
            }
      input: "{{tasks.transform_js.output}}"
      timeout: 30

edges:
  - from: process_python
    to: transform_js
  - from: transform_js
    to: final_process
```

### Example 5: Complex Workflow with HTTP and Code

A real-world example combining HTTP calls and data processing:

```yaml
nodes:
  - name: Get Auth Token
    node_id: get_auth_token
    type: HTTP
    spec:
      url: "{{config.api.auth_url}}"
      method: POST
      headers:
        Content-Type: "application/x-www-form-urlencoded"
      body: "grant_type=client_credentials&client_id={{config.api.client_id}}&client_secret={{config.api.client_secret}}"
      timeout_seconds: 30
  
  - name: Fetch Data
    node_id: fetch_data
    type: HTTP
    spec:
      url: "{{config.api.data_url}}/items"
      method: GET
      headers:
        Authorization: "Bearer {{tasks.get_auth_token.output.access_token}}"
        Content-Type: "application/json"
      timeout_seconds: 60
  
  - name: Transform Data
    node_id: transform_data
    type: CODE
    runtime: python
    spec:
      code: |
        def handler(input):
            import json
            data = json.loads(input) if isinstance(input, str) else input
            items = data.get("items", [])
            
            transformed = []
            for item in items:
                transformed.append({
                    "id": item.get("id"),
                    "name": item.get("name", "").upper(),
                    "price": float(item.get("price", 0)) * 1.1,  # Add 10% markup
                    "category": item.get("category", "unknown")
                })
            
            return {
                "items": transformed,
                "count": len(transformed),
                "total_value": sum(i["price"] for i in transformed)
            }
      input: "{{tasks.fetch_data.output}}"
      timeout: 60
  
  - name: Save Results
    node_id: save_results
    type: HTTP
    spec:
      url: "{{config.api.results_url}}"
      method: POST
      headers:
        Authorization: "Bearer {{tasks.get_auth_token.output.access_token}}"
        Content-Type: "application/json"
      body: "{{tasks.transform_data.output}}"
      timeout_seconds: 30

edges:
  - from: get_auth_token
    to: fetch_data
  - from: fetch_data
    to: transform_data
  - from: transform_data
    to: save_results
```

### Creating and Running Workflows

```bash
# Create workflow
curl -X POST http://localhost:8080/api/v1/workflows \
  -H "Content-Type: application/json" \
  -d '{
    "workflow_id": "my-workflow",
    "name": "My Workflow",
    "version": 1,
    "spec": "<yaml_content_as_string>"
  }'

# Execute workflow
curl -X POST http://localhost:8080/api/v1/workflows/my-workflow/execute \
  -H "Content-Type: application/json" \
  -d '{
    "input": {
      "user_id": "123",
      "data": "example"
    }
  }'
```


## Architecture

### Overview

Composer follows a distributed architecture with clear separation of concerns:

```
┌─────────────┐      ┌──────────────┐      ┌─────────────┐
│  Coordinator│──────│  PostgreSQL  │      │    Redis    │
│  (gRPC/HTTP)│      │   (State)    │      │  (Streams)  │
└──────┬──────┘      └──────────────┘      └──────┬──────┘
       │                                          │
       │ Task Dispatch (Outbox Pattern)           │
       │                                          │
       ▼                                          ▼
┌─────────────────────────────────────────────────────┐
│                    Workers                          │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐           │
│  │ Go Worker│  │ Go Worker│  │ Go Worker│           │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘           │
│       │             │             │                 │
│       └─────────────┴─────────────┘                 │
│                    │                                │
│                    ▼                                │
│       ┌────────────────────────┐                    │
│       │  Language Servers      │                    │
│       │  (Python, JavaScript)  │                    │
│       └────────────────────────┘                    │
└─────────────────────────────────────────────────────┘
```

### Workflow Execution Sequence

The following sequence diagram illustrates the complete flow of a workflow execution from initiation to completion:

```
┌─────────┐  ┌──────────────┐  ┌──────────┐   ┌─────────────┐  ┌──────────────┐  ┌──────────────┐
│ Client  │  │ Coordinator  │  │PostgreSQL│   │    Redis    │  │   Worker     │  │Lang Server   │
│/API/UI  │  │              │  │          │   │  (Streams)  │  │              │  │ (Python/JS)  │
└────┬────┘  └──────┬───────┘  └────┬─────┘   └──────┬──────┘  └──────┬───────┘  └──────┬───────┘
     │              │               │                │                │                 │
     │ 1. Execute   │               │                │                │                 │
     │ Workflow     │               │                │                │                 │
     ├─────────────>│               │                │                │                 │
     │              │               │                │                │                 │
     │              │ 2. Create     │                │                │                 │
     │              │ Workflow Run  │                │                │                 │
     │              ├──────────────>│                │                │                 │
     │              │               │                │                │                 │
     │              │ 3. Create     │                │                │                 │
     │              │ Task Instances│                │                │                 │
     │              │ (PENDING)     │                │                │                 │
     │              ├──────────────>│                │                │                 │
     │              │               │                │                │                 │
     │              │ 4. Insert into│                │                │                 │
     │              │ Outbox Table  │                │                │                 │
     │              │ (same tx)     │                │                │                 │
     │              ├──────────────>│                │                │                 │
     │              │               │                │                │                 │
     │              │ 5. Commit     │                │                │                 │
     │              │ Transaction   │                │                │                 │
     │              ├──────────────>│                │                │                 │
     │              │               │                │                │                 │
     │              │               │                │                │                 │
     │              │ 6. Outbox     │                │                │                 │
     │              │ Enqueuer      │                │                │                 │
     │              │ processes     │                │                │                 │
     │              │ outbox rows   │                │                │                 │
     │              │               │                │                │                 │
     │              │ 7. Publish to │                │                │                 │
     │              │ Redis Stream  │                │                │                 │
     │              ├───────────────────────────────>│                │                 │
     │              │               │                │                │                 │
     │              │               │                │                │                 │
     │              │               │                │ 8. XReadGroup  │                 │
     │              │               │                │ (consume msg)  │                 │
     │              │               │                │<───────────────┤                 │
     │              │               │                │                │                 │
     │              │               │                │                │                 │
     │              │ 9. Acquire    │                │                │                 │
     │              │ Task Lease    │                │                │                 │
     │              │ (gRPC)        │                │                │                 │
     │              │<──────────────┤                │                │                 │
     │              │               │                │                │                 │
     │              │ 10. Atomic    │                │                │                 │
     │              │ UPDATE lease  │                │                │                 │
     │              │ (PENDING→RUNNING)              │                │                 │
     │              ├──────────────>│                │                │                 │
     │              │               │                │                │                 │
     │              │ 11. Lease     │                │                │                 │
     │              │ Acquired      │                │                │                 │
     │              ├───────────────┤                │                │                 │
     │              │               │                │                │                 │
     │              │               │                │                │                 │
     │              │               │                │ 12. Execute    │                 │
     │              │               │                │ Task           │                 │
     │              │               │                │                │                 │
     │              │               │                │                │                 │
     │              │               │                │ 13. If CODE    │                 │
     │              │               │                │ node, call     │                 │
     │              │               │                │ Language Server│                 │
     │              │               │                ├─────────────────────────────────>│
     │              │               │                │                │                 │
     │              │               │                │                │ 14. Execute Code│
     │              │               │                │                │ (with timeout)  │
     │              │               │                │                │                 │
     │              │               │                │                │ 15. Return      │
     │              │               │                │                │ Result          │
     │              │               │                │<─────────────────────────────────┤
     │              │               │                │                │                 │
     │              │               │                │                │                 │
     │              │ 16. Report    │                │                │                 │
     │              │ Task Result   │                │                │                 │
     │              │ (gRPC)        │                │                │                 │
     │              │<──────────────┤                │                │                 │
     │              │               │                │                │                 │
     │              │ 17. Update    │                │                │                 │
     │              │ Task State    │                │                │                 │
     │              │ (COMPLETED)   │                │                │                 │
     │              ├──────────────>│                │                │                 │
     │              │               │                │                │                 │
     │              │ 18. Clear     │                │                │                 │
     │              │ Lease         │                │                │                 │
     │              ├──────────────>│                │                │                 │
     │              │               │                │                │                 │
     │              │ 19. Evaluate  │                │                │                 │
     │              │ Next Nodes    │                │                │                 │
     │              │ (DAG traversal)                │                │                 │
     │              │               │                │                │                 │
     │              │ 20. Create    │                │                │                 │
     │              │ Next Tasks    │                │                │                 │
     │              │ (if any)      │                │                │                 │
     │              ├──────────────>│                │                │                 │
     │              │               │                │                │                 │
     │              │ 21. Repeat    │                │                │                 │
     │              │ steps 4-20    │                │                │                 │
     │              │ for next tasks│                │                │                 │
     │              │               │                │                │                 │
     │              │ 22. Mark      │                │                │                 │
     │              │ Workflow      │                │                │                 │
     │              │ COMPLETED     │                │                │                 │
     │              ├──────────────>│                │                │                 │
     │              │               │                │                │                 │
     │ 23. Return   │               │                │                │                 │
     │ Run ID       │               │                │                │                 │
     │<─────────────┤               │                │                │                 │
     │              │               │                │                │                 │
     │              │               │                │ 24. XAck       │                 │
     │              │               │                │ (ack message)  │                 │
     │              │               │                │<───────────────┤                 │
     │              │               │                │                │                 │
```

**Key Steps Explained:**

1. **Workflow Initiation**: Client (API/UI) sends execution request to Coordinator
2. **State Creation**: Coordinator creates workflow run and task instances in PostgreSQL within a transaction
3. **Outbox Pattern**: Task messages are inserted into the outbox table in the same transaction
4. **Message Publishing**: Background outbox enqueuer publishes messages to Redis Streams
5. **Task Consumption**: Workers consume messages from Redis Streams using consumer groups
6. **Lease Acquisition**: Worker requests lease from Coordinator via gRPC to prevent duplicate execution
7. **Atomic Lease Update**: Coordinator atomically updates task state from PENDING to RUNNING
8. **Task Execution**: Worker executes the task (HTTP or CODE)
9. **Language Server Call**: For CODE nodes, worker calls appropriate language server (Python/JavaScript) via gRPC
10. **Result Reporting**: Worker reports execution result back to Coordinator
11. **State Update**: Coordinator updates task state to COMPLETED and clears lease
12. **Next Node Evaluation**: Coordinator evaluates DAG to determine next nodes to execute
13. **Acknowledgment**: Worker acknowledges Redis message after successful execution

**Error Handling:**
- If lease acquisition fails, worker leaves message in Redis for retry
- If task execution fails, error is reported and workflow may be marked FAILED
- Expired leases allow other workers to retry tasks
- Outbox retry logic handles transient Redis failures

### Why PostgreSQL?

PostgreSQL is chosen as the primary data store for several critical reasons:
#### 1. **Outbox Pattern Support**
The `outbox` table leverages PostgreSQL features:
- **FOR UPDATE SKIP LOCKED**: Allows multiple workers to process outbox rows concurrently without blocking
- **Transactional Guarantees**: Outbox rows are created in the same transaction as workflow state changes
- **Retry Logic**: Built-in retry scheduling with exponential backoff stored in the database

#### 2. **Lease Management**
Task leases use PostgreSQL's atomic updates:
- **Atomic Lease Acquisition**: `UPDATE ... WHERE state IN ('PENDING','QUEUED')` ensures only one worker can claim a task
- **Lease Expiration**: `lease_expires_at` timestamps with automatic cleanup queries
- **Concurrent Safety**: Row-level locking prevents double-execution

### Why Redis?

Redis Streams provide the task distribution mechanism:

#### 1. **Redis Streams for Task Queue**
- **Ordered Messages**: Streams maintain message order, critical for workflow dependencies
- **Consumer Groups**: Enable load balancing across multiple workers
- **Message Acknowledgment**: Workers acknowledge messages after processing, enabling retry on failure
- **Pending Entry List**: Tracks messages claimed but not yet acknowledged

### Execution Guarantees

Composer provides strong execution guarantees through a combination of database transactions and lease mechanisms:

#### 1. **Lease-Based Execution**

**Lease Acquisition Flow:**
1. Worker reads a task message from Redis Stream
2. Worker calls coordinator's `AcquireTaskLease` gRPC method
3. Coordinator atomically updates `task_instances`:
   ```sql
   UPDATE task_instances 
   SET state='RUNNING', 
       lease_owner=$1, 
       lease_expires_at=NOW() + $2::interval,
       started_at=NOW()
   WHERE id=$3 AND state IN ('PENDING','QUEUED')
   RETURNING id
   ```
4. If the UPDATE returns a row, the lease is acquired; otherwise, another worker already claimed it
5. Worker executes the task only if lease acquisition succeeds

**Lease Properties:**
- **Atomic**: Only one worker can acquire a lease for a given task
- **Time-Bounded**: Leases expire after a configurable TTL (default: 90 seconds)
- **Idempotent**: Multiple workers can attempt to acquire the same lease; only one succeeds
- **Recoverable**: Expired leases can be reclaimed by other workers

#### 2. **Outbox Pattern for Task Dispatch**

The outbox pattern ensures that task creation and message publishing are atomic:

1. **Task Creation**: Coordinator creates a task instance in `task_instances` and an entry in `outbox` within the same transaction
2. **Message Publishing**: A background process (`OutboxEnqueuer`) reads from `outbox` and publishes to Redis Streams
3. **Guarantees**:
   - If the transaction commits, the task is guaranteed to be published (eventually)
   - If the transaction rolls back, no message is published
   - Retry logic handles transient Redis failures

**Outbox Processing:**
- Uses `FOR UPDATE SKIP LOCKED` to allow concurrent processing
- Marks rows as `IN_FLIGHT` during processing
- Updates status to `SENT` on success, `RETRY` on failure
- Moves to `outbox_dlq` after max retries

#### 3. **At-Least-Once Semantics**

For non-critical tasks:
- **Redis Streams**: Messages are redelivered if not acknowledged
- **Lease Expiration**: Expired leases allow retry by other workers
- **Error Handling**: Failed tasks can be retried with exponential backoff

### Worker Communication via gRPC

Workers communicate with the coordinator and language servers using gRPC for efficient, type-safe communication:

#### 1. **Coordinator-Worker Communication**

**gRPC Service: `WorkflowService`**

**Methods:**
- **`AcquireTaskLease`**: Worker requests to acquire a lease before executing a task
  ```protobuf
  rpc AcquireTaskLease(AcquireTaskLeaseRequest) returns (AcquireTaskLeaseResponse);
  ```
  - Request includes: `task_id`, `owner` (worker ID), `ttl_seconds`
  - Response indicates whether lease was acquired

- **`ReportTaskResult`**: Worker reports task execution completion
  ```protobuf
  rpc ReportTaskResult(ReportTaskResultRequest) returns (ReportTaskResultResponse);
  ```
  - Request includes: `task_id`, `run_id`, `node_id`, `output` (JSON), `error` (optional)
  - Coordinator updates task state and triggers next nodes in workflow


**Protocol:**
- **Transport**: gRPC over TCP (default port: 50051)
- **Serialization**: Protocol Buffers (efficient binary format)
- **Connection**: Persistent connections with connection pooling
- **Timeout**: 5-second timeout for lease acquisition

#### 2. **Worker-Language Server Communication**

**gRPC Service: `LanguageExecutor`**

**Methods:**
- **`Execute`**: Worker requests code execution on a language server
  ```protobuf
  rpc Execute(ExecuteRequest) returns (ExecuteResponse);
  ```
  - Request includes: `task_id`, `runtime` ("python" or "js"), `inline_code`, `input_json`, `timeout_seconds`
  - Response includes: `success`, `output_json`, `error`, `duration_ms`, `logs`

**Protocol:**
- **Transport**: gRPC over TCP
- **Ports**: JavaScript (50052), Python (50053)
- **Timeout**: Configurable per request (default: 30 seconds)
- **Error Handling**: Language servers return structured error responses

**Worker Routing:**
- Go Worker maintains a `LangClientManager` that routes requests to appropriate language servers
- Language server addresses are configured via `LANG_WORKER_ADDRS` environment variable
- Format: `runtime:host:port,runtime:host:port` (e.g., `js:localhost:50052,python:localhost:50053`)

### Language Servers

Composer supports executing tasks in multiple languages through dedicated language servers:

#### 1. **Python Language Server**

**Location**: `cmd/language-server-py/`

**Features:**
- Executes Python 3.11+ code in isolated sandboxes
- Supports inline code execution with timeout protection
- Handles JSON input/output serialization
- Error handling for syntax errors, import errors, and runtime exceptions
- Logging to `logs/language-server-py.log`

**Execution Flow:**
1. Receives gRPC `Execute` request with Python code
2. Parses input JSON into Python objects
3. Executes code in a sandboxed environment
4. Calls the `handler` function with input data
5. Serializes output to JSON
6. Returns response with success/error status

**Configuration:**
- `PORT`: gRPC server port (default: 50051)
- `DEFAULT_TIMEOUT_SECONDS`: Execution timeout (default: 30)

**Dependencies:**
- Python 3.11+
- UV package manager
- `grpcio`, `grpcio-tools` for gRPC

#### 2. **JavaScript Language Server**

**Location**: `cmd/language-server-js/`

**Features:**
- Executes Node.js/JavaScript code
- Supports ES6+ syntax and Node.js built-in modules
- Runtime proto loading (no code generation needed)
- JSON input/output handling
- Error formatting and timeout protection

**Execution Flow:**
1. Receives gRPC `Execute` request with JavaScript code
2. Parses input JSON
3. Executes code using Node.js `vm` module (sandboxed)
4. Calls the `handler` function with input
5. Serializes output to JSON
6. Returns response

**Configuration:**
- `PORT`: gRPC server port (default: 50052)

**Dependencies:**
- Node.js 20+
- `@grpc/grpc-js`, `@grpc/proto-loader` for gRPC

#### 3. **Language Server Architecture**

**Common Patterns:**
- **Sandboxing**: Code execution is isolated to prevent system access
- **Timeout Protection**: All executions have configurable timeouts
- **Error Handling**: Structured error responses with stack traces
- **Logging**: Comprehensive logging for debugging
- **Stateless**: Language servers are stateless; each request is independent

**Integration:**
- Language servers are started independently and registered with Go Workers
- Go Workers route tasks to appropriate language servers based on `node_type`
- Language servers can be scaled independently based on workload

## Configuration

### Coordinator

Environment variables or command-line flags:

- `STORE_URL` / `--store.url` - PostgreSQL connection URL
- `REDIS_ADDRS` / `--redis.addrs` - Redis addresses (comma-separated)
- `API_HTTP_PORT` / `--api.http_port` - HTTP API port (default: 8080)
- `API_GRPC_PORT` / `--api.grpc_port` - gRPC API port (default: 50051)
- `LOG_LEVEL` / `--log.level` - Log level (debug, info, warn, error)
- `INTELLIGENCE_AGENT_URL` / `--intelligence.agent_url` - AI service URL (optional)

### Worker

Environment variables:

- `REDIS_ADDR` - Redis address (host:port)
- `COORDINATOR_ADDR` - Coordinator gRPC address (host:port)
- `LANG_WORKER_ADDRS` - Language worker addresses (format: `runtime:host:port,runtime:host:port`)
- `STREAM_NAME` - Redis stream name (default: `workflow.tasks`)
- `CONSUMER_GROUP` - Consumer group name (default: `workflow-workers`)
- `WORKER_ID` - Unique worker identifier
- `LEASE_TTL_SECONDS` - Task lease TTL in seconds (default: 90)

### Language Servers

- **JavaScript**: `PORT` - gRPC server port (default: 50052)
- **Python**: `PORT` - gRPC server port (default: 50053), `DEFAULT_TIMEOUT_SECONDS` - Execution timeout

## Development

### Generate Protobuf Files

```bash
# Generate all protobuf files
make proto

# Generate for specific language
make proto-go    # Go
make proto-py    # Python
make proto-js    # JavaScript (verification only, uses runtime loading)
```

### Running Tests

```bash
# Unit tests
make test

# Integration tests (requires PostgreSQL and Redis)
make test-integration

# End-to-end tests (requires Docker for Testcontainers)
make test-e2e
```
