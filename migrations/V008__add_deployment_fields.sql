-- Add deployment fields to workflow_definitions

-- Add deployed flag and deployed_at timestamp
ALTER TABLE workflow_definitions 
ADD COLUMN IF NOT EXISTS deployed boolean NOT NULL DEFAULT false;

ALTER TABLE workflow_definitions 
ADD COLUMN IF NOT EXISTS deployed_at timestamptz;

-- Create index for faster queries filtering deployed workflows
CREATE INDEX IF NOT EXISTS idx_workflow_definitions_deployed ON workflow_definitions(id, deployed) WHERE deployed = true;
