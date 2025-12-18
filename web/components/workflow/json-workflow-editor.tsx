"use client";

import React, { useState, useEffect, useRef } from "react";
import type { WorkflowDefinitionSpec, NodeTemplate } from "@/lib/api-client";
import { templateApi, configurationApi } from "@/lib/api-client";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import Editor from "@monaco-editor/react";
import type { Monaco } from "@monaco-editor/react";
import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { validateWorkflowSpec, type ValidationResult } from "@/lib/workflow-validation";
import { SAMPLE_TEMPLATE, DEFAULT_TEMPLATE } from "@/lib/workflow-constants";
import { createCompletionProvider } from "@/components/workflow/monaco-completion-provider";
import { NodeTemplateDialog } from "@/components/workflow/node-template-dialog";
import { ValidationResults } from "@/components/workflow/validation-results";

interface JsonWorkflowEditorProps {
  initialSpec?: WorkflowDefinitionSpec;
  initialName?: string;
  initialId?: string;
  isSaving?: boolean;
  isNew?: boolean;
  onSave: (args: { spec: WorkflowDefinitionSpec; name: string; id: string }) => Promise<void>;
}

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
  const [selectedRuntime, setSelectedRuntime] = useState<string>("");
  const editorRef = useRef<any>(null);
  const monacoRef = useRef<Monaco | null>(null);
  const completionProviderRef = useRef<any>(null);
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
      setJsonText(JSON.stringify(initialSpec, null, 2));
    }
  }, [initialSpec]);

  // Auto-validate on JSON change (debounced)
  useEffect(() => {
    const timer = setTimeout(() => {
      try {
        const parsed = JSON.parse(jsonText);
        const result = validateWorkflowSpec(parsed);
        setValidationResult(result);
      } catch (err: any) {
        // Only show errors for invalid JSON, not on every keystroke
        if (jsonText.trim().length > 0) {
          setValidationResult({
            isValid: false,
            errors: [`JSON Parse Error: ${err?.message ?? "Invalid JSON"}`],
            warnings: [],
          });
        } else {
          setValidationResult(null);
        }
      }
    }, 500); // Debounce validation

    return () => clearTimeout(timer);
  }, [jsonText]);

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
    
    // Configure JSON schema validation
    monaco.languages.json.jsonDefaults.setDiagnosticsOptions({
      validate: true,
      schemas: [],
      allowComments: false,
    });

    // Register completion provider (will be updated when configurations change)
    registerCompletionProvider(monaco);
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

      <ValidationResults
        validationResult={validationResult}
        showWarnings={showWarnings}
      />

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
          JSON is automatically formatted and validated as you type.
        </p>
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
