-- Add soft delete support for workflows and workflow runs

-- Add deleted_at column to workflow_definitions
ALTER TABLE workflow_definitions 
ADD COLUMN IF NOT EXISTS deleted_at timestamptz;

-- Add deleted_at column to workflow_runs
ALTER TABLE workflow_runs 
ADD COLUMN IF NOT EXISTS deleted_at timestamptz;

-- Create indexes for faster queries filtering deleted records
CREATE INDEX IF NOT EXISTS idx_workflow_definitions_deleted_at ON workflow_definitions(deleted_at) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_workflow_runs_deleted_at ON workflow_runs(deleted_at) WHERE deleted_at IS NULL;
