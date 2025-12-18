'use client';

import { useState, useEffect } from 'react';
import { JsonWorkflowEditor } from '@/components/workflow/json-workflow-editor';
import { workflowApi, templateApi } from '@/lib/api-client';
import { useMutation, useQueryClient, useQuery } from '@tanstack/react-query';
import * as yaml from 'yaml';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';

export default function NewWorkflowPage() {
  const queryClient = useQueryClient();
  const [isFirstSave, setIsFirstSave] = useState(true);
  const [workflowId, setWorkflowId] = useState<string | null>(null);
  const [templateDialogOpen, setTemplateDialogOpen] = useState(true);
  const [selectedTemplate, setSelectedTemplate] = useState<string | null>(null);
  const [initialSpec, setInitialSpec] = useState<WorkflowDefinitionSpec | undefined>(undefined);

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

  const updateMutation = useMutation({
    mutationFn: async (data: {
      spec: string; // YAML string
      name: string;
      id: string;
    }) => {
      // Get current workflow to determine next version
      const currentWorkflow = await workflowApi.get(data.id);
      const nextVersion = (currentWorkflow.version || 1) + 1;

      // Create new version with updated spec (YAML string)
      await workflowApi.create({
        workflow_id: data.id,
        name: data.name,
        version: nextVersion,
        spec: data.spec, // YAML string
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

      <div className="h-[calc(100vh-8rem)]">
        <JsonWorkflowEditor
          onSave={handleSave}
          isSaving={isSaving}
          initialId={workflowId ?? undefined}
          initialSpec={initialSpec}
          isNew={true}
        />
      </div>
    </>
  );
}
