package tests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	redistestcontainers "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestInfra holds the test infrastructure (containers, connections, etc.)
type TestInfra struct {
	PostgresContainer testcontainers.Container
	RedisContainer    testcontainers.Container
	DBPool            db.DB
	DBPoolRaw         *pgxpool.Pool // For direct access when needed
	RedisClient       *redis.Client
	RedisStream       *redisq.RedisStream
	DBURL             string
	RedisAddr         string
	Logger            zerolog.Logger
}

// SetupTestInfra sets up PostgreSQL and Redis containers, runs migrations, and returns test infrastructure
func SetupTestInfra(ctx context.Context, t *testing.T) *TestInfra {
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger().Level(zerolog.DebugLevel)

	// Start PostgreSQL container
	t.Log("Starting PostgreSQL container...")
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("composer"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("Failed to start PostgreSQL container: %v", err)
	}

	// Get PostgreSQL connection string
	dbURL, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get PostgreSQL connection string: %v", err)
	}

	// Start Redis container
	t.Log("Starting Redis container...")
	redisContainer, err := redistestcontainers.Run(ctx,
		"redis:7-alpine",
		testcontainers.WithWaitStrategy(
			wait.ForLog("Ready to accept connections").
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("Failed to start Redis container: %v", err)
	}

	// Get Redis connection string
	redisEndpoint, err := redisContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("Failed to get Redis connection string: %v", err)
	}
	// Extract host:port from connection string (format: redis://localhost:port)
	redisAddr := strings.TrimPrefix(redisEndpoint, "redis://")

	// Create database pool
	t.Log("Connecting to PostgreSQL...")
	poolRaw, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}

	// Wrap in db.Pool for interface compatibility
	dbConfig := &db.Config{URL: dbURL}
	dbPool, err := db.NewPool(ctx, dbConfig, logger)
	if err != nil {
		poolRaw.Close()
		t.Fatalf("Failed to create db pool: %v", err)
	}

	// Run migrations
	t.Log("Running database migrations...")
	if err := runMigrations(ctx, poolRaw); err != nil {
		poolRaw.Close()
		dbPool.Close()
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Create Redis client
	t.Log("Connecting to Redis...")
	redisClient := redis.NewClient(&redis.Options{
		Addr: redisAddr,
		DB:   0,
		// DisableIndentity prevents the maint_notifications warning
		// This is safe to use and suppresses the harmless warning from go-redis
		DisableIndentity: true,
	})

	// Test Redis connection
	err = redisClient.Ping(ctx).Err()
	if err != nil {
		t.Fatalf("Failed to connect to Redis: %v", err)
	}

	// Create Redis stream
	redisStream, err := redisq.NewRedisStream(&redisq.RedisConfig{
		Addrs:    []string{redisAddr},
		Username: "",
		Password: "",
		DB:       0,
	})
	if err != nil {
		t.Fatalf("Failed to create Redis stream: %v", err)
	}

	return &TestInfra{
		PostgresContainer: pgContainer,
		RedisContainer:    redisContainer,
		DBPool:            dbPool,
		DBPoolRaw:         poolRaw,
		RedisClient:       redisClient,
		RedisStream:       redisStream,
		DBURL:             dbURL,
		RedisAddr:         redisAddr,
		Logger:            logger,
	}
}

// Cleanup terminates containers and closes connections
func (ti *TestInfra) Cleanup(ctx context.Context, t *testing.T) {
	t.Log("Cleaning up test infrastructure...")

	if ti.RedisStream != nil {
		ti.RedisStream.Close()
	}

	if ti.RedisClient != nil {
		ti.RedisClient.Close()
	}

	if ti.DBPool != nil {
		ti.DBPool.Close()
	}

	if ti.RedisContainer != nil {
		if err := ti.RedisContainer.Terminate(ctx); err != nil {
			t.Logf("Failed to terminate Redis container: %v", err)
		}
	}

	if ti.PostgresContainer != nil {
		if err := ti.PostgresContainer.Terminate(ctx); err != nil {
			t.Logf("Failed to terminate PostgreSQL container: %v", err)
		}
	}
}

// runMigrations executes all SQL migration files in the migrations directory in order
func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	// Find migrations directory relative to project root
	// When running tests, working directory is typically project root
	// Try current directory first, then go up one level if in tests/
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	var migrationsDir string
	// Check if migrations exists in current directory
	if _, err := os.Stat(filepath.Join(wd, "migrations")); err == nil {
		migrationsDir = filepath.Join(wd, "migrations")
	} else if _, err := os.Stat(filepath.Join(wd, "..", "migrations")); err == nil {
		// If in tests/ directory, go up one level
		migrationsDir = filepath.Join(wd, "..", "migrations")
	} else {
		return fmt.Errorf("migrations directory not found (searched in %s and %s)", wd, filepath.Join(wd, ".."))
	}

	// Read all files in migrations directory
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	// Filter and collect SQL migration files
	var migrationFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			migrationFiles = append(migrationFiles, entry.Name())
		}
	}

	// Sort migration files by name (ensures V001, V002, V003 order)
	sort.Strings(migrationFiles)

	if len(migrationFiles) == 0 {
		return fmt.Errorf("no migration files found in %s", migrationsDir)
	}

	// Execute each migration file in order
	for _, migrationFile := range migrationFiles {
		migrationPath := filepath.Join(migrationsDir, migrationFile)

		// Read migration file
		migrationSQL, err := os.ReadFile(migrationPath)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", migrationPath, err)
		}

		// Execute migration
		_, err = pool.Exec(ctx, string(migrationSQL))
		if err != nil {
			return fmt.Errorf("failed to execute migration %s: %w", migrationPath, err)
		}
	}

	return nil
}
