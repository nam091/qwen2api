package fingerprint

import "testing"

func TestNewGenerator(t *testing.T) {
	g := NewGenerator()
	p := g.Get()
	if p == nil {
		t.Fatal("expected non-nil profile")
	}
	if p.UserAgent == "" {
		t.Error("expected non-empty user agent")
	}
	if p.CanvasHash == "" {
		t.Error("expected non-empty canvas hash")
	}
	if p.Platform == "" {
		t.Error("expected non-empty platform")
	}
}

func TestRotate(t *testing.T) {
	g := NewGenerator()
	p1 := g.Get()
	p2 := g.Rotate()
	if p1 == p2 {
		t.Error("rotated profile should be a new object")
	}
	if p2.CanvasHash == "" {
		t.Error("rotated profile should have canvas hash")
	}
}

func TestApplyHeaders(t *testing.T) {
	g := NewGenerator()
	p := g.Get()
	headers := make(map[string]string)
	p.ApplyHeaders(headers)
	if headers["User-Agent"] == "" {
		t.Error("expected User-Agent header")
	}
	if headers["Accept-Language"] == "" {
		t.Error("expected Accept-Language header")
	}
	if headers["Sec-Ch-Ua-Platform"] == "" {
		t.Error("expected Sec-Ch-Ua-Platform header")
	}
}
