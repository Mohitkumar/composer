'use client';

import { useCallback, useMemo, useState } from 'react';
import ReactFlow, {
  Background,
  Controls,
  MiniMap,
  addEdge,
  useNodesState,
  useEdgesState,
  Connection,
  Edge,
  Node,
  NodeTypes,
  Handle,
  Position,
} from 'reactflow';
import dagre from 'dagre';
import 'reactflow/dist/style.css';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { Plus, Save, Play, Layout } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import type { WorkflowDefinitionSpec, Node as WorkflowNode } from '@/lib/api-client';
import { NodeConfigForm } from './node-config-form';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Separator } from '@/components/ui/separator';
import { getNodeFormSchema } from '@/lib/node-schemas';

// Custom Node Component
function CustomNode({ data, selected }: { data: any; selected: boolean }) {
  return (
    <div
      className={`px-3 py-2 shadow-lg rounded-lg bg-white border-2 w-[180px] ${
        selected ? 'border-blue-500 shadow-blue-200' : 'border-gray-300'
      }`}
    >
      <Handle type="target" position={Position.Left} className="w-3 h-3 !bg-blue-500 hover:!bg-blue-600" />
      <div className="flex flex-col gap-1.5">
        <div className="flex items-center justify-center">
          <Badge variant="outline" className="text-xs">{data.runtime || 'N/A'}</Badge>
        </div>
        <div className="font-semibold text-sm text-center truncate" title={data.label}>
          {data.label}
        </div>
      </div>
      <Handle type="source" position={Position.Right} className="w-3 h-3 !bg-blue-500 hover:!bg-blue-600" />
    </div>
  );
}

const nodeTypes: NodeTypes = {
  custom: CustomNode,
};

// Layout function using dagre - horizontal layout
function getLayoutedElements(nodes: Node[], edges: Edge[]) {
  if (nodes.length === 0) {
    return { nodes, edges };
  }

  const dagreGraph = new dagre.graphlib.Graph();
  dagreGraph.setDefaultEdgeLabel(() => ({}));
  dagreGraph.setGraph({ 
    rankdir: 'LR', // Left to right (horizontal)
    nodesep: 80, // Vertical spacing between nodes in same rank
    ranksep: 200, // Horizontal spacing between ranks (columns) - reduced for compact layout
    marginx: 50,
    marginy: 50,
    align: 'UL', // Align nodes to upper left
    acyclicer: 'greedy', // Handle cycles if any
    ranker: 'tight-tree', // Better layout algorithm for compact graphs
    edgesep: 20, // Minimum separation between edges
  });

  const nodeWidth = 180;
  const nodeHeight = 90;

  nodes.forEach((node) => {
    dagreGraph.setNode(node.id, { 
      width: nodeWidth, 
      height: nodeHeight 
    });
  });

  edges.forEach((edge) => {
    dagreGraph.setEdge(edge.source, edge.target);
  });

  dagre.layout(dagreGraph);

  const layoutedNodes = nodes.map((node) => {
    const nodeWithPosition = dagreGraph.node(node.id);
    return {
      ...node,
      targetPosition: Position.Left, // Input on left for horizontal layout
      sourcePosition: Position.Right, // Output on right for horizontal layout
      position: {
        x: nodeWithPosition.x - nodeWidth / 2, // Center the node
        y: nodeWithPosition.y - nodeHeight / 2,
      },
    };
  });

  return { nodes: layoutedNodes, edges };
}

interface WorkflowEditorProps {
  initialSpec?: WorkflowDefinitionSpec;
  workflowId?: string;
  workflowName?: string;
  onSave: (spec: WorkflowDefinitionSpec, name: string, id: string) => Promise<void>;
  isSaving?: boolean;
}

export function WorkflowEditor({
  initialSpec,
  workflowId,
  workflowName,
  onSave,
  isSaving = false,
}: WorkflowEditorProps) {
  const initialNodes = initialSpec?.nodes?.map((node) => ({
    id: node.node_id,
    type: 'custom',
    position: { x: 0, y: 0 }, // Will be calculated by layout
    data: {
      label: node.name,
      runtime: node.runtime,
      nodeData: node,
    },
  })) || [];

  const initialEdges = initialSpec?.edges?.map((edge) => ({
    id: `${edge.from}-${edge.to}`,
    source: edge.from,
    target: edge.to,
    type: 'smoothstep', // Better looking edges
    animated: false,
    style: { strokeWidth: 2 },
    pathOptions: {
      borderRadius: 10,
    },
  })) || [];

  // Apply layout to initial nodes
  const { nodes: layoutedNodes, edges: layoutedEdges } = useMemo(() => {
    if (initialNodes.length === 0) {
      return { nodes: initialNodes, edges: initialEdges };
    }
    return getLayoutedElements(initialNodes, initialEdges);
  }, [initialNodes.length, initialEdges.length]);

  const [nodes, setNodes, onNodesChange] = useNodesState(layoutedNodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(layoutedEdges);
  const [selectedNode, setSelectedNode] = useState<Node | null>(null);
  const [nodeConfigOpen, setNodeConfigOpen] = useState(false);
  const [nodeName, setNodeName] = useState('');
  const [nodeType, setNodeType] = useState<'TASK' | 'SWITCH' | 'PARALLEL' | 'WAIT'>('TASK');
  const [nodeRuntime, setNodeRuntime] = useState('http');
  const [nodeSpec, setNodeSpec] = useState<Record<string, any>>({});
  const [workflowNameState, setWorkflowNameState] = useState(workflowName || '');
  const [workflowIdState, setWorkflowIdState] = useState(workflowId || '');

  const onConnect = useCallback(
    (params: Connection) => {
      const newEdge = {
        ...params,
        id: `${params.source}-${params.target}`,
        type: 'smoothstep',
        animated: false,
        style: { strokeWidth: 2 },
        pathOptions: {
          borderRadius: 10,
        },
      };
      const newEdges = addEdge(newEdge, edges);
      const { nodes: layoutedNodes, edges: layoutedEdges } = getLayoutedElements(nodes, newEdges);
      setNodes(layoutedNodes);
      setEdges(layoutedEdges);
    },
    [nodes, edges, setNodes, setEdges]
  );

  const addNode = () => {
    const newNodeId = `node_${Date.now()}`;
    const newNode: Node = {
      id: newNodeId,
      type: 'custom',
      position: { x: 0, y: 0 }, // Will be positioned by layout
      data: {
        label: 'New Node',
        runtime: 'http',
        nodeData: {
          node_id: newNodeId,
          name: 'New Node',
          type: 'TASK',
          runtime: 'http',
          spec: {},
        },
      },
    };
    const newNodes = [...nodes, newNode];
    const { nodes: layoutedNewNodes, edges: layoutedNewEdges } = getLayoutedElements(newNodes, edges);
    setNodes(layoutedNewNodes);
    setEdges(layoutedNewEdges);
  };

  const handleNodeClick = (_event: React.MouseEvent, node: Node) => {
    setSelectedNode(node);
    const nodeData = node.data.nodeData as WorkflowNode;
    setNodeName(nodeData.name);
    setNodeType(nodeData.type);
    setNodeRuntime(nodeData.runtime);
    // Parse spec if it's a string, otherwise use as-is
    if (typeof nodeData.spec === 'string') {
      try {
        setNodeSpec(JSON.parse(nodeData.spec));
      } catch {
        setNodeSpec({});
      }
    } else {
      setNodeSpec(nodeData.spec || {});
    }
    setNodeConfigOpen(true);
  };

  const updateNode = () => {
    if (!selectedNode) return;

    if (!nodeName.trim()) {
      alert('Node name is required');
      return;
    }

    const updatedNodeData: WorkflowNode = {
      node_id: selectedNode.id,
      name: nodeName,
      type: nodeType,
      runtime: nodeRuntime,
      spec: nodeSpec,
    };

    const updatedNodes = nodes.map((node) =>
      node.id === selectedNode.id
        ? {
            ...node,
            data: {
              label: nodeName,
              runtime: nodeRuntime,
              nodeData: updatedNodeData,
            },
          }
        : node
    );

    // Recalculate layout after update
    const { nodes: layoutedNodes, edges: layoutedEdges } = getLayoutedElements(updatedNodes, edges);
    setNodes(layoutedNodes);
    setEdges(layoutedEdges);

    setNodeConfigOpen(false);
    setSelectedNode(null);
  };

  const deleteNode = () => {
    if (!selectedNode) return;
    const newNodes = nodes.filter((node) => node.id !== selectedNode.id);
    const newEdges = edges.filter(
      (edge) => edge.source !== selectedNode.id && edge.target !== selectedNode.id
    );
    const { nodes: layoutedNodes, edges: layoutedEdges } = getLayoutedElements(newNodes, newEdges);
    setNodes(layoutedNodes);
    setEdges(layoutedEdges);
    setNodeConfigOpen(false);
    setSelectedNode(null);
  };

  const handleSave = async () => {
    const spec: WorkflowDefinitionSpec = {
      nodes: nodes.map((node) => node.data.nodeData as WorkflowNode),
      edges: edges.map((edge) => ({
        from: edge.source,
        to: edge.target,
      })),
    };

    const id = workflowIdState || `workflow_${Date.now()}`;
    await onSave(spec, workflowNameState, id);
  };

  // Button handler that recalculates layout before saving
  const handleSaveWithLayout = async () => {
    const { nodes: layoutedNodes, edges: layoutedEdges } = getLayoutedElements(nodes, edges);
    setNodes(layoutedNodes);
    setEdges(layoutedEdges);
    
    // Use the layouted nodes/edges for saving
    const spec: WorkflowDefinitionSpec = {
      nodes: layoutedNodes.map((node) => node.data.nodeData as WorkflowNode),
      edges: layoutedEdges.map((edge) => ({
        from: edge.source,
        to: edge.target,
      })),
    };

    const id = workflowIdState || `workflow_${Date.now()}`;
    await onSave(spec, workflowNameState, id);
  };

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between p-4 border-b">
        <div className="flex items-center gap-4 flex-1">
          <div className="flex-1 max-w-xs">
            <Label htmlFor="workflow-name">Workflow Name</Label>
            <Input
              id="workflow-name"
              value={workflowNameState}
              onChange={(e) => setWorkflowNameState(e.target.value)}
              placeholder="Enter workflow name"
            />
          </div>
          {!workflowId && (
            <div className="flex-1 max-w-xs">
              <Label htmlFor="workflow-id">Workflow ID</Label>
              <Input
                id="workflow-id"
                value={workflowIdState}
                onChange={(e) => setWorkflowIdState(e.target.value)}
                placeholder="Enter workflow ID"
              />
            </div>
          )}
        </div>
        <div className="flex gap-2">
          <Button 
            onClick={addNode} 
            variant="outline"
            title="Add a new node to the workflow"
          >
            <Plus className="mr-2 h-4 w-4" />
            Add Node
          </Button>
          <Button 
            onClick={() => {
              const { nodes: layoutedNodes, edges: layoutedEdges } = getLayoutedElements(nodes, edges);
              setNodes(layoutedNodes);
              setEdges(layoutedEdges);
            }}
            variant="outline"
            title="Auto-arrange nodes in a hierarchical layout"
          >
            <Layout className="mr-2 h-4 w-4" />
            Auto Layout
          </Button>
          <Button 
            onClick={handleSaveWithLayout}
            title="Save the workflow"
            disabled={isSaving}
          >
            {isSaving ? (
              <>
                <div className="mr-2 h-4 w-4 animate-spin rounded-full border-2 border-background border-t-transparent" />
                Saving...
              </>
            ) : (
              <>
                <Save className="mr-2 h-4 w-4" />
                Save Workflow
              </>
            )}
          </Button>
        </div>
      </div>

      <div className="flex-1 relative">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          onNodeClick={handleNodeClick}
          nodeTypes={nodeTypes}
          fitView
          fitViewOptions={{ padding: 0.2, maxZoom: 1.5 }}
          defaultEdgeOptions={{ 
            type: 'smoothstep',
            style: { strokeWidth: 2 },
          }}
          nodesDraggable={true}
          nodesConnectable={true}
          snapToGrid={true}
          snapGrid={[20, 20]}
        >
          <Background />
          <Controls />
          <MiniMap />
        </ReactFlow>
      </div>

      <Dialog open={nodeConfigOpen} onOpenChange={setNodeConfigOpen}>
        <DialogContent className="max-w-[90vw] w-[90vw] max-h-[95vh] flex flex-col sm:max-w-[90vw]">
          <DialogHeader className="flex-shrink-0">
            <DialogTitle>Configure Node</DialogTitle>
            <DialogDescription>
              Edit the properties and configuration of the selected node
            </DialogDescription>
          </DialogHeader>
          
          <Tabs defaultValue="basic" className="flex-1 flex flex-col min-h-0">
            <TabsList className="grid w-full grid-cols-2 flex-shrink-0">
              <TabsTrigger value="basic">Basic Settings</TabsTrigger>
              <TabsTrigger value="config">Configuration</TabsTrigger>
            </TabsList>
            
            <TabsContent value="basic" className="flex-1 overflow-y-auto space-y-4 mt-4 min-h-0">
              <div className="space-y-2">
                <Label htmlFor="node-name">
                  Node Name
                  <span className="text-destructive ml-1">*</span>
                </Label>
                <Input
                  id="node-name"
                  value={nodeName}
                  onChange={(e) => setNodeName(e.target.value)}
                  placeholder="Enter node name"
                />
              </div>
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-2">
                  <Label htmlFor="node-type">Node Type</Label>
                  <Select value={nodeType} onValueChange={(v: any) => setNodeType(v)}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="TASK">Task</SelectItem>
                      <SelectItem value="SWITCH">Switch</SelectItem>
                      <SelectItem value="PARALLEL">Parallel</SelectItem>
                      <SelectItem value="WAIT">Wait</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-2">
                  <Label htmlFor="node-runtime">Runtime</Label>
                  <Select 
                    value={nodeRuntime} 
                    onValueChange={(newRuntime) => {
                      setNodeRuntime(newRuntime);
                      // Reset spec when runtime changes to avoid invalid configurations
                      const newSchema = getNodeFormSchema(newRuntime);
                      const defaultSpec: Record<string, any> = {};
                      newSchema.fields.forEach((field) => {
                        if (field.defaultValue !== undefined) {
                          defaultSpec[field.key] = field.defaultValue;
                        }
                      });
                      setNodeSpec(defaultSpec);
                    }}
                  >
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="http">HTTP</SelectItem>
                      <SelectItem value="javascript">JavaScript</SelectItem>
                      <SelectItem value="python">Python</SelectItem>
                      <SelectItem value="go">Go</SelectItem>
                      <SelectItem value="sql">SQL</SelectItem>
                      <SelectItem value="s3">S3</SelectItem>
                      <SelectItem value="csv">CSV</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              </div>
            </TabsContent>
            
            <TabsContent value="config" className="flex-1 overflow-y-auto mt-4 min-h-0">
              <NodeConfigForm
                runtime={nodeRuntime}
                spec={nodeSpec}
                onChange={(newSpec) => setNodeSpec(newSpec)}
                nodeIds={nodes.map((n) => n.id)}
              />
            </TabsContent>
          </Tabs>

          <Separator className="my-4 flex-shrink-0" />
          
          <div className="flex justify-between flex-shrink-0">
            <Button variant="destructive" onClick={deleteNode}>
              Delete Node
            </Button>
            <div className="flex gap-2">
              <Button variant="outline" onClick={() => setNodeConfigOpen(false)}>
                Cancel
              </Button>
              <Button onClick={updateNode}>Save Node</Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
