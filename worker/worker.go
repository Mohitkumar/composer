package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/mohitkumar/composer/pkg/stream"
	"github.com/rs/zerolog"
)

// Worker processes tasks from a stream and reports results via gRPC.
type Worker struct {
	cfg           WorkerConfig
	router        *Router
	coordClient   *CoordinatorClient
	langClientMgr *LangClientManager
}

// TaskMessage is the fields we expect in each stream message.
type TaskMessage struct {
	TaskID   string          `json:"task_id"`
	RunID    string          `json:"run_id"`
	NodeID   string          `json:"node_id"`
	NodeType string          `json:"node_type"` // e.g. "http", "python", "js"
	Input    json.RawMessage `json:"input"`
}

// NewWorker creates a new Worker.
func NewWorker(cfg WorkerConfig) (*Worker, error) {
	if cfg.Logger.GetLevel() == zerolog.NoLevel {
		cfg.Logger = zerolog.New(nil).Level(zerolog.InfoLevel)
	}

	var langClientMgr *LangClientManager
	var err error

	if len(cfg.LangWorkerAddrs) > 0 {
		langClientMgr, err = NewLangClientManager(cfg.LangWorkerAddrs, cfg.Logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create language client manager: %w", err)
		}
		runtimeNames := make([]string, 0, len(cfg.LangWorkerAddrs))
		for runtime := range cfg.LangWorkerAddrs {
			runtimeNames = append(runtimeNames, runtime)
		}
		cfg.Logger.Info().
			Strs("runtimes", runtimeNames).
			Msg("language client manager created with multiple runtimes")
	}

	coordClient, err := NewCoordinatorClient(cfg.CoordinatorAddr, cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create coordinator client: %w", err)
	}

	router := NewRouter(langClientMgr, cfg.Logger)

	return &Worker{
		cfg:           cfg,
		router:        router,
		coordClient:   coordClient,
		langClientMgr: langClientMgr,
	}, nil
}

// Close releases all connections.
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

// EnsureConsumerGroup delegates to the stream consumer's Setup method.
// Idempotent — safe to call on every startup.
func (w *Worker) EnsureConsumerGroup(ctx context.Context) error {
	return w.cfg.Stream.Setup(ctx)
}

// Run starts the worker loop: reads new messages and reclaims orphaned pending
// messages in parallel.
func (w *Worker) Run(ctx context.Context) {
	w.cfg.Logger.Info().
		Str("consumer", w.cfg.ConsumerName).
		Msg("starting worker")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.reclaimPendingLoop(ctx)
	}()

	for {
		select {
		case <-ctx.Done():
			w.cfg.Logger.Info().Msg("context canceled, stopping worker")
			wg.Wait()
			return
		default:
		}

		msgs, err := w.cfg.Stream.ReadNew(ctx)
		if err != nil {
			w.cfg.Logger.Error().Err(err).Msg("read new messages failed")
			time.Sleep(1 * time.Second)
			continue
		}
		for _, msg := range msgs {
			w.handleMessage(ctx, msg)
		}
	}
}

// reclaimPendingLoop periodically reclaims messages that have been pending too
// long (e.g. from crashed workers).
func (w *Worker) reclaimPendingLoop(ctx context.Context) {
	reclaimInterval := time.Duration(w.cfg.LeaseTTLSeconds/2) * time.Second
	if reclaimInterval < 10*time.Second {
		reclaimInterval = 10 * time.Second
	}
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

// reclaimPendingMessages reclaims orphaned messages and enforces the DLQ limit.
// Messages whose DeliveryCount exceeds MaxDeliveries are dead-lettered instead
// of being reprocessed.
func (w *Worker) reclaimPendingMessages(ctx context.Context, minIdleTime time.Duration) {
	msgs, err := w.cfg.Stream.ReclaimOrphaned(ctx, minIdleTime)
	if err != nil {
		w.cfg.Logger.Error().Err(err).Msg("reclaim orphaned messages failed")
		return
	}
	if len(msgs) > 0 {
		w.cfg.Logger.Info().Int("reclaimed", len(msgs)).Msg("reclaimed orphaned pending messages")
	}

	maxDeliveries := w.cfg.MaxDeliveries
	if maxDeliveries <= 0 {
		maxDeliveries = 5
	}

	for _, msg := range msgs {
		if msg.DeliveryCount > maxDeliveries {
			w.deadLetter(ctx, msg, maxDeliveries)
			continue
		}
		w.handleMessage(ctx, msg)
	}
}

// deadLetter reports a message as permanently failed to the coordinator and
// removes it from the stream so it is never redelivered.
// If ReportTaskResult fails the message is not acked so the next reclaim cycle
// will retry the DLQ report.
func (w *Worker) deadLetter(ctx context.Context, msg stream.Message, maxDeliveries int64) {
	taskMsg, err := parseTaskMessage(msg.Values)
	if err != nil {
		w.cfg.Logger.Error().
			Err(err).
			Str("msg_id", msg.ID).
			Int64("delivery_count", msg.DeliveryCount).
			Msg("dead-letter: unparseable message payload, discarding")
		_ = w.cfg.Stream.Ack(ctx, msg.ID)
		return
	}

	w.cfg.Logger.Error().
		Str("msg_id", msg.ID).
		Str("task_id", taskMsg.TaskID).
		Int64("delivery_count", msg.DeliveryCount).
		Int64("max_deliveries", maxDeliveries).
		Msg("dead-lettering task: exceeded max delivery attempts")

	errStr := fmt.Sprintf("task exceeded max delivery attempts (%d)", maxDeliveries)
	if reportErr := w.coordClient.ReportTaskResult(
		ctx, taskMsg.TaskID, taskMsg.RunID, taskMsg.NodeID, nil, &errStr,
	); reportErr != nil {
		w.cfg.Logger.Error().
			Err(reportErr).
			Str("task_id", taskMsg.TaskID).
			Msg("failed to report dead-lettered task; will retry on next reclaim cycle")
		return // do not ack — retry DLQ report next cycle
	}

	_ = w.cfg.Stream.Ack(ctx, msg.ID)
}

// handleMessage parses and processes a single message.
func (w *Worker) handleMessage(ctx context.Context, msg stream.Message) {
	taskMsg, err := parseTaskMessage(msg.Values)
	if err != nil {
		w.cfg.Logger.Error().Err(err).Msg("invalid task message; acking and discarding")
		_ = w.cfg.Stream.Ack(ctx, msg.ID)
		return
	}

	processCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	if err := w.processMessage(processCtx, &taskMsg); err != nil {
		w.cfg.Logger.Error().Err(err).Str("task", taskMsg.TaskID).Msg("task processing failed")
		// Do NOT ack — will be retried via reclaim.
	} else {
		_ = w.cfg.Stream.Ack(ctx, msg.ID)
	}
}

func parseTaskMessage(values map[string]any) (TaskMessage, error) {
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
		t.NodeType = "http"
	}
	if s, ok := get("input"); ok {
		t.Input = json.RawMessage(s)
	}
	return t, nil
}

func (w *Worker) processMessage(ctx context.Context, msg *TaskMessage) error {
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
		return nil
	}

	leaseCtx2, stopRenewal := context.WithCancel(ctx)
	go w.renewLeaseLoop(leaseCtx2, msg.TaskID, logger)

	var execErr *string
	start := time.Now()

	execOutput, routeErr := w.router.Route(ctx, msg.NodeType, msg.Input)
	if routeErr != nil {
		errStr := routeErr.Error()
		execErr = &errStr
	}

	stopRenewal()

	duration := time.Since(start)
	logger.Info().
		Dur("duration", duration).
		Bool("success", execErr == nil).
		Msg("task executed")

	if err := w.coordClient.ReportTaskResult(ctx, msg.TaskID, msg.RunID, msg.NodeID, execOutput, execErr); err != nil {
		logger.Error().Err(err).Msg("failed to report task result to coordinator")
		return fmt.Errorf("failed to report result: %w", err)
	}

	logger.Info().Msg("reported completion to coordinator")
	return nil
}

// renewLeaseLoop periodically renews the task lease until ctx is canceled.
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
