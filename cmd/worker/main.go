package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"strconv"

	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/mohitkumar/composer/worker"
	"github.com/rs/zerolog"
)

func main() {
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()

	// Get configuration from environment variables
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "127.0.0.1:6379"
	}

	coordAddr := os.Getenv("COORDINATOR_ADDR")
	if coordAddr == "" {
		coordAddr = "localhost:50051"
	}

	langWorkerAddr := os.Getenv("LANG_WORKER_ADDR") // Optional: for python/js workers

	streamName := os.Getenv("STREAM_NAME")
	if streamName == "" {
		streamName = "workflow.tasks"
	}

	consumerGroup := os.Getenv("CONSUMER_GROUP")
	if consumerGroup == "" {
		consumerGroup = "workflow-workers"
	}

	workerID := os.Getenv("WORKER_ID")
	if workerID == "" {
		workerID = fmt.Sprintf("worker-%d", time.Now().Unix())
	}

	leaseTTLSeconds := int64(90)
	if v := os.Getenv("LEASE_TTL_SECONDS"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed > 0 {
			leaseTTLSeconds = parsed
		}
	}

	ctx := context.Background()

	// Create Redis stream
	redisConfig := &redisq.RedisConfig{
		Addrs:    []string{redisAddr},
		Username: "",
		Password: "",
		DB:       0,
	}
	redisStream := redisq.NewRedisStream(redisConfig)
	defer redisStream.Close()

	// Create worker
	wcfg := worker.WorkerConfig{
		Redis:           redisStream,
		CoordinatorAddr: coordAddr,
		LangWorkerAddr:  langWorkerAddr,
		StreamName:      streamName,
		ConsumerGroup:   consumerGroup,
		ConsumerName:    workerID,
		LeaseTTLSeconds: leaseTTLSeconds,
		Logger:          logger,
	}

	w, err := worker.NewWorker(wcfg)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to create worker")
	}
	defer w.Close()

	// Ensure consumer group exists
	if err := w.EnsureConsumerGroup(ctx); err != nil {
		logger.Fatal().Err(err).Msg("failed to ensure consumer group")
	}

	logger.Info().
		Str("worker_id", workerID).
		Str("stream", streamName).
		Str("group", consumerGroup).
		Str("coordinator", coordAddr).
		Msg("starting worker")

	// Handle shutdown
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)

	// Run worker in goroutine
	done := make(chan error, 1)
	go func() {
		w.Run(ctx)
		done <- nil
	}()

	// Wait for signal or error
	select {
	case <-sigc:
		logger.Info().Msg("shutdown signal received")
	case err := <-done:
		if err != nil {
			logger.Fatal().Err(err).Msg("worker failed")
		}
	}
}
