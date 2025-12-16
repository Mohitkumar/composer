package coord

import (
	"time"

	"github.com/mohitkumar/composer/pkg/api"
	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/redisq"
)

// OutboxConfig configures the outbox enqueuer
type OutboxConfig struct {
	BatchSize    int           `yaml:"batch_size" mapstructure:"batch_size"`
	Concurrency  int           `yaml:"concurrency" mapstructure:"concurrency"`
	PollInterval time.Duration `yaml:"poll_interval" mapstructure:"poll_interval"`
	StreamPrefix string        `yaml:"stream_prefix" mapstructure:"stream_prefix"`
}

type Config struct {
	LogLevel             string             `yaml:"log_level" mapstructure:"log_level"`
	RedisConfig          redisq.RedisConfig `yaml:"redis" mapstructure:"redis"`
	DBConfig             db.Config          `yaml:"store" mapstructure:"store"`
	APIConfig            api.APIConfig      `yaml:"api" mapstructure:"api"`
	DisableObservability bool               `yaml:"disable_observability" mapstructure:"disable_observability"`
	OutboxConfig         OutboxConfig       `yaml:"outbox" mapstructure:"outbox"`
}
