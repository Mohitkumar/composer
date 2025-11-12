package redisq

type RedisConfig struct {
	Addrs    []string `yaml:"addrs" mapstructure:"addrs"`
	Username string   `yaml:"username" mapstructure:"username"`
	Password string   `yaml:"password" mapstructure:"password"`
	DB       int      `yaml:"db" mapstructure:"db"`
}
