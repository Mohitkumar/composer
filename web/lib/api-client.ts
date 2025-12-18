import axios from 'axios';

// Use Next.js API proxy to avoid CORS issues
// In production, you can set NEXT_PUBLIC_USE_PROXY=false to call backend directly
const USE_PROXY = process.env.NEXT_PUBLIC_USE_PROXY !== 'false';
const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080';

// Use proxy route to avoid CORS - the proxy route at /api/[...path] will forward to backend
// The path will be: /api/api/v1/workflows -> backend: http://localhost:8080/api/v1/workflows
const baseURL = USE_PROXY
  ? '/api/api/v1' // Next.js API route proxy (path: /api/[...path] where path = 'api/v1/...')
  : `${API_BASE_URL}/api/v1`; // Direct backend URL (if proxy disabled)

export const apiClient = axios.create({
  baseURL,
  headers: {
    'Content-Type': 'application/json',
  },
});

// Add auth token interceptor
apiClient.interceptors.request.use((config) => {
  if (typeof window !== 'undefined') {
    const token = localStorage.getItem('auth_token');
    if (token) {
      config.headers.Authorization = `Bearer ${token}`;
    }
  }
  return config;
});

// Add response interceptor for error handling
apiClient.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response) {
      // Server responded with error status
      console.error('[API Error]', error.response.status, error.response.data);
      return Promise.reject(
        new Error(error.response.data?.error || error.response.data?.message || `Request failed with status ${error.response.status}`)
      );
    } else if (error.request) {
      // Request was made but no response received
      console.error('[API Error] No response received', error.request);
      return Promise.reject(
        new Error('No response from server. Please check if the backend is running.')
      );
    } else {
      // Something else happened
      console.error('[API Error]', error.message);
      return Promise.reject(error);
    }
  }
);

// Types
export interface Node {
  node_id: string;
  name: string;
  type: 'TASK' | 'SWITCH' | 'PARALLEL' | 'WAIT';
  runtime: string;
  spec: Record<string, any>;
}

export interface Edge {
  from: string;
  to: string;
}

export interface WorkflowDefinitionSpec {
  nodes: Node[];
  edges: Edge[];
}

export interface WorkflowDefinition {
  id: string;
  name: string;
  version: number;
  spec: WorkflowDefinitionSpec | string; // Can be object or JSON string
  created_by?: string;
  enabled: boolean;
  webhook_url?: string;
  created_at: string;
}

export interface CreateWorkflowRequest {
  workflow_id: string;
  name: string;
  version: number;
  spec: WorkflowDefinitionSpec;
  created_by?: string;
}

export interface UpdateWorkflowRequest {
  name?: string;
  enabled?: boolean;
  webhook_url?: string;
}

export interface WorkflowInstance {
  id: string;
  workflow_definition_id: string;
  workflow_version: number;
  status: 'PENDING' | 'RUNNING' | 'SUCCEEDED' | 'COMPLETED' | 'FAILED';
  input?: Record<string, any>;
  created_at: string;
  updated_at: string;
  completed_at?: string;
}

export interface WorkflowInstanceStatus {
  id: string;
  status: 'PENDING' | 'RUNNING' | 'SUCCEEDED' | 'COMPLETED' | 'FAILED';
  pending_tasks: number;
  running_tasks: number;
  completed_tasks: number;
  failed_tasks: number;
  total_tasks: number;
  created_at: string;
  updated_at: string;
}

export interface StartWorkflowRequest {
  run_id?: string;
  workflow_id: string;
  workflow_version: number;
  input?: Record<string, any>;
  started_by?: string;
  execution_options?: Record<string, any>;
  start_node_id?: string;
  started_at_unix_ms?: number;
}

// API functions
export const workflowApi = {
  list: async (enabledOnly?: boolean): Promise<WorkflowDefinition[]> => {
    const response = await apiClient.get('/workflows', {
      params: enabledOnly ? { enabled: 'true' } : {},
    });
    return response.data;
  },

  get: async (id: string, version?: number): Promise<WorkflowDefinition> => {
    const response = await apiClient.get(`/workflows/${id}`, {
      params: version ? { version } : {},
    });
    return response.data;
  },

  create: async (data: CreateWorkflowRequest): Promise<void> => {
    await apiClient.post('/workflows', data);
  },

  update: async (
    id: string,
    data: UpdateWorkflowRequest,
    version?: number
  ): Promise<void> => {
    await apiClient.put(`/workflows/${id}`, data, {
      params: version ? { version } : {},
    });
  },

  delete: async (id: string, version?: number, hard?: boolean): Promise<void> => {
    await apiClient.delete(`/workflows/${id}`, {
      params: {
        ...(version ? { version } : {}),
        ...(hard ? { hard: 'true' } : {}),
      },
    });
  },

  execute: async (id: string, data: Omit<StartWorkflowRequest, 'workflow_id'>): Promise<{ run_id: string }> => {
    const response = await apiClient.post(`/workflows/${id}/execute`, {
      ...data,
      workflow_id: id,
    });
    return response.data;
  },

  getStatus: async (id: string, runId?: string): Promise<WorkflowInstanceStatus> => {
    const response = await apiClient.get(`/workflows/${id}/status`, {
      params: runId ? { run_id: runId } : {},
    });
    return response.data;
  },
};

export const executionApi = {
  list: async (params?: {
    workflow_id?: string;
    status?: string;
    limit?: number;
    offset?: number;
  }): Promise<WorkflowInstance[]> => {
    const response = await apiClient.get('/executions', { params });
    return response.data;
  },

  get: async (id: string): Promise<WorkflowInstance> => {
    const response = await apiClient.get(`/executions/${id}`);
    return response.data;
  },

  cancel: async (id: string, reason?: string): Promise<void> => {
    await apiClient.post(`/executions/${id}/cancel`, { reason });
  },
};

export interface NodeTemplate {
  id: string;
  name: string;
  description?: string;
  runtime: string;
  node_type: string;
  spec_json: Record<string, any>;
  category?: string;
  tags?: string[];
  created_at: string;
  updated_at: string;
}

export interface WorkflowTemplate {
  id: string;
  name: string;
  description?: string;
  spec_json: WorkflowDefinitionSpec;
  category?: string;
  tags?: string[];
  created_at: string;
  updated_at: string;
}

export const templateApi = {
  listNodeTemplates: async (params?: {
    runtime?: string;
    category?: string;
  }): Promise<NodeTemplate[]> => {
    const response = await apiClient.get('/templates/nodes', { params });
    return response.data;
  },

  getNodeTemplate: async (id: string): Promise<NodeTemplate> => {
    const response = await apiClient.get(`/templates/nodes/${id}`);
    return response.data;
  },

  listWorkflowTemplates: async (params?: {
    category?: string;
  }): Promise<WorkflowTemplate[]> => {
    const response = await apiClient.get('/templates/workflows', { params });
    return response.data;
  },

  getWorkflowTemplate: async (id: string): Promise<WorkflowTemplate> => {
    const response = await apiClient.get(`/templates/workflows/${id}`);
    return response.data;
  },
};

export interface Configuration {
  id: string;
  name: string;
  description?: string;
  type: string; // 's3', 'http', 'database', 'api_key', etc.
  config_json: Record<string, any>;
  created_by?: string;
  created_at: string;
  updated_at: string;
}

export interface CreateConfigurationRequest {
  id: string;
  name: string;
  description?: string;
  type: string;
  config_json: Record<string, any>;
  created_by?: string;
}

export interface UpdateConfigurationRequest {
  name?: string;
  description?: string;
  config_json?: Record<string, any>;
}

export const configurationApi = {
  list: async (params?: {
    type?: string;
  }): Promise<Configuration[]> => {
    const response = await apiClient.get('/configurations', { params });
    return response.data;
  },

  get: async (id: string): Promise<Configuration> => {
    const response = await apiClient.get(`/configurations/${id}`);
    return response.data;
  },

  create: async (data: CreateConfigurationRequest): Promise<void> => {
    await apiClient.post('/configurations', data);
  },

  update: async (id: string, data: UpdateConfigurationRequest): Promise<void> => {
    await apiClient.put(`/configurations/${id}`, data);
  },

  delete: async (id: string): Promise<void> => {
    await apiClient.delete(`/configurations/${id}`);
  },
};
