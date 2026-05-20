# Composer Benchmark

Load-testing suite for the Composer workflow engine using [k6](https://k6.io).

## Directory layout

```
benchmark/
├── docker-compose.yml      # Self-contained benchmark environment
├── mock-server/
│   ├── main.go             # Go mock HTTP server (all workflow HTTP targets)
│   └── Dockerfile
└── k6/
    ├── smoke-test.js       # Sanity check — 1 VU, 60s, all workflows must succeed
    ├── load-test.js        # Sustained ramp: 0→50 VUs over 35 min
    ├── stress-test.js      # Spike test: 100→150 VUs, finds breaking point
    └── lib/
        ├── auth.js         # Login helper with retry
        ├── workflows.js    # Workflow specs + registration helpers
        └── executor.js     # Execute/poll helpers + custom k6 metrics
```

## Workflow coverage

| ID | Node types used | Pattern | Expected duration |
|----|-----------------|---------|-------------------|
| `bench-http-simple`   | HTTP                      | single node            | ~5ms  |
| `bench-http-chain`    | HTTP × 3                  | serial pipeline        | ~85ms |
| `bench-switch-branch` | HTTP + SWITCH             | conditional routing    | ~70ms |
| `bench-fork-join`     | HTTP + FORK + JOIN        | parallel fan-out       | ~55ms |
| `bench-mixed`         | HTTP + SWITCH + FORK+JOIN | all non-code types     | ~90ms |

All workflow HTTP calls target the **mock-server** which runs inside the same Docker network and adds realistic latency jitter.

## Mock server endpoints

| Endpoint | Method | Simulated latency | Purpose |
|----------|--------|-------------------|---------|
| `/api/fast`      | GET  | 5ms   | Baseline node |
| `/api/data`      | GET  | 20ms  | Entry-point fetch (always returns `value=100`) |
| `/api/process`   | POST | 50ms  | Heavy processing step |
| `/api/enrich`    | POST | 30ms  | Data enrichment branch |
| `/api/validate`  | POST | 10ms  | Fast validation branch |
| `/api/transform` | POST | 40ms  | Data transform branch |
| `/api/notify`    | POST | 15ms  | Terminal notification |
| `/api/slow`      | GET  | 300ms | Simulates sluggish external service |
| `/health`        | GET  | 0ms   | Health check |

## Quick start

### 1. Start the benchmark environment

```bash
cd benchmark
docker compose up -d --build
```

This starts: **postgres**, **redis**, **coordinator** (3 outbox workers), **worker** (×3 replicas), **mock-server**.

Wait for everything to be healthy:

```bash
docker compose ps
# all services should show "healthy" or "running"
```

### 2. Run the smoke test first

```bash
docker compose run --rm k6 run /scripts/smoke-test.js
```

This runs 1 VU for 60 s, cycling through every workflow type. All must succeed before you run the load test.

### 3. Run the load test

```bash
docker compose run --rm k6 run /scripts/load-test.js
```

35-minute run. Ramps from 0 → 20 → 50 VUs. Prints a summary at the end.

### 4. Run the stress test

```bash
docker compose run --rm k6 run /scripts/stress-test.js
```

Spike to 100 → 150 VUs. Watch coordinator and worker logs for errors.

## Customising

### Override VU counts without editing files

```bash
docker compose run --rm k6 run --vus 10 --duration 5m /scripts/load-test.js
```

### Point at a different coordinator (non-Docker)

```bash
docker compose run --rm \
  -e BASE_URL=http://host.docker.internal:8080 \
  -e MOCK_SERVER_URL=http://host.docker.internal:8081 \
  k6 run /scripts/load-test.js
```

### Scale workers

```bash
docker compose up -d --scale worker=5
```

## Key metrics to watch

| Metric | What it measures |
|--------|-----------------|
| `workflow_duration_ms` (p50/p95/p99) | End-to-end workflow latency |
| `workflow_success_rate` | Fraction of workflows that reached COMPLETED/SUCCEEDED |
| `workflow_failures` | Absolute count of failed/timed-out workflows |
| `http_req_duration` (p95) | Raw API response time (execute + poll calls) |
| `http_req_failed` | Fraction of HTTP 4xx/5xx responses |

## Diagnosing issues under load

**Postgres connection exhaustion**
```bash
docker compose logs coordinator | grep "connection"
# tune: --outbox.concurrency and postgres max_connections
```

**Redis stream backlog growing**
```bash
docker compose exec redis redis-cli XPENDING workflow.tasks workflow-workers - + 100
# tune: --scale worker=N or --outbox.batch_size
```

**Outbox falling behind**
```bash
docker compose exec postgres psql -U postgres composer \
  -c "SELECT status, COUNT(*) FROM outbox GROUP BY status;"
```

## Tear down

```bash
docker compose down -v   # -v removes the postgres volume
```
