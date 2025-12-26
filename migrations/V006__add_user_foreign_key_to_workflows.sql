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

