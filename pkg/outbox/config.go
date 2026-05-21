package outbox

import (
	"time"

	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/stream"
	"github.com/rs/zerolog"
)

// EnqueuerConfig configures the outbox enqueuer.
type EnqueuerConfig struct {
	DB           db.DB
	Publisher    stream.Publisher
	Logger       zerolog.Logger
	BatchSize    int
	Concurrency  int
	PollInterval time.Duration
	StreamPrefix string // e.g. "workflow." -> stream "workflow.tasks" or supply full stream in outbox payload
}
