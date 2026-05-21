// Package stream defines the messaging abstractions used by the workflow
// engine.  Concrete implementations live in sub-packages (e.g. stream/redis)
// so that the coordinator, outbox, and worker never import a transport
// directly.
package stream

import (
	"context"
	"time"
)

// Message is a transport-independent representation of a single stream entry.
type Message struct {
	// ID is the stream-assigned message identifier (e.g. Redis stream ID or
	// Kafka offset string).  It must be passed back to Ack unchanged.
	ID string

	// Values contains the key/value pairs published with the message.
	Values map[string]any

	// DeliveryCount is how many times this message has been delivered.
	// 1 = first delivery; >1 = redelivery after a previous consumer failed.
	// Populated only for messages returned by ReclaimOrphaned.
	DeliveryCount int64
}

// Publisher writes messages to a named stream.
// Implementations must be safe for concurrent use.
type Publisher interface {
	// Publish appends values to the named stream.  The call is synchronous;
	// a nil error means the message is durably stored in the stream.
	Publish(ctx context.Context, stream string, values map[string]any) error
}

// Consumer reads messages from a stream with consumer-group semantics,
// providing at-least-once delivery, orphan reclaim, and delivery-count
// tracking needed for DLQ enforcement.
// Implementations must be safe for concurrent use.
type Consumer interface {
	// Setup creates the consumer group (and the stream itself if absent).
	// Idempotent — safe to call on every startup.
	Setup(ctx context.Context) error

	// ReadNew blocks up to a short internal timeout and returns messages that
	// have not yet been assigned to any consumer in the group.  Returns nil,
	// nil when the timeout elapses with no new messages.
	ReadNew(ctx context.Context) ([]Message, error)

	// ReclaimOrphaned takes ownership of messages that have been pending
	// (delivered but not yet acked) for longer than minIdle — typically
	// because the original consumer crashed.  Each returned Message has
	// DeliveryCount populated so the caller can enforce DLQ limits.
	ReclaimOrphaned(ctx context.Context, minIdle time.Duration) ([]Message, error)

	// Ack marks a message as successfully processed and permanently removes
	// it from the stream.  Must be called only after the message has been
	// durably handled end-to-end.
	Ack(ctx context.Context, msgID string) error

	// Close releases all underlying resources (connections, goroutines).
	Close() error
}
