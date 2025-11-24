# Infrastructure

This directory contains all infrastructure-related files for Composer.

## Directory Structure

```
infra/
├── docker/              # Docker Compose setup for local development
│   ├── docker-compose.yml
│   ├── Dockerfile.coordinator
│   ├── Dockerfile.worker-go
│   ├── Dockerfile.worker-js
│   ├── Dockerfile.worker-py
│   ├── otel-collector-config.yaml
│   ├── prometheus.yml
│   └── grafana/         # Grafana provisioning configs
├── k8s/                 # Kubernetes manifests
│   ├── helm/            # Helm chart
│   │   └── composer/
│   │       ├── Chart.yaml
│   │       ├── values.yaml
│   │       ├── templates/
│   │       └── README.md
│   └── *.yaml          # Legacy K8s manifests (can be removed after Helm migration)
└── scripts/            # Bootstrap and utility scripts
    ├── bootstrap_local.sh
    └── bootstrap_k8s.sh
```

## Docker Compose (Local Development)

The `docker/` directory contains everything needed to run Composer locally using Docker Compose.

### Quick Start

```bash
# From project root
cd infra/docker
docker-compose up -d

# Or use Makefile from project root
make docker-up
```

### Services

- **Coordinator**: Workflow orchestration service (port 8080, 50051)
- **Worker**: Task execution workers (scales to 2 replicas)
- **Worker-JS**: JavaScript language worker (port 50052)
- **Worker-Py**: Python language worker (port 50053)
- **PostgreSQL**: Database (port 5432)
- **Redis**: Task queue (port 6379)
- **OpenTelemetry Collector**: Telemetry aggregation (ports 4317, 4318, 8889)
- **Jaeger**: Distributed tracing UI (port 16686)
- **Prometheus**: Metrics storage (port 9090)
- **Grafana**: Visualization dashboards (port 3000)

### Accessing Services

- Coordinator API: http://localhost:8080
- Jaeger UI: http://localhost:16686
- Prometheus UI: http://localhost:9090
- Grafana UI: http://localhost:3000 (admin/admin)

## Kubernetes / Helm

The `k8s/helm/composer/` directory contains a Helm chart for deploying Composer to Kubernetes.

### Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- kubectl configured

### Installation

```bash
# Install with default values
helm install composer ./infra/k8s/helm/composer

# Install with custom values
helm install composer ./infra/k8s/helm/composer -f my-values.yaml

# Install with PostgreSQL and Redis
helm install composer ./infra/k8s/helm/composer \
  --set postgresql.enabled=true \
  --set redis.enabled=true
```

### Configuration

See `k8s/helm/composer/values.yaml` for all configuration options.

### Upgrading

```bash
helm upgrade composer ./infra/k8s/helm/composer
```

### Uninstallation

```bash
helm uninstall composer
```

For more details, see `k8s/helm/composer/README.md`.

## Scripts

### bootstrap_local.sh

Bootstrap script for running Composer services locally (not in Docker/K8s).

**Usage:**
```bash
./infra/scripts/bootstrap_local.sh
```

**What it does:**
- Checks prerequisites (Go, Node.js, Python/UV, PostgreSQL, Redis)
- Builds Go binaries (coordinator, worker)
- Generates protobuf files
- Sets up language workers (JS, Python)
- Starts all services locally:
  - Coordinator (HTTP + gRPC)
  - Go Worker
  - JavaScript Worker (if Node.js available)
  - Python Worker (if UV available)
- Creates log files in `logs/` directory
- Handles graceful shutdown on Ctrl+C

**Prerequisites:**
- Go 1.21+
- PostgreSQL 16+ (running locally or via Docker)
- Redis 7+ (running locally or via Docker)
- Node.js 20+ (optional, for JS worker)
- UV (optional, for Python worker)

**Environment Variables:**
- `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`
- `REDIS_HOST`, `REDIS_PORT`
- `COORDINATOR_HTTP_PORT`, `COORDINATOR_GRPC_PORT`

**Quick Start PostgreSQL/Redis with Docker:**
```bash
docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=password -e POSTGRES_DB=composer postgres:16-alpine
docker run -d -p 6379:6379 redis:7-alpine
```

### bootstrap_docker_compose.sh

Bootstrap script for local development using Docker Compose (if it exists).

### bootstrap_k8s.sh

Bootstrap script for Kubernetes deployment using Helm.

**Usage:**
```bash
# Basic installation
./infra/scripts/bootstrap_k8s.sh

# With custom namespace and release name
./infra/scripts/bootstrap_k8s.sh -n my-namespace -r my-release

# With custom values file
./infra/scripts/bootstrap_k8s.sh -f my-values.yaml

# Dry run (test without installing)
./infra/scripts/bootstrap_k8s.sh --dry-run

# Upgrade existing release
./infra/scripts/bootstrap_k8s.sh --upgrade
```

**Options:**
- `-n, --namespace NAME`: Kubernetes namespace (default: composer)
- `-r, --release NAME`: Helm release name (default: composer)
- `-f, --values FILE`: Path to custom values file
- `--dry-run`: Perform a dry run without installing
- `--upgrade`: Upgrade existing release
- `-h, --help`: Show help message

**What it does:**
- Checks prerequisites (kubectl, Helm, cluster connection)
- Creates namespace if needed
- Deploys Composer using Helm
- Waits for deployments to be ready
- Displays deployment status and access information

