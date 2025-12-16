package coord

import (
	"context"
	"time"

	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/lease"
	"github.com/mohitkumar/composer/pkg/outbox"
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/rs/zerolog"
)

type Coordinator struct {
	Config            *Config
	Logger            *zerolog.Logger
	Redis             *redisq.RedisStream
	DB                db.DB
	LeaseManager      *lease.Manager
	Outbox            *outbox.OutboxEnqueuer
	ParameterResolver *ParameterResolver
}

func NewCoordinator(config *Config, logger *zerolog.Logger) (*Coordinator, error) {
	// Create Redis stream
	redisStream := redisq.NewRedisStream(&config.RedisConfig)

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
		BatchSize:    50,
		Concurrency:  2,
		PollInterval: 1 * time.Second,
		StreamPrefix: "workflow.",
	})

	// Create parameter resolver
	parameterResolver, err := NewParameterResolver()
	if err != nil {
		dbPool.Close()
		return nil, err
	}

	return &Coordinator{
		Config:            config,
		Logger:            logger,
		Redis:             redisStream,
		DB:                dbPool,
		LeaseManager:      leaseManager,
		Outbox:            outboxEnqueuer,
		ParameterResolver: parameterResolver,
	}, nil
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
