'use client';

import React, { useMemo, useCallback } from 'react';
import ReactFlow, {
  Node,
  Edge,
  Background,
  Controls,
  MiniMap,
  Connection,
  addEdge,
  useNodesState,
  useEdgesState,
  MarkerType,
  Position,
  Handle,
} from 'reactflow';
import 'reactflow/dist/style.css';
import type { WorkflowDefinitionSpec } from '@/lib/api-client';
import { Badge } from '@/components/ui/badge';

interface WorkflowGraphProps {
  spec: WorkflowDefinitionSpec;
}

// Color mapping for different node types
const nodeTypeColors: Record<string, { bg: string; border: string; text: string }> = {
  CODE: { bg: 'bg-blue-50', border: 'border-blue-300', text: 'text-blue-700' },
  HTTP: { bg: 'bg-green-50', border: 'border-green-300', text: 'text-green-700' },
  SWITCH: { bg: 'bg-yellow-50', border: 'border-yellow-300', text: 'text-yellow-700' },
  FORK: { bg: 'bg-orange-50', border: 'border-orange-300', text: 'text-orange-700' },
  JOIN: { bg: 'bg-pink-50', border: 'border-pink-300', text: 'text-pink-700' },
  WAIT: { bg: 'bg-gray-50', border: 'border-gray-300', text: 'text-gray-700' },
};

// Custom node component
const CustomNode = ({ data }: { data: any }) => {
  const colors = nodeTypeColors[data.type] || nodeTypeColors.CODE;
  
  return (
    <div className={`px-4 py-3 rounded-lg border-2 ${colors.bg} ${colors.border} shadow-sm min-w-[180px] relative`}>
      {/* Input handle (left side) */}
      <Handle
        type="target"
        position={Position.Left}
        style={{ background: '#555', width: 8, height: 8 }}
      />
      
      <div className="flex items-start justify-between gap-2 mb-1">
        <div className="flex-1">
          <div className={`font-semibold text-sm ${colors.text} mb-1`}>
            {data.name}
          </div>
          <Badge variant="outline" className="text-xs">
            {data.type}
            {data.runtime && ` • ${data.runtime}`}
          </Badge>
        </div>
      </div>
      <div className="text-xs text-muted-foreground mt-2 font-mono">
        {data.nodeId}
      </div>
      
      {/* Output handle (right side) */}
      <Handle
        type="source"
        position={Position.Right}
        style={{ background: '#555', width: 8, height: 8 }}
      />
    </div>
  );
};

const nodeTypes = {
  custom: CustomNode,
};

export function WorkflowGraph({ spec }: WorkflowGraphProps) {
  // Convert workflow spec to ReactFlow nodes and edges
  const { initialNodes, initialEdges } = useMemo(() => {
    const nodes: Node[] = [];
    const edges: Edge[] = [];
    const nodePositions = new Map<string, { x: number; y: number }>();
    
    // Calculate positions using a simple hierarchical layout
    // Find start nodes (nodes with no incoming edges)
    const incomingCount = new Map<string, number>();
    const outgoingCount = new Map<string, number>();
    
    spec.nodes.forEach(node => {
      incomingCount.set(node.node_id, 0);
      outgoingCount.set(node.node_id, 0);
    });
    
    spec.edges.forEach(edge => {
      incomingCount.set(edge.to, (incomingCount.get(edge.to) || 0) + 1);
      outgoingCount.set(edge.from, (outgoingCount.get(edge.from) || 0) + 1);
    });
    
    // Find start nodes (no incoming edges)
    const startNodes = spec.nodes.filter(node => (incomingCount.get(node.node_id) || 0) === 0);
    
    // Improved hierarchical layout using BFS with better positioning
    const visited = new Set<string>();
    const queue: Array<{ nodeId: string; level: number }> = [];
    const levelNodes = new Map<number, string[]>();
    const nodeLevels = new Map<string, number>();
    
    // Initialize with start nodes
    startNodes.forEach((node) => {
      queue.push({ nodeId: node.node_id, level: 0 });
      nodeLevels.set(node.node_id, 0);
      if (!levelNodes.has(0)) {
        levelNodes.set(0, []);
      }
      levelNodes.get(0)!.push(node.node_id);
    });
    
    // BFS to assign levels (using longest path for nodes with multiple incoming edges)
    while (queue.length > 0) {
      const { nodeId, level } = queue.shift()!;
      if (visited.has(nodeId)) continue;
      visited.add(nodeId);
      
      spec.edges
        .filter(edge => edge.from === nodeId)
        .forEach((edge) => {
          const currentLevel = nodeLevels.get(edge.to) || -1;
          const nextLevel = level + 1;
          
          // Use the maximum level (longest path) for nodes with multiple incoming edges
          if (nextLevel > currentLevel) {
            nodeLevels.set(edge.to, nextLevel);
            if (!levelNodes.has(nextLevel)) {
              levelNodes.set(nextLevel, []);
            }
            // Remove from previous level if it was added
            if (currentLevel >= 0) {
              const prevLevelNodes = levelNodes.get(currentLevel);
              if (prevLevelNodes) {
                const index = prevLevelNodes.indexOf(edge.to);
                if (index > -1) {
                  prevLevelNodes.splice(index, 1);
                }
              }
            }
            levelNodes.get(nextLevel)!.push(edge.to);
            queue.push({ nodeId: edge.to, level: nextLevel });
          }
        });
    }
    
    // Handle nodes that weren't reached (shouldn't happen in valid DAGs, but handle gracefully)
    spec.nodes.forEach(node => {
      if (!nodeLevels.has(node.node_id)) {
        const maxLevel = Math.max(...Array.from(levelNodes.keys()), -1);
        nodeLevels.set(node.node_id, maxLevel + 1);
        if (!levelNodes.has(maxLevel + 1)) {
          levelNodes.set(maxLevel + 1, []);
        }
        levelNodes.get(maxLevel + 1)!.push(node.node_id);
      }
    });
    
    // Assign positions based on levels - HORIZONTAL LAYOUT (left to right)
    const horizontalSpacing = 300; // Spacing between levels (left to right)
    const verticalSpacing = 200; // Spacing between nodes in same level (top to bottom)
    const startX = 150;
    const startY = 100;
    
    levelNodes.forEach((nodeIds, level) => {
      // Center nodes vertically within each level
      const totalHeight = (nodeIds.length - 1) * verticalSpacing;
      const startYForLevel = startY - (totalHeight / 2);
      
      nodeIds.forEach((nodeId, index) => {
        // X increases with level (horizontal flow)
        const x = startX + (level * horizontalSpacing);
        // Y varies within level (vertical stacking)
        const y = startYForLevel + (index * verticalSpacing);
        nodePositions.set(nodeId, { x, y });
      });
    });
    
    // Create ReactFlow nodes
    spec.nodes.forEach(node => {
      const position = nodePositions.get(node.node_id) || { x: 100, y: 100 };
      nodes.push({
        id: node.node_id,
        type: 'custom',
        position,
        data: {
          name: node.name,
          type: node.type,
          runtime: node.runtime,
          nodeId: node.node_id,
        },
      });
    });
    
    // Create ReactFlow edges with explicit styling
    spec.edges.forEach(edge => {
      edges.push({
        id: `${edge.from}-${edge.to}`,
        source: edge.from,
        target: edge.to,
        type: 'smoothstep',
        animated: false,
        style: {
          stroke: '#64748b',
          strokeWidth: 2,
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: '#64748b',
          width: 20,
          height: 20,
        },
        label: edge.condition ? 'if' : undefined,
        labelStyle: {
          fontSize: 10,
          fill: '#666',
          fontWeight: 500,
        },
        labelBgStyle: {
          fill: '#fff',
          fillOpacity: 0.8,
        },
      });
    });
    
    return { initialNodes: nodes, initialEdges: edges };
  }, [spec]);
  
  const [nodes, setNodes, onNodesChange] = useNodesState(initialNodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(initialEdges);
  
  const onConnect = useCallback(
    (params: Connection) => setEdges((eds) => addEdge(params, eds)),
    [setEdges]
  );
  
  // Get color for minimap
  const getNodeColor = useCallback((node: Node) => {
    const type = node.data?.type || 'CODE';
    const colorMap: Record<string, string> = {
      CODE: '#3b82f6',
      HTTP: '#10b981',
      SWITCH: '#eab308',
      FORK: '#f97316',
      JOIN: '#ec4899',
      WAIT: '#6b7280',
    };
    return colorMap[type] || '#94a3b8';
  }, []);
  
  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      onNodesChange={onNodesChange}
      onEdgesChange={onEdgesChange}
      onConnect={onConnect}
      nodeTypes={nodeTypes}
      fitView
      className="bg-background"
      defaultViewport={{ x: 0, y: 0, zoom: 0.8 }}
    >
      <Background />
      <Controls />
      <MiniMap 
        nodeColor={getNodeColor}
        maskColor="rgba(0, 0, 0, 0.1)"
        pannable
        zoomable
      />
    </ReactFlow>
  );
}

