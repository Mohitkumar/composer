#!/bin/bash
# Bootstrap script for local development environment
# This script sets up the local development environment using Docker Compose
#
# Usage:
#   ./bootstrap_docker_compose.sh                    # Build images and start all services
#   ./bootstrap_docker_compose.sh --no-build        # Skip building images, use existing ones
#   ./bootstrap_docker_compose.sh -s web            # Build and start only web service
#   ./bootstrap_docker_compose.sh -s web --build-only    # Only build web service
#   ./bootstrap_docker_compose.sh -s coordinator --up-only  # Only start coordinator (no build)

# Ensure we're using bash (not zsh or other shells)
if [ -z "$BASH_VERSION" ]; then
    exec bash "$0" "$@"
fi

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Available services
AVAILABLE_SERVICES=("postgres" "redis" "coordinator" "worker" "worker-js" "worker-py" "ai-service" "web" "otel-collector" "jaeger")

# Parse command line arguments
SKIP_BUILD=false
BUILD_ONLY=false
UP_ONLY=false
SERVICE=""
ALL_SERVICES=true

while [[ $# -gt 0 ]]; do
    case $1 in
        --no-build|--skip-build|-n)
            SKIP_BUILD=true
            shift
            ;;
        --build-only|-b)
            BUILD_ONLY=true
            shift
            ;;
        --up-only|-u)
            UP_ONLY=true
            SKIP_BUILD=true
            shift
            ;;
        --service|-s)
            if [ -z "${2:-}" ]; then
                echo -e "${RED}Error: --service requires a service name${NC}"
                exit 1
            fi
            SERVICE="$2"
            ALL_SERVICES=false
            shift 2
            ;;
        -h|--help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --no-build, --skip-build, -n    Skip building Docker images"
            echo "  --build-only, -b                 Only build images, don't start services"
            echo "  --up-only, -u                   Only start services, don't build (implies --no-build)"
            echo "  --service, -s <name>             Build/start specific service (one at a time)"
            echo "  -h, --help                       Show this help message"
            echo ""
            echo "Available services:"
            for svc in "${AVAILABLE_SERVICES[@]}"; do
                echo "  - $svc"
            done
            echo ""
            echo "Examples:"
            echo "  $0                              # Build and start all services"
            echo "  $0 -s web                       # Build and start web service"
            echo "  $0 -s web --build-only          # Only build web service"
            echo "  $0 -s coordinator --up-only     # Only start coordinator (no build)"
            exit 0
            ;;
        *)
            echo -e "${RED}Unknown option: $1${NC}"
            echo "Use -h or --help for usage information"
            exit 1
            ;;
    esac
done

# Validate service name if provided
if [ "$ALL_SERVICES" = false ]; then
    if [[ ! " ${AVAILABLE_SERVICES[@]} " =~ " ${SERVICE} " ]]; then
        echo -e "${RED}Error: Unknown service: $SERVICE${NC}"
        echo "Available services: ${AVAILABLE_SERVICES[*]}"
        exit 1
    fi
fi

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DOCKER_DIR="$PROJECT_ROOT/infra/docker"

echo -e "${GREEN}=== Composer Local Development Bootstrap ===${NC}\n"

# Check prerequisites
echo -e "${YELLOW}Checking prerequisites...${NC}"

# Check Docker
if ! command -v docker &> /dev/null; then
    echo -e "${RED}Error: Docker is not installed. Please install Docker first.${NC}"
    exit 1
fi

# Check Docker Compose
DOCKER_COMPOSE_CMD=""
if command -v docker-compose &> /dev/null; then
    DOCKER_COMPOSE_CMD="docker-compose"
elif docker compose version &> /dev/null; then
    DOCKER_COMPOSE_CMD="docker compose"
else
    echo -e "${RED}Error: Docker Compose is not installed. Please install Docker Compose first.${NC}"
    exit 1
fi

# Check if Docker daemon is running
if ! docker info &> /dev/null; then
    echo -e "${RED}Error: Docker daemon is not running. Please start Docker first.${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Prerequisites check passed${NC}\n"

# Navigate to docker directory
cd "$DOCKER_DIR"

# Function to check if service is running
is_service_running() {
    local service=$1
    $DOCKER_COMPOSE_CMD ps "$service" 2>/dev/null | grep -q "Up"
}

# Determine which services to operate on
if [ "$ALL_SERVICES" = true ]; then
    TARGET_SERVICE=""
    SERVICES_STRING="all services"
else
    TARGET_SERVICE="$SERVICE"
    SERVICES_STRING="$SERVICE"
fi

# Check if service is already running (only for single service mode)
if [ "$UP_ONLY" = false ] && [ "$BUILD_ONLY" = false ] && [ "$ALL_SERVICES" = false ]; then
    if is_service_running "$SERVICE"; then
        echo -e "${YELLOW}Service $SERVICE is already running.${NC}"
        read -p "Do you want to restart it? (y/N): " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            echo -e "${YELLOW}Stopping $SERVICE...${NC}"
            $DOCKER_COMPOSE_CMD stop "$SERVICE" 2>/dev/null || true
            $DOCKER_COMPOSE_CMD rm -f "$SERVICE" 2>/dev/null || true
        else
            echo -e "${GREEN}Keeping $SERVICE running.${NC}"
            exit 0
        fi
    fi
fi

# Build images (if not skipped and not up-only)
if [ "$SKIP_BUILD" = false ] && [ "$UP_ONLY" = false ]; then
    if [ "$ALL_SERVICES" = true ]; then
        echo -e "${YELLOW}Building all Docker images...${NC}"
        $DOCKER_COMPOSE_CMD build --pull
    else
        echo -e "${YELLOW}Building Docker image for: $SERVICE${NC}"
        $DOCKER_COMPOSE_CMD build --pull "$SERVICE"
    fi
elif [ "$UP_ONLY" = false ]; then
    echo -e "${YELLOW}Skipping image build (using existing images)...${NC}"
fi

# Exit if build-only mode
if [ "$BUILD_ONLY" = true ]; then
    echo -e "${GREEN}✓ Build completed${NC}"
    exit 0
fi

# Start services
if [ "$ALL_SERVICES" = true ]; then
    echo -e "${YELLOW}Starting all services...${NC}"
    if [ "$SKIP_BUILD" = false ]; then
        $DOCKER_COMPOSE_CMD up --build -d
    else
        $DOCKER_COMPOSE_CMD up -d
    fi
else
    echo -e "${YELLOW}Starting service: $SERVICE${NC}"
    # For single service: build only that service, then start it (dependencies will start but not rebuild)
    if [ "$SKIP_BUILD" = false ]; then
        # Build only the specified service (not dependencies)
        echo -e "${BLUE}Building $SERVICE (dependencies will not be rebuilt)...${NC}"
        $DOCKER_COMPOSE_CMD build "$SERVICE"
        # Start the service (dependencies will start if needed, but won't rebuild)
        $DOCKER_COMPOSE_CMD up -d "$SERVICE"
    else
        # Just start the service (dependencies will start if needed)
        $DOCKER_COMPOSE_CMD up -d "$SERVICE"
    fi
fi

# Wait for services to be healthy
echo -e "${YELLOW}Waiting for services to be ready...${NC}"
sleep 5

# Check service health
echo -e "${YELLOW}Checking service health...${NC}"

# Wait for PostgreSQL
echo -n "Waiting for PostgreSQL..."
for i in {1..30}; do
    if $DOCKER_COMPOSE_CMD exec -T postgres pg_isready -U postgres &> /dev/null; then
        echo -e " ${GREEN}✓${NC}"
        break
    fi
    echo -n "."
    sleep 1
done

# Wait for Redis
echo -n "Waiting for Redis..."
for i in {1..30}; do
    if $DOCKER_COMPOSE_CMD exec -T redis redis-cli ping &> /dev/null; then
        echo -e " ${GREEN}✓${NC}"
        break
    fi
    echo -n "."
    sleep 1
done

# Check and run database migrations if needed
echo -e "\n${YELLOW}Checking database migrations...${NC}"
MIGRATIONS_SCRIPT="$DOCKER_DIR/run-migrations.sh"
if [ -f "$MIGRATIONS_SCRIPT" ]; then
    # Check if workflow_definitions table exists
    if ! $DOCKER_COMPOSE_CMD exec -T postgres psql -U postgres -d composer -tAc "SELECT 1 FROM information_schema.tables WHERE table_name='workflow_definitions'" | grep -q 1; then
        echo -e "${YELLOW}Database tables not found. Running migrations...${NC}"
        bash "$MIGRATIONS_SCRIPT"
        echo -e "${GREEN}✓ Migrations completed${NC}"
    else
        echo -e "${GREEN}✓ Database tables already exist${NC}"
    fi
else
    echo -e "${YELLOW}Warning: Migration script not found at $MIGRATIONS_SCRIPT${NC}"
fi

# Display service status
echo -e "\n${GREEN}=== Service Status ===${NC}"
if [ "$ALL_SERVICES" = true ]; then
    $DOCKER_COMPOSE_CMD ps
else
    $DOCKER_COMPOSE_CMD ps "$SERVICE"
fi

# Display access information
echo -e "\n${GREEN}=== Access Information ===${NC}"
echo -e "Web UI:              ${GREEN}http://localhost:3000${NC}"
echo -e "Coordinator API:     ${GREEN}http://localhost:8080${NC}"
echo -e "Coordinator gRPC:    ${GREEN}localhost:50051${NC}"
echo -e "Jaeger UI:           ${GREEN}http://localhost:16686${NC} (distributed tracing)"
echo -e "OTEL Collector:      ${GREEN}localhost:4317${NC} (gRPC) / ${GREEN}localhost:4318${NC} (HTTP)"
echo -e "PostgreSQL:          ${GREEN}localhost:5432${NC} (postgres/password)"
echo -e "Redis:               ${GREEN}localhost:6379${NC}"

echo -e "\n${GREEN}=== Useful Commands ===${NC}"
echo -e "View logs:           ${YELLOW}cd infra/docker && $DOCKER_COMPOSE_CMD logs -f${NC}"
echo -e "Stop services:       ${YELLOW}cd infra/docker && $DOCKER_COMPOSE_CMD down${NC}"
echo -e "Restart services:    ${YELLOW}cd infra/docker && $DOCKER_COMPOSE_CMD restart${NC}"
echo -e "View status:        ${YELLOW}cd infra/docker && $DOCKER_COMPOSE_CMD ps${NC}"

echo -e "\n${GREEN}✓ Local development environment is ready!${NC}\n"
