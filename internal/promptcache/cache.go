// Package promptcache caches Qwen chat sessions keyed by a hash of the request
// payload, so repeated prompts can reuse the same chat_id and skip the
// /api/v2/chats/new round-trip.
package promptcache

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

type entry struct {
	chatID    string
	expiresAt time.Time
	hits      int
}

// Cache stores chat_id keyed by a deterministic hash of the inbound request.
// Safe for concurrent use.
type Cache struct {
	mu      sync.Mutex
	entries map[string]*entry
	order   []string
	max     int
	ttl     time.Duration
	hits    int64
	misses  int64
}

// New constructs a cache with the given capacity and TTL. If max <= 0, the
// cache is disabled (every Get returns a miss and Put is a no-op).
func New(max int, ttl time.Duration) *Cache {
	if max < 0 {
		max = 0
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &Cache{
		entries: make(map[string]*entry),
		max:     max,
		ttl:     ttl,
	}
}

// Key derives a stable cache key from the model and collapsed message payload.
func Key(model, payload string) string {
	h := sha256.New()
	h.Write([]byte(model))
	h.Write([]byte{0})
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

// Get returns the cached chat_id, or "" on miss.
func (c *Cache) Get(key string) (string, bool) {
	if c == nil || c.max == 0 {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		c.misses++
		return "", false
	}
	if time.Now().After(e.expiresAt) {
		c.removeLocked(key)
		c.misses++
		return "", false
	}
	e.hits++
	c.hits++
	return e.chatID, true
}

// Put stores chat_id under the given key. Evicts oldest entries when at capacity.
func (c *Cache) Put(key, chatID string) {
	if c == nil || c.max == 0 || chatID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.entries[key]; ok {
		existing.chatID = chatID
		existing.expiresAt = time.Now().Add(c.ttl)
		return
	}
	for len(c.entries) >= c.max && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
	c.entries[key] = &entry{
		chatID:    chatID,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.order = append(c.order, key)
}

// Stats returns counters for observability.
func (c *Cache) Stats() (size int, hits, misses int64) {
	if c == nil {
		return 0, 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.hits, c.misses
}

// Invalidate removes one cached entry.
func (c *Cache) Invalidate(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(key)
}

func (c *Cache) removeLocked(key string) {
	if _, ok := c.entries[key]; !ok {
		return
	}
	delete(c.entries, key)
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}
