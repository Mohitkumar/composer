'use client';

import { useState, useEffect } from 'react';
import { useParams, useRouter } from 'next/navigation';
import { configurationApi } from '@/lib/api-client';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import Editor from '@monaco-editor/react';
import { toast } from 'sonner';

export default function EditConfigurationPage() {
  const params = useParams();
  const router = useRouter();
  const configId = params.id as string;
  const queryClient = useQueryClient();

  const { data: config, isLoading } = useQuery({
    queryKey: ['configuration', configId],
    queryFn: () => configurationApi.get(configId),
  });

  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [configJson, setConfigJson] = useState('{}');
  const [validationError, setValidationError] = useState<string | null>(null);

  useEffect(() => {
    if (config) {
      setName(config.name);
      setDescription(config.description || '');
      setConfigJson(JSON.stringify(config.config_json, null, 2));
    }
  }, [config]);

  const updateMutation = useMutation({
    mutationFn: async () => {
      let parsedConfig: Record<string, any>;
      try {
        parsedConfig = JSON.parse(configJson);
      } catch (err: any) {
        throw new Error(`Invalid JSON: ${err.message}`);
      }

      await configurationApi.update(configId, {
        name,
        description: description || undefined,
        config_json: parsedConfig,
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['configurations'] });
      queryClient.invalidateQueries({ queryKey: ['configuration', configId] });
      toast.success('Configuration updated successfully!');
      router.push('/configurations');
    },
    onError: (error) => {
      toast.error('Failed to update configuration', {
        description: error instanceof Error ? error.message : 'An unknown error occurred',
      });
    },
  });

  const handleSave = () => {
    setValidationError(null);

    if (!name.trim()) {
      setValidationError('Name is required');
      return;
    }

    try {
      JSON.parse(configJson);
    } catch (err: any) {
      setValidationError(`Invalid JSON: ${err.message}`);
      return;
    }

    updateMutation.mutate();
  };

  const handleFormat = () => {
    try {
      const parsed = JSON.parse(configJson);
      setConfigJson(JSON.stringify(parsed, null, 2));
      setValidationError(null);
    } catch (err: any) {
      setValidationError(`Invalid JSON: ${err.message}`);
    }
  };

  if (isLoading) {
    return <div>Loading configuration...</div>;
  }

  if (!config) {
    return <div>Configuration not found</div>;
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Edit Configuration</h1>
          <p className="text-muted-foreground">
            Update configuration: {config.name}
          </p>
        </div>
      </div>

      <div className="grid gap-6">
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label htmlFor="name">
              Name <span className="text-destructive">*</span>
            </Label>
            <Input
              id="name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="My S3 Configuration"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="type">Type</Label>
            <Input
              id="type"
              value={config.type}
              disabled
              className="bg-muted"
            />
          </div>
        </div>

        <div className="space-y-2">
          <Label htmlFor="description">Description</Label>
          <Textarea
            id="description"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Optional description for this configuration"
            rows={2}
          />
        </div>

        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <Label htmlFor="config-json">
              Configuration JSON <span className="text-destructive">*</span>
            </Label>
            <Button type="button" variant="outline" size="sm" onClick={handleFormat}>
              Format JSON
            </Button>
          </div>
          <div className="border rounded-md overflow-hidden" style={{ height: '400px' }}>
            <Editor
              height="100%"
              defaultLanguage="json"
              value={configJson}
              onChange={(value) => setConfigJson(value || '{}')}
              options={{
                minimap: { enabled: false },
                fontSize: 14,
                lineNumbers: 'on',
                tabSize: 2,
                wordWrap: 'on',
                formatOnPaste: true,
                formatOnType: true,
                automaticLayout: true,
                scrollBeyondLastLine: false,
              }}
              theme="vs-dark"
            />
          </div>
          {validationError && (
            <p className="text-sm text-destructive">{validationError}</p>
          )}
        </div>

        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={() => router.back()}>
            Cancel
          </Button>
          <Button onClick={handleSave} disabled={updateMutation.isPending}>
            {updateMutation.isPending ? 'Saving...' : 'Save Configuration'}
          </Button>
        </div>
      </div>
    </div>
  );
}
