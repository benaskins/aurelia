//go:build darwin

package gpu

import (
	"context"
	"testing"
	"time"
)

func TestQueryGPU(t *testing.T) {
	info := queryGPU()

	if info.Name == "" {
		t.Error("expected GPU name")
	}
	t.Logf("GPU: %s", info.Name)
	t.Logf("Allocated: %.1f GB", info.AllocatedGB())
	t.Logf("Recommended Max: %.1f GB", info.RecommendedMaxGB())
	t.Logf("Usage: %.1f%%", info.UsagePercent)
	t.Logf("Thermal: %s", info.ThermalState)
	t.Logf("Unified Memory: %v", info.HasUnifiedMemory)

	if info.RecommendedMax == 0 {
		t.Error("expected non-zero recommended max")
	}

	if info.ThermalState == "" {
		t.Error("expected thermal state")
	}
}

func TestObserver(t *testing.T) {
	o := NewObserver(100 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	o.Start(ctx)
	defer o.Stop()

	// Should have data immediately after Start
	info := o.Info()
	if info.Name == "" {
		t.Error("expected GPU name from observer")
	}
	if info.Timestamp.IsZero() {
		t.Error("expected timestamp")
	}

	// Wait for a poll cycle
	time.Sleep(200 * time.Millisecond)

	info2 := o.Info()
	if info2.Timestamp.Before(info.Timestamp) {
		t.Error("expected timestamp to advance")
	}
}

func TestInfoHelpers(t *testing.T) {
	info := Info{
		AllocatedBytes: 48 * 1024 * 1024 * 1024, // 48 GB
		RecommendedMax: 64 * 1024 * 1024 * 1024, // 64 GB
	}

	if info.AllocatedGB() != 48.0 {
		t.Errorf("expected 48.0 GB, got %.1f", info.AllocatedGB())
	}
	if info.RecommendedMaxGB() != 64.0 {
		t.Errorf("expected 64.0 GB, got %.1f", info.RecommendedMaxGB())
	}
}
