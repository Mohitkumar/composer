package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB defines the interface for database operations.
// This allows for easier testing and potential future database implementations.
type DB interface {
	// Begin starts a new transaction
	Begin(ctx context.Context) (pgx.Tx, error)
	// Query executes a query that returns rows
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	// QueryRow executes a query that returns a single row
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	// Exec executes a query without returning rows
	Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
	// Acquire gets a connection from the pool
	Acquire(ctx context.Context) (*pgxpool.Conn, error)
	// Close closes the database connection pool
	Close()
	// Pool returns the underlying pgxpool.Pool for cases where direct access is needed
	Pool() *pgxpool.Pool
}

// Querier is a subset of DB interface for query operations.
// This is useful for accepting both DB and Tx types.
type Querier interface {
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
}
