/**
 * Load test — realistic sustained load with gradual ramp-up.
 *
 * Stages:
 *   0→5 min:  ramp 0→20 VUs   (warm up, find baseline)
 *   5→15 min: hold 20 VUs     (steady-state throughput)
 *   15→20min: ramp 20→50 VUs  (push harder)
 *   20→30min: hold 50 VUs     (peak load)
 *   30→35min: ramp 50→0 VUs   (cool down)
 *
 * Each VU picks a workflow by weighted random (simple 30%, chain 25%,
 * switch 20%, fork 15%, mixed 10%), executes it, polls for completion,
 * then immediately starts the next iteration. This maximises throughput
 * measurement while keeping each VU's behaviour realistic.
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
    { duration: '5m',  target: 20 },
    { duration: '10m', target: 20 },
    { duration: '5m',  target: 50 },
    { duration: '10m', target: 50 },
    { duration: '5m',  target: 0  },
  ],
  thresholds: {
    // SLOs — adjust to match your deployment targets
    workflow_success_rate: ['rate>0.95'],           // ≥95% of executions complete
    workflow_duration_ms:  ['p(50)<5000', 'p(95)<15000', 'p(99)<30000'],
    workflow_failures:     ['count<100'],
    http_req_failed:       ['rate<0.01'],           // <1% raw HTTP errors
    http_req_duration:     ['p(95)<2000'],          // API calls answer in <2s
  },
};

export function setup() {
  const token       = login();
  const workflowIds = setupWorkflows(token);
  console.log(`[load] setup complete — ${workflowIds.length} workflows ready`);
  return { token, workflowIds };
}

export default function (data) {
  const { token, workflowIds } = data;

  // Weighted pick: simple workflows run more often (cheaper), complex ones run less
  const wfId = pickWorkflow(workflowIds, [30, 25, 20, 15, 10]);

  runWorkflow(token, wfId, { benchmark: true, vu: __VU, iter: __ITER }, {
    maxWaitMs:    45000,
    intervalMs:   500,
    backoffAfter: 10,
  });

  // No sleep between iterations — the poll loop acts as natural pacing.
  // Remove this if you want to cap per-VU rate explicitly.
}

export function handleSummary(data) {
  const completions = data.metrics.workflow_completions
    ? data.metrics.workflow_completions.values.count : 0;
  const successRate = data.metrics.workflow_success_rate
    ? (data.metrics.workflow_success_rate.values.rate * 100).toFixed(1) : 'n/a';
  const p50 = data.metrics.workflow_duration_ms
    ? data.metrics.workflow_duration_ms.values['p(50)'].toFixed(0) : 'n/a';
  const p95 = data.metrics.workflow_duration_ms
    ? data.metrics.workflow_duration_ms.values['p(95)'].toFixed(0) : 'n/a';
  const p99 = data.metrics.workflow_duration_ms
    ? data.metrics.workflow_duration_ms.values['p(99)'].toFixed(0) : 'n/a';

  console.log('\n========== LOAD TEST SUMMARY ==========');
  console.log(`Total workflow executions : ${completions}`);
  console.log(`Success rate              : ${successRate}%`);
  console.log(`Duration p50 / p95 / p99  : ${p50}ms / ${p95}ms / ${p99}ms`);
  console.log('=========================================\n');

  return {
    'stdout': '\n' + JSON.stringify(data.metrics, null, 2),
  };
}
