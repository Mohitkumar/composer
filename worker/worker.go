package worker

import (
	"context"
	"fmt"
	"time"

	workflowpb "github.com/mohitkumar/composer/pkg/api/proto/gen/go/workflow"
	"github.com/mohitkumar/composer/worker/executors"
	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Worker processes tasks received from the coordinator via long-polling and
// reports results via gRPC.
type Worker struct {
	cfg           WorkerConfig
	router        *Router
	coordClient   *CoordinatorClient
	langClientMgr *LangClientManager
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
			Msg("language client manager created")
	}

	coordClient, err := NewCoordinatorClient(cfg.CoordinatorAddr, cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create coordinator client: %w", err)
	}

	return &Worker{
		cfg:           cfg,
		router:        NewRouter(langClientMgr, cfg.Logger),
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

// Run starts the worker poll loop. It long-polls the coordinator for tasks and
// processes them one at a time. Re-polls immediately on timeout, backs off
// briefly on unexpected errors.
func (w *Worker) Run(ctx context.Context) {
	w.cfg.Logger.Info().
		Str("worker_id", w.cfg.WorkerID).
		Strs("task_types", w.cfg.TaskTypes).
		Msg("starting worker")

	for {
		select {
		case <-ctx.Done():
			w.cfg.Logger.Info().Msg("context canceled, stopping worker")
			return
		default:
		}

		assignment, err := w.coordClient.PollTaskQueue(ctx, w.cfg.WorkerID, w.cfg.TaskTypes)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if isTimeoutErr(err) {
				// Normal poll timeout — re-poll immediately.
				continue
			}
			w.cfg.Logger.Error().Err(err).Msg("poll error")
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
			}
			continue
		}

		if err := w.processTask(ctx, assignment); err != nil {
			w.cfg.Logger.Error().Err(err).
				Str("task_id", assignment.TaskId).
				Msg("task processing failed")
		}
	}
}

func isTimeoutErr(err error) bool {
	if s, ok := status.FromError(err); ok {
		return s.Code() == codes.DeadlineExceeded
	}
	return err == context.DeadlineExceeded
}

// processTask acquires the lease, executes the task, and reports the result.
func (w *Worker) processTask(ctx context.Context, a *workflowpb.TaskAssignment) error {
	logger := w.cfg.Logger.With().
		Str("task_id", a.TaskId).
		Str("node_id", a.NodeId).
		Str("run_id", a.RunId).
		Logger()

	leaseCtx, cancelLease := context.WithTimeout(ctx, 5*time.Second)
	acquired, err := w.coordClient.AcquireTaskLease(leaseCtx, a.TaskId, w.cfg.WorkerID, w.cfg.LeaseTTLSeconds)
	cancelLease()
	if err != nil {
		logger.Error().Err(err).Msg("failed to acquire task lease")
		return err
	}
	if !acquired {
		logger.Warn().Msg("task lease not acquired (already running or completed), skipping")
		return nil
	}

	renewCtx, stopRenewal := context.WithCancel(ctx)
	go w.renewLeaseLoop(renewCtx, a.TaskId, logger)

	execCtx := executors.WithTaskID(ctx, a.TaskId)

	var execErr *string
	start := time.Now()
	execOutput, routeErr := w.router.Route(execCtx, a.NodeType, a.Input)
	if routeErr != nil {
		errStr := routeErr.Error()
		execErr = &errStr
	}

	stopRenewal()

	logger.Info().
		Dur("duration", time.Since(start)).
		Bool("success", execErr == nil).
		Msg("task executed")

	if err := w.coordClient.ReportTaskResult(ctx,
		a.TaskId, a.RunId, a.NodeId, a.WorkflowId, int(a.WorkflowVersion),
		execOutput, execErr,
	); err != nil {
		logger.Error().Err(err).Msg("failed to report task result")
		return fmt.Errorf("failed to report result: %w", err)
	}

	logger.Info().Msg("task result reported")
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
			acquired, err := w.coordClient.AcquireTaskLease(renewCtx, taskID, w.cfg.WorkerID, w.cfg.LeaseTTLSeconds)
			cancel()
			if err != nil {
				logger.Warn().Err(err).Str("task_id", taskID).Msg("failed to renew task lease")
				continue
			}
			if !acquired {
				logger.Warn().Str("task_id", taskID).Msg("lease renewal failed: task no longer leasable")
				return
			}
			logger.Debug().Str("task_id", taskID).Msg("task lease renewed")
		}
	}
}
