import { nodeFormSchemas } from "@/lib/node-schemas";

export interface ValidationResult {
  isValid: boolean;
  errors: string[];
  warnings: string[];
}

// Valid node types
export const VALID_NODE_TYPES = ["CODE", "HTTP", "SWITCH", "FORK", "JOIN", "WAIT", "WAIT_FOR_EVENT"];

// Valid runtimes (only for CODE type nodes)
export const VALID_RUNTIMES = ["python", "javascript", "js", "node", "go"];

// Basic CEL expression validation (checks for common syntax errors)
export function validateCELExpression(expr: string): string | null {
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
export function validateJavaScriptCode(code: string): string | null {
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
export function validatePythonCode(code: string): string | null {
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
export function validateWorkflowSpec(spec: any): ValidationResult {
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

    // Runtime is only required for CODE type nodes
    if (node.type === "CODE") {
      if (!node.runtime || typeof node.runtime !== "string") {
        errors.push(`Node '${node.node_id || i}': CODE type nodes require a 'runtime' field (e.g., python, javascript)`);
      } else if (!VALID_RUNTIMES.includes(node.runtime.toLowerCase())) {
        warnings.push(`Node '${node.node_id || i}': unknown runtime '${node.runtime}'. Valid runtimes: ${VALID_RUNTIMES.join(", ")}`);
      }
    } else if (node.runtime) {
      // Runtime should not be specified for non-CODE nodes
      warnings.push(`Node '${node.node_id || i}': 'runtime' field is only valid for CODE type nodes. It will be ignored.`);
    }

    // Wait nodes are resolved by the coordinator and need a resolvable trigger.
    if (node.type === "WAIT" && node.spec?.duration_seconds === undefined && !node.spec?.until) {
      errors.push(`Node '${node.node_id || i}': WAIT nodes require spec.duration_seconds or spec.until`);
    }
    if (node.type === "WAIT_FOR_EVENT" && !node.spec?.event) {
      errors.push(`Node '${node.node_id || i}': WAIT_FOR_EVENT nodes require spec.event`);
    }

    // Validate node spec based on runtime (for CODE nodes) or type (for other nodes)
    if (node.spec && typeof node.spec === "object") {
      // For CODE nodes, use runtime to get schema; for others, use type
      const schemaKey = node.type === "CODE" && node.runtime 
        ? node.runtime.toLowerCase() 
        : node.type?.toLowerCase();
      const schema = nodeFormSchemas[schemaKey || ""];
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
