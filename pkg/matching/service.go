package matching

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/rs/zerolog"
)

// Task is a unit of work to be dispatched to a worker.
type Task struct {
	TaskID          string
	RunID           string
	NodeID          string
	NodeType        string
	WorkflowID      string
	WorkflowVersion int
	Input           json.RawMessage
}

type waiter struct {
	workerID string
	ch       chan *Task
}

// Service is the in-memory matching service. It holds a queue of pending tasks
// per task type and a list of workers waiting for tasks. When a task is
// dispatched it is immediately routed to a waiting worker if one is available;
// otherwise it is buffered until a worker calls Poll.
//
// On coordinator startup, all PENDING task_instances are recovered from the
// database and re-enqueued via Dispatch so that they are not lost across
// restarts.
type Service struct {
	mu      sync.Mutex
	pending map[string][]*Task   // nodeType -> []Task
	waiters map[string][]*waiter // nodeType -> []waiter
	logger  zerolog.Logger
}

// NewService creates a new matching service.
func NewService(logger zerolog.Logger) *Service {
	return &Service{
		pending: make(map[string][]*Task),
		waiters: make(map[string][]*waiter),
		logger:  logger,
	}
}

// Dispatch enqueues a task. If a worker is already waiting for this task type
// the task is delivered directly to that worker's channel; otherwise it sits in
// the pending queue until a worker calls Poll.
func (s *Service) Dispatch(task *Task) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ws := s.waiters[task.NodeType]; len(ws) > 0 {
		w := ws[0]
		s.waiters[task.NodeType] = ws[1:]
		select {
		case w.ch <- task:
			s.logger.Debug().
				Str("task_id", task.TaskID).
				Str("worker_id", w.workerID).
				Str("node_type", task.NodeType).
				Msg("dispatched task to waiting worker")
			return
		default:
			// Channel unexpectedly full; restore the waiter and fall through to queue.
			s.waiters[task.NodeType] = append([]*waiter{w}, s.waiters[task.NodeType]...)
		}
	}

	s.pending[task.NodeType] = append(s.pending[task.NodeType], task)
	s.logger.Debug().
		Str("task_id", task.TaskID).
		Str("node_type", task.NodeType).
		Int("queue_depth", len(s.pending[task.NodeType])).
		Msg("task queued (no waiting worker)")
}

// Poll blocks until a task matching one of taskTypes is available, then returns
// it. Callers should set a deadline on ctx (e.g. 30 s) so the RPC times out
// and the worker re-polls; context.DeadlineExceeded is a normal outcome.
func (s *Service) Poll(ctx context.Context, workerID string, taskTypes []string) (*Task, error) {
	s.mu.Lock()

	// Fast path: a task is already waiting.
	for _, tt := range taskTypes {
		if tasks := s.pending[tt]; len(tasks) > 0 {
			task := tasks[0]
			s.pending[tt] = tasks[1:]
			s.mu.Unlock()
			s.logger.Debug().
				Str("task_id", task.TaskID).
				Str("worker_id", workerID).
				Msg("task dispatched immediately (from queue)")
			return task, nil
		}
	}

	// Slow path: register as a waiter for each requested task type.
	ch := make(chan *Task, 1)
	w := &waiter{workerID: workerID, ch: ch}
	for _, tt := range taskTypes {
		s.waiters[tt] = append(s.waiters[tt], w)
	}
	s.mu.Unlock()

	defer s.removeWaiter(w, taskTypes)

	select {
	case task := <-ch:
		return task, nil
	case <-ctx.Done():
		// If Dispatch fired a task into ch just as ctx was cancelled, reclaim
		// it so it isn't dropped.
		select {
		case task := <-ch:
			s.Dispatch(task)
		default:
		}
		return nil, ctx.Err()
	}
}

func (s *Service) removeWaiter(w *waiter, taskTypes []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, tt := range taskTypes {
		ws := s.waiters[tt]
		filtered := ws[:0]
		for _, existing := range ws {
			if existing != w {
				filtered = append(filtered, existing)
			}
		}
		s.waiters[tt] = filtered
	}
}

// QueueDepth returns the number of pending (un-dispatched) tasks per task type.
func (s *Service) QueueDepth() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]int, len(s.pending))
	for tt, tasks := range s.pending {
		result[tt] = len(tasks)
	}
	return result
}
