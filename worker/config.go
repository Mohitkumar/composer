package worker

import (
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/rs/zerolog"
)

// WorkerConfig - inject dependencies
type WorkerConfig struct {
	Redis           *redisq.RedisStream
	CoordinatorAddr string            // gRPC address (e.g., "localhost:50051")
	LangWorkerAddr  string            // gRPC address for language workers (optional, deprecated - use LangWorkerAddrs instead)
	LangWorkerAddrs map[string]string // Map of runtime -> gRPC address (e.g., "python" -> "localhost:50053", "js" -> "localhost:50052")

	StreamName      string
	ConsumerGroup   string
	ConsumerName    string
	LeaseTTLSeconds int64
	Logger          zerolog.Logger
}
