package coord

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// txn is a transaction plus side effects (metrics, logs) that must only
// happen if it commits.
type txn struct {
	pgx.Tx
	afterCommit []func()
}

func (t *txn) onCommit(fn func()) {
	t.afterCommit = append(t.afterCommit, fn)
}

// inTx runs fn in a transaction and commits it if fn succeeds.
func (c *Coordinator) inTx(ctx context.Context, fn func(t *txn) error) error {
	tx, err := c.DB.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	t := &txn{Tx: tx}
	if err := fn(t); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	for _, f := range t.afterCommit {
		f()
	}
	return nil
}
