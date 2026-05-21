package ssxmod

import (
	"log/slog"
	"os"
	"testing"
)

func TestGenerate(t *testing.T) {
	cookies := generate()
	if cookies.SsxmodItna == "" {
		t.Error("expected non-empty ssxmod_itna")
	}
	if cookies.SsxmodItna2 == "" {
		t.Error("expected non-empty ssxmod_itna2")
	}
	if cookies.SsxmodItna == cookies.SsxmodItna2 {
		t.Error("itna and itna2 should differ")
	}
	if cookies.Timestamp <= 0 {
		t.Error("expected positive timestamp")
	}
}

func TestManagerGetAndRefresh(t *testing.T) {
	m := NewManager(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	c1 := m.Get()
	if c1.SsxmodItna == "" {
		t.Error("initial cookies should not be empty")
	}
	m.refresh()
	c2 := m.Get()
	// Refreshed cookies should differ (different random data)
	if c1.SsxmodItna == c2.SsxmodItna && c1.SsxmodItna2 == c2.SsxmodItna2 {
		t.Error("refreshed cookies should differ from initial")
	}
}

func TestLZWCompress(t *testing.T) {
	result := lzwCompress("hello world")
	if result == "" {
		t.Error("expected non-empty compressed output")
	}
	// Different inputs should produce different outputs
	result2 := lzwCompress("goodbye world")
	if result == result2 {
		t.Error("different inputs should produce different outputs")
	}
}

func TestLZWCompressEmpty(t *testing.T) {
	result := lzwCompress("")
	if result != "" {
		t.Error("empty input should produce empty output")
	}
}
