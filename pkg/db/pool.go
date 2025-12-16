package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// Pool wraps pgxpool.Pool to implement the DB interface
type Pool struct {
	pool *pgxpool.Pool
	log  zerolog.Logger
}

// NewPool creates a new database connection pool
func NewPool(ctx context.Context, config *Config, log zerolog.Logger) (*Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(config.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	// Apply connection pool settings
	if config.MaxConns > 0 {
		poolConfig.MaxConns = int32(config.MaxConns)
	}
	// Note: pgxpool doesn't have MaxIdleConns, it manages connections automatically
	// We can set MinConns if needed
	if config.MaxConnLifetime > 0 {
		poolConfig.MaxConnLifetime = config.MaxConnLifetime
	}
	if config.MaxConnIdleTime > 0 {
		poolConfig.MaxConnIdleTime = config.MaxConnIdleTime
	}
	if config.MaxConnTimeout > 0 {
		poolConfig.ConnConfig.ConnectTimeout = config.MaxConnTimeout
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Info().Msg("database connection pool created successfully")

	return &Pool{
		pool: pool,
		log:  log,
	}, nil
}

// Begin starts a new transaction
func (p *Pool) Begin(ctx context.Context) (pgx.Tx, error) {
	return p.pool.Begin(ctx)
}

// Query executes a query that returns rows
func (p *Pool) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	return p.pool.Query(ctx, sql, args...)
}

// QueryRow executes a query that returns a single row
func (p *Pool) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	return p.pool.QueryRow(ctx, sql, args...)
}

// Exec executes a query without returning rows
func (p *Pool) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	return p.pool.Exec(ctx, sql, args...)
}

// Acquire gets a connection from the pool
func (p *Pool) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	return p.pool.Acquire(ctx)
}

// Close closes the database connection pool
func (p *Pool) Close() {
	p.pool.Close()
	p.log.Info().Msg("database connection pool closed")
}

// Pool returns the underlying pgxpool.Pool for cases where direct access is needed
func (p *Pool) Pool() *pgxpool.Pool {
	return p.pool
}
