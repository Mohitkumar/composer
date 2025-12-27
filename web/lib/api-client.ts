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
      
      // Handle 401 Unauthorized - redirect to login
      if (error.response.status === 401 && typeof window !== 'undefined') {
        // Clear auth token
        localStorage.removeItem('auth_token');
        localStorage.removeItem('auth_user');
        
        // Redirect to login if not already there
        if (window.location.pathname !== '/login') {
          window.location.href = '/login';
        }
      }
      
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
  type: 'CODE' | 'HTTP' | 'SWITCH' | 'FORK' | 'JOIN' | 'WAIT';
  runtime?: string; // Optional, only for CODE type nodes
  spec: Record<string, any>;
}

export interface Edge {
  from: string;
  to: string;
  condition?: string; // CEL expression for conditional edges (used in SWITCH nodes)
}

export interface WorkflowDefinitionSpec {
  nodes: Node[];
  edges: Edge[];
}

export interface WorkflowDefinition {
  id: string;
  name: string;
  version: number;
  spec: string; // YAML string
  created_by?: string;
  enabled: boolean;
  webhook_url?: string;
  deployed: boolean;
  deployed_at?: string;
  created_at: string;
  success_rate?: number; // Success rate percentage (0-100)
}

export interface CreateWorkflowRequest {
  workflow_id: string;
  name: string;
  version: number;
  spec: string; // YAML string
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

// Paginated workflow list response
export interface WorkflowListResponse {
  workflows: WorkflowDefinition[];
  total: number;
  page: number;
  page_size: number;
  total_pages: number;
}

export interface WorkflowIssueSummary {
  summary: string;
  errors: string[];
}

// API functions
export const workflowApi = {
  list: async (
    enabledOnly?: boolean,
    search?: string,
    page?: number,
    pageSize?: number
  ): Promise<WorkflowListResponse> => {
    const params: Record<string, string> = {};
    if (enabledOnly) params.enabled = 'true';
    if (search) params.search = search;
    if (page !== undefined) params.page = page.toString();
    if (pageSize !== undefined) params.page_size = pageSize.toString();
    
    const response = await apiClient.get('/workflows', { params });
    return response.data;
  },

  get: async (id: string, version?: number, latest?: boolean): Promise<WorkflowDefinition> => {
    const params: Record<string, string> = {};
    if (version !== undefined && version > 0) {
      params.version = version.toString();
    }
    if (latest) {
      params.latest = 'true';
    }
    const response = await apiClient.get(`/workflows/${id}`, { params });
    return response.data;
  },

  create: async (data: CreateWorkflowRequest): Promise<void> => {
    // Send as JSON with spec as YAML string
    // The data.spec is already a YAML string, send it as-is in JSON body
    // Note: created_by is automatically set from JWT token on the backend
    await apiClient.post('/workflows', {
      workflow_id: data.workflow_id,
      name: data.name,
      version: data.version,
      spec: data.spec, // YAML string
    });
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
        ...(version !== undefined ? { version } : {}),
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

  deploy: async (id: string, version: number): Promise<void> => {
    await apiClient.post(`/workflows/${id}/deploy`, null, {
      params: { version: version.toString() },
    });
  },

  rollback: async (id: string, version: number): Promise<void> => {
    await apiClient.post(`/workflows/${id}/rollback`, null, {
      params: { version: version.toString() },
    });
  },

  getPreviousDeployedVersions: async (id: string, currentVersion: number): Promise<number[]> => {
    const response = await apiClient.get(`/workflows/${id}/previous-deployed-versions`, {
      params: { version: currentVersion.toString() },
    });
    return response.data.versions || [];
  },

  getStatus: async (id: string, runId?: string): Promise<WorkflowInstanceStatus> => {
    const response = await apiClient.get(`/workflows/${id}/status`, {
      params: runId ? { run_id: runId } : {},
    });
    return response.data;
  },

  getIssueSummary: async (id: string): Promise<WorkflowIssueSummary> => {
    const response = await apiClient.get(`/workflows/${id}/issue-summary`);
    return response.data;
  },
};

export interface WorkflowIssueSummary {
  summary: string;
  errors: string[];
}

export interface TaskInstance {
  id: string;
  run_id: string;
  node_id: string;
  node_type: string;
  state: string;
  attempt: number;
  input?: Record<string, any>;
  output?: Record<string, any>;
  last_error?: Record<string, any> | string;
  scheduled_at?: string;
  started_at?: string;
  finished_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
  lease_owner?: string;
  lease_expires_at?: string;
}

export interface RunContext {
  input?: Record<string, any>;
  config?: Record<string, any>;
  tasks?: Record<string, {
    status: string;
    output?: any;
  }>;
}

export interface ExecutionDetail {
  execution: WorkflowInstance;
  context?: RunContext;
  task_instances: TaskInstance[];
}

// Paginated execution list response
export interface ExecutionListResponse {
  executions: WorkflowInstance[];
  total: number;
  page: number;
  page_size: number;
  total_pages: number;
}

export const executionApi = {
  list: async (params?: {
    workflow_id?: string;
    status?: string;
    search?: string;
    page?: number;
    page_size?: number;
  }): Promise<ExecutionListResponse> => {
    const response = await apiClient.get('/executions', { params });
    return response.data;
  },

  get: async (id: string): Promise<WorkflowInstance> => {
    const response = await apiClient.get(`/executions/${id}`);
    return response.data;
  },

  getDetails: async (id: string): Promise<ExecutionDetail> => {
    const response = await apiClient.get(`/executions/${id}`, {
      params: { detailed: 'true' },
    });
    return response.data;
  },

  cancel: async (id: string, reason?: string): Promise<void> => {
    await apiClient.post(`/executions/${id}/cancel`, { reason });
  },

  retry: async (id: string, data: {
    node_id: string;
    input?: Record<string, any>;
    execution_options?: Record<string, any>;
    workflow_version?: number;
  }): Promise<{ message: string; run_id: string }> => {
    const response = await apiClient.post(`/executions/${id}/retry`, data);
    return response.data;
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
    // Note: created_by is automatically set from JWT token on the backend
    // Remove it from the request if present
    const { created_by, ...requestData } = data;
    await apiClient.post('/configurations', requestData);
  },

  update: async (id: string, data: UpdateConfigurationRequest): Promise<void> => {
    await apiClient.put(`/configurations/${id}`, data);
  },

  delete: async (id: string): Promise<void> => {
    await apiClient.delete(`/configurations/${id}`);
  },
};
