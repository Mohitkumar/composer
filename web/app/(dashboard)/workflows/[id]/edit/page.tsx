'use client';

import { useRouter, useParams } from 'next/navigation';
import { WorkflowEditor } from '@/components/workflow/workflow-editor';
import { workflowApi } from '@/lib/api-client';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import type { WorkflowDefinitionSpec } from '@/lib/api-client';
import { toast } from 'sonner';

export default function EditWorkflowPage() {
  const router = useRouter();
  const params = useParams();
  const workflowId = params.id as string;
  const queryClient = useQueryClient();

  const { data: workflow, isLoading } = useQuery({
    queryKey: ['workflow', workflowId],
    queryFn: () => workflowApi.get(workflowId),
  });

  const updateMutation = useMutation({
    mutationFn: async (data: {
      spec: WorkflowDefinitionSpec;
      name: string;
    }) => {
      // Get current workflow to determine next version
      const currentWorkflow = await workflowApi.get(workflowId);
      const nextVersion = (currentWorkflow.version || 1) + 1;
      
      // Create new version with updated spec
      await workflowApi.create({
        workflow_id: workflowId,
        name: data.name,
        version: nextVersion,
        spec: data.spec,
        created_by: currentWorkflow.created_by || 'user',
      });
      
      // Update name if changed
      if (data.name !== currentWorkflow.name) {
        await workflowApi.update(workflowId, {
          name: data.name,
        }, nextVersion);
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      queryClient.invalidateQueries({ queryKey: ['workflow', workflowId] });
      toast.success('Workflow saved successfully!');
      // Don't redirect - stay on the edit page
    },
    onError: (error) => {
      toast.error('Failed to save workflow', {
        description: error instanceof Error ? error.message : 'An unknown error occurred',
      });
    },
  });

  if (isLoading) {
    return <div>Loading workflow...</div>;
  }

  if (!workflow) {
    return <div>Workflow not found</div>;
  }

  // Parse the spec from JSON if it's a string
  let spec: WorkflowDefinitionSpec;
  if (typeof workflow.spec === 'string') {
    spec = JSON.parse(workflow.spec);
  } else {
    spec = workflow.spec as WorkflowDefinitionSpec;
  }

  const handleSave = async (
    updatedSpec: WorkflowDefinitionSpec,
    name: string,
    _id: string
  ) => {
    // TODO: Implement spec update when backend supports it
    await updateMutation.mutateAsync({ spec: updatedSpec, name });
  };

  return (
    <div className="h-[calc(100vh-8rem)]">
      <WorkflowEditor
        initialSpec={spec}
        workflowId={workflow.id}
        workflowName={workflow.name}
        onSave={handleSave}
        isSaving={updateMutation.isPending}
      />
    </div>
  );
}
