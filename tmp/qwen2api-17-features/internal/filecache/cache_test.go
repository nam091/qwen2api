package filecache

import (
	"testing"
	"time"
)

func TestPutGet(t *testing.T) {
	c := New(10, time.Minute)
	c.Put("key1", "/path/a.go", "content-a")
	got, ok := c.Get("key1", "/path/a.go")
	if !ok || got != "content-a" {
		t.Errorf("expected content-a, got %q ok=%v", got, ok)
	}
}

func TestExpiry(t *testing.T) {
	c := New(10, 1*time.Millisecond)
	c.Put("k", "/p", "data")
	time.Sleep(5 * time.Millisecond)
	_, ok := c.Get("k", "/p")
	if ok {
		t.Error("expected expired entry to miss")
	}
}

func TestLRUEviction(t *testing.T) {
	c := New(2, time.Minute)
	c.Put("k", "a", "1")
	c.Put("k", "b", "2")
	c.Put("k", "c", "3") // should evict "a"
	_, ok := c.Get("k", "a")
	if ok {
		t.Error("expected 'a' to be evicted")
	}
	if c.Len() != 2 {
		t.Errorf("expected len 2, got %d", c.Len())
	}
}

func TestIsUnchangedHint(t *testing.T) {
	if !IsUnchangedHint("File unchanged since last read") {
		t.Error("should match")
	}
	if !IsUnchangedHint("<file_unchanged>") {
		t.Error("should match XML tag")
	}
	if IsUnchangedHint("normal content here") {
		t.Error("should not match")
	}
}

func TestReplaceUnchangedHints(t *testing.T) {
	c := New(10, time.Minute)
	c.Put("k", "file.go", "real content")
	result := c.ReplaceUnchangedHints("k", "file.go", "Tool result: File unchanged since last read")
	if result != "Tool result: real content" {
		t.Errorf("unexpected: %q", result)
	}
}
