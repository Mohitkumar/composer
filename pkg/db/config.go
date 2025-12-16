package db

import "time"

// Config holds database connection configuration
type Config struct {
	URL                   string        `yaml:"url" mapstructure:"url"`
	MaxConns              int           `yaml:"max_conns" mapstructure:"max_conns"`
	MaxIdleConns          int           `yaml:"max_idle_conns" mapstructure:"max_idle_conns"`
	MaxConnLifetime       time.Duration `yaml:"max_conn_lifetime" mapstructure:"max_conn_lifetime"`
	MaxConnIdleTime       time.Duration `yaml:"max_conn_idle_time" mapstructure:"max_conn_idle_time"`
	MaxConnTimeout        time.Duration `yaml:"max_conn_timeout" mapstructure:"max_conn_timeout"`
	MaxConnReleaseTimeout time.Duration `yaml:"max_conn_release_timeout" mapstructure:"max_conn_release_timeout"`
}
