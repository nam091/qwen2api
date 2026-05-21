package affinity

import (
	"testing"
	"time"
)

func TestDeriveSessionKey(t *testing.T) {
	k1 := DeriveSessionKey("api-key-1", "hello world")
	k2 := DeriveSessionKey("api-key-1", "hello world")
	k3 := DeriveSessionKey("api-key-2", "hello world")
	if k1 != k2 {
		t.Error("same inputs should produce same key")
	}
	if k1 == k3 {
		t.Error("different API keys should produce different keys")
	}
}

func TestBindAndLookup(t *testing.T) {
	s := NewStore(time.Hour)
	s.Bind("session-1", "token-abc", "chat-123")
	r, ok := s.Lookup("session-1")
	if !ok {
		t.Fatal("expected to find session-1")
	}
	if r.TokenValue != "token-abc" || r.ChatID != "chat-123" {
		t.Errorf("unexpected record: %+v", r)
	}
}

func TestLookupExpired(t *testing.T) {
	s := NewStore(1 * time.Millisecond)
	s.Bind("session-1", "token", "chat")
	time.Sleep(5 * time.Millisecond)
	_, ok := s.Lookup("session-1")
	if ok {
		t.Error("expected expired session to not be found")
	}
}

func TestAddFile(t *testing.T) {
	s := NewStore(time.Hour)
	s.Bind("sess", "tok", "chat")
	s.AddFile("sess", "file-1")
	s.AddFile("sess", "file-2")
	r, ok := s.Lookup("sess")
	if !ok {
		t.Fatal("expected to find session")
	}
	if len(r.UploadedFiles) != 2 {
		t.Errorf("expected 2 files, got %d", len(r.UploadedFiles))
	}
}

func TestCleanExpired(t *testing.T) {
	s := NewStore(1 * time.Millisecond)
	s.Bind("s1", "t1", "c1")
	s.Bind("s2", "t2", "c2")
	time.Sleep(5 * time.Millisecond)
	removed := s.CleanExpired()
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}
	if s.Len() != 0 {
		t.Errorf("expected 0 remaining, got %d", s.Len())
	}
}
