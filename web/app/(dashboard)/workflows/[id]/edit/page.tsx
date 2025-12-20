'use client';

import { useParams, useRouter } from 'next/navigation';
import { JsonWorkflowEditor } from '@/components/workflow/json-workflow-editor';
import { workflowApi } from '@/lib/api-client';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import type { WorkflowDefinitionSpec } from '@/lib/api-client';
import { Button } from '@/components/ui/button';
import { Rocket, RotateCcw } from 'lucide-react';
import { toast } from 'sonner';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useState } from 'react';

export default function EditWorkflowPage() {
  const params = useParams();
  const router = useRouter();
  const workflowId = params.id as string;
  const queryClient = useQueryClient();
  const [rollbackDialogOpen, setRollbackDialogOpen] = useState(false);
  const [selectedRollbackVersion, setSelectedRollbackVersion] = useState<number | null>(null);

  // For editing, we want the latest version (which might be a draft), not the deployed version
  const { data: workflow, isLoading } = useQuery({
    queryKey: ['workflow', workflowId, 'latest'],
    queryFn: () => workflowApi.get(workflowId, undefined, true), // latest=true to get latest version
  });

  // Fetch deployed version to show what's currently deployed
  const { data: deployedWorkflow } = useQuery({
    queryKey: ['workflow', workflowId, 'deployed'],
    queryFn: () => workflowApi.get(workflowId), // Get deployed version (default)
    enabled: !!workflowId,
  });

  const updateMutation = useMutation({
    mutationFn: async (data: {
      spec: string; // YAML string
      name: string;
      id: string;
    }) => {
      // Get current workflow to check if deployed and get created_by
      const currentWorkflow = await workflowApi.get(workflowId, undefined, true); // Get latest version
      
      // Pass the current version - backend will handle versioning:
      // - If not deployed, it will update in place
      // - If deployed, it will create a new version automatically
      await workflowApi.create({
        workflow_id: workflowId,
        name: data.name,
        version: currentWorkflow.version,
        spec: data.spec, // YAML string
        created_by: currentWorkflow.created_by || 'user',
      });

      // Update name if changed (only if not creating new version)
      if (data.name !== currentWorkflow.name && !currentWorkflow.deployed) {
        await workflowApi.update(
          workflowId,
          {
            name: data.name,
          },
          currentWorkflow.version
        );
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      queryClient.invalidateQueries({ queryKey: ['workflow', workflowId] });
      toast.success('Workflow saved successfully');
    },
    onError: (error) => {
      toast.error('Failed to save workflow', {
        description: error instanceof Error ? error.message : 'Unknown error occurred',
      });
    },
  });

  const deployMutation = useMutation({
    mutationFn: () => workflowApi.deploy(workflowId, workflow?.version || 1),
    onSuccess: () => {
      // Invalidate all workflow-related queries to refresh the data
      queryClient.invalidateQueries({ queryKey: ['workflow', workflowId] });
      queryClient.invalidateQueries({ queryKey: ['workflow', workflowId, 'latest'] });
      queryClient.invalidateQueries({ queryKey: ['workflow', workflowId, 'deployed'] });
      // Invalidate workflows list to refresh the row
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      // Also invalidate any latest version queries that might be cached
      queryClient.invalidateQueries({ queryKey: ['workflow'], exact: false });
      toast.success('Workflow deployed successfully');
    },
    onError: (error) => {
      toast.error('Failed to deploy workflow', {
        description: error instanceof Error ? error.message : 'Unknown error occurred',
      });
    },
  });

  // Get previous deployed versions for rollback
  const { data: previousDeployedVersions = [] } = useQuery({
    queryKey: ['previous-deployed-versions', workflowId, workflow?.version],
    queryFn: () => workflowApi.getPreviousDeployedVersions(workflowId, workflow?.version || 0),
    enabled: !!workflow && workflow.deployed && workflow.version > 1,
  });

  const rollbackMutation = useMutation({
    mutationFn: (version: number) => workflowApi.rollback(workflowId, version),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflow', workflowId] });
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      queryClient.invalidateQueries({ queryKey: ['previous-deployed-versions', workflowId] });
      toast.success('Workflow rolled back successfully');
      setRollbackDialogOpen(false);
      setSelectedRollbackVersion(null);
      // Refresh the page to show the rolled back version
      router.refresh();
    },
    onError: (error) => {
      toast.error('Failed to rollback workflow', {
        description: error instanceof Error ? error.message : 'Unknown error occurred',
      });
    },
  });

  if (isLoading) {
    return <div>Loading workflow...</div>;
  }

  if (!workflow) {
    return <div>Workflow not found</div>;
  }

  // Spec is now YAML string from backend, pass it directly
  const spec = workflow.spec as string;

  const handleSave = async (args: {
    spec: string; // YAML string
    name: string;
    id: string;
  }) => {
    await updateMutation.mutateAsync(args);
  };

  return (
    <>
      <div className="h-[calc(100vh-8rem)] flex flex-col">
        <JsonWorkflowEditor
          initialSpec={spec}
          initialName={workflow.name}
          initialId={workflow.id}
          onSave={handleSave}
          isSaving={updateMutation.isPending}
          workflow={{
            version: workflow.version,
            deployed: workflow.deployed,
            deployedVersion: deployedWorkflow?.version,
          }}
          onDeploy={() => deployMutation.mutate()}
          onRollback={() => setRollbackDialogOpen(true)}
          isDeploying={deployMutation.isPending}
          isRollbackDisabled={!workflow.deployed || (previousDeployedVersions?.length || 0) === 0}
        />
      </div>

      <Dialog open={rollbackDialogOpen} onOpenChange={setRollbackDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Rollback Workflow</DialogTitle>
            <DialogDescription>
              Select a previous deployed version to rollback to. This will deploy the selected version
              and make it the active deployed version.
            </DialogDescription>
          </DialogHeader>
          <div className="py-4">
            <p className="text-sm text-muted-foreground mb-2">
              Current version: v{workflow.version}
            </p>
            <p className="text-sm text-muted-foreground mb-4">
              Select a previous deployed version to rollback to:
            </p>
            <div className="space-y-2 max-h-60 overflow-y-auto">
              {previousDeployedVersions && previousDeployedVersions.length > 0 ? (
                previousDeployedVersions.map((v) => (
                  <Button
                    key={v}
                    variant={selectedRollbackVersion === v ? 'default' : 'outline'}
                    className="w-full justify-start"
                    onClick={() => setSelectedRollbackVersion(v)}
                  >
                    Version {v}
                  </Button>
                ))
              ) : (
                <p className="text-sm text-muted-foreground text-center py-4">
                  No previous deployed versions available
                </p>
              )}
            </div>
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setRollbackDialogOpen(false);
                setSelectedRollbackVersion(null);
              }}
            >
              Cancel
            </Button>
            <Button
              onClick={() => {
                if (selectedRollbackVersion !== null) {
                  rollbackMutation.mutate(selectedRollbackVersion);
                }
              }}
              disabled={rollbackMutation.isPending || selectedRollbackVersion === null}
            >
              {rollbackMutation.isPending ? 'Rolling back...' : 'Rollback'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
