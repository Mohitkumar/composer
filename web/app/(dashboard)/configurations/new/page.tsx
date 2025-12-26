'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { configurationApi } from '@/lib/api-client';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import Editor from '@monaco-editor/react';
import type { Monaco } from '@monaco-editor/react';
import { toast } from 'sonner';

const CONFIG_TYPES = [
  { value: 's3', label: 'S3' },
  { value: 'http', label: 'HTTP' },
  { value: 'database', label: 'Database' },
  { value: 'api_key', label: 'API Key' },
  { value: 'oauth', label: 'OAuth' },
  { value: 'custom', label: 'Custom' },
];

// Default templates for different config types
const CONFIG_TEMPLATES: Record<string, Record<string, any>> = {
  s3: {
    bucket: '',
    region: 'us-east-1',
    access_key_id: '',
    secret_access_key: '',
  },
  http: {
    base_url: '',
    headers: {},
    auth_type: 'none', // none, basic, bearer, api_key
    username: '',
    password: '',
    api_key: '',
    api_key_header: 'X-API-Key',
  },
  database: {
    connection_string: '',
    host: '',
    port: 5432,
    database: '',
    username: '',
    password: '',
    ssl_mode: 'disable',
  },
  api_key: {
    api_key: '',
    header_name: 'X-API-Key',
  },
  oauth: {
    client_id: '',
    client_secret: '',
    token_url: '',
    scope: '',
  },
  custom: {},
};

export default function NewConfigurationPage() {
  const router = useRouter();
  const queryClient = useQueryClient();
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [type, setType] = useState<string>('');
  const [configJson, setConfigJson] = useState('{}');
  const [validationError, setValidationError] = useState<string | null>(null);

  const createMutation = useMutation({
    mutationFn: async () => {
      let parsedConfig: Record<string, any>;
      try {
        parsedConfig = JSON.parse(configJson);
      } catch (err: any) {
        throw new Error(`Invalid JSON: ${err.message}`);
      }

      const id = `config_${Date.now()}`;
      await configurationApi.create({
        id,
        name,
        description: description || undefined,
        type,
        config_json: parsedConfig,
        // created_by is automatically set from JWT token on the backend
      });
      return id;
    },
    onSuccess: (id) => {
      queryClient.invalidateQueries({ queryKey: ['configurations'] });
      toast.success('Configuration created successfully!');
      router.push('/configurations');
    },
    onError: (error) => {
      toast.error('Failed to create configuration', {
        description: error instanceof Error ? error.message : 'An unknown error occurred',
      });
    },
  });

  const handleTypeChange = (newType: string) => {
    setType(newType);
    if (newType && CONFIG_TEMPLATES[newType]) {
      setConfigJson(JSON.stringify(CONFIG_TEMPLATES[newType], null, 2));
    } else {
      setConfigJson('{}');
    }
  };

  const handleSave = () => {
    setValidationError(null);

    if (!name.trim()) {
      setValidationError('Name is required');
      return;
    }

    if (!type) {
      setValidationError('Type is required');
      return;
    }

    try {
      JSON.parse(configJson);
    } catch (err: any) {
      setValidationError(`Invalid JSON: ${err.message}`);
      return;
    }

    createMutation.mutate();
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

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold tracking-tight">Create Configuration</h1>
          <p className="text-muted-foreground">
            Create a reusable configuration template
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
            <Label htmlFor="type">
              Type <span className="text-destructive">*</span>
            </Label>
            <Select value={type} onValueChange={handleTypeChange}>
              <SelectTrigger>
                <SelectValue placeholder="Select configuration type" />
              </SelectTrigger>
              <SelectContent>
                {CONFIG_TYPES.map((t) => (
                  <SelectItem key={t.value} value={t.value}>
                    {t.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
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
          <p className="text-xs text-muted-foreground">
            Enter the configuration data as JSON. A template will be pre-filled based on the selected type.
          </p>
        </div>

        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={() => router.back()}>
            Cancel
          </Button>
          <Button onClick={handleSave} disabled={createMutation.isPending}>
            {createMutation.isPending ? 'Creating...' : 'Create Configuration'}
          </Button>
        </div>
      </div>
    </div>
  );
}
