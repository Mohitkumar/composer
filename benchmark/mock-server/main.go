package main

import (
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"
)

var requestCounter int64

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", health)
	mux.HandleFunc("/api/fast", fast)
	mux.HandleFunc("/api/data", data)
	mux.HandleFunc("/api/process", process)
	mux.HandleFunc("/api/enrich", enrich)
	mux.HandleFunc("/api/validate", validate)
	mux.HandleFunc("/api/notify", notify)
	mux.HandleFunc("/api/transform", transform)
	mux.HandleFunc("/api/slow", slow)

	log.Println("mock-server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func respond(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func readBody(r *http.Request) map[string]any {
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	if body == nil {
		body = map[string]any{}
	}
	return body
}

func health(w http.ResponseWriter, r *http.Request) {
	respond(w, map[string]any{"status": "ok", "ts": time.Now().UnixMilli()})
}

// fast: ~5ms — used by http-simple as baseline
func fast(w http.ResponseWriter, r *http.Request) {
	time.Sleep(jitter(5, 2))
	n := atomic.AddInt64(&requestCounter, 1)
	respond(w, map[string]any{
		"ok":      true,
		"latency": "fast",
		"seq":     n,
		"ts":      time.Now().UnixMilli(),
	})
}

// data: ~20ms — used as the entry point for most workflows; always returns value>=75 for SWITCH routing
func data(w http.ResponseWriter, r *http.Request) {
	time.Sleep(jitter(20, 5))
	respond(w, map[string]any{
		"id":     "item-001",
		"value":  100,
		"status": "premium",
		"items":  []string{"a", "b", "c"},
		"ts":     time.Now().UnixMilli(),
	})
}

// process: ~50ms — general processing step
func process(w http.ResponseWriter, r *http.Request) {
	time.Sleep(jitter(50, 10))
	body := readBody(r)
	body["processed"] = true
	body["processed_at"] = time.Now().UnixMilli()
	respond(w, body)
}

// enrich: ~30ms — data enrichment branch
func enrich(w http.ResponseWriter, r *http.Request) {
	time.Sleep(jitter(30, 5))
	body := readBody(r)
	body["enriched"] = true
	body["score"] = 90 + rand.Intn(10)
	body["enriched_at"] = time.Now().UnixMilli()
	respond(w, body)
}

// validate: ~10ms — fast validation branch
func validate(w http.ResponseWriter, r *http.Request) {
	time.Sleep(jitter(10, 3))
	respond(w, map[string]any{
		"valid":        true,
		"message":      "validation passed",
		"validated_at": time.Now().UnixMilli(),
	})
}

// notify: ~15ms — terminal notification step
func notify(w http.ResponseWriter, r *http.Request) {
	time.Sleep(jitter(15, 3))
	n := atomic.AddInt64(&requestCounter, 1)
	respond(w, map[string]any{
		"notified":        true,
		"notification_id": n,
		"notified_at":     time.Now().UnixMilli(),
	})
}

// transform: ~40ms — data transformation branch
func transform(w http.ResponseWriter, r *http.Request) {
	time.Sleep(jitter(40, 8))
	body := readBody(r)
	body["transformed"] = true
	body["transform_version"] = "v1"
	body["transformed_at"] = time.Now().UnixMilli()
	respond(w, body)
}

// slow: ~300ms — simulates a sluggish external dependency
func slow(w http.ResponseWriter, r *http.Request) {
	time.Sleep(jitter(300, 30))
	respond(w, map[string]any{
		"ok":                 true,
		"latency":            "slow",
		"simulated_delay_ms": 300,
		"ts":                 time.Now().UnixMilli(),
	})
}

// jitter adds ±spread ms of random jitter to base ms to make latency realistic
func jitter(base, spread int) time.Duration {
	delta := rand.Intn(2*spread+1) - spread
	d := base + delta
	if d < 1 {
		d = 1
	}
	return time.Duration(d) * time.Millisecond
}
