// Package affinity binds API sessions (identified by api_key + first user
// message) to specific upstream Qwen accounts and chat IDs. This ensures
// multi-turn conversations reuse the same account.
package affinity

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// DefaultTTL is the default expiry for affinity records.
const DefaultTTL = 2 * time.Hour

// Record represents a session → account binding.
type Record struct {
	SessionKey  string    `json:"session_key"`
	TokenValue  string    `json:"token_value"` // upstream Qwen token
	ChatID      string    `json:"chat_id"`
	UploadedFiles []string `json:"uploaded_files,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsed    time.Time `json:"last_used"`
}

// Store holds session affinity records in memory.
type Store struct {
	mu      sync.RWMutex
	records map[string]*Record
	ttl     time.Duration
}

// NewStore creates a session affinity store.
func NewStore(ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Store{
		records: make(map[string]*Record),
		ttl:     ttl,
	}
}

// DeriveSessionKey generates a session key from client identity fields.
func DeriveSessionKey(apiKey, firstUserText string) string {
	// Truncate first user text to 400 chars for key derivation
	text := firstUserText
	if len(text) > 400 {
		text = text[:400]
	}
	h := sha256.Sum256([]byte(apiKey + "\x00" + text))
	return hex.EncodeToString(h[:16])
}

// Lookup finds an existing affinity record.
func (s *Store) Lookup(sessionKey string) (*Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.records[sessionKey]
	if !ok {
		return nil, false
	}
	if time.Since(r.LastUsed) > s.ttl {
		return nil, false
	}
	return r, true
}

// Bind creates or updates a session affinity record.
func (s *Store) Bind(sessionKey, tokenValue, chatID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if r, ok := s.records[sessionKey]; ok {
		r.TokenValue = tokenValue
		if chatID != "" {
			r.ChatID = chatID
		}
		r.LastUsed = now
		return
	}
	s.records[sessionKey] = &Record{
		SessionKey: sessionKey,
		TokenValue: tokenValue,
		ChatID:     chatID,
		CreatedAt:  now,
		LastUsed:   now,
	}
}

// AddFile tracks an uploaded file for a session.
func (s *Store) AddFile(sessionKey, fileRef string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[sessionKey]
	if !ok {
		return
	}
	r.UploadedFiles = append(r.UploadedFiles, fileRef)
	r.LastUsed = time.Now()
}

// CleanExpired removes stale records.
func (s *Store) CleanExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	removed := 0
	for k, r := range s.records {
		if now.Sub(r.LastUsed) > s.ttl {
			delete(s.records, k)
			removed++
		}
	}
	return removed
}

// Len returns the number of tracked sessions.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}

// All returns a snapshot of all records for admin inspection.
func (s *Store) All() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, *r)
	}
	return out
}
