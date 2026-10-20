package coord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/statemachine"
)

// dueWait is a WAITING task whose timer fired or whose event wait timed out.
type dueWait struct {
	RunID  string
	TaskID string
	NodeID string
}

// DueWaits returns up to limit WAITING tasks whose wake_at has passed, oldest first.
func (repository) DueWaits(ctx context.Context, q db.Querier, limit int) ([]dueWait, error) {
	rows, err := q.Query(ctx, `
		SELECT run_id, id, node_id
		  FROM task_instances
		 WHERE state = 'WAITING' AND wake_at <= NOW()
		 ORDER BY wake_at
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query due waits: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (dueWait, error) {
		var w dueWait
		err := row.Scan(&w.RunID, &w.TaskID, &w.NodeID)
		return w, err
	})
}

// ResolveWait moves a WAITING task to COMPLETED (with output) or FAILED (with
// reason). It reports false if the task was no longer waiting.
func (repository) ResolveWait(ctx context.Context, q db.Querier, runID string, a statemachine.ResolveWait) (bool, error) {
	var lastError []byte
	if a.Reason != "" {
		lastError, _ = json.Marshal(map[string]string{"error": a.Reason})
	}
	tag, err := q.Exec(ctx, `
		UPDATE task_instances
		   SET state = $1, output_json = $2::jsonb, last_error = $3::jsonb,
		       finished_at = NOW(), updated_at = NOW(),
		       completed_at = CASE WHEN $1 = 'COMPLETED' THEN NOW() END
		 WHERE run_id = $4 AND id = $5 AND state = 'WAITING'`,
		a.Status, nullJSON(a.Output), lastError, runID, a.TaskID)
	if err != nil {
		return false, fmt.Errorf("resolve wait: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// BufferSignal stores a signal until a WAIT_FOR_EVENT node consumes it.
func (repository) BufferSignal(ctx context.Context, q db.Querier, runID string, a statemachine.BufferSignal) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	if _, err := q.Exec(ctx,
		`INSERT INTO workflow_events (id, run_id, name, payload) VALUES ($1, $2, $3, $4::jsonb)`,
		id.String(), runID, a.Name, nullJSON(a.Payload)); err != nil {
		return fmt.Errorf("buffer signal: %w", err)
	}
	return nil
}

// PendingSignal returns the oldest unconsumed signal of that name, or nil.
func (repository) PendingSignal(ctx context.Context, q db.Querier, runID, name string) (*statemachine.Signal, error) {
	s := &statemachine.Signal{}
	err := q.QueryRow(ctx, `
		SELECT id, payload FROM workflow_events
		 WHERE run_id = $1 AND name = $2 AND consumed_at IS NULL
		 ORDER BY created_at
		 LIMIT 1`,
		runID, name).Scan(&s.ID, &s.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query pending signal: %w", err)
	}
	return s, nil
}

// ConsumeSignal marks a buffered signal as used.
func (repository) ConsumeSignal(ctx context.Context, q db.Querier, runID, signalID string) error {
	if _, err := q.Exec(ctx,
		`UPDATE workflow_events SET consumed_at = NOW() WHERE run_id = $1 AND id = $2`,
		runID, signalID); err != nil {
		return fmt.Errorf("consume signal: %w", err)
	}
	return nil
}
