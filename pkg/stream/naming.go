package stream

import (
	"fmt"
	"hash/fnv"
)

const (
	// TaskStreamPrefix prefixes every task stream: workflow.tasks.<queue>.<shard>.
	TaskStreamPrefix = "workflow.tasks."
	// DefaultShards is the number of streams per queue when not configured.
	DefaultShards = 4
	// ShardsKey is the Redis key where the coordinator publishes its shard
	// count, so workers read exactly the streams the coordinator writes.
	ShardsKey = "composer:stream_shards"
)

// TaskStream returns the stream name for one shard of a task queue. Each shard
// is its own key, so in Redis Cluster the shards of a queue spread across nodes.
func TaskStream(queue string, shard int) string {
	return fmt.Sprintf("%s%s.%d", TaskStreamPrefix, queue, shard)
}

// ShardFor picks the shard for a task. Any stable spread works: shards only
// distribute load, they carry no ordering guarantee.
func ShardFor(taskID string, shards int) int {
	if shards <= 1 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(taskID))
	return int(h.Sum32() % uint32(shards))
}

// QueueStreams returns every shard stream of the given queues.
func QueueStreams(queues []string, shards int) []string {
	if shards < 1 {
		shards = 1
	}
	out := make([]string, 0, len(queues)*shards)
	for _, q := range queues {
		for s := range shards {
			out = append(out, TaskStream(q, s))
		}
	}
	return out
}
