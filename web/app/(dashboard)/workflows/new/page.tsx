'use client';

import { useRouter } from 'next/navigation';
import { useState } from 'react';
import { WorkflowEditor } from '@/components/workflow/workflow-editor';
import { workflowApi } from '@/lib/api-client';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import type { WorkflowDefinitionSpec } from '@/lib/api-client';
import { toast } from 'sonner';

export default function NewWorkflowPage() {
  const router = useRouter();
  const queryClient = useQueryClient();
  const [workflowId, setWorkflowId] = useState<string | null>(null);
  const [isFirstSave, setIsFirstSave] = useState(true);

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
      toast.success('Workflow created successfully!');
      // Don't redirect - stay on the editor
      // Subsequent saves will be treated as edits (new versions)
    },
    onError: (error) => {
      toast.error('Failed to create workflow', {
        description: error instanceof Error ? error.message : 'An unknown error occurred',
      });
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
        await workflowApi.update(data.id, {
          name: data.name,
        }, nextVersion);
      }
    },
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      queryClient.invalidateQueries({ queryKey: ['workflow', variables.id] });
      toast.success('Workflow updated successfully!');
      // Don't redirect - stay on the page
    },
    onError: (error) => {
      toast.error('Failed to update workflow', {
        description: error instanceof Error ? error.message : 'An unknown error occurred',
      });
    },
  });

  const handleSave = async (
    spec: WorkflowDefinitionSpec,
    name: string,
    id: string
  ) => {
    if (isFirstSave) {
      await createMutation.mutateAsync({ spec, name, id });
    } else {
      // Subsequent saves are edits - create new version
      await updateMutation.mutateAsync({ spec, name, id });
    }
  };

  const isSaving = createMutation.isPending || updateMutation.isPending;

  return (
    <div className="h-[calc(100vh-8rem)]">
      <WorkflowEditor 
        onSave={handleSave}
        workflowId={workflowId || undefined}
        isSaving={isSaving}
      />
    </div>
  );
}
