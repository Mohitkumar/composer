package util

import (
	"bufio"
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/metric"
)

// SampleProcessMetrics records RSS memory, Go heap in-use, and goroutine count.
// Called from each service's periodic sampler goroutine.
func SampleProcessMetrics(ctx context.Context) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	if ProcessHeapInuseGauge != nil {
		ProcessHeapInuseGauge.Record(ctx, int64(ms.HeapInuse))
	}
	if ProcessGoroutinesGauge != nil {
		ProcessGoroutinesGauge.Record(ctx, int64(runtime.NumGoroutine()))
	}
	if ProcessRSSGauge != nil {
		if rss := readRSSBytes(); rss > 0 {
			ProcessRSSGauge.Record(ctx, rss)
		} else {
			// Fallback: use Go's Sys (virtual memory mapped) when /proc is unavailable (macOS dev)
			ProcessRSSGauge.Record(ctx, int64(ms.Sys), metric.WithAttributes())
		}
	}
}

// readRSSBytes reads the resident set size from /proc/self/status (Linux only).
// Returns 0 on non-Linux or on any read error.
func readRSSBytes() int64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseInt(fields[1], 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}
