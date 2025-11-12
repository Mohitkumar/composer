package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	grpcapi "github.com/mohitkumar/composer/pkg/api/grpc"
	httpapi "github.com/mohitkumar/composer/pkg/api/http"
	"github.com/mohitkumar/composer/pkg/coord"
	"github.com/mohitkumar/composer/pkg/outbox"
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/mohitkumar/composer/pkg/store"
	"github.com/rs/zerolog"
)

type Bootstrap struct {
	Config      *Config
	Logger      *zerolog.Logger
	Redis       *redisq.RedisStream
	Store       *store.Store
	Coordinator *coord.Coordinator
	Outbox      *outbox.OutboxEnqueuer
	HTTP        *httpapi.Server
	GRPC        *grpcapi.Server
}

func NewBootstrap(config *Config) (*Bootstrap, error) {
	logger := initLogger(config)

	// Create Redis stream
	redisStream := redisq.NewRedisStream(&config.RedisConfig)

	// Create store (database pool)
	dbStore := store.NewStore(&config.StoreConfig, *logger)

	// Create coordinator
	coordinator := coord.NewCoordinator(dbStore.DB(), *logger)

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

	// Create HTTP server with coordinator
	httpServer := httpapi.NewServer(&config.APIConfig, coordinator)

	// Create gRPC server with coordinator
	grpcServer := grpcapi.NewServer(&config.APIConfig, coordinator)

	return &Bootstrap{
		Config:      config,
		Logger:      logger,
		Redis:       redisStream,
		Store:       dbStore,
		Coordinator: coordinator,
		Outbox:      outboxEnqueuer,
		HTTP:        httpServer,
		GRPC:        grpcServer,
	}, nil
}

func initLogger(config *Config) *zerolog.Logger {
	level := zerolog.InfoLevel
	switch config.LogLevel {
	case "debug":
		level = zerolog.DebugLevel
	case "warn":
		level = zerolog.WarnLevel
	case "error":
		level = zerolog.ErrorLevel
	}
	logger := zerolog.New(os.Stderr).
		With().
		Timestamp().
		Logger().
		Level(level)
	return &logger
}

func (b *Bootstrap) Run() error {
	b.Logger.Info().Msg("starting coordinator bootstrap")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start outbox enqueuer
	b.Logger.Info().Msg("starting outbox enqueuer")
	go b.Outbox.Run(ctx)

	// Start HTTP server
	b.Logger.Info().Int("port", b.Config.APIConfig.HTTPPort).Msg("starting HTTP server")
	go func() {
		if err := b.HTTP.Start(); err != nil {
			b.Logger.Error().Err(err).Msg("HTTP server failed")
		}
	}()

	// Start gRPC server
	b.Logger.Info().Int("port", b.Config.APIConfig.GRPCPort).Msg("starting gRPC server")
	go func() {
		if err := b.GRPC.Start(); err != nil {
			b.Logger.Error().Err(err).Msg("gRPC server failed")
		}
	}()

	// Wait for interrupt signal
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	<-sigc

	b.Logger.Info().Msg("shutting down coordinator")
	return nil
}

func (b *Bootstrap) Shutdown() error {
	b.Logger.Info().Msg("stopping outbox enqueuer")
	b.Outbox.Stop()

	b.Logger.Info().Msg("stopping gRPC server")
	b.GRPC.Stop()

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
