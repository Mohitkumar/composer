import http from 'k6/http';
import { check } from 'k6';
import { authHeaders } from './auth.js';

const BASE_URL = __ENV.BASE_URL || 'http://coordinator:8080';
const MOCK_URL = __ENV.MOCK_SERVER_URL || 'http://mock-server:8080';

// ---------------------------------------------------------------------------
// Workflow specs — pure HTTP node workflows (no language runtime needed).
// Each workflow exercises a different combination of node types.
// ---------------------------------------------------------------------------

/**
 * WORKFLOW 1: http-simple
 * Single HTTP node → /api/fast (~5ms)
 * Purpose: baseline latency — isolates coordinator + worker overhead.
 */
const HTTP_SIMPLE_SPEC = `
nodes:
  - node_id: fetch
    name: Fetch Fast
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/fast"
      method: GET
      timeout_seconds: 10
edges: []
`;

/**
 * WORKFLOW 2: http-chain
 * Linear chain: fetch_data → process → notify (3 serial HTTP nodes)
 * Purpose: measures sequential task scheduling and context propagation.
 * Expected duration: ~85ms (20 + 50 + 15ms)
 */
const HTTP_CHAIN_SPEC = `
nodes:
  - node_id: fetch_data
    name: Fetch Data
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/data"
      method: GET
      timeout_seconds: 10
  - node_id: process
    name: Process Data
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/process"
      method: POST
      body: "{{tasks.fetch_data.output}}"
      timeout_seconds: 10
  - node_id: notify
    name: Notify
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/notify"
      method: POST
      body: "{{tasks.process.output}}"
      timeout_seconds: 10
edges:
  - from: fetch_data
    to: process
  - from: process
    to: notify
`;

/**
 * WORKFLOW 3: switch-branch
 * fetch → SWITCH (on value >= 75) → process_high OR process_low
 * Mock /api/data always returns value=100, so high branch is always taken.
 * Purpose: measures SWITCH node evaluation and conditional routing.
 * Expected duration: ~70ms (20 + 50ms for fetch + process_high)
 */
const SWITCH_BRANCH_SPEC = `
nodes:
  - node_id: fetch_data
    name: Fetch Data
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/data"
      method: GET
      timeout_seconds: 10
  - node_id: route
    name: Route By Value
    type: SWITCH
    spec: {}
  - node_id: process_high
    name: Process High Value
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/process"
      method: POST
      body: "{{tasks.route.output}}"
      timeout_seconds: 10
  - node_id: process_low
    name: Process Low Value
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/enrich"
      method: POST
      body: "{{tasks.route.output}}"
      timeout_seconds: 10
edges:
  - from: fetch_data
    to: route
  - condition: "{{tasks.route.output.value >= 75}}"
    from: route
    to: process_high
  - condition: "{{tasks.route.output.value < 75}}"
    from: route
    to: process_low
`;

/**
 * WORKFLOW 4: fork-join
 * init → FORK → [enrich, validate, transform] (parallel) → aggregate
 * All 3 branches run concurrently; aggregate is the implicit join node.
 * Purpose: measures FORK fan-out, parallel execution, and join synchronisation.
 * Expected duration: ~55ms (20ms init + max(30,10,40)ms parallel + 15ms aggregate)
 */
const FORK_JOIN_SPEC = `
nodes:
  - node_id: init
    name: Initialize
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/data"
      method: GET
      timeout_seconds: 10
  - node_id: split
    name: Fork
    type: FORK
    spec: {}
  - node_id: branch_enrich
    name: Branch Enrich
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/enrich"
      method: POST
      body: "{{tasks.init.output}}"
      timeout_seconds: 10
  - node_id: branch_validate
    name: Branch Validate
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/validate"
      method: POST
      body: "{{tasks.init.output}}"
      timeout_seconds: 10
  - node_id: branch_transform
    name: Branch Transform
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/transform"
      method: POST
      body: "{{tasks.init.output}}"
      timeout_seconds: 10
  - node_id: aggregate
    name: Aggregate Results
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/notify"
      method: POST
      body: "{{tasks.branch_enrich.output}}"
      timeout_seconds: 10
edges:
  - from: init
    to: split
  - from: split
    to: branch_enrich
  - from: split
    to: branch_validate
  - from: split
    to: branch_transform
  - from: branch_enrich
    to: aggregate
  - from: branch_validate
    to: aggregate
  - from: branch_transform
    to: aggregate
`;

/**
 * WORKFLOW 5: mixed
 * fetch → SWITCH → [FORK → [enrich, validate] → process | direct_notify]
 * Mock always routes to the FORK branch (value=100 >= 75).
 * Purpose: exercises all non-code node types together in one workflow.
 * Expected duration: ~90ms (20 + max(30,10)ms parallel + 50ms process)
 */
const MIXED_SPEC = `
nodes:
  - node_id: fetch_data
    name: Fetch Data
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/data"
      method: GET
      timeout_seconds: 10
  - node_id: route
    name: Route Decision
    type: SWITCH
    spec: {}
  - node_id: parallel_fork
    name: Parallel Fork
    type: FORK
    spec: {}
  - node_id: enrich
    name: Enrich
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/enrich"
      method: POST
      body: "{{tasks.fetch_data.output}}"
      timeout_seconds: 10
  - node_id: validate
    name: Validate
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/validate"
      method: POST
      body: "{{tasks.fetch_data.output}}"
      timeout_seconds: 10
  - node_id: process
    name: Process
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/process"
      method: POST
      body: "{{tasks.enrich.output}}"
      timeout_seconds: 10
  - node_id: direct_notify
    name: Direct Notify (low-value branch)
    type: HTTP
    spec:
      url: "${MOCK_URL}/api/notify"
      method: POST
      timeout_seconds: 10
edges:
  - from: fetch_data
    to: route
  - condition: "{{tasks.route.output.value >= 75}}"
    from: route
    to: parallel_fork
  - condition: "{{tasks.route.output.value < 75}}"
    from: route
    to: direct_notify
  - from: parallel_fork
    to: enrich
  - from: parallel_fork
    to: validate
  - from: enrich
    to: process
  - from: validate
    to: process
`;

export const WORKFLOW_DEFINITIONS = [
  { id: 'bench-http-simple',   name: 'Bench HTTP Simple',   spec: HTTP_SIMPLE_SPEC   },
  { id: 'bench-http-chain',    name: 'Bench HTTP Chain',    spec: HTTP_CHAIN_SPEC    },
  { id: 'bench-switch-branch', name: 'Bench Switch Branch', spec: SWITCH_BRANCH_SPEC },
  { id: 'bench-fork-join',     name: 'Bench Fork Join',     spec: FORK_JOIN_SPEC     },
  { id: 'bench-mixed',         name: 'Bench Mixed',         spec: MIXED_SPEC         },
];

// ---------------------------------------------------------------------------
// Registration helpers
// ---------------------------------------------------------------------------

export function createWorkflow(token, wf) {
  const res = http.post(
    `${BASE_URL}/api/v1/workflows`,
    JSON.stringify({ workflow_id: wf.id, name: wf.name, version: 1, spec: wf.spec }),
    { headers: authHeaders(token), timeout: '15s' }
  );

  const ok = check(res, { [`create ${wf.id} 201`]: (r) => r.status === 201 });
  if (!ok) {
    console.warn(`[workflows] create ${wf.id} → ${res.status}: ${res.body}`);
  }
  return ok;
}

export function deployWorkflow(token, id) {
  const res = http.post(
    `${BASE_URL}/api/v1/workflows/${id}/deploy?version=1`,
    null,
    { headers: authHeaders(token), timeout: '15s' }
  );

  const ok = check(res, { [`deploy ${id} 200`]: (r) => r.status === 200 });
  if (!ok) {
    console.warn(`[workflows] deploy ${id} → ${res.status}: ${res.body}`);
  }
  return ok;
}

/**
 * Creates and deploys all benchmark workflows. Safe to call multiple times —
 * duplicate workflow creation is silently ignored (coordinator upserts).
 * Returns the list of deployed workflow IDs.
 */
export function setupWorkflows(token) {
  const deployed = [];
  for (const wf of WORKFLOW_DEFINITIONS) {
    createWorkflow(token, wf);
    if (deployWorkflow(token, wf.id)) {
      deployed.push(wf.id);
    }
  }
  console.log(`[workflows] ${deployed.length}/${WORKFLOW_DEFINITIONS.length} workflows ready`);
  return deployed;
}
