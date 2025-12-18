import type { Monaco } from "@monaco-editor/react";

export function createCompletionProvider(
  monaco: Monaco,
  configurationsRef: React.MutableRefObject<any[]>,
  configMapByNameRef: React.MutableRefObject<Map<string, any>>
) {
  return monaco.languages.registerCompletionItemProvider("json", {
    triggerCharacters: [".", "c", "o", "n", "f", "i", "g"],
    provideCompletionItems: (model: any, position: any) => {
      const lineContent = model.getLineContent(position.lineNumber);
      const textBeforeCursor = lineContent.substring(0, position.column - 1);
      
      // Get word at cursor position
      const wordAtPosition = model.getWordUntilPosition(position);
      const wordRange = {
        startLineNumber: position.lineNumber,
        endLineNumber: position.lineNumber,
        startColumn: wordAtPosition.startColumn,
        endColumn: wordAtPosition.endColumn,
      };

      // Extract text from template strings or quoted strings
      let textToMatch = textBeforeCursor;
      const templateMatch = textBeforeCursor.match(/\{\{([^}]*)\}?\}?$/);
      if (templateMatch) {
        textToMatch = templateMatch[1];
      } else {
        const inStringMatch = textBeforeCursor.match(/["'`]([^"'`]*)$/);
        if (inStringMatch) {
          textToMatch = inStringMatch[1];
        }
      }
      
      // Match config pattern: config, config., config.name, or config.name.
      const configPattern = /config(\.([a-zA-Z0-9_-]+))?(\.)?$/;
      const match = textToMatch.match(configPattern);
      
      if (!match) {
        // Also try to match if we're in the middle of typing "config"
        if (textToMatch.endsWith("config") || textToMatch.endsWith("config.")) {
          return createConfigNameSuggestions(monaco, configurationsRef, wordRange);
        }
        return { suggestions: [] };
      }

      const configName = match[2];
      const hasTrailingDot = match[3] === "." || textToMatch.endsWith(".") || textBeforeCursor.endsWith(".");

      if (!configName) {
        // User is typing "config" or "config." - suggest all configuration names
        return createConfigNameSuggestions(monaco, configurationsRef, wordRange);
      }

      // We have a config name
      const exactConfig = configMapByNameRef.current.get(configName);
      
      if (exactConfig && hasTrailingDot) {
        // User typed "config.<name>." - suggest keys from that config's config_json
        return createConfigKeySuggestions(monaco, exactConfig, wordRange);
      } else if (exactConfig && !hasTrailingDot) {
        // User typed "config.<name>" but no trailing dot - suggest completing the name with dot
        return {
          suggestions: [{
            label: `config.${exactConfig.name}.`,
            kind: monaco.languages.CompletionItemKind.Property,
            insertText: `config.${exactConfig.name}.`,
            detail: exactConfig.description || `Configuration: ${exactConfig.name}`,
            documentation: exactConfig.type ? `Type: ${exactConfig.type}` : undefined,
            range: wordRange,
          }],
        };
      } else {
        // Config not found, but user might be typing it - suggest config names that match
        const partialMatch = configName.toLowerCase();
        const matchingConfigs = configurationsRef.current.filter((c: any) => 
          c.name.toLowerCase().startsWith(partialMatch)
        );
        return createConfigNameSuggestions(monaco, { current: matchingConfigs }, wordRange);
      }
    },
  });
}

function createConfigNameSuggestions(
  monaco: Monaco,
  configurationsRef: React.MutableRefObject<any[]>,
  wordRange: any
) {
  const suggestions: any[] = [];
  const seenNames = new Set<string>();
  
  configurationsRef.current.forEach((config: any) => {
    if (config.name && !seenNames.has(config.name)) {
      seenNames.add(config.name);
      suggestions.push({
        label: `config.${config.name}`,
        kind: monaco.languages.CompletionItemKind.Property,
        insertText: `config.${config.name}`,
        detail: config.description || `Configuration: ${config.name}`,
        documentation: config.type ? `Type: ${config.type}` : undefined,
        range: wordRange,
      });
    }
  });
  
  return { suggestions };
}

function createConfigKeySuggestions(
  monaco: Monaco,
  config: any,
  wordRange: any
) {
  const suggestions: any[] = [];
  
  if (config.config_json && typeof config.config_json === "object") {
    const keys = Object.keys(config.config_json);
    if (keys.length > 0) {
      keys.forEach((key) => {
        const value = config.config_json[key];
        const valueType = typeof value;
        suggestions.push({
          label: key,
          kind: monaco.languages.CompletionItemKind.Field,
          insertText: key,
          detail: `Type: ${valueType}`,
          documentation: valueType === "object" 
            ? JSON.stringify(value, null, 2).substring(0, 200)
            : String(value),
          range: wordRange,
        });
      });
    }
  }
  
  return { suggestions };
}
