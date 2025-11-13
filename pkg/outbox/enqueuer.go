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
		cfg.PollInterval = 1 * time.Second
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
				if err := o.processBatch(ctx); err != nil {
					o.cfg.Logger.Error().Err(err).Msg("processBatch failed")
				}
			}()
		case <-ctx.Done():
			o.shutdown()
			return
		}

		// Throttle polling
		time.Sleep(o.cfg.PollInterval)
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

// processBatch picks a batch of outbox rows with FOR UPDATE SKIP LOCKED, marks them IN_FLIGHT,
// commits, and processes them one by one (pushing to Redis) and updates DB accordingly.
func (o *OutboxEnqueuer) processBatch(ctx context.Context) error {
	// 1) begin tx and select rows for update skip locked
	tx, err := o.cfg.DB.Begin(ctx)
	if err != nil {
		return err
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
		return err
	}
	defer rows.Close()

	var rowsToProcess []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.ID, &r.Stream, &r.Payload, &r.Attempt, &r.MaxRetries, &r.NextAttempt, &r.BackoffSeconds); err != nil {
			return err
		}
		rowsToProcess = append(rowsToProcess, r)
	}

	if len(rowsToProcess) == 0 {
		// nothing to do
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return nil
	}

	// mark selected rows IN_FLIGHT
	for _, r := range rowsToProcess {
		if _, err := tx.Exec(ctx, `UPDATE outbox SET status='IN_FLIGHT', updated_at=NOW() WHERE id=$1`, r.ID); err != nil {
			return err
		}
	}

	// commit tx to release locks (we will process rows outside transaction)
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	// process items one by one (sequentially here; could parallelize per batch)
	for _, r := range rowsToProcess {
		util.RecordOutboxInFlight(ctx, r.Stream, 1)
		startTime := time.Now()
		if err := o.processRow(ctx, r); err != nil {
			// log and continue; processRow includes DB updates for failure
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

	return nil
}

// processRow pushes a single outbox row to Redis and updates DB accordingly (success -> SENT, failure -> schedule retry or DLQ).
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
		// fallback stream if missing
		stream = "workflow.tasks"
	}

	// Try push to redis using redisq abstraction
	_, err := o.cfg.Redis.XAdd(ctx, stream, payloadMap)
	if err == nil {
		// success -> mark SENT
		_, uerr := o.cfg.DB.Exec(ctx, `UPDATE outbox SET status='SENT', sent_at=NOW(), updated_at=NOW() WHERE id=$1`, r.ID)
		if uerr != nil {
			// critical: cannot update outbox status; log error
			o.cfg.Logger.Error().Err(uerr).Str("outbox_id", r.ID).Msg("failed mark SENT after xadd success")
			return uerr
		}
		return nil
	}

	// failure path: schedule retry or DLQ
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
		// move to DLQ (insert into outbox_dlq and mark original DLQ)
		_, derr := o.cfg.DB.Exec(ctx, `INSERT INTO outbox_dlq (id, stream, payload, last_error, attempts, created_at, moved_at)
			VALUES ($1,$2,$3::jsonb,$4,$5,NOW(),NOW()) ON CONFLICT (id) DO NOTHING`, r.ID, stream, r.Payload, err.Error(), attempt)
		if derr != nil {
			o.cfg.Logger.Error().Err(derr).Str("outbox_id", r.ID).Msg("failed move to dlq")
			// still attempt to mark as DLQ to avoid infinite retries
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
	// full jitter
	jitter := rand.Float64() * exp
	return time.Duration(int64(jitter)) * time.Second
}
