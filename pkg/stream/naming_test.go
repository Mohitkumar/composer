package stream_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/mohitkumar/composer/pkg/stream"
)

func TestTaskStreamNames(t *testing.T) {
	if got := stream.TaskStream("http", 2); got != "workflow.tasks.http.2" {
		t.Fatalf("got %s", got)
	}
	got := stream.QueueStreams([]string{"http", "py"}, 2)
	want := []string{"workflow.tasks.http.0", "workflow.tasks.http.1", "workflow.tasks.py.0", "workflow.tasks.py.1"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	if got := stream.QueueStreams([]string{"http"}, 0); len(got) != 1 {
		t.Fatalf("a shard count below 1 means one stream per queue: %v", got)
	}
}

func TestShardForIsStableAndSpreads(t *testing.T) {
	if stream.ShardFor("x", 1) != 0 || stream.ShardFor("x", 0) != 0 {
		t.Fatal("one shard (or none) always maps to 0")
	}
	counts := make([]int, 4)
	for i := range 4000 {
		id := fmt.Sprintf("task-%d", i)
		s := stream.ShardFor(id, 4)
		if s != stream.ShardFor(id, 4) {
			t.Fatal("ShardFor must be deterministic")
		}
		counts[s]++
	}
	for shard, n := range counts {
		if n < 800 { // perfectly even would be 1000
			t.Fatalf("shard %d got only %d of 4000 tasks: %v", shard, n, counts)
		}
	}
}
