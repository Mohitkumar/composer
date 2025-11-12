package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// Worker processes tasks from Redis stream & reports results via gRPC
type Worker struct {
	cfg         WorkerConfig
	router      *Router
	coordClient *CoordinatorClient
	langClient  *LangClient
}

// TaskMessage is the fields we expect in Redis stream message
type TaskMessage struct {
	TaskID   string          `json:"task_id"` // task_instance id
	RunID    string          `json:"run_id"`
	NodeID   string          `json:"node_id"`
	NodeType string          `json:"node_type"` // e.g., "http", "python", "js", "s3", "csv"
	Input    json.RawMessage `json:"input"`
}

// NewWorker creates a new worker instance
func NewWorker(cfg WorkerConfig) (*Worker, error) {
	if cfg.Logger.GetLevel() == zerolog.NoLevel {
		cfg.Logger = zerolog.New(nil).Level(zerolog.InfoLevel)
	}

	// Create language client if address provided
	var langClient *LangClient
	var err error
	if cfg.LangWorkerAddr != "" {
		langClient, err = NewLangClient(cfg.LangWorkerAddr, cfg.Logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create language client: %w", err)
		}
	}

	// Create coordinator client
	coordClient, err := NewCoordinatorClient(cfg.CoordinatorAddr, cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create coordinator client: %w", err)
	}

	// Create router
	router := NewRouter(langClient, cfg.Logger)

	return &Worker{
		cfg:         cfg,
		router:      router,
		coordClient: coordClient,
		langClient:  langClient,
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
	if w.langClient != nil {
		if err := w.langClient.Close(); err != nil {
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

// Run infinite loop: read messages, process
func (w *Worker) Run(ctx context.Context) {
	w.cfg.Logger.Info().
		Str("stream", w.cfg.StreamName).
		Str("group", w.cfg.ConsumerGroup).
		Str("consumer", w.cfg.ConsumerName).
		Msg("starting worker")

	for {
		select {
		case <-ctx.Done():
			w.cfg.Logger.Info().Msg("context canceled, stopping worker")
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
				// Parse fields into TaskMessage
				taskMsg, err := parseTaskMessage(msg.Values)
				if err != nil {
					w.cfg.Logger.Error().Err(err).Msg("invalid task message; ack and continue")
					// Ack to remove from stream
					_, _ = w.cfg.Redis.XAck(ctx, w.cfg.StreamName, w.cfg.ConsumerGroup, msg.ID)
					continue
				}

				// Process with timeout (default 90 seconds)
				processCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
				if err := w.processMessage(processCtx, &taskMsg, msg.ID); err != nil {
					w.cfg.Logger.Error().Err(err).Str("task", taskMsg.TaskID).Msg("task processing failed")
					// Do NOT ack; will be retried or moved to DLQ
				} else {
					// Ack message after successful processing
					_, _ = w.cfg.Redis.XAck(ctx, w.cfg.StreamName, w.cfg.ConsumerGroup, msg.ID)
				}
				cancel()
			}
		}
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

	// Execute the task using router
	var execOutput json.RawMessage
	var execErr *string
	start := time.Now()

	execOutput, err := w.router.Route(ctx, msg.NodeType, msg.Input)
	if err != nil {
		errStr := err.Error()
		execErr = &errStr
	}

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
