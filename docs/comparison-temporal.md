# Composer vs Temporal — Architecture & Capability Comparison

## Core architecture difference

**Temporal** uses **event sourcing + durable execution**. Every workflow is a replay-safe function: the coordinator records every event (task scheduled, task completed, timer fired) to an append-only history. On worker restart, the workflow function is re-executed from scratch against that history — it "fast-forwards" to the current state deterministically. The workflow itself is code that can sleep for months.

**Composer** uses a **DAG state machine with a task queue**. The coordinator persists the DAG execution state in Postgres, enqueues task nodes onto a Redis stream, and workers pull and execute them. State is explicit (node statuses in DB), not implicit in a replay log.

---

## Reliability

| Scenario | Temporal | Composer |
|---|---|---|
| Worker crash mid-task | Activity heartbeat timeout → retry on another worker | Lease TTL expires → XAUTOCLAIM reclaims → retry |
| Coordinator crash | History is durable in Cassandra/Postgres; replay resumes | Outbox retries + DB state is durable; resumes cleanly |
| Message lost from queue | Can't happen — queue is derived from history | Redis AOF/RDB + outbox pattern prevents loss |
| Poison-pill task | Per-activity retry policy with backoff + dead-letter workflow | MaxDeliveries DLQ — same concept |
| Exactly-once execution | History deduplication makes this possible | At-least-once + lease idempotency — not exactly-once |

**The gap**: Temporal's event-sourced history is the canonical source of truth. Composer uses an outbox + Redis stream in two phases: if the coordinator writes to the outbox but the stream consumer processes it twice before the first ack lands, you could in theory schedule the same node twice. The lease (`AcquireTaskLease`) is the idempotency guard — it works, but it's a separate distributed lock rather than structural deduplication.

---

## Long-running flows

This is where Temporal has a fundamental advantage:

- A Temporal workflow can `sleep(365 days)` — the timer is stored in the history, survives all restarts, costs nothing while sleeping.
- Workflows can `waitForSignal()` — block indefinitely waiting for an external event.
- Workflows can have child workflows, callbacks, and queries at any point in their lifetime.
- History is compressed but workflows can run for years as long as event counts stay manageable.

**Composer today**:

- No timer nodes — a workflow blocks until all active nodes complete or fail.
- No external signal/wait mechanism.
- A workflow that needs to wait for a human approval or a webhook would need to poll or keep a node alive.
- The 90-second task timeout is a hard ceiling on any single step.

If a workflow needs to wait for a payment webhook that could arrive anywhere from 1 second to 30 days, Temporal handles it natively. Composer would need a polling HTTP node or a new "wait for signal" node type.

---

## Speed / throughput

Composer is faster for short-lived, high-throughput workflows:

- Redis stream dispatch is microseconds; worker latency is dominated by the actual task (HTTP call, code execution).
- No history replay overhead — the DAG state machine is O(nodes), not O(history events).
- The http-simple workflow runs at ~5ms expected duration. Temporal's minimum overhead for a single activity is typically 10–50ms due to history writes.
- Temporal's Cassandra/Postgres history writes are on the critical path for every event; Composer's Postgres writes are only on task completion.

Temporal's throughput ceiling is around 50k–200k workflow actions/sec per namespace on a well-tuned cluster. Composer's ceiling is Redis throughput (millions of ops/sec) bounded by worker count and task execution time.

---

## Operational complexity

| | Temporal | Composer |
|---|---|---|
| Infrastructure | Temporal server cluster + Cassandra or Postgres + Elasticsearch (visibility) | Redis + Postgres (already in stack) |
| Versioning | Built-in `workflow.GetVersion()` for in-flight migration | No versioning — changing a workflow definition mid-run is undefined |
| Visibility / search | Elasticsearch-backed workflow search, filter by status/type/attribute | Query against `workflow_runs` table |
| Debugging | Web UI with full event history, replay in local dev | Grafana dashboard + structured logs |
| SDK | Go, Java, Python, TypeScript, .NET | Custom node types (HTTP, CODE, SWITCH, FORK) |

---

## Summary

| Dimension | Temporal | Composer |
|---|---|---|
| Reliability | Higher — history is source of truth, exactly-once possible | Good — at-least-once + lease idempotency |
| Long-running flows | First-class — years, signals, timers | Limited — no sleep / signal primitives |
| Throughput (short tasks) | ~10–50ms floor per step | ~1–5ms floor per step |
| Operational cost | High — separate cluster, steep learning curve | Low — reuses existing Redis + Postgres |
| DAG flexibility | Arbitrary code, branching in workflow function | Explicit SWITCH / FORK nodes, YAML-defined |
| Versioning | Built-in | Not yet implemented |

---

## Gaps to close

The two biggest areas where Temporal wins decisively that Composer could address:

1. **Timer / sleep nodes** — a `WAIT` node type that suspends a workflow branch until a deadline, backed by a Postgres-scheduled job rather than keeping a stream message alive.
2. **External signal / wait primitives** — a `WAIT_FOR_EVENT` node type that blocks until an external HTTP call or webhook delivers a named signal to a running workflow instance.

Closing these two gaps would cover the majority of long-running workflow use cases without requiring a full event-sourcing rewrite.
