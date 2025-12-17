'use client';

import { useState, useRef, useEffect, useCallback } from 'react';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';

interface CELInputProps {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  rows?: number;
  className?: string;
  required?: boolean;
  id?: string;
  nodeIds?: string[]; // Available node IDs for tasks.* autocomplete
}

// CEL expression pattern: {{ expression }}
const CEL_PATTERN = /\{\{([^}]+)\}\}/g;

// Available CEL variables and their suggestions
const getCELSuggestions = (nodeIds: string[] = []) => ({
  base: [
    { label: 'config', value: 'config', description: 'Workflow configuration variables' },
    { label: 'input', value: 'input', description: 'Workflow input data' },
    { label: 'tasks', value: 'tasks', description: 'Previous task outputs' },
    { label: 'system', value: 'system', description: 'System variables (e.g., system.now)' },
  ],
  config: [
    { label: 'config.*', value: 'config.', description: 'Access configuration values' },
  ],
  input: [
    { label: 'input.*', value: 'input.', description: 'Access input data fields' },
  ],
  tasks: [
    { label: 'tasks.*.output', value: 'tasks.', description: 'Access task output by node ID' },
    { label: 'tasks.*.status', value: 'tasks.', description: 'Access task status by node ID' },
    ...nodeIds.map((nodeId) => ({
      label: `tasks.${nodeId}.output`,
      value: `tasks.${nodeId}.output.`,
      description: `Output from node: ${nodeId}`,
    })),
    ...nodeIds.map((nodeId) => ({
      label: `tasks.${nodeId}.status`,
      value: `tasks.${nodeId}.status`,
      description: `Status of node: ${nodeId}`,
    })),
  ],
  system: [
    { label: 'system.now', value: 'system.now', description: 'Current timestamp' },
  ],
});

export function CELInput({
  value,
  onChange,
  placeholder,
  rows,
  className,
  required,
  id,
  nodeIds = [],
}: CELInputProps) {
  const [showAutocomplete, setShowAutocomplete] = useState(false);
  const [autocompletePosition, setAutocompletePosition] = useState({ top: 0, left: 0 });
  const [suggestions, setSuggestions] = useState<typeof CEL_SUGGESTIONS.base>([]);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement | HTMLTextAreaElement>(null);
  const autocompleteRef = useRef<HTMLDivElement>(null);

  // Find CEL expressions in text and highlight them
  const highlightCELExpressions = useCallback((text: string) => {
    if (!text) return [];
    
    const parts: Array<{ text: string; isCEL: boolean }> = [];
    let lastIndex = 0;
    let match;

    while ((match = CEL_PATTERN.exec(text)) !== null) {
      // Add text before the CEL expression
      if (match.index > lastIndex) {
        parts.push({
          text: text.substring(lastIndex, match.index),
          isCEL: false,
        });
      }
      // Add the CEL expression
      parts.push({
        text: match[0],
        isCEL: true,
      });
      lastIndex = match.index + match[0].length;
    }

    // Add remaining text
    if (lastIndex < text.length) {
      parts.push({
        text: text.substring(lastIndex),
        isCEL: false,
      });
    }

    return parts;
  }, []);

  // Get suggestions based on current cursor position and text
  const getSuggestions = useCallback((text: string, cursorPos: number) => {
    const suggestions = getCELSuggestions(nodeIds);
    // Find if we're inside a CEL expression
    const beforeCursor = text.substring(0, cursorPos);
    const afterCursor = text.substring(cursorPos);
    
    // Find the opening {{ before cursor
    const lastOpen = beforeCursor.lastIndexOf('{{');
    const lastClose = beforeCursor.lastIndexOf('}}');
    
    // If we're inside a CEL expression (opening {{ found and no closing }} after it)
    if (lastOpen !== -1 && (lastClose === -1 || lastClose < lastOpen)) {
      const exprText = beforeCursor.substring(lastOpen + 2);
      
      // Check what we're typing
      if (exprText.trim() === '' || exprText.match(/^\s*$/)) {
        return suggestions.base;
      }
      
      // Check if it starts with a known variable
      if (exprText.trim().startsWith('config')) {
        return suggestions.config;
      } else if (exprText.trim().startsWith('input')) {
        return suggestions.input;
      } else if (exprText.trim().startsWith('tasks')) {
        return suggestions.tasks;
      } else if (exprText.trim().startsWith('system')) {
        return suggestions.system;
      }
      
      return suggestions.base;
    }
    
    return [];
  }, [nodeIds]);

  const handleChange = (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => {
    const newValue = e.target.value;
    const cursorPos = e.target.selectionStart || 0;
    
    onChange(newValue);
    
    // Check if we should show autocomplete
    const suggestions = getSuggestions(newValue, cursorPos);
    if (suggestions.length > 0) {
      setSuggestions(suggestions);
      setShowAutocomplete(true);
      setSelectedIndex(0);
      
      // Calculate position for autocomplete dropdown (for textarea, calculate line position)
      if (inputRef.current && rows) {
        const textBeforeCursor = newValue.substring(0, cursorPos);
        const lines = textBeforeCursor.split('\n');
        const currentLine = lines.length - 1;
        const lineHeight = 20; // Approximate line height for textarea
        
        setAutocompletePosition({
          top: (currentLine + 1) * lineHeight,
          left: 0,
        });
      } else {
        setAutocompletePosition({ top: 0, left: 0 });
      }
    } else {
      setShowAutocomplete(false);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement | HTMLTextAreaElement>) => {
    if (showAutocomplete && suggestions.length > 0) {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setSelectedIndex((prev) => (prev + 1) % suggestions.length);
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setSelectedIndex((prev) => (prev - 1 + suggestions.length) % suggestions.length);
      } else if (e.key === 'Enter' || e.key === 'Tab') {
        e.preventDefault();
        insertSuggestion(suggestions[selectedIndex].value);
      } else if (e.key === 'Escape') {
        setShowAutocomplete(false);
      }
    }
  };

  const insertSuggestion = (suggestion: string) => {
    if (!inputRef.current) return;
    
    const input = inputRef.current;
    const start = input.selectionStart || 0;
    const end = input.selectionEnd || 0;
    const text = value;
    
    // Find the CEL expression we're in
    const beforeCursor = text.substring(0, start);
    const lastOpen = beforeCursor.lastIndexOf('{{');
    
    if (lastOpen !== -1) {
      const afterOpen = beforeCursor.substring(lastOpen + 2);
      const exprText = afterOpen.trim();
      
      // Replace the current partial expression with the suggestion
      const newText =
        text.substring(0, lastOpen + 2) +
        suggestion +
        text.substring(end);
      
      onChange(newText);
      
      // Set cursor position after the inserted suggestion
      setTimeout(() => {
        if (inputRef.current) {
          const newPos = lastOpen + 2 + suggestion.length;
          inputRef.current.setSelectionRange(newPos, newPos);
          inputRef.current.focus();
        }
      }, 0);
    }
    
    setShowAutocomplete(false);
  };

  // Close autocomplete when clicking outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (
        autocompleteRef.current &&
        !autocompleteRef.current.contains(event.target as Node) &&
        inputRef.current &&
        !inputRef.current.contains(event.target as Node)
      ) {
        setShowAutocomplete(false);
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  const parts = highlightCELExpressions(value);
  const hasCEL = parts.some((p) => p.isCEL);

  const InputComponent = rows ? Textarea : Input;
  const inputProps = {
    ref: inputRef as any,
    value,
    onChange: handleChange,
    onKeyDown: handleKeyDown,
    placeholder,
    className: cn(className, hasCEL && 'font-mono'),
    required,
    id,
    ...(rows ? { rows } : {}),
  };

  return (
    <div className="relative w-full">
      <InputComponent {...inputProps} />
      
      {/* Visual CEL expression indicator */}
      {hasCEL && (
        <div className="absolute top-2 right-2 text-xs font-medium text-blue-600 dark:text-blue-400 bg-blue-50 dark:bg-blue-950 px-2 py-0.5 rounded border border-blue-200 dark:border-blue-800">
          CEL
        </div>
      )}
      
      {/* Autocomplete dropdown */}
      {showAutocomplete && suggestions.length > 0 && (
        <div
          ref={autocompleteRef}
          className="absolute z-50 w-[320px] rounded-md border bg-popover shadow-lg max-h-[240px] overflow-auto"
          style={{
            top: rows ? `${autocompletePosition.top}px` : '100%',
            left: '0',
            marginTop: rows ? '4px' : '2px',
          }}
        >
          <div className="p-1">
            {suggestions.map((suggestion, index) => (
              <div
                key={suggestion.value}
                onClick={() => insertSuggestion(suggestion.value)}
                className={cn(
                  'cursor-pointer rounded-sm px-2 py-1.5 text-sm transition-colors',
                  index === selectedIndex
                    ? 'bg-accent text-accent-foreground'
                    : 'hover:bg-accent/50'
                )}
              >
                <div className="flex flex-col">
                  <span className="font-medium">{suggestion.label}</span>
                  <span className="text-xs text-muted-foreground">
                    {suggestion.description}
                  </span>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
