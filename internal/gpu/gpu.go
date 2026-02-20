// Package gpu provides GPU observability for Apple Silicon Macs.
//
// Exposes VRAM usage via Metal framework and thermal state via IOKit.
// Polled periodically and cached — not real-time.
package gpu

import (
	"context"
	"sync"
	"time"
)

// Info holds a snapshot of GPU state.
type Info struct {
	Name             string    `json:"name"`
	AllocatedBytes   uint64    `json:"allocated_bytes"`
	RecommendedMax   uint64    `json:"recommended_max_bytes"`
	UsagePercent     float64   `json:"usage_percent"`
	ThermalState     string    `json:"thermal_state"` // "nominal", "fair", "serious", "critical"
	HasUnifiedMemory bool      `json:"has_unified_memory"`
	Timestamp        time.Time `json:"timestamp"`
}

// AllocatedGB returns allocated memory in gigabytes.
func (i Info) AllocatedGB() float64 {
	return float64(i.AllocatedBytes) / (1024 * 1024 * 1024)
}

// RecommendedMaxGB returns recommended max working set in gigabytes.
func (i Info) RecommendedMaxGB() float64 {
	return float64(i.RecommendedMax) / (1024 * 1024 * 1024)
}

// Observer periodically polls GPU state and caches the result.
type Observer struct {
	mu       sync.RWMutex
	info     Info
	interval time.Duration
	cancel   context.CancelFunc
}

// NewObserver creates a GPU observer that polls at the given interval.
func NewObserver(interval time.Duration) *Observer {
	return &Observer{
		interval: interval,
	}
}

// Start begins polling GPU state in the background.
func (o *Observer) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	o.mu.Lock()
	o.cancel = cancel
	o.mu.Unlock()

	// Initial poll
	o.poll()

	go func() {
		ticker := time.NewTicker(o.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				o.poll()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop stops the observer.
func (o *Observer) Stop() {
	o.mu.Lock()
	cancel := o.cancel
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Info returns the latest cached GPU info.
func (o *Observer) Info() Info {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.info
}

// QueryNow returns a one-shot GPU info snapshot.
func QueryNow() Info {
	info := queryGPU()
	info.Timestamp = time.Now()
	return info
}

func (o *Observer) poll() {
	info := QueryNow()

	o.mu.Lock()
	o.info = info
	o.mu.Unlock()
}
