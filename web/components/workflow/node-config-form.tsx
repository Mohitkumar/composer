'use client';

import { useState, useEffect } from 'react';
import { Label } from '@/components/ui/label';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Button } from '@/components/ui/button';
import { Plus, X } from 'lucide-react';
import { getNodeFormSchema, type NodeFormSchema } from '@/lib/node-schemas';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Separator } from '@/components/ui/separator';
import { CELInput } from './cel-input';
import { CodeEditor } from './code-editor';

interface NodeConfigFormProps {
  runtime: string;
  spec: Record<string, any>;
  onChange: (spec: Record<string, any>) => void;
  nodeIds?: string[]; // Available node IDs for tasks.* autocomplete
}

export function NodeConfigForm({ runtime, spec, onChange, nodeIds = [] }: NodeConfigFormProps) {
  const schema = getNodeFormSchema(runtime);
  const [formData, setFormData] = useState<Record<string, any>>(spec || {});

  useEffect(() => {
    // Reset form data when runtime or spec changes
    setFormData(spec || {});
  }, [spec, runtime]);

  const updateField = (key: string, value: any) => {
    const newData = { ...formData, [key]: value };
    setFormData(newData);
    onChange(newData);
  };

  const updateKeyValueField = (key: string, index: number, field: 'key' | 'value', newValue: string) => {
    const current = formData[key] || {};
    const entries = Object.entries(current);
    entries[index][field === 'key' ? 0 : 1] = newValue;
    const updated = Object.fromEntries(entries);
    updateField(key, updated);
  };

  const addKeyValuePair = (key: string) => {
    const current = formData[key] || {};
    const entries = Object.entries(current);
    const newKey = `key${entries.length + 1}`;
    const newData = { ...formData, [key]: { ...current, [newKey]: '' } };
    setFormData(newData);
    onChange(newData);
  };

  const removeKeyValuePair = (key: string, index: number) => {
    const current = formData[key] || {};
    const entries = Object.entries(current);
    entries.splice(index, 1);
    const updated = Object.fromEntries(entries);
    updateField(key, updated);
  };

  const renderField = (field: typeof schema.fields[0]) => {
    const value = formData[field.key] ?? field.defaultValue ?? '';

    switch (field.type) {
      case 'text':
        const textValue = typeof value === 'string' ? value : (typeof value === 'object' ? JSON.stringify(value) : String(value || ''));
        return (
          <div key={field.key} className="space-y-2">
            <Label htmlFor={field.key}>
              {field.label}
              {field.required && <span className="text-destructive ml-1">*</span>}
            </Label>
            {field.supportsCEL ? (
              <CELInput
                id={field.key}
                value={textValue}
                onChange={(val) => updateField(field.key, val)}
                placeholder={field.placeholder}
                required={field.required}
                nodeIds={nodeIds}
              />
            ) : (
              <Input
                id={field.key}
                value={textValue}
                onChange={(e) => updateField(field.key, e.target.value)}
                placeholder={field.placeholder}
                required={field.required}
              />
            )}
            {field.description && (
              <p className="text-xs text-muted-foreground">{field.description}</p>
            )}
          </div>
        );

      case 'textarea':
        const textareaValue = typeof value === 'string' ? value : (typeof value === 'object' ? JSON.stringify(value, null, 2) : String(value || ''));
        return (
          <div key={field.key} className="space-y-2">
            <Label htmlFor={field.key}>
              {field.label}
              {field.required && <span className="text-destructive ml-1">*</span>}
            </Label>
            {field.isCode && field.codeLanguage ? (
              <CodeEditor
                value={textareaValue}
                onChange={(val) => updateField(field.key, val)}
                language={field.codeLanguage}
                placeholder={field.placeholder}
                height={`${(field.rows || 20) * 20}px`}
                nodeIds={nodeIds}
              />
            ) : field.supportsCEL ? (
              <CELInput
                id={field.key}
                value={textareaValue}
                onChange={(val) => updateField(field.key, val)}
                placeholder={field.placeholder}
                rows={field.rows || 4}
                required={field.required}
                nodeIds={nodeIds}
              />
            ) : (
              <Textarea
                id={field.key}
                value={textareaValue}
                onChange={(e) => updateField(field.key, e.target.value)}
                placeholder={field.placeholder}
                rows={field.rows || 4}
                className="font-mono text-sm"
                required={field.required}
              />
            )}
            {field.description && (
              <p className="text-xs text-muted-foreground">{field.description}</p>
            )}
          </div>
        );

      case 'number':
        return (
          <div key={field.key} className="space-y-2">
            <Label htmlFor={field.key}>
              {field.label}
              {field.required && <span className="text-destructive ml-1">*</span>}
            </Label>
            <Input
              id={field.key}
              type="number"
              value={value}
              onChange={(e) => updateField(field.key, parseInt(e.target.value) || 0)}
              placeholder={field.placeholder}
              required={field.required}
            />
            {field.description && (
              <p className="text-xs text-muted-foreground">{field.description}</p>
            )}
          </div>
        );

      case 'select':
        return (
          <div key={field.key} className="space-y-2">
            <Label htmlFor={field.key}>
              {field.label}
              {field.required && <span className="text-destructive ml-1">*</span>}
            </Label>
            <Select
              value={String(value || field.defaultValue || '')}
              onValueChange={(val) => {
                // Handle boolean strings
                if (val === 'true') updateField(field.key, true);
                else if (val === 'false') updateField(field.key, false);
                else updateField(field.key, val);
              }}
            >
              <SelectTrigger>
                <SelectValue placeholder={field.placeholder} />
              </SelectTrigger>
              <SelectContent>
                {field.options?.map((opt) => (
                  <SelectItem key={opt.value} value={opt.value}>
                    {opt.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {field.description && (
              <p className="text-xs text-muted-foreground">{field.description}</p>
            )}
          </div>
        );

      case 'keyvalue':
        const kvData = typeof value === 'object' && value !== null ? value : {};
        const entries = Object.entries(kvData);
        return (
          <div key={field.key} className="space-y-2">
            <Label>
              {field.label}
              {field.required && <span className="text-destructive ml-1">*</span>}
            </Label>
            <div className="space-y-2 border rounded-md p-3">
              {entries.map(([k, v], index) => (
                <div key={index} className="flex gap-2 items-center">
                  <Input
                    placeholder="Key"
                    value={k}
                    onChange={(e) => {
                      const newKey = e.target.value;
                      const newKv = { ...kvData };
                      delete newKv[k];
                      newKv[newKey] = v;
                      updateField(field.key, newKv);
                    }}
                    className="flex-1"
                  />
                  <Input
                    placeholder="Value"
                    value={String(v)}
                    onChange={(e) => updateKeyValueField(field.key, index, 'value', e.target.value)}
                    className="flex-1"
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    onClick={() => removeKeyValuePair(field.key, index)}
                  >
                    <X className="h-4 w-4" />
                  </Button>
                </div>
              ))}
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => addKeyValuePair(field.key)}
                className="w-full"
              >
                <Plus className="h-4 w-4 mr-2" />
                Add {field.label.slice(0, -1)}
              </Button>
            </div>
            {field.description && (
              <p className="text-xs text-muted-foreground">{field.description}</p>
            )}
          </div>
        );

      case 'json':
        return (
          <div key={field.key} className="space-y-2">
            <Label htmlFor={field.key}>
              {field.label}
              {field.required && <span className="text-destructive ml-1">*</span>}
            </Label>
            <Textarea
              id={field.key}
              value={typeof value === 'string' ? value : JSON.stringify(value, null, 2)}
              onChange={(e) => {
                try {
                  const parsed = JSON.parse(e.target.value);
                  updateField(field.key, parsed);
                } catch {
                  updateField(field.key, e.target.value);
                }
              }}
              placeholder={field.placeholder}
              rows={field.rows || 6}
              className="font-mono text-sm"
              required={field.required}
            />
            {field.description && (
              <p className="text-xs text-muted-foreground">{field.description}</p>
            )}
          </div>
        );

      default:
        return null;
    }
  };

  if (schema.fields.length === 0) {
    return (
      <div className="text-center py-8 text-muted-foreground">
        <p>No configuration form available for runtime: {runtime}</p>
        <p className="text-xs mt-2">Please select a supported runtime type.</p>
      </div>
    );
  }

  return (
    <div className="space-y-6 pr-2 pb-4">
      <div className="space-y-6">
        {schema.fields.map((field) => renderField(field))}
      </div>
    </div>
  );
}
