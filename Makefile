.PHONY: help build build-coordinator build-worker build-worker-js build-worker-py proto proto-go proto-py proto-js clean test docker-build docker-up docker-down install-deps run-local

# Variables
GO_CMD := go
PROTOC := protoc
PROTO_DIR := pkg/api/proto
PROTO_GO_OUT := pkg/api/proto/gen/go
PROTO_PY_OUT := cmd/language-server-py
PROTO_JS_OUT := cmd/language-server-js

# Go binaries
COORDINATOR_BIN := bin/coordinator
WORKER_BIN := bin/worker

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

build: proto-go build-coordinator build-worker build-language-server-js build-language-server-py ## Build all binaries (coordinator, worker, and language workers)

build-coordinator: ## Build coordinator binary
	@echo "Building coordinator..."
	@mkdir -p bin
	$(GO_CMD) build -o $(COORDINATOR_BIN) ./cmd/coordinator

build-worker: proto-go ## Build Go worker binary
	@echo "Building Go worker..."
	@mkdir -p bin
	$(GO_CMD) build -o $(WORKER_BIN) ./cmd/worker

build-language-server-js: proto-js ## Build JavaScript language worker
	@echo "Building JavaScript language worker..."
	@cd cmd/language-server-js && npm install --omit=dev
	@echo "JavaScript language server dependencies installed"

build-language-server-py: proto-py ## Build Python language worker
	@echo "Building Python language worker..."
	@cd cmd/language-server-py && uv sync
	@echo "Python language server dependencies installed"

proto: proto-go proto-py proto-js ## Generate all protobuf files

proto-go: ## Generate Go protobuf files
	@echo "Generating Go protobuf files..."
	@mkdir -p $(PROTO_GO_OUT)
	$(PROTOC) \
		--go_out=$(PROTO_GO_OUT) \
		--go_opt=paths=source_relative \
		--go-grpc_out=$(PROTO_GO_OUT) \
		--go-grpc_opt=paths=source_relative \
		--proto_path=$(PROTO_DIR) \
		$(PROTO_DIR)/workflow.proto \
		$(PROTO_DIR)/workflow_executor.proto
	@mkdir -p $(PROTO_GO_OUT)/workflow $(PROTO_GO_OUT)/workflow_exec
	@mv $(PROTO_GO_OUT)/workflow.pb.go $(PROTO_GO_OUT)/workflow/ 2>/dev/null || true
	@mv $(PROTO_GO_OUT)/workflow_grpc.pb.go $(PROTO_GO_OUT)/workflow/ 2>/dev/null || true
	@mv $(PROTO_GO_OUT)/workflow_executor.pb.go $(PROTO_GO_OUT)/workflow_exec/ 2>/dev/null || true
	@mv $(PROTO_GO_OUT)/workflow_executor_grpc.pb.go $(PROTO_GO_OUT)/workflow_exec/ 2>/dev/null || true

proto-py: ## Generate Python protobuf files
	@echo "Generating Python protobuf files..."
	@mkdir -p $(PROTO_PY_OUT)
	@cd $(PROTO_PY_OUT) && uv run python -m grpc_tools.protoc \
		-I../../$(PROTO_DIR) \
		--python_out=. \
		--grpc_python_out=. \
		../../$(PROTO_DIR)/workflow_executor.proto
	@echo "Python protobuf files generated in $(PROTO_PY_OUT)"

proto-js: ## Generate JavaScript protobuf files (runtime, no generation needed)
	@echo "JavaScript uses runtime proto loading, no generation needed"

install-deps: ## Install dependencies for all projects
	@echo "Installing Go dependencies..."
	$(GO_CMD) mod download
	@echo "Installing Node.js dependencies..."
	cd cmd/language-server-js && npm install
	@echo "Installing Python dependencies..."
	cd cmd/language-server-py && uv sync
	@echo "All dependencies installed"

clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -rf bin/
	rm -rf $(PROTO_GO_OUT)/*
	find $(PROTO_PY_OUT) -name "*_pb2.py" -delete
	find $(PROTO_PY_OUT) -name "*_pb2_grpc.py" -delete
	rm -rf cmd/language-server-js/node_modules
test: ## Run unit tests
	$(GO_CMD) test ./...

test-integration: ## Run integration tests (requires PostgreSQL and Redis)
	@echo "Running integration tests..."
	@echo "Ensure PostgreSQL and Redis are running"
	$(GO_CMD) test -v ./tests -run TestIntegrationWorkflowExecution -short=false

test-e2e: ## Run end-to-end tests (requires Docker for Testcontainers)
	@echo "Running end-to-end tests..."
	@echo "Ensure Docker is running"
	$(GO_CMD) test -v ./tests -run TestEndToEndWorkflowExecution -short=false

docker-build: ## Build Docker images
	@echo "Building Docker images..."
	docker-compose build

docker-up: ## Start all services with docker-compose
	@echo "Starting services..."
	docker-compose up -d

docker-down: ## Stop all services
	@echo "Stopping services..."
	docker-compose down

docker-logs: ## Show docker-compose logs
	docker-compose logs -f

run-local: ## Run the project locally using bootstrap script
	@echo "Running local bootstrap script..."
	@bash infra/scripts/bootstrap_local.sh POSTGRES_USER=root POSTGRES_PASSWORD=root POSTGRES_DB=composer REDIS_HOST=localhost REDIS_PORT=6379

