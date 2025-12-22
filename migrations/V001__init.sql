-- Initial database schema migration
-- This migration includes all schema changes merged from V001-V008

-- Workflow definitions table
-- Note: id is TEXT (workflow_id), not serial, to allow custom workflow IDs
CREATE TABLE IF NOT EXISTS workflow_definitions(
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    version INT NOT NULL,
    spec_json JSONB NOT NULL,
    created_by TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    webhook_url TEXT,
    deployed BOOLEAN NOT NULL DEFAULT false,
    deployed_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (id, version)
);

CREATE INDEX IF NOT EXISTS idx_workflow_definitions_id ON workflow_definitions(id);
CREATE INDEX IF NOT EXISTS idx_workflow_definitions_enabled ON workflow_definitions(enabled);
CREATE INDEX IF NOT EXISTS idx_workflow_definitions_deployed ON workflow_definitions(id, deployed) WHERE deployed = true;
CREATE INDEX IF NOT EXISTS idx_workflow_definitions_deleted_at ON workflow_definitions(deleted_at) WHERE deleted_at IS NULL;

-- Workflow runs (instances) table
CREATE TABLE IF NOT EXISTS workflow_runs(
    id TEXT PRIMARY KEY,
    workflow_definition_id TEXT NOT NULL,
    workflow_version INT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    input_json JSONB,
    context_json JSONB,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    FOREIGN KEY (workflow_definition_id, workflow_version) REFERENCES workflow_definitions(id, version)
);

CREATE INDEX IF NOT EXISTS idx_workflow_runs_definition ON workflow_runs(workflow_definition_id, workflow_version);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_status ON workflow_runs(status);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_created ON workflow_runs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_deleted_at ON workflow_runs(deleted_at) WHERE deleted_at IS NULL;

-- Task instances table
CREATE TABLE IF NOT EXISTS task_instances(
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    node_type TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'PENDING',
    attempt INT NOT NULL DEFAULT 0,
    input_json JSONB,
    output_json JSONB,
    last_error JSONB,
    scheduled_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_owner TEXT,
    lease_expires_at TIMESTAMPTZ,
    FOREIGN KEY (run_id) REFERENCES workflow_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_task_instances_node_type ON task_instances(node_type);
CREATE INDEX IF NOT EXISTS idx_task_instances_run_id ON task_instances(run_id);
CREATE INDEX IF NOT EXISTS idx_task_instances_state ON task_instances(state);
CREATE INDEX IF NOT EXISTS idx_task_instances_node_id ON task_instances(node_id);
CREATE INDEX IF NOT EXISTS idx_task_instances_lease ON task_instances(lease_owner, lease_expires_at) WHERE lease_owner IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_task_instances_scheduled ON task_instances(scheduled_at) WHERE scheduled_at IS NOT NULL;

-- Outbox table for transactional message publishing
CREATE TABLE IF NOT EXISTS outbox(
    id TEXT PRIMARY KEY,
    stream TEXT NOT NULL,
    payload JSONB,
    type TEXT,
    status TEXT NOT NULL DEFAULT 'PENDING',
    attempt_count INT NOT NULL DEFAULT 0,
    max_retries INT NOT NULL DEFAULT 5,
    next_attempt_at TIMESTAMPTZ,
    last_error TEXT,
    backoff_seconds INT NOT NULL DEFAULT 5,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_outbox_status ON outbox(status, created_at);
CREATE INDEX IF NOT EXISTS idx_outbox_stream ON outbox(stream);
CREATE INDEX IF NOT EXISTS idx_outbox_status_next_attempt ON outbox (status, next_attempt_at) 
WHERE status IN ('PENDING', 'RETRY') AND next_attempt_at IS NOT NULL;

-- Create dead letter table for outbox failures
CREATE TABLE IF NOT EXISTS outbox_dlq (
  id TEXT PRIMARY KEY,
  stream TEXT NOT NULL,
  payload JSONB NULL,
  last_error TEXT,
  attempts INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    moved_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_outbox_dlq_created ON outbox_dlq(created_at DESC);

-- Node templates table for storing reusable node templates
CREATE TABLE IF NOT EXISTS node_templates (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    runtime TEXT NOT NULL,
    node_type TEXT NOT NULL,
    spec_json JSONB NOT NULL,
    category TEXT,
    tags TEXT[],
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_node_templates_runtime ON node_templates(runtime);
CREATE INDEX IF NOT EXISTS idx_node_templates_category ON node_templates(category);
CREATE INDEX IF NOT EXISTS idx_node_templates_tags ON node_templates USING GIN(tags);

-- Workflow templates table for storing reusable workflow templates
CREATE TABLE IF NOT EXISTS workflow_templates (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    spec_json JSONB NOT NULL,
    category TEXT,
    tags TEXT[],
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_workflow_templates_category ON workflow_templates(category);
CREATE INDEX IF NOT EXISTS idx_workflow_templates_tags ON workflow_templates USING GIN(tags);

-- Configurations table for storing reusable configuration templates
CREATE TABLE IF NOT EXISTS configurations (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    type TEXT NOT NULL, -- 's3', 'http', 'database', 'api_key', etc.
    config_json JSONB NOT NULL, -- Configuration data (credentials, endpoints, etc.)
    created_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_configurations_type ON configurations(type);
CREATE INDEX IF NOT EXISTS idx_configurations_created_by ON configurations(created_by);
