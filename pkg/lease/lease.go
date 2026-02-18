package lease

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mohitkumar/composer/pkg/db"
)

// Manager handles task lease operations
type Manager struct {
	db db.DB
}

// NewManager creates a new lease manager
func NewManager(db db.DB) *Manager {
	return &Manager{db: db}
}

// AcquireTaskLease attempts to atomically move task state to RUNNING and set a lease owner + expiry.
// For new acquisitions: claims tasks in PENDING/QUEUED state.
// For renewals: extends the lease for tasks already RUNNING with the same owner.
// Returns true if lease acquired or renewed.
func (m *Manager) AcquireTaskLease(ctx context.Context, taskID, owner string, ttl time.Duration) (bool, error) {
	conn, err := m.db.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()

	interval := fmt.Sprintf("%ds", int(ttl.Seconds()))

	// First try: acquire a new lease (PENDING/QUEUED -> RUNNING)
	query := `UPDATE task_instances SET state='RUNNING', lease_owner=$1, lease_expires_at=NOW() + $2::interval, started_at=NOW(), updated_at=NOW()
              WHERE id=$3 AND state IN ('PENDING','QUEUED')
              RETURNING id`
	var id string
	err = conn.QueryRow(ctx, query, owner, interval, taskID).Scan(&id)
	if err == nil && id != "" {
		return true, nil
	}
	if err != nil && err != pgx.ErrNoRows {
		return false, err
	}

	// Second try: renew an existing lease (RUNNING with same owner)
	renewQuery := `UPDATE task_instances SET lease_expires_at=NOW() + $1::interval, updated_at=NOW()
              WHERE id=$2 AND state='RUNNING' AND lease_owner=$3
              RETURNING id`
	err = conn.QueryRow(ctx, renewQuery, interval, taskID, owner).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return id != "", nil
}

// ReleaseTaskLease releases lease and optionally set state back to PENDING or leave as-is if finished externally.
// Here we just clear the lease_owner and leave state as-is (coordinator will update on completion)
func (m *Manager) ReleaseTaskLease(ctx context.Context, taskID, owner string) error {
	conn, err := m.db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	_, err = conn.Exec(ctx, `UPDATE task_instances SET lease_owner=NULL, lease_expires_at=NULL, updated_at=NOW() WHERE id=$1 AND lease_owner=$2`, taskID, owner)
	return err
}
