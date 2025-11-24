package coord

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mohitkumar/composer/pkg/outbox"
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/mohitkumar/composer/pkg/store"
	"github.com/rs/zerolog"
)

type Coordinator struct {
	Config *Config
	Logger *zerolog.Logger
	Redis  *redisq.RedisStream
	Store  *store.Store
	Outbox *outbox.OutboxEnqueuer
}

func NewCoordinator(config *Config, logger *zerolog.Logger) (*Coordinator, error) {

	// Create Redis stream
	redisStream := redisq.NewRedisStream(&config.RedisConfig)

	// Create store (database pool)
	dbStore := store.NewStore(&config.StoreConfig, *logger)

	// Create outbox enqueuer
	outboxEnqueuer := outbox.NewOutboxEnqueuer(outbox.EnqueuerConfig{
		DB:           dbStore.DB(),
		Redis:        redisStream,
		Logger:       *logger,
		BatchSize:    50,
		Concurrency:  2,
		PollInterval: 1 * time.Second,
		StreamPrefix: "workflow.",
	})

	return &Coordinator{
		Config: config,
		Logger: logger,
		Redis:  redisStream,
		Store:  dbStore,
		Outbox: outboxEnqueuer,
	}, nil
}

func (b *Coordinator) Run() error {
	b.Logger.Info().Msg("starting coordinator bootstrap")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start outbox enqueuer
	b.Logger.Info().Msg("starting outbox enqueuer")
	go b.Outbox.Run(ctx)

	// Wait for interrupt signal
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	<-sigc

	b.Logger.Info().Msg("shutting down coordinator")
	return nil
}

func (b *Coordinator) Shutdown() error {
	b.Logger.Info().Msg("stopping outbox enqueuer")
	b.Outbox.Stop()

	b.Logger.Info().Msg("closing connections")
	if b.Redis != nil {
		b.Redis.Close()
	}
	if b.Store != nil {
		b.Store.Close()
	}

	b.Logger.Info().Msg("coordinator shutdown complete")
	return nil
}
