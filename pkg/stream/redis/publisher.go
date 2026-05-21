package redisstream

import (
	"context"
	"fmt"

	"github.com/mohitkumar/composer/pkg/redisq"
)

// Publisher implements stream.Publisher using Redis Streams (XADD).
type Publisher struct {
	rdb *redisq.RedisStream
}

// NewPublisher creates a Publisher.  The caller retains ownership of rdb.
func NewPublisher(rdb *redisq.RedisStream) *Publisher {
	return &Publisher{rdb: rdb}
}

// Publish appends values to the named Redis stream.
func (p *Publisher) Publish(ctx context.Context, stream string, values map[string]any) error {
	if _, err := p.rdb.XAdd(ctx, stream, values); err != nil {
		return fmt.Errorf("xadd to %s: %w", stream, err)
	}
	return nil
}
