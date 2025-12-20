"use client";

import React, { useState, useEffect, useRef } from "react";
import type { WorkflowDefinitionSpec, NodeTemplate } from "@/lib/api-client";
import { templateApi, configurationApi } from "@/lib/api-client";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import Editor from "@monaco-editor/react";
import type { Monaco } from "@monaco-editor/react";
import { useQuery } from "@tanstack/react-query";
import { Plus, CheckCircle2, XCircle, AlertCircle, Rocket, RotateCcw } from "lucide-react";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { validateWorkflowSpec, type ValidationResult } from "@/lib/workflow-validation";
import { SAMPLE_TEMPLATE, DEFAULT_TEMPLATE } from "@/lib/workflow-constants";
import { createCompletionProvider } from "@/components/workflow/monaco-completion-provider";
import { NodeTemplateDialog } from "@/components/workflow/node-template-dialog";
import * as yaml from "yaml";

interface JsonWorkflowEditorProps {
  initialSpec?: WorkflowDefinitionSpec | string; // Can be object or YAML string
  initialName?: string;
  initialId?: string;
  isSaving?: boolean;
  isNew?: boolean;
  onSave: (args: { spec: string; name: string; id: string }) => Promise<void>; // spec is YAML string
  // Optional props for deploy/rollback (only for existing workflows)
  workflow?: {
    version: number;
    deployed: boolean;
    deployedVersion?: number;
  };
  onDeploy?: () => void;
  onRollback?: () => void;
  isDeploying?: boolean;
  isRollbackDisabled?: boolean;
}

export function JsonWorkflowEditor({
  initialSpec,
  initialName,
  initialId,
  isSaving = false,
  isNew = false,
  onSave,
  workflow,
  onDeploy,
  onRollback,
  isDeploying = false,
  isRollbackDisabled = false,
}: JsonWorkflowEditorProps) {
  const [workflowId, setWorkflowId] = useState(() => {
    if (initialId) return initialId;
    if (isNew) {
      // Generate ID as workflow_<timestamp>
      return `workflow_${Date.now()}`;
    }
    return "";
  });
  const [name, setName] = useState(initialName ?? "");
  const [yamlText, setYamlText] = useState(() => {
    if (initialSpec) {
      // If it's already a string (YAML), use it directly
      if (typeof initialSpec === 'string') {
        return initialSpec;
      }
      // Otherwise convert from object to YAML
      return yaml.stringify(initialSpec, { indent: 2 });
    }
    // Use sample template for new workflows
    return isNew ? SAMPLE_TEMPLATE : DEFAULT_TEMPLATE;
  });
  const [validationResult, setValidationResult] = useState<ValidationResult | null>(null);
  const [nodeTemplateDialogOpen, setNodeTemplateDialogOpen] = useState(false);
  const [selectedRuntime, setSelectedRuntime] = useState<string>("");
  const editorRef = useRef<any>(null);
  const monacoRef = useRef<Monaco | null>(null);
  const completionProviderRef = useRef<any>(null);
  const editorContainerRef = useRef<HTMLDivElement>(null);
  const [editorHeight, setEditorHeight] = useState<number>(600);
  // Store configurations in refs so completion provider can always access latest values
  const configurationsRef = useRef<any[]>([]);
  const configMapByNameRef = useRef<Map<string, any>>(new Map());

  // Fetch node templates
  const { data: nodeTemplates = [], isLoading: loadingNodeTemplates } = useQuery({
    queryKey: ["nodeTemplates", selectedRuntime],
    queryFn: () => templateApi.listNodeTemplates({ runtime: selectedRuntime || undefined }),
  });

  // Fetch all configurations for autocomplete
  const { data: configurationsRaw = [] } = useQuery({
    queryKey: ["configurations"],
    queryFn: () => configurationApi.list(),
  });

  // Deduplicate configurations by name and update refs
  React.useEffect(() => {
    const seen = new Set<string>();
    const deduped = configurationsRaw.filter((config) => {
      if (seen.has(config.name)) {
        return false;
      }
      seen.add(config.name);
      return true;
    });
    
    // Create a map for quick lookup by name
    const map = new Map<string, any>();
    deduped.forEach((config) => {
      map.set(config.name, config);
    });
    
    // Update refs
    configurationsRef.current = deduped;
    configMapByNameRef.current = map;
    
    // Re-register completion provider with new configurations
    if (monacoRef.current) {
      registerCompletionProvider(monacoRef.current);
    }
  }, [configurationsRaw]);


  useEffect(() => {
    if (initialSpec) {
      // If it's already a string (YAML), use it directly
      if (typeof initialSpec === 'string') {
        setYamlText(initialSpec);
      } else {
        // Otherwise convert from object to YAML
        setYamlText(yaml.stringify(initialSpec, { indent: 2 }));
      }
    }
  }, [initialSpec]);

  // Auto-validate on YAML change (debounced)
  useEffect(() => {
    const timer = setTimeout(() => {
      try {
        const parsed = yaml.parse(yamlText);
        const result = validateWorkflowSpec(parsed);
        setValidationResult(result);
      } catch (err: any) {
        // Only show errors for invalid YAML, not on every keystroke
        if (yamlText.trim().length > 0) {
          setValidationResult({
            isValid: false,
            errors: [`YAML Parse Error: ${err?.message ?? "Invalid YAML"}`],
            warnings: [],
          });
        } else {
          setValidationResult(null);
        }
      }
    }, 500); // Debounce validation

    return () => clearTimeout(timer);
  }, [yamlText]);

  const handleSaveClick = async () => {
    setValidationResult(null);

    try {
      // Parse YAML to JSON
      const parsed = yaml.parse(yamlText);
      const result = validateWorkflowSpec(parsed);
      
      if (!result.isValid) {
        setValidationResult(result);
        return;
      }

      // Generate ID from name if not provided
      let finalId = workflowId;
      if (!finalId && name) {
        finalId = generateWorkflowId(name);
      }
      if (!finalId) {
        finalId = `workflow_${Date.now()}`;
      }
      const finalName = name || finalId;

      // Convert parsed object back to YAML for saving
      const yamlForSave = yaml.stringify(parsed, { indent: 2 });
      await onSave({
        spec: yamlForSave,
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

  const registerCompletionProvider = (monaco: Monaco) => {
    // Dispose old provider if exists
    if (completionProviderRef.current) {
      completionProviderRef.current.dispose();
    }

    // Register new provider using extracted function
    completionProviderRef.current = createCompletionProvider(
      monaco,
      configurationsRef,
      configMapByNameRef
    );
  };

  const handleEditorDidMount = (editor: any, monaco: Monaco) => {
    editorRef.current = editor;
    monacoRef.current = monaco;
    
    // Register YAML language if not already registered
    const languages = monaco.languages.getLanguages();
    if (!languages.find((lang: { id: string }) => lang.id === 'yaml')) {
      monaco.languages.register({ id: 'yaml' });
    }

    // Register completion provider (will be updated when configurations change)
    registerCompletionProvider(monaco);
  };

  // Generate workflow ID from name
  const generateWorkflowId = (name: string): string => {
    if (!name.trim()) return "";
    // Convert to lowercase, replace spaces and special chars with underscores
    let id = name
      .toLowerCase()
      .trim()
      .replace(/[^a-z0-9]+/g, "_")
      .replace(/^_+|_+$/g, ""); // Remove leading/trailing underscores
    
    // Ensure it starts with a letter or number
    if (id && !/^[a-z0-9]/.test(id)) {
      id = "workflow_" + id;
    }
    
    // Ensure it's not empty
    if (!id) {
      id = "workflow";
    }
    
    return id;
  };

  // For new workflows, ID is already generated as workflow_<timestamp>
  // No need to auto-generate from name anymore

  // Calculate editor height based on container
  useEffect(() => {
    const updateEditorHeight = () => {
      if (editorContainerRef.current) {
        const height = editorContainerRef.current.clientHeight;
        if (height > 0) {
          setEditorHeight(height);
          // Update editor layout if editor is mounted
          if (editorRef.current) {
            setTimeout(() => {
              editorRef.current?.layout();
            }, 0);
          }
        }
      }
    };

    // Initial calculation with a small delay to ensure DOM is ready
    const timer = setTimeout(updateEditorHeight, 100);

    // Update on resize
    const resizeObserver = new ResizeObserver(() => {
      updateEditorHeight();
    });
    if (editorContainerRef.current) {
      resizeObserver.observe(editorContainerRef.current);
    }

    // Also listen to window resize
    window.addEventListener('resize', updateEditorHeight);

    return () => {
      clearTimeout(timer);
      resizeObserver.disconnect();
      window.removeEventListener('resize', updateEditorHeight);
    };
  }, []);

  // Update editor layout when height changes
  useEffect(() => {
    if (editorRef.current && editorHeight > 0) {
      setTimeout(() => {
        editorRef.current?.layout();
      }, 0);
    }
  }, [editorHeight]);

  const insertNodeTemplate = async (template: NodeTemplate) => {
    try {
      const currentSpec = yaml.parse(yamlText) as WorkflowDefinitionSpec;
      const templateNode = {
        node_id: `${template.runtime}_${Date.now()}`,
        name: template.name,
        type: template.node_type as any,
        runtime: template.runtime,
        spec: template.spec_json,
      };

      currentSpec.nodes.push(templateNode);
      setYamlText(yaml.stringify(currentSpec, { indent: 2 }));
      setNodeTemplateDialogOpen(false);
      setSelectedRuntime("");
    } catch (err) {
      console.error("Failed to insert node template:", err);
    }
  };

  return (
    <div className="flex flex-col h-full overflow-hidden">
      {/* Header: Name field with label on left, ID badge, Save button */}
      <div className="flex items-center gap-3 py-1 flex-shrink-0">
        <div className="flex items-center gap-2 flex-1">
          <Label htmlFor="workflow-name" className="text-xs text-muted-foreground whitespace-nowrap">
            Name:
          </Label>
          <Input
            id="workflow-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="My workflow"
            className="h-7 text-xs flex-1 max-w-md"
          />
          {workflowId && (
            <Badge variant="secondary" className="text-xs font-mono h-7 px-2">
              ID: {workflowId}
            </Badge>
          )}
          {workflow && (
            <>
              <Badge variant="outline" className="text-xs h-7 px-2">
                Version: v{workflow.version}
              </Badge>
              {workflow.deployedVersion !== undefined && workflow.deployedVersion !== workflow.version && (
                <Badge variant="secondary" className="text-xs h-7 px-2">
                  Deployed: v{workflow.deployedVersion}
                </Badge>
              )}
            </>
          )}
        </div>
        <Button
          type="button"
          onClick={handleSaveClick}
          disabled={isSaving || !name.trim() || !workflowId.trim()}
          size="sm"
          className="h-7 text-xs"
        >
          {isSaving ? "Saving..." : "Save"}
        </Button>
      </div>

      <div className="flex-1 min-h-0 flex flex-col mt-2 overflow-hidden">
        <div className="flex items-center justify-between mb-0.5 flex-shrink-0">
          <Label htmlFor="workflow-yaml" className="text-xs text-muted-foreground">
            Workflow YAML
          </Label>
          <div className="flex items-center gap-1.5">
            {workflow && onDeploy && !workflow.deployed && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={onDeploy}
                    disabled={isDeploying}
                    className="h-6 w-6 p-0"
                  >
                    <Rocket className="h-3.5 w-3.5" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>{isDeploying ? 'Deploying...' : 'Deploy'}</TooltipContent>
              </Tooltip>
            )}
            {workflow && onRollback && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={onRollback}
                    disabled={isRollbackDisabled}
                    className="h-6 w-6 p-0"
                  >
                    <RotateCcw className="h-3.5 w-3.5" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>Rollback</TooltipContent>
              </Tooltip>
            )}
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => setNodeTemplateDialogOpen(true)}
                  className="h-6 w-6 p-0"
                >
                  <Plus className="h-3.5 w-3.5" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>Insert Node Template</TooltipContent>
            </Tooltip>
            {validationResult && (
              <>
                {validationResult.errors.length > 0 ? (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <div className="flex items-center gap-1 text-destructive text-xs cursor-help">
                        <XCircle className="h-3.5 w-3.5" />
                        <span>{validationResult.errors.length} error{validationResult.errors.length !== 1 ? 's' : ''}</span>
                      </div>
                    </TooltipTrigger>
                    <TooltipContent side="left" className="max-w-md">
                      <div className="space-y-1">
                        <div className="font-semibold">Validation Errors:</div>
                        {validationResult.errors.map((error, idx) => (
                          <div key={idx} className="text-xs">• {error}</div>
                        ))}
                      </div>
                    </TooltipContent>
                  </Tooltip>
                ) : validationResult.warnings.length > 0 ? (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <div className="flex items-center gap-1 text-yellow-600 dark:text-yellow-400 text-xs cursor-help">
                        <AlertCircle className="h-3.5 w-3.5" />
                        <span>{validationResult.warnings.length} warning{validationResult.warnings.length !== 1 ? 's' : ''}</span>
                      </div>
                    </TooltipTrigger>
                    <TooltipContent side="left" className="max-w-md">
                      <div className="space-y-1">
                        <div className="font-semibold">Validation Warnings:</div>
                        {validationResult.warnings.map((warning, idx) => (
                          <div key={idx} className="text-xs">• {warning}</div>
                        ))}
                      </div>
                    </TooltipContent>
                  </Tooltip>
                ) : validationResult.isValid ? (
                  <div className="flex items-center gap-1 text-emerald-600 dark:text-emerald-400 text-xs">
                    <CheckCircle2 className="h-3.5 w-3.5" />
                    <span>Valid</span>
                  </div>
                ) : null}
              </>
            )}
          </div>
        </div>
        <div ref={editorContainerRef} className="flex-1 border rounded-md overflow-hidden min-h-0 w-full">
          <Editor
            height={editorHeight}
            defaultLanguage="yaml"
            value={yamlText}
            onChange={(value) => setYamlText(value || "")}
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
      </div>

      <NodeTemplateDialog
        open={nodeTemplateDialogOpen}
        onOpenChange={setNodeTemplateDialogOpen}
        nodeTemplates={nodeTemplates}
        isLoading={loadingNodeTemplates}
        selectedRuntime={selectedRuntime}
        onRuntimeChange={setSelectedRuntime}
        onSelectTemplate={insertNodeTemplate}
      />


    </div>
  );
}
