-- Complete database schema migration
-- This migration includes all schema changes and initial data

-- ============================================================================
-- SECTION 1: Core Schema Tables
-- ============================================================================

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

-- ----------------------------------------------------------------------------
-- Run-scoped tables: workflow_runs, task_instances, workflow_events
--
-- All three are range-partitioned by month on the run ID. Run IDs are UUIDv7,
-- whose text form sorts by creation time, so the run ID alone routes every
-- hot-path query (they all carry run_id) to a single partition, and old months
-- can be dropped wholesale for retention. IDs use COLLATE "C" so text order is
-- byte order. Partitions are managed by composer_ensure_partitions() and
-- composer_drop_partitions() below; there is deliberately no DEFAULT partition,
-- which could block creating future partitions.
-- ----------------------------------------------------------------------------

-- Workflow runs (instances) table
CREATE TABLE IF NOT EXISTS workflow_runs(
    id TEXT COLLATE "C" NOT NULL,
    workflow_definition_id TEXT NOT NULL,
    workflow_version INT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    input_json JSONB,
    context_json JSONB, -- {input, config} only; task outputs live in task_instances
    error_json JSONB,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (id),
    FOREIGN KEY (workflow_definition_id, workflow_version) REFERENCES workflow_definitions(id, version)
) PARTITION BY RANGE (id);

CREATE INDEX IF NOT EXISTS idx_workflow_runs_definition ON workflow_runs(workflow_definition_id, workflow_version);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_status ON workflow_runs(status);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_created ON workflow_runs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_deleted_at ON workflow_runs(deleted_at) WHERE deleted_at IS NULL;

-- Task instances table
CREATE TABLE IF NOT EXISTS task_instances(
    id TEXT COLLATE "C" NOT NULL,
    run_id TEXT COLLATE "C" NOT NULL,
    node_id TEXT NOT NULL,
    node_type TEXT NOT NULL,
    queue TEXT,                -- worker queue the task is dispatched on
    state TEXT NOT NULL DEFAULT 'PENDING',
    attempt INT NOT NULL DEFAULT 0,
    input_json JSONB,
    output_json JSONB,
    last_error JSONB,
    branches TEXT[],           -- SWITCH: successors it selected
    wake_at TIMESTAMPTZ,       -- WAITING: when the timer fires (WAIT) or times out (WAIT_FOR_EVENT)
    event_name TEXT,           -- WAITING on WAIT_FOR_EVENT: the event that resolves it
    scheduled_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_owner TEXT,
    lease_expires_at TIMESTAMPTZ,
    PRIMARY KEY (run_id, id),
    FOREIGN KEY (run_id) REFERENCES workflow_runs(id) ON DELETE CASCADE
) PARTITION BY RANGE (run_id);

-- Keep task_instances indexes minimal: every state change rewrites each index,
-- and indexing the state/lease columns would prevent HOT updates.
-- (run_id, node_id) also serves every lookup by run_id.
CREATE UNIQUE INDEX IF NOT EXISTS idx_task_instances_run_node ON task_instances(run_id, node_id);
-- Partial index for counting a run's active tasks: only rows in active states,
-- which are a small fraction of the table once workflows complete.
CREATE INDEX IF NOT EXISTS idx_task_instances_run_active
    ON task_instances (run_id)
    WHERE state IN ('PENDING', 'RUNNING', 'QUEUED', 'WAITING');
-- Lease sweeper: find RUNNING tasks whose lease expired.
CREATE INDEX IF NOT EXISTS idx_task_instances_running_lease
    ON task_instances (lease_expires_at)
    WHERE state = 'RUNNING';
-- Timer: find WAITING tasks that are due.
CREATE INDEX IF NOT EXISTS idx_task_instances_wake
    ON task_instances (wake_at)
    WHERE state = 'WAITING' AND wake_at IS NOT NULL;

-- Events signalled to a run before a WAIT_FOR_EVENT node was waiting for them.
CREATE TABLE IF NOT EXISTS workflow_events(
    id TEXT COLLATE "C" NOT NULL,
    run_id TEXT COLLATE "C" NOT NULL,
    name TEXT NOT NULL,
    payload JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    consumed_at TIMESTAMPTZ,
    PRIMARY KEY (run_id, id),
    FOREIGN KEY (run_id) REFERENCES workflow_runs(id) ON DELETE CASCADE
) PARTITION BY RANGE (run_id);

CREATE INDEX IF NOT EXISTS idx_workflow_events_pending
    ON workflow_events (run_id, name, created_at)
    WHERE consumed_at IS NULL;

-- composer_uuid7_bound returns the smallest UUIDv7 text with a timestamp >= ts:
-- the partition boundary for runs created from ts onwards.
CREATE OR REPLACE FUNCTION composer_uuid7_bound(ts TIMESTAMPTZ) RETURNS TEXT
LANGUAGE sql IMMUTABLE AS $$
    SELECT substr(h, 1, 8) || '-' || substr(h, 9, 4) || '-0000-0000-000000000000'
      FROM (SELECT lpad(to_hex(floor(extract(epoch FROM ts) * 1000)::BIGINT), 12, '0') AS h) b
$$;

-- composer_ensure_partitions creates the monthly partitions of every run-scoped
-- table from the current month to months_ahead months ahead. Idempotent and
-- safe to call concurrently.
CREATE OR REPLACE FUNCTION composer_ensure_partitions(months_ahead INT DEFAULT 3) RETURNS INT
LANGUAGE plpgsql AS $$
DECLARE
    month_start TIMESTAMPTZ;
    tbl TEXT;
    created INT := 0;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('composer_partitions'));
    FOR i IN 0..months_ahead LOOP
        month_start := date_trunc('month', NOW() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
                       + make_interval(months => i);
        FOREACH tbl IN ARRAY ARRAY['workflow_runs', 'task_instances', 'workflow_events'] LOOP
            IF to_regclass(format('%I_p%s', tbl, to_char(month_start AT TIME ZONE 'UTC', 'YYYYMM'))) IS NULL THEN
                EXECUTE format('CREATE TABLE %I PARTITION OF %I FOR VALUES FROM (%L) TO (%L)',
                    tbl || '_p' || to_char(month_start AT TIME ZONE 'UTC', 'YYYYMM'), tbl,
                    composer_uuid7_bound(month_start),
                    composer_uuid7_bound(month_start + INTERVAL '1 month'));
                created := created + 1;
            END IF;
        END LOOP;
    END LOOP;
    RETURN created;
END;
$$;

-- composer_drop_partitions drops every month that ended before older_than.
-- A month that still has non-terminal runs is kept. Returns the months dropped.
CREATE OR REPLACE FUNCTION composer_drop_partitions(older_than TIMESTAMPTZ) RETURNS SETOF TEXT
LANGUAGE plpgsql AS $$
DECLARE
    part RECORD;
    suffix TEXT;
    active BOOLEAN;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('composer_partitions'));
    FOR part IN
        SELECT c.relname
          FROM pg_inherits i
          JOIN pg_class c ON c.oid = i.inhrelid
         WHERE i.inhparent = 'workflow_runs'::regclass
         ORDER BY c.relname
    LOOP
        suffix := substring(part.relname FROM '_p(\d{6})$');
        CONTINUE WHEN suffix IS NULL
            OR to_timestamp(suffix, 'YYYYMM') AT TIME ZONE 'UTC' + INTERVAL '1 month' > older_than;

        EXECUTE format('SELECT EXISTS (SELECT 1 FROM %I WHERE status IN (''RUNNING'', ''PENDING''))',
                       part.relname) INTO active;
        IF active THEN
            RAISE NOTICE 'keeping partition %: it still has active runs', part.relname;
            CONTINUE;
        END IF;

        -- Referencing tables first, so their foreign keys never see orphans.
        EXECUTE format('DROP TABLE IF EXISTS %I', 'workflow_events_p' || suffix);
        EXECUTE format('DROP TABLE IF EXISTS %I', 'task_instances_p' || suffix);
        EXECUTE format('ALTER TABLE workflow_runs DETACH PARTITION %I', part.relname);
        EXECUTE format('DROP TABLE %I', part.relname);
        RETURN NEXT suffix;
    END LOOP;
END;
$$;

SELECT composer_ensure_partitions(3);

-- Outbox table for transactional message publishing
CREATE TABLE IF NOT EXISTS outbox(
    id TEXT PRIMARY KEY,
    task_id TEXT, -- task this row dispatches, so recovery can find tasks without a pending dispatch
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
-- Partial index matching the outbox poll query's filter and ORDER BY created_at.
CREATE INDEX IF NOT EXISTS idx_outbox_pending_next
    ON outbox (created_at)
    WHERE status IN ('PENDING', 'RETRY');
CREATE INDEX IF NOT EXISTS idx_outbox_task_id ON outbox (task_id) WHERE task_id IS NOT NULL;

-- Wake outbox enqueuers (LISTEN outbox_new) as soon as a transaction that
-- inserted outbox rows commits. Statement-level, and Postgres folds duplicate
-- notifications within a transaction, so a batch insert sends one.
CREATE OR REPLACE FUNCTION composer_notify_outbox() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('outbox_new', '');
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS outbox_notify ON outbox;
CREATE TRIGGER outbox_notify AFTER INSERT ON outbox
    FOR EACH STATEMENT EXECUTE FUNCTION composer_notify_outbox();

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
    type TEXT NOT NULL, -- 'http', 'database', 'api_key', etc.
    config_json JSONB NOT NULL, -- Configuration data (credentials, endpoints, etc.)
    created_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_configurations_type ON configurations(type);
CREATE INDEX IF NOT EXISTS idx_configurations_created_by ON configurations(created_by);

-- ============================================================================
-- SECTION 2: Users and Authentication
-- ============================================================================

-- Users table for authentication
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    email TEXT,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email) WHERE email IS NOT NULL;

-- Insert a default admin user (password: admin123)
-- Password hash is bcrypt hash of "admin123" with cost 10
-- You should change this password after first login
INSERT INTO users (id, username, email, password_hash) 
VALUES (
    'admin',
    'admin',
    'admin@example.com',
    '$2a$10$PjMH8x54x3Ersl57dyiAQ.2n6KpsXtv6sp3GR6U73C2d/.5xwoytu'
) ON CONFLICT (username) DO NOTHING;

-- ============================================================================
-- SECTION 3: Foreign Key Constraints
-- ============================================================================

-- Add foreign key relationship between workflow_definitions and users
-- First, update any existing workflows with invalid created_by values to use the admin user
UPDATE workflow_definitions 
SET created_by = 'admin' 
WHERE created_by IS NULL 
   OR created_by NOT IN (SELECT id FROM users);

-- Add foreign key constraint
ALTER TABLE workflow_definitions
ADD CONSTRAINT fk_workflow_definitions_created_by 
FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE RESTRICT;

-- Add index for better query performance
CREATE INDEX IF NOT EXISTS idx_workflow_definitions_created_by ON workflow_definitions(created_by);

-- Add foreign key relationship between configurations and users
-- First, update any existing configurations with invalid created_by values to use the admin user
UPDATE configurations 
SET created_by = 'admin' 
WHERE created_by IS NULL 
   OR created_by NOT IN (SELECT id FROM users);

-- Add foreign key constraint
ALTER TABLE configurations
ADD CONSTRAINT fk_configurations_created_by 
FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE RESTRICT;

-- Add index for better query performance
CREATE INDEX IF NOT EXISTS idx_configurations_created_by ON configurations(created_by);

-- ============================================================================
-- SECTION 4: Sample Templates and Initial Data
-- ============================================================================

-- Insert sample node templates

-- HTTP Node Templates
INSERT INTO node_templates (id, name, description, runtime, node_type, spec_json, category, tags) VALUES
('http-get', 'HTTP GET Request', 'Basic HTTP GET request to fetch data from an API endpoint', 'http', 'TASK', 
 '{"url": "https://api.example.com/data", "method": "GET", "headers": {}, "timeout_seconds": 30}'::jsonb,
 'http', ARRAY['http', 'api', 'get', 'fetch']),

('http-post', 'HTTP POST Request', 'HTTP POST request to send data to an API endpoint', 'http', 'TASK',
 '{"url": "https://api.example.com/data", "method": "POST", "headers": {"Content-Type": "application/json"}, "body": "{}", "timeout_seconds": 30}'::jsonb,
 'http', ARRAY['http', 'api', 'post', 'send']),

('http-put', 'HTTP PUT Request', 'HTTP PUT request to update data via API', 'http', 'TASK',
 '{"url": "https://api.example.com/data/{{input.id}}", "method": "PUT", "headers": {"Content-Type": "application/json"}, "body": "{{input.data}}", "timeout_seconds": 30}'::jsonb,
 'http', ARRAY['http', 'api', 'put', 'update']),

-- JavaScript Node Templates
('js-process', 'JavaScript Processor', 'Process input data using JavaScript', 'javascript', 'TASK',
 '{"code": "function handler(input) {\n  // Process the input data\n  const result = {\n    processed: true,\n    data: input,\n    timestamp: new Date().toISOString()\n  };\n  return result;\n}", "timeout": 60}'::jsonb,
 'code', ARRAY['javascript', 'js', 'process', 'transform']),

('js-transform', 'JavaScript Data Transform', 'Transform and reshape data using JavaScript', 'javascript', 'TASK',
 '{"code": "function handler(input) {\n  // Transform input data\n  if (Array.isArray(input)) {\n    return input.map(item => ({\n      ...item,\n      transformed: true\n    }));\n  }\n  return { ...input, transformed: true };\n}", "timeout": 60}'::jsonb,
 'code', ARRAY['javascript', 'js', 'transform', 'map']),

-- Python Node Templates
('py-process', 'Python Processor', 'Process input data using Python', 'python', 'TASK',
 '{"code": "def handler(input):\n    # Process the input data\n    result = {\n        \"processed\": True,\n        \"data\": input,\n        \"timestamp\": __import__(\"datetime\").datetime.now().isoformat()\n    }\n    return result", "timeout": 60}'::jsonb,
 'code', ARRAY['python', 'py', 'process', 'transform']),

('py-calculate', 'Python Calculator', 'Perform calculations using Python', 'python', 'TASK',
 '{"code": "def handler(input):\n    # Perform calculations\n    if isinstance(input, dict) and \"a\" in input and \"b\" in input:\n        return {\n            \"sum\": input[\"a\"] + input[\"b\"],\n            \"product\": input[\"a\"] * input[\"b\"],\n            \"difference\": input[\"a\"] - input[\"b\"]\n        }\n    return {\"error\": \"Invalid input\"}", "timeout": 60}'::jsonb,
 'code', ARRAY['python', 'py', 'calculate', 'math']),

-- SQL Node Templates
('sql-select', 'SQL SELECT Query', 'Execute a SELECT query to fetch data from database', 'sql', 'TASK',
 '{"query": "SELECT * FROM users WHERE status = ? LIMIT 100", "parameters": ["active"], "connection_string": "postgresql://user:pass@localhost:5432/dbname"}'::jsonb,
 'database', ARRAY['sql', 'database', 'select', 'query']),

('sql-insert', 'SQL INSERT Query', 'Insert data into database table', 'sql', 'TASK',
 '{"query": "INSERT INTO users (name, email, status) VALUES (?, ?, ?) RETURNING id", "parameters": ["{{input.name}}", "{{input.email}}", "active"], "connection_string": "postgresql://user:pass@localhost:5432/dbname"}'::jsonb,
 'database', ARRAY['sql', 'database', 'insert', 'create'])

ON CONFLICT (id) DO NOTHING;

-- Insert sample workflow templates

INSERT INTO workflow_templates (id, name, description, spec_json, category, tags) VALUES
('simple-api-pipeline', 'Simple API Pipeline', 'A basic workflow that fetches data from an API and processes it',
 '{
   "nodes": [
     {
       "node_id": "fetch_data",
       "name": "Fetch Data",
       "type": "TASK",
       "runtime": "http",
       "spec": {
         "url": "https://api.example.com/data",
         "method": "GET",
         "timeout_seconds": 30
       }
     },
     {
       "node_id": "process_data",
       "name": "Process Data",
       "type": "TASK",
       "runtime": "javascript",
       "spec": {
         "code": "function handler(input) {\n  const data = input || {};\n  return {\n    processed: true,\n    result: data,\n    timestamp: new Date().toISOString()\n  };\n}",
         "timeout": 60
       }
     }
   ],
   "edges": [
     {
       "from": "fetch_data",
       "to": "process_data"
     }
   ]
 }'::jsonb,
 'pipeline', ARRAY['api', 'pipeline', 'simple', 'http', 'javascript']),

('data-transform-pipeline', 'Data Transform Pipeline', 'Fetch data, transform it, and store results',
 '{
   "nodes": [
     {
       "node_id": "fetch",
       "name": "Fetch Data",
       "type": "TASK",
       "runtime": "http",
       "spec": {
         "url": "https://api.example.com/source",
         "method": "GET",
         "timeout_seconds": 30
       }
     },
     {
       "node_id": "transform",
       "name": "Transform",
       "type": "TASK",
       "runtime": "javascript",
       "spec": {
         "code": "function handler(input) {\n  if (Array.isArray(input)) {\n    return input.map(item => ({\n      ...item,\n      transformed: true,\n      processed_at: new Date().toISOString()\n    }));\n  }\n  return { ...input, transformed: true };\n}",
         "timeout": 60
       }
     },
     {
       "node_id": "store",
       "name": "Store Result",
       "type": "TASK",
       "runtime": "http",
       "spec": {
         "url": "https://api.example.com/results",
         "method": "POST",
         "headers": {"Content-Type": "application/json"},
         "body": "{{tasks.transform.output}}",
         "timeout_seconds": 30
       }
     }
   ],
   "edges": [
     {
       "from": "fetch",
       "to": "transform"
     },
     {
       "from": "transform",
       "to": "store"
     }
   ]
 }'::jsonb,
 'pipeline', ARRAY['transform', 'pipeline', 'etl', 'http', 'javascript']),

('parallel-processing', 'Parallel Processing', 'Process multiple data sources in parallel',
 '{
   "nodes": [
     {
       "node_id": "fetch_source1",
       "name": "Fetch Source 1",
       "type": "TASK",
       "runtime": "http",
       "spec": {
         "url": "https://api.example.com/source1",
         "method": "GET",
         "timeout_seconds": 30
       }
     },
     {
       "node_id": "fetch_source2",
       "name": "Fetch Source 2",
       "type": "TASK",
       "runtime": "http",
       "spec": {
         "url": "https://api.example.com/source2",
         "method": "GET",
         "timeout_seconds": 30
       }
     },
     {
       "node_id": "merge",
       "name": "Merge Results",
       "type": "TASK",
       "runtime": "javascript",
       "spec": {
         "code": "function handler(input) {\n  return {\n    source1: input.source1 || null,\n    source2: input.source2 || null,\n    merged_at: new Date().toISOString()\n  };\n}",
         "timeout": 60
       }
     }
   ],
   "edges": [
     {
       "from": "fetch_source1",
       "to": "merge"
     },
     {
       "from": "fetch_source2",
       "to": "merge"
     }
   ]
 }'::jsonb,
 'parallel', ARRAY['parallel', 'merge', 'http', 'javascript'])

ON CONFLICT (id) DO NOTHING;
