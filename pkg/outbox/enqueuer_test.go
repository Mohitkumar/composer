package outbox

import (
	"context"
	"testing"
	"time"
)

func TestBackoffGrowsWithJitterAndFloor(t *testing.T) {
	for attempt := 1; attempt <= 5; attempt++ {
		ceiling := time.Duration(5+5*(1<<(attempt-1))) * time.Second
		for range 50 {
			d := computeBackoffDuration(5, attempt)
			if d < 5*time.Second || d > ceiling {
				t.Fatalf("attempt %d: %v outside [5s, %v]", attempt, d, ceiling)
			}
		}
	}
	if computeBackoffDuration(0, 1) < 5*time.Second {
		t.Fatal("a non-positive base falls back to 5s")
	}
}

type recordingPublisher struct {
	stream string
	values map[string]any
}

func (p *recordingPublisher) Publish(_ context.Context, stream string, values map[string]any) error {
	p.stream, p.values = stream, values
	return nil
}

func TestPublishRowDecodesPayload(t *testing.T) {
	pub := &recordingPublisher{}
	o := NewOutboxEnqueuer(EnqueuerConfig{Publisher: pub})

	if err := o.publishRow(context.Background(), outboxRow{ID: "1", Stream: "workflow.tasks.http.0",
		Payload: []byte(`{"task_id": "t1", "attempt": 2}`)}); err != nil {
		t.Fatal(err)
	}
	if pub.stream != "workflow.tasks.http.0" || pub.values["task_id"] != "t1" {
		t.Fatalf("published %s %v", pub.stream, pub.values)
	}

	_ = o.publishRow(context.Background(), outboxRow{ID: "2", Payload: []byte(`not json`)})
	if pub.stream != "workflow.tasks" || pub.values["data"] != "not json" {
		t.Fatalf("non-JSON payloads are wrapped and a missing stream defaults: %s %v", pub.stream, pub.values)
	}
}
