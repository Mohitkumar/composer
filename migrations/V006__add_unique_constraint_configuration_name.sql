-- Add unique constraint on configuration name
ALTER TABLE configurations ADD CONSTRAINT unique_configuration_name UNIQUE (name);
