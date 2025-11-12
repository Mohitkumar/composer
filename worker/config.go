package worker

import (
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/rs/zerolog"
)

// WorkerConfig - inject dependencies
type WorkerConfig struct {
	Redis           *redisq.RedisStream
	CoordinatorAddr string // gRPC address (e.g., "localhost:50051")
	LangWorkerAddr  string // gRPC address for language workers (optional)

	StreamName    string
	ConsumerGroup string
	ConsumerName  string
	Logger        zerolog.Logger
}
