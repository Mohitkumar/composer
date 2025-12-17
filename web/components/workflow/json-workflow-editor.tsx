"use client";

import { useState, useEffect, useRef } from "react";
import type { WorkflowDefinitionSpec, NodeTemplate, WorkflowTemplate } from "@/lib/api-client";
import { templateApi } from "@/lib/api-client";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import Editor from "@monaco-editor/react";
import type { Monaco } from "@monaco-editor/react";
import { nodeFormSchemas } from "@/lib/node-schemas";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useQuery } from "@tanstack/react-query";
import { Plus, FileCode } from "lucide-react";

interface JsonWorkflowEditorProps {
  initialSpec?: WorkflowDefinitionSpec;
  initialName?: string;
  initialId?: string;
  isSaving?: boolean;
  isNew?: boolean; // Indicates if this is a new workflow
  onSave: (args: { spec: WorkflowDefinitionSpec; name: string; id: string }) => Promise<void>;
}

interface ValidationResult {
  isValid: boolean;
  errors: string[];
  warnings: string[];
}

// Valid node types
const VALID_NODE_TYPES = ["TASK", "SWITCH", "PARALLEL", "WAIT"];

// Valid runtimes
const VALID_RUNTIMES = ["http", "javascript", "python", "go", "sql", "s3", "csv"];

// Basic CEL expression validation (checks for common syntax errors)
function validateCELExpression(expr: string): string | null {
  if (typeof expr !== "string") return null; // Not a CEL expression
  
  // Check for balanced parentheses
  let parenCount = 0;
  let bracketCount = 0;
  let braceCount = 0;
  
  for (let i = 0; i < expr.length; i++) {
    const char = expr[i];
    if (char === "(") parenCount++;
    else if (char === ")") parenCount--;
    else if (char === "[") bracketCount++;
    else if (char === "]") bracketCount--;
    else if (char === "{") braceCount++;
    else if (char === "}") braceCount--;
    
    if (parenCount < 0 || bracketCount < 0 || braceCount < 0) {
      return `Unbalanced brackets in CEL expression: ${expr.substring(0, 50)}...`;
    }
  }
  
  if (parenCount !== 0 || bracketCount !== 0 || braceCount !== 0) {
    return `Unbalanced brackets in CEL expression: ${expr.substring(0, 50)}...`;
  }
  
  return null;
}

// Basic JavaScript syntax validation
function validateJavaScriptCode(code: string): string | null {
  if (typeof code !== "string") return null;
  
  try {
    // Try to parse as JavaScript (basic check)
    // This is a simple check - full validation would require a proper JS parser
    const hasFunction = code.includes("function") || code.includes("=>") || code.includes("handler");
    if (!hasFunction && code.trim().length > 0) {
      return "JavaScript code should contain a function or handler";
    }
    
    // Check for balanced braces
    let count = 0;
    for (const char of code) {
      if (char === "{") count++;
      else if (char === "}") count--;
      if (count < 0) return "Unbalanced braces in JavaScript code";
    }
    if (count !== 0) return "Unbalanced braces in JavaScript code";
  } catch (err) {
    return `JavaScript syntax error: ${err instanceof Error ? err.message : "Unknown error"}`;
  }
  
  return null;
}

// Basic Python syntax validation
function validatePythonCode(code: string): string | null {
  if (typeof code !== "string") return null;
  
  try {
    // Basic Python validation - check indentation consistency
    const lines = code.split("\n");
    let indentChar: string | null = null;
    
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      if (line.trim().length === 0) continue;
      
      const indent = line.match(/^(\s*)/)?.[1] || "";
      if (indent.length > 0) {
        if (indentChar === null) {
          indentChar = indent[0] === "\t" ? "\t" : " ";
        }
        // Check if indentation uses consistent character
        if (indentChar === " " && indent.includes("\t")) {
          return "Python code mixes spaces and tabs for indentation (line " + (i + 1) + ")";
        }
        if (indentChar === "\t" && indent.includes(" ")) {
          return "Python code mixes spaces and tabs for indentation (line " + (i + 1) + ")";
        }
      }
    }
    
    // Check for function definition
    const hasFunction = code.includes("def ") || code.includes("handler");
    if (!hasFunction && code.trim().length > 0) {
      return "Python code should contain a function definition (def handler)";
    }
  } catch (err) {
    return `Python syntax error: ${err instanceof Error ? err.message : "Unknown error"}`;
  }
  
  return null;
}

// Comprehensive workflow spec validation
function validateWorkflowSpec(spec: any): ValidationResult {
  const errors: string[] = [];
  const warnings: string[] = [];

  // Basic structure validation
  if (typeof spec !== "object" || spec === null) {
    return { isValid: false, errors: ["Spec must be a JSON object"], warnings: [] };
  }

  if (!Array.isArray(spec.nodes)) {
    return { isValid: false, errors: ["Spec must contain a 'nodes' array"], warnings: [] };
  }

  if (!Array.isArray(spec.edges)) {
    return { isValid: false, errors: ["Spec must contain an 'edges' array"], warnings: [] };
  }

  const nodes = spec.nodes;
  const edges = spec.edges;

  // Validate nodes
  const nodeIds = new Set<string>();
  const nodeIdSet = new Set<string>();

  for (let i = 0; i < nodes.length; i++) {
    const node = nodes[i];
    
    if (typeof node !== "object" || node === null) {
      errors.push(`Node at index ${i} is not an object`);
      continue;
    }

    // Check required fields
    if (!node.node_id || typeof node.node_id !== "string") {
      errors.push(`Node at index ${i}: missing or invalid 'node_id'`);
    } else {
      if (nodeIdSet.has(node.node_id)) {
        errors.push(`Node at index ${i}: duplicate node_id '${node.node_id}'`);
      }
      nodeIdSet.add(node.node_id);
      nodeIds.add(node.node_id);
    }

    if (!node.name || typeof node.name !== "string") {
      errors.push(`Node '${node.node_id || i}': missing or invalid 'name'`);
    }

    if (!node.type || !VALID_NODE_TYPES.includes(node.type)) {
      errors.push(`Node '${node.node_id || i}': invalid 'type'. Must be one of: ${VALID_NODE_TYPES.join(", ")}`);
    }

    if (!node.runtime || typeof node.runtime !== "string") {
      errors.push(`Node '${node.node_id || i}': missing or invalid 'runtime'`);
    } else if (!VALID_RUNTIMES.includes(node.runtime.toLowerCase())) {
      warnings.push(`Node '${node.node_id || i}': unknown runtime '${node.runtime}'`);
    }

    // Validate node spec based on runtime
    if (node.spec && typeof node.spec === "object") {
      const schema = nodeFormSchemas[node.runtime?.toLowerCase()];
      if (schema) {
        // Validate code fields
        for (const field of schema.fields) {
          if (field.isCode && node.spec[field.key]) {
            const code = node.spec[field.key];
            if (typeof code === "string") {
              if (field.codeLanguage === "javascript") {
                const error = validateJavaScriptCode(code);
                if (error) errors.push(`Node '${node.node_id || i}': ${error}`);
              } else if (field.codeLanguage === "python") {
                const error = validatePythonCode(code);
                if (error) errors.push(`Node '${node.node_id || i}': ${error}`);
              }
            }
          }
          
          // Validate CEL expressions
          if (field.supportsCEL && node.spec[field.key]) {
            const value = node.spec[field.key];
            if (typeof value === "string" && (value.includes("{{") || value.includes("tasks.") || value.includes("input."))) {
              const error = validateCELExpression(value);
              if (error) errors.push(`Node '${node.node_id || i}': ${error}`);
            }
          }
        }
      }
    } else if (node.spec && typeof node.spec !== "object") {
      errors.push(`Node '${node.node_id || i}': 'spec' must be an object`);
    }
  }

  // Validate edges
  for (let i = 0; i < edges.length; i++) {
    const edge = edges[i];
    
    if (typeof edge !== "object" || edge === null) {
      errors.push(`Edge at index ${i} is not an object`);
      continue;
    }

    if (!edge.from || typeof edge.from !== "string") {
      errors.push(`Edge at index ${i}: missing or invalid 'from'`);
    } else if (!nodeIds.has(edge.from)) {
      errors.push(`Edge at index ${i}: 'from' node '${edge.from}' does not exist`);
    }

    if (!edge.to || typeof edge.to !== "string") {
      errors.push(`Edge at index ${i}: missing or invalid 'to'`);
    } else if (!nodeIds.has(edge.to)) {
      errors.push(`Edge at index ${i}: 'to' node '${edge.to}' does not exist`);
    }

    if (edge.from && edge.to && edge.from === edge.to) {
      errors.push(`Edge at index ${i}: self-loop detected (node '${edge.from}' connects to itself)`);
    }
  }

  // Check for orphaned nodes (nodes with no edges)
  if (nodes.length > 0 && edges.length === 0) {
    warnings.push("Workflow has nodes but no edges. Nodes may not be connected.");
  }

  // Check for nodes with no incoming edges (potential start nodes)
  const hasIncoming = new Set<string>();
  for (const edge of edges) {
    if (edge.to) hasIncoming.add(edge.to);
  }
  const startNodes = nodes.filter((n: any) => n.node_id && !hasIncoming.has(n.node_id));
  if (startNodes.length === 0 && nodes.length > 0 && edges.length > 0) {
    warnings.push("No start nodes found (nodes with no incoming edges)");
  }

  // Check for cycles (simple check - nodes that form a cycle)
  // This is a simplified cycle detection
  const visited = new Set<string>();
  const recStack = new Set<string>();
  
  function hasCycle(nodeId: string): boolean {
    if (recStack.has(nodeId)) return true;
    if (visited.has(nodeId)) return false;
    
    visited.add(nodeId);
    recStack.add(nodeId);
    
    for (const edge of edges) {
      if (edge.from === nodeId) {
        if (hasCycle(edge.to)) return true;
      }
    }
    
    recStack.delete(nodeId);
    return false;
  }
  
  for (const nodeId of nodeIds) {
    if (!visited.has(nodeId) && hasCycle(nodeId)) {
      warnings.push("Workflow may contain cycles. Ensure your workflow is a DAG (Directed Acyclic Graph)");
      break;
    }
  }

  return {
    isValid: errors.length === 0,
    errors,
    warnings,
  };
}

// Sample nodes for new workflows
const SAMPLE_NODES = [
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

const DEFAULT_TEMPLATE = JSON.stringify(
  {
    nodes: [],
    edges: [],
  },
  null,
  2
);

const SAMPLE_TEMPLATE = JSON.stringify(
  {
    nodes: SAMPLE_NODES,
    edges: [
      { from: "start_node", to: "process_node" },
    ],
  },
  null,
  2
);

export function JsonWorkflowEditor({
  initialSpec,
  initialName,
  initialId,
  isSaving = false,
  isNew = false,
  onSave,
}: JsonWorkflowEditorProps) {
  const [workflowId, setWorkflowId] = useState(initialId ?? "");
  const [name, setName] = useState(initialName ?? "");
  const [jsonText, setJsonText] = useState(() => {
    if (initialSpec) {
      return JSON.stringify(initialSpec, null, 2);
    }
    // Use sample template for new workflows
    return isNew ? SAMPLE_TEMPLATE : DEFAULT_TEMPLATE;
  });
  const [validationResult, setValidationResult] = useState<ValidationResult | null>(null);
  const [showWarnings, setShowWarnings] = useState(true);
  const [nodeTemplateDialogOpen, setNodeTemplateDialogOpen] = useState(false);
  const [workflowTemplateDialogOpen, setWorkflowTemplateDialogOpen] = useState(false);
  const [selectedRuntime, setSelectedRuntime] = useState<string>("");
  const editorRef = useRef<any>(null);

  // Fetch node templates
  const { data: nodeTemplates = [], isLoading: loadingNodeTemplates } = useQuery({
    queryKey: ["nodeTemplates", selectedRuntime],
    queryFn: () => templateApi.listNodeTemplates({ runtime: selectedRuntime || undefined }),
  });

  // Fetch workflow templates
  const { data: workflowTemplates = [], isLoading: loadingWorkflowTemplates } = useQuery({
    queryKey: ["workflowTemplates"],
    queryFn: () => templateApi.listWorkflowTemplates(),
  });

  useEffect(() => {
    if (initialSpec) {
      setJsonText(JSON.stringify(initialSpec, null, 2));
    }
  }, [initialSpec]);

  const handleValidate = () => {
    setValidationResult(null);

    try {
      const parsed = JSON.parse(jsonText);
      const result = validateWorkflowSpec(parsed);
      setValidationResult(result);
    } catch (err: any) {
      setValidationResult({
        isValid: false,
        errors: [`JSON Parse Error: ${err?.message ?? "Invalid JSON"}`],
        warnings: [],
      });
    }
  };

  const handleFormat = () => {
    try {
      const parsed = JSON.parse(jsonText);
      const formatted = JSON.stringify(parsed, null, 2);
      setJsonText(formatted);
      setValidationResult(null);
    } catch (err: any) {
      setValidationResult({
        isValid: false,
        errors: [`JSON Parse Error: ${err?.message ?? "Invalid JSON"}`],
        warnings: [],
      });
    }
  };

  const handleSaveClick = async () => {
    setValidationResult(null);

    try {
      const parsed = JSON.parse(jsonText);
      const result = validateWorkflowSpec(parsed);
      
      if (!result.isValid) {
        setValidationResult(result);
        return;
      }

      const finalId = workflowId || `workflow_${Date.now()}`;
      const finalName = name || finalId;

      await onSave({
        spec: parsed as WorkflowDefinitionSpec,
        name: finalName,
        id: finalId,
      });

      setWorkflowId(finalId);
      setName(finalName);
      setValidationResult({
        isValid: true,
        errors: [],
        warnings: result.warnings,
      });
    } catch (err: any) {
      setValidationResult({
        isValid: false,
        errors: [`Failed to save workflow: ${err?.message ?? "Unknown error"}`],
        warnings: [],
      });
    }
  };

  const handleEditorDidMount = (editor: any, monaco: Monaco) => {
    editorRef.current = editor;
    
    // Configure JSON schema validation
    monaco.languages.json.jsonDefaults.setDiagnosticsOptions({
      validate: true,
      schemas: [],
      allowComments: false,
    });
  };

  const insertNodeTemplate = async (template: NodeTemplate) => {
    try {
      const currentSpec = JSON.parse(jsonText) as WorkflowDefinitionSpec;
      const templateNode = {
        node_id: `${template.runtime}_${Date.now()}`,
        name: template.name,
        type: template.node_type as any,
        runtime: template.runtime,
        spec: template.spec_json,
      };

      currentSpec.nodes.push(templateNode);
      setJsonText(JSON.stringify(currentSpec, null, 2));
      setNodeTemplateDialogOpen(false);
      setSelectedRuntime("");
    } catch (err) {
      console.error("Failed to insert node template:", err);
    }
  };

  const insertWorkflowTemplate = async (template: WorkflowTemplate) => {
    try {
      setJsonText(JSON.stringify(template.spec_json, null, 2));
      setWorkflowTemplateDialogOpen(false);
    } catch (err) {
      console.error("Failed to insert workflow template:", err);
    }
  };

  return (
    <div className="flex flex-col gap-4 h-[calc(100vh-8rem)]">
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div className="space-y-2">
          <Label htmlFor="workflow-id">Workflow ID</Label>
          <Input
            id="workflow-id"
            value={workflowId}
            onChange={(e) => setWorkflowId(e.target.value)}
            placeholder="Leave blank to auto-generate"
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="workflow-name">Workflow Name</Label>
          <Input
            id="workflow-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="My workflow"
          />
        </div>
      </div>

      <div className="flex items-center gap-2 flex-wrap">
        <Button
          type="button"
          variant="outline"
          onClick={() => setNodeTemplateDialogOpen(true)}
        >
          <Plus className="mr-2 h-4 w-4" />
          Insert Node Template
        </Button>
        <Button
          type="button"
          variant="outline"
          onClick={() => setWorkflowTemplateDialogOpen(true)}
        >
          <FileCode className="mr-2 h-4 w-4" />
          Insert Workflow Template
        </Button>
        <Button type="button" variant="outline" onClick={handleValidate}>
          Validate JSON
        </Button>
        <Button type="button" variant="outline" onClick={handleFormat}>
          Format JSON
        </Button>
        <Button
          type="button"
          onClick={handleSaveClick}
          disabled={isSaving}
        >
          {isSaving ? "Saving..." : "Save Workflow"}
        </Button>
        {validationResult && validationResult.warnings.length > 0 && (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => setShowWarnings(!showWarnings)}
          >
            {showWarnings ? "Hide" : "Show"} Warnings ({validationResult.warnings.length})
          </Button>
        )}
      </div>

      {validationResult && (
        <div className="space-y-2">
          {validationResult.errors.length > 0 && (
            <div className="p-3 rounded-md bg-destructive/10 border border-destructive/20">
              <p className="text-sm font-semibold text-destructive mb-2">
              Validation Errors ({validationResult.errors.length}):
              </p>
              <ul className="list-disc list-inside space-y-1 text-sm text-destructive">
                {validationResult.errors.map((error, idx) => (
                  <li key={idx}>{error}</li>
                ))}
              </ul>
            </div>
          )}
          {validationResult.warnings.length > 0 && showWarnings && (
            <div className="p-3 rounded-md bg-yellow-500/10 border border-yellow-500/20">
              <p className="text-sm font-semibold text-yellow-700 dark:text-yellow-400 mb-2">
              Warnings ({validationResult.warnings.length}):
              </p>
              <ul className="list-disc list-inside space-y-1 text-sm text-yellow-700 dark:text-yellow-400">
                {validationResult.warnings.map((warning, idx) => (
                  <li key={idx}>{warning}</li>
                ))}
              </ul>
            </div>
          )}
          {validationResult.isValid && validationResult.errors.length === 0 && (
            <div className="p-3 rounded-md bg-emerald-500/10 border border-emerald-500/20">
              <p className="text-sm font-semibold text-emerald-700 dark:text-emerald-400">
              ✓ Workflow JSON is valid
              </p>
            </div>
          )}
        </div>
      )}

      <div className="flex-1 min-h-0 flex flex-col">
        <Label htmlFor="workflow-json" className="mb-1 block">
          Workflow JSON
        </Label>
        <div className="flex-1 border rounded-md overflow-hidden">
          <Editor
            height="100%"
            defaultLanguage="json"
            value={jsonText}
            onChange={(value) => setJsonText(value || "")}
            onMount={handleEditorDidMount}
            options={{
              minimap: { enabled: false },
              fontSize: 14,
              lineNumbers: "on",
              tabSize: 2,
              wordWrap: "on",
              formatOnPaste: true,
              formatOnType: true,
              automaticLayout: true,
              scrollBeyondLastLine: false,
            }}
            theme="vs-dark"
          />
        </div>
        <p className="mt-2 text-xs text-muted-foreground">
          The JSON should contain a <code>nodes</code> array and an <code>edges</code> array.
          Use Validate JSON to check for errors in node structure, CEL expressions, and code snippets.
        </p>
      </div>

      {/* Node Template Dialog */}
      <Dialog open={nodeTemplateDialogOpen} onOpenChange={setNodeTemplateDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Insert Node Template</DialogTitle>
            <DialogDescription>
              Select a node template to insert into your workflow
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label>Filter by Runtime</Label>
              <Select value={selectedRuntime || "all"} onValueChange={(value) => setSelectedRuntime(value === "all" ? "" : value)}>
                <SelectTrigger>
                  <SelectValue placeholder="All runtimes" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">All runtimes</SelectItem>
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
            <div className="max-h-[400px] overflow-y-auto space-y-2">
              {loadingNodeTemplates ? (
                <p className="text-sm text-muted-foreground">Loading templates...</p>
              ) : nodeTemplates.length === 0 ? (
                <p className="text-sm text-muted-foreground">No templates found</p>
              ) : (
                nodeTemplates.map((template) => (
                  <div
                    key={template.id}
                    className="p-3 border rounded-md hover:bg-accent cursor-pointer"
                    onClick={() => insertNodeTemplate(template)}
                  >
                    <div className="font-medium">{template.name}</div>
                    {template.description && (
                      <div className="text-sm text-muted-foreground mt-1">
                        {template.description}
                      </div>
                    )}
                    <div className="text-xs text-muted-foreground mt-1">
                      Runtime: {template.runtime} | Type: {template.node_type}
                    </div>
                  </div>
                ))
              )}
            </div>
          </div>
        </DialogContent>
      </Dialog>

      {/* Workflow Template Dialog */}
      <Dialog open={workflowTemplateDialogOpen} onOpenChange={setWorkflowTemplateDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Insert Workflow Template</DialogTitle>
            <DialogDescription>
              Select a workflow template to replace your current workflow
            </DialogDescription>
          </DialogHeader>
          <div className="max-h-[400px] overflow-y-auto space-y-2">
            {loadingWorkflowTemplates ? (
              <p className="text-sm text-muted-foreground">Loading templates...</p>
            ) : workflowTemplates.length === 0 ? (
              <p className="text-sm text-muted-foreground">No templates found</p>
            ) : (
              workflowTemplates.map((template) => (
                <div
                  key={template.id}
                  className="p-3 border rounded-md hover:bg-accent cursor-pointer"
                  onClick={() => insertWorkflowTemplate(template)}
                >
                  <div className="font-medium">{template.name}</div>
                  {template.description && (
                    <div className="text-sm text-muted-foreground mt-1">
                      {template.description}
                    </div>
                  )}
                </div>
              ))
            )}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
