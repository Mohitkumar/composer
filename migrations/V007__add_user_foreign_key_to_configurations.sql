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

