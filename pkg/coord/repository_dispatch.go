package coord

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/stream"
)

// taskDispatch is the message a worker receives for one task attempt.
type taskDispatch struct {
	TaskID          string
	RunID           string
	WorkflowID      string
	WorkflowVersion int
	NodeID          string
	NodeType        string
	Queue           string
	Attempt         int
	Input           json.RawMessage
}

// EnqueueDispatch writes the task's dispatch message to the outbox, addressed
// to one shard of the task's queue. The outbox enqueuer publishes it no
// earlier than notBefore.
func (r repository) EnqueueDispatch(ctx context.Context, q db.Querier, d *taskDispatch, notBefore time.Time) error {
	payload, err := json.Marshal(map[string]any{
		"task_id":          d.TaskID,
		"run_id":           d.RunID,
		"workflow_id":      d.WorkflowID,
		"workflow_version": d.WorkflowVersion,
		"node_id":          d.NodeID,
		"node_type":        d.NodeType,
		"attempt":          d.Attempt,
		"input":            string(d.Input),
	})
	if err != nil {
		return fmt.Errorf("marshal dispatch payload: %w", err)
	}
	streamName := stream.TaskStream(d.Queue, stream.ShardFor(d.TaskID, r.streamShards))
	if _, err := q.Exec(ctx, `
		INSERT INTO outbox (id, task_id, stream, payload, type, status, next_attempt_at)
		VALUES ($1, $2, $3, $4::jsonb, 'TASK_EXECUTION', 'PENDING', $5)`,
		uuid.NewString(), d.TaskID, streamName, payload, notBefore); err != nil {
		return fmt.Errorf("insert outbox entry for task %s: %w", d.TaskID, err)
	}
	return nil
}

// dispatchInsert is the outbox INSERT … SELECT shared by the recovery
// statements below; they supply FROM with task rows "t" and their run "r",
// and the shard count as parameter $1.
const dispatchInsert = `
	INSERT INTO outbox (id, task_id, stream, payload, type, status, next_attempt_at)
	SELECT gen_random_uuid()::text, t.id,
	       '` + stream.TaskStreamPrefix + `' || COALESCE(t.queue, lower(t.node_type)) || '.'
	           || (((hashtextextended(t.id, 0) % $1) + $1) % $1)::text,
	       jsonb_build_object(
	           'task_id', t.id, 'run_id', t.run_id,
	           'workflow_id', r.workflow_definition_id, 'workflow_version', r.workflow_version,
	           'node_id', t.node_id, 'node_type', t.node_type,
	           'attempt', t.attempt, 'input', t.input_json::text),
	       'TASK_EXECUTION', 'PENDING', NOW()`

// RequeueExpiredLeases resets RUNNING tasks whose lease expired — their worker
// died or lost contact — and re-dispatches them. Safe to run concurrently from
// several coordinators: each task row is claimed by exactly one UPDATE.
func (r repository) RequeueExpiredLeases(ctx context.Context, q db.Querier) (int64, error) {
	tag, err := q.Exec(ctx, `
		WITH expired AS (
		    UPDATE task_instances
		       SET state = 'PENDING', lease_owner = NULL, lease_expires_at = NULL,
		           started_at = NULL, updated_at = NOW()
		     WHERE state = 'RUNNING' AND (lease_expires_at IS NULL OR lease_expires_at < NOW())
		    RETURNING id, run_id, node_id, node_type, queue, attempt, input_json
		)`+dispatchInsert+`
		  FROM expired t JOIN workflow_runs r ON r.id = t.run_id`,
		r.shards())
	if err != nil {
		return 0, fmt.Errorf("requeue expired leases: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RedispatchPendingTasks re-dispatches PENDING tasks of running workflows that
// have no outbox entry and were not touched for olderThan — their stream
// message was lost. Touching updated_at limits each task to one re-dispatch per
// interval; a task still queued in the stream may run twice, which the lease
// deduplicates.
func (r repository) RedispatchPendingTasks(ctx context.Context, q db.Querier, olderThan time.Duration) (int64, error) {
	tag, err := q.Exec(ctx, `
		WITH lost AS (
		    UPDATE task_instances t
		       SET updated_at = NOW()
		      FROM workflow_runs r
		     WHERE r.id = t.run_id
		       AND r.status = 'RUNNING'
		       AND t.state = 'PENDING'
		       AND t.updated_at < NOW() - make_interval(secs => $2)
		       AND NOT EXISTS (SELECT 1 FROM outbox o WHERE o.task_id = t.id)
		    RETURNING t.id, t.run_id, t.node_id, t.node_type, t.queue, t.attempt, t.input_json
		)`+dispatchInsert+`
		  FROM lost t JOIN workflow_runs r ON r.id = t.run_id`,
		r.shards(), olderThan.Seconds())
	if err != nil {
		return 0, fmt.Errorf("redispatch pending tasks: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r repository) shards() int {
	return max(r.streamShards, 1)
}
