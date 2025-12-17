'use client';

import { useParams } from 'next/navigation';
import { JsonWorkflowEditor } from '@/components/workflow/json-workflow-editor';
import { workflowApi } from '@/lib/api-client';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import type { WorkflowDefinitionSpec } from '@/lib/api-client';

export default function EditWorkflowPage() {
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
      id: string;
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
        await workflowApi.update(
          workflowId,
          {
            name: data.name,
          },
          nextVersion
        );
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      queryClient.invalidateQueries({ queryKey: ['workflow', workflowId] });
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

  const handleSave = async (args: {
    spec: WorkflowDefinitionSpec;
    name: string;
    id: string;
  }) => {
    await updateMutation.mutateAsync(args);
  };

  return (
    <div className="h-[calc(100vh-8rem)]">
      <JsonWorkflowEditor
        initialSpec={spec}
        initialName={workflow.name}
        initialId={workflow.id}
        onSave={handleSave}
        isSaving={updateMutation.isPending}
      />
    </div>
  );
}
