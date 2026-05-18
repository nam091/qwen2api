// Package metrics implements a small Prometheus-compatible registry for
// qwen2api. It avoids the prometheus/client_golang dependency to keep the
// runtime tree minimal.
package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Registry tracks counters, gauges, and histograms.
type Registry struct {
	mu         sync.RWMutex
	counters   map[string]*Counter
	gauges     map[string]*Gauge
	histograms map[string]*Histogram
}

// New constructs an empty Registry.
func New() *Registry {
	return &Registry{
		counters:   make(map[string]*Counter),
		gauges:     make(map[string]*Gauge),
		histograms: make(map[string]*Histogram),
	}
}

// Counter is a monotonically increasing value.
type Counter struct {
	v atomic.Int64
}

// Inc adds 1 to the counter.
func (c *Counter) Inc() { c.v.Add(1) }

// Add adds n to the counter.
func (c *Counter) Add(n int64) { c.v.Add(n) }

// Value returns the current count.
func (c *Counter) Value() int64 { return c.v.Load() }

// Gauge is a value that can go up or down.
type Gauge struct {
	v atomic.Int64
}

// Set sets the gauge to v.
func (g *Gauge) Set(v int64) { g.v.Store(v) }

// Inc adds 1.
func (g *Gauge) Inc() { g.v.Add(1) }

// Dec subtracts 1.
func (g *Gauge) Dec() { g.v.Add(-1) }

// Value returns the current value.
func (g *Gauge) Value() int64 { return g.v.Load() }

// Histogram tracks a distribution with predefined buckets (in seconds).
type Histogram struct {
	mu      sync.Mutex
	buckets []float64
	counts  []int64
	sum     float64
	count   int64
}

// DefaultBuckets covers latencies from 5ms to 60s.
var DefaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

// Observe records a value (in seconds).
func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sum += v
	h.count++
	for i, b := range h.buckets {
		if v <= b {
			h.counts[i]++
		}
	}
}

// Counter returns or creates the named counter.
func (r *Registry) Counter(name string) *Counter {
	r.mu.RLock()
	c, ok := r.counters[name]
	r.mu.RUnlock()
	if ok {
		return c
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.counters[name]; ok {
		return c
	}
	c = &Counter{}
	r.counters[name] = c
	return c
}

// Gauge returns or creates the named gauge.
func (r *Registry) Gauge(name string) *Gauge {
	r.mu.RLock()
	g, ok := r.gauges[name]
	r.mu.RUnlock()
	if ok {
		return g
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.gauges[name]; ok {
		return g
	}
	g = &Gauge{}
	r.gauges[name] = g
	return g
}

// Histogram returns or creates the named histogram with default buckets.
func (r *Registry) Histogram(name string) *Histogram {
	r.mu.RLock()
	h, ok := r.histograms[name]
	r.mu.RUnlock()
	if ok {
		return h
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.histograms[name]; ok {
		return h
	}
	h = &Histogram{
		buckets: append([]float64(nil), DefaultBuckets...),
		counts:  make([]int64, len(DefaultBuckets)),
	}
	r.histograms[name] = h
	return h
}

// Render returns the registry's contents in Prometheus text exposition format.
func (r *Registry) Render() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var sb strings.Builder

	names := make([]string, 0, len(r.counters))
	for n := range r.counters {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&sb, "# TYPE %s counter\n%s %d\n", n, n, r.counters[n].Value())
	}

	names = names[:0]
	for n := range r.gauges {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&sb, "# TYPE %s gauge\n%s %d\n", n, n, r.gauges[n].Value())
	}

	names = names[:0]
	for n := range r.histograms {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h := r.histograms[n]
		h.mu.Lock()
		fmt.Fprintf(&sb, "# TYPE %s histogram\n", n)
		var cumulative int64
		for i, b := range h.buckets {
			cumulative += h.counts[i]
			_ = cumulative
			fmt.Fprintf(&sb, "%s_bucket{le=\"%g\"} %d\n", n, b, h.counts[i])
		}
		fmt.Fprintf(&sb, "%s_bucket{le=\"+Inf\"} %d\n", n, h.count)
		fmt.Fprintf(&sb, "%s_sum %g\n", n, h.sum)
		fmt.Fprintf(&sb, "%s_count %d\n", n, h.count)
		h.mu.Unlock()
	}
	return sb.String()
}

// Snapshot returns a JSON-friendly view for the dashboard.
func (r *Registry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := Snapshot{
		Counters: make(map[string]int64, len(r.counters)),
		Gauges:   make(map[string]int64, len(r.gauges)),
	}
	for n, c := range r.counters {
		s.Counters[n] = c.Value()
	}
	for n, g := range r.gauges {
		s.Gauges[n] = g.Value()
	}
	return s
}

// Snapshot is a point-in-time copy of registry values.
type Snapshot struct {
	Counters map[string]int64 `json:"counters"`
	Gauges   map[string]int64 `json:"gauges"`
}
