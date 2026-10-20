package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mohitkumar/composer/pkg/util"
	"github.com/rs/zerolog"
)

// OutboxEnqueuer processes outbox rows and pushes them to Redis streams.
type OutboxEnqueuer struct {
	cfg  EnqueuerConfig
	wg   sync.WaitGroup
	quit chan struct{}
}

// outboxRow represents a row from the outbox table
type outboxRow struct {
	ID             string
	Stream         string
	Payload        []byte
	Attempt        int
	MaxRetries     int
	NextAttempt    sql.NullTime
	BackoffSeconds int
}

// NewOutboxEnqueuer returns an enqueuer instance.
func NewOutboxEnqueuer(cfg EnqueuerConfig) *OutboxEnqueuer {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 100 * time.Millisecond
	}
	if cfg.Logger.GetLevel() == zerolog.NoLevel {
		cfg.Logger = zerolog.New(nil).Level(zerolog.InfoLevel)
	}
	return &OutboxEnqueuer{
		cfg:  cfg,
		quit: make(chan struct{}),
	}
}

// Run starts the enqueuer loop and returns when ctx is done (or Stop called).
func (o *OutboxEnqueuer) Run(ctx context.Context) {
	o.cfg.Logger.Info().Int("batch", o.cfg.BatchSize).Msg("starting outbox enqueuer")

	// Start worker goroutines
	workerChan := make(chan struct{}, o.cfg.Concurrency)
	for i := 0; i < o.cfg.Concurrency; i++ {
		workerChan <- struct{}{}
	}

	// batchFull receives a signal when a batch was at full capacity, meaning
	// more rows are likely waiting. The main loop skips its sleep in that case
	// so we drain the queue without introducing artificial delay.
	batchFull := make(chan struct{}, 1)

	// Start periodic cleanup and recovery in background
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		o.maintenanceLoop(ctx)
	}()

	for {
		select {
		case <-ctx.Done():
			o.cfg.Logger.Info().Msg("context canceled, stopping enqueuer")
			o.shutdown()
			return
		case <-o.quit:
			o.cfg.Logger.Info().Msg("quit signal received, stopping enqueuer")
			o.shutdown()
			return
		default:
		}

		// Acquire a worker slot
		select {
		case <-workerChan:
			// process one batch in goroutine
			o.wg.Add(1)
			go func() {
				defer func() { workerChan <- struct{}{} }()
				defer o.wg.Done()
				count, err := o.processBatch(ctx)
				if err != nil {
					o.cfg.Logger.Error().Err(err).Msg("processBatch failed")
				}
				if count >= o.cfg.BatchSize {
					select {
					case batchFull <- struct{}{}:
					default:
					}
				}
			}()
		case <-ctx.Done():
			o.shutdown()
			return
		}

		// Throttle: only sleep when the queue appears empty. If the last batch
		// was at full capacity there are likely more rows ready, so poll again
		// immediately instead of waiting for the full PollInterval.
		select {
		case <-batchFull:
			// more work pending — re-poll immediately
		case <-time.After(o.cfg.PollInterval):
		case <-ctx.Done():
			o.shutdown()
			return
		case <-o.quit:
			o.shutdown()
			return
		}
	}
}

func (o *OutboxEnqueuer) shutdown() {
	// wait for inflight goroutines
	o.cfg.Logger.Info().Msg("waiting for in-flight batches to finish")
	o.wg.Wait()
	o.cfg.Logger.Info().Msg("outbox enqueuer stopped")
}

// Stop stops the enqueuer loop.
func (o *OutboxEnqueuer) Stop() {
	close(o.quit)
}

// maintenanceLoop periodically cleans up SENT rows and recovers stale IN_FLIGHT rows.
func (o *OutboxEnqueuer) maintenanceLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-o.quit:
			return
		case <-ticker.C:
			o.cleanupSentRows(ctx)
			o.recoverStaleInFlight(ctx)
		}
	}
}

// cleanupSentRows deletes outbox rows that have been successfully sent.
func (o *OutboxEnqueuer) cleanupSentRows(ctx context.Context) {
	result, err := o.cfg.DB.Exec(ctx,
		`DELETE FROM outbox WHERE status = 'SENT' AND sent_at < NOW() - INTERVAL '5 minutes'`)
	if err != nil {
		o.cfg.Logger.Error().Err(err).Msg("failed to cleanup sent outbox rows")
		return
	}
	if result.RowsAffected() > 0 {
		o.cfg.Logger.Debug().Int64("deleted", result.RowsAffected()).Msg("cleaned up sent outbox rows")
	}
}

// recoverStaleInFlight resets IN_FLIGHT rows that have been stuck for too long
// (e.g., the enqueuer crashed after marking IN_FLIGHT but before completing processing).
func (o *OutboxEnqueuer) recoverStaleInFlight(ctx context.Context) {
	result, err := o.cfg.DB.Exec(ctx,
		`UPDATE outbox SET status = 'RETRY', updated_at = NOW(), next_attempt_at = NOW()
		 WHERE status = 'IN_FLIGHT' AND updated_at < NOW() - INTERVAL '2 minutes'`)
	if err != nil {
		o.cfg.Logger.Error().Err(err).Msg("failed to recover stale IN_FLIGHT outbox rows")
		return
	}
	if result.RowsAffected() > 0 {
		o.cfg.Logger.Warn().Int64("recovered", result.RowsAffected()).Msg("recovered stale IN_FLIGHT outbox rows")
	}
}

// processBatch claims a batch of due outbox rows (marking them IN_FLIGHT so
// other coordinators skip them), publishes them and records each outcome.
// Returns the number of rows claimed and any error.
func (o *OutboxEnqueuer) processBatch(ctx context.Context) (int, error) {
	rows, err := o.cfg.DB.Query(ctx, `
		UPDATE outbox SET status = 'IN_FLIGHT', updated_at = NOW()
		 WHERE id IN (
		       SELECT id FROM outbox
		        WHERE status IN ('PENDING', 'RETRY')
		          AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
		        ORDER BY created_at
		        LIMIT $1
		          FOR UPDATE SKIP LOCKED)
		RETURNING id, stream, payload, attempt_count, max_retries, next_attempt_at, backoff_seconds`,
		o.cfg.BatchSize)
	if err != nil {
		return 0, err
	}
	rowsToProcess, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (outboxRow, error) {
		var r outboxRow
		err := row.Scan(&r.ID, &r.Stream, &r.Payload, &r.Attempt, &r.MaxRetries, &r.NextAttempt, &r.BackoffSeconds)
		return r, err
	})
	if err != nil {
		return 0, err
	}

	// Publish each row; failures are rescheduled individually, successes are
	// deleted together in one statement.
	sent := make([]string, 0, len(rowsToProcess))
	for _, r := range rowsToProcess {
		util.RecordOutboxInFlight(ctx, r.Stream, 1)
		startTime := time.Now()
		if err := o.publishRow(ctx, r); err != nil {
			o.cfg.Logger.Warn().Err(err).Str("outbox_id", r.ID).Str("stream", r.Stream).Msg("publish failed; scheduling retry/dlq")
			util.RecordOutboxFailed(ctx, r.Stream, err.Error())
			if err := o.scheduleRetry(ctx, r, err); err != nil {
				o.cfg.Logger.Error().Err(err).Str("outbox_id", r.ID).Msg("failed to schedule outbox retry")
			}
		} else {
			sent = append(sent, r.ID)
			util.RecordOutboxProcessed(ctx, r.Stream)
			o.cfg.Logger.Debug().Str("outbox_id", r.ID).Dur("duration", time.Since(startTime)).Msg("outbox row published")
		}
		util.RecordOutboxInFlight(ctx, r.Stream, -1)
	}

	if len(sent) > 0 {
		if _, err := o.cfg.DB.Exec(ctx, `DELETE FROM outbox WHERE id = ANY($1)`, sent); err != nil {
			// Fallback: mark SENT so the maintenance loop cleans them up and
			// the stale IN_FLIGHT recovery does not publish them again.
			o.cfg.Logger.Error().Err(err).Int("count", len(sent)).Msg("failed to delete sent outbox rows, marking SENT")
			_, _ = o.cfg.DB.Exec(ctx,
				`UPDATE outbox SET status='SENT', sent_at=NOW(), updated_at=NOW() WHERE id = ANY($1)`, sent)
		}
	}

	return len(rowsToProcess), nil
}

// publishRow pushes a single outbox row to its stream.
func (o *OutboxEnqueuer) publishRow(ctx context.Context, r outboxRow) error {
	var payload map[string]interface{}
	if len(r.Payload) == 0 {
		payload = map[string]interface{}{"id": r.ID}
	} else if err := json.Unmarshal(r.Payload, &payload); err != nil {
		payload = map[string]interface{}{"data": string(r.Payload)}
	}

	stream := r.Stream
	if stream == "" {
		stream = "workflow.tasks"
	}
	return o.cfg.Publisher.Publish(ctx, stream, payload)
}

// scheduleRetry reschedules a row whose publish failed with exponential
// backoff, or moves it to the DLQ once its retries are exhausted.
func (o *OutboxEnqueuer) scheduleRetry(ctx context.Context, r outboxRow, publishErr error) error {
	attempt := r.Attempt + 1
	maxRetries := r.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 5
	}

	if attempt > maxRetries {
		if _, err := o.cfg.DB.Exec(ctx, `INSERT INTO outbox_dlq (id, stream, payload, last_error, attempts, created_at, moved_at)
			VALUES ($1,$2,$3::jsonb,$4,$5,NOW(),NOW()) ON CONFLICT (id) DO NOTHING`,
			r.ID, r.Stream, r.Payload, publishErr.Error(), attempt); err != nil {
			o.cfg.Logger.Error().Err(err).Str("outbox_id", r.ID).Msg("failed move to dlq")
		}
		if _, err := o.cfg.DB.Exec(ctx, `UPDATE outbox SET status='DLQ', last_error=$1, attempt_count=$2, updated_at=NOW() WHERE id=$3`,
			publishErr.Error(), attempt, r.ID); err != nil {
			return err
		}
		util.RecordOutboxDLQ(ctx, r.Stream)
		return nil
	}

	nextAt := time.Now().Add(computeBackoffDuration(r.BackoffSeconds, attempt))
	_, err := o.cfg.DB.Exec(ctx, `UPDATE outbox SET status='RETRY', attempt_count=$1, next_attempt_at=$2, last_error=$3, updated_at=NOW() WHERE id=$4`,
		attempt, nextAt, publishErr.Error(), r.ID)
	return err
}

func computeBackoffDuration(baseSeconds, attempt int) time.Duration {
	if baseSeconds <= 0 {
		baseSeconds = 5
	}
	// exponential factor
	exp := float64(baseSeconds) * math.Pow(2, float64(attempt-1))
	// full jitter with guaranteed minimum of baseSeconds
	jitter := float64(baseSeconds) + rand.Float64()*exp
	return time.Duration(int64(jitter)) * time.Second
}
