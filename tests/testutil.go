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
	"github.com/rs/zerolog"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestInfra holds the test infrastructure (containers, connections, etc.)
type TestInfra struct {
	PostgresContainer testcontainers.Container
	DBPool            db.DB
	DBPoolRaw         *pgxpool.Pool
	DBURL             string
	Logger            zerolog.Logger
}

// SetupTestInfra sets up a PostgreSQL container, runs migrations, and returns test infrastructure.
func SetupTestInfra(ctx context.Context, t *testing.T) *TestInfra {
	t.Helper()
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger().Level(zerolog.DebugLevel)

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

	dbURL, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get PostgreSQL connection string: %v", err)
	}

	t.Log("Connecting to PostgreSQL...")
	poolRaw, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}

	dbPool, err := db.NewPool(ctx, &db.Config{URL: dbURL}, logger)
	if err != nil {
		poolRaw.Close()
		t.Fatalf("Failed to create db pool: %v", err)
	}

	t.Log("Running database migrations...")
	if err := runMigrations(ctx, poolRaw); err != nil {
		poolRaw.Close()
		dbPool.Close()
		t.Fatalf("Failed to run migrations: %v", err)
	}

	return &TestInfra{
		PostgresContainer: pgContainer,
		DBPool:            dbPool,
		DBPoolRaw:         poolRaw,
		DBURL:             dbURL,
		Logger:            logger,
	}
}

// Cleanup terminates containers and closes connections.
func (ti *TestInfra) Cleanup(ctx context.Context, t *testing.T) {
	t.Helper()
	t.Log("Cleaning up test infrastructure...")

	if ti.DBPool != nil {
		ti.DBPool.Close()
	}

	if ti.PostgresContainer != nil {
		if err := ti.PostgresContainer.Terminate(ctx); err != nil {
			t.Logf("Failed to terminate PostgreSQL container: %v", err)
		}
	}
}

func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	var migrationsDir string
	if _, err := os.Stat(filepath.Join(wd, "migrations")); err == nil {
		migrationsDir = filepath.Join(wd, "migrations")
	} else if _, err := os.Stat(filepath.Join(wd, "..", "migrations")); err == nil {
		migrationsDir = filepath.Join(wd, "..", "migrations")
	} else {
		return fmt.Errorf("migrations directory not found (searched %s and parent)", wd)
	}

	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	var migrationFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			migrationFiles = append(migrationFiles, entry.Name())
		}
	}
	sort.Strings(migrationFiles)

	if len(migrationFiles) == 0 {
		return fmt.Errorf("no migration files found in %s", migrationsDir)
	}

	for _, migrationFile := range migrationFiles {
		migrationSQL, err := os.ReadFile(filepath.Join(migrationsDir, migrationFile))
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", migrationFile, err)
		}
		if _, err = pool.Exec(ctx, string(migrationSQL)); err != nil {
			return fmt.Errorf("failed to execute migration %s: %w", migrationFile, err)
		}
	}
	return nil
}
