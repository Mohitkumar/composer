"use client";

import React, { useState, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { configurationApi, type Configuration } from "@/lib/api-client";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import Editor from "@monaco-editor/react";
import { Checkbox } from "@/components/ui/checkbox";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { ScrollArea } from "@/components/ui/scroll-area";
import { X, ChevronDown, Search } from "lucide-react";

interface ExecuteWorkflowDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  workflowId: string;
  workflowVersion: number;
  onExecute: (data: {
    input: Record<string, any>;
    execution_options?: Record<string, any>;
  }) => Promise<void>;
  isExecuting: boolean;
}

export function ExecuteWorkflowDialog({
  open,
  onOpenChange,
  workflowId,
  workflowVersion,
  onExecute,
  isExecuting,
}: ExecuteWorkflowDialogProps) {
  const [inputJson, setInputJson] = useState("{}");
  const [selectedConfigs, setSelectedConfigs] = useState<Set<string>>(new Set());
  const [inputError, setInputError] = useState<string | null>(null);
  const [configSearchQuery, setConfigSearchQuery] = useState("");
  const [configPopoverOpen, setConfigPopoverOpen] = useState(false);

  // Reset state when dialog opens
  React.useEffect(() => {
    if (open) {
      setInputJson("{}");
      setSelectedConfigs(new Set());
      setInputError(null);
      setConfigSearchQuery("");
      setConfigPopoverOpen(false);
    }
  }, [open]);

  // Fetch all configurations
  const { data: configurations = [] } = useQuery({
    queryKey: ["configurations"],
    queryFn: () => configurationApi.list(),
  });

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

  const handleConfigToggle = (configName: string) => {
    const newSelected = new Set(selectedConfigs);
    if (newSelected.has(configName)) {
      newSelected.delete(configName);
    } else {
      newSelected.add(configName);
    }
    setSelectedConfigs(newSelected);
  };

  const handleRemoveConfig = (configName: string, e: React.MouseEvent) => {
    e.stopPropagation();
    const newSelected = new Set(selectedConfigs);
    newSelected.delete(configName);
    setSelectedConfigs(newSelected);
  };

  const handleExecute = async () => {
    setInputError(null);

    // Parse input JSON
    let input: Record<string, any>;
    try {
      input = JSON.parse(inputJson);
    } catch (err: any) {
      setInputError(`Invalid JSON: ${err?.message ?? "Unknown error"}`);
      return;
    }

    // Build config object from selected configurations
    // The config object should be keyed by configuration name, with values being the config_json
    const configObject: Record<string, any> = {};
    selectedConfigs.forEach((configName) => {
      const config = configurations.find((c) => c.name === configName);
      if (config && config.config_json) {
        // Use config name as key, and config_json as value
        configObject[configName] = config.config_json;
      }
    });

    // Build execution_options - the backend expects execution_options to be the config object
    // Based on newRunContext(p.Input, p.ExecutionOptions), execution_options is passed as config
    const execution_options: Record<string, any> | undefined = 
      Object.keys(configObject).length > 0 ? configObject : undefined;

    await onExecute({
      input,
      execution_options,
    });
  };

  const handleFormat = () => {
    try {
      const parsed = JSON.parse(inputJson);
      setInputJson(JSON.stringify(parsed, null, 2));
      setInputError(null);
    } catch (err: any) {
      setInputError(`Invalid JSON: ${err?.message ?? "Unknown error"}`);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-4xl max-h-[90vh]">
        <DialogHeader>
          <DialogTitle>Execute Workflow</DialogTitle>
          <DialogDescription>
            Provide input JSON and select configurations for workflow execution
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          {/* Input JSON Section */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label htmlFor="input-json">Input JSON</Label>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={handleFormat}
              >
                Format JSON
              </Button>
            </div>
            <div className="border rounded-md overflow-hidden" style={{ height: '300px' }}>
              <Editor
                height="300px"
                defaultLanguage="json"
                value={inputJson}
                onChange={(value) => {
                  setInputJson(value || "{}");
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
              <p className="text-sm text-destructive mt-1">{inputError}</p>
            )}
          </div>

          {/* Configurations Section */}
          <div className="space-y-2">
            <Label>Configurations</Label>
            
            {/* Selected Configurations as Tags */}
            {selectedConfigs.size > 0 && (
              <div className="flex flex-wrap gap-2 p-2 border rounded-md min-h-[2.5rem]">
                {Array.from(selectedConfigs).map((configName) => {
                  const config = configurations.find((c) => c.name === configName);
                  return (
                    <Badge
                      key={configName}
                      variant="secondary"
                      className="flex items-center gap-1 pr-1"
                    >
                      <span>{configName}</span>
                      <button
                        type="button"
                        onClick={(e) => handleRemoveConfig(configName, e)}
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
                    {selectedConfigs.size > 0
                      ? `${selectedConfigs.size} configuration${selectedConfigs.size > 1 ? "s" : ""} selected`
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
                            onClick={() => handleConfigToggle(config.name)}
                          >
                            <Checkbox
                              id={`config-${config.id}`}
                              checked={selectedConfigs.has(config.name)}
                              onCheckedChange={() => handleConfigToggle(config.name)}
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
            disabled={isExecuting}
          >
            Cancel
          </Button>
          <Button onClick={handleExecute} disabled={isExecuting || !!inputError}>
            {isExecuting ? "Executing..." : "Execute"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
