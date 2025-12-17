// Form field types
export type FieldType = 'text' | 'textarea' | 'number' | 'select' | 'json' | 'keyvalue';

export interface FormField {
  key: string;
  label: string;
  type: FieldType;
  required?: boolean;
  placeholder?: string;
  defaultValue?: any;
  options?: { label: string; value: string }[];
  description?: string;
  rows?: number;
  supportsCEL?: boolean; // Whether this field supports CEL expressions
  isCode?: boolean; // Whether this field should use code editor
  codeLanguage?: 'javascript' | 'python'; // Language for code editor
}

export interface NodeFormSchema {
  runtime: string;
  fields: FormField[];
}

// Define form schemas for different node runtimes
export const nodeFormSchemas: Record<string, NodeFormSchema> = {
  http: {
    runtime: 'http',
    fields: [
      {
        key: 'url',
        label: 'URL',
        type: 'text',
        required: true,
        placeholder: 'https://api.example.com/endpoint',
        description: 'The HTTP endpoint URL. Supports template variables like {{input.key}}',
        supportsCEL: true,
      },
      {
        key: 'method',
        label: 'HTTP Method',
        type: 'select',
        required: true,
        defaultValue: 'GET',
        options: [
          { label: 'GET', value: 'GET' },
          { label: 'POST', value: 'POST' },
          { label: 'PUT', value: 'PUT' },
          { label: 'PATCH', value: 'PATCH' },
          { label: 'DELETE', value: 'DELETE' },
        ],
      },
      {
        key: 'headers',
        label: 'Headers',
        type: 'keyvalue',
        description: 'HTTP headers as key-value pairs',
      },
      {
        key: 'body',
        label: 'Request Body',
        type: 'textarea',
        placeholder: 'Request body (JSON string or template)',
        rows: 6,
        description: 'Request body. Can use template variables like {{tasks.node_id.output}}',
        supportsCEL: true,
      },
      {
        key: 'timeout_seconds',
        label: 'Timeout (seconds)',
        type: 'number',
        defaultValue: 30,
        placeholder: '30',
        description: 'Request timeout in seconds',
      },
    ],
  },
  javascript: {
    runtime: 'javascript',
    fields: [
      {
        key: 'code',
        label: 'JavaScript Code',
        type: 'textarea',
        required: true,
        placeholder: 'function handler(input) {\n  // Your code here\n  return input;\n}',
        rows: 20,
        description: 'JavaScript function that processes the input. Must export a handler function.',
        isCode: true,
        codeLanguage: 'javascript',
      },
      {
        key: 'input',
        label: 'Input (optional)',
        type: 'textarea',
        placeholder: '{{input}} or JSON string',
        rows: 4,
        description: 'Input data for the function. Can use template variables.',
        supportsCEL: true,
      },
      {
        key: 'filename',
        label: 'Filename',
        type: 'text',
        placeholder: 'handler.js',
        description: 'Optional filename for the script',
      },
      {
        key: 'timeout',
        label: 'Timeout (seconds)',
        type: 'number',
        defaultValue: 60,
        placeholder: '60',
        description: 'Execution timeout in seconds',
      },
    ],
  },
  python: {
    runtime: 'python',
    fields: [
      {
        key: 'code',
        label: 'Python Code',
        type: 'textarea',
        required: true,
        placeholder: 'def handler(input):\n    # Your code here\n    return input',
        rows: 20,
        description: 'Python function that processes the input. Must define a handler function.',
        isCode: true,
        codeLanguage: 'python',
      },
      {
        key: 'input',
        label: 'Input (optional)',
        type: 'textarea',
        placeholder: '{{input}} or JSON string',
        rows: 4,
        description: 'Input data for the function. Can use template variables.',
        supportsCEL: true,
      },
      {
        key: 'filename',
        label: 'Filename',
        type: 'text',
        placeholder: 'handler.py',
        description: 'Optional filename for the script',
      },
      {
        key: 'timeout',
        label: 'Timeout (seconds)',
        type: 'number',
        defaultValue: 60,
        placeholder: '60',
        description: 'Execution timeout in seconds',
      },
    ],
  },
  s3: {
    runtime: 's3',
    fields: [
      {
        key: 'operation',
        label: 'Operation',
        type: 'select',
        required: true,
        options: [
          { label: 'Upload', value: 'upload' },
          { label: 'Download', value: 'download' },
          { label: 'List', value: 'list' },
          { label: 'Delete', value: 'delete' },
        ],
      },
      {
        key: 'bucket',
        label: 'Bucket Name',
        type: 'text',
        required: true,
        placeholder: 'my-bucket',
      },
      {
        key: 'key',
        label: 'Object Key/Path',
        type: 'text',
        required: true,
        placeholder: 'path/to/file.txt',
        description: 'S3 object key (file path)',
      },
      {
        key: 'region',
        label: 'AWS Region',
        type: 'text',
        placeholder: 'us-east-1',
        defaultValue: 'us-east-1',
      },
      {
        key: 'data',
        label: 'Data (for upload)',
        type: 'textarea',
        rows: 6,
        placeholder: 'Data to upload (JSON string or template)',
        description: 'Required for upload operation. Can use template variables.',
        supportsCEL: true,
      },
    ],
  },
  csv: {
    runtime: 'csv',
    fields: [
      {
        key: 'operation',
        label: 'Operation',
        type: 'select',
        required: true,
        options: [
          { label: 'Parse/To JSON', value: 'parse' },
          { label: 'Filter', value: 'filter' },
        ],
      },
      {
        key: 'data',
        label: 'CSV Data',
        type: 'textarea',
        required: true,
        rows: 8,
        placeholder: 'col1,col2,col3\nval1,val2,val3',
        description: 'CSV data as string. Can use template variables.',
        supportsCEL: true,
      },
      {
        key: 'has_header',
        label: 'Has Header Row',
        type: 'select',
        defaultValue: 'true',
        options: [
          { label: 'Yes', value: 'true' },
          { label: 'No', value: 'false' },
        ],
      },
      {
        key: 'delimiter',
        label: 'Delimiter',
        type: 'text',
        placeholder: ',',
        defaultValue: ',',
        description: 'CSV delimiter character (default: comma)',
      },
      {
        key: 'filter_col',
        label: 'Filter Column (for filter operation)',
        type: 'text',
        placeholder: 'column_name or col0',
        description: 'Column name or index to filter on',
      },
      {
        key: 'filter_val',
        label: 'Filter Value (for filter operation)',
        type: 'text',
        placeholder: 'value to match',
        description: 'Value to match in the filter column',
      },
    ],
  },
  sql: {
    runtime: 'sql',
    fields: [
      {
        key: 'query',
        label: 'SQL Query',
        type: 'textarea',
        required: true,
        rows: 15,
        placeholder: 'SELECT * FROM table WHERE id = ?',
        description: 'SQL query. Use ? for parameters.',
      },
      {
        key: 'parameters',
        label: 'Query Parameters',
        type: 'json',
        rows: 4,
        placeholder: '["value1", "value2"]',
        description: 'Array of parameters for the query',
      },
      {
        key: 'connection_string',
        label: 'Database Connection',
        type: 'text',
        placeholder: 'postgresql://user:pass@host:5432/db',
        description: 'Database connection string',
      },
    ],
  },
  go: {
    runtime: 'go',
    fields: [
      {
        key: 'code',
        label: 'Go Code',
        type: 'textarea',
        required: true,
        rows: 20,
        placeholder: 'package main\n\nfunc Handler(input interface{}) interface{} {\n    // Your code\n    return input\n}',
        description: 'Go code. Must define a Handler function.',
        isCode: true,
        codeLanguage: 'javascript', // Use JS syntax highlighting for Go (closest match)
      },
      {
        key: 'input',
        label: 'Input (optional)',
        type: 'textarea',
        rows: 4,
        placeholder: '{{input}} or JSON string',
      },
      {
        key: 'timeout',
        label: 'Timeout (seconds)',
        type: 'number',
        defaultValue: 60,
      },
    ],
  },
};

// Get schema for a runtime, or return a generic JSON editor
export function getNodeFormSchema(runtime: string): NodeFormSchema {
  return nodeFormSchemas[runtime.toLowerCase()] || {
    runtime,
    fields: [
      {
        key: 'spec',
        label: 'Configuration (JSON)',
        type: 'json',
        required: true,
        rows: 12,
        description: 'Node configuration as JSON',
      },
    ],
  };
}
