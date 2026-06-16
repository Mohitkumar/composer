package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/mohitkumar/composer/pkg/api"
	"github.com/mohitkumar/composer/pkg/coord"
	"github.com/mohitkumar/composer/pkg/db"
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
	cmd.Flags().String("log-level", "info", "The log level (debug, info, warn, error)")

	// Store flags
	cmd.Flags().String("store.url", "", "PostgreSQL connection URL")

	// API flags
	cmd.Flags().Int("api.http_port", 8080, "HTTP API port")
	cmd.Flags().Int("api.grpc_port", 50051, "gRPC API port")

	// Observability flags
	cmd.Flags().Bool("disable-observability", false, "Disable metrics and traces export (useful for local development)")

	// Config file flag
	cmd.Flags().String("config", "", "Config file path (default: ./config.yaml)")

	viper.BindPFlags(cmd.Flags())
}

func initConfig() {
	configPath := viper.GetString("config")
	if configPath != "" {
		viper.SetConfigFile(configPath)
	} else {
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
		viper.AddConfigPath("./config")
		viper.AddConfigPath("/etc/composer")
	}

	viper.SetEnvPrefix("COMPOSER")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			fmt.Printf("Error reading config file: %v\n", err)
		}
	}

	cfg = generateConfig()
}

func generateConfig() *coord.Config {
	config := &coord.Config{
		LogLevel: viper.GetString("log-level"),
		DBConfig: db.Config{
			URL:             viper.GetString("store.url"),
			MaxConns:        viper.GetInt("store.max-conns"),
			MaxIdleConns:    viper.GetInt("store.max-idle-conns"),
			MaxConnLifetime: viper.GetDuration("store.max-conn-lifetime"),
			MaxConnIdleTime: viper.GetDuration("store.max-conn-idle-time"),
			MaxConnTimeout:  viper.GetDuration("store.max-conn-timeout"),
		},
		APIConfig: api.APIConfig{
			HTTPPort:  viper.GetInt("api.http_port"),
			GRPCPort:  viper.GetInt("api.grpc_port"),
			JWTSecret: viper.GetString("api.jwt_secret"),
		},
		DisableObservability: viper.GetBool("disable-observability"),
	}

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
