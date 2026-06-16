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

	"github.com/mohitkumar/composer/pkg/util"
	"github.com/mohitkumar/composer/worker"
	"github.com/rs/zerolog"
)

// parseLangWorkerAddrs parses language worker addresses from environment variables.
// Supports two formats:
//  1. LANG_WORKER_ADDRS=python:localhost:50053,js:localhost:50052
//  2. Individual vars: LANG_WORKER_PYTHON_ADDR, LANG_WORKER_JS_ADDR, etc.
func parseLangWorkerAddrs() map[string]string {
	addrs := make(map[string]string)

	if addrsStr := os.Getenv("LANG_WORKER_ADDRS"); addrsStr != "" {
		for _, pair := range strings.Split(addrsStr, ",") {
			pair = strings.TrimSpace(pair)
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

// defaultTaskTypes returns the built-in task types the go worker handles.
// The go worker handles http natively; language runtimes are proxied to
// language workers via gRPC, so they're included when addresses are configured.
func defaultTaskTypes(langWorkerAddrs map[string]string) []string {
	types := []string{"http"}
	for runtime := range langWorkerAddrs {
		types = append(types, runtime)
	}
	return types
}

func main() {
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()

	coordAddr := os.Getenv("COORDINATOR_ADDR")
	if coordAddr == "" {
		coordAddr = "localhost:50051"
	}

	langWorkerAddrs := parseLangWorkerAddrs()

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

	// TASK_TYPES overrides the default set (comma-separated, e.g. "http,python")
	taskTypes := defaultTaskTypes(langWorkerAddrs)
	if v := os.Getenv("TASK_TYPES"); v != "" {
		taskTypes = strings.Split(v, ",")
		for i := range taskTypes {
			taskTypes[i] = strings.TrimSpace(taskTypes[i])
		}
	}

	ctx := context.Background()

	shutdownOTel, err := util.InitOpenTelemetry(ctx, "composer-worker", "1.0.0", false)
	if err != nil {
		logger.Error().Err(err).Msg("failed to initialize OpenTelemetry")
	} else {
		defer func() {
			if shutdownOTel != nil {
				if err := shutdownOTel(); err != nil {
					logger.Error().Err(err).Msg("failed to shutdown OpenTelemetry")
				}
			}
		}()
	}

	w, err := worker.NewWorker(worker.WorkerConfig{
		CoordinatorAddr: coordAddr,
		LangWorkerAddrs: langWorkerAddrs,
		WorkerID:        workerID,
		TaskTypes:       taskTypes,
		LeaseTTLSeconds: leaseTTLSeconds,
		Logger:          logger,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to create worker")
	}
	defer w.Close()

	logger.Info().
		Str("worker_id", workerID).
		Strs("task_types", taskTypes).
		Str("coordinator", coordAddr).
		Msg("starting worker")

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)

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

	done := make(chan error, 1)
	go func() {
		w.Run(ctx)
		done <- nil
	}()

	select {
	case <-sigc:
		logger.Info().Msg("shutdown signal received")
	case err := <-done:
		if err != nil {
			logger.Fatal().Err(err).Msg("worker failed")
		}
	}
}
