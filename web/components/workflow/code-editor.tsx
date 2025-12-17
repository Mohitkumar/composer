'use client';

import { useRef, useEffect } from 'react';
import Editor from '@monaco-editor/react';
import type { Monaco } from '@monaco-editor/react';
import * as monaco from 'monaco-editor';

interface CodeEditorProps {
  value: string;
  onChange: (value: string) => void;
  language: 'javascript' | 'python';
  placeholder?: string;
  height?: string;
  nodeIds?: string[];
}

export function CodeEditor({
  value,
  onChange,
  language,
  placeholder,
  height = '400px',
  nodeIds = [],
}: CodeEditorProps) {
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const monacoRef = useRef<Monaco | null>(null);

  // Setup autocomplete for workflow variables
  useEffect(() => {
    if (!monacoRef.current) return;

    const monaco = monacoRef.current;
    let jsProvider: monaco.IDisposable | null = null;
    let pythonProvider: monaco.IDisposable | null = null;

    // Register completion item provider for JavaScript
    if (language === 'javascript') {
      const provider = monaco.languages.registerCompletionItemProvider('javascript', {
        provideCompletionItems: (model: monaco.editor.ITextModel, position: monaco.Position) => {
          const word = model.getWordUntilPosition(position);
          const range = {
            startLineNumber: position.lineNumber,
            endLineNumber: position.lineNumber,
            startColumn: word.startColumn,
            endColumn: word.endColumn,
          };

          const suggestions: monaco.languages.CompletionItem[] = [
            // Config suggestions
            {
              label: 'config',
              kind: monaco.languages.CompletionItemKind.Variable,
              documentation: 'Workflow configuration variables',
              insertText: 'config',
              range,
            },
            {
              label: 'config.*',
              kind: monaco.languages.CompletionItemKind.Property,
              documentation: 'Access configuration values (e.g., config.apiKey)',
              insertText: 'config.',
              range,
            },
            // Input suggestions
            {
              label: 'input',
              kind: monaco.languages.CompletionItemKind.Variable,
              documentation: 'Workflow input data',
              insertText: 'input',
              range,
            },
            {
              label: 'input.*',
              kind: monaco.languages.CompletionItemKind.Property,
              documentation: 'Access input data fields (e.g., input.userId)',
              insertText: 'input.',
              range,
            },
            // Tasks suggestions
            {
              label: 'tasks',
              kind: monaco.languages.CompletionItemKind.Variable,
              documentation: 'Previous task outputs',
              insertText: 'tasks',
              range,
            },
            {
              label: 'tasks.*.output',
              kind: monaco.languages.CompletionItemKind.Property,
              documentation: 'Access task output by node ID',
              insertText: 'tasks.',
              range,
            },
            {
              label: 'tasks.*.status',
              kind: monaco.languages.CompletionItemKind.Property,
              documentation: 'Access task status by node ID',
              insertText: 'tasks.',
              range,
            },
            // System suggestions
            {
              label: 'system',
              kind: monaco.languages.CompletionItemKind.Variable,
              documentation: 'System variables',
              insertText: 'system',
              range,
            },
            {
              label: 'system.now',
              kind: monaco.languages.CompletionItemKind.Property,
              documentation: 'Current timestamp',
              insertText: 'system.now',
              range,
            },
            // Node-specific suggestions
            ...nodeIds.map((nodeId) => ({
              label: `tasks.${nodeId}.output`,
              kind: monaco.languages.CompletionItemKind.Property,
              documentation: `Output from node: ${nodeId}`,
              insertText: `tasks.${nodeId}.output.`,
              range,
            })),
            ...nodeIds.map((nodeId) => ({
              label: `tasks.${nodeId}.status`,
              kind: monaco.languages.CompletionItemKind.Property,
              documentation: `Status of node: ${nodeId}`,
              insertText: `tasks.${nodeId}.status`,
              range,
            })),
          ];

          return { suggestions };
        },
      });

      jsProvider = provider;
    }

    // Register completion item provider for Python
    if (language === 'python') {
      const provider = monaco.languages.registerCompletionItemProvider('python', {
        provideCompletionItems: (model: monaco.editor.ITextModel, position: monaco.Position) => {
          const word = model.getWordUntilPosition(position);
          const range = {
            startLineNumber: position.lineNumber,
            endLineNumber: position.lineNumber,
            startColumn: word.startColumn,
            endColumn: word.endColumn,
          };

          const suggestions: monaco.languages.CompletionItem[] = [
            // Config suggestions
            {
              label: 'config',
              kind: monaco.languages.CompletionItemKind.Variable,
              documentation: 'Workflow configuration variables',
              insertText: 'config',
              range,
            },
            {
              label: 'config.*',
              kind: monaco.languages.CompletionItemKind.Property,
              documentation: 'Access configuration values (e.g., config["apiKey"])',
              insertText: 'config',
              range,
            },
            // Input suggestions
            {
              label: 'input',
              kind: monaco.languages.CompletionItemKind.Variable,
              documentation: 'Workflow input data',
              insertText: 'input',
              range,
            },
            {
              label: 'input.*',
              kind: monaco.languages.CompletionItemKind.Property,
              documentation: 'Access input data fields (e.g., input["userId"])',
              insertText: 'input',
              range,
            },
            // Tasks suggestions
            {
              label: 'tasks',
              kind: monaco.languages.CompletionItemKind.Variable,
              documentation: 'Previous task outputs',
              insertText: 'tasks',
              range,
            },
            {
              label: 'tasks.*.output',
              kind: monaco.languages.CompletionItemKind.Property,
              documentation: 'Access task output by node ID',
              insertText: 'tasks',
              range,
            },
            {
              label: 'tasks.*.status',
              kind: monaco.languages.CompletionItemKind.Property,
              documentation: 'Access task status by node ID',
              insertText: 'tasks',
              range,
            },
            // System suggestions
            {
              label: 'system',
              kind: monaco.languages.CompletionItemKind.Variable,
              documentation: 'System variables',
              insertText: 'system',
              range,
            },
            {
              label: 'system.now',
              kind: monaco.languages.CompletionItemKind.Property,
              documentation: 'Current timestamp',
              insertText: 'system.now',
              range,
            },
            // Node-specific suggestions
            ...nodeIds.map((nodeId) => ({
              label: `tasks.${nodeId}.output`,
              kind: monaco.languages.CompletionItemKind.Property,
              documentation: `Output from node: ${nodeId}`,
              insertText: `tasks["${nodeId}"]["output"]`,
              range,
            })),
            ...nodeIds.map((nodeId) => ({
              label: `tasks.${nodeId}.status`,
              kind: monaco.languages.CompletionItemKind.Property,
              documentation: `Status of node: ${nodeId}`,
              insertText: `tasks["${nodeId}"]["status"]`,
              range,
            })),
          ];

          return { suggestions };
        },
      });

      pythonProvider = provider;
    }

    return () => {
      jsProvider?.dispose();
      pythonProvider?.dispose();
    };
  }, [language, nodeIds]);

  const handleEditorDidMount = (
    editor: monaco.editor.IStandaloneCodeEditor,
    monacoInstance: Monaco
  ) => {
    editorRef.current = editor;
    monacoRef.current = monacoInstance;

    // Configure editor options
    editor.updateOptions({
      minimap: { enabled: false },
      fontSize: 14,
      lineNumbers: 'on',
      scrollBeyondLastLine: false,
      automaticLayout: true,
      tabSize: 2,
      wordWrap: 'on',
      formatOnPaste: true,
      formatOnType: false, // Disable format on type to prevent space key issues
      suggestOnTriggerCharacters: true,
      quickSuggestions: {
        other: false, // Disable automatic suggestions - only show on Ctrl+Space
        comments: false,
        strings: false,
      },
      acceptSuggestionOnCommitCharacter: false, // Don't accept on space/commit chars
      acceptSuggestionOnEnter: 'on',
      quickSuggestionsDelay: 500,
      snippetSuggestions: 'none',
      // Disable features that might interfere with space key
      tabCompletion: 'off',
      wordBasedSuggestions: 'off',
    });

    // Add a handler that ensures space key works in Chrome
    // Chrome handles keyboard events differently than Safari
    editor.onKeyDown((e) => {
      // Check for space key using both keyCode and key for browser compatibility
      const isSpace = e.keyCode === monacoInstance.KeyCode.Space || 
                      e.browserEvent.key === ' ' || 
                      e.browserEvent.code === 'Space';
      
      if (isSpace) {
        // Check if autocomplete is open
        const suggestController = editor.getContribution('editor.contrib.suggestController') as any;
        let isOpen = false;
        
        try {
          isOpen = suggestController?.widget?.value?.isOpen?.() || false;
        } catch (err) {
          isOpen = false;
        }
        
        if (isOpen) {
          // Dismiss autocomplete first
          try {
            suggestController.cancel();
          } catch (err) {
            // Ignore errors
          }
          
          // For Chrome, explicitly insert space after dismissing autocomplete
          // Use requestAnimationFrame for better timing
          requestAnimationFrame(() => {
            const position = editor.getPosition();
            if (position) {
              const range = new monacoInstance.Range(
                position.lineNumber,
                position.column,
                position.lineNumber,
                position.column
              );
              editor.executeEdits('chrome-space-fix', [{
                range,
                text: ' ',
              }]);
            }
          });
          
          e.preventDefault();
          e.stopPropagation();
          return false;
        }
        // If autocomplete is NOT open, don't prevent default
        // Let Monaco handle space normally - this is crucial for Chrome
      }
    });

    // Set cursor to top and scroll to top if editor is empty or has placeholder
    const currentValue = editor.getValue();
    if (!currentValue || currentValue.trim() === '' || (placeholder && currentValue === placeholder)) {
      editor.setPosition({ lineNumber: 1, column: 1 });
      editor.revealLineInCenter(1);
    }
  };

  const handleEditorChange = (value: string | undefined) => {
    onChange(value || '');
  };

  // Use placeholder as default value if editor is empty
  const editorValue = value || (placeholder || '');

  return (
    <div className="border rounded-md overflow-hidden relative w-full">
      <Editor
        height={height}
        language={language}
        value={editorValue}
        onChange={handleEditorChange}
        onMount={handleEditorDidMount}
        theme="vs-dark"
        width="100%"
        options={{
          minimap: { enabled: false },
          fontSize: 14,
          lineNumbers: 'on',
          scrollBeyondLastLine: false,
          automaticLayout: true,
          tabSize: 2,
          wordWrap: 'on',
          formatOnPaste: true,
          formatOnType: false,
          suggestOnTriggerCharacters: true,
          quickSuggestions: {
            other: false, // Disable automatic suggestions - only show on Ctrl+Space
            comments: false,
            strings: false,
          },
          // CRITICAL: These settings ensure space key works in all browsers
          acceptSuggestionOnCommitCharacter: false, // Don't accept suggestions on space
          acceptSuggestionOnEnter: 'on',
          quickSuggestionsDelay: 500,
          snippetSuggestions: 'none',
          tabCompletion: 'off',
          wordBasedSuggestions: 'off',
          // Additional Chrome-specific fixes
          autoIndent: 'full',
          autoClosingBrackets: 'always',
          autoClosingQuotes: 'always',
          parameterHints: {
            enabled: false,
          },
          multiCursorModifier: 'ctrlCmd',
        }}
      />
      <div className="px-3 py-2 bg-muted border-t text-xs text-muted-foreground flex-shrink-0">
        <span className="font-medium">Tip:</span> Use <kbd className="px-1.5 py-0.5 bg-background border rounded text-xs">Ctrl+Space</kbd> or <kbd className="px-1.5 py-0.5 bg-background border rounded text-xs">Cmd+Space</kbd> for autocomplete. Available variables: <code className="text-xs">config</code>, <code className="text-xs">input</code>, <code className="text-xs">tasks</code>, <code className="text-xs">system</code>
      </div>
    </div>
  );
}
