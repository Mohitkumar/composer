'use client';

import React, { useState, useEffect, useMemo } from 'react';
import { useMutation, useQueryClient, useQuery } from '@tanstack/react-query';
import { executionApi, configurationApi, type ExecutionDetail, type Configuration } from '@/lib/api-client';
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
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover';
import { Input } from '@/components/ui/input';
import { Checkbox } from '@/components/ui/checkbox';
import Editor from '@monaco-editor/react';
import { toast } from 'sonner';
import { useRouter } from 'next/navigation';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Badge } from '@/components/ui/badge';
import { X, ChevronDown, Search } from 'lucide-react';

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

  // Get failed nodes - use the first failed node for retry
  const failedNodes = executionDetail.task_instances
    .filter((task) => task.state === 'FAILED')
    .map((task) => task.node_id);

  const failedNodeId = failedNodes.length > 0 ? failedNodes[0] : null;

  // Fetch configurations
  const { data: configurations = [] } = useQuery({
    queryKey: ['configurations'],
    queryFn: () => configurationApi.list(),
  });

  const [inputJson, setInputJson] = useState('{}');
  const [selectedConfigIds, setSelectedConfigIds] = useState<Set<string>>(new Set());
  const [inputError, setInputError] = useState<string | null>(null);
  const [configSearchQuery, setConfigSearchQuery] = useState('');
  const [configPopoverOpen, setConfigPopoverOpen] = useState(false);

  // Initialize with execution data when dialog opens
  useEffect(() => {
    if (!open) {
      // Reset state when dialog closes
      setInputJson('{}');
      setSelectedConfigIds(new Set());
      setInputError(null);
      setConfigSearchQuery('');
      setConfigPopoverOpen(false);
      return;
    }

    if (!execution) return;

    // Set input from execution or context (read-only display)
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

    setInputError(null);
    setConfigSearchQuery('');
    setConfigPopoverOpen(false);
  }, [open, execution?.id, context?.input]);

  // Initialize selected configs from context when configurations are loaded
  // Use a ref to track if we've already initialized to prevent re-initialization
  const configInitializedRef = React.useRef<string | null>(null);
  
  useEffect(() => {
    if (!open) {
      configInitializedRef.current = null;
      return;
    }

    // Create a stable key from execution ID and context config
    const initKey = `${execution?.id}-${context?.config ? JSON.stringify(context.config) : 'no-config'}`;
    
    // Skip if already initialized for this execution/context
    if (configInitializedRef.current === initKey || !context?.config || configurations.length === 0) {
      return;
    }

    // If config is a string, try to parse it
    let configObj: Record<string, any> = {};
    try {
      configObj = typeof context.config === 'string'
        ? JSON.parse(context.config)
        : context.config;
    } catch {
      // If parsing fails, mark as initialized
      configInitializedRef.current = initKey;
      return;
    }

    // Try to match configs by name or ID
    const configIds = new Set<string>();
    for (const config of configurations) {
      // Check if config name matches any key in configObj or if config_json matches
      if (configObj[config.name] || JSON.stringify(config.config_json) === JSON.stringify(configObj)) {
        configIds.add(config.id);
      }
    }
    
    // Only update if we found matching configs
    if (configIds.size > 0) {
      setSelectedConfigIds(configIds);
    }
    
    configInitializedRef.current = initKey;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, execution?.id, context?.config]); // Only depend on stable values

  // Filter configurations based on search query
  const filteredConfigurations = useMemo(() => {
    if (!configSearchQuery.trim()) {
      return configurations;
    }
    const query = configSearchQuery.toLowerCase();
    return configurations.filter(
      (config) =>
        config.name.toLowerCase().includes(query) ||
        config.description?.toLowerCase().includes(query) ||
        config.type.toLowerCase().includes(query)
    );
  }, [configurations, configSearchQuery]);

  const retryMutation = useMutation({
    mutationFn: async () => {
      if (!failedNodeId) {
        throw new Error('No failed node found to retry from');
      }

      // Build execution options from selected configurations
      const executionOptions: Record<string, any> = {};
      
      // Merge selected configurations
      for (const configId of selectedConfigIds) {
        const config = configurations.find((c) => c.id === configId);
        if (config) {
          // Use config name as key and config_json as value
          executionOptions[config.name] = config.config_json;
        }
      }

      // Call retry API
      return executionApi.retry(execution.id, {
        node_id: failedNodeId,
        input: inputJson !== '{}' ? JSON.parse(inputJson) : undefined,
        execution_options: Object.keys(executionOptions).length > 0 ? executionOptions : undefined,
        workflow_version: execution.workflow_version,
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

    // Validate input JSON format
    if (inputJson.trim() && inputJson !== '{}') {
      try {
        JSON.parse(inputJson);
      } catch (err: any) {
        setInputError(`Invalid JSON: ${err?.message ?? "Unknown error"}`);
        return;
      }
    }

    retryMutation.mutate();
  };

  const handleConfigToggle = (configId: string) => {
    const newSelected = new Set(selectedConfigIds);
    if (newSelected.has(configId)) {
      newSelected.delete(configId);
    } else {
      newSelected.add(configId);
    }
    setSelectedConfigIds(newSelected);
  };

  const handleRemoveConfig = (configId: string, e: React.MouseEvent) => {
    e.stopPropagation();
    const newSelected = new Set(selectedConfigIds);
    newSelected.delete(configId);
    setSelectedConfigIds(newSelected);
  };

  if (!failedNodeId) {
    return (
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Cannot Retry</DialogTitle>
            <DialogDescription>
              No failed nodes found in this execution. Cannot retry.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button onClick={() => onOpenChange(false)}>Close</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-4xl max-h-[90vh] flex flex-col">
        <DialogHeader>
          <DialogTitle>Retry Workflow Execution</DialogTitle>
        </DialogHeader>

        <div className="flex-1 overflow-auto flex flex-col gap-4">
          {/* Retry Info */}
          <div className="space-y-1 p-3 bg-muted rounded-md">
            <p className="text-sm font-medium">Retry : {failedNodeId}</p>
          </div>

          {/* Input JSON Section (Read-only display) */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label htmlFor="retry-input-json">Input (from original execution)</Label>
            </div>
            <div className="border rounded-md overflow-hidden" style={{ height: '200px' }}>
              <Editor
                height="200px"
                defaultLanguage="json"
                value={inputJson}
                onChange={() => {}} // Read-only
                options={{
                  minimap: { enabled: false },
                  fontSize: 14,
                  lineNumbers: "on",
                  tabSize: 2,
                  wordWrap: "on",
                  readOnly: true,
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

          {/* Configuration Selector Section */}
          <div className="space-y-2">
            <Label>Configuration (optional)</Label>
            <p className="text-xs text-muted-foreground">
              Select configurations to use for this retry. If not selected, the saved context will be used.
            </p>
            
            {/* Selected Configurations as Tags */}
            {selectedConfigIds.size > 0 && (
              <div className="flex flex-wrap gap-2 p-2 border rounded-md min-h-[2.5rem]">
                {Array.from(selectedConfigIds).map((configId) => {
                  const config = configurations.find((c) => c.id === configId);
                  if (!config) return null;
                  return (
                    <Badge
                      key={configId}
                      variant="secondary"
                      className="flex items-center gap-1 pr-1"
                    >
                      <span>{config.name}</span>
                      <button
                        type="button"
                        onClick={(e) => handleRemoveConfig(configId, e)}
                        className="ml-1 rounded-full hover:bg-secondary-foreground/20 p-0.5"
                      >
                        <X className="h-3 w-3" />
                      </button>
                    </Badge>
                  );
                })}
              </div>
            )}

            {/* Configuration Selector Popover */}
            <Popover open={configPopoverOpen} onOpenChange={setConfigPopoverOpen}>
              <PopoverTrigger asChild>
                <Button
                  variant="outline"
                  role="combobox"
                  className="w-full justify-between"
                >
                  <span className="text-muted-foreground">
                    {selectedConfigIds.size > 0
                      ? `${selectedConfigIds.size} configuration${selectedConfigIds.size > 1 ? "s" : ""} selected`
                      : "Select configurations..."}
                  </span>
                  <ChevronDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
                </Button>
              </PopoverTrigger>
              <PopoverContent className="w-[400px] p-0" align="start">
                <div className="flex flex-col">
                  {/* Search Input */}
                  <div className="flex items-center border-b px-3">
                    <Search className="mr-2 h-4 w-4 shrink-0 opacity-50" />
                    <Input
                      placeholder="Search configurations..."
                      value={configSearchQuery}
                      onChange={(e) => setConfigSearchQuery(e.target.value)}
                      className="border-0 focus-visible:ring-0 focus-visible:ring-offset-0"
                    />
                  </div>

                  {/* Configuration List */}
                  <ScrollArea className="max-h-[300px]">
                    {filteredConfigurations.length === 0 ? (
                      <div className="p-4 text-center text-sm text-muted-foreground">
                        {configSearchQuery
                          ? "No configurations found"
                          : "No configurations available"}
                      </div>
                    ) : (
                      <div className="p-2">
                        {filteredConfigurations.map((config) => (
                          <div
                            key={config.id}
                            className="flex items-start space-x-3 p-2 rounded-md hover:bg-accent cursor-pointer"
                            onClick={() => handleConfigToggle(config.id)}
                          >
                            <Checkbox
                              id={`config-${config.id}`}
                              checked={selectedConfigIds.has(config.id)}
                              onCheckedChange={() => handleConfigToggle(config.id)}
                              className="mt-0.5"
                            />
                            <div className="flex-1">
                              <label
                                htmlFor={`config-${config.id}`}
                                className="text-sm font-medium leading-none cursor-pointer"
                              >
                                {config.name}
                              </label>
                              {config.description && (
                                <p className="text-xs text-muted-foreground mt-1">
                                  {config.description}
                                </p>
                              )}
                              <p className="text-xs text-muted-foreground mt-1">
                                Type: {config.type}
                              </p>
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                  </ScrollArea>
                </div>
              </PopoverContent>
            </Popover>
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
            disabled={retryMutation.isPending || !!inputError}
          >
            {retryMutation.isPending ? 'Retrying...' : 'Retry Workflow'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
