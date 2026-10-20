package coord

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mohitkumar/composer/pkg/model"
	"github.com/mohitkumar/composer/pkg/statemachine"
	"github.com/mohitkumar/composer/pkg/util"
	"github.com/rs/zerolog"
)

// errStaleSnapshot means a write did not find the row state the machine
// decided on. Under the run lock this cannot happen unless something wrote
// outside the engine; the transaction is rolled back and the event fails.
var errStaleSnapshot = errors.New("run changed under the state machine")

// persister writes one transition's actions inside the run's transaction.
type persister struct {
	c           *Coordinator
	ctx         context.Context
	t           *txn
	logger      zerolog.Logger
	run         *statemachine.Run
	scheduledAt time.Time
}

func (p *persister) persist(actions []statemachine.Action) error {
	for _, a := range actions {
		if err := p.apply(a); err != nil {
			return fmt.Errorf("persist %T: %w", a, err)
		}
	}
	return nil
}

func (p *persister) apply(action statemachine.Action) error {
	repo, ctx, q, runID := p.c.repo, p.ctx, p.t, p.run.ID

	switch a := action.(type) {
	case statemachine.CreateTask:
		inserted, err := repo.InsertTask(ctx, q, runID, a, p.scheduledAt)
		if err != nil {
			return err
		}
		if !inserted {
			return errStaleSnapshot
		}
		if a.Status != model.TaskInstanceStatusSkipped {
			nodeType := a.Node.GetNodeTypeForRouting()
			p.t.onCommit(func() { util.RecordTaskCreated(ctx, a.TaskID, runID, a.Node.NodeID, nodeType) })
		}
		return nil

	case statemachine.DispatchTask:
		return repo.EnqueueDispatch(ctx, q, p.dispatch(a.TaskID, a.Node.NodeID, a.Node.GetNodeTypeForRouting(),
			a.Node.Queue(), a.Attempt, a.Input), a.NotBefore)

	case statemachine.CompleteTask:
		task, ok, err := repo.CompleteTask(ctx, q, p.ref(a.TaskID, a.NodeID, a.Attempt), a.Output)
		if err != nil || !ok {
			return orStale(err)
		}
		p.t.onCommit(func() {
			util.RecordTaskCompleted(ctx, a.TaskID, runID, a.NodeID, task.NodeType, sinceOrZero(task.StartedAt))
		})
		return nil

	case statemachine.FailTask:
		task, ok, err := repo.FailTask(ctx, q, p.ref(a.TaskID, a.NodeID, a.Attempt), a.Reason)
		if err != nil || !ok {
			return orStale(err)
		}
		p.t.onCommit(func() {
			util.RecordTaskFailed(ctx, a.TaskID, runID, a.NodeID, task.NodeType, sinceOrZero(task.StartedAt), a.Reason)
			p.logger.Warn().Str("node_id", a.NodeID).Str("reason", a.Reason).Msg("task failed")
		})
		return nil

	case statemachine.RetryTask:
		d, ok, err := repo.RequeueTask(ctx, q, p.ref(a.TaskID, a.NodeID, a.Attempt), a.Reason)
		if err != nil || !ok {
			return orStale(err)
		}
		d.WorkflowID, d.WorkflowVersion = p.run.WorkflowID, p.run.WorkflowVersion
		p.t.onCommit(func() {
			p.logger.Warn().Str("node_id", a.NodeID).Int("next_attempt", d.Attempt).
				Time("not_before", a.NotBefore).Msg("task failed, retry scheduled")
		})
		return repo.EnqueueDispatch(ctx, q, d, a.NotBefore)

	case statemachine.ResolveWait:
		ok, err := repo.ResolveWait(ctx, q, runID, a)
		if err != nil || !ok {
			return orStale(err)
		}
		return nil

	case statemachine.BufferSignal:
		return repo.BufferSignal(ctx, q, runID, a)

	case statemachine.ConsumeSignal:
		return repo.ConsumeSignal(ctx, q, runID, a.SignalID)

	case statemachine.ReopenRun:
		if err := repo.ResetRunForRetry(ctx, q, runID, a.Version, a.Context.Input); err != nil {
			return err
		}
		if err := repo.SaveRunContext(ctx, q, runID, a.Context); err != nil {
			return err
		}
		return repo.DeleteTasks(ctx, q, runID, a.ResetNodes)

	case statemachine.CancelTasks:
		return repo.CancelRunTasks(ctx, q, runID, a.Reason)

	case statemachine.FinishRun:
		duration, changed, err := repo.FinishRun(ctx, q, runID, a.Status, a.Reason)
		if err != nil || !changed {
			return orStale(err)
		}
		workflowID := p.run.WorkflowID
		p.t.onCommit(func() {
			if a.Status == model.WorkflowInstanceStatusFailed {
				util.RecordWorkflowFailed(ctx, workflowID, runID, duration, a.Reason)
				p.logger.Error().Str("reason", a.Reason).Msg("workflow failed")
			} else {
				util.RecordWorkflowCompleted(ctx, workflowID, runID, duration)
				p.logger.Info().Dur("duration", duration).Msg("workflow succeeded")
			}
		})
		return nil

	default:
		return fmt.Errorf("unknown action %T", action)
	}
}

func (p *persister) ref(taskID, nodeID string, attempt int) taskRef {
	return taskRef{TaskID: taskID, RunID: p.run.ID, NodeID: nodeID, Attempt: attempt}
}

func (p *persister) dispatch(taskID, nodeID, nodeType, queue string, attempt int, input []byte) *taskDispatch {
	return &taskDispatch{
		TaskID:          taskID,
		RunID:           p.run.ID,
		WorkflowID:      p.run.WorkflowID,
		WorkflowVersion: p.run.WorkflowVersion,
		NodeID:          nodeID,
		NodeType:        nodeType,
		Queue:           queue,
		Attempt:         attempt,
		Input:           input,
	}
}

// orStale turns "no row matched" (err == nil) into errStaleSnapshot.
func orStale(err error) error {
	if err == nil {
		return errStaleSnapshot
	}
	return err
}

func sinceOrZero(t time.Time) time.Duration {
	if t.IsZero() {
		return 0
	}
	return time.Since(t)
}
