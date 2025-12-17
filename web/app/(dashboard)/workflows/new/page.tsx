'use client';

import { useRouter } from 'next/navigation';
import { WorkflowEditor } from '@/components/workflow/workflow-editor';
import { workflowApi } from '@/lib/api-client';
import { useMutation } from '@tanstack/react-query';
import type { WorkflowDefinitionSpec } from '@/lib/api-client';

export default function NewWorkflowPage() {
  const router = useRouter();

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
    },
    onSuccess: () => {
      router.push('/workflows');
    },
  });

  const handleSave = async (
    spec: WorkflowDefinitionSpec,
    name: string,
    id: string
  ) => {
    await createMutation.mutateAsync({ spec, name, id });
  };

  return (
    <div className="h-[calc(100vh-8rem)]">
      <WorkflowEditor onSave={handleSave} />
    </div>
  );
}
