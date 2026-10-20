package main

import (
	"slices"
	"testing"
)

func TestWorkerQueues(t *testing.T) {
	t.Setenv("WORKER_QUEUES", "")
	got := workerQueues(map[string]string{"python": "a", "js": "b"})
	slices.Sort(got)
	if !slices.Equal(got, []string{"http", "js", "python"}) {
		t.Fatalf("default queues: %v", got)
	}

	t.Setenv("WORKER_QUEUES", " GPU, http ,,")
	if got := workerQueues(nil); !slices.Equal(got, []string{"gpu", "http"}) {
		t.Fatalf("explicit queues: %v", got)
	}
}

func TestEnvInt(t *testing.T) {
	t.Setenv("N", "12")
	if envInt("N", 3) != 12 {
		t.Fatal("parsed value")
	}
	for _, bad := range []string{"", "-1", "0", "x"} {
		t.Setenv("N", bad)
		if envInt("N", 3) != 3 {
			t.Fatalf("%q must fall back to the default", bad)
		}
	}
}
