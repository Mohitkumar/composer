package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// Worker processes tasks from Redis stream & reports results via gRPC
type Worker struct {
	cfg           WorkerConfig
	router        *Router
	coordClient   *CoordinatorClient
	langClient    *LangClient        // Legacy single client (for backward compatibility)
	langClientMgr *LangClientManager // New multi-client manager
}

// TaskMessage is the fields we expect in Redis stream message
type TaskMessage struct {
	TaskID   string          `json:"task_id"` // task_instance id
	RunID    string          `json:"run_id"`
	NodeID   string          `json:"node_id"`
	NodeType string          `json:"node_type"` // e.g., "http", "python", "js"
	Input    json.RawMessage `json:"input"`
}

// NewWorker creates a new worker instance
func NewWorker(cfg WorkerConfig) (*Worker, error) {
	if cfg.Logger.GetLevel() == zerolog.NoLevel {
		cfg.Logger = zerolog.New(nil).Level(zerolog.InfoLevel)
	}

	// Create language client manager if addresses provided
	var langClientMgr *LangClientManager
	var err error

	if len(cfg.LangWorkerAddrs) > 0 {
		langClientMgr, err = NewLangClientManager(cfg.LangWorkerAddrs, cfg.Logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create language client manager: %w", err)
		}
		// Extract runtime names for logging
		runtimeNames := make([]string, 0, len(cfg.LangWorkerAddrs))
		for runtime := range cfg.LangWorkerAddrs {
			runtimeNames = append(runtimeNames, runtime)
		}
		cfg.Logger.Info().
			Strs("runtimes", runtimeNames).
			Msg("language client manager created with multiple runtimes")
	}

	// Create coordinator client
	coordClient, err := NewCoordinatorClient(cfg.CoordinatorAddr, cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create coordinator client: %w", err)
	}

	// Create router
	router := NewRouter(langClientMgr, cfg.Logger)

	return &Worker{
		cfg:           cfg,
		router:        router,
		coordClient:   coordClient,
		langClientMgr: langClientMgr,
	}, nil
}

// Close closes all connections
func (w *Worker) Close() error {
	var errs []error
	if w.coordClient != nil {
		if err := w.coordClient.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if w.langClientMgr != nil {
		if err := w.langClientMgr.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing connections: %v", errs)
	}
	return nil
}

// EnsureConsumerGroup creates the consumer group if not exists
func (w *Worker) EnsureConsumerGroup(ctx context.Context) error {
	err := w.cfg.Redis.XGroupCreateMkStream(ctx, w.cfg.StreamName, w.cfg.ConsumerGroup, "0")
	if err != nil {
		// Ignore BUSYGROUP error (group already exists)
		if err.Error() != "BUSYGROUP Consumer Group name already exists" {
			w.cfg.Logger.Debug().Err(err).Msg("xgroup create maybe exists")
		}
	}
	return nil
}

// Run starts the worker loop: reads new messages and reclaims orphaned pending messages.
func (w *Worker) Run(ctx context.Context) {
	w.cfg.Logger.Info().
		Str("stream", w.cfg.StreamName).
		Str("group", w.cfg.ConsumerGroup).
		Str("consumer", w.cfg.ConsumerName).
		Msg("starting worker")

	// Start the pending message recovery loop in a separate goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.reclaimPendingLoop(ctx)
	}()

	// Main loop: read new messages
	for {
		select {
		case <-ctx.Done():
			w.cfg.Logger.Info().Msg("context canceled, stopping worker")
			wg.Wait()
			return
		default:
		}

		// XREADGROUP BLOCK for new messages
		args := &redis.XReadGroupArgs{
			Group:    w.cfg.ConsumerGroup,
			Consumer: w.cfg.ConsumerName,
			Streams:  []string{w.cfg.StreamName, ">"},
			Count:    1,
			Block:    5 * time.Second,
		}

		res, err := w.cfg.Redis.XReadGroup(ctx, args)
		if err != nil {
			if err == redis.Nil {
				continue
			}
			w.cfg.Logger.Error().Err(err).Msg("xreadgroup failed")
			time.Sleep(1 * time.Second)
			continue
		}

		// Process each stream result
		for _, streamRes := range res {
			for _, msg := range streamRes.Messages {
				w.handleMessage(ctx, msg)
			}
		}
	}
}

// reclaimPendingLoop periodically reclaims messages that have been pending too long
// (e.g., from crashed workers). Uses XAUTOCLAIM to atomically claim orphaned messages.
func (w *Worker) reclaimPendingLoop(ctx context.Context) {
	// Reclaim interval: check every lease TTL / 2
	reclaimInterval := time.Duration(w.cfg.LeaseTTLSeconds/2) * time.Second
	if reclaimInterval < 10*time.Second {
		reclaimInterval = 10 * time.Second
	}
	// Messages idle longer than the lease TTL are considered orphaned
	minIdleTime := time.Duration(w.cfg.LeaseTTLSeconds) * time.Second

	ticker := time.NewTicker(reclaimInterval)
	defer ticker.Stop()

	w.cfg.Logger.Info().
		Dur("reclaim_interval", reclaimInterval).
		Dur("min_idle_time", minIdleTime).
		Msg("starting pending message recovery loop")

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.reclaimPendingMessages(ctx, minIdleTime)
		}
	}
}

// reclaimPendingMessages uses XAUTOCLAIM to take ownership of messages that have been
// pending longer than minIdleTime (orphaned by crashed/slow workers).
func (w *Worker) reclaimPendingMessages(ctx context.Context, minIdleTime time.Duration) {
	args := &redis.XAutoClaimArgs{
		Stream:   w.cfg.StreamName,
		Group:    w.cfg.ConsumerGroup,
		Consumer: w.cfg.ConsumerName,
		MinIdle:  minIdleTime,
		Start:    "0-0",
		Count:    10,
	}

	msgs, _, err := w.cfg.Redis.XAutoClaim(ctx, args)
	if err != nil {
		w.cfg.Logger.Error().Err(err).Msg("xautoclaim failed")
		return
	}

	if len(msgs) > 0 {
		w.cfg.Logger.Info().Int("reclaimed", len(msgs)).Msg("reclaimed orphaned pending messages")
	}

	for _, msg := range msgs {
		w.handleMessage(ctx, msg)
	}
}

// handleMessage parses and processes a single Redis stream message.
func (w *Worker) handleMessage(ctx context.Context, msg redis.XMessage) {
	taskMsg, err := parseTaskMessage(msg.Values)
	if err != nil {
		w.cfg.Logger.Error().Err(err).Msg("invalid task message; ack and continue")
		_, _ = w.cfg.Redis.XAck(ctx, w.cfg.StreamName, w.cfg.ConsumerGroup, msg.ID)
		return
	}

	// Process with timeout (default 90 seconds)
	processCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	if err := w.processMessage(processCtx, &taskMsg, msg.ID); err != nil {
		w.cfg.Logger.Error().Err(err).Str("task", taskMsg.TaskID).Msg("task processing failed")
		// Do NOT ack; will be retried via XAUTOCLAIM
	} else {
		// Ack message after successful processing
		_, _ = w.cfg.Redis.XAck(ctx, w.cfg.StreamName, w.cfg.ConsumerGroup, msg.ID)
	}
}

func parseTaskMessage(values map[string]interface{}) (TaskMessage, error) {
	var t TaskMessage
	get := func(k string) (string, bool) {
		v, ok := values[k]
		if !ok {
			return "", false
		}
		switch vt := v.(type) {
		case string:
			return vt, true
		case []byte:
			return string(vt), true
		default:
			b, err := json.Marshal(vt)
			if err != nil {
				return "", false
			}
			return string(b), true
		}
	}

	if s, ok := get("task_id"); ok {
		t.TaskID = s
	} else {
		return t, fmt.Errorf("missing task_id")
	}
	if s, ok := get("run_id"); ok {
		t.RunID = s
	}
	if s, ok := get("node_id"); ok {
		t.NodeID = s
	}
	if s, ok := get("node_type"); ok {
		t.NodeType = s
	} else {
		t.NodeType = "http" // default
	}
	if s, ok := get("input"); ok {
		t.Input = json.RawMessage(s)
	}
	return t, nil
}

func (w *Worker) processMessage(ctx context.Context, msg *TaskMessage, msgID string) error {
	logger := w.cfg.Logger.With().
		Str("task", msg.TaskID).
		Str("node", msg.NodeID).
		Str("run", msg.RunID).
		Logger()

	leaseCtx, cancelLease := context.WithTimeout(ctx, 5*time.Second)
	acquired, err := w.coordClient.AcquireTaskLease(leaseCtx, msg.TaskID, w.cfg.ConsumerName, w.cfg.LeaseTTLSeconds)
	cancelLease()
	if err != nil {
		logger.Error().Err(err).Msg("failed to acquire task lease")
		return err
	}
	if !acquired {
		logger.Warn().Msg("task lease not acquired (already running or completed), acking message")
		// Task is already owned or finished; ack so it doesn't keep getting reclaimed
		return nil
	}

	// Start lease renewal goroutine — renews every TTL/3 to prevent expiry during execution
	leaseCtx2, stopRenewal := context.WithCancel(ctx)
	go w.renewLeaseLoop(leaseCtx2, msg.TaskID, logger)

	// Execute the task using router
	var execOutput json.RawMessage
	var execErr *string
	start := time.Now()

	execOutput, routeErr := w.router.Route(ctx, msg.NodeType, msg.Input)
	if routeErr != nil {
		errStr := routeErr.Error()
		execErr = &errStr
	}

	// Stop lease renewal before reporting result
	stopRenewal()

	duration := time.Since(start)
	logger.Info().
		Dur("duration", duration).
		Bool("success", execErr == nil).
		Msg("task executed")

	// Report completion to coordinator via gRPC
	if err := w.coordClient.ReportTaskResult(ctx, msg.TaskID, msg.RunID, msg.NodeID, execOutput, execErr); err != nil {
		logger.Error().Err(err).Msg("failed to report task result to coordinator")
		return fmt.Errorf("failed to report result: %w", err)
	}

	logger.Info().Msg("reported completion to coordinator")
	return nil
}

// renewLeaseLoop periodically renews the task lease until ctx is canceled.
// Renews every TTL/3 to ensure the lease doesn't expire during task execution.
func (w *Worker) renewLeaseLoop(ctx context.Context, taskID string, logger zerolog.Logger) {
	renewInterval := time.Duration(w.cfg.LeaseTTLSeconds/3) * time.Second
	if renewInterval < 5*time.Second {
		renewInterval = 5 * time.Second
	}

	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			acquired, err := w.coordClient.AcquireTaskLease(renewCtx, taskID, w.cfg.ConsumerName, w.cfg.LeaseTTLSeconds)
			cancel()
			if err != nil {
				logger.Warn().Err(err).Str("task_id", taskID).Msg("failed to renew task lease")
				continue
			}
			if !acquired {
				logger.Warn().Str("task_id", taskID).Msg("lease renewal failed: task no longer in leasable state")
				return
			}
			logger.Debug().Str("task_id", taskID).Msg("task lease renewed")
		}
	}
}
