// Sample nodes for new workflows
export const SAMPLE_NODES = [
  {
    node_id: "start_node",
    name: "Start",
    type: "HTTP",
    spec: {
      url: "https://api.example.com/start",
      method: "GET",
      timeout_seconds: 30,
    },
  },
  {
    node_id: "process_node",
    name: "Process",
    type: "CODE",
    runtime: "javascript",
    spec: {
      code: "function handler(input) {\n  // Process the input\n  return input;\n}",
    },
  },
];

import * as yaml from 'yaml';

export const DEFAULT_TEMPLATE = yaml.stringify(
  {
    nodes: [],
    edges: [],
  },
  { indent: 2 }
);

export const SAMPLE_TEMPLATE = yaml.stringify(
  {
    nodes: SAMPLE_NODES,
    edges: [
      { from: "start_node", to: "process_node" },
    ],
  },
  { indent: 2 }
);
