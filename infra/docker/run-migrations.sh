#!/bin/bash
# Script to run migrations manually on an existing database
# Usage: ./run-migrations.sh
# 
# This script runs migrations using psql commands directly in the PostgreSQL container

set -e

POSTGRES_CONTAINER="composer-postgres"
POSTGRES_USER="postgres"
POSTGRES_DB="composer"

echo "Running database migrations on $POSTGRES_CONTAINER..."

# Check if container is running
if ! docker ps | grep -q "$POSTGRES_CONTAINER"; then
    echo "Error: Container $POSTGRES_CONTAINER is not running"
    exit 1
fi

# Run migrations in order using psql
echo "Running V001__init.sql..."
docker exec "$POSTGRES_CONTAINER" psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f /migrations/V001__init.sql

echo "Running V004__insert_sample_templates.sql..."
docker exec "$POSTGRES_CONTAINER" psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f /migrations/V004__insert_sample_templates.sql

echo "Migrations completed successfully!"

