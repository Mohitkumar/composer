package coord

import (
	"github.com/mohitkumar/composer/pkg/api"
	"github.com/mohitkumar/composer/pkg/db"
)

type Config struct {
	LogLevel             string        `yaml:"log_level" mapstructure:"log_level"`
	DBConfig             db.Config     `yaml:"store" mapstructure:"store"`
	APIConfig            api.APIConfig `yaml:"api" mapstructure:"api"`
	DisableObservability bool          `yaml:"disable_observability" mapstructure:"disable_observability"`
}
