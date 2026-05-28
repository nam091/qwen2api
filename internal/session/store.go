package session

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Message is a single turn stored in a session.
type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Session tracks conversation history for a context hash.
type Session struct {
	ID          string    `json:"id"`
	ContextHash string    `json:"context_hash"`
	Summary     string    `json:"summary"`
	Messages    []Message `json:"messages"`
	TurnCount   int       `json:"turn_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Store is an in-memory session store with optional JSON file persistence.
type Store struct {
	mu          sync.RWMutex
	sessions    map[string]*Session
	maxSessions int
	maxHistory  int
	dataDir     string // "" = no persistence
	logger      *slog.Logger
}

// StoreConfig configures the session store.
type StoreConfig struct {
	MaxSessions int
	MaxHistory  int
	DataDir     string
	Logger      *slog.Logger
}

// NewStore creates a session store.
func NewStore(cfg StoreConfig) *Store {
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = 1000
	}
	if cfg.MaxHistory <= 0 {
		cfg.MaxHistory = 20
	}
	s := &Store{
		sessions:    make(map[string]*Session),
		maxSessions: cfg.MaxSessions,
		maxHistory:  cfg.MaxHistory,
		dataDir:     cfg.DataDir,
		logger:      cfg.Logger,
	}
	if cfg.DataDir != "" {
		_ = os.MkdirAll(cfg.DataDir, 0o755)
		s.load()
	}
	return s
}

// ResolveByContextHash finds an existing session or creates a new one.
func (s *Store) ResolveByContextHash(hash string) *Session {
	if hash == "" {
		return nil
	}
	s.mu.RLock()
	if sess, ok := s.sessions[hash]; ok {
		s.mu.RUnlock()
		return sess
	}
	s.mu.RUnlock()
	return s.Create(hash)
}

// Create makes a new session for the given context hash.
func (s *Store) Create(hash string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Evict oldest if at capacity
	if len(s.sessions) >= s.maxSessions {
		s.evictOldest()
	}
	now := time.Now()
	sess := &Session{
		ID:          hash,
		ContextHash: hash,
		Messages:    nil,
		TurnCount:   0,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.sessions[hash] = sess
	return sess
}

// AppendMessages adds messages to a session.
func (s *Store) AppendMessages(hash string, msgs []Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[hash]
	if !ok {
		return
	}
	sess.Messages = append(sess.Messages, msgs...)
	sess.TurnCount++
	sess.UpdatedAt = time.Now()
	// Trim to max history
	if len(sess.Messages) > s.maxHistory {
		sess.Messages = sess.Messages[len(sess.Messages)-s.maxHistory:]
	}
	s.persistSession(sess)
}

// SetSummary updates the rolling summary for a session.
func (s *Store) SetSummary(hash, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[hash]
	if !ok {
		return
	}
	sess.Summary = summary
	sess.UpdatedAt = time.Now()
	s.persistSession(sess)
}

// GetSummary returns the current summary for a session.
func (s *Store) GetSummary(hash string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if sess, ok := s.sessions[hash]; ok {
		return sess.Summary
	}
	return ""
}

// GetMessageCount returns the number of messages in a session.
func (s *Store) GetMessageCount(hash string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if sess, ok := s.sessions[hash]; ok {
		return len(sess.Messages)
	}
	return 0
}

// GetTurnCount returns the turn count for a session.
func (s *Store) GetTurnCount(hash string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if sess, ok := s.sessions[hash]; ok {
		return sess.TurnCount
	}
	return 0
}

func (s *Store) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	for k, sess := range s.sessions {
		if oldestKey == "" || sess.UpdatedAt.Before(oldestTime) {
			oldestKey = k
			oldestTime = sess.UpdatedAt
		}
	}
	if oldestKey != "" {
		delete(s.sessions, oldestKey)
	}
}

func (s *Store) persistSession(sess *Session) {
	if s.dataDir == "" {
		return
	}
	go func() {
		path := filepath.Join(s.dataDir, sess.ID+".json")
		tmp := path + ".tmp"
		data, err := json.MarshalIndent(sess, "", "  ")
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("session marshal failed", "id", sess.ID, "err", err)
			}
			return
		}
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			if s.logger != nil {
				s.logger.Warn("session write failed", "id", sess.ID, "err", err)
			}
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			if s.logger != nil {
				s.logger.Warn("session rename failed", "id", sess.ID, "err", err)
			}
		}
	}()
}

func (s *Store) load() {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return
	}
	loaded := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(s.dataDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var sess Session
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}
		if sess.ID != "" {
			s.sessions[sess.ContextHash] = &sess
			loaded++
		}
	}
	if s.logger != nil && loaded > 0 {
		s.logger.Info("loaded sessions from disk", "count", loaded)
	}
}
