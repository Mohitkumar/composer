package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mohitkumar/composer/pkg/api"
	"github.com/mohitkumar/composer/pkg/coord"
	"github.com/mohitkumar/composer/pkg/db"
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfg *coord.Config

var rootCmd = &cobra.Command{
	Use:   "coordinator",
	Short: "Composer coordinator - the control plane for workflow orchestration",
	Long:  `Coordinator is the main control plane component that manages workflow execution.`,
	RunE:  runCoordinator,
}

func init() {
	cobra.OnInitialize(initConfig)
	setupFlags(rootCmd)
}

func setupFlags(cmd *cobra.Command) {
	// General flags
	cmd.Flags().String("log-level", "info", "The log level (debug, info, warn, error)")

	// Redis flags
	cmd.Flags().StringSlice("redis.addrs", []string{"localhost:6379"}, "Redis server addresses")
	cmd.Flags().String("redis.username", "", "Redis username")
	cmd.Flags().String("redis.password", "", "Redis password")
	cmd.Flags().Int("redis.db", 0, "Redis database number")

	// Store flags
	cmd.Flags().String("store.url", "", "PostgreSQL connection URL")

	// API flags
	cmd.Flags().Int("api.http_port", 8080, "HTTP API port")
	cmd.Flags().Int("api.grpc_port", 50051, "gRPC API port")

	// Observability flags
	cmd.Flags().Bool("disable-observability", false, "Disable metrics and traces export (useful for local development)")

	// Outbox flags
	cmd.Flags().Int("outbox.batch_size", 50, "Outbox batch size (number of messages to process per batch)")
	cmd.Flags().Int("outbox.concurrency", 2, "Outbox concurrency (number of concurrent workers)")
	cmd.Flags().Duration("outbox.poll_interval", 100*time.Millisecond, "Outbox poll interval (how often to check for new messages)")
	cmd.Flags().String("outbox.stream_prefix", "workflow.", "Outbox stream prefix (e.g., 'workflow.' for 'workflow.tasks')")

	// Config file flag
	cmd.Flags().String("config", "", "Config file path (default: ./config.yaml)")

	// Bind flags to viper
	viper.BindPFlags(cmd.Flags())
}

func initConfig() {
	// Set config file path
	configPath := viper.GetString("config")
	if configPath != "" {
		viper.SetConfigFile(configPath)
	} else {
		// Default config file locations
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
		viper.AddConfigPath("./config")
		viper.AddConfigPath("/etc/composer")
	}

	// Environment variables
	viper.SetEnvPrefix("COMPOSER")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.AutomaticEnv()

	// Read config file if it exists
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			fmt.Printf("Error reading config file: %v\n", err)
		}
	}

	// Generate Config from viper
	cfg = generateConfig()
}

func generateConfig() *coord.Config {
	config := &coord.Config{
		LogLevel: viper.GetString("log-level"),
		RedisConfig: redisq.RedisConfig{
			Addrs:    viper.GetStringSlice("redis.addrs"),
			Username: viper.GetString("redis.username"),
			Password: viper.GetString("redis.password"),
			DB:       viper.GetInt("redis.db"),
		},
		DBConfig: db.Config{
			URL:                   viper.GetString("store.url"),
			MaxConns:              viper.GetInt("store.max-conns"),
			MaxIdleConns:          viper.GetInt("store.max-idle-conns"),
			MaxConnLifetime:       time.Duration(viper.GetInt("store.max-conn-lifetime")) * time.Second,
			MaxConnIdleTime:       time.Duration(viper.GetInt("store.max-conn-idle-time")) * time.Second,
			MaxConnTimeout:        time.Duration(viper.GetInt("store.max-conn-timeout")) * time.Second,
			MaxConnReleaseTimeout: time.Duration(viper.GetInt("store.max-conn-release-timeout")) * time.Second,
		},
		APIConfig: api.APIConfig{
			HTTPPort:  viper.GetInt("api.http_port"),
			GRPCPort:  viper.GetInt("api.grpc_port"),
			JWTSecret: viper.GetString("api.jwt_secret"),
		},
		DisableObservability: viper.GetBool("disable-observability"),
		OutboxConfig: coord.OutboxConfig{
			BatchSize:    viper.GetInt("outbox.batch_size"),
			Concurrency:  viper.GetInt("outbox.concurrency"),
			PollInterval: viper.GetDuration("outbox.poll_interval"),
			StreamPrefix: viper.GetString("outbox.stream_prefix"),
		},
	}

	// Set defaults if not provided
	if config.APIConfig.HTTPPort == 0 {
		config.APIConfig.HTTPPort = 8080
	}
	if config.APIConfig.GRPCPort == 0 {
		config.APIConfig.GRPCPort = 50051
	}
	if config.DBConfig.URL == "" {
		config.DBConfig.URL = "postgres://postgres:password@localhost:5432/composer?sslmode=disable"
	}
	if config.DBConfig.MaxConns == 0 {
		config.DBConfig.MaxConns = 25
	}
	// Set outbox defaults if not provided
	if config.OutboxConfig.BatchSize == 0 {
		config.OutboxConfig.BatchSize = 50
	}
	if config.OutboxConfig.Concurrency == 0 {
		config.OutboxConfig.Concurrency = 2
	}
	if config.OutboxConfig.PollInterval == 0 {
		config.OutboxConfig.PollInterval = 100 * time.Millisecond
	}
	if config.OutboxConfig.StreamPrefix == "" {
		config.OutboxConfig.StreamPrefix = "workflow."
	}

	return config
}

func runCoordinator(cmd *cobra.Command, args []string) error {
	bootstrap, err := NewBootstrap(cfg)
	if err != nil {
		return fmt.Errorf("failed to create bootstrap: %w", err)
	}

	if err := bootstrap.Run(); err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}

	if err := bootstrap.Shutdown(); err != nil {
		return fmt.Errorf("shutdown failed: %w", err)
	}
	return nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
