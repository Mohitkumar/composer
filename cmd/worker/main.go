package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"strconv"

	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/mohitkumar/composer/pkg/util"
	"github.com/mohitkumar/composer/worker"
	"github.com/rs/zerolog"
)

// parseLangWorkerAddrs parses language worker addresses from environment variables
// Supports multiple formats:
// 1. LANG_WORKER_ADDRS=python:localhost:50053,js:localhost:50052
// 2. Individual env vars: LANG_WORKER_PYTHON_ADDR, LANG_WORKER_JS_ADDR, etc.
func parseLangWorkerAddrs() map[string]string {
	addrs := make(map[string]string)

	// Format 1: Comma-separated list with runtime:address format
	if addrsStr := os.Getenv("LANG_WORKER_ADDRS"); addrsStr != "" {
		pairs := strings.Split(addrsStr, ",")
		for _, pair := range pairs {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			parts := strings.SplitN(pair, ":", 2)
			if len(parts) == 2 {
				runtime := strings.TrimSpace(parts[0])
				addr := strings.TrimSpace(parts[1])
				if runtime != "" && addr != "" {
					addrs[runtime] = addr
				}
			}
		}
	}

	// Format 2: Individual environment variables
	// LANG_WORKER_PYTHON_ADDR, LANG_WORKER_JS_ADDR, etc.
	runtimeEnvVars := map[string]string{
		"LANG_WORKER_PYTHON_ADDR":     "python",
		"LANG_WORKER_PY_ADDR":         "python",
		"LANG_WORKER_JS_ADDR":         "js",
		"LANG_WORKER_NODE_ADDR":       "js",
		"LANG_WORKER_JAVASCRIPT_ADDR": "js",
	}

	for envVar, runtime := range runtimeEnvVars {
		if addr := os.Getenv(envVar); addr != "" {
			addrs[runtime] = addr
		}
	}

	return addrs
}

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

	// Parse language worker addresses
	// Support multiple formats:
	// 1. LANG_WORKER_ADDRS=python:localhost:50053,js:localhost:50052
	// 2. Individual env vars: LANG_WORKER_PYTHON_ADDR, LANG_WORKER_JS_ADDR, etc.
	langWorkerAddrs := parseLangWorkerAddrs()

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

	// Initialize OpenTelemetry SDK for traces
	shutdownOTel, err := util.InitOpenTelemetry(ctx, "composer-worker", "1.0.0", false)
	if err != nil {
		logger.Error().Err(err).Msg("failed to initialize OpenTelemetry")
		// Continue without observability if initialization fails
	} else {
		logger.Info().Msg("initialized OpenTelemetry SDK for traces")
		defer func() {
			if shutdownOTel != nil {
				if err := shutdownOTel(); err != nil {
					logger.Error().Err(err).Msg("failed to shutdown OpenTelemetry")
				}
			}
		}()
	}

	// Create Redis stream
	redisConfig := &redisq.RedisConfig{
		Addrs:    []string{redisAddr},
		Username: "",
		Password: "",
		DB:       0,
	}
	redisStream, err := redisq.NewRedisStream(redisConfig)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to create redis stream")
	}
	defer redisStream.Close()

	// Create worker
	wcfg := worker.WorkerConfig{
		Redis:           redisStream,
		CoordinatorAddr: coordAddr,
		LangWorkerAddrs: langWorkerAddrs,
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

	// Periodically sample process metrics (RSS, heap, goroutines)
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				util.SampleProcessMetrics(ctx)
			}
		}
	}()

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
