# Composer - Workflow Engine

A distributed workflow orchestration engine built with Go, using PostgreSQL and Redis Streams.

## Architecture

- **Coordinator**: Manages workflow definitions, instances, and task dispatching
- **Worker**: Consumes tasks from Redis streams and executes them
- **Language Workers**: Execute Python and JavaScript code via gRPC
- **Outbox Pattern**: Ensures atomic task dispatching to Redis streams

## Quick Start

### Prerequisites

- Go 1.21+
- Node.js 20+ (for JS worker)
- Python 3.11+ with UV (for Python worker)
- PostgreSQL 16+
- Redis 7+
- Docker & Docker Compose (optional)

### Build

```bash
# Install dependencies
make install-deps

# Generate protobuf files
make proto

# Build binaries
make build
```

### Run with Docker Compose

```bash
# Start all services
make docker-up

# View logs
make docker-logs

# Stop services
make docker-down
```

### Run Locally

1. **Start PostgreSQL and Redis**:
   ```bash
   docker-compose up -d postgres redis
   ```

2. **Run migrations** (if needed):
   ```bash
   # Apply migrations manually or via coordinator
   ```

3. **Start Coordinator**:
   ```bash
   make dev-coordinator
   # Or:
   ./bin/coordinator --store.url="postgres://postgres:password@localhost:5432/composer?sslmode=disable"
   ```

4. **Start Worker**:
   ```bash
   make dev-worker
   # Or:
   REDIS_ADDR=localhost:6379 COORDINATOR_ADDR=localhost:50051 ./bin/worker
   ```

5. **Start Language Workers** (optional):
   ```bash
   # JavaScript worker
   make dev-worker-js
   
   # Python worker
   make dev-worker-py
   ```

## Makefile Targets

- `make build` - Build all Go binaries
- `make proto` - Generate all protobuf files
- `make proto-go` - Generate Go protobuf files
- `make proto-py` - Generate Python protobuf files
- `make install-deps` - Install all dependencies
- `make clean` - Clean build artifacts
- `make docker-build` - Build Docker images
- `make docker-up` - Start all services
- `make docker-down` - Stop all services
- `make help` - Show all available targets

## Project Structure

```
.
├── cmd/
│   ├── coordinator/     # Coordinator main application
│   ├── worker-go/        # Go worker main
│   ├── worker-js/         # JavaScript language worker
│   └── worker-py/         # Python language worker
├── pkg/
│   ├── api/              # HTTP and gRPC APIs
│   ├── coord/            # Coordinator logic
│   ├── model/            # Data models
│   ├── outbox/           # Outbox pattern implementation
│   ├── redisq/           # Redis abstraction
│   └── store/            # Database store
├── worker/               # Worker package
│   ├── executors/        # Task executors (HTTP, S3, CSV)
│   ├── router.go         # Task router
│   └── worker.go         # Main worker logic
└── migrations/           # Database migrations
```

## Configuration

### Coordinator

Environment variables or flags:
- `STORE_URL` - PostgreSQL connection URL
- `REDIS_ADDRS` - Redis addresses (comma-separated)
- `API_HTTP_PORT` - HTTP API port (default: 8080)
- `API_GRPC_PORT` - gRPC API port (default: 50051)
- `LOG_LEVEL` - Log level (debug, info, warn, error)

### Worker

Environment variables:
- `REDIS_ADDR` - Redis address
- `COORDINATOR_ADDR` - Coordinator gRPC address
- `LANG_WORKER_ADDR` - Language worker address (optional)
- `STREAM_NAME` - Redis stream name (default: workflow.tasks)
- `CONSUMER_GROUP` - Consumer group name (default: workflow-workers)
- `WORKER_ID` - Unique worker identifier

### Language Workers

- **JavaScript**: `PORT` - gRPC server port (default: 50052)
- **Python**: `PORT` - gRPC server port (default: 50051)

## Development

### Generate Protobuf Files

```bash
# All languages
make proto

# Specific language
make proto-go
make proto-py
```

### Running Tests

```bash
make test
```

## License

MIT
