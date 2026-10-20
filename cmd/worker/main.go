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

	"github.com/google/uuid"
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/mohitkumar/composer/pkg/stream"
	redisstream "github.com/mohitkumar/composer/pkg/stream/redis"
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

	queues := workerQueues(langWorkerAddrs)

	consumerGroup := os.Getenv("CONSUMER_GROUP")
	if consumerGroup == "" {
		consumerGroup = "workflow-workers"
	}

	// The worker ID is both the stream consumer name and the lease owner, so it
	// must be unique per process.
	workerID := os.Getenv("WORKER_ID")
	if workerID == "" {
		host, _ := os.Hostname()
		workerID = fmt.Sprintf("worker-%s-%s", host, uuid.NewString()[:8])
	}

	leaseTTLSeconds := envInt("LEASE_TTL_SECONDS", 90)
	concurrency := envInt("WORKER_CONCURRENCY", 10)

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

	// One consumer per shard stream of every queue this worker serves.
	shards := streamShards(ctx, redisStream, logger)
	streamNames := stream.QueueStreams(queues, shards)
	consumers := make([]stream.Consumer, len(streamNames))
	for i, name := range streamNames {
		consumers[i] = redisstream.NewConsumer(redisstream.ConsumerConfig{
			StreamName:    name,
			ConsumerGroup: consumerGroup,
			ConsumerName:  workerID,
		}, redisStream, logger)
	}

	// Create worker
	wcfg := worker.WorkerConfig{
		Streams:         consumers,
		CoordinatorAddr: coordAddr,
		LangWorkerAddrs: langWorkerAddrs,
		ConsumerName:    workerID,
		LeaseTTLSeconds: leaseTTLSeconds,
		Concurrency:     int(concurrency),
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
		Strs("queues", queues).
		Int("shards", shards).
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

// envInt reads a positive integer from the environment, or returns def.
func envInt(name string, def int64) int64 {
	if v, err := strconv.ParseInt(os.Getenv(name), 10, 64); err == nil && v > 0 {
		return v
	}
	return def
}

// workerQueues returns the task queues to consume: WORKER_QUEUES if set,
// otherwise "http" plus one queue per configured language runtime.
func workerQueues(langWorkerAddrs map[string]string) []string {
	if v := os.Getenv("WORKER_QUEUES"); v != "" {
		var queues []string
		for _, q := range strings.Split(v, ",") {
			if q = strings.ToLower(strings.TrimSpace(q)); q != "" {
				queues = append(queues, q)
			}
		}
		return queues
	}
	queues := []string{"http"}
	for runtime := range langWorkerAddrs {
		queues = append(queues, runtime)
	}
	return queues
}

// streamShards returns the shard count the coordinator published in Redis,
// falling back to STREAM_SHARDS or the default when it is not set yet.
func streamShards(ctx context.Context, rs *redisq.RedisStream, logger zerolog.Logger) int {
	if n, err := rs.Client().Get(ctx, stream.ShardsKey).Int(); err == nil && n > 0 {
		return n
	}
	n := int(envInt("STREAM_SHARDS", stream.DefaultShards))
	logger.Warn().Int("shards", n).Msg("coordinator has not published a shard count yet; using STREAM_SHARDS")
	return n
}
