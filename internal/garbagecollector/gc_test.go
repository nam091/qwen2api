package garbagecollector

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestMarkActive(t *testing.T) {
	g := New(slog.New(slog.NewTextHandler(os.Stderr, nil)), time.Minute, time.Hour)
	g.MarkActive("chat-1")
	g.MarkActive("chat-2")
	if g.ActiveCount() != 2 {
		t.Errorf("expected 2 active chats, got %d", g.ActiveCount())
	}
	if !g.IsActive("chat-1") {
		t.Error("chat-1 should be active")
	}
}

func TestRemove(t *testing.T) {
	g := New(slog.New(slog.NewTextHandler(os.Stderr, nil)), time.Minute, time.Hour)
	g.MarkActive("chat-1")
	g.Remove("chat-1")
	if g.IsActive("chat-1") {
		t.Error("chat-1 should not be active after remove")
	}
}

func TestNotActive(t *testing.T) {
	g := New(slog.New(slog.NewTextHandler(os.Stderr, nil)), time.Minute, time.Hour)
	if g.IsActive("nonexistent") {
		t.Error("nonexistent chat should not be active")
	}
}
