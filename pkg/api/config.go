package api

type APIConfig struct {
	HTTPPort int `yaml:"http_port" mapstructure:"http_port"`
	GRPCPort int `yaml:"grpc_port" mapstructure:"grpc_port"`
}
