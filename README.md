# Composer

**A distributed workflow engine for high-throughput, multi-step automation.**

Composer runs workflows defined as a graph of steps: HTTP calls, Python or JavaScript code, branches, parallel fan-out and joins, timers, and waits for external events. It persists every state change in PostgreSQL, dispatches work through Redis Streams to a horizontally scaled worker pool, and guarantees that a workflow never loses a step and never runs one twice concurrently.

- **Simple to operate** — PostgreSQL + Redis, both of which you probably already run.
- **Fast** — a few milliseconds of orchestration overhead per step; dispatch is push-based.
- **Reliable** — every decision and its effects commit in one transaction; crashed workers are detected through leases and their tasks re-dispatched.
- **Long waits are free** — `WAIT` and `WAIT_FOR_EVENT` park a branch for hours or days without holding a worker.
- **Polyglot** — mix HTTP steps with sandboxed Python and JavaScript in one workflow.

---

## Contents

1. [Use cases](#use-cases)
2. [Concepts](#concepts)
3. [Quick start](#quick-start)
4. [Building and running](#building-and-running)
5. [Defining workflows](#defining-workflows)
6. [API](#api)
7. [Design — high level](#design--high-level)
8. [Design — low level](#design--low-level)
9. [Configuration](#configuration)
10. [Project layout](#project-layout)
11. [Development and testing](#development-and-testing)

---

## Use cases

**A good fit**

| Use case | Why Composer works |
|---|---|
| **API orchestration** | Fan out to several services in parallel, join the results, branch on responses (`FORK`, joins, `SWITCH`). |
| **Business processes with humans in the loop** | `WAIT_FOR_EVENT` pauses a run until an approval or webhook arrives, for as long as it takes; `WAIT` adds cool-off periods and scheduled steps. |
| **Data pipelines** | Fetch → transform (Python/JS) → load chains, with per-step retries and backoff. |
| **Event-driven automation** | Start a run per incoming event; thousands of short runs execute concurrently. |
| **Multi-language processing** | Each step runs in the runtime that suits it, all wired together with templates. |

**Not a good fit**

- **Long computations in one step** — a single worker step is capped at 90 seconds. Split long work into steps, or hand it to a dedicated system and use `WAIT_FOR_EVENT` for the callback.
- **Code-as-workflow** — workflows are declarative graphs, not arbitrary programs with loops; there is no deterministic replay of workflow code as in Temporal.
- **Exactly-once side effects** — execution is at-least-once (a step can re-run after a worker crash). Steps that call external systems should be idempotent; HTTP steps send the task ID as an `Idempotency-Key` header to help.

---

## Concepts

| Term | Meaning |
|---|---|
| **Workflow definition** | A versioned graph of **nodes** and **edges**, written in YAML. One version per workflow is *deployed*. |
| **Run** | One execution of a definition version, with its own input. Runs are pinned to the version they started on. |
| **Node / task** | A step in the graph. When a run reaches a node it creates a **task** for it — at most one per node per run. |
| **Worker node** | `HTTP` or `CODE`: executed by a worker. |
| **Control node** | `SWITCH`, `FORK`, `JOIN`: resolved instantly by the coordinator. |
| **Wait node** | `WAIT`, `WAIT_FOR_EVENT`: parked until a timer fires or an event (signal) arrives. |
| **Queue** | Worker nodes are dispatched on a task queue (`http`, `python`, `js`, or a custom one) that a pool of workers consumes. |
| **Event** | Something that happened to a run (a task completed, a timer fired, …). The engine's state machine consumes events and decides what happens next. |

---

## Quick start

Requirements: Docker with Compose.

```bash
make docker-up          # PostgreSQL, Redis, coordinator, 2 workers, language servers, web UI, observability
```

Open the web UI at **http://localhost:3000** and sign in as `admin` / `admin123` (change it after first login).

Or drive it from the command line:

```bash
# 1. Log in
TOKEN=$(curl -s localhost:8080/api/v1/auth/login \
  -d '{"username":"admin","password":"admin123"}' | jq -r .token)

# 2. Register a two-step workflow (the spec is YAML)
curl -s localhost:8080/api/v1/workflows -H "Authorization: Bearer $TOKEN" -d @- <<'JSON'
{
  "workflow_id": "hello",
  "name": "Hello",
  "version": 1,
  "spec": "nodes:\n  - {node_id: fetch, name: Fetch, type: HTTP, spec: {url: 'https://httpbin.org/json', method: GET}}\n  - node_id: shout\n    name: Shout\n    type: CODE\n    runtime: python\n    spec:\n      code: |\n        def handler(input):\n            return {'title': input['title'].upper()}\n      title: '{{tasks.fetch.output.slideshow.title}}'\nedges:\n  - {from: fetch, to: shout}\n"
}
JSON

# 3. Deploy it and start a run
curl -s -X POST "localhost:8080/api/v1/workflows/hello/deploy?version=1" -H "Authorization: Bearer $TOKEN"
RUN=$(curl -s localhost:8080/api/v1/workflows/hello/execute -H "Authorization: Bearer $TOKEN" \
  -d '{"input":{}}' | jq -r .run_id)

# 4. Inspect it
curl -s localhost:8080/api/v1/executions/$RUN -H "Authorization: Bearer $TOKEN" | jq
```

| Service | Address |
|---|---|
| Web UI | http://localhost:3000 |
| Coordinator HTTP API | http://localhost:8080 |
| Coordinator gRPC (workers) | localhost:50051 |
| Jaeger (traces) | http://localhost:16686 |
| Grafana (dashboards) | http://localhost:3001 |
| Prometheus | http://localhost:9090 |
| PostgreSQL / Redis | localhost:5432 / localhost:6379 |

`make docker-logs` follows the logs; `make docker-down` stops everything.

---

## Building and running

### Prerequisites

| Tool | Version | Used for |
|---|---|---|
| Go | 1.25+ | coordinator, worker |
| Node.js | 22 | JavaScript language server (`isolated-vm` is built for it), web UI |
| Python + [uv](https://docs.astral.sh/uv/) | 3.11+ | Python language server |
| `protoc` + `protoc-gen-go`, `protoc-gen-go-grpc` | | gRPC code generation |
| PostgreSQL / Redis | 16+ / 7+ | storage and queues (or use Docker) |

### Build

```bash
make install-deps   # Go modules, npm and uv dependencies
make proto          # generate gRPC code (Go, Python)
make build          # bin/coordinator, bin/worker, language servers, web UI
```

Individual targets: `build-coordinator`, `build-worker`, `build-worker-js`, `build-worker-py`, `build-web`. Run `make help` for the full list.

### Run locally (without Docker)

```bash
bash infra/scripts/bootstrap_local.sh
```

The script checks prerequisites, builds everything, starts the services in the background and prints the endpoints and log files (`logs/`). To run the pieces yourself:

```bash
psql "$DATABASE_URL" -f migrations/V001__init.sql      # schema, partitions, seed data

./bin/coordinator --store.url "$DATABASE_URL" --redis.addrs localhost:6379

(cd cmd/language-server-py && PORT=50053 uv run python main.py)
(cd cmd/language-server-js && PORT=50052 npm start)

LANG_WORKER_ADDRS=python:localhost:50053,js:localhost:50052 ./bin/worker

(cd web && npm run dev)                                 # http://localhost:3000
```

### Run on Kubernetes

A Helm chart is in `infra/k8s/helm/composer` (`infra/scripts/bootstrap_k8s.sh` installs it). It runs the coordinator, workers and language servers; the language servers are deployed with a read-only root filesystem, no capabilities, a non-root user and the `RuntimeDefault` seccomp profile.

> **Schema note.** `migrations/V001__init.sql` creates the run-scoped tables as partitioned tables. Existing unpartitioned databases cannot be converted in place; migrate data into a fresh database.

---

## Defining workflows

A workflow is a list of **nodes** and **edges**:

```yaml
nodes:
  - node_id: fetch_order          # unique id, used in templates as tasks.fetch_order
    name: Fetch order
    type: HTTP
    retry: {max_attempts: 3, backoff_seconds: 2}
    spec:
      url: "https://shop.example.com/orders/{{input.order_id}}"
      method: GET

  - node_id: is_large
    name: Large order?
    type: SWITCH

  - node_id: approval
    name: Manager approval
    type: WAIT_FOR_EVENT
    spec: {event: "approve-{{input.order_id}}", timeout_seconds: 86400}

  - node_id: ship
    name: Ship
    type: CODE
    runtime: python
    spec:
      code: |
        def handler(input):
            return {"shipped": True, "order": input["order"]}
      order: "{{tasks.fetch_order.output}}"

edges:
  - {from: fetch_order, to: is_large}
  - {from: is_large, to: approval, condition: "{{tasks.fetch_order.output.total > 1000}}"}
  - {from: is_large, to: ship,     condition: "{{tasks.fetch_order.output.total <= 1000}}"}
  - {from: approval, to: ship}
```

Here, small orders ship immediately. Large orders wait up to a day for an approval event, then ship. `ship` has two incoming edges, but only one of them is ever taken: the untaken branch is *skipped*, so `ship` runs exactly once either way.

### Node types

| Type | Executed by | Spec |
|---|---|---|
| `HTTP` | worker | `url`, `method`, optional `headers`, `body`, `timeout_seconds`. The response body (JSON, or `{"body": "…"}` for text) is the output; status ≥ 400 fails the step. |
| `CODE` | worker → language server | `runtime: python \| js` on the node; `code` must define `handler(input)`, where `input` is the resolved spec. The return value is the output. |
| `SWITCH` | coordinator | none. Follows every outgoing edge whose `condition` is true (or that has none); passes its predecessor's output through. |
| `FORK` | coordinator | none. Starts all outgoing branches in parallel. |
| `JOIN` | coordinator | none. Optional pass-through: any node with several incoming edges already waits for all of them. |
| `WAIT` | coordinator timer | `duration_seconds` (number or template) or `until` (RFC 3339). Output: `{"woke_at": …}`. |
| `WAIT_FOR_EVENT` | API signal | `event` (name, may be templated), optional `timeout_seconds` (the node fails on timeout). Output: the event's payload. |

### Joins and skipped branches

One rule decides when a node runs. A node becomes ready once every incoming edge is resolved:

- An edge is **active** when its source completed and, for a SWITCH, selected this target.
- An edge is **inactive** when its source was skipped, or the SWITCH chose another branch.

A ready node **runs** if at least one incoming edge is active, and is **skipped** otherwise. Skips propagate down untaken branches. This handles fork/join with branches of any length, diamonds, and SWITCH branches that merge again.

### Templates

Any string in a spec or condition can contain `{{ expression }}`, written in [CEL](https://github.com/google/cel-spec):

| Variable | Contents |
|---|---|
| `input` | the run's input |
| `config` | the run's `execution_options` |
| `tasks.<node_id>.output` / `.status` | a completed node's output and status |
| `system.now` | current time (RFC 3339) |

A string that is exactly one template keeps the value's type: `"{{input.count}}"` resolves to a number. Templates inside text are interpolated as strings.

### Retries, queues

```yaml
  - node_id: call_partner
    type: HTTP
    retry:
      max_attempts: 4        # total executions, including the first
      backoff_seconds: 5     # retry n waits 5 * 2^(n-1) seconds, capped at 10 minutes
    queue: partner-api       # optional: dispatch to workers started with WORKER_QUEUES=partner-api
    spec: {url: "https://partner.example.com/v1/sync", method: POST}
```

Without `retry` a node executes once. Without `queue` a node goes to its runtime's queue (`http`, `python`, `js`).

---

## API

All endpoints are under `/api/v1` and, except login, need `Authorization: Bearer <token>`.

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/auth/login` | Exchange username/password for a JWT |
| `POST` | `/workflows` | Register a definition version (`spec` is YAML) |
| `GET` | `/workflows`, `/workflows/{id}` | List / get definitions |
| `POST` | `/workflows/{id}/deploy?version=N`, `/workflows/{id}/rollback` | Choose the deployed version |
| `POST` | `/workflows/{id}/execute` | Start a run: `{"input": {…}, "execution_options": {…}}` → `{"run_id"}` |
| `GET` | `/executions`, `/executions/{id}` | List runs / run details with every task |
| `POST` | `/executions/{id}/events` | Signal a run: `{"event": "approve-42", "payload": {…}}` → `{"delivered": bool}` |
| `POST` | `/executions/{id}/retry` | Restart a finished run from a node: `{"node_id": "…"}` |
| `POST` | `/executions/{id}/cancel` | Cancel a run |

Error codes: `400` invalid request, `404` not found, `409` conflict (e.g. retrying a run that still has tasks in flight, signalling a finished run).

Workers talk to the coordinator over gRPC (`pkg/api/proto/workflow.proto`): `AcquireTaskLease`, `RenewTaskLease`, `ReportTaskResult`.

---

## Design — high level

![High-level architecture](docs/diagrams/hld-architecture.svg)

| Component | Role | Scales by |
|---|---|---|
| **Coordinator** | Hosts the APIs and the workflow **state machine**. Every input (API call, worker result, timer) becomes an event that is applied to the run in one transaction. Also runs the outbox enqueuer, timer loop, lease recovery and partition maintenance. | Adding replicas. It is stateless: runs are serialized by a database row lock, so any replica can handle any run. |
| **PostgreSQL** | Source of truth: definitions, runs, tasks, buffered events and the outbox. Run-scoped tables are partitioned by month. | Partition pruning keeps hot indexes small; retention drops whole months. |
| **Outbox → Redis Streams** | Task dispatches are written to the `outbox` in the same transaction as the state change, then published to `workflow.tasks.<queue>.<shard>`. An insert trigger notifies the enqueuer, so publishing happens right after commit. | Shard count per queue (`--stream_shards`); queues per workload. |
| **Workers** | Pull tasks from the streams of the queues they serve, lease them, execute them (HTTP directly; code via language servers) and report results. | Adding replicas or raising `WORKER_CONCURRENCY`. |
| **Language servers** | Run user code in sandboxes: Python (RestrictedPython + audit hooks + process limits), JavaScript (V8 isolates). | Independently of workers. |

### Life of a run

![Life of a workflow run](docs/diagrams/hld-request-flow.svg)

1. The client starts a run. In one transaction the coordinator inserts the run and applies `RunStarted`. The state machine creates tasks for the start node (and anything reachable inline), and dispatches are written to the outbox.
2. On commit, Postgres notifies the outbox enqueuer, which publishes each dispatch to its queue shard.
3. A worker reads the message, acquires the task's **lease**, executes the node and reports the result together with its **attempt number**.
4. The coordinator applies `NodeCompleted` (or `NodeFailed`) under the run's lock. Successors become ready and are dispatched in the same transaction. When nothing is left active, the run finishes.

### Guarantees

| Failure | What happens |
|---|---|
| Coordinator crashes mid-event | The transaction rolls back; the worker's report fails and is retried. Nothing is half-applied. |
| Worker dies mid-task | Its lease expires. Another worker can take over the task, and every coordinator re-dispatches expired leases every 30 s. |
| Duplicate or late report | Ignored: results must match the task's current `(task_id, attempt)`. |
| Redis loses messages | Tasks still `PENDING` without a dispatch are re-dispatched after 10 minutes; duplicates are dropped by the lease. |
| Step fails | The node's retry policy re-dispatches it with backoff; when attempts run out the node fails, and the run fails once nothing else is active. |

Execution is **at-least-once**. A step runs again only after its previous attempt's lease was lost.

---

## Design — low level

### Workflow state machine

![Workflow state machine](docs/diagrams/lld-state-machine.svg)

The engine follows the *functional core, imperative shell* pattern:

| Concept | Code |
|---|---|
| **Events** — what happened | `pkg/statemachine/events.go`: `RunStarted`, `NodeCompleted`, `NodeFailed`, `TimerFired`, `SignalReceived`, `RunRetried`, `RunCancelled` |
| **State** — the run snapshot | `pkg/statemachine/state.go`: `Run` (status, context) and a `NodeState` per reached node |
| **Transition** — decide | `Machine.Apply(event)` in `machine.go`; routing in `routing.go`, waits in `waits.go` |
| **Actions** — what must change | `pkg/statemachine/actions.go`: `CreateTask`, `DispatchTask`, `CompleteTask`, `RetryTask`, `ResolveWait`, `FinishRun`, … |
| **Shell** — load, decide, persist | `pkg/coord/engine.go` (inputs → events, one transaction per event) and `pkg/coord/persist.go` (actions → SQL + outbox) |

The machine is pure: it never writes, dispatches, or reads the clock. Every rule is unit-tested without a database: fencing stale reports, retries, join and skip routing, waits, and finishing the run. It reads task outputs and buffered signals on demand through a small `Store` port, so routing loads only the outputs a node's templates reference.

### Node and run lifecycles

![Node and run lifecycles](docs/diagrams/lld-lifecycles.svg)

### Data model

![Data model](docs/diagrams/lld-data-model.svg)

- **Task outputs live only in `task_instances`.** `workflow_runs.context_json` keeps just the input and execution options, so a run's row never grows with its number of nodes.
- **Partitioning.** `workflow_runs`, `task_instances` and `workflow_events` are range-partitioned by month on the run ID. Run IDs are UUIDv7, whose text form sorts by creation time, and every hot-path query carries the run ID, so it touches a single partition. `composer_ensure_partitions()` keeps three months ready. With `--retention_days`, `composer_drop_partitions()` drops finished months; a month with active runs is kept. Client-supplied `run_id`s must therefore be UUIDv7 from the last 24 hours.
- **Outbox.** An `AFTER INSERT` trigger runs `NOTIFY outbox_new`. Enqueuers `LISTEN` on a dedicated connection and claim rows with `FOR UPDATE SKIP LOCKED`. Polling (1 s) remains as a fallback and for delayed retries. Rows that keep failing move to `outbox_dlq`.

### Worker

![Worker internals](docs/diagrams/lld-worker.svg)

- One reader goroutine per stream (queue shard) fetches only as many messages as the hand-off channel has room for. A reader blocked on an idle stream therefore holds no execution capacity.
- `WORKER_CONCURRENCY` executors each run the cycle lease → execute → report → ack. A background loop renews the lease every TTL/3; if renewal reports the lease lost, the execution is cancelled and its result discarded.
- A reclaim loop takes over messages idle longer than the lease TTL (`XAUTOCLAIM`). After `MaxDeliveries` attempts the task is reported as failed (dead-lettered).

### Sandboxing user code

| Layer | Python | JavaScript |
|---|---|---|
| Language | RestrictedPython: no `_`-prefixed attributes, no `eval`/`exec`; import allowlist; module objects reachable only if allowlisted | V8 isolate: no `require`, `process`, timers or Node.js APIs |
| Interpreter | PEP 578 audit hook, irremovable: no file writes, no reads outside Python, no network, no subprocesses, no `ctypes`, no pickle loading | — |
| Process | per-execution child process with memory, CPU, file-size, file-descriptor and process limits; empty environment | isolate memory limit, timeout, capped logs and output; isolates run off the event loop |
| Container | non-root, read-only filesystem, no capabilities, `no-new-privileges`, PID limit | same |

For hostile multi-tenant workloads, also run the language servers under a VM-level sandbox (gVisor, Firecracker) and deny them network egress.

> The diagrams are Excalidraw drawings: open any `docs/diagrams/*.excalidraw` file at [excalidraw.com](https://excalidraw.com) to edit it, then export the SVG next to it.

---

## Configuration

### Coordinator

Flags, or environment variables with the `COMPOSER_` prefix (e.g. `COMPOSER_STORE_URL`):

| Flag | Default | Meaning |
|---|---|---|
| `--store.url` | `postgres://postgres:password@localhost:5432/composer` | PostgreSQL URL |
| `--redis.addrs` | `localhost:6379` | Redis address |
| `--api.http_port` / `--api.grpc_port` | `8080` / `50051` | API ports |
| `--stream_shards` | `4` | streams per task queue (published to Redis for workers) |
| `--retention_days` | `0` | drop run history for months that ended more than N days ago (`0` keeps everything) |
| `--outbox.poll_interval` | `1s` | outbox fallback poll (new rows arrive via `LISTEN/NOTIFY`) |
| `--outbox.batch_size` / `--outbox.concurrency` | `50` / `2` | outbox publishing |
| `--disable-observability` | `false` | turn off metrics and trace export |

### Worker

| Variable | Default | Meaning |
|---|---|---|
| `COORDINATOR_ADDR` | `localhost:50051` | coordinator gRPC address |
| `REDIS_ADDR` | `127.0.0.1:6379` | Redis address |
| `LANG_WORKER_ADDRS` | — | language servers, e.g. `python:host:50053,js:host:50052` |
| `WORKER_QUEUES` | `http` + each configured runtime | queues to consume, comma-separated |
| `WORKER_CONCURRENCY` | `10` | tasks executed in parallel |
| `LEASE_TTL_SECONDS` | `90` | task lease TTL (renewed every TTL/3) |
| `WORKER_ID` | hostname + random suffix | consumer name and lease owner; must be unique |
| `CONSUMER_GROUP` | `workflow-workers` | Redis consumer group |
| `STREAM_SHARDS` | `4` | fallback if the coordinator has not published its shard count |

### Language servers

| Server | Variable | Default |
|---|---|---|
| Python | `PORT`, `LOG_FILE` (optional) | `50051` |
| JavaScript | `PORT`, `MAX_CONCURRENT_ISOLATES` | `50052`, `4` |

---

## Project layout

```
cmd/
  coordinator/          coordinator binary (flags, bootstrap)
  worker/               worker binary (queues, streams, concurrency)
  language-server-py/   Python sandbox server (RestrictedPython + audit hooks)
  language-server-js/   JavaScript sandbox server (isolated-vm)
pkg/
  statemachine/         the workflow state machine: events, state, actions (pure)
  coord/                coordinator shell: engine, persister, repository, definitions, runs
  model/                definitions, DAG index, run context, statuses
  template/             CEL template resolver
  lease/                task leases
  outbox/               outbox enqueuer (LISTEN/NOTIFY + polling)
  stream/               stream abstraction, queue/shard naming, Redis implementation
  api/                  HTTP (REST) and gRPC servers, protobuf definitions
worker/                 worker runtime: readers, executor pool, executors, coordinator client
migrations/             database schema (partitions, triggers, seed data)
web/                    Next.js web UI
infra/                  Docker Compose, Helm chart, bootstrap scripts, observability config
tests/                  end-to-end tests against real PostgreSQL and Redis (Testcontainers)
docs/diagrams/          architecture diagrams (Excalidraw sources + SVG)
benchmark/              k6 load tests
```

---

## Development and testing

```bash
go test -short ./...                         # unit tests only (no Docker needed)
make test                                    # everything, incl. container-backed tests (needs Docker)
go test ./tests/ -run TestEngine -v          # engine scenarios end to end

(cd cmd/language-server-py && uv run python -m unittest test_sandbox -v)   # Python sandbox
(cd cmd/language-server-js && npm test)                                    # JS sandbox (Node 22)
```

| Suite | What it covers |
|---|---|
| `pkg/statemachine` | every engine rule, event by event, against an in-memory snapshot |
| `pkg/model`, `pkg/template`, `pkg/stream` | DAG index, template references, CEL resolution, queue sharding |
| `pkg/coord`, `pkg/api/...`, `pkg/outbox`, `pkg/auth` | query building, run ID validation, error mapping, request validation, outbox backoff, JWT |
| `worker` | the worker pipeline against an in-memory stream and a fake coordinator |
| `tests/` | full system: fork/join, switches, retries, waits, signals, queues, lease recovery, NOTIFY latency, partition retention |
| sandbox suites | known escape techniques are blocked; ordinary code keeps working |

Regenerate gRPC code after changing `pkg/api/proto/*.proto` with `make proto`.
