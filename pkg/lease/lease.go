// Package lease implements the database-backed task lease that guards against
// two workers executing the same task at the same time.
package lease

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mohitkumar/composer/pkg/db"
)

// Manager handles task lease operations.
type Manager struct {
	db db.DB
}

// NewManager creates a new lease manager.
func NewManager(db db.DB) *Manager {
	return &Manager{db: db}
}

// Acquire claims a task for owner and moves it to RUNNING. It succeeds when the
// task is waiting to run, or when it is RUNNING under a lease that has expired
// (the previous worker died). An owner never re-acquires a lease it still
// holds, so a redelivered message cannot start a second concurrent execution.
func (m *Manager) Acquire(ctx context.Context, taskID, owner string, ttl time.Duration) (bool, error) {
	return m.exec(ctx, `
		UPDATE task_instances
		   SET state = 'RUNNING',
		       lease_owner = $1,
		       lease_expires_at = NOW() + make_interval(secs => $2),
		       started_at = NOW(),
		       updated_at = NOW()
		 WHERE id = $3
		   AND (state IN ('PENDING', 'QUEUED')
		        OR (state = 'RUNNING' AND lease_expires_at < NOW()))
		RETURNING id`,
		owner, ttl.Seconds(), taskID)
}

// Renew extends a lease that owner currently holds. It returns false once the
// lease was lost (expired and taken over, or the task finished), which tells
// the worker to abandon the execution.
func (m *Manager) Renew(ctx context.Context, taskID, owner string, ttl time.Duration) (bool, error) {
	return m.exec(ctx, `
		UPDATE task_instances
		   SET lease_expires_at = NOW() + make_interval(secs => $1),
		       updated_at = NOW()
		 WHERE id = $2 AND state = 'RUNNING' AND lease_owner = $3
		RETURNING id`,
		ttl.Seconds(), taskID, owner)
}

func (m *Manager) exec(ctx context.Context, query string, args ...any) (bool, error) {
	var id string
	err := m.db.QueryRow(ctx, query, args...).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("task lease: %w", err)
	}
	return true, nil
}
