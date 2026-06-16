package worker

import "github.com/rs/zerolog"

// WorkerConfig holds everything the Worker needs injected at construction.
type WorkerConfig struct {
	// CoordinatorAddr is the gRPC address of the coordinator service
	// (e.g. "localhost:50051").
	CoordinatorAddr string

	// LangWorkerAddrs maps runtime names to their gRPC addresses
	// (e.g. {"python": "localhost:50053", "js": "localhost:50052"}).
	LangWorkerAddrs map[string]string

	// WorkerID is the unique identity of this worker instance. Used as
	// the lease owner when acquiring task leases from the coordinator.
	WorkerID string

	// TaskTypes is the list of node types this worker can execute
	// (e.g. ["http", "python", "js"]). Passed to PollTaskQueue so the
	// coordinator only routes matching tasks to this worker.
	TaskTypes []string

	// LeaseTTLSeconds is the duration (in seconds) for which a task lease
	// is held. The worker renews the lease every TTL/3 during execution.
	LeaseTTLSeconds int64

	Logger zerolog.Logger
}
