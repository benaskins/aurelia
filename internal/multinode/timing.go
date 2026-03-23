package multinode

import (
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

// TimingCollector records operation durations for performance analysis.
// Safe for concurrent use.
type TimingCollector struct {
	mu      sync.Mutex
	samples map[string][]time.Duration
}

// NewTimingCollector creates a new collector.
func NewTimingCollector() *TimingCollector {
	return &TimingCollector{
		samples: make(map[string][]time.Duration),
	}
}

// Record stores a duration for the named operation.
func (tc *TimingCollector) Record(op string, d time.Duration) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.samples[op] = append(tc.samples[op], d)
}

// Time returns a function that, when called, records the elapsed time since Time was called.
// Usage: done := tc.Time("my-op"); /* do work */; done()
func (tc *TimingCollector) Time(op string) func() {
	start := time.Now()
	return func() {
		tc.Record(op, time.Since(start))
	}
}

// Report logs p50, p95, and max for each operation.
func (tc *TimingCollector) Report(t *testing.T) {
	t.Helper()
	tc.mu.Lock()
	defer tc.mu.Unlock()

	ops := make([]string, 0, len(tc.samples))
	for op := range tc.samples {
		ops = append(ops, op)
	}
	sort.Strings(ops)

	for _, op := range ops {
		samples := tc.samples[op]
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

		n := len(samples)
		p50 := samples[n/2]
		p95 := samples[int(float64(n)*0.95)]
		max := samples[n-1]

		t.Logf("timing: %-30s  n=%-4d  p50=%-10s  p95=%-10s  max=%s",
			op, n, fmtDuration(p50), fmtDuration(p95), fmtDuration(max))
	}
}

// Samples returns a copy of all recorded samples for an operation.
func (tc *TimingCollector) Samples(op string) []time.Duration {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	src := tc.samples[op]
	dst := make([]time.Duration, len(src))
	copy(dst, src)
	return dst
}

func fmtDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dus", d.Microseconds())
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}
