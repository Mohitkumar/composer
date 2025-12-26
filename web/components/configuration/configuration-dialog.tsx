'use client';

import { useState, useEffect } from 'react';
import { configurationApi, type Configuration } from '@/lib/api-client';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import Editor from '@monaco-editor/react';
import { toast } from 'sonner';
import { ScrollArea } from '@/components/ui/scroll-area';

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
    auth_type: 'none',
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

interface ConfigurationDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  configId?: string; // If provided, edit mode; otherwise, create mode
}

export function ConfigurationDialog({
  open,
  onOpenChange,
  configId,
}: ConfigurationDialogProps) {
  const queryClient = useQueryClient();
  const isEditMode = !!configId;

  const { data: config, isLoading } = useQuery({
    queryKey: ['configuration', configId],
    queryFn: () => (configId ? configurationApi.get(configId) : null),
    enabled: isEditMode && open && !!configId,
  });

  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [type, setType] = useState<string>('');
  const [configJson, setConfigJson] = useState('{}');
  const [validationError, setValidationError] = useState<string | null>(null);

  // Reset form when dialog opens/closes or config changes
  useEffect(() => {
    if (!open) {
      setName('');
      setDescription('');
      setType('');
      setConfigJson('{}');
      setValidationError(null);
    } else if (isEditMode && config) {
      setName(config.name);
      setDescription(config.description || '');
      setType(config.type);
      setConfigJson(JSON.stringify(config.config_json, null, 2));
      setValidationError(null);
    } else if (!isEditMode) {
      setName('');
      setDescription('');
      setType('');
      setConfigJson('{}');
      setValidationError(null);
    }
  }, [open, config, isEditMode]);

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
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['configurations'] });
      toast.success('Configuration created successfully!');
      onOpenChange(false);
    },
    onError: (error) => {
      toast.error('Failed to create configuration', {
        description: error instanceof Error ? error.message : 'An unknown error occurred',
      });
    },
  });

  const updateMutation = useMutation({
    mutationFn: async () => {
      let parsedConfig: Record<string, any>;
      try {
        parsedConfig = JSON.parse(configJson);
      } catch (err: any) {
        throw new Error(`Invalid JSON: ${err.message}`);
      }

      if (!configId) throw new Error('Configuration ID is required');

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
      onOpenChange(false);
    },
    onError: (error) => {
      toast.error('Failed to update configuration', {
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

    if (!isEditMode && !type) {
      setValidationError('Type is required');
      return;
    }

    try {
      JSON.parse(configJson);
    } catch (err: any) {
      setValidationError(`Invalid JSON: ${err.message}`);
      return;
    }

    if (isEditMode) {
      updateMutation.mutate();
    } else {
      createMutation.mutate();
    }
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

  const isSaving = createMutation.isPending || updateMutation.isPending;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-4xl max-h-[90vh] flex flex-col overflow-hidden">
        <DialogHeader className="flex-shrink-0">
          <DialogTitle>{isEditMode ? 'Edit Configuration' : 'Create Configuration'}</DialogTitle>
          <DialogDescription>
            {isEditMode
              ? `Update configuration: ${config?.name || ''}`
              : 'Create a reusable configuration template'}
          </DialogDescription>
        </DialogHeader>

        {isEditMode && isLoading ? (
          <div className="flex items-center justify-center py-12 flex-shrink-0">
            <div className="text-center">
              <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary mx-auto mb-4"></div>
              <p className="text-muted-foreground">Loading configuration...</p>
            </div>
          </div>
        ) : (
          <ScrollArea className="flex-1 min-h-0 pr-4">
            <div className="grid gap-4 py-4">
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <div className="space-y-1.5">
                  <Label htmlFor="name">
                    Name <span className="text-destructive">*</span>
                  </Label>
                  <Input
                    id="name"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="My S3 Configuration"
                    disabled={isSaving}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="type">
                    {isEditMode ? 'Type' : 'Type '}
                    {!isEditMode && <span className="text-destructive">*</span>}
                  </Label>
                  {isEditMode ? (
                    <Input
                      id="type"
                      value={type}
                      disabled
                      className="bg-muted"
                    />
                  ) : (
                    <Select value={type} onValueChange={handleTypeChange} disabled={isSaving}>
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
                  )}
                </div>
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="description">Description</Label>
                <Textarea
                  id="description"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="Optional description for this configuration"
                  rows={2}
                  disabled={isSaving}
                />
              </div>

              <div className="space-y-1.5">
                <div className="flex items-center justify-between">
                  <Label htmlFor="config-json">
                    Configuration JSON <span className="text-destructive">*</span>
                  </Label>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={handleFormat}
                    disabled={isSaving}
                  >
                    Format JSON
                  </Button>
                </div>
                <div className="border rounded-md overflow-hidden" style={{ height: '200px' }}>
                  <Editor
                    height="100%"
                    defaultLanguage="json"
                    value={configJson}
                    onChange={(value) => setConfigJson(value || '{}')}
                    options={{
                      minimap: { enabled: false },
                      fontSize: 13,
                      lineNumbers: 'on',
                      tabSize: 2,
                      wordWrap: 'on',
                      formatOnPaste: true,
                      formatOnType: true,
                      automaticLayout: true,
                      scrollBeyondLastLine: false,
                      readOnly: isSaving,
                    }}
                    theme="vs-dark"
                  />
                </div>
                {validationError && (
                  <p className="text-sm text-destructive">{validationError}</p>
                )}
                {!isEditMode && (
                  <p className="text-xs text-muted-foreground">
                    Enter the configuration data as JSON. A template will be pre-filled based on the selected type.
                  </p>
                )}
              </div>
            </div>
          </ScrollArea>
        )}

        <DialogFooter className="flex-shrink-0">
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={isSaving}
          >
            Cancel
          </Button>
          <Button onClick={handleSave} disabled={isSaving}>
            {isSaving
              ? isEditMode
                ? 'Saving...'
                : 'Creating...'
              : isEditMode
              ? 'Save Configuration'
              : 'Create Configuration'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
