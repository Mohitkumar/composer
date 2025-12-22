#!/bin/bash
# This script runs on first database initialization only
# For existing databases, use run-migrations.sh instead
set -e

echo "Running initial database migrations..."

# Run migrations in order using psql -f
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -f /migrations/V001__init.sql
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -f /migrations/V004__insert_sample_templates.sql

echo "Initial migrations completed successfully!"

