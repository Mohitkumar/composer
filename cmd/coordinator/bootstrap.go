package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	grpcapi "github.com/mohitkumar/composer/pkg/api/grpc"
	httpapi "github.com/mohitkumar/composer/pkg/api/http"
	"github.com/mohitkumar/composer/pkg/coord"
	"github.com/mohitkumar/composer/pkg/util"
	"github.com/rs/zerolog"
)

type Bootstrap struct {
	Logger       *zerolog.Logger
	Config       *coord.Config
	Coordinator  *coord.Coordinator
	HTTP         *httpapi.Server
	GRPC         *grpcapi.Server
	shutdownOTel func() error
}

func NewBootstrap(config *coord.Config) (*Bootstrap, error) {
	logger := initLogger(config)

	// Initialize OpenTelemetry SDK
	ctx := context.Background()
	shutdown, err := util.InitOpenTelemetry(ctx, "composer-coordinator", "1.0.0")
	if err != nil {
		logger.Error().Err(err).Msg("failed to initialize OpenTelemetry")
		return nil, fmt.Errorf("failed to initialize OpenTelemetry: %w", err)
	}
	logger.Info().Msg("initialized OpenTelemetry SDK")

	// Initialize OpenTelemetry metrics
	if err := util.InitMetrics(); err != nil {
		logger.Error().Err(err).Msg("failed to initialize metrics")
		return nil, fmt.Errorf("failed to initialize metrics: %w", err)
	}
	logger.Info().Msg("initialized OpenTelemetry metrics")

	// Create coordinator
	coordinator, err := coord.NewCoordinator(config, logger)
	if err != nil {
		logger.Error().Err(err).Msg("failed to create coordinator")
		return nil, fmt.Errorf("failed to create coordinator: %w", err)
	}

	// Create HTTP server with coordinator
	httpServer := httpapi.NewServer(&config.APIConfig, coordinator)

	// Create gRPC server with coordinator
	grpcServer := grpcapi.NewServer(&config.APIConfig, coordinator)

	return &Bootstrap{
		Logger:       logger,
		Config:       config,
		Coordinator:  coordinator,
		HTTP:         httpServer,
		GRPC:         grpcServer,
		shutdownOTel: shutdown,
	}, nil
}

func (b *Bootstrap) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b.Logger.Info().Msg("starting composer")
	err := b.Coordinator.Run(ctx)
	if err != nil {
		b.Logger.Error().Err(err).Msg("failed to run coordinator")
		return fmt.Errorf("failed to run coordinator: %w", err)
	}
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

	b.Logger.Info().Msg("shutting down composer")
	return nil
}

func initLogger(config *coord.Config) *zerolog.Logger {
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

func (b *Bootstrap) Shutdown() error {
	b.Logger.Info().Msg("shutting down composer....")
	err := b.Coordinator.Shutdown()

	if err != nil {
		b.Logger.Error().Err(err).Msg("failed to shutdown coordinator")
		return fmt.Errorf("failed to shutdown coordinator: %w", err)
	}
	b.Logger.Info().Msg("stopping gRPC server")
	b.GRPC.Stop()

	b.Logger.Info().Msg("shutting down OpenTelemetry")
	if b.shutdownOTel != nil {
		if err := b.shutdownOTel(); err != nil {
			b.Logger.Error().Err(err).Msg("failed to shutdown OpenTelemetry")
		}
	}
	b.Logger.Info().Msg("shutdown complete")
	return nil
}
