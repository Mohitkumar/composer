/**
 * Stress / spike test — finds the breaking point.
 *
 * Phase 1 — Spike: instant ramp to 100 VUs to reveal queue saturation,
 *   connection pool exhaustion, and Redis stream backlog.
 * Phase 2 — Sustain: hold to see if the system recovers or degrades.
 * Phase 3 — Spike again: second spike at 150 VUs.
 * Phase 4 — Cool down.
 *
 * Watch for:
 *   - workflow_success_rate dropping below 0.9
 *   - workflow_duration_ms p99 spiking above 60s
 *   - postgres connection errors in coordinator logs
 *   - redis XPENDING growing unboundedly (run: redis-cli XPENDING workflow.tasks workflow-workers - +)
 */
import { sleep } from 'k6';
import { login }          from './lib/auth.js';
import { setupWorkflows } from './lib/workflows.js';
import {
  runWorkflow, pickWorkflow,
  workflowDuration, workflowSucceeded, workflowFailed, workflowCompleted,
} from './lib/executor.js';

export const options = {
  stages: [
    { duration: '30s', target: 100 }, // spike 1
    { duration: '3m',  target: 100 }, // sustain
    { duration: '30s', target: 0   }, // brief recovery
    { duration: '30s', target: 150 }, // spike 2 — harder
    { duration: '3m',  target: 150 }, // sustain
    { duration: '1m',  target: 0   }, // cool down
  ],
  // Looser thresholds — this is about finding limits, not enforcing SLOs
  thresholds: {
    workflow_success_rate: ['rate>0.80'],
    workflow_duration_ms:  ['p(95)<60000'],
    http_req_failed:       ['rate<0.05'],
  },
};

export function setup() {
  const token       = login();
  const workflowIds = setupWorkflows(token);
  console.log(`[stress] setup complete — ${workflowIds.length} workflows ready`);
  return { token, workflowIds };
}

export default function (data) {
  const { token, workflowIds } = data;

  // Under stress, prefer lighter workflows to maximise concurrency
  const wfId = pickWorkflow(workflowIds, [40, 30, 15, 10, 5]);

  runWorkflow(token, wfId, { benchmark: true, stress: true, vu: __VU, iter: __ITER }, {
    maxWaitMs:    90000,  // longer ceiling under load
    intervalMs:   1000,   // poll less aggressively to reduce noise
    backoffAfter: 5,
  });
}

export function handleSummary(data) {
  const completions = data.metrics.workflow_completions
    ? data.metrics.workflow_completions.values.count : 0;
  const failures = data.metrics.workflow_failures
    ? data.metrics.workflow_failures.values.count : 0;
  const successRate = data.metrics.workflow_success_rate
    ? (data.metrics.workflow_success_rate.values.rate * 100).toFixed(1) : 'n/a';
  const p95 = data.metrics.workflow_duration_ms
    ? data.metrics.workflow_duration_ms.values['p(95)'].toFixed(0) : 'n/a';
  const p99 = data.metrics.workflow_duration_ms
    ? data.metrics.workflow_duration_ms.values['p(99)'].toFixed(0) : 'n/a';

  console.log('\n========== STRESS TEST SUMMARY ==========');
  console.log(`Total completions  : ${completions}`);
  console.log(`Failures           : ${failures}`);
  console.log(`Success rate       : ${successRate}%`);
  console.log(`Duration p95 / p99 : ${p95}ms / ${p99}ms`);
  console.log('==========================================\n');

  return {
    'stdout': '\n' + JSON.stringify(data.metrics, null, 2),
  };
}
