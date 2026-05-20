/**
 * Smoke test — 1 VU, ~60s
 *
 * Verifies the full setup path (login → create → deploy → execute → poll)
 * works end-to-end for every workflow type before running a real load test.
 * Run this first after `docker compose up -d`.
 */
import { sleep } from 'k6';
import { login }          from './lib/auth.js';
import { setupWorkflows } from './lib/workflows.js';
import { runWorkflow }    from './lib/executor.js';
import { workflowDuration, workflowSucceeded, workflowFailed } from './lib/executor.js';

export const options = {
  vus:      1,
  duration: '60s',
  thresholds: {
    // Every single execution must succeed in smoke mode
    workflow_success_rate:  ['rate==1.0'],
    workflow_failures:      ['count==0'],
    workflow_duration_ms:   ['p(99)<30000'],
  },
};

// setup() runs once before any VU starts
export function setup() {
  const token       = login();
  const workflowIds = setupWorkflows(token);
  console.log(`[smoke] setup complete — ${workflowIds.length} workflows deployed`);
  return { token, workflowIds };
}

export default function (data) {
  const { token, workflowIds } = data;

  // Run one of each workflow type in round-robin order
  const idx = (__ITER % workflowIds.length);
  const wfId = workflowIds[idx];

  const { status, durationMs } = runWorkflow(token, wfId);
  console.log(`[smoke] iter=${__ITER} workflow=${wfId} status=${status} duration=${durationMs}ms`);

  sleep(1);
}

export function teardown(data) {
  console.log('[smoke] done');
}
