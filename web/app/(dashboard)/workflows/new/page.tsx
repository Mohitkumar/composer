'use client';

import { useState } from 'react';
import { JsonWorkflowEditor } from '@/components/workflow/json-workflow-editor';
import { workflowApi } from '@/lib/api-client';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import type { WorkflowDefinitionSpec } from '@/lib/api-client';

export default function NewWorkflowPage() {
  const queryClient = useQueryClient();
  const [isFirstSave, setIsFirstSave] = useState(true);
  const [workflowId, setWorkflowId] = useState<string | null>(null);

  const createMutation = useMutation({
    mutationFn: async (data: {
      spec: WorkflowDefinitionSpec;
      name: string;
      id: string;
    }) => {
      await workflowApi.create({
        workflow_id: data.id,
        name: data.name,
        version: 1,
        spec: data.spec,
        created_by: 'user', // TODO: Get from auth
      });
      return data.id;
    },
    onSuccess: (id) => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      setWorkflowId(id);
      setIsFirstSave(false);
    },
  });

  const updateMutation = useMutation({
    mutationFn: async (data: {
      spec: WorkflowDefinitionSpec;
      name: string;
      id: string;
    }) => {
      // Get current workflow to determine next version
      const currentWorkflow = await workflowApi.get(data.id);
      const nextVersion = (currentWorkflow.version || 1) + 1;

      // Create new version with updated spec
      await workflowApi.create({
        workflow_id: data.id,
        name: data.name,
        version: nextVersion,
        spec: data.spec,
        created_by: 'user',
      });

      // Update name if changed
      if (data.name !== currentWorkflow.name) {
        await workflowApi.update(
          data.id,
          {
            name: data.name,
          },
          nextVersion
        );
      }
    },
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      queryClient.invalidateQueries({ queryKey: ['workflow', variables.id] });
    },
  });

  const handleSave = async (args: {
    spec: WorkflowDefinitionSpec;
    name: string;
    id: string;
  }) => {
    if (isFirstSave || !workflowId) {
      await createMutation.mutateAsync(args);
    } else {
      await updateMutation.mutateAsync(args);
    }
  };

  const isSaving = createMutation.isPending || updateMutation.isPending;

  return (
    <div className="h-[calc(100vh-8rem)]">
      <JsonWorkflowEditor
        onSave={handleSave}
        isSaving={isSaving}
        initialId={workflowId ?? undefined}
        isNew={true}
      />
    </div>
  );
}
