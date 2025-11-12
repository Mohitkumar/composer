-- migrations/V003__task_instance_node_type.sql
-- Add node_type column to task_instances for worker execution

ALTER TABLE task_instances
ADD COLUMN IF NOT EXISTS node_type text;

CREATE INDEX IF NOT EXISTS idx_task_instances_node_type ON task_instances(node_type);

