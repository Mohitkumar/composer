package coord

import (
	"github.com/mohitkumar/composer/pkg/api"
	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/redisq"
)

type Config struct {
	LogLevel             string             `yaml:"log_level" mapstructure:"log_level"`
	RedisConfig          redisq.RedisConfig `yaml:"redis" mapstructure:"redis"`
	DBConfig             db.Config          `yaml:"store" mapstructure:"store"`
	APIConfig            api.APIConfig      `yaml:"api" mapstructure:"api"`
	DisableObservability bool               `yaml:"disable_observability" mapstructure:"disable_observability"`
}
