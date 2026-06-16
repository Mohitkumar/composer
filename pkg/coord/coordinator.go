package coord

import (
	"context"
	"time"

	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/lease"
	"github.com/mohitkumar/composer/pkg/matching"
	"github.com/mohitkumar/composer/pkg/template"
	"github.com/mohitkumar/composer/pkg/util"
	"github.com/rs/zerolog"
)

type Coordinator struct {
	Config         *Config
	Logger         *zerolog.Logger
	DB             db.DB
	LeaseManager   *lease.Manager
	MatchingService *matching.Service
	Template       *template.Resolver
	Repository     WorkflowRepository
	wfCache        *workflowCache

	// last-seen cumulative values for cache hit/miss delta counters
	lastCacheHits   uint64
	lastCacheMisses uint64
}

func NewCoordinator(config *Config, logger *zerolog.Logger) (*Coordinator, error) {
	dbPool, err := db.NewPool(context.Background(), &config.DBConfig, *logger)
	if err != nil {
		return nil, err
	}

	leaseManager := lease.NewManager(dbPool)
	matchingService := matching.NewService(*logger)

	templateResolver, err := template.NewResolver()
	if err != nil {
		dbPool.Close()
		return nil, err
	}

	wfCache, err := newWorkflowCache(defaultCacheTTL)
	if err != nil {
		dbPool.Close()
		return nil, err
	}

	coordinator := &Coordinator{
		Config:          config,
		Logger:          logger,
		DB:              dbPool,
		LeaseManager:    leaseManager,
		MatchingService: matchingService,
		Template:        templateResolver,
		wfCache:         wfCache,
	}

	coordinator.Repository = NewWorkflowRepository(dbPool, coordinator)

	return coordinator, nil
}

func (b *Coordinator) Run(ctx context.Context) error {
	b.Logger.Info().Msg("starting coordinator")

	// Recover tasks orphaned by a previous coordinator crash: reset expired
	// RUNNING tasks to PENDING, then dispatch all PENDING tasks into the
	// in-memory matching service so waiting workers pick them up immediately.
	tasks, err := b.Repository.RecoverOrphanedTasks(ctx, *b.Logger)
	if err != nil {
		b.Logger.Error().Err(err).Msg("startup recovery failed — continuing anyway")
	} else {
		for _, t := range tasks {
			b.MatchingService.Dispatch(t)
		}
		if len(tasks) > 0 {
			b.Logger.Info().Int("count", len(tasks)).Msg("re-dispatched orphaned tasks to matching service")
		}
	}

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
	// Matching service queue depth per task type
	for tt, depth := range b.MatchingService.QueueDepth() {
		b.Logger.Debug().
			Str("task_type", tt).
			Int("depth", depth).
			Msg("matching service queue depth")
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

	util.SampleProcessMetrics(ctx)
}

func (b *Coordinator) Shutdown() error {
	b.Logger.Info().Msg("closing connections")
	if b.wfCache != nil {
		b.wfCache.close()
	}
	if b.DB != nil {
		b.DB.Close()
	}
	b.Logger.Info().Msg("coordinator shutdown complete")
	return nil
}
