package metrics

import (
	"strings"
	"testing"
)

func TestCounter(t *testing.T) {
	r := New()
	c := r.Counter("foo_total")
	c.Inc()
	c.Inc()
	c.Add(3)
	if got := c.Value(); got != 5 {
		t.Errorf("counter=%d want 5", got)
	}
}

func TestGauge(t *testing.T) {
	r := New()
	g := r.Gauge("foo_current")
	g.Set(10)
	g.Inc()
	g.Dec()
	g.Dec()
	if got := g.Value(); got != 9 {
		t.Errorf("gauge=%d want 9", got)
	}
}

func TestHistogram(t *testing.T) {
	r := New()
	h := r.Histogram("foo_seconds")
	h.Observe(0.001)
	h.Observe(0.5)
	h.Observe(2)
	h.Observe(60)

	out := r.Render()
	if !strings.Contains(out, "foo_seconds_count 4") {
		t.Errorf("render missing count: %s", out)
	}
	if !strings.Contains(out, "foo_seconds_sum") {
		t.Errorf("render missing sum: %s", out)
	}
}

func TestRenderFormat(t *testing.T) {
	r := New()
	r.Counter("requests_total").Inc()
	r.Gauge("active").Set(2)
	out := r.Render()
	if !strings.Contains(out, "# TYPE requests_total counter") {
		t.Errorf("missing counter header: %s", out)
	}
	if !strings.Contains(out, "requests_total 1") {
		t.Errorf("missing counter value: %s", out)
	}
	if !strings.Contains(out, "# TYPE active gauge") {
		t.Errorf("missing gauge header: %s", out)
	}
}

func TestSnapshot(t *testing.T) {
	r := New()
	r.Counter("a_total").Add(5)
	r.Gauge("b").Set(3)
	s := r.Snapshot()
	if s.Counters["a_total"] != 5 || s.Gauges["b"] != 3 {
		t.Errorf("snapshot wrong: %+v", s)
	}
}
