# Composer Helm Chart

This Helm chart deploys the Composer workflow orchestration system to Kubernetes.

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- PostgreSQL (can be deployed via chart or external)
- Redis (can be deployed via chart or external)

## Installation

### Install with default values

```bash
helm install composer ./infra/k8s/helm/composer
```

### Install with custom values

```bash
helm install composer ./infra/k8s/helm/composer -f my-values.yaml
```

### Install with PostgreSQL and Redis subcharts

The chart supports using Bitnami PostgreSQL and Redis charts as dependencies:

```bash
helm install composer ./infra/k8s/helm/composer \
  --set postgresql.enabled=true \
  --set redis.enabled=true
```

## Configuration

See `values.yaml` for all available configuration options.

### Key Configuration Options

- `coordinator.replicaCount`: Number of coordinator replicas (default: 1)
- `worker.replicaCount`: Number of worker replicas (default: 2)
- `coordinator.config.store.url`: PostgreSQL connection string
- `coordinator.config.redis.addrs`: Redis addresses
- `otelCollector.enabled`: Enable OpenTelemetry Collector (default: true)
- `jaeger.enabled`: Enable Jaeger for tracing (default: true)
- `prometheus.enabled`: Enable Prometheus for metrics (default: true)
- `grafana.enabled`: Enable Grafana for visualization (default: true)

## Components

The chart deploys:

1. **Coordinator**: Workflow orchestration service
2. **Worker**: Task execution workers
3. **Worker-JS**: JavaScript language worker
4. **Worker-Py**: Python language worker
5. **OpenTelemetry Collector**: Telemetry aggregation
6. **Jaeger**: Distributed tracing
7. **Prometheus**: Metrics storage
8. **Grafana**: Visualization dashboards

## Uninstallation

```bash
helm uninstall composer
```

## Upgrading

```bash
helm upgrade composer ./infra/k8s/helm/composer
```

## Accessing Services

After installation, services can be accessed via:

- **Coordinator HTTP API**: `composer-coordinator:8080`
- **Coordinator gRPC**: `composer-coordinator:50051`
- **Jaeger UI**: Port-forward to service or use Ingress
- **Prometheus UI**: Port-forward to service or use Ingress
- **Grafana UI**: Port-forward to service or use Ingress

Example port-forward commands:

```bash
# Jaeger
kubectl port-forward svc/composer-jaeger-query 16686:16686

# Prometheus
kubectl port-forward svc/prometheus-server 9090:9090

# Grafana
kubectl port-forward svc/composer-grafana 3000:3000
```

