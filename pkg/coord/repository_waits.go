package coord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/model"
)

// dueWait is a WAITING task whose timer fired or whose event wait timed out.
type dueWait struct {
	RunID    string
	TaskID   string
	NodeID   string
	NodeType model.NodeType
}

// DueWaits returns up to limit WAITING tasks whose wake_at has passed, oldest first.
func (repository) DueWaits(ctx context.Context, q db.Querier, limit int) ([]dueWait, error) {
	rows, err := q.Query(ctx, `
		SELECT run_id, id, node_id, node_type
		  FROM task_instances
		 WHERE state = 'WAITING' AND wake_at <= NOW()
		 ORDER BY wake_at
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query due waits: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (dueWait, error) {
		var w dueWait
		err := row.Scan(&w.RunID, &w.TaskID, &w.NodeID, &w.NodeType)
		return w, err
	})
}

// FindEventWaiter returns the oldest task of the run waiting for the event.
func (repository) FindEventWaiter(ctx context.Context, q db.Querier, runID, event string) (taskID, nodeID string, ok bool, err error) {
	err = q.QueryRow(ctx, `
		SELECT id, node_id FROM task_instances
		 WHERE run_id = $1 AND state = 'WAITING' AND event_name = $2
		 ORDER BY created_at
		 LIMIT 1`,
		runID, event).Scan(&taskID, &nodeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("find event waiter: %w", err)
	}
	return taskID, nodeID, true, nil
}

// ResolveWait moves a WAITING task to COMPLETED (with output) or FAILED (with
// reason). It reports false if the task was no longer waiting.
func (repository) ResolveWait(ctx context.Context, q db.Querier, runID, taskID string,
	status model.TaskInstanceStatus, output json.RawMessage, reason string) (bool, error) {

	var lastError []byte
	if reason != "" {
		lastError, _ = json.Marshal(map[string]string{"error": reason})
	}
	tag, err := q.Exec(ctx, `
		UPDATE task_instances
		   SET state = $1, output_json = $2::jsonb, last_error = $3::jsonb,
		       finished_at = NOW(), updated_at = NOW(),
		       completed_at = CASE WHEN $1 = 'COMPLETED' THEN NOW() END
		 WHERE run_id = $4 AND id = $5 AND state = 'WAITING'`,
		status, nullJSON(output), lastError, runID, taskID)
	if err != nil {
		return false, fmt.Errorf("resolve wait: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// InsertEvent buffers an event for a run until a WAIT_FOR_EVENT node consumes it.
func (repository) InsertEvent(ctx context.Context, q db.Querier, runID, name string, payload json.RawMessage) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	if _, err := q.Exec(ctx,
		`INSERT INTO workflow_events (id, run_id, name, payload) VALUES ($1, $2, $3, $4::jsonb)`,
		id.String(), runID, name, nullJSON(payload)); err != nil {
		return fmt.Errorf("insert workflow event: %w", err)
	}
	return nil
}

// ConsumeEvent marks the oldest buffered event of that name consumed and
// returns its payload.
func (repository) ConsumeEvent(ctx context.Context, q db.Querier, runID, name string) (json.RawMessage, bool, error) {
	var payload json.RawMessage
	err := q.QueryRow(ctx, `
		UPDATE workflow_events SET consumed_at = NOW()
		 WHERE run_id = $1 AND id = (
		       SELECT id FROM workflow_events
		        WHERE run_id = $1 AND name = $2 AND consumed_at IS NULL
		        ORDER BY created_at
		        LIMIT 1)
		RETURNING payload`,
		runID, name).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("consume workflow event: %w", err)
	}
	return payload, true, nil
}
