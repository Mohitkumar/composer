'use client';

import { useState, useEffect } from 'react';
import { JsonWorkflowEditor } from '@/components/workflow/json-workflow-editor';
import { workflowApi, templateApi, type WorkflowDefinitionSpec } from '@/lib/api-client';
import { useMutation, useQueryClient, useQuery } from '@tanstack/react-query';
import * as yaml from 'yaml';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Rocket, RotateCcw } from 'lucide-react';
import { toast } from 'sonner';
import { useRouter } from 'next/navigation';

export default function NewWorkflowPage() {
  const queryClient = useQueryClient();
  const router = useRouter();
  const [isFirstSave, setIsFirstSave] = useState(true);
  const [workflowId, setWorkflowId] = useState<string | null>(null);
  const [templateDialogOpen, setTemplateDialogOpen] = useState(true);
  const [selectedTemplate, setSelectedTemplate] = useState<string | null>(null);
  const [initialSpec, setInitialSpec] = useState<WorkflowDefinitionSpec | undefined>(undefined);
  const [rollbackDialogOpen, setRollbackDialogOpen] = useState(false);
  const [selectedRollbackVersion, setSelectedRollbackVersion] = useState<number | null>(null);

  // Fetch workflow templates
  const { data: workflowTemplates = [], isLoading: loadingTemplates } = useQuery({
    queryKey: ['workflowTemplates'],
    queryFn: () => templateApi.listWorkflowTemplates(),
  });

  const createMutation = useMutation({
    mutationFn: async (data: {
      spec: string; // YAML string
      name: string;
      id: string;
    }) => {
      await workflowApi.create({
        workflow_id: data.id,
        name: data.name,
        version: 1,
        spec: data.spec, // YAML string
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

  // Fetch workflow after it's created to get deployment status
  const { data: workflow } = useQuery({
    queryKey: ['workflow', workflowId, 'latest'],
    queryFn: () => workflowApi.get(workflowId!, undefined, true),
    enabled: !!workflowId && !isFirstSave,
  });

  const updateMutation = useMutation({
    mutationFn: async (data: {
      spec: string; // YAML string
      name: string;
      id: string;
    }) => {
      // Get current workflow (latest version) to check if deployed
      const currentWorkflow = await workflowApi.get(data.id, undefined, true);
      
      // Pass the current version - backend will handle versioning:
      // - If not deployed, it will update in place
      // - If deployed, it will create a new version automatically
      await workflowApi.create({
        workflow_id: data.id,
        name: data.name,
        version: currentWorkflow.version,
        spec: data.spec, // YAML string
        created_by: currentWorkflow.created_by || 'user',
      });

      // Update name if changed (only if not creating new version)
      if (data.name !== currentWorkflow.name && !currentWorkflow.deployed) {
        await workflowApi.update(
          data.id,
          {
            name: data.name,
          },
          currentWorkflow.version
        );
      }
    },
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      queryClient.invalidateQueries({ queryKey: ['workflow', variables.id] });
      queryClient.invalidateQueries({ queryKey: ['workflow', variables.id, 'latest'] });
      toast.success('Workflow saved successfully');
    },
    onError: (error) => {
      toast.error('Failed to save workflow', {
        description: error instanceof Error ? error.message : 'Unknown error occurred',
      });
    },
  });

  const deployMutation = useMutation({
    mutationFn: () => workflowApi.deploy(workflowId!, workflow?.version || 1),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflow', workflowId] });
      queryClient.invalidateQueries({ queryKey: ['workflow', workflowId, 'latest'] });
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      queryClient.invalidateQueries({ queryKey: ['previous-deployed-versions', workflowId] });
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
    queryFn: () => workflowApi.getPreviousDeployedVersions(workflowId!, workflow?.version || 0),
    enabled: !!workflowId && !!workflow && workflow.deployed && workflow.version > 1,
  });

  const rollbackMutation = useMutation({
    mutationFn: (version: number) => workflowApi.rollback(workflowId!, version),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflow', workflowId] });
      queryClient.invalidateQueries({ queryKey: ['workflow', workflowId, 'latest'] });
      queryClient.invalidateQueries({ queryKey: ['workflows'] });
      queryClient.invalidateQueries({ queryKey: ['previous-deployed-versions', workflowId] });
      toast.success('Workflow rolled back successfully');
      setRollbackDialogOpen(false);
      setSelectedRollbackVersion(null);
      router.refresh();
    },
    onError: (error) => {
      toast.error('Failed to rollback workflow', {
        description: error instanceof Error ? error.message : 'Unknown error occurred',
      });
    },
  });

  const handleSave = async (args: {
    spec: string; // YAML string
    name: string;
    id: string;
  }) => {
    if (isFirstSave || !workflowId) {
      await createMutation.mutateAsync(args);
    } else {
      await updateMutation.mutateAsync(args);
    }
  };

  const handleTemplateSelect = async (templateId: string) => {
    try {
      const template = await templateApi.getWorkflowTemplate(templateId);
      // Template spec_json is JSON, convert to YAML
      const specObj = typeof template.spec_json === 'string' 
        ? JSON.parse(template.spec_json) 
        : template.spec_json;
      const yaml = require('yaml');
      setInitialSpec(yaml.stringify(specObj, { indent: 2 }));
      setSelectedTemplate(templateId);
      setTemplateDialogOpen(false);
    } catch (err) {
      console.error('Failed to load template:', err);
    }
  };

  const handleSkipTemplate = () => {
    setTemplateDialogOpen(false);
    // Use default empty template
    setInitialSpec(undefined);
  };

  const isSaving = createMutation.isPending || updateMutation.isPending;

  return (
    <>
      <Dialog open={templateDialogOpen} onOpenChange={(open) => {
        if (!open) {
          // If dialog is being closed and no template was selected, use blank template
          if (!selectedTemplate && initialSpec === undefined) {
            handleSkipTemplate();
          }
        } else {
          setTemplateDialogOpen(open);
        }
      }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Select Workflow Template</DialogTitle>
            <DialogDescription>
              Choose a template to start with, or skip to create a blank workflow
            </DialogDescription>
          </DialogHeader>
          <div className="max-h-[500px] overflow-y-auto space-y-2">
            {loadingTemplates ? (
              <p className="text-sm text-muted-foreground">Loading templates...</p>
            ) : workflowTemplates.length === 0 ? (
              <p className="text-sm text-muted-foreground">No templates available</p>
            ) : (
              workflowTemplates.map((template) => (
                <div
                  key={template.id}
                  className={`p-3 border rounded-md hover:bg-accent cursor-pointer ${
                    selectedTemplate === template.id ? 'border-primary bg-accent' : ''
                  }`}
                  onClick={() => handleTemplateSelect(template.id)}
                >
                  <div className="font-medium">{template.name}</div>
                  {template.description && (
                    <div className="text-sm text-muted-foreground mt-1">
                      {template.description}
                    </div>
                  )}
                </div>
              ))
            )}
          </div>
          <div className="flex justify-end gap-2 mt-4">
            <Button variant="outline" onClick={handleSkipTemplate}>
              Skip (Blank Workflow)
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      <div className="h-[calc(100vh-8rem)] flex flex-col">
        <JsonWorkflowEditor
          onSave={handleSave}
          isSaving={isSaving}
          initialId={workflowId ?? undefined}
          initialSpec={initialSpec}
          isNew={true}
          workflow={workflow ? {
            version: workflow.version,
            deployed: workflow.deployed,
            deployedVersion: deployedWorkflow?.version,
          } : undefined}
          onDeploy={workflowId && workflow ? () => deployMutation.mutate() : undefined}
          onRollback={workflowId && workflow ? () => setRollbackDialogOpen(true) : undefined}
          isDeploying={deployMutation.isPending}
          isRollbackDisabled={!workflow || !workflow.deployed || (previousDeployedVersions?.length || 0) === 0}
        />
      </div>

      {workflowId && workflow && (
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
      )}
    </>
  );
}
