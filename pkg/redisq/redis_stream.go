package redisq

import (
	"context"

	"github.com/redis/go-redis/v9"
)

type RedisStream struct {
	config *RedisConfig
	client *redis.Client
}

// NewRedisStream creates a new RedisStream instance with the given config.
// It also accepts an optional existing redis.Client for reuse.
func NewRedisStream(config *RedisConfig, client ...*redis.Client) *RedisStream {
	var rdb *redis.Client
	if len(client) > 0 && client[0] != nil {
		rdb = client[0]
	} else {
		rdb = redis.NewClient(&redis.Options{
			Addr:     config.Addrs[0],
			Username: config.Username,
			Password: config.Password,
			DB:       config.DB,
		})
	}

	rs := &RedisStream{
		config: config,
		client: rdb,
	}

	// Test connection
	if err := rs.client.Ping(context.Background()).Err(); err != nil {
		panic(err)
	}
	return rs
}

// Publish publishes a message to a Redis pub/sub channel
func (r *RedisStream) Publish(ctx context.Context, channel string, message interface{}) error {
	return r.client.Publish(ctx, channel, message).Err()
}

// Subscribe subscribes to a Redis pub/sub channel
func (r *RedisStream) Subscribe(ctx context.Context, channel string) (*redis.PubSub, error) {
	return r.client.Subscribe(ctx, channel), nil
}

// XAdd adds an entry to a Redis stream
// stream is the stream name, values is a map of field-value pairs
// Returns the message ID and any error
func (r *RedisStream) XAdd(ctx context.Context, stream string, values map[string]interface{}) (string, error) {
	args := &redis.XAddArgs{
		Stream: stream,
		Values: values,
	}
	return r.client.XAdd(ctx, args).Result()
}

// Close closes the Redis client connection
func (r *RedisStream) Close() error {
	return r.client.Close()
}

// XReadGroup reads messages from a Redis stream consumer group
func (r *RedisStream) XReadGroup(ctx context.Context, args *redis.XReadGroupArgs) ([]redis.XStream, error) {
	return r.client.XReadGroup(ctx, args).Result()
}

// XAck acknowledges a message in a consumer group
func (r *RedisStream) XAck(ctx context.Context, stream, group string, ids ...string) (int64, error) {
	return r.client.XAck(ctx, stream, group, ids...).Result()
}

// XGroupCreateMkStream creates a consumer group for a stream
func (r *RedisStream) XGroupCreateMkStream(ctx context.Context, stream, group, start string) error {
	return r.client.XGroupCreateMkStream(ctx, stream, group, start).Err()
}

// Client returns the underlying Redis client (for advanced use cases)
func (r *RedisStream) Client() *redis.Client {
	return r.client
}
