package coord

import (
	"context"
	"fmt"

	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/intelligence"
	"github.com/mohitkumar/composer/pkg/lease"
	"github.com/mohitkumar/composer/pkg/outbox"
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/mohitkumar/composer/pkg/template"
	"github.com/rs/zerolog"
)

type Coordinator struct {
	Config       *Config
	Logger       *zerolog.Logger
	Redis        *redisq.RedisStream
	DB           db.DB
	LeaseManager *lease.Manager
	Outbox       *outbox.OutboxEnqueuer
	Template     *template.Resolver
	Intelligence *intelligence.Client
	Repository   WorkflowRepository // Repository for database operations
}

func NewCoordinator(config *Config, logger *zerolog.Logger) (*Coordinator, error) {
	// Create Redis stream
	redisStream, err := redisq.NewRedisStream(&config.RedisConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create redis stream: %w", err)
	}

	// Create database connection pool
	dbPool, err := db.NewPool(context.Background(), &config.DBConfig, *logger)
	if err != nil {
		return nil, err
	}

	// Create lease manager
	leaseManager := lease.NewManager(dbPool)

	// Create outbox enqueuer
	outboxEnqueuer := outbox.NewOutboxEnqueuer(outbox.EnqueuerConfig{
		DB:           dbPool,
		Redis:        redisStream,
		Logger:       *logger,
		BatchSize:    config.OutboxConfig.BatchSize,
		Concurrency:  config.OutboxConfig.Concurrency,
		PollInterval: config.OutboxConfig.PollInterval,
		StreamPrefix: config.OutboxConfig.StreamPrefix,
	})

	// Create template resolver
	templateResolver, err := template.NewResolver()
	if err != nil {
		dbPool.Close()
		return nil, err
	}

	// Create intelligence client
	intelligenceClient := intelligence.NewClient(config.IntelligenceConfig, config.EnableIntelligence, *logger)

	coordinator := &Coordinator{
		Config:       config,
		Logger:       logger,
		Redis:        redisStream,
		DB:           dbPool,
		LeaseManager: leaseManager,
		Outbox:       outboxEnqueuer,
		Template:     templateResolver,
		Intelligence: intelligenceClient,
	}

	// Create repository
	coordinator.Repository = NewWorkflowRepository(dbPool, coordinator)

	return coordinator, nil
}

func (b *Coordinator) Run(ctx context.Context) error {
	b.Logger.Info().Msg("starting coordinator")
	b.Logger.Info().Msg("starting outbox enqueuer")
	go b.Outbox.Run(ctx)
	return nil
}

func (b *Coordinator) Shutdown() error {
	b.Logger.Info().Msg("stopping outbox enqueuer")
	b.Outbox.Stop()

	b.Logger.Info().Msg("closing connections")
	if b.Redis != nil {
		b.Redis.Close()
	}
	if b.DB != nil {
		b.DB.Close()
	}

	b.Logger.Info().Msg("coordinator shutdown complete")
	return nil
}
