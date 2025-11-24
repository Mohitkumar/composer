package coord

import (
	"github.com/mohitkumar/composer/pkg/api"
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/mohitkumar/composer/pkg/store"
)

type Config struct {
	LogLevel    string             `yaml:"log_level" mapstructure:"log_level"`
	RedisConfig redisq.RedisConfig `yaml:"redis" mapstructure:"redis"`
	StoreConfig store.StoreConfig  `yaml:"store" mapstructure:"store"`
	APIConfig   api.APIConfig      `yaml:"api" mapstructure:"api"`
}
