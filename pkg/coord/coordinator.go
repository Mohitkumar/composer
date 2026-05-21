package coord

import (
	"context"
	"fmt"
	"time"

	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/lease"
	"github.com/mohitkumar/composer/pkg/outbox"
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/mohitkumar/composer/pkg/template"
	"github.com/mohitkumar/composer/pkg/util"
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
	Repository   WorkflowRepository // Repository for database operations
	wfCache      *workflowCache
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

	wfCache, err := newWorkflowCache(defaultCacheTTL)
	if err != nil {
		dbPool.Close()
		return nil, fmt.Errorf("failed to create workflow cache: %w", err)
	}

	coordinator := &Coordinator{
		Config:       config,
		Logger:       logger,
		Redis:        redisStream,
		DB:           dbPool,
		LeaseManager: leaseManager,
		Outbox:       outboxEnqueuer,
		Template:     templateResolver,
		wfCache:      wfCache,
	}

	// Create repository
	coordinator.Repository = NewWorkflowRepository(dbPool, coordinator)

	return coordinator, nil
}

func (b *Coordinator) Run(ctx context.Context) error {
	b.Logger.Info().Msg("starting coordinator")

	// Recover any tasks orphaned by a previous coordinator crash before starting
	// the outbox enqueuer, so recovered outbox entries are picked up in the first poll.
	if err := b.Repository.RecoverOrphanedTasks(ctx, *b.Logger); err != nil {
		b.Logger.Error().Err(err).Msg("startup recovery failed — continuing anyway")
	}

	b.Logger.Info().Msg("starting outbox enqueuer")
	go b.Outbox.Run(ctx)

	go b.depthSamplerLoop(ctx)
	return nil
}

// depthSamplerLoop periodically records Redis stream depth and outbox backlog as OTel gauges.
func (b *Coordinator) depthSamplerLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.sampleDepths(ctx)
		}
	}
}

func (b *Coordinator) sampleDepths(ctx context.Context) {
	if util.RedisStreamDepthGauge != nil {
		n, err := b.Redis.Client().XLen(ctx, b.Config.OutboxConfig.StreamPrefix+"tasks").Result()
		if err == nil {
			util.RedisStreamDepthGauge.Record(ctx, n)
		}
	}
	if util.OutboxBacklogGauge != nil {
		var count int64
		err := b.DB.QueryRow(ctx,
			`SELECT COUNT(*) FROM outbox WHERE status IN ('PENDING','RETRY','IN_FLIGHT')`).Scan(&count)
		if err == nil {
			util.OutboxBacklogGauge.Record(ctx, count)
		}
	}
}

func (b *Coordinator) Shutdown() error {
	b.Logger.Info().Msg("stopping outbox enqueuer")
	b.Outbox.Stop()

	b.Logger.Info().Msg("closing connections")
	if b.wfCache != nil {
		b.wfCache.close()
	}
	if b.Redis != nil {
		b.Redis.Close()
	}
	if b.DB != nil {
		b.DB.Close()
	}

	b.Logger.Info().Msg("coordinator shutdown complete")
	return nil
}
