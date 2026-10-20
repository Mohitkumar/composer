package worker

import (
	"github.com/mohitkumar/composer/pkg/stream"
	"github.com/rs/zerolog"
)

// WorkerConfig holds everything the Worker needs injected at construction.
type WorkerConfig struct {
	// Streams are the Consumers the worker reads, reclaims and acks task
	// messages on — one per stream (task queue shard) it serves. Use
	// pkg/stream/redis.NewConsumer (or any other stream.Consumer) for each.
	Streams []stream.Consumer

	// CoordinatorAddr is the gRPC address of the coordinator service
	// (e.g. "localhost:50051").
	CoordinatorAddr string

	// LangWorkerAddrs maps runtime names to their gRPC addresses
	// (e.g. {"python": "localhost:50053", "js": "localhost:50052"}).
	LangWorkerAddrs map[string]string

	// ConsumerName is the unique identity of this worker instance.  Used as
	// the lease owner when acquiring task leases from the coordinator — must
	// match the ConsumerName given to the stream.Consumer so that lease
	// ownership can be correlated with stream delivery.
	ConsumerName string

	// LeaseTTLSeconds is the duration (in seconds) for which a task lease is
	// held.  The worker renews the lease every TTL/3 during execution and
	// reclaims orphaned messages after TTL seconds of idle time.
	LeaseTTLSeconds int64

	// Concurrency is the number of tasks executed in parallel. 0 uses 1.
	Concurrency int

	// MaxDeliveries is the maximum number of times a stream message may be
	// redelivered before it is dead-lettered (reported as failed to the
	// coordinator and removed from the stream).  0 uses the default of 5.
	MaxDeliveries int64

	Logger zerolog.Logger
}
