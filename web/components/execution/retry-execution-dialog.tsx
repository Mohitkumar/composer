'use client';

import { useState, useEffect } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { executionApi, workflowApi, type ExecutionDetail } from '@/lib/api-client';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import Editor from '@monaco-editor/react';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { toast } from 'sonner';
import { useRouter } from 'next/navigation';

interface RetryExecutionDialogProps {
  executionDetail: ExecutionDetail;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function RetryExecutionDialog({
  executionDetail,
  open,
  onOpenChange,
}: RetryExecutionDialogProps) {
  const router = useRouter();
  const queryClient = useQueryClient();
  const execution = executionDetail.execution;
  const context = executionDetail.context;

  // Get failed nodes
  const failedNodes = executionDetail.task_instances
    .filter((task) => task.state === 'FAILED')
    .map((task) => task.node_id);

  // Get all unique node IDs from task instances
  const allNodeIds = [
    ...new Set(executionDetail.task_instances.map((task) => task.node_id)),
  ].sort();

  const [inputJson, setInputJson] = useState('{}');
  const [configJson, setConfigJson] = useState('{}');
  const [selectedStartNode, setSelectedStartNode] = useState<string>('');
  const [inputError, setInputError] = useState<string | null>(null);
  const [configError, setConfigError] = useState<string | null>(null);

  // Initialize with execution data when dialog opens
  useEffect(() => {
    if (open && execution) {
      // Set input from execution or context
      if (execution.input) {
        try {
          const input = typeof execution.input === 'string' 
            ? JSON.parse(execution.input) 
            : execution.input;
          setInputJson(JSON.stringify(input, null, 2));
        } catch {
          setInputJson('{}');
        }
      } else if (context?.input) {
        try {
          const input = typeof context.input === 'string'
            ? JSON.parse(context.input)
            : context.input;
          setInputJson(JSON.stringify(input, null, 2));
        } catch {
          setInputJson('{}');
        }
      } else {
        setInputJson('{}');
      }

      // Set config from context
      if (context?.config) {
        try {
          const config = typeof context.config === 'string'
            ? JSON.parse(context.config)
            : context.config;
          setConfigJson(JSON.stringify(config, null, 2));
        } catch {
          setConfigJson('{}');
        }
      } else {
        setConfigJson('{}');
      }

      // Set default start node to first failed node, or empty (start from beginning)
      setSelectedStartNode(failedNodes.length > 0 ? failedNodes[0] : '');
      setInputError(null);
      setConfigError(null);
    }
  }, [open, execution, context, failedNodes]);

  const retryMutation = useMutation({
    mutationFn: async () => {
      // Parse and validate input
      let input: Record<string, any>;
      try {
        input = JSON.parse(inputJson);
      } catch (err: any) {
        throw new Error(`Invalid input JSON: ${err?.message ?? "Unknown error"}`);
      }

      // Parse and validate config
      let config: Record<string, any> = {};
      if (configJson.trim() && configJson !== '{}') {
        try {
          config = JSON.parse(configJson);
        } catch (err: any) {
          throw new Error(`Invalid config JSON: ${err?.message ?? "Unknown error"}`);
        }
      }

      // Build execution options with config
      const execution_options = Object.keys(config).length > 0 ? config : undefined;

      // Call execute workflow API with same workflow ID and version
      return workflowApi.execute(execution.workflow_definition_id, {
        workflow_version: execution.workflow_version,
        input,
        execution_options,
        start_node_id: selectedStartNode || undefined,
      });
    },
    onSuccess: (data) => {
      toast.success('Workflow retry started');
      onOpenChange(false);
      queryClient.invalidateQueries({ queryKey: ['executions'] });
      router.push(`/executions/${data.run_id}`);
    },
    onError: (error) => {
      toast.error('Failed to retry workflow', {
        description: error instanceof Error ? error.message : 'Unknown error occurred',
      });
    },
  });

  const handleRetry = () => {
    setInputError(null);
    setConfigError(null);

    // Validate input
    try {
      JSON.parse(inputJson);
    } catch (err: any) {
      setInputError(`Invalid JSON: ${err?.message ?? "Unknown error"}`);
      return;
    }

    // Validate config if not empty
    if (configJson.trim() && configJson !== '{}') {
      try {
        JSON.parse(configJson);
      } catch (err: any) {
        setConfigError(`Invalid JSON: ${err?.message ?? "Unknown error"}`);
        return;
      }
    }

    retryMutation.mutate();
  };

  const handleFormat = (type: 'input' | 'config') => {
    try {
      const json = type === 'input' ? inputJson : configJson;
      const parsed = JSON.parse(json);
      const formatted = JSON.stringify(parsed, null, 2);
      if (type === 'input') {
        setInputJson(formatted);
        setInputError(null);
      } else {
        setConfigJson(formatted);
        setConfigError(null);
      }
    } catch (err: any) {
      if (type === 'input') {
        setInputError(`Invalid JSON: ${err?.message ?? "Unknown error"}`);
      } else {
        setConfigError(`Invalid JSON: ${err?.message ?? "Unknown error"}`);
      }
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-4xl max-h-[90vh] flex flex-col">
        <DialogHeader>
          <DialogTitle>Retry Workflow Execution</DialogTitle>
          <DialogDescription>
            Edit input and configuration, then retry from a specific node
          </DialogDescription>
        </DialogHeader>

        <div className="flex-1 overflow-auto flex flex-col gap-4">
          {/* Start Node Selection */}
          <div className="space-y-2">
            <Label>Start From Node (optional)</Label>
            <Select value={selectedStartNode || "__beginning__"} onValueChange={(value) => setSelectedStartNode(value === "__beginning__" ? "" : value)}>
              <SelectTrigger>
                <SelectValue placeholder="Start from beginning" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__beginning__">Start from beginning</SelectItem>
                {failedNodes.length > 0 && (
                  <>
                    {failedNodes.map((nodeId) => (
                      <SelectItem key={`failed-${nodeId}`} value={nodeId}>
                        {nodeId} (Failed)
                      </SelectItem>
                    ))}
                  </>
                )}
                {allNodeIds.length > 0 && (
                  <>
                    {allNodeIds
                      .filter((nodeId) => !failedNodes.includes(nodeId))
                      .map((nodeId) => (
                        <SelectItem key={nodeId} value={nodeId}>
                          {nodeId}
                        </SelectItem>
                      ))}
                  </>
                )}
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              {selectedStartNode
                ? `Workflow will start from node: ${selectedStartNode}`
                : 'Workflow will start from the beginning'}
            </p>
          </div>

          {/* Input JSON Section */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label htmlFor="retry-input-json">Input JSON</Label>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => handleFormat('input')}
              >
                Format JSON
              </Button>
            </div>
            <div className="border rounded-md overflow-hidden" style={{ height: '250px' }}>
              <Editor
                height="250px"
                defaultLanguage="json"
                value={inputJson}
                onChange={(value) => {
                  setInputJson(value || '{}');
                  setInputError(null);
                }}
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
            {inputError && (
              <p className="text-sm text-destructive">{inputError}</p>
            )}
          </div>

          {/* Config JSON Section */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label htmlFor="retry-config-json">Config JSON (optional)</Label>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => handleFormat('config')}
              >
                Format JSON
              </Button>
            </div>
            <div className="border rounded-md overflow-hidden" style={{ height: '250px' }}>
              <Editor
                height="250px"
                defaultLanguage="json"
                value={configJson}
                onChange={(value) => {
                  setConfigJson(value || '{}');
                  setConfigError(null);
                }}
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
            {configError && (
              <p className="text-sm text-destructive">{configError}</p>
            )}
          </div>
        </div>

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={retryMutation.isPending}
          >
            Cancel
          </Button>
          <Button
            onClick={handleRetry}
            disabled={retryMutation.isPending || !!inputError || !!configError}
          >
            {retryMutation.isPending ? 'Retrying...' : 'Retry Workflow'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

