package promptcache

import (
	"testing"
	"time"
)

func TestCacheBasicGetPut(t *testing.T) {
	c := New(10, time.Minute)
	if _, ok := c.Get("missing"); ok {
		t.Error("empty cache returned hit")
	}
	c.Put("k1", "chat-1")
	got, ok := c.Get("k1")
	if !ok || got != "chat-1" {
		t.Errorf("get k1 = (%q,%v) want (chat-1,true)", got, ok)
	}
}

func TestCacheTTLExpiry(t *testing.T) {
	c := New(10, 10*time.Millisecond)
	c.Put("k1", "chat-1")
	time.Sleep(20 * time.Millisecond)
	if _, ok := c.Get("k1"); ok {
		t.Error("expired entry returned hit")
	}
}

func TestCacheEviction(t *testing.T) {
	c := New(2, time.Minute)
	c.Put("k1", "chat-1")
	c.Put("k2", "chat-2")
	c.Put("k3", "chat-3")

	size, _, _ := c.Stats()
	if size != 2 {
		t.Errorf("size = %d want 2 after eviction", size)
	}
	if _, ok := c.Get("k1"); ok {
		t.Error("oldest entry should have been evicted")
	}
	if _, ok := c.Get("k3"); !ok {
		t.Error("newest entry missing")
	}
}

func TestCacheStats(t *testing.T) {
	c := New(10, time.Minute)
	c.Put("k1", "chat-1")
	c.Get("k1")
	c.Get("k1")
	c.Get("missing")
	_, hits, misses := c.Stats()
	if hits != 2 || misses != 1 {
		t.Errorf("stats hits=%d misses=%d want 2/1", hits, misses)
	}
}

func TestCacheDisabled(t *testing.T) {
	c := New(0, time.Minute)
	c.Put("k1", "chat-1")
	if _, ok := c.Get("k1"); ok {
		t.Error("disabled cache should never hit")
	}
}

func TestKeyDeterminism(t *testing.T) {
	a := Key("qwen3-max", "hello")
	b := Key("qwen3-max", "hello")
	c := Key("qwen3-max", "world")
	if a != b {
		t.Error("same input produced different keys")
	}
	if a == c {
		t.Error("different input produced same key")
	}
}

func TestInvalidate(t *testing.T) {
	c := New(10, time.Minute)
	c.Put("k1", "chat-1")
	c.Invalidate("k1")
	if _, ok := c.Get("k1"); ok {
		t.Error("invalidated entry returned hit")
	}
}
