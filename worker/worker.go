package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/mohitkumar/composer/pkg/stream"
	"github.com/mohitkumar/composer/worker/executors"
	"github.com/rs/zerolog"
)

const (
	// taskTimeout bounds a single task execution.
	taskTimeout = 90 * time.Second
	// defaultConcurrency is used when WorkerConfig.Concurrency is unset.
	defaultConcurrency = 1
	// defaultMaxDeliveries is used when WorkerConfig.MaxDeliveries is unset.
	defaultMaxDeliveries = 5
)

// errLeaseLost cancels an execution whose lease was taken over or released.
var errLeaseLost = errors.New("task lease lost")

// Worker executes tasks from a stream and reports results to the coordinator.
// Up to Concurrency tasks run at once; each holds a lease for its duration.
type Worker struct {
	cfg           WorkerConfig
	router        *Router
	coordClient   *CoordinatorClient
	langClientMgr *LangClientManager

	slots    chan struct{} // one token per free execution slot
	inflight sync.WaitGroup
}

// TaskMessage is the fields we expect in each stream message.
type TaskMessage struct {
	TaskID          string          `json:"task_id"`
	RunID           string          `json:"run_id"`
	NodeID          string          `json:"node_id"`
	NodeType        string          `json:"node_type"` // e.g. "http", "python", "js"
	WorkflowID      string          `json:"workflow_id"`
	WorkflowVersion int             `json:"workflow_version"`
	Attempt         int             `json:"attempt"`
	Input           json.RawMessage `json:"input"`
}

// NewWorker creates a new Worker.
func NewWorker(cfg WorkerConfig) (*Worker, error) {
	if cfg.Logger.GetLevel() == zerolog.NoLevel {
		cfg.Logger = zerolog.New(nil).Level(zerolog.InfoLevel)
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = defaultConcurrency
	}
	if cfg.MaxDeliveries <= 0 {
		cfg.MaxDeliveries = defaultMaxDeliveries
	}

	var langClientMgr *LangClientManager
	if len(cfg.LangWorkerAddrs) > 0 {
		var err error
		langClientMgr, err = NewLangClientManager(cfg.LangWorkerAddrs, cfg.Logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create language client manager: %w", err)
		}
		runtimeNames := make([]string, 0, len(cfg.LangWorkerAddrs))
		for runtime := range cfg.LangWorkerAddrs {
			runtimeNames = append(runtimeNames, runtime)
		}
		cfg.Logger.Info().Strs("runtimes", runtimeNames).Msg("language client manager created")
	}

	coordClient, err := NewCoordinatorClient(cfg.CoordinatorAddr, cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create coordinator client: %w", err)
	}

	slots := make(chan struct{}, cfg.Concurrency)
	for range cfg.Concurrency {
		slots <- struct{}{}
	}

	return &Worker{
		cfg:           cfg,
		router:        NewRouter(langClientMgr, cfg.Logger),
		coordClient:   coordClient,
		langClientMgr: langClientMgr,
		slots:         slots,
	}, nil
}

// Close releases all connections.
func (w *Worker) Close() error {
	var errs []error
	if w.coordClient != nil {
		errs = append(errs, w.coordClient.Close())
	}
	if w.langClientMgr != nil {
		errs = append(errs, w.langClientMgr.Close())
	}
	return errors.Join(errs...)
}

// EnsureConsumerGroup delegates to the stream consumer's Setup method.
// Idempotent — safe to call on every startup.
func (w *Worker) EnsureConsumerGroup(ctx context.Context) error {
	return w.cfg.Stream.Setup(ctx)
}

// Run reads new messages into free execution slots, and reclaims messages
// orphaned by crashed workers, until ctx is canceled. It returns after all
// in-flight tasks have finished.
func (w *Worker) Run(ctx context.Context) {
	w.cfg.Logger.Info().
		Str("consumer", w.cfg.ConsumerName).
		Int("concurrency", w.cfg.Concurrency).
		Msg("starting worker")

	var loops sync.WaitGroup
	loops.Add(1)
	go func() {
		defer loops.Done()
		w.reclaimPendingLoop(ctx)
	}()

	for ctx.Err() == nil {
		free := w.acquireSlots(ctx)
		if free == 0 {
			break
		}
		msgs, err := w.cfg.Stream.ReadNew(ctx, int64(free))
		if err != nil && ctx.Err() == nil {
			w.cfg.Logger.Error().Err(err).Msg("read new messages failed")
			time.Sleep(time.Second)
		}
		for _, msg := range msgs {
			w.spawn(ctx, msg)
		}
		w.releaseSlots(free - len(msgs))
	}

	w.cfg.Logger.Info().Msg("context canceled, waiting for in-flight tasks")
	loops.Wait()
	w.inflight.Wait()
}

// acquireSlots blocks until at least one execution slot is free, then takes
// every free slot. It returns 0 when ctx is canceled.
func (w *Worker) acquireSlots(ctx context.Context) int {
	select {
	case <-w.slots:
	case <-ctx.Done():
		return 0
	}
	n := 1
	for {
		select {
		case <-w.slots:
			n++
		default:
			return n
		}
	}
}

func (w *Worker) releaseSlots(n int) {
	for range n {
		w.slots <- struct{}{}
	}
}

// spawn runs msg in its own goroutine; the caller must hold a slot for it.
func (w *Worker) spawn(ctx context.Context, msg stream.Message) {
	w.inflight.Add(1)
	go func() {
		defer w.inflight.Done()
		defer w.releaseSlots(1)
		w.handleMessage(ctx, msg)
	}()
}

// reclaimPendingLoop periodically reclaims messages that have been pending too
// long (e.g. from crashed workers).
func (w *Worker) reclaimPendingLoop(ctx context.Context) {
	reclaimInterval := max(time.Duration(w.cfg.LeaseTTLSeconds/2)*time.Second, 10*time.Second)
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

	for _, msg := range msgs {
		if msg.DeliveryCount > w.cfg.MaxDeliveries {
			w.deadLetter(ctx, msg)
			continue
		}
		if w.acquireSlot(ctx) {
			w.spawn(ctx, msg)
		}
	}
}

func (w *Worker) acquireSlot(ctx context.Context) bool {
	select {
	case <-w.slots:
		return true
	case <-ctx.Done():
		return false
	}
}

// deadLetter reports a message as permanently failed to the coordinator and
// removes it from the stream so it is never redelivered.
// If the report fails the message is not acked so the next reclaim cycle
// retries the DLQ report.
func (w *Worker) deadLetter(ctx context.Context, msg stream.Message) {
	logger := w.cfg.Logger.With().Str("msg_id", msg.ID).Int64("delivery_count", msg.DeliveryCount).Logger()

	taskMsg, err := parseTaskMessage(msg.Values)
	if err != nil {
		logger.Error().Err(err).Msg("dead-letter: unparseable message payload, discarding")
		_ = w.cfg.Stream.Ack(ctx, msg.ID)
		return
	}

	logger.Error().Str("task_id", taskMsg.TaskID).Msg("dead-lettering task: exceeded max delivery attempts")
	errStr := fmt.Sprintf("task exceeded max delivery attempts (%d)", w.cfg.MaxDeliveries)
	if err := w.coordClient.ReportTaskResult(ctx, &taskMsg, nil, &errStr); err != nil {
		logger.Error().Err(err).Msg("failed to report dead-lettered task; will retry on next reclaim cycle")
		return
	}
	_ = w.cfg.Stream.Ack(ctx, msg.ID)
}

// handleMessage processes one message and acks it once the outcome is durable
// at the coordinator (or the message turned out to be stale or invalid).
func (w *Worker) handleMessage(ctx context.Context, msg stream.Message) {
	taskMsg, err := parseTaskMessage(msg.Values)
	if err != nil {
		w.cfg.Logger.Error().Err(err).Msg("invalid task message; acking and discarding")
		_ = w.cfg.Stream.Ack(ctx, msg.ID)
		return
	}

	if err := w.processMessage(ctx, &taskMsg); err != nil {
		w.cfg.Logger.Error().Err(err).Str("task_id", taskMsg.TaskID).Msg("task processing failed")
		return // not acked — redelivered via reclaim
	}
	_ = w.cfg.Stream.Ack(ctx, msg.ID)
}

func (w *Worker) processMessage(ctx context.Context, msg *TaskMessage) error {
	logger := w.cfg.Logger.With().
		Str("task_id", msg.TaskID).
		Str("node_id", msg.NodeID).
		Str("run_id", msg.RunID).
		Int("attempt", msg.Attempt).
		Logger()

	acquired, err := w.coordClient.AcquireTaskLease(ctx, msg.TaskID, w.cfg.ConsumerName, w.cfg.LeaseTTLSeconds)
	if err != nil {
		return fmt.Errorf("acquire task lease: %w", err)
	}
	if !acquired {
		// Running elsewhere under a live lease, already finished, or superseded.
		logger.Debug().Msg("task lease not acquired, dropping message")
		return nil
	}

	execCtx, cancel := context.WithTimeout(ctx, taskTimeout)
	defer cancel()
	leaseCtx, stopRenewal := context.WithCancelCause(execCtx)
	defer stopRenewal(nil)
	go w.renewLeaseLoop(leaseCtx, msg.TaskID, stopRenewal, logger)

	// Embed the task ID so executors can use task_instances.id as an
	// idempotency key (e.g. Idempotency-Key header on HTTP requests).
	start := time.Now()
	output, execErr := w.router.Route(executors.WithTaskID(leaseCtx, msg.TaskID), msg.NodeType, msg.Input)
	stopRenewal(nil)

	if errors.Is(context.Cause(leaseCtx), errLeaseLost) {
		// Another worker owns the task now; its result is the one that counts.
		logger.Warn().Msg("lease lost during execution, discarding result")
		return nil
	}
	if ctx.Err() != nil {
		// Shutting down: don't report the interruption as a task failure. The
		// lease expires and the coordinator re-dispatches the task.
		return fmt.Errorf("worker stopped during execution: %w", ctx.Err())
	}

	var errStr *string
	if execErr != nil {
		s := execErr.Error()
		errStr = &s
	}
	logger.Info().Dur("duration", time.Since(start)).Bool("success", execErr == nil).Msg("task executed")

	if err := w.coordClient.ReportTaskResult(ctx, msg, output, errStr); err != nil {
		return fmt.Errorf("report result: %w", err)
	}
	return nil
}

// renewLeaseLoop renews the task lease every TTL/3 until ctx is canceled. If
// the lease is lost it cancels the execution with errLeaseLost.
func (w *Worker) renewLeaseLoop(ctx context.Context, taskID string, cancel context.CancelCauseFunc, logger zerolog.Logger) {
	renewInterval := max(time.Duration(w.cfg.LeaseTTLSeconds/3)*time.Second, 5*time.Second)
	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewed, err := w.coordClient.RenewTaskLease(ctx, taskID, w.cfg.ConsumerName, w.cfg.LeaseTTLSeconds)
			switch {
			case err != nil:
				// Transient; the lease survives until its TTL, so keep trying.
				logger.Warn().Err(err).Msg("failed to renew task lease")
			case !renewed:
				cancel(errLeaseLost)
				return
			}
		}
	}
}

func parseTaskMessage(values map[string]any) (TaskMessage, error) {
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
	getInt := func(k string) int {
		s, _ := get(k)
		n, _ := strconv.Atoi(s)
		return n
	}

	var t TaskMessage
	var ok bool
	if t.TaskID, ok = get("task_id"); !ok {
		return t, fmt.Errorf("missing task_id")
	}
	t.RunID, _ = get("run_id")
	t.NodeID, _ = get("node_id")
	if t.NodeType, ok = get("node_type"); !ok {
		t.NodeType = "http"
	}
	t.WorkflowID, _ = get("workflow_id")
	t.WorkflowVersion = getInt("workflow_version")
	t.Attempt = getInt("attempt")
	if s, ok := get("input"); ok {
		t.Input = json.RawMessage(s)
	}
	return t, nil
}
