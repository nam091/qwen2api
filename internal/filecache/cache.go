// Package filecache provides an in-memory LRU cache for file contents keyed by
// (api_key, file_path). It detects "File unchanged since last read" hints from
// Claude Code and replaces them with the real cached content.
package filecache

import (
	"container/list"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DefaultMaxEntries is the default LRU capacity.
const DefaultMaxEntries = 200

// DefaultTTL is the default time-to-live for cache entries.
const DefaultTTL = 15 * time.Minute

var unchangedRe = regexp.MustCompile(`(?i)(file unchanged since last read|content unchanged|<file_unchanged>)`)

type entry struct {
	key     string
	content string
	expires time.Time
}

// Cache is a thread-safe LRU cache for file content.
type Cache struct {
	mu         sync.Mutex
	maxEntries int
	ttl        time.Duration
	ll         *list.List
	items      map[string]*list.Element
}

// New creates a new Cache.
func New(maxEntries int, ttl time.Duration) *Cache {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Cache{
		maxEntries: maxEntries,
		ttl:        ttl,
		ll:         list.New(),
		items:      make(map[string]*list.Element),
	}
}

func cacheKey(apiKey, filePath string) string {
	return apiKey + "\x00" + filePath
}

// Put stores file content in the cache.
func (c *Cache) Put(apiKey, filePath, content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := cacheKey(apiKey, filePath)
	if el, ok := c.items[k]; ok {
		c.ll.MoveToFront(el)
		el.Value.(*entry).content = content
		el.Value.(*entry).expires = time.Now().Add(c.ttl)
		return
	}
	e := &entry{key: k, content: content, expires: time.Now().Add(c.ttl)}
	el := c.ll.PushFront(e)
	c.items[k] = el
	for c.ll.Len() > c.maxEntries {
		c.evictOldest()
	}
}

// Get retrieves cached content. Returns ("", false) on miss or expiry.
func (c *Cache) Get(apiKey, filePath string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := cacheKey(apiKey, filePath)
	el, ok := c.items[k]
	if !ok {
		return "", false
	}
	e := el.Value.(*entry)
	if time.Now().After(e.expires) {
		c.removeElement(el)
		return "", false
	}
	c.ll.MoveToFront(el)
	return e.content, true
}

// Len returns the number of cached entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

func (c *Cache) evictOldest() {
	el := c.ll.Back()
	if el == nil {
		return
	}
	c.removeElement(el)
}

func (c *Cache) removeElement(el *list.Element) {
	c.ll.Remove(el)
	delete(c.items, el.Value.(*entry).key)
}

// IsUnchangedHint checks if text looks like a "file unchanged" placeholder.
func IsUnchangedHint(text string) bool {
	return unchangedRe.MatchString(text)
}

// ReplaceUnchangedHints scans text for file-unchanged placeholders and replaces
// them with cached content when available.
func (c *Cache) ReplaceUnchangedHints(apiKey, filePath, text string) string {
	if !IsUnchangedHint(text) {
		return text
	}
	content, ok := c.Get(apiKey, filePath)
	if !ok {
		return text
	}
	return unchangedRe.ReplaceAllString(text, strings.ReplaceAll(content, "$", "$$"))
}
