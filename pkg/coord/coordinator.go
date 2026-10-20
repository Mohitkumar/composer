package coord

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/lease"
	"github.com/mohitkumar/composer/pkg/outbox"
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/mohitkumar/composer/pkg/stream"
	redisstream "github.com/mohitkumar/composer/pkg/stream/redis"
	"github.com/mohitkumar/composer/pkg/template"
	"github.com/mohitkumar/composer/pkg/util"
	"github.com/rs/zerolog"
)

const (
	// recoveryInterval is how often the coordinator sweeps for tasks whose
	// worker died (expired lease) or whose dispatch message was lost.
	recoveryInterval = 30 * time.Second
	// timerInterval is how often due WAIT timers and event timeouts are fired.
	timerInterval = time.Second
	// partitionInterval is how often partitions are created ahead / expired.
	partitionInterval = 6 * time.Hour
	// partitionMonthsAhead is how many future monthly partitions are kept ready.
	partitionMonthsAhead = 3
	// lostDispatchAge is how long a PENDING task may sit without an outbox
	// entry before it is assumed lost and re-dispatched.
	lostDispatchAge = 10 * time.Minute
)

type Coordinator struct {
	Config       *Config
	Logger       *zerolog.Logger
	Redis        *redisq.RedisStream
	DB           db.DB
	LeaseManager *lease.Manager
	Outbox       *outbox.OutboxEnqueuer
	Template     *template.Resolver

	repo    repository
	wfCache *workflowCache

	// last-seen cumulative values for cache hit/miss delta counters
	lastCacheHits   uint64
	lastCacheMisses uint64
}

func NewCoordinator(config *Config, logger *zerolog.Logger) (*Coordinator, error) {
	redisStream, err := redisq.NewRedisStream(&config.RedisConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create redis stream: %w", err)
	}

	if config.StreamShards < 1 {
		config.StreamShards = stream.DefaultShards
	}

	dbPool, err := db.NewPool(context.Background(), &config.DBConfig, *logger)
	if err != nil {
		redisStream.Close()
		return nil, err
	}

	templateResolver, err := template.NewResolver()
	if err != nil {
		dbPool.Close()
		redisStream.Close()
		return nil, err
	}

	wfCache, err := newWorkflowCache(defaultCacheTTL)
	if err != nil {
		dbPool.Close()
		redisStream.Close()
		return nil, fmt.Errorf("failed to create workflow cache: %w", err)
	}

	return &Coordinator{
		Config:       config,
		Logger:       logger,
		Redis:        redisStream,
		DB:           dbPool,
		LeaseManager: lease.NewManager(dbPool),
		Outbox: outbox.NewOutboxEnqueuer(outbox.EnqueuerConfig{
			DB:           dbPool,
			Publisher:    redisstream.NewPublisher(redisStream),
			Logger:       *logger,
			BatchSize:    config.OutboxConfig.BatchSize,
			Concurrency:  config.OutboxConfig.Concurrency,
			PollInterval: config.OutboxConfig.PollInterval,
		}),
		Template: templateResolver,
		repo:     repository{streamShards: config.StreamShards},
		wfCache:  wfCache,
	}, nil
}

// Run prepares storage, starts the coordinator's background loops and returns.
func (c *Coordinator) Run(ctx context.Context) error {
	c.Logger.Info().Int("stream_shards", c.Config.StreamShards).Msg("starting coordinator")

	// Partitions must exist before any run is inserted.
	if err := c.MaintainPartitions(ctx); err != nil {
		return err
	}
	// Workers read the shard count to know which streams to consume.
	if err := c.Redis.Client().Set(ctx, stream.ShardsKey, c.Config.StreamShards, 0).Err(); err != nil {
		return fmt.Errorf("publish stream shard count: %w", err)
	}

	// Recover before the outbox starts so re-dispatched tasks go out in its first poll.
	c.RecoverTasks(ctx)

	go c.Outbox.Run(ctx)
	go c.every(ctx, recoveryInterval, c.RecoverTasks)
	go c.every(ctx, timerInterval, c.fireTimers)
	go c.every(ctx, partitionInterval, func(ctx context.Context) {
		if err := c.MaintainPartitions(ctx); err != nil {
			c.Logger.Error().Err(err).Msg("partition maintenance failed")
		}
	})
	go c.every(ctx, 15*time.Second, c.sampleMetrics)
	return nil
}

// MaintainPartitions creates the monthly partitions of run-scoped tables ahead
// of time and, with RetentionDays set, drops months past retention.
func (c *Coordinator) MaintainPartitions(ctx context.Context) error {
	var created int
	if err := c.DB.QueryRow(ctx, `SELECT composer_ensure_partitions($1)`, partitionMonthsAhead).Scan(&created); err != nil {
		return fmt.Errorf("ensure partitions: %w", err)
	}
	if created > 0 {
		c.Logger.Info().Int("created", created).Msg("created run partitions")
	}
	if c.Config.RetentionDays <= 0 {
		return nil
	}
	rows, err := c.DB.Query(ctx,
		`SELECT composer_drop_partitions(NOW() - make_interval(days => $1))`, c.Config.RetentionDays)
	if err != nil {
		return fmt.Errorf("drop expired partitions: %w", err)
	}
	dropped, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("drop expired partitions: %w", err)
	}
	if len(dropped) > 0 {
		c.Logger.Info().Strs("months", dropped).Msg("dropped expired run partitions")
	}
	return nil
}

func (c *Coordinator) fireTimers(ctx context.Context) {
	if _, err := c.FireDueWaits(ctx); err != nil {
		c.Logger.Error().Err(err).Msg("firing due waits failed")
	}
}

// RecoverTasks re-dispatches tasks orphaned by a dead worker or a lost stream
// message. Every coordinator runs it; the statements are safe to race.
func (c *Coordinator) RecoverTasks(ctx context.Context) {
	if n, err := c.repo.RequeueExpiredLeases(ctx, c.DB); err != nil {
		c.Logger.Error().Err(err).Msg("recovery: requeue expired leases failed")
	} else if n > 0 {
		c.Logger.Warn().Int64("count", n).Msg("recovery: re-dispatched tasks with expired leases")
	}

	if n, err := c.repo.RedispatchPendingTasks(ctx, c.DB, lostDispatchAge); err != nil {
		c.Logger.Error().Err(err).Msg("recovery: redispatch pending tasks failed")
	} else if n > 0 {
		c.Logger.Warn().Int64("count", n).Msg("recovery: re-dispatched pending tasks with lost messages")
	}
}

// every calls fn on each tick until ctx is done.
func (c *Coordinator) every(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn(ctx)
		}
	}
}

// sampleMetrics records all coordinator-side OTel gauges.
func (c *Coordinator) sampleMetrics(ctx context.Context) {
	if util.RedisStreamDepthGauge != nil {
		util.RedisStreamDepthGauge.Record(ctx, c.taskStreamDepth(ctx))
	}

	if util.OutboxBacklogGauge != nil {
		var count int64
		if err := c.DB.QueryRow(ctx,
			`SELECT COUNT(*) FROM outbox WHERE status IN ('PENDING','RETRY','IN_FLIGHT')`).Scan(&count); err == nil {
			util.OutboxBacklogGauge.Record(ctx, count)
		}
	}

	c.sampleDBPoolMetrics(ctx)
	c.sampleRedisMemory(ctx)
	c.sampleCacheMetrics(ctx)
	util.SampleProcessMetrics(ctx)
}

// taskStreamDepth sums the length of every task stream (all queues and shards).
func (c *Coordinator) taskStreamDepth(ctx context.Context) int64 {
	var total int64
	iter := c.Redis.Client().ScanType(ctx, 0, stream.TaskStreamPrefix+"*", 100, "stream").Iterator()
	for iter.Next(ctx) {
		if n, err := c.Redis.Client().XLen(ctx, iter.Val()).Result(); err == nil {
			total += n
		}
	}
	return total
}

func (c *Coordinator) sampleDBPoolMetrics(ctx context.Context) {
	pool := c.DB.Pool()
	if pool == nil {
		return
	}
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

func (c *Coordinator) sampleRedisMemory(ctx context.Context) {
	if util.RedisMemoryUsedGauge == nil {
		return
	}
	info, err := c.Redis.Client().Info(ctx, "memory").Result()
	if err != nil {
		return
	}
	for _, line := range strings.Split(info, "\r\n") {
		if v, ok := strings.CutPrefix(line, "used_memory:"); ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				util.RedisMemoryUsedGauge.Record(ctx, n)
			}
			return
		}
	}
}

func (c *Coordinator) sampleCacheMetrics(ctx context.Context) {
	if c.wfCache == nil {
		return
	}
	if util.WfCacheSizeGauge != nil {
		util.WfCacheSizeGauge.Record(ctx, int64(c.wfCache.c.Len()))
	}
	stats := c.wfCache.c.Stats()
	if util.WfCacheHitsCounter != nil {
		if delta := int64(stats.Hits()) - int64(c.lastCacheHits); delta > 0 {
			util.WfCacheHitsCounter.Add(ctx, delta)
		}
	}
	if util.WfCacheMissesCounter != nil {
		if delta := int64(stats.Misses()) - int64(c.lastCacheMisses); delta > 0 {
			util.WfCacheMissesCounter.Add(ctx, delta)
		}
	}
	c.lastCacheHits = stats.Hits()
	c.lastCacheMisses = stats.Misses()
}

func (c *Coordinator) Shutdown() error {
	c.Logger.Info().Msg("stopping outbox enqueuer")
	c.Outbox.Stop()

	c.Logger.Info().Msg("closing connections")
	if c.wfCache != nil {
		c.wfCache.close()
	}
	if c.Redis != nil {
		c.Redis.Close()
	}
	if c.DB != nil {
		c.DB.Close()
	}

	c.Logger.Info().Msg("coordinator shutdown complete")
	return nil
}
