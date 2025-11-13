# Observability Setup

This document describes the OpenTelemetry observability stack for Composer.

## Components

### OpenTelemetry Collector
- **Port**: 4317 (gRPC), 4318 (HTTP)
- **Purpose**: Receives telemetry data from services and routes it to backends
- **Config**: `otel-collector-config.yaml`

### Jaeger (Distributed Tracing)
- **UI**: http://localhost:16686
- **Purpose**: Visualize distributed traces
- **Ports**: 
  - 16686: Web UI
  - 14250: gRPC collector
  - 14268: HTTP collector

### Prometheus (Metrics)
- **UI**: http://localhost:9090
- **Purpose**: Store and query metrics
- **Config**: `prometheus.yml`
- **Scrapes**: OpenTelemetry Collector's Prometheus exporter endpoint (port 8889)

### Grafana (Visualization)
- **UI**: http://localhost:3000
- **Default Credentials**: admin/admin
- **Purpose**: Create dashboards for metrics and traces
- **Pre-configured**: Prometheus and Jaeger datasources

## Environment Variables

Services are configured via environment variables:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317
OTEL_EXPORTER_OTLP_PROTOCOL=grpc
OTEL_SERVICE_NAME=composer-coordinator
OTEL_RESOURCE_ATTRIBUTES=service.name=composer-coordinator,service.version=1.0.0
```

## Accessing the UIs

After starting docker-compose:

1. **Jaeger UI**: http://localhost:16686
   - Search for traces by service name, operation, tags
   - View trace timelines and spans

2. **Prometheus UI**: http://localhost:9090
   - Query metrics using PromQL
   - View metrics like `workflow.started`, `task.completed`, etc.

3. **Grafana UI**: http://localhost:3000
   - Login with admin/admin
   - Create dashboards for workflow metrics
   - View traces from Jaeger

## Metrics Available

### Workflow Metrics
- `workflow.started` - Counter
- `workflow.completed` - Counter
- `workflow.failed` - Counter
- `workflow.duration` - Histogram (seconds)

### Task Metrics
- `task.created` - Counter
- `task.started` - Counter
- `task.completed` - Counter
- `task.failed` - Counter
- `task.duration` - Histogram (seconds)

### Outbox Metrics
- `outbox.processed` - Counter
- `outbox.failed` - Counter
- `outbox.dlq` - Counter
- `outbox.in_flight` - UpDownCounter

All metrics include attributes for filtering:
- `workflow.id`, `workflow.run_id`
- `task.id`, `task.node_id`, `task.node_type`
- `outbox.stream`

## Example Prometheus Queries

```promql
# Workflow success rate
rate(workflow_completed_total[5m]) / rate(workflow_started_total[5m])

# Average workflow duration
histogram_quantile(0.95, rate(workflow_duration_bucket[5m]))

# Task failure rate by node type
rate(task_failed_total[5m]) / rate(task_created_total[5m])
```

## Troubleshooting

1. **No traces in Jaeger**: Check that services have `OTEL_EXPORTER_OTLP_ENDPOINT` set correctly
2. **No metrics in Prometheus**: Verify Prometheus is scraping `otel-collector:8889`
3. **Collector errors**: Check collector logs: `docker logs composer-otel-collector`

