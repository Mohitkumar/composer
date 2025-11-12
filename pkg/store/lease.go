package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AcquireTaskLease attempts to atomically move task state to RUNNING and set a lease owner + expiry.
// Returns true if lease acquired.
func AcquireTaskLease(ctx context.Context, pool *pgxpool.Pool, taskID, owner string, ttl time.Duration) (bool, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()

	// we attempt to update task_instances row where state is PENDING/QUEUED and claim it
	query := `UPDATE task_instances SET state='RUNNING', lease_owner=$1, lease_expires_at=NOW() + $2::interval, started_at=NOW(), updated_at=NOW()
              WHERE id=$3 AND state IN ('PENDING','QUEUED')
              RETURNING id`
	var id string
	err = conn.QueryRow(ctx, query, owner, fmt.Sprintf("%ds", int(ttl.Seconds())), taskID).Scan(&id)
	if err != nil {
		// no row means not acquired OR error
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return id != "", nil
}

// ReleaseTaskLease releases lease and optionally set state back to PENDING or leave as-is if finished externally.
// Here we just clear the lease_owner and leave state as-is (coordinator will update on completion)
func ReleaseTaskLease(ctx context.Context, pool *pgxpool.Pool, taskID, owner string) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	_, err = conn.Exec(ctx, `UPDATE task_instances SET lease_owner=NULL, lease_expires_at=NULL, updated_at=NOW() WHERE id=$1 AND lease_owner=$2`, taskID, owner)
	return err
}
