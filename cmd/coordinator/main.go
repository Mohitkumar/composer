package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/mohitkumar/composer/pkg/api"
	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/mohitkumar/composer/pkg/store"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfg *Config

var rootCmd = &cobra.Command{
	Use:   "coordinator",
	Short: "Composer coordinator - Raft-based workflow orchestration",
	Long:  `Coordinator is the main control plane component that manages workflow execution using Raft consensus.`,
	RunE:  runCoordinator,
}

func init() {
	cobra.OnInitialize(initConfig)
	setupFlags(rootCmd)
}

func setupFlags(cmd *cobra.Command) {
	// General flags
	cmd.Flags().String("node-name", "", "The name of the node")
	cmd.Flags().StringSlice("start-join-addrs", []string{}, "Comma-separated addresses of other nodes to join")
	cmd.Flags().Int("port", 8080, "The port to listen on")
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

func generateConfig() *Config {
	config := &Config{
		NodeName:       viper.GetString("node-name"),
		StartJoinAddrs: viper.GetStringSlice("start-join-addrs"),
		Port:           viper.GetInt("port"),
		LogLevel:       viper.GetString("log-level"),
		RedisConfig: redisq.RedisConfig{
			Addrs:    viper.GetStringSlice("redis.addrs"),
			Username: viper.GetString("redis.username"),
			Password: viper.GetString("redis.password"),
			DB:       viper.GetInt("redis.db"),
		},
		StoreConfig: store.StoreConfig{
			URL:             viper.GetString("store.url"),
			MaxConns:        viper.GetInt("store.max-conns"),
			MaxIdleConns:    viper.GetInt("store.max-idle-conns"),
			MaxConnLifetime: viper.GetInt("store.max-conn-lifetime"),
			MaxConnIdleTime: viper.GetInt("store.max-conn-idle-time"),
			MaxConnTimeout:  viper.GetInt("store.max-conn-timeout"),
		},
		APIConfig: api.APIConfig{
			HTTPPort: viper.GetInt("api.http_port"),
			GRPCPort: viper.GetInt("api.grpc_port"),
		},
	}

	// Set defaults if not provided
	if config.APIConfig.HTTPPort == 0 {
		config.APIConfig.HTTPPort = 8080
	}
	if config.APIConfig.GRPCPort == 0 {
		config.APIConfig.GRPCPort = 50051
	}
	if config.StoreConfig.URL == "" {
		config.StoreConfig.URL = "postgres://postgres:password@localhost:5432/composer?sslmode=disable"
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
