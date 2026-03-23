package multinode

import (
	"sync"
	"testing"
	"time"
)

func TestTimingCollectorRecord(t *testing.T) {
	tc := NewTimingCollector()
	tc.Record("op-a", 10*time.Millisecond)
	tc.Record("op-a", 20*time.Millisecond)
	tc.Record("op-b", 5*time.Millisecond)

	a := tc.Samples("op-a")
	if len(a) != 2 {
		t.Fatalf("op-a: len = %d, want 2", len(a))
	}
	b := tc.Samples("op-b")
	if len(b) != 1 {
		t.Fatalf("op-b: len = %d, want 1", len(b))
	}
}

func TestTimingCollectorTime(t *testing.T) {
	tc := NewTimingCollector()
	done := tc.Time("sleep-op")
	time.Sleep(10 * time.Millisecond)
	done()

	samples := tc.Samples("sleep-op")
	if len(samples) != 1 {
		t.Fatalf("len = %d, want 1", len(samples))
	}
	if samples[0] < 10*time.Millisecond {
		t.Errorf("duration = %v, expected >= 10ms", samples[0])
	}
}

func TestTimingCollectorConcurrent(t *testing.T) {
	tc := NewTimingCollector()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tc.Record("concurrent", time.Millisecond)
		}()
	}
	wg.Wait()

	samples := tc.Samples("concurrent")
	if len(samples) != 100 {
		t.Fatalf("len = %d, want 100", len(samples))
	}
}

func TestTimingCollectorReport(t *testing.T) {
	tc := NewTimingCollector()
	for i := 0; i < 20; i++ {
		tc.Record("report-op", time.Duration(i+1)*time.Millisecond)
	}
	// Should not panic, output visible with -v
	tc.Report(t)
}

func TestTimingCollectorSamplesIsolation(t *testing.T) {
	tc := NewTimingCollector()
	tc.Record("iso", 5*time.Millisecond)

	samples := tc.Samples("iso")
	samples[0] = 999 * time.Second // mutate the copy

	original := tc.Samples("iso")
	if original[0] != 5*time.Millisecond {
		t.Errorf("mutation leaked into collector: got %v", original[0])
	}
}
