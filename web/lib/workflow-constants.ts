// Sample nodes for new workflows
export const SAMPLE_NODES = [
  {
    node_id: "start_node",
    name: "Start",
    type: "TASK",
    runtime: "http",
    spec: {
      url: "https://api.example.com/start",
      method: "GET",
      timeout_seconds: 30,
    },
  },
  {
    node_id: "process_node",
    name: "Process",
    type: "TASK",
    runtime: "javascript",
    spec: {
      code: "function handler(input) {\n  // Process the input\n  return input;\n}",
    },
  },
];

export const DEFAULT_TEMPLATE = JSON.stringify(
  {
    nodes: [],
    edges: [],
  },
  null,
  2
);

export const SAMPLE_TEMPLATE = JSON.stringify(
  {
    nodes: SAMPLE_NODES,
    edges: [
      { from: "start_node", to: "process_node" },
    ],
  },
  null,
  2
);
