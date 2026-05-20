import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Rate, Counter } from 'k6/metrics';
import { authHeaders } from './auth.js';

const BASE_URL = __ENV.BASE_URL || 'http://coordinator:8080';

// ---------------------------------------------------------------------------
// Custom metrics
// ---------------------------------------------------------------------------
export const workflowDuration  = new Trend('workflow_duration_ms', true);
export const workflowSucceeded = new Rate('workflow_success_rate');
export const workflowFailed    = new Counter('workflow_failures');
export const workflowCompleted = new Counter('workflow_completions');

// ---------------------------------------------------------------------------
// Execution helpers
// ---------------------------------------------------------------------------

/**
 * Submit a workflow execution and return the run_id.
 * Returns null on failure.
 */
export function executeWorkflow(token, workflowId, input) {
  input = input || { benchmark: true, ts: Date.now() };

  const res = http.post(
    `${BASE_URL}/api/v1/workflows/${workflowId}/execute`,
    JSON.stringify({ input }),
    { headers: authHeaders(token), timeout: '10s' }
  );

  const ok = check(res, { 'execute → 202': (r) => r.status === 202 });
  if (!ok) {
    console.warn(`[executor] execute ${workflowId} → ${res.status}: ${res.body}`);
    workflowFailed.add(1);
    workflowSucceeded.add(false);
    return null;
  }

  return JSON.parse(res.body).run_id;
}

/**
 * Poll workflow status until COMPLETED/SUCCEEDED/FAILED or timeout.
 * Returns the final status string, or 'TIMEOUT'.
 */
export function pollStatus(token, workflowId, runId, opts) {
  opts = opts || {};
  const maxWaitMs    = opts.maxWaitMs    || 60000;   // 60s ceiling
  const intervalMs   = opts.intervalMs   || 500;
  const backoffAfter = opts.backoffAfter || 10;      // after N polls, slow down

  const start = Date.now();
  let polls   = 0;

  while (Date.now() - start < maxWaitMs) {
    polls++;

    const res = http.get(
      `${BASE_URL}/api/v1/workflows/${workflowId}/status?run_id=${runId}`,
      { headers: authHeaders(token), timeout: '5s' }
    );

    check(res, { 'poll status → 200': (r) => r.status === 200 });

    if (res.status === 200) {
      const body = JSON.parse(res.body);
      const status = body.status;

      if (status === 'COMPLETED' || status === 'SUCCEEDED') {
        return status;
      }
      if (status === 'FAILED') {
        return 'FAILED';
      }
    }

    // Slow down after backoffAfter polls to reduce poll pressure under long workflows
    const wait = polls >= backoffAfter ? intervalMs * 2 : intervalMs;
    sleep(wait / 1000);
  }

  return 'TIMEOUT';
}

/**
 * Execute a workflow and poll for completion. Records all custom metrics.
 * Returns { status, durationMs }.
 */
export function runWorkflow(token, workflowId, input, pollOpts) {
  const start = Date.now();

  const runId = executeWorkflow(token, workflowId, input);
  if (!runId) {
    return { status: 'EXEC_FAILED', durationMs: Date.now() - start };
  }

  const status = pollStatus(token, workflowId, runId, pollOpts);
  const durationMs = Date.now() - start;

  workflowDuration.add(durationMs);
  workflowCompleted.add(1);

  const succeeded = status === 'COMPLETED' || status === 'SUCCEEDED';
  workflowSucceeded.add(succeeded);
  if (!succeeded) {
    workflowFailed.add(1);
    console.warn(`[executor] ${workflowId} run=${runId} ended as ${status} in ${durationMs}ms`);
  }

  return { status, durationMs };
}

// ---------------------------------------------------------------------------
// Weighted random workflow picker
// ---------------------------------------------------------------------------

/**
 * Pick a workflow ID according to weights. Weights don't need to sum to 1.
 * workflowIds and weights arrays must be same length.
 *
 * Default distribution mirrors real-world patterns:
 *   simple 30% | chain 25% | switch 20% | fork 15% | mixed 10%
 */
export function pickWorkflow(workflowIds, weights) {
  weights = weights || [30, 25, 20, 15, 10];
  const total = weights.reduce((a, b) => a + b, 0);
  let r = Math.random() * total;
  for (let i = 0; i < workflowIds.length; i++) {
    r -= weights[i];
    if (r <= 0) return workflowIds[i];
  }
  return workflowIds[workflowIds.length - 1];
}
