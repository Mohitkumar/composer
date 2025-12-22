#!/bin/bash
# Bootstrap script for local development environment
# This script sets up the local development environment using Docker Compose

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

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

# Check if services are already running
if $DOCKER_COMPOSE_CMD ps 2>/dev/null | grep -q "Up"; then
    echo -e "${YELLOW}Some services are already running.${NC}"
    read -p "Do you want to restart all services? (y/N): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        echo -e "${YELLOW}Stopping existing services...${NC}"
        $DOCKER_COMPOSE_CMD down
    else
        echo -e "${GREEN}Keeping existing services running.${NC}"
        exit 0
    fi
fi

# Build images
echo -e "${YELLOW}Rebuilding Docker images...${NC}"
$DOCKER_COMPOSE_CMD build --pull

# Start services
echo -e "${YELLOW}Starting services...${NC}"
$DOCKER_COMPOSE_CMD up --build -d

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

# Display service status
echo -e "\n${GREEN}=== Service Status ===${NC}"
$DOCKER_COMPOSE_CMD ps

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
