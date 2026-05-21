package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"math/rand"
	"sync"
	"time"

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

// processBatch picks a batch of outbox rows with FOR UPDATE SKIP LOCKED, marks them IN_FLIGHT,
// commits, and processes them one by one (pushing to Redis) and updates DB accordingly.
// Returns the number of rows processed and any error.
func (o *OutboxEnqueuer) processBatch(ctx context.Context) (int, error) {
	tx, err := o.cfg.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// select key fields from outbox where status in PENDING/RETRY and next_attempt_at <= now
	// include attempt_count, max_retries, backoff_seconds for retry planning
	query := `
SELECT id, stream, payload, attempt_count, max_retries, next_attempt_at, backoff_seconds
FROM outbox
WHERE status IN ('PENDING','RETRY') AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
ORDER BY created_at
LIMIT $1
FOR UPDATE SKIP LOCKED
`
	rows, err := tx.Query(ctx, query, o.cfg.BatchSize)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var rowsToProcess []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.ID, &r.Stream, &r.Payload, &r.Attempt, &r.MaxRetries, &r.NextAttempt, &r.BackoffSeconds); err != nil {
			return 0, err
		}
		rowsToProcess = append(rowsToProcess, r)
	}

	if len(rowsToProcess) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return 0, err
		}
		return 0, nil
	}

	// mark selected rows IN_FLIGHT
	for _, r := range rowsToProcess {
		if _, err := tx.Exec(ctx, `UPDATE outbox SET status='IN_FLIGHT', updated_at=NOW() WHERE id=$1`, r.ID); err != nil {
			return 0, err
		}
	}

	// commit tx to release locks (we will process rows outside transaction)
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}

	// process items one by one (sequentially here; could parallelize per batch)
	for _, r := range rowsToProcess {
		util.RecordOutboxInFlight(ctx, r.Stream, 1)
		startTime := time.Now()
		if err := o.processRow(ctx, r); err != nil {
			o.cfg.Logger.Error().
				Err(err).
				Str("outbox_id", r.ID).
				Str("stream", r.Stream).
				Dur("duration_ms", time.Since(startTime)).
				Msg("processing outbox row failed")
			util.RecordOutboxFailed(ctx, r.Stream, err.Error())
		} else {
			duration := time.Since(startTime)
			o.cfg.Logger.Debug().
				Str("outbox_id", r.ID).
				Str("stream", r.Stream).
				Dur("duration_ms", duration).
				Msg("outbox row processed successfully")
			util.RecordOutboxProcessed(ctx, r.Stream)
		}
		util.RecordOutboxInFlight(ctx, r.Stream, -1)
	}

	return len(rowsToProcess), nil
}

// processRow pushes a single outbox row to Redis and updates DB accordingly (success -> delete, failure -> schedule retry or DLQ).
func (o *OutboxEnqueuer) processRow(ctx context.Context, r outboxRow) error {
	// parse payload to fetch stream override or payload map
	var payloadMap map[string]interface{}
	if len(r.Payload) == 0 {
		payloadMap = map[string]interface{}{"id": r.ID}
	} else {
		// payload may already include stream and values, but we expect the outbox.table.stream column to contain target stream
		if err := json.Unmarshal(r.Payload, &payloadMap); err != nil {
			// if payload not JSON, wrap raw string
			payloadMap = map[string]interface{}{"data": string(r.Payload)}
		}
	}

	stream := r.Stream
	if stream == "" {
		stream = "workflow.tasks"
	}

	err := o.cfg.Publisher.Publish(ctx, stream, payloadMap)
	if err == nil {
		// Successfully sent — delete the outbox row to prevent table bloat
		_, uerr := o.cfg.DB.Exec(ctx, `DELETE FROM outbox WHERE id=$1`, r.ID)
		if uerr != nil {
			// Fallback: mark as SENT so the maintenance loop can clean it up later
			o.cfg.Logger.Error().Err(uerr).Str("outbox_id", r.ID).Msg("failed to delete outbox row after send, marking SENT")
			_, _ = o.cfg.DB.Exec(ctx, `UPDATE outbox SET status='SENT', sent_at=NOW(), updated_at=NOW() WHERE id=$1`, r.ID)
		}
		return nil
	}

	o.cfg.Logger.Warn().Err(err).Str("outbox_id", r.ID).Msg("redis xadd failed; scheduling retry/dlq")

	attempt := r.Attempt + 1
	maxRetries := r.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 5
	}
	backoffSec := r.BackoffSeconds
	if backoffSec <= 0 {
		backoffSec = 5
	}

	if attempt > maxRetries {
		_, derr := o.cfg.DB.Exec(ctx, `INSERT INTO outbox_dlq (id, stream, payload, last_error, attempts, created_at, moved_at)
			VALUES ($1,$2,$3::jsonb,$4,$5,NOW(),NOW()) ON CONFLICT (id) DO NOTHING`, r.ID, stream, r.Payload, err.Error(), attempt)
		if derr != nil {
			o.cfg.Logger.Error().Err(derr).Str("outbox_id", r.ID).Msg("failed move to dlq")
		}
		if _, uerr := o.cfg.DB.Exec(ctx, `UPDATE outbox SET status='DLQ', last_error=$1, attempt_count=$2, updated_at=NOW() WHERE id=$3`, err.Error(), attempt, r.ID); uerr != nil {
			o.cfg.Logger.Error().Err(uerr).Str("outbox_id", r.ID).Msg("failed mark DLQ")
			return uerr
		}
		util.RecordOutboxDLQ(ctx, stream)
		return nil
	}

	// schedule next attempt with exponential backoff + jitter
	delay := computeBackoffDuration(backoffSec, attempt)
	nextAt := time.Now().Add(delay)
	if _, uerr := o.cfg.DB.Exec(ctx, `UPDATE outbox SET status='RETRY', attempt_count=$1, next_attempt_at=$2, last_error=$3, updated_at=NOW() WHERE id=$4`, attempt, nextAt, err.Error(), r.ID); uerr != nil {
		o.cfg.Logger.Error().Err(uerr).Str("outbox_id", r.ID).Msg("failed schedule retry")
		return uerr
	}
	return nil
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
