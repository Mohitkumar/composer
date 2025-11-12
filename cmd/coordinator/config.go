package main

import (
	"github.com/mohitkumar/composer/pkg/api"
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/mohitkumar/composer/pkg/store"
)

type Config struct {
	NodeName       string             `yaml:"node_name" mapstructure:"node_name"`
	StartJoinAddrs []string           `yaml:"start_join_addrs" mapstructure:"start_join_addrs"`
	Port           int                `yaml:"port" mapstructure:"port"`
	LogLevel       string             `yaml:"log_level" mapstructure:"log_level"`
	RedisConfig    redisq.RedisConfig `yaml:"redis" mapstructure:"redis"`
	StoreConfig    store.StoreConfig  `yaml:"store" mapstructure:"store"`
	APIConfig      api.APIConfig      `yaml:"api" mapstructure:"api"`
}
