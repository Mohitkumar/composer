-- migrations/V002__outbox_retries.sql
-- Enhances outbox schema with retry/backoff, visibility and dead-letter handling.
-- Note: Base outbox table columns are now in V001, this migration adds:
-- - Additional index for retry scheduling
-- - Dead letter queue table

-- Add index for efficient retry scheduling queries (used by outbox enqueuer)
CREATE INDEX IF NOT EXISTS idx_outbox_status_next_attempt ON outbox (status, next_attempt_at) 
WHERE status IN ('PENDING', 'RETRY') AND next_attempt_at IS NOT NULL;

-- Create dead letter table for outbox failures
CREATE TABLE IF NOT EXISTS outbox_dlq (
  id TEXT PRIMARY KEY,
  stream TEXT NOT NULL,
  payload JSONB NULL,
  last_error TEXT,
  attempts INT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  moved_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_outbox_dlq_created ON outbox_dlq(created_at DESC);
