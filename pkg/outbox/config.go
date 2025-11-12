package outbox

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/rs/zerolog"
)

// EnqueuerConfig configures the outbox enqueuer.
type EnqueuerConfig struct {
	DB           *pgxpool.Pool
	Redis        *redisq.RedisStream
	Logger       zerolog.Logger
	BatchSize    int
	Concurrency  int
	PollInterval time.Duration
	StreamPrefix string // e.g. "workflow." -> stream "workflow.tasks" or supply full stream in outbox payload
}
