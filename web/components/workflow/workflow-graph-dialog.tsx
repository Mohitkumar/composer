'use client';

import { useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';
import { workflowApi, type WorkflowDefinitionSpec } from '@/lib/api-client';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Loader2, AlertCircle } from 'lucide-react';
import { WorkflowGraph } from '@/components/workflow/workflow-graph';
import * as yaml from 'yaml';

interface WorkflowGraphDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  workflowId: string;
}

export function WorkflowGraphDialog({
  open,
  onOpenChange,
  workflowId,
}: WorkflowGraphDialogProps) {
  const { data: workflow, isLoading, error } = useQuery({
    queryKey: ['workflow', workflowId, 'latest'],
    queryFn: () => workflowApi.get(workflowId, undefined, true), // Get latest version
    enabled: open && !!workflowId,
  });

  // Parse the YAML spec using useMemo to avoid re-parsing on every render
  const { spec, parseError } = useMemo(() => {
    if (!workflow?.spec) {
      return { spec: null, parseError: null };
    }

    try {
      let parsed: WorkflowDefinitionSpec;
      
      // Handle both string (YAML) and already parsed object
      if (typeof workflow.spec === 'string') {
        // Parse YAML string
        parsed = yaml.parse(workflow.spec) as WorkflowDefinitionSpec;
      } else if (typeof workflow.spec === 'object' && workflow.spec !== null) {
        // Already an object, use it directly
        parsed = workflow.spec as WorkflowDefinitionSpec;
      } else {
        return { spec: null, parseError: 'Workflow spec must be a string or object' };
      }
      
      // Validate that it has the required structure
      if (!parsed || typeof parsed !== 'object') {
        return { spec: null, parseError: 'Invalid workflow specification format' };
      }
      
      if (!Array.isArray(parsed.nodes) || !Array.isArray(parsed.edges)) {
        return { spec: null, parseError: 'Workflow specification must contain nodes and edges arrays' };
      }

      return { spec: parsed, parseError: null };
    } catch (err) {
      return {
        spec: null,
        parseError: err instanceof Error ? err.message : 'Failed to parse workflow specification',
      };
    }
  }, [workflow?.spec]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-[95vw] w-full h-[90vh] flex flex-col p-0">
        <DialogHeader className="px-6 pt-6 pb-4 border-b">
          <DialogTitle className="text-xl">
            {workflow?.name || 'Workflow Graph'}
          </DialogTitle>
          <DialogDescription className="text-xs">
            {workflow
              ? `ID: ${workflow.id} • Version: ${workflow.version}`
              : 'Visualizing workflow structure'}
          </DialogDescription>
        </DialogHeader>
        <div className="flex-1 overflow-hidden p-6">
          {isLoading ? (
            <div className="flex items-center justify-center h-full">
              <div className="text-center">
                <Loader2 className="h-8 w-8 animate-spin mx-auto mb-4 text-primary" />
                <p className="text-muted-foreground">Loading workflow...</p>
              </div>
            </div>
          ) : error ? (
            <div className="flex items-center justify-center h-full">
              <div className="text-center space-y-2">
                <AlertCircle className="h-8 w-8 mx-auto text-destructive" />
                <p className="text-destructive font-medium">Failed to load workflow</p>
                <p className="text-sm text-muted-foreground">
                  {error instanceof Error ? error.message : 'Unknown error occurred'}
                </p>
              </div>
            </div>
          ) : parseError ? (
            <div className="flex items-center justify-center h-full">
              <div className="text-center space-y-2">
                <AlertCircle className="h-8 w-8 mx-auto text-destructive" />
                <p className="text-destructive font-medium">Failed to parse workflow</p>
                <p className="text-sm text-muted-foreground">{parseError}</p>
              </div>
            </div>
          ) : !spec ? (
            <div className="flex items-center justify-center h-full">
              <div className="text-center space-y-2">
                <AlertCircle className="h-8 w-8 mx-auto text-muted-foreground" />
                <p className="text-muted-foreground">No workflow specification found</p>
              </div>
            </div>
          ) : (
            <div className="h-full border rounded-lg">
              <WorkflowGraph spec={spec} />
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

