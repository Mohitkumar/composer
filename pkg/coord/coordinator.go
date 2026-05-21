package coord

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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

	// last-seen cumulative values for cache hit/miss delta counters
	lastCacheHits   uint64
	lastCacheMisses uint64
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

	go b.metricsSamplerLoop(ctx)
	return nil
}

// metricsSamplerLoop periodically records all coordinator-side OTel gauges.
func (b *Coordinator) metricsSamplerLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.sampleMetrics(ctx)
		}
	}
}

func (b *Coordinator) sampleMetrics(ctx context.Context) {
	// Redis stream depth
	if util.RedisStreamDepthGauge != nil {
		if n, err := b.Redis.Client().XLen(ctx, b.Config.OutboxConfig.StreamPrefix+"tasks").Result(); err == nil {
			util.RedisStreamDepthGauge.Record(ctx, n)
		}
	}

	// Outbox backlog
	if util.OutboxBacklogGauge != nil {
		var count int64
		if err := b.DB.QueryRow(ctx,
			`SELECT COUNT(*) FROM outbox WHERE status IN ('PENDING','RETRY','IN_FLIGHT')`).Scan(&count); err == nil {
			util.OutboxBacklogGauge.Record(ctx, count)
		}
	}

	// DB connection pool stats
	if pool := b.DB.Pool(); pool != nil {
		stat := pool.Stat()
		if util.DBPoolAcquiredGauge != nil {
			util.DBPoolAcquiredGauge.Record(ctx, int64(stat.AcquiredConns()))
		}
		if util.DBPoolIdleGauge != nil {
			util.DBPoolIdleGauge.Record(ctx, int64(stat.IdleConns()))
		}
		if util.DBPoolTotalGauge != nil {
			util.DBPoolTotalGauge.Record(ctx, int64(stat.TotalConns()))
		}
		if util.DBPoolMaxGauge != nil {
			util.DBPoolMaxGauge.Record(ctx, int64(stat.MaxConns()))
		}
		if util.DBPoolWaitCountGauge != nil {
			util.DBPoolWaitCountGauge.Record(ctx, stat.EmptyAcquireCount())
		}
	}

	// Redis server memory
	if util.RedisMemoryUsedGauge != nil {
		if info, err := b.Redis.Client().Info(ctx, "memory").Result(); err == nil {
			for _, line := range strings.Split(info, "\r\n") {
				if strings.HasPrefix(line, "used_memory:") {
					if v, err := strconv.ParseInt(strings.TrimPrefix(line, "used_memory:"), 10, 64); err == nil {
						util.RedisMemoryUsedGauge.Record(ctx, v)
					}
					break
				}
			}
		}
	}

	// Workflow DAG cache
	if b.wfCache != nil {
		if util.WfCacheSizeGauge != nil {
			util.WfCacheSizeGauge.Record(ctx, int64(b.wfCache.c.Len()))
		}
		stats := b.wfCache.c.Stats()
		if util.WfCacheHitsCounter != nil {
			if delta := int64(stats.Hits()) - int64(b.lastCacheHits); delta > 0 {
				util.WfCacheHitsCounter.Add(ctx, delta)
			}
		}
		if util.WfCacheMissesCounter != nil {
			if delta := int64(stats.Misses()) - int64(b.lastCacheMisses); delta > 0 {
				util.WfCacheMissesCounter.Add(ctx, delta)
			}
		}
		b.lastCacheHits = stats.Hits()
		b.lastCacheMisses = stats.Misses()
	}

	// Process metrics
	util.SampleProcessMetrics(ctx)
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
