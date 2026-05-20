#!/bin/bash
# Bootstrap script for local development environment
# This script sets up and runs Composer services locally (not in Docker/K8s)

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

echo -e "${GREEN}=== Composer Local Development Bootstrap ===${NC}\n"

# Default values
POSTGRES_HOST="${POSTGRES_HOST:-localhost}"
POSTGRES_PORT="${POSTGRES_PORT:-5432}"
POSTGRES_USER="${POSTGRES_USER:-postgres}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-password}"
POSTGRES_DB="${POSTGRES_DB:-composers}"
REDIS_HOST="${REDIS_HOST:-localhost}"
REDIS_PORT="${REDIS_PORT:-6379}"
COORDINATOR_HTTP_PORT="${COORDINATOR_HTTP_PORT:-8080}"
COORDINATOR_GRPC_PORT="${COORDINATOR_GRPC_PORT:-50051}"
WORKER_JS_PORT="${WORKER_JS_PORT:-50052}"
WORKER_PY_PORT="${WORKER_PY_PORT:-50053}"
AI_SERVICE_PORT="${AI_SERVICE_PORT:-8000}"
INTELLIGENCE_AGENT_URL="${INTELLIGENCE_AGENT_URL:-}"
GOOGLE_API_KEY="${GOOGLE_API_KEY:-}"

# Check prerequisites
echo -e "${YELLOW}Checking prerequisites...${NC}"

# Check Go
if ! command -v go &> /dev/null; then
    echo -e "${RED}Error: Go is not installed. Please install Go 1.21+ first.${NC}"
    exit 1
fi
GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
echo -e "${GREEN}✓ Go ${GO_VERSION} found${NC}"

# Check Node.js (for JS worker)
if ! command -v node &> /dev/null; then
    echo -e "${YELLOW}⚠ Node.js not found. JS worker will be skipped.${NC}"
    SKIP_JS_WORKER=true
else
    NODE_VERSION=$(node --version)
    echo -e "${GREEN}✓ Node.js ${NODE_VERSION} found${NC}"
    SKIP_JS_WORKER=false
fi

# Check Python/UV (for Python worker and AI service)
if ! command -v uv &> /dev/null; then
    echo -e "${YELLOW}⚠ UV not found. Python worker and AI service will be skipped.${NC}"
    SKIP_PY_WORKER=true
    SKIP_AI_SERVICE=true
else
    UV_VERSION=$(uv --version | awk '{print $2}')
    echo -e "${GREEN}✓ UV ${UV_VERSION} found${NC}"
    SKIP_PY_WORKER=false
    SKIP_AI_SERVICE=false
fi

# Check PostgreSQL connection
echo -n "Checking PostgreSQL connection..."
if command -v psql &> /dev/null; then
    export PGPASSWORD="$POSTGRES_PASSWORD"
    if psql -h "$POSTGRES_HOST" -p "$POSTGRES_PORT" -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT 1;" &> /dev/null; then
        echo -e " ${GREEN}✓${NC}"
    else
        echo -e " ${RED}✗${NC}"
        echo -e "${YELLOW}Warning: Cannot connect to PostgreSQL.${NC}"
        echo -e "${YELLOW}Please ensure PostgreSQL is running and accessible at ${POSTGRES_HOST}:${POSTGRES_PORT}${NC}"
        echo -e "${YELLOW}You can start it with: docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=password -e POSTGRES_DB=composer postgres:16-alpine${NC}"
        read -p "Continue anyway? (y/N): " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            exit 1
        fi
    fi
    unset PGPASSWORD
else
    echo -e " ${YELLOW}⚠ psql not found, skipping connection test${NC}"
fi

# Check Redis connection
echo -n "Checking Redis connection..."
if command -v redis-cli &> /dev/null; then
    if redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" ping &> /dev/null; then
        echo -e " ${GREEN}✓${NC}"
    else
        echo -e " ${RED}✗${NC}"
        echo -e "${YELLOW}Warning: Cannot connect to Redis.${NC}"
        echo -e "${YELLOW}Please ensure Redis is running and accessible at ${REDIS_HOST}:${REDIS_PORT}${NC}"
        echo -e "${YELLOW}You can start it with: docker run -d -p 6379:6379 redis:7-alpine${NC}"
        read -p "Continue anyway? (y/N): " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            exit 1
        fi
    fi
else
    echo -e " ${YELLOW}⚠ redis-cli not found, skipping connection test${NC}"
fi

echo -e "${GREEN}✓ Prerequisites check passed${NC}\n"

# Navigate to project root
cd "$PROJECT_ROOT"

# Build binaries
echo -e "${YELLOW}Building binaries...${NC}"
if ! make build-coordinator build-worker; then
    echo -e "${RED}Error: Failed to build binaries${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Binaries built${NC}\n"

# Generate protobuf files
echo -e "${YELLOW}Generating protobuf files...${NC}"
if ! make proto; then
    echo -e "${RED}Error: Failed to generate protobuf files${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Protobuf files generated${NC}\n"

# Setup language workers
if [ "$SKIP_JS_WORKER" = false ]; then
    echo -e "${YELLOW}Setting up JavaScript worker...${NC}"
    cd "$PROJECT_ROOT/cmd/language-server-js"
    if [ ! -d "node_modules" ]; then
        npm install
    fi
    echo -e "${GREEN}✓ JavaScript worker ready${NC}\n"
    cd "$PROJECT_ROOT"
fi

if [ "$SKIP_PY_WORKER" = false ]; then
    echo -e "${YELLOW}Setting up Python worker...${NC}"
    cd "$PROJECT_ROOT/cmd/language-server-py"
    if [ ! -d ".venv" ]; then
        uv sync
    fi
    echo -e "${GREEN}✓ Python worker ready${NC}\n"
    cd "$PROJECT_ROOT"
fi

# Setup AI service
if [ "$SKIP_AI_SERVICE" = false ]; then
    echo -e "${YELLOW}Setting up AI service...${NC}"
    cd "$PROJECT_ROOT/ai"
    if [ ! -d ".venv" ]; then
        uv sync
    fi
    echo -e "${GREEN}✓ AI service ready${NC}\n"
    cd "$PROJECT_ROOT"
fi

# Create logs directory
mkdir -p "$PROJECT_ROOT/logs"

# Build connection strings
STORE_URL="postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@${POSTGRES_HOST}:${POSTGRES_PORT}/${POSTGRES_DB}?sslmode=disable"
REDIS_ADDR="${REDIS_HOST}:${REDIS_PORT}"
COORDINATOR_ADDR="localhost:${COORDINATOR_GRPC_PORT}"

# Function to cleanup on exit
cleanup() {
    echo -e "\n${YELLOW}Shutting down services...${NC}"
    jobs -p | xargs -r kill 2>/dev/null || true
    wait 2>/dev/null || true
    echo -e "${GREEN}✓ Services stopped${NC}"
}

trap cleanup EXIT INT TERM

# Start Coordinator
echo -e "${BLUE}Starting Coordinator...${NC}"
COORDINATOR_ARGS=(
    --store.url="$STORE_URL"
    --redis.addrs="$REDIS_ADDR"
    --api.http_port="$COORDINATOR_HTTP_PORT"
    --api.grpc_port="$COORDINATOR_GRPC_PORT"
    --disable-observability
)

# Add intelligence agent URL if provided (default to AI service if running)
if [ -n "$INTELLIGENCE_AGENT_URL" ]; then
    COORDINATOR_ARGS+=(--intelligence.agent_url="$INTELLIGENCE_AGENT_URL")
    echo -e "${GREEN}✓ Intelligence agent configured: $INTELLIGENCE_AGENT_URL${NC}"
elif [ "$SKIP_AI_SERVICE" = false ]; then
    # Auto-configure AI service URL if AI service is available
    # Note: AI service will be started later, but we set the URL now
    INTELLIGENCE_AGENT_URL="http://localhost:${AI_SERVICE_PORT}"
    COORDINATOR_ARGS+=(--intelligence.agent_url="$INTELLIGENCE_AGENT_URL")
    echo -e "${GREEN}✓ Intelligence agent auto-configured: $INTELLIGENCE_AGENT_URL${NC}"
    echo -e "${YELLOW}  (AI service will start after coordinator)${NC}"
fi

STORE_URL="$STORE_URL" \
COMPOSER_REDIS_ADDRS="$REDIS_ADDR" \
COMPOSER_API_HTTP_PORT="$COORDINATOR_HTTP_PORT" \
COMPOSER_API_GRPC_PORT="$COORDINATOR_GRPC_PORT" \
COMPOSER_LOG_LEVEL=info \
./bin/coordinator "${COORDINATOR_ARGS[@]}" \
    > "$PROJECT_ROOT/logs/coordinator.log" 2>&1 &
COORDINATOR_PID=$!
echo -e "${GREEN}✓ Coordinator started (PID: $COORDINATOR_PID)${NC}"

# Wait for coordinator to be ready
echo -n "Waiting for coordinator to be ready..."
for i in {1..30}; do
    if curl -s "http://localhost:${COORDINATOR_HTTP_PORT}/health" &> /dev/null 2>&1 || (command -v nc &> /dev/null && nc -z localhost "$COORDINATOR_HTTP_PORT" 2>/dev/null); then
        echo -e " ${GREEN}✓${NC}"
        break
    fi
    echo -n "."
    sleep 1
done
echo ""

# Start JavaScript Worker first (if available) - needed for Go Worker to connect
LANG_WORKER_ADDRS=""
if [ "$SKIP_JS_WORKER" = false ]; then
    echo -e "${BLUE}Starting JavaScript Worker...${NC}"
    # Check if port is already in use
    if command -v lsof &> /dev/null; then
        EXISTING_PID=$(lsof -ti :"$WORKER_JS_PORT" 2>/dev/null || true)
        if [ -n "$EXISTING_PID" ]; then
            echo -e "${YELLOW}Port $WORKER_JS_PORT is already in use (PID: $EXISTING_PID), killing existing process...${NC}"
            kill "$EXISTING_PID" 2>/dev/null || true
            sleep 1
        fi
    fi
    cd "$PROJECT_ROOT/cmd/language-server-js" || exit 1
    (PORT="$WORKER_JS_PORT" npm start > "$PROJECT_ROOT/logs/language-server-js.log" 2>&1) &
    WORKER_JS_PID=$!
    sleep 2  # Give it a moment to start
    if ! kill -0 "$WORKER_JS_PID" 2>/dev/null; then
        echo -e "${YELLOW}⚠ JavaScript Worker may have failed to start, check logs${NC}"
    else
        echo -e "${GREEN}✓ JavaScript Worker started (PID: $WORKER_JS_PID)${NC}"
        if [ -n "$LANG_WORKER_ADDRS" ]; then
            LANG_WORKER_ADDRS="$LANG_WORKER_ADDRS,js:localhost:$WORKER_JS_PORT"
        else
            LANG_WORKER_ADDRS="js:localhost:$WORKER_JS_PORT"
        fi
    fi
    cd "$PROJECT_ROOT" || exit 1
fi

# Start Python Worker (if available)
if [ "$SKIP_PY_WORKER" = false ]; then
    echo -e "${BLUE}Starting Python Worker...${NC}"
    # Check if port is already in use
    if command -v lsof &> /dev/null; then
        EXISTING_PID=$(lsof -ti :"$WORKER_PY_PORT" 2>/dev/null || true)
        if [ -n "$EXISTING_PID" ]; then
            echo -e "${YELLOW}Port $WORKER_PY_PORT is already in use (PID: $EXISTING_PID), killing existing process...${NC}"
            kill "$EXISTING_PID" 2>/dev/null || true
            sleep 1
        fi
    fi
    cd "$PROJECT_ROOT/cmd/language-server-py" || exit 1
    (PORT="$WORKER_PY_PORT" uv run python main.py > "$PROJECT_ROOT/logs/language-server-py.log" 2>&1) &
    WORKER_PY_PID=$!
    sleep 1  # Give it a moment to start
    if ! kill -0 "$WORKER_PY_PID" 2>/dev/null; then
        echo -e "${YELLOW}⚠ Python Worker may have failed to start, check logs${NC}"
    else
        echo -e "${GREEN}✓ Python Worker started (PID: $WORKER_PY_PID)${NC}"
        if [ -n "$LANG_WORKER_ADDRS" ]; then
            LANG_WORKER_ADDRS="$LANG_WORKER_ADDRS,python:localhost:$WORKER_PY_PORT"
        else
            LANG_WORKER_ADDRS="python:localhost:$WORKER_PY_PORT"
        fi
    fi
    cd "$PROJECT_ROOT" || exit 1
fi

# Start Go Worker
echo -e "${BLUE}Starting Go Worker...${NC}"
REDIS_ADDR="$REDIS_ADDR" \
COORDINATOR_ADDR="$COORDINATOR_ADDR" \
LANG_WORKER_ADDRS="$LANG_WORKER_ADDRS" \
STREAM_NAME=workflow.tasks \
CONSUMER_GROUP=workflow-workers \
WORKER_ID="worker-$(hostname)-$$" \
./bin/worker > "$PROJECT_ROOT/logs/worker.log" 2>&1 &
WORKER_PID=$!
echo -e "${GREEN}✓ Go Worker started (PID: $WORKER_PID)${NC}"
if [ -n "$LANG_WORKER_ADDRS" ]; then
    echo -e "${GREEN}  Language worker addresses: $LANG_WORKER_ADDRS${NC}"
else
    echo -e "${YELLOW}  Warning: No language workers configured. JavaScript/Python nodes will fail.${NC}"
fi

# Start AI Service (if available)
if [ "$SKIP_AI_SERVICE" = false ]; then
    echo -e "${BLUE}Starting AI Service...${NC}"
    # Check if port is already in use
    if command -v lsof &> /dev/null; then
        EXISTING_PID=$(lsof -ti :"$AI_SERVICE_PORT" 2>/dev/null || true)
        if [ -n "$EXISTING_PID" ]; then
            echo -e "${YELLOW}Port $AI_SERVICE_PORT is already in use (PID: $EXISTING_PID), killing existing process...${NC}"
            kill "$EXISTING_PID" 2>/dev/null || true
            sleep 1
        fi
    fi
    cd "$PROJECT_ROOT/ai" || exit 1
    (GOOGLE_API_KEY="$GOOGLE_API_KEY" HOST=0.0.0.0 PORT="$AI_SERVICE_PORT" uv run python main.py > "$PROJECT_ROOT/logs/ai-service.log" 2>&1) &
    AI_SERVICE_PID=$!
    sleep 2  # Give it a moment to start
    
    # Wait for AI service to be ready
    echo -n "Waiting for AI service to be ready..."
    for i in {1..10}; do
        if curl -s "http://localhost:${AI_SERVICE_PORT}/health" &> /dev/null 2>&1 || (command -v nc &> /dev/null && nc -z localhost "$AI_SERVICE_PORT" 2>/dev/null); then
            echo -e " ${GREEN}✓${NC}"
            break
        fi
        echo -n "."
        sleep 1
    done
    echo ""
    
    if ! kill -0 "$AI_SERVICE_PID" 2>/dev/null; then
        echo -e "${YELLOW}⚠ AI Service may have failed to start, check logs${NC}"
        if [ -z "$GOOGLE_API_KEY" ]; then
            echo -e "${YELLOW}⚠ GOOGLE_API_KEY not set. AI service requires GOOGLE_API_KEY environment variable.${NC}"
        fi
    else
        echo -e "${GREEN}✓ AI Service started (PID: $AI_SERVICE_PID)${NC}"
    fi
    cd "$PROJECT_ROOT" || exit 1
fi

# Display status
echo -e "\n${GREEN}=== Services Running ===${NC}"
echo -e "Coordinator HTTP:  ${GREEN}http://localhost:${COORDINATOR_HTTP_PORT}${NC}"
echo -e "Coordinator gRPC:  ${GREEN}localhost:${COORDINATOR_GRPC_PORT}${NC}"
if [ "$SKIP_JS_WORKER" = false ]; then
    echo -e "JavaScript Worker: ${GREEN}localhost:${WORKER_JS_PORT}${NC}"
fi
if [ "$SKIP_PY_WORKER" = false ]; then
    echo -e "Python Worker:     ${GREEN}localhost:${WORKER_PY_PORT}${NC}"
fi
if [ "$SKIP_AI_SERVICE" = false ]; then
    echo -e "AI Service:        ${GREEN}http://localhost:${AI_SERVICE_PORT}${NC}"
fi

echo -e "\n${GREEN}=== Log Files ===${NC}"
echo -e "Coordinator:  ${YELLOW}$PROJECT_ROOT/logs/coordinator.log${NC}"
echo -e "Go Worker:    ${YELLOW}$PROJECT_ROOT/logs/worker.log${NC}"
if [ "$SKIP_JS_WORKER" = false ]; then
    echo -e "JavaScript Worker: ${YELLOW}$PROJECT_ROOT/logs/language-server-js.log${NC}"
fi
if [ "$SKIP_PY_WORKER" = false ]; then
    echo -e "Python Worker: ${YELLOW}$PROJECT_ROOT/logs/language-server-py.log${NC}"
fi
if [ "$SKIP_AI_SERVICE" = false ]; then
    echo -e "AI Service: ${YELLOW}$PROJECT_ROOT/logs/ai-service.log${NC}"
fi

echo -e "\n${GREEN}=== Useful Commands ===${NC}"
echo -e "View coordinator logs:  ${YELLOW}tail -f logs/coordinator.log${NC}"
echo -e "View worker logs:       ${YELLOW}tail -f logs/worker.log${NC}"
echo -e "Stop all services:      ${YELLOW}Press Ctrl+C${NC}"

echo -e "\n${GREEN}✓ Local development environment is ready!${NC}"
echo -e "${YELLOW}Press Ctrl+C to stop all services${NC}\n"

# Wait for all background jobs (ignore errors from background processes)
wait || true

