-- Add context_json column to workflow_runs if it does not exist
ALTER TABLE workflow_runs
    ADD COLUMN IF NOT EXISTS context_json jsonb;

