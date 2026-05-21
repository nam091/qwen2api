// Package garbagecollector periodically deletes stale Qwen chats that were
// created by the API proxy (title prefix "api_") and are no longer in active use.
package garbagecollector

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// DefaultInterval is the default garbage collection interval.
const DefaultInterval = 15 * time.Minute

// ChatPrefix is the title prefix used to identify API-created chats.
const ChatPrefix = "api_"

// ChatDeleter deletes upstream chats. Implemented by the Qwen client.
type ChatDeleter interface {
	DeleteChat(ctx context.Context, token, chatID string) error
	ListChats(ctx context.Context, token string) ([]ChatInfo, error)
}

// ChatInfo represents a minimal chat descriptor.
type ChatInfo struct {
	ID    string
	Title string
}

// GC manages the set of active chat IDs and periodically cleans stale ones.
type GC struct {
	mu        sync.Mutex
	active    map[string]time.Time // chatID -> last used
	logger    *slog.Logger
	interval  time.Duration
	ttl       time.Duration
	stopCh    chan struct{}
}

// New creates a GC.
func New(logger *slog.Logger, interval, ttl time.Duration) *GC {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	return &GC{
		active:   make(map[string]time.Time),
		logger:   logger,
		interval: interval,
		ttl:      ttl,
		stopCh:   make(chan struct{}),
	}
}

// MarkActive marks a chat ID as in-use.
func (g *GC) MarkActive(chatID string) {
	if chatID == "" {
		return
	}
	g.mu.Lock()
	g.active[chatID] = time.Now()
	g.mu.Unlock()
}

// Remove removes a chat ID from tracking.
func (g *GC) Remove(chatID string) {
	g.mu.Lock()
	delete(g.active, chatID)
	g.mu.Unlock()
}

// IsActive checks if a chat is currently active.
func (g *GC) IsActive(chatID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.active[chatID]
	return ok
}

// ActiveCount returns the number of tracked chats.
func (g *GC) ActiveCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.active)
}

// Start runs the GC loop in a goroutine. Provide a function that returns
// tokens to use for listing/deleting chats.
func (g *GC) Start(deleter ChatDeleter, tokenFn func() string) {
	go func() {
		ticker := time.NewTicker(g.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				g.collect(deleter, tokenFn)
			case <-g.stopCh:
				return
			}
		}
	}()
}

// Stop halts the GC loop.
func (g *GC) Stop() {
	select {
	case g.stopCh <- struct{}{}:
	default:
	}
}

func (g *GC) collect(deleter ChatDeleter, tokenFn func() string) {
	token := tokenFn()
	if token == "" {
		return
	}
	// Expire old active entries
	g.mu.Lock()
	now := time.Now()
	for id, last := range g.active {
		if now.Sub(last) > g.ttl {
			delete(g.active, id)
		}
	}
	g.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	chats, err := deleter.ListChats(ctx, token)
	if err != nil {
		g.logger.Warn("gc: list chats failed", "error", err)
		return
	}

	deleted := 0
	for _, c := range chats {
		if len(c.Title) < len(ChatPrefix) {
			continue
		}
		if c.Title[:len(ChatPrefix)] != ChatPrefix {
			continue
		}
		if g.IsActive(c.ID) {
			continue
		}
		if err := deleter.DeleteChat(ctx, token, c.ID); err != nil {
			g.logger.Warn("gc: delete chat failed", "chat_id", c.ID, "error", err)
			continue
		}
		deleted++
	}
	if deleted > 0 {
		g.logger.Info("gc: cleaned stale chats", "deleted", deleted)
	}
}
