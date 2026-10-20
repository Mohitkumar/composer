// Package redisstream provides Redis-backed implementations of stream.Consumer
// and stream.Publisher using Redis Streams (XREADGROUP / XAUTOCLAIM / XADD).
package redisstream

import (
	"context"
	"fmt"
	"time"

	"github.com/mohitkumar/composer/pkg/redisq"
	"github.com/mohitkumar/composer/pkg/stream"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// ConsumerConfig holds the Redis-specific options for a Consumer.
type ConsumerConfig struct {
	// StreamName is the Redis stream key (e.g. "workflow.tasks").
	StreamName string
	// ConsumerGroup is the consumer group name.
	ConsumerGroup string
	// ConsumerName uniquely identifies this consumer within the group.
	ConsumerName string
	// BlockDuration is how long ReadNew blocks waiting for new messages.
	// Defaults to 5 s.
	BlockDuration time.Duration
	// ReclaimCount is the maximum number of orphaned messages claimed per
	// ReclaimOrphaned call.  Defaults to 10.
	ReclaimCount int64
}

// Consumer implements stream.Consumer using Redis Streams.
type Consumer struct {
	cfg    ConsumerConfig
	rdb    *redisq.RedisStream
	logger zerolog.Logger
}

// NewConsumer creates a Consumer.  Defaults are applied for zero-valued
// numeric fields; the caller retains ownership of rdb.
func NewConsumer(cfg ConsumerConfig, rdb *redisq.RedisStream, logger zerolog.Logger) *Consumer {
	if cfg.BlockDuration == 0 {
		cfg.BlockDuration = 5 * time.Second
	}
	if cfg.ReclaimCount == 0 {
		cfg.ReclaimCount = 10
	}
	return &Consumer{cfg: cfg, rdb: rdb, logger: logger}
}

// Setup creates the consumer group (and the stream if absent).  Safe to call
// on every startup — ignores BUSYGROUP errors.
func (c *Consumer) Setup(ctx context.Context) error {
	err := c.rdb.XGroupCreateMkStream(ctx, c.cfg.StreamName, c.cfg.ConsumerGroup, "0")
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return fmt.Errorf("xgroup create: %w", err)
	}
	return nil
}

// ReadNew blocks up to BlockDuration and returns at most max newly dispatched
// messages (those not yet assigned to any consumer).  Returns nil, nil on timeout.
func (c *Consumer) ReadNew(ctx context.Context, max int64) ([]stream.Message, error) {
	if max < 1 {
		max = 1
	}
	args := &redis.XReadGroupArgs{
		Group:    c.cfg.ConsumerGroup,
		Consumer: c.cfg.ConsumerName,
		Streams:  []string{c.cfg.StreamName, ">"},
		Count:    max,
		Block:    c.cfg.BlockDuration,
	}
	res, err := c.rdb.XReadGroup(ctx, args)
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("xreadgroup: %w", err)
	}

	var msgs []stream.Message
	for _, s := range res {
		for _, m := range s.Messages {
			msgs = append(msgs, stream.Message{ID: m.ID, Values: m.Values})
		}
	}
	return msgs, nil
}

// ReclaimOrphaned uses XAUTOCLAIM to take ownership of messages idle longer
// than minIdle, then fetches their delivery counts via XPENDING so callers
// can enforce DLQ limits without an extra round-trip per message.
func (c *Consumer) ReclaimOrphaned(ctx context.Context, minIdle time.Duration) ([]stream.Message, error) {
	raw, _, err := c.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   c.cfg.StreamName,
		Group:    c.cfg.ConsumerGroup,
		Consumer: c.cfg.ConsumerName,
		MinIdle:  minIdle,
		Start:    "0-0",
		Count:    c.cfg.ReclaimCount,
	})
	if err != nil {
		return nil, fmt.Errorf("xautoclaim: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}

	// Fetch delivery counts for all claimed messages in one XPENDING range.
	// XAUTOCLAIM already incremented each counter before returning.
	counts := c.fetchDeliveryCounts(ctx, raw)

	msgs := make([]stream.Message, len(raw))
	for i, m := range raw {
		msgs[i] = stream.Message{
			ID:            m.ID,
			Values:        m.Values,
			DeliveryCount: counts[m.ID],
		}
	}
	return msgs, nil
}

// fetchDeliveryCounts queries XPENDING for the given set of claimed messages
// and returns a map of message ID → delivery count.  On error it logs a
// warning and returns an empty map so the caller degrades gracefully.
func (c *Consumer) fetchDeliveryCounts(ctx context.Context, msgs []redis.XMessage) map[string]int64 {
	pendingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	pending, err := c.rdb.XPendingExt(pendingCtx, &redis.XPendingExtArgs{
		Stream:   c.cfg.StreamName,
		Group:    c.cfg.ConsumerGroup,
		Start:    msgs[0].ID,
		End:      msgs[len(msgs)-1].ID,
		Count:    int64(len(msgs)),
		Consumer: c.cfg.ConsumerName,
	})
	if err != nil {
		c.logger.Warn().Err(err).Msg("xpending failed; delivery counts unavailable for reclaimed messages")
		return make(map[string]int64)
	}

	counts := make(map[string]int64, len(pending))
	for _, p := range pending {
		counts[p.ID] = p.RetryCount
	}
	return counts
}

// Ack removes the message from the consumer group's PEL (XACK) and deletes
// it from the stream (XDEL) to reclaim memory.
func (c *Consumer) Ack(ctx context.Context, msgID string) error {
	_, _ = c.rdb.XAck(ctx, c.cfg.StreamName, c.cfg.ConsumerGroup, msgID)
	_, _ = c.rdb.XDel(ctx, c.cfg.StreamName, msgID)
	return nil
}

// Close is a no-op: the Consumer does not own the underlying RedisStream.
func (c *Consumer) Close() error { return nil }
