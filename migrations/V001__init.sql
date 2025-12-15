-- Initial database schema migration

-- Workflow definitions table
-- Note: id is TEXT (workflow_id), not serial, to allow custom workflow IDs
create table IF NOT EXISTS workflow_definitions(
    id text not null,
    name text not null,
    version int not null,
    spec_json jsonb not null,
    created_by text not null,
    enabled boolean not null default true,
    webhook_url text,
    created_at timestamptz default now(),
    PRIMARY KEY (id, version)
);

CREATE INDEX IF NOT EXISTS idx_workflow_definitions_id ON workflow_definitions(id);
CREATE INDEX IF NOT EXISTS idx_workflow_definitions_enabled ON workflow_definitions(enabled);

-- Workflow runs (instances) table
create table IF NOT EXISTS workflow_runs(
    id text primary key,
    workflow_definition_id text not null,
    workflow_version int not null,
    status text not null default 'PENDING',
    input_json jsonb,
    context_json jsonb,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    completed_at timestamptz,
    FOREIGN KEY (workflow_definition_id, workflow_version) REFERENCES workflow_definitions(id, version)
);

CREATE INDEX IF NOT EXISTS idx_workflow_runs_definition ON workflow_runs(workflow_definition_id, workflow_version);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_status ON workflow_runs(status);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_created ON workflow_runs(created_at DESC);

-- Task instances table
create table IF NOT EXISTS task_instances(
    id text primary key,
    run_id text not null,
    node_id text not null,
    node_type text not null,
    state text not null default 'PENDING',
    attempt int not null default 0,
    input_json jsonb,
    output_json jsonb,
    last_error jsonb,
    scheduled_at timestamptz,
    started_at timestamptz,
    finished_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    lease_owner text,
    lease_expires_at timestamptz,
    FOREIGN KEY (run_id) REFERENCES workflow_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_task_instances_node_type ON task_instances(node_type);
CREATE INDEX IF NOT EXISTS idx_task_instances_run_id ON task_instances(run_id);
CREATE INDEX IF NOT EXISTS idx_task_instances_state ON task_instances(state);
CREATE INDEX IF NOT EXISTS idx_task_instances_node_id ON task_instances(node_id);
CREATE INDEX IF NOT EXISTS idx_task_instances_lease ON task_instances(lease_owner, lease_expires_at) WHERE lease_owner IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_task_instances_scheduled ON task_instances(scheduled_at) WHERE scheduled_at IS NOT NULL;

-- Outbox table for transactional message publishing
create table IF NOT EXISTS outbox(
    id text primary key,
    stream text not null,
    payload jsonb,
    type text,
    status text not null default 'PENDING',
    attempt_count int not null default 0,
    max_retries int not null default 5,
    next_attempt_at timestamptz,
    last_error text,
    backoff_seconds int not null default 5,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    sent_at timestamptz
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
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  moved_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_outbox_dlq_created ON outbox_dlq(created_at DESC);